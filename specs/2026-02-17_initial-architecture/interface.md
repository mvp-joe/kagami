# Interface Definitions

## Wire Protocol

All messages are sent as **WebSocket binary frames** using a hybrid format: a small JSON metadata header followed by raw body bytes. This avoids base64 overhead and keeps the body as untransformed bytes — kagami is a transport, not an interpreter.

### Frame Format

```
┌─────────────────────────┬──────────────────────────┬──────────────┐
│ header length (4 bytes) │ JSON header (variable)   │ body (raw)   │
│ uint32 big-endian       │ UTF-8 encoded JSON       │ raw bytes    │
└─────────────────────────┴──────────────────────────┴──────────────┘
```

1. **Header length** (4 bytes): uint32 big-endian indicating the byte length of the JSON header
2. **JSON header** (variable): UTF-8 JSON object with message metadata (method, path, status, etc.)
3. **Body** (remaining bytes): Raw bytes — HTTP request/response body with zero transformation. May be empty (0 bytes remaining after JSON header).

### Control Messages

Control messages (ping, pong, error) have no body — the frame is just `[4-byte length][JSON header]`.

### Chunking

Bodies that would exceed the Cloudflare WebSocket message size limit (1 MiB) are split across multiple frames. The initial `http_request` or `http_response` frame includes `chunked: true` and carries the first body chunk. Subsequent `http_body_chunk` frames carry continuation data. The final chunk has `final: true`.

Non-chunked messages (small bodies) omit the `chunked` field entirely.

## Wire Protocol Messages (TypeScript — `packages/kagami/src/protocol.ts`)

```typescript
// JSON header for all message types
interface MessageHeader {
  type: "http_request" | "http_response" | "http_body_chunk" | "ping" | "pong" | "error";
  id: string;
}

// DO → Agent: HTTP request to proxy locally
interface HttpRequestHeader extends MessageHeader {
  type: "http_request";
  method: string;
  host: string;           // original Host header (full, including subdomains)
  path: string;           // path without query string (e.g., "/users")
  query: string;          // query string without leading "?" (e.g., "page=2&limit=10"), empty if none
  headers: Record<string, string[]>; // multi-value headers preserved
  chunked?: boolean;      // true if body is split across multiple frames
  // First chunk (or full body if not chunked) follows as raw bytes
}

// Agent → DO: HTTP response from local service
interface HttpResponseHeader extends MessageHeader {
  type: "http_response";
  status: number;
  headers: Record<string, string[]>; // multi-value headers preserved
  chunked?: boolean;      // true if body is split across multiple frames
  // First chunk (or full body if not chunked) follows as raw bytes
}

// Continuation body chunk for a chunked request/response
interface HttpBodyChunkHeader extends MessageHeader {
  type: "http_body_chunk";
  final: boolean;         // true if this is the last chunk
  // Body chunk follows as raw bytes
}

// Agent → DO: Keepalive ping (no body)
interface PingHeader extends MessageHeader {
  type: "ping";
}

// DO → Agent: Keepalive pong (no body)
interface PongHeader extends MessageHeader {
  type: "pong";
}

// Either direction: Error (no body)
interface ErrorHeader extends MessageHeader {
  type: "error";
  code: string;
  message: string;
}

type TunnelHeader =
  | HttpRequestHeader
  | HttpResponseHeader
  | HttpBodyChunkHeader
  | PingHeader
  | PongHeader
  | ErrorHeader;
```

## Agent Authentication (HTTP Headers)

Used during the WebSocket upgrade handshake, not in WebSocket messages.
The agent sends these as HTTP headers on the `GET /_kagami/connect` request.
The Worker validates the per-machine secret against D1 before routing to the DO.

```typescript
// Sent as HTTP headers during WebSocket upgrade — not a WebSocket message.
// X-Kagami-Tunnel-ID: tunnelId
// X-Kagami-Secret: machineSecret
interface AgentAuth {
  tunnelId: string;       // machine's tunnel ID (maps to DO name)
  secret: string;         // per-machine secret (from registration)
}
```

## Management API Types (TypeScript — `packages/kagami/src/types.ts`)

