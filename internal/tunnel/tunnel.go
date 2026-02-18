// Package tunnel manages the outbound WebSocket connection to the
// Cloudflare Durable Object. Handles dial, reconnection with
// exponential backoff, keepalive pings, and dispatching incoming
// request frames to the proxy layer.
package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/protocol"
	"github.com/jward/kagami/internal/proxy"
)

// chunkBuffer accumulates body chunks for a single chunked request.
type chunkBuffer struct {
	header *protocol.HttpRequestHeader
	chunks [][]byte
}

// Client manages a WebSocket tunnel connection to the Cloudflare DO.
type Client struct {
	config *config.Config
	router *proxy.Router
	proxy  *proxy.Proxy
	logger *slog.Logger

	// Parsed durations from config (resolved at construction time).
	pingInterval         time.Duration
	reconnectInterval    time.Duration
	maxReconnectInterval time.Duration

	// Connection state, guarded by mu.
	mu   sync.Mutex
	conn *websocket.Conn

	// In-flight request tracking for graceful shutdown.
	inflight sync.WaitGroup

	// Chunk buffers for chunked request reassembly, guarded by chunkMu.
	chunkMu      sync.Mutex
	chunkBuffers map[string]*chunkBuffer
}

// NewClient creates a tunnel Client with the given dependencies.
// Returns an error if config duration fields fail to parse (should not happen
// if config.Load validated them, but we avoid panicking).
func NewClient(cfg *config.Config, router *proxy.Router, p *proxy.Proxy, logger *slog.Logger) (*Client, error) {
	pingInterval, err := time.ParseDuration(cfg.Agent.PingInterval)
	if err != nil {
		return nil, fmt.Errorf("parsing ping_interval: %w", err)
	}
	reconnectInterval, err := time.ParseDuration(cfg.Agent.ReconnectInterval)
	if err != nil {
		return nil, fmt.Errorf("parsing reconnect_interval: %w", err)
	}
	maxReconnectInterval, err := time.ParseDuration(cfg.Agent.MaxReconnectInterval)
	if err != nil {
		return nil, fmt.Errorf("parsing max_reconnect_interval: %w", err)
	}

	return &Client{
		config:               cfg,
		router:               router,
		proxy:                p,
		logger:               logger,
		pingInterval:         pingInterval,
		reconnectInterval:    reconnectInterval,
		maxReconnectInterval: maxReconnectInterval,
		chunkBuffers:         make(map[string]*chunkBuffer),
	}, nil
}

// Run connects to the DO and processes messages until ctx is cancelled.
// On disconnect, it reconnects with exponential backoff.
// Returns nil when ctx is cancelled (graceful shutdown).
func (c *Client) Run(ctx context.Context) error {
	backoff := c.reconnectInterval
	for {
		connected, err := c.connectAndServe(ctx)
		if ctx.Err() != nil {
			// Context cancelled — graceful shutdown.
			return nil
		}
		c.logger.Error("connection lost", "error", err)

		// If we had a successful connection, reset backoff to base interval
		// so we don't penalise the next reconnect after a long-lived session.
		if connected {
			backoff = c.reconnectInterval
		}

		c.logger.Info("reconnecting", "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		backoff = nextBackoff(backoff, c.maxReconnectInterval)
	}
}

// Shutdown waits for in-flight requests to complete, up to the given timeout.
// Call this after cancelling the context passed to Run.
func (c *Client) Shutdown(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		c.inflight.Wait()
		close(done)
	}()

	select {
	case <-done:
		c.logger.Info("all in-flight requests completed")
	case <-time.After(timeout):
		c.logger.Warn("shutdown timeout exceeded, some requests may be incomplete")
	}

	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		conn.Close(websocket.StatusNormalClosure, "shutdown")
	}
}

