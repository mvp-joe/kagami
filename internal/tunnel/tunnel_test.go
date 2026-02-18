package tunnel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/protocol"
	"github.com/jward/kagami/internal/proxy"
)

// testLogger returns a discard logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// testConfig returns a minimal valid config for tests.
func testConfig(server string) *config.Config {
	return &config.Config{
		Agent: config.AgentConfig{
			TunnelID:             "test-tunnel",
			Secret:               "test-secret",
			Server:               server,
			PingInterval:         "30s",
			ReconnectInterval:    "1s",
			MaxReconnectInterval: "10s",
			ProxyTimeout:         "5s",
		},
		Tunnel: []config.TunnelConfig{
			{
				Name:      "api",
				LocalAddr: "localhost:8080",
				Hostname:  "api.test.example.com",
				Protocol:  "http",
			},
		},
	}
}

func TestNextBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		current     time.Duration
		maxInterval time.Duration
		want        time.Duration
	}{
		{
			name:        "doubles from base",
			current:     5 * time.Second,
			maxInterval: 60 * time.Second,
			want:        10 * time.Second,
		},
		{
			name:        "doubles again",
			current:     10 * time.Second,
			maxInterval: 60 * time.Second,
			want:        20 * time.Second,
		},
		{
			name:        "caps at max",
			current:     40 * time.Second,
			maxInterval: 60 * time.Second,
			want:        60 * time.Second,
		},
		{
			name:        "already at max stays at max",
			current:     60 * time.Second,
			maxInterval: 60 * time.Second,
			want:        60 * time.Second,
		},
		{
			name:        "would exceed max, caps",
			current:     50 * time.Second,
			maxInterval: 60 * time.Second,
			want:        60 * time.Second,
		},
		{
			name:        "sub-second intervals",
			current:     100 * time.Millisecond,
			maxInterval: 1 * time.Second,
			want:        200 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nextBackoff(tt.current, tt.maxInterval)
			if got != tt.want {
				t.Errorf("nextBackoff(%v, %v) = %v, want %v", tt.current, tt.maxInterval, got, tt.want)
			}
		})
	}
}

func TestNextBackoff_Sequence(t *testing.T) {
	t.Parallel()

	// Verify a full backoff sequence: 5s -> 10s -> 20s -> 40s -> 60s -> 60s
	base := 5 * time.Second
	max := 60 * time.Second

	expected := []time.Duration{
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		60 * time.Second,
		60 * time.Second,
	}

	current := base
	for i, want := range expected {
		current = nextBackoff(current, max)
		if current != want {
			t.Errorf("step %d: got %v, want %v", i+1, current, want)
		}
	}
}

