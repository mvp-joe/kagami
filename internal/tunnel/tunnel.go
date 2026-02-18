// Package tunnel manages the outbound WebSocket connection to the
// Cloudflare Durable Object. Handles dial, reconnection with
// exponential backoff, keepalive pings, and dispatching incoming
// request frames to the proxy layer.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/protocol"
	"github.com/jward/kagami/internal/proxy"
)

// streamBuffer holds a pipe writer for streaming chunked request body data
// to a goroutine that is proxying the request to a local service.
type streamBuffer struct {
	header *protocol.HttpRequestHeader
	writer *io.PipeWriter
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

	// Stream buffers for chunked request body streaming, guarded by streamMu.
	streamMu      sync.Mutex
	streamBuffers map[string]*streamBuffer
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
		streamBuffers:        make(map[string]*streamBuffer),
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
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			c.mu.Lock()
			c.conn = nil
			c.mu.Unlock()
			return true, fmt.Errorf("read: %w", err)
		}

		// Text messages are pong replies from the DO's auto-response.
		if msgType == websocket.MessageText {
			if string(data) == "pong" {
				c.logger.Debug("pong received")
			}
			continue
		}

		c.handleRawMessage(ctx, conn, data)
	}
}

// pingLoop sends periodic text "ping" messages to the DO.
// The DO uses setWebSocketAutoResponse to reply with "pong" without waking.
func (c *Client) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := conn.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
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
		// First chunk of a chunked request — create pipe and start processing.
		pr, pw := io.Pipe()

		c.streamMu.Lock()
		c.streamBuffers[reqHeader.ID] = &streamBuffer{
			header: &reqHeader,
			writer: pw,
		}
		c.streamMu.Unlock()

		// Start processing in a goroutine first — it reads from the pipe reader.
		// Must start before writing to the pipe, because pw.Write blocks until
		// the reader consumes the data (io.Pipe is unbuffered).
		firstChunk := frame.Body
		c.inflight.Go(func() {
			defer pr.Close()
			c.processRequest(ctx, conn, &reqHeader, pr)
		})

		// Write the first chunk into the pipe. Blocks until processRequest's
		// Forward call starts reading the body.
		if _, err := pw.Write(firstChunk); err != nil {
			c.logger.Error("writing first chunk to pipe", "error", err)
			pw.Close()
			return
		}

		c.logger.Debug("streaming chunked request", "id", reqHeader.ID)
		return
	}

	// Non-chunked: process immediately in a goroutine.
	body := frame.Body
	c.inflight.Go(func() {
		c.processRequest(ctx, conn, &reqHeader, bytes.NewReader(body))
	})
}

// handleBodyChunk writes a continuation chunk to the streaming pipe and
// closes the pipe writer on the final chunk (signaling EOF to the reader).
func (c *Client) handleBodyChunk(_ context.Context, _ *websocket.Conn, frame protocol.Frame) {
	var chunkHeader protocol.HttpBodyChunkHeader
	if err := json.Unmarshal(frame.Header, &chunkHeader); err != nil {
		c.logger.Error("parsing http_body_chunk header", "error", err)
		return
	}

	c.streamMu.Lock()
	sb, ok := c.streamBuffers[chunkHeader.ID]
	if !ok {
		c.streamMu.Unlock()
		c.logger.Warn("chunk for unknown request", "id", chunkHeader.ID)
		return
	}

	if chunkHeader.Final {
		delete(c.streamBuffers, chunkHeader.ID)
		c.streamMu.Unlock()
	} else {
		c.streamMu.Unlock()
	}

	// Write chunk data to the pipe.
	if len(frame.Body) > 0 {
		if _, err := sb.writer.Write(frame.Body); err != nil {
			c.logger.Error("writing chunk to pipe", "error", err, "id", chunkHeader.ID)
		}
	}

	if chunkHeader.Final {
		// Close the writer to signal EOF to the reader.
		sb.writer.Close()
	}
}

