package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/protocol"
)

// testTunnels returns a standard set of tunnel configs for routing tests.
func testTunnels() []config.TunnelConfig {
	return []config.TunnelConfig{
		{
			Name:      "api",
			LocalAddr: "localhost:8080",
			Hostname:  "api.my-homelab.kagami.myworkers.dev",
			Protocol:  "http",
		},
		{
			Name:      "admin",
			LocalAddr: "localhost:3000",
			Hostname:  "admin.my-homelab.kagami.myworkers.dev",
			Protocol:  "http",
		},
		{
			Name:       "docs",
			LocalAddr:  "localhost:4000",
			PathPrefix: "/docs",
			Protocol:   "http",
		},
		{
			Name:       "api-v2",
			LocalAddr:  "localhost:9090",
			PathPrefix: "/api/v2",
			Protocol:   "http",
		},
	}
}

func TestRouter_Match_HostnameMatch(t *testing.T) {
	t.Parallel()
	r := NewRouter(testTunnels())

	tests := []struct {
		name      string
		host      string
		path      string
		wantName  string
		wantAddr  string
		wantMatch bool
	}{
		{
			name:      "matches api hostname",
			host:      "api.my-homelab.kagami.myworkers.dev",
			path:      "/users",
			wantName:  "api",
			wantAddr:  "localhost:8080",
			wantMatch: true,
		},
		{
			name:      "matches admin hostname",
			host:      "admin.my-homelab.kagami.myworkers.dev",
			path:      "/dashboard",
			wantName:  "admin",
			wantAddr:  "localhost:3000",
			wantMatch: true,
		},
		{
			name:      "unrecognized hostname returns nil",
			host:      "unknown.my-homelab.kagami.myworkers.dev",
			path:      "/",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := r.Match(tt.host, tt.path)
			if tt.wantMatch {
				if got == nil {
					t.Fatal("expected a match, got nil")
				}
				if got.Name != tt.wantName {
					t.Errorf("name = %q, want %q", got.Name, tt.wantName)
				}
				if got.LocalAddr != tt.wantAddr {
					t.Errorf("local_addr = %q, want %q", got.LocalAddr, tt.wantAddr)
				}
			} else {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
			}
		})
	}
}

func TestRouter_Match_PathPrefixMatch(t *testing.T) {
	t.Parallel()
	r := NewRouter(testTunnels())

	tests := []struct {
		name      string
		host      string
		path      string
		wantName  string
		wantAddr  string
		wantMatch bool
	}{
		{
			name:      "matches /docs prefix",
			host:      "something-else.example.com",
			path:      "/docs/getting-started",
			wantName:  "docs",
			wantAddr:  "localhost:4000",
			wantMatch: true,
		},
		{
			name:      "matches /api/v2 prefix",
			host:      "something-else.example.com",
			path:      "/api/v2/users",
			wantName:  "api-v2",
			wantAddr:  "localhost:9090",
			wantMatch: true,
		},
		{
			name:      "longer prefix wins over shorter",
			host:      "something-else.example.com",
			path:      "/api/v2/items",
			wantName:  "api-v2",
			wantAddr:  "localhost:9090",
			wantMatch: true,
		},
		{
			name:      "no matching prefix returns nil",
			host:      "something-else.example.com",
			path:      "/unknown/path",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := r.Match(tt.host, tt.path)
			if tt.wantMatch {
				if got == nil {
					t.Fatal("expected a match, got nil")
				}
				if got.Name != tt.wantName {
					t.Errorf("name = %q, want %q", got.Name, tt.wantName)
				}
				if got.LocalAddr != tt.wantAddr {
					t.Errorf("local_addr = %q, want %q", got.LocalAddr, tt.wantAddr)
				}
			} else {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
			}
		})
	}
}

func TestRouter_Match_HostnameTakesPriority(t *testing.T) {
	t.Parallel()

	// A tunnel with both hostname and a path prefix that could also match.
	tunnels := []config.TunnelConfig{
		{
			Name:       "path-service",
			LocalAddr:  "localhost:5000",
			PathPrefix: "/api",
			Protocol:   "http",
		},
		{
			Name:      "host-service",
			LocalAddr: "localhost:6000",
			Hostname:  "api.example.com",
			Protocol:  "http",
		},
	}

	r := NewRouter(tunnels)
	got := r.Match("api.example.com", "/api/users")
	if got == nil {
		t.Fatal("expected a match, got nil")
	}
	if got.Name != "host-service" {
		t.Errorf("expected hostname match to win, got %q", got.Name)
	}
}

// localService starts an httptest.Server that records the request and sends
// the configured response. It returns the server and a channel that receives
// the recorded request details.
type recordedRequest struct {
	Method  string
	Path    string
	Query   string
	Host    string
	Headers http.Header
	Body    []byte
}