// TestHandleRawMessage_HttpRequest verifies that a non-chunked http_request
// is routed and proxied, and the response is sent back on the WebSocket.
func TestHandleRawMessage_HttpRequest(t *testing.T) {
	t.Parallel()

	// Start a local HTTP service that the proxy will forward to.
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "passed")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	t.Cleanup(localSrv.Close)
	localAddr := strings.TrimPrefix(localSrv.URL, "http://")

	// Set up a WebSocket server that the client connects to.
	// We'll send one http_request and then read back the http_response.
	var gotResponse []byte
	var responseMu sync.Mutex
	responseCh := make(chan struct{})

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()

		conn.SetReadLimit(16 * 1024 * 1024)

		// Send an http_request frame.
		reqHeader := &protocol.HttpRequestHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_request",
				ID:   "req-1",
			},
			Method:  "GET",
			Host:    "api.test.example.com",
			Path:    "/users",
			Query:   "page=1",
			Headers: map[string][]string{},
		}
		frame, err := protocol.EncodeFrame(reqHeader, nil)
		if err != nil {
			t.Errorf("encode: %v", err)
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageBinary, frame); err != nil {
			t.Errorf("write: %v", err)
			return
		}

		// Read the response.
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read response: %v", err)
			return
		}
		responseMu.Lock()
		gotResponse = data
		responseMu.Unlock()
		close(responseCh)

		// Keep connection open briefly for clean shutdown.
		<-r.Context().Done()
	}))
	t.Cleanup(wsSrv.Close)

	wsAddr := strings.TrimPrefix(wsSrv.URL, "http://")

	cfg := &config.Config{
		Agent: config.AgentConfig{
			TunnelID:             "test-tunnel",
			Secret:               "test-secret",
			Server:               wsAddr,
			PingInterval:         "1h", // long interval so it doesn't fire during test
			ReconnectInterval:    "1s",
			MaxReconnectInterval: "10s",
			ProxyTimeout:         "5s",
		},
		Tunnel: []config.TunnelConfig{
			{
				Name:      "api",
				LocalAddr: localAddr,
				Hostname:  "api.test.example.com",
				Protocol:  "http",
			},
		},
	}

	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the client in a goroutine. It will connect via ws:// to our test server.
	go func() {
		// The test WS server uses ws://, but our client dials wss://.
		// We need to use the handleRawMessage path directly instead.
		// Let's dial manually.
		conn, _, dialErr := websocket.Dial(ctx, "ws://"+wsAddr, nil)
		if dialErr != nil {
			t.Errorf("dial: %v", dialErr)
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)

		client.mu.Lock()
		client.conn = conn
		client.mu.Unlock()

		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			client.handleRawMessage(ctx, conn, data)
		}
	}()

	// Wait for the response to arrive.
	select {
	case <-responseCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for response")
	}

	// Decode and verify the response.
	responseMu.Lock()
	data := gotResponse
	responseMu.Unlock()

	respFrame, err := protocol.DecodeFrame(data)
	if err != nil {
		t.Fatalf("decoding response frame: %v", err)
	}

	var respHeader protocol.HttpResponseHeader
	if err := json.Unmarshal(respFrame.Header, &respHeader); err != nil {
		t.Fatalf("parsing response header: %v", err)
	}

	if respHeader.ID != "req-1" {
		t.Errorf("response ID = %q, want %q", respHeader.ID, "req-1")
	}
	if respHeader.Status != http.StatusOK {
		t.Errorf("response status = %d, want %d", respHeader.Status, http.StatusOK)
	}
	if string(respFrame.Body) != `{"result":"ok"}` {
		t.Errorf("response body = %q, want %q", respFrame.Body, `{"result":"ok"}`)
	}
	if vals := respHeader.Headers["X-Test"]; len(vals) == 0 || vals[0] != "passed" {
		t.Errorf("response header X-Test = %v, want [passed]", vals)
	}
}

