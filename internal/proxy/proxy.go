// Package proxy implements the HTTP reverse proxy to local services.
// It receives wire protocol request data from the tunnel, routes to the
// correct local service based on hostname or path prefix, forwards via
// http.Client, and returns the response for framing back over the WebSocket.
package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jward/kagami/internal/config"
	"github.com/jward/kagami/internal/protocol"
)

// DefaultTimeout is the default proxy timeout for forwarding requests to local services.
// Should be less than the DO-side 30s timeout so the agent can respond with
// an error before the DO gives up.
const DefaultTimeout = 25 * time.Second

// Response holds the result of proxying a request to a local service.
type Response struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// Router matches incoming requests to the correct TunnelConfig
// based on hostname and path prefix.
type Router struct {
	tunnels []config.TunnelConfig
}

// NewRouter creates a Router from the given tunnel configs.
func NewRouter(tunnels []config.TunnelConfig) *Router {
	return &Router{tunnels: tunnels}
}

// Match returns the TunnelConfig that matches the given host and path,
// or nil if no match is found.
//
// Matching priority:
//  1. Hostname match (exact match against TunnelConfig.Hostname)
//  2. Path prefix match (longest matching prefix wins)
func (r *Router) Match(host, path string) *config.TunnelConfig {
	// First pass: hostname match.
	for i := range r.tunnels {
		if r.tunnels[i].Hostname != "" && r.tunnels[i].Hostname == host {
			return &r.tunnels[i]
		}
	}

	// Second pass: longest path prefix match.
	var best *config.TunnelConfig
	bestLen := 0
	for i := range r.tunnels {
		pfx := r.tunnels[i].PathPrefix
		if pfx == "" {
			continue
		}
		if strings.HasPrefix(path, pfx) && len(pfx) > bestLen {
			best = &r.tunnels[i]
			bestLen = len(pfx)
		}
	}
	return best
}

// Proxy forwards HTTP requests to local services.
type Proxy struct {
	client *http.Client
}

// NewProxy creates a Proxy with a shared http.Transport and the given timeout.
// If timeout is zero, DefaultTimeout is used.
func NewProxy(timeout time.Duration) *Proxy {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Proxy{
		client: &http.Client{
			Transport: &http.Transport{
				ResponseHeaderTimeout: timeout,
			},
			// Disable automatic redirects — we want to return the redirect
			// response as-is back through the tunnel.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Forward proxies a request described by the wire protocol header and body
// to the local service identified by the TunnelConfig. It returns the
// response from the local service, or a 502 response if the service is
// unreachable.
func (p *Proxy) Forward(ctx context.Context, cfg *config.TunnelConfig, reqHeader *protocol.HttpRequestHeader, body []byte) (*Response, error) {
	scheme := cfg.Protocol
	if scheme == "" {
		scheme = "http"
	}

	target := &url.URL{
		Scheme:   scheme,
		Host:     cfg.LocalAddr,
		Path:     reqHeader.Path,
		RawQuery: reqHeader.Query,
	}

	// Build the outbound request from wire protocol data.
	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	outReq, err := http.NewRequestWithContext(ctx, reqHeader.Method, target.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	// Copy headers from the wire protocol request.
	for k, vals := range reqHeader.Headers {
		for _, v := range vals {
			outReq.Header.Add(k, v)
		}
	}

	// Set Host header to the original host from the wire protocol.
	outReq.Host = reqHeader.Host

	resp, err := p.client.Do(outReq)
	if err != nil {
		// Distinguish timeout from other errors.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return &Response{Status: http.StatusGatewayTimeout}, nil
		}
		return &Response{Status: http.StatusBadGateway}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Build the response headers map.
	headers := make(map[string][]string, len(resp.Header))
	maps.Copy(headers, resp.Header)

	return &Response{
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    respBody,
	}, nil
}