// processRequest routes and proxies a complete request, then streams the response.
func (c *Client) processRequest(ctx context.Context, conn *websocket.Conn, reqHeader *protocol.HttpRequestHeader, body io.Reader) {
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
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	logger.Info("request completed", "status", resp.Status)

	if err := c.sendResponse(ctx, conn, reqHeader.ID, resp); err != nil {
		logger.Error("sending response", "error", err)
	}
}

// sendResponse streams an HTTP response body over the WebSocket in chunked frames.
// Reads the body in ChunkSize-sized chunks and sends each as it's read,
// holding at most one chunk (~512KB) in memory at a time.
func (c *Client) sendResponse(ctx context.Context, conn *websocket.Conn, requestID string, resp *proxy.Response) error {
	respHeader := &protocol.HttpResponseHeader{
		MessageHeader: protocol.MessageHeader{
			Type: "http_response",
			ID:   requestID,
		},
		Status:  resp.Status,
		Headers: resp.Headers,
	}

	// No body (error responses like 502/504 have nil Body).
	if resp.Body == nil {
		frame, err := protocol.EncodeFrame(respHeader, nil)
		if err != nil {
			return fmt.Errorf("encoding response frame: %w", err)
		}
		return conn.Write(ctx, websocket.MessageBinary, frame)
	}

	// Read the first chunk to determine if the body fits in a single frame.
	buf := make([]byte, protocol.ChunkSize)
	n, err := io.ReadFull(resp.Body, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return fmt.Errorf("reading response body: %w", err)
	}
	firstChunk := buf[:n]

	if err == io.ErrUnexpectedEOF || err == io.EOF {
		// Entire body fits in a single frame.
		frame, encErr := protocol.EncodeFrame(respHeader, firstChunk)
		if encErr != nil {
			return fmt.Errorf("encoding response frame: %w", encErr)
		}
		return conn.Write(ctx, websocket.MessageBinary, frame)
	}

	// Body is larger than one chunk — need to check if there's more data.
	// Read one more byte to confirm.
	extra := make([]byte, 1)
	n2, err2 := resp.Body.Read(extra)
	if n2 == 0 && (err2 == io.EOF) {
		// Body was exactly ChunkSize bytes.
		frame, encErr := protocol.EncodeFrame(respHeader, firstChunk)
		if encErr != nil {
			return fmt.Errorf("encoding response frame: %w", encErr)
		}
		return conn.Write(ctx, websocket.MessageBinary, frame)
	}

	// Multi-chunk response: send initial frame with chunked=true.
	respHeader.Chunked = true
	frame, encErr := protocol.EncodeFrame(respHeader, firstChunk)
	if encErr != nil {
		return fmt.Errorf("encoding chunked response header: %w", encErr)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
		return fmt.Errorf("writing chunked response header: %w", err)
	}

	// Prepend the extra byte to the next read cycle.
	var leftover []byte
	if n2 > 0 {
		leftover = extra[:n2]
	}

	// Stream continuation chunks.
	for {
		var chunkData []byte

		if leftover != nil {
			// Start with leftover byte(s) from the probe read.
			copy(buf, leftover)
			n, err = io.ReadFull(resp.Body, buf[len(leftover):])
			n += len(leftover)
			leftover = nil
		} else {
			n, err = io.ReadFull(resp.Body, buf)
		}
		chunkData = buf[:n]

		isFinal := err == io.ErrUnexpectedEOF || err == io.EOF
		if err != nil && !isFinal {
			return fmt.Errorf("reading response body chunk: %w", err)
		}

		chunkHeader := &protocol.HttpBodyChunkHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_body_chunk",
				ID:   requestID,
			},
			Final: isFinal,
		}

		frame, encErr := protocol.EncodeFrame(chunkHeader, chunkData)
		if encErr != nil {
			return fmt.Errorf("encoding body chunk: %w", encErr)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
			return fmt.Errorf("writing body chunk: %w", err)
		}

		if isFinal {
			break
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