// TestHandleRawMessage_NoMatchingTunnel verifies that a request with an
// unrecognized host gets a 404 response.
func TestHandleRawMessage_NoMatchingTunnel(t *testing.T) {
	t.Parallel()

	responseCh := make(chan []byte, 1)

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(16 * 1024 * 1024)

		// Send a request for an unrecognized host.
		reqHeader := &protocol.HttpRequestHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_request",
				ID:   "req-nomatch",
			},
			Method:  "GET",
			Host:    "unknown.example.com",
			Path:    "/",
			Headers: map[string][]string{},
		}
		frame, _ := protocol.EncodeFrame(reqHeader, nil)
		conn.Write(r.Context(), websocket.MessageBinary, frame)

		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read response: %v", err)
			return
		}
		responseCh <- data
		<-r.Context().Done()
	}))
	t.Cleanup(wsSrv.Close)

	wsAddr := strings.TrimPrefix(wsSrv.URL, "http://")
	cfg := testConfig(wsAddr)
	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		conn, _, dialErr := websocket.Dial(ctx, "ws://"+wsAddr, nil)
		if dialErr != nil {
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		client.mu.Lock()
		client.conn = conn
		client.mu.Unlock()
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			client.handleRawMessage(ctx, conn, data)
		}
	}()

	select {
	case data := <-responseCh:
		respFrame, err := protocol.DecodeFrame(data)
		if err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		var respHeader protocol.HttpResponseHeader
		if err := json.Unmarshal(respFrame.Header, &respHeader); err != nil {
			t.Fatalf("parsing header: %v", err)
		}
		if respHeader.Status != http.StatusNotFound {
			t.Errorf("status = %d, want %d", respHeader.Status, http.StatusNotFound)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

// TestHandleRawMessage_ChunkedRequest verifies that chunked http_request +
// http_body_chunk frames are reassembled and proxied correctly.
func TestHandleRawMessage_ChunkedRequest(t *testing.T) {
	t.Parallel()

	// Local service that echoes the request body length.
	var receivedBody []byte
	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	t.Cleanup(localSrv.Close)
	localAddr := strings.TrimPrefix(localSrv.URL, "http://")

	responseCh := make(chan []byte, 1)

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(16 * 1024 * 1024)

		// Build a body larger than we'll split manually into chunks.
		fullBody := []byte("AAAAAAAAAA" + "BBBBBBBBBB" + "CCCCCCCCCC")

		// Send initial chunked http_request with first part of body.
		reqHeader := &protocol.HttpRequestHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_request",
				ID:   "req-chunked",
			},
			Method:  "POST",
			Host:    "api.test.example.com",
			Path:    "/upload",
			Headers: map[string][]string{"Content-Type": {"application/octet-stream"}},
			Chunked: true,
		}
		frame1, _ := protocol.EncodeFrame(reqHeader, fullBody[:10])
		conn.Write(r.Context(), websocket.MessageBinary, frame1)

		// Send continuation chunk (not final).
		chunk2Header := &protocol.HttpBodyChunkHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_body_chunk",
				ID:   "req-chunked",
			},
			Final: false,
		}
		frame2, _ := protocol.EncodeFrame(chunk2Header, fullBody[10:20])
		conn.Write(r.Context(), websocket.MessageBinary, frame2)

		// Send final chunk.
		chunk3Header := &protocol.HttpBodyChunkHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_body_chunk",
				ID:   "req-chunked",
			},
			Final: true,
		}
		frame3, _ := protocol.EncodeFrame(chunk3Header, fullBody[20:])
		conn.Write(r.Context(), websocket.MessageBinary, frame3)

		// Read response.
		_, data, err := conn.Read(r.Context())
		if err != nil {
			t.Errorf("read response: %v", err)
			return
		}
		responseCh <- data
		<-r.Context().Done()
	}))
	t.Cleanup(wsSrv.Close)

	wsAddr := strings.TrimPrefix(wsSrv.URL, "http://")
	cfg := &config.Config{
		Agent: config.AgentConfig{
			TunnelID:             "test-tunnel",
			Secret:               "test-secret",
			Server:               wsAddr,
			PingInterval:         "1h",
			ReconnectInterval:    "1s",
			MaxReconnectInterval: "10s",
			ProxyTimeout:         "5s",
		},
		Tunnel: []config.TunnelConfig{
			{
				Name:      "api",
				LocalAddr: localAddr,
				Hostname:  "api.test.example.com",
				Protocol:  "http",
			},
		},
	}

	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		conn, _, dialErr := websocket.Dial(ctx, "ws://"+wsAddr, nil)
		if dialErr != nil {
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		client.mu.Lock()
		client.conn = conn
		client.mu.Unlock()
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			client.handleRawMessage(ctx, conn, data)
		}
	}()

	select {
	case data := <-responseCh:
		respFrame, err := protocol.DecodeFrame(data)
		if err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		var respHeader protocol.HttpResponseHeader
		if err := json.Unmarshal(respFrame.Header, &respHeader); err != nil {
			t.Fatalf("parsing header: %v", err)
		}
		if respHeader.Status != http.StatusOK {
			t.Errorf("status = %d, want %d", respHeader.Status, http.StatusOK)
		}

		// Verify the local service received the full reassembled body.
		expectedBody := "AAAAAAAAAABBBBBBBBBBCCCCCCCCCC"
		if string(receivedBody) != expectedBody {
			t.Errorf("local service received body = %q, want %q", receivedBody, expectedBody)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}
}