// connectAndServe dials the DO, starts the keepalive loop, and reads messages
// until the connection drops or ctx is cancelled. Returns (true, err) if a
// connection was successfully established before the error, or (false, err) if
// the dial itself failed. The caller uses this to decide whether to reset backoff.
func (c *Client) connectAndServe(ctx context.Context) (connected bool, err error) {
	connectURL := fmt.Sprintf("wss://%s/_kagami/connect", c.config.Agent.Server)

	c.logger.Info("connecting", "url", connectURL, "tunnel_id", c.config.Agent.TunnelID)

	conn, _, err := websocket.Dial(ctx, connectURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"X-Kagami-Tunnel-ID": {c.config.Agent.TunnelID},
			"X-Kagami-Secret":    {c.config.Agent.Secret},
		},
	})
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}

	// Increase the read limit to accommodate large frames.
	// Default is 32KB which is too small for our protocol.
	conn.SetReadLimit(16 * 1024 * 1024) // 16 MB

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.logger.Info("connected", "tunnel_id", c.config.Agent.TunnelID)

	// Start keepalive ping loop.
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go c.pingLoop(pingCtx, conn)

	// Read messages until disconnect or context cancellation.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			return true, fmt.Errorf("read: %w", err)
		}
		c.handleRawMessage(ctx, conn, data)
	}
}

// pingLoop sends periodic ping frames to the DO.
func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			header := &protocol.PingHeader{
				MessageHeader: protocol.MessageHeader{
					Type: "ping",
					ID:   "",
				},
			}
			frame, err := protocol.EncodeFrame(header, nil)
			if err != nil {
				c.logger.Error("encoding ping", "error", err)
				continue
			}
			if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
				c.logger.Debug("ping write failed (connection may be closing)", "error", err)
				return
			}
			c.logger.Debug("ping sent")
		}
	}
}

// handleRawMessage decodes a binary frame and dispatches by type.
func (c *Client) handleRawMessage(ctx context.Context, conn *websocket.Conn, data []byte) {
	frame, err := protocol.DecodeFrame(data)
	if err != nil {
		c.logger.Error("decoding frame", "error", err)
		return
	}

	msgType, err := protocol.ParseHeaderType(frame.Header)
	if err != nil {
		c.logger.Error("parsing header type", "error", err)
		return
	}

	switch msgType {
	case "http_request":
		c.handleHttpRequest(ctx, conn, frame)
	case "http_body_chunk":
		c.handleBodyChunk(ctx, conn, frame)
	case "pong":
		c.logger.Debug("pong received")
	case "error":
		var errHeader protocol.ErrorHeader
		if err := json.Unmarshal(frame.Header, &errHeader); err != nil {
			c.logger.Error("parsing error header", "error", err)
			return
		}
		c.logger.Warn("error from server", "code", errHeader.Code, "message", errHeader.Message)
	default:
		c.logger.Warn("unhandled message type", "type", msgType)
	}
}

// handleHttpRequest processes a complete (non-chunked) or initial (chunked) HTTP request.
func (c *Client) handleHttpRequest(ctx context.Context, conn *websocket.Conn, frame protocol.Frame) {
	var reqHeader protocol.HttpRequestHeader
	if err := json.Unmarshal(frame.Header, &reqHeader); err != nil {
		c.logger.Error("parsing http_request header", "error", err)
		return
	}

	if reqHeader.Chunked {
		// First chunk of a chunked request — buffer and wait for continuation.
		c.chunkMu.Lock()
		c.chunkBuffers[reqHeader.ID] = &chunkBuffer{
			header: &reqHeader,
			chunks: [][]byte{frame.Body},
		}
		c.chunkMu.Unlock()
		c.logger.Debug("buffering chunked request", "id", reqHeader.ID)
		return
	}

	// Non-chunked: process immediately in a goroutine.
	c.inflight.Go(func() {
		c.processRequest(ctx, conn, &reqHeader, frame.Body)
	})
}