```typescript
// POST /_kagami/register — request body
interface RegisterMachineRequest {
  tunnel_id: string;       // user-chosen machine name
  hostname?: string;       // machine hostname for display
  os?: string;             // machine OS for display
}

// POST /_kagami/register — response body
interface RegisterMachineResponse {
  machine_id: string;      // generated UUID
  tunnel_id: string;
  secret: string;          // per-machine secret (shown once, never stored in plaintext)
}

// GET /_kagami/machines — response body
interface ListMachinesResponse {
  machines: MachineInfo[];
}

// Machine info for management API
interface MachineInfo {
  id: string;
  tunnel_id: string;
  registered_at: string;   // ISO 8601
  last_seen_at: string | null; // ISO 8601, null if never connected
  hostname: string | null;
  os: string | null;
}
```

## Kagami Package API (TypeScript — `packages/kagami/src/index.ts`)

```typescript
interface KagamiConfig {
  maxBodySize?: number;    // max request body size in bytes (default: 10MB), enforced at DO
  chunkSize?: number;      // WebSocket frame body chunk size (default: 512KB)
}

interface Kagami {
  routes: Hono;            // management routes: register, connect, machines
  proxy: MiddlewareHandler; // subdomain proxy middleware
}

function createKagami(config?: KagamiConfig): Kagami;
```

Required Worker env bindings:

```typescript
interface KagamiEnv {
  TUNNEL: DurableObjectNamespace<TunnelDO>;
  KAGAMI_DB: D1Database;
  KAGAMI_PROJECT_SECRET: string;
  KAGAMI_BASE_DOMAIN: string;
}
```

## Wire Protocol Messages (Go — `internal/protocol/`)

```go
// MessageHeader is the JSON header envelope for all wire messages.
type MessageHeader struct {
    Type string `json:"type"`
    ID   string `json:"id"`
}

// HttpRequestHeader is the JSON header for an HTTP request from DO to agent.
// The raw HTTP body follows as bytes after the JSON header in the frame.
type HttpRequestHeader struct {
    MessageHeader
    Method  string              `json:"method"`
    Host    string              `json:"host"`    // original Host header (full, including subdomains)
    Path    string              `json:"path"`    // path without query string
    Query   string              `json:"query"`   // query string without leading "?", empty if none
    Headers map[string][]string `json:"headers"` // multi-value headers preserved
    Chunked bool                `json:"chunked,omitempty"` // true if body is chunked
}

// HttpResponseHeader is the JSON header for an HTTP response from agent to DO.
// The raw HTTP body follows as bytes after the JSON header in the frame.
type HttpResponseHeader struct {
    MessageHeader
    Status  int                 `json:"status"`
    Headers map[string][]string `json:"headers"` // multi-value headers preserved
    Chunked bool                `json:"chunked,omitempty"` // true if body is chunked
}

// HttpBodyChunkHeader is a continuation chunk for a chunked request/response.
type HttpBodyChunkHeader struct {
    MessageHeader
    Final bool `json:"final"` // true if this is the last chunk
}

// PingHeader is a keepalive from agent to DO. No body.
type PingHeader struct {
    MessageHeader
}

// PongHeader is a keepalive response from DO to agent. No body.
type PongHeader struct {
    MessageHeader
}

// ErrorHeader signals an error. No body.
type ErrorHeader struct {
    MessageHeader
    Code    string `json:"code"`
    Message string `json:"message"`
}

// Frame represents a decoded wire frame: JSON header + raw body bytes.
type Frame struct {
    Header []byte // raw JSON header bytes (for lazy deserialization)
    Body   []byte // raw body bytes (may be nil for control messages)
}
```

## Config File (`/etc/kagami/kagami.toml`)