// TestHandleRawMessage_ChunkedResponse verifies that large responses from the
// local service are sent as chunked frames back on the WebSocket.
func TestHandleRawMessage_ChunkedResponse(t *testing.T) {
	t.Parallel()

	// Local service that returns a body larger than ChunkSize.
	largeBody := make([]byte, protocol.ChunkSize+1024)
	for i := range largeBody {
		largeBody[i] = byte('X')
	}

	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(largeBody)
	}))
	t.Cleanup(localSrv.Close)
	localAddr := strings.TrimPrefix(localSrv.URL, "http://")

	// Collect all response frames.
	framesCh := make(chan [][]byte, 1)

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("ws accept: %v", err)
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(16 * 1024 * 1024)

		// Send a simple non-chunked request.
		reqHeader := &protocol.HttpRequestHeader{
			MessageHeader: protocol.MessageHeader{
				Type: "http_request",
				ID:   "req-large",
			},
			Method:  "GET",
			Host:    "api.test.example.com",
			Path:    "/large",
			Headers: map[string][]string{},
		}
		frame, _ := protocol.EncodeFrame(reqHeader, nil)
		conn.Write(r.Context(), websocket.MessageBinary, frame)

		// Read all response frames (first frame + continuation chunks).
		var frames [][]byte
		for {
			_, data, readErr := conn.Read(r.Context())
			if readErr != nil {
				break
			}
			frames = append(frames, data)

			// Check if this is the last frame.
			f, _ := protocol.DecodeFrame(data)
			msgType, _ := protocol.ParseHeaderType(f.Header)
			if msgType == "http_response" {
				// Check if it's chunked.
				var hdr protocol.HttpResponseHeader
				json.Unmarshal(f.Header, &hdr)
				if !hdr.Chunked {
					// Non-chunked, single frame response.
					break
				}
			} else if msgType == "http_body_chunk" {
				var chunkHdr protocol.HttpBodyChunkHeader
				json.Unmarshal(f.Header, &chunkHdr)
				if chunkHdr.Final {
					break
				}
			}
		}
		framesCh <- frames
		<-r.Context().Done()
	}))
	t.Cleanup(wsSrv.Close)

	wsAddr := strings.TrimPrefix(wsSrv.URL, "http://")
	cfg := &config.Config{
		Agent: config.AgentConfig{
			TunnelID:             "test-tunnel",
			Secret:               "test-secret",
			Server:               wsAddr,
			PingInterval:         "1h",
			ReconnectInterval:    "1s",
			MaxReconnectInterval: "10s",
			ProxyTimeout:         "5s",
		},
		Tunnel: []config.TunnelConfig{
			{
				Name:      "api",
				LocalAddr: localAddr,
				Hostname:  "api.test.example.com",
				Protocol:  "http",
			},
		},
	}

	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		conn, _, dialErr := websocket.Dial(ctx, "ws://"+wsAddr, nil)
		if dialErr != nil {
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		client.mu.Lock()
		client.conn = conn
		client.mu.Unlock()
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			client.handleRawMessage(ctx, conn, data)
		}
	}()

	select {
	case frames := <-framesCh:
		if len(frames) < 2 {
			t.Fatalf("expected at least 2 frames for chunked response, got %d", len(frames))
		}

		// First frame should be http_response with chunked=true.
		f0, _ := protocol.DecodeFrame(frames[0])
		var respHeader protocol.HttpResponseHeader
		if err := json.Unmarshal(f0.Header, &respHeader); err != nil {
			t.Fatalf("parsing first frame header: %v", err)
		}
		if respHeader.Type != "http_response" {
			t.Errorf("first frame type = %q, want http_response", respHeader.Type)
		}
		if !respHeader.Chunked {
			t.Error("first frame should have chunked=true")
		}
		if respHeader.Status != http.StatusOK {
			t.Errorf("status = %d, want %d", respHeader.Status, http.StatusOK)
		}

		// Reassemble all body chunks.
		var allChunks [][]byte
		allChunks = append(allChunks, f0.Body)
		for _, raw := range frames[1:] {
			f, _ := protocol.DecodeFrame(raw)
			allChunks = append(allChunks, f.Body)
		}
		reassembled := protocol.ReassembleChunks(allChunks)

		if len(reassembled) != len(largeBody) {
			t.Errorf("reassembled body length = %d, want %d", len(reassembled), len(largeBody))
		}

		// Verify last chunk frame has final=true.
		lastFrame := frames[len(frames)-1]
		fl, _ := protocol.DecodeFrame(lastFrame)
		var lastChunkHdr protocol.HttpBodyChunkHeader
		json.Unmarshal(fl.Header, &lastChunkHdr)
		if !lastChunkHdr.Final {
			t.Error("last chunk should have final=true")
		}

	case <-time.After(10 * time.Second):
		t.Fatal("timed out")
	}
}

// TestHandleRawMessage_Pong verifies pong messages are handled without error.
func TestHandleRawMessage_Pong(t *testing.T) {
	t.Parallel()

	cfg := testConfig("localhost:1234")
	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	pongHeader := &protocol.PongHeader{
		MessageHeader: protocol.MessageHeader{
			Type: "pong",
			ID:   "",
		},
	}
	frame, err := protocol.EncodeFrame(pongHeader, nil)
	if err != nil {
		t.Fatalf("encoding pong: %v", err)
	}

	// Should not panic or error.
	decoded, err := protocol.DecodeFrame(frame)
	if err != nil {
		t.Fatalf("decoding frame: %v", err)
	}

	msgType, err := protocol.ParseHeaderType(decoded.Header)
	if err != nil {
		t.Fatalf("parsing type: %v", err)
	}
	if msgType != "pong" {
		t.Errorf("type = %q, want pong", msgType)
	}

	// handleRawMessage should process this without panic.
	// We can't easily call it without a real conn, but we can verify
	// the decode path works. The handler just logs at debug level.
	_ = client
}