// handleBodyChunk buffers a continuation chunk and processes the complete
// request when the final chunk arrives.
func (c *Client) handleBodyChunk(ctx context.Context, conn *websocket.Conn, frame protocol.Frame) {
	var chunkHeader protocol.HttpBodyChunkHeader
	if err := json.Unmarshal(frame.Header, &chunkHeader); err != nil {
		c.logger.Error("parsing http_body_chunk header", "error", err)
		return
	}

	c.chunkMu.Lock()
	buf, ok := c.chunkBuffers[chunkHeader.ID]
	if !ok {
		c.chunkMu.Unlock()
		c.logger.Warn("chunk for unknown request", "id", chunkHeader.ID)
		return
	}

	buf.chunks = append(buf.chunks, frame.Body)

	if !chunkHeader.Final {
		c.chunkMu.Unlock()
		return
	}

	// Final chunk — remove buffer and process.
	delete(c.chunkBuffers, chunkHeader.ID)
	c.chunkMu.Unlock()

	body := protocol.ReassembleChunks(buf.chunks)

	c.inflight.Go(func() {
		c.processRequest(ctx, conn, buf.header, body)
	})
}

// processRequest routes and proxies a complete request, then sends the response.
func (c *Client) processRequest(ctx context.Context, conn *websocket.Conn, reqHeader *protocol.HttpRequestHeader, body []byte) {
	logger := c.logger.With("id", reqHeader.ID, "method", reqHeader.Method, "host", reqHeader.Host, "path", reqHeader.Path)
	logger.Info("processing request")

	tunnelCfg := c.router.Match(reqHeader.Host, reqHeader.Path)
	if tunnelCfg == nil {
		logger.Warn("no matching tunnel")
		c.sendErrorResponse(ctx, conn, reqHeader.ID, http.StatusNotFound, "no matching tunnel for host/path")
		return
	}

	resp, err := c.proxy.Forward(ctx, tunnelCfg, reqHeader, body)
	if err != nil {
		logger.Error("proxy forward failed", "error", err)
		c.sendErrorResponse(ctx, conn, reqHeader.ID, http.StatusBadGateway, "proxy error")
		return
	}

	logger.Info("request completed", "status", resp.Status)

	if err := c.sendResponse(ctx, conn, reqHeader.ID, resp); err != nil {
		logger.Error("sending response", "error", err)
	}
}

// sendResponse frames and sends an HTTP response back over the WebSocket.
func (c *Client) sendResponse(ctx context.Context, conn *websocket.Conn, requestID string, resp *proxy.Response) error {
	respHeader := &protocol.HttpResponseHeader{
		MessageHeader: protocol.MessageHeader{
			Type: "http_response",
			ID:   requestID,
		},
		Status:  resp.Status,
		Headers: resp.Headers,
	}

	if len(resp.Body) <= protocol.ChunkSize {
		// Single frame.
		frame, err := protocol.EncodeFrame(respHeader, resp.Body)
		if err != nil {
			return fmt.Errorf("encoding response frame: %w", err)
		}
		return conn.Write(ctx, websocket.MessageBinary, frame)
	}

	// Chunked response.
	frames, err := protocol.ChunkBody(respHeader, resp.Body, protocol.ChunkSize)
	if err != nil {
		return fmt.Errorf("chunking response: %w", err)
	}

	for _, frame := range frames {
		if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
			return fmt.Errorf("writing response chunk: %w", err)
		}
	}
	return nil
}

// sendErrorResponse sends a synthetic HTTP error response for routing/proxy failures.
func (c *Client) sendErrorResponse(ctx context.Context, conn *websocket.Conn, requestID string, status int, message string) {
	body, _ := json.Marshal(struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}{
		Error:   fmt.Sprintf("%d", status),
		Message: message,
	})
	respHeader := &protocol.HttpResponseHeader{
		MessageHeader: protocol.MessageHeader{
			Type: "http_response",
			ID:   requestID,
		},
		Status: status,
		Headers: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}

	frame, err := protocol.EncodeFrame(respHeader, body)
	if err != nil {
		c.logger.Error("encoding error response", "error", err)
		return
	}

	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		c.logger.Error("writing error response", "error", err)
	}
}

// nextBackoff doubles the current backoff, capping at maxInterval.
func nextBackoff(current, maxInterval time.Duration) time.Duration {
	next := current * 2
	if next > maxInterval {
		return maxInterval
	}
	return next
}