```go
// Config is the top-level kagami configuration.
type Config struct {
    Agent  AgentConfig    `toml:"agent"`
    Tunnel []TunnelConfig `toml:"tunnel"`
}

// AgentConfig holds agent-level settings.
type AgentConfig struct {
    // TunnelID is the user-chosen name for this machine's tunnel.
    // Maps to DO name: idFromName(TunnelID).
    // Set during `kagami init`.
    TunnelID string `toml:"tunnel_id"`

    // Secret is the per-machine secret for agent authentication.
    // Generated by the Worker during registration and stored locally.
    // Set during `kagami init`.
    Secret string `toml:"secret"`

    // Server is the Worker base URL to connect to (e.g., "kagami.myworkers.dev").
    // Set during `kagami init`.
    Server string `toml:"server"`

    // PingInterval is the interval between keepalive pings sent to the DO (default: 30s).
    // Parsed as a Go duration string (e.g., "30s", "1m").
    PingInterval string `toml:"ping_interval,omitempty"`

    // ReconnectInterval is the base delay before reconnecting (default: 5s).
    // Parsed as a Go duration string (e.g., "5s", "30s").
    ReconnectInterval string `toml:"reconnect_interval,omitempty"`

    // MaxReconnectInterval is the max backoff delay (default: 60s).
    // Parsed as a Go duration string (e.g., "60s", "5m").
    MaxReconnectInterval string `toml:"max_reconnect_interval,omitempty"`

    // ProxyTimeout is the timeout for forwarding requests to local services (default: 25s).
    // Should be less than the DO-side 30s timeout to allow the agent to respond with an error
    // before the DO gives up. Parsed as a Go duration string (e.g., "25s", "30s").
    ProxyTimeout string `toml:"proxy_timeout,omitempty"`
}

// TunnelConfig defines a local service to expose.
type TunnelConfig struct {
    // Name is a human-readable label for this tunnel entry.
    Name string `toml:"name"`

    // LocalAddr is the address of the local service (e.g., "localhost:8080").
    LocalAddr string `toml:"local_addr"`

    // Hostname is the public hostname that routes to this service.
    // Used for hostname-based routing within the DO.
    Hostname string `toml:"hostname,omitempty"`

    // PathPrefix routes requests matching this prefix to this service.
    // Used when multiple services share a single hostname.
    PathPrefix string `toml:"path_prefix,omitempty"`

    // Protocol is the protocol to use when connecting to the local service.
    // One of: "http" (default), "https".
    Protocol string `toml:"protocol,omitempty"`
}
```

### Config File Location

Default: `/etc/kagami/kagami.toml`
Override: `kagami run --config /path/to/kagami.toml`

All subcommands that read config support the `--config` flag for development/testing.

### Example Config

```toml
# /etc/kagami/kagami.toml
# Generated by `kagami init`

[agent]
tunnel_id = "my-homelab"
secret = "kgm_mach_aBcDeFgHiJkLmNoPqRsTuVwXyZ123456"
server = "kagami.myworkers.dev"
# ping_interval = "30s"
# reconnect_interval = "5s"
# max_reconnect_interval = "60s"

[[tunnel]]
name = "api"
local_addr = "localhost:8080"
hostname = "api.my-homelab.kagami.myworkers.dev"

[[tunnel]]
name = "admin"
local_addr = "localhost:3000"
hostname = "admin.my-homelab.kagami.myworkers.dev"
```

## Example Worker Integration (`examples/basic-worker/`)

### `wrangler.toml`

```toml
name = "my-app"
main = "src/index.ts"
compatibility_date = "2026-02-01"

[durable_objects]
bindings = [
  { name = "TUNNEL", class_name = "TunnelDO" }
]

[[d1_databases]]
binding = "KAGAMI_DB"
database_name = "kagami"
database_id = "<your-database-id>"

# Set via: wrangler secret put KAGAMI_PROJECT_SECRET
[vars]
KAGAMI_BASE_DOMAIN = "kagami.myworkers.dev"

[[migrations]]
tag = "v1"
new_classes = ["TunnelDO"]
```

### `src/index.ts`

```typescript
import { Hono } from 'hono';
import { createKagami, TunnelDO } from 'kagami';

type Env = {
  TUNNEL: DurableObjectNamespace<TunnelDO>;
  KAGAMI_DB: D1Database;
  KAGAMI_PROJECT_SECRET: string;
  KAGAMI_BASE_DOMAIN: string;
};

const app = new Hono<{ Bindings: Env }>();
const kagami = createKagami();

// Subdomain requests (*.BASE_DOMAIN) are proxied to the matching DO
app.use('*', kagami.proxy);

// Management routes (only reached on base domain — not subdomains)
app.route('/_kagami', kagami.routes);

// User's own routes
app.get('/', (c) => c.text('My app'));

export { TunnelDO };
export default app;
```