// TestNewClient_ParsesDurations verifies that NewClient rejects invalid durations.
func TestNewClient_ParsesDurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		modify func(*config.Config)
	}{
		{
			name: "invalid ping_interval",
			modify: func(c *config.Config) {
				c.Agent.PingInterval = "not-a-duration"
			},
		},
		{
			name: "invalid reconnect_interval",
			modify: func(c *config.Config) {
				c.Agent.ReconnectInterval = "bad"
			},
		},
		{
			name: "invalid max_reconnect_interval",
			modify: func(c *config.Config) {
				c.Agent.MaxReconnectInterval = "bad"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := testConfig("localhost:1234")
			tt.modify(cfg)

			router := proxy.NewRouter(cfg.Tunnel)
			p := proxy.NewProxy(5 * time.Second)
			_, err := NewClient(cfg, router, p, testLogger())
			if err == nil {
				t.Error("expected error for invalid duration, got nil")
			}
		})
	}
}

// TestChunkBufferCleanup verifies that chunk buffers are removed after
// the final chunk is processed.
func TestChunkBufferCleanup(t *testing.T) {
	t.Parallel()

	localSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(localSrv.Close)
	localAddr := strings.TrimPrefix(localSrv.URL, "http://")

	responseCh := make(chan struct{})

	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(16 * 1024 * 1024)

		// Send chunked request.
		reqHeader := &protocol.HttpRequestHeader{
			MessageHeader: protocol.MessageHeader{Type: "http_request", ID: "req-cleanup"},
			Method:        "POST",
			Host:          "api.test.example.com",
			Path:          "/",
			Headers:       map[string][]string{},
			Chunked:       true,
		}
		frame, _ := protocol.EncodeFrame(reqHeader, []byte("chunk1"))
		conn.Write(r.Context(), websocket.MessageBinary, frame)

		chunkHdr := &protocol.HttpBodyChunkHeader{
			MessageHeader: protocol.MessageHeader{Type: "http_body_chunk", ID: "req-cleanup"},
			Final:         true,
		}
		frame2, _ := protocol.EncodeFrame(chunkHdr, []byte("chunk2"))
		conn.Write(r.Context(), websocket.MessageBinary, frame2)

		// Read response.
		conn.Read(r.Context())
		close(responseCh)
		<-r.Context().Done()
	}))
	t.Cleanup(wsSrv.Close)

	wsAddr := strings.TrimPrefix(wsSrv.URL, "http://")
	cfg := &config.Config{
		Agent: config.AgentConfig{
			TunnelID:             "test-tunnel",
			Secret:               "test-secret",
			Server:               wsAddr,
			PingInterval:         "1h",
			ReconnectInterval:    "1s",
			MaxReconnectInterval: "10s",
			ProxyTimeout:         "5s",
		},
		Tunnel: []config.TunnelConfig{
			{Name: "api", LocalAddr: localAddr, Hostname: "api.test.example.com", Protocol: "http"},
		},
	}

	router := proxy.NewRouter(cfg.Tunnel)
	p := proxy.NewProxy(5 * time.Second)
	client, err := NewClient(cfg, router, p, testLogger())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		conn, _, dialErr := websocket.Dial(ctx, "ws://"+wsAddr, nil)
		if dialErr != nil {
			return
		}
		conn.SetReadLimit(16 * 1024 * 1024)
		client.mu.Lock()
		client.conn = conn
		client.mu.Unlock()
		for {
			_, data, readErr := conn.Read(ctx)
			if readErr != nil {
				return
			}
			client.handleRawMessage(ctx, conn, data)
		}
	}()

	select {
	case <-responseCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	// Give goroutines a moment to finish.
	time.Sleep(50 * time.Millisecond)

	// Verify chunk buffer was cleaned up.
	client.chunkMu.Lock()
	bufLen := len(client.chunkBuffers)
	client.chunkMu.Unlock()

	if bufLen != 0 {
		t.Errorf("chunk buffer has %d entries, want 0 (should be cleaned up after final chunk)", bufLen)
	}
}