func startLocalService(t *testing.T, status int, respHeaders map[string]string, respBody []byte) (*httptest.Server, <-chan recordedRequest) {
	t.Helper()
	ch := make(chan recordedRequest, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ch <- recordedRequest{
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Host:    r.Host,
			Headers: r.Header,
			Body:    body,
		}
		for k, v := range respHeaders {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		w.Write(respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// addrFromServer extracts host:port from an httptest.Server URL.
func addrFromServer(srv *httptest.Server) string {
	return strings.TrimPrefix(srv.URL, "http://")
}

// readBody reads the entire resp.Body and returns the bytes.
// Handles nil Body (error responses).
func readBody(t *testing.T, resp *Response) []byte {
	t.Helper()
	if resp.Body == nil {
		return nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}
	return data
}

func TestProxy_Forward_RoutesToCorrectService(t *testing.T) {
	t.Parallel()

	srv, reqCh := startLocalService(t, http.StatusOK, nil, []byte(`{"ok":true}`))
	cfg := &config.TunnelConfig{
		Name:      "test",
		LocalAddr: addrFromServer(srv),
		Hostname:  "api.example.com",
		Protocol:  "http",
	}

	p := NewProxy(5 * time.Second)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "GET",
		Host:   "api.example.com",
		Path:   "/users",
		Query:  "page=1",
	}, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	body := readBody(t, resp)

	if resp.Status != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusOK)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q, want %q", body, `{"ok":true}`)
	}

	rec := <-reqCh
	if rec.Path != "/users" {
		t.Errorf("proxied path = %q, want /users", rec.Path)
	}
	if rec.Query != "page=1" {
		t.Errorf("proxied query = %q, want page=1", rec.Query)
	}
}

func TestProxy_Forward_Returns502WhenUnreachable(t *testing.T) {
	t.Parallel()

	cfg := &config.TunnelConfig{
		Name:      "dead",
		LocalAddr: "127.0.0.1:1", // port 1 should be unreachable
		Protocol:  "http",
	}

	p := NewProxy(2 * time.Second)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "GET",
		Host:   "dead.example.com",
		Path:   "/",
	}, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	if resp.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusBadGateway)
	}
	if resp.Body != nil {
		resp.Body.Close()
		t.Error("expected nil Body for error response")
	}
}

func TestProxy_Forward_PreservesRequestHeaders(t *testing.T) {
	t.Parallel()

	srv, reqCh := startLocalService(t, http.StatusOK, nil, nil)
	cfg := &config.TunnelConfig{
		Name:      "test",
		LocalAddr: addrFromServer(srv),
		Protocol:  "http",
	}

	p := NewProxy(5 * time.Second)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "POST",
		Host:   "api.example.com",
		Path:   "/submit",
		Headers: map[string][]string{
			"Content-Type":    {"application/json"},
			"X-Custom-Header": {"custom-value"},
			"Accept":          {"text/html", "application/json"},
		},
	}, bytes.NewReader([]byte(`{"data":"test"}`)))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}

	rec := <-reqCh

	if got := rec.Headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Headers.Get("X-Custom-Header"); got != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want custom-value", got)
	}
	// Multi-value headers.
	accepts := rec.Headers.Values("Accept")
	if len(accepts) < 2 {
		t.Errorf("expected at least 2 Accept values, got %d: %v", len(accepts), accepts)
	}
	if string(rec.Body) != `{"data":"test"}` {
		t.Errorf("body = %q, want %q", rec.Body, `{"data":"test"}`)
	}
}

func TestProxy_Forward_PreservesResponseHeaders(t *testing.T) {
	t.Parallel()

	respHeaders := map[string]string{
		"X-Response-Id":  "resp-123",
		"X-Request-Time": "42ms",
		"Content-Type":   "application/json",
	}
	srv, _ := startLocalService(t, http.StatusCreated, respHeaders, []byte(`{"id":1}`))
	cfg := &config.TunnelConfig{
		Name:      "test",
		LocalAddr: addrFromServer(srv),
		Protocol:  "http",
	}

	p := NewProxy(5 * time.Second)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "POST",
		Host:   "api.example.com",
		Path:   "/create",
	}, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}

	if resp.Status != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusCreated)
	}
	for k, want := range respHeaders {
		got := resp.Headers[k]
		if len(got) == 0 || got[0] != want {
			t.Errorf("response header %q = %v, want [%q]", k, got, want)
		}
	}
}

func TestProxy_Forward_HandlesBinaryResponseBody(t *testing.T) {
	t.Parallel()

	// Binary data with null bytes and non-UTF8 sequences.
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD, 0x89, 0x50, 0x4E, 0x47}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(binaryData)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.TunnelConfig{
		Name:      "binary",
		LocalAddr: addrFromServer(srv),
		Protocol:  "http",
	}

	p := NewProxy(5 * time.Second)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "GET",
		Host:   "binary.example.com",
		Path:   "/image.png",
	}, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}

	body := readBody(t, resp)

	if resp.Status != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.Status, http.StatusOK)
	}
	if len(body) != len(binaryData) {
		t.Fatalf("body length = %d, want %d", len(body), len(binaryData))
	}
	for i, b := range body {
		if b != binaryData[i] {
			t.Errorf("body[%d] = 0x%02X, want 0x%02X", i, b, binaryData[i])
		}
	}
}

func TestProxy_Forward_TimesOutSlowService(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a service that never responds within the timeout.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(10 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := &config.TunnelConfig{
		Name:      "slow",
		LocalAddr: addrFromServer(srv),
		Protocol:  "http",
	}

	// Use a very short timeout for the test.
	p := NewProxy(100 * time.Millisecond)
	resp, err := p.Forward(context.Background(), cfg, &protocol.HttpRequestHeader{
		Method: "GET",
		Host:   "slow.example.com",
		Path:   "/slow",
	}, nil)
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	if resp.Body != nil {
		resp.Body.Close()
	}

	if resp.Status != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d (504 on timeout)", resp.Status, http.StatusGatewayTimeout)
	}
}
