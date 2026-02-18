# Test Specifications

## Unit Tests

### Wire Protocol (Go — `internal/protocol/`)

- Encoding an `HttpRequestHeader` + body produces a valid binary frame (4-byte length + JSON + body)
- Decoding a valid binary frame produces correct `HttpRequestHeader` and raw body bytes
- Decoding a frame with unknown `type` in the JSON header returns an error
- Round-tripping binary body data through encode/decode preserves exact bytes
- Control messages (ping, pong, error) encode with zero body bytes
- Frame with zero-length body decodes correctly (body is nil/empty)
- Header length field accurately reflects the JSON header byte length
- Chunked request: large body is split into correct number of chunks
- Chunked request: first frame has `chunked: true` and first body segment
- Chunked request: continuation frames have type `http_body_chunk` with correct `final` flags
- Chunked request: reassembling all chunks produces the original body exactly
- Non-chunked request: body below chunk size threshold produces a single frame with no `chunked` field

### Wire Protocol (TypeScript — `packages/kagami/src/protocol.ts`)

- Encoding `HttpRequestHeader` + body produces a valid binary frame (ArrayBuffer)
- Decoding a binary frame produces correct `HttpResponseHeader` and raw body bytes
- Rejects frames with missing `type` field in JSON header
- Rejects frames with missing `id` field in JSON header
- Rejects frames where header length exceeds frame size
- Control messages (ping, pong) round-trip with no body
- Chunked response: large body is split into correct number of chunks
- Chunked response: reassembling all chunks produces the original body exactly
- Non-chunked response: small body produces a single frame

### Config Parsing (Go — `internal/config/`)

- Valid TOML with one tunnel parses correctly
- Valid TOML with multiple tunnels parses all entries
- Missing `[agent]` section returns a validation error
- Missing `tunnel_id` returns a validation error
- Missing `secret` returns a validation error
- Missing `server` returns a validation error
- Empty `[[tunnel]]` array returns a validation error (at least one tunnel required)
- Tunnel entry missing `local_addr` returns a validation error
- Tunnel entry missing both `hostname` and `path_prefix` returns a validation error
- Default values applied: `protocol` defaults to `"http"`, reconnect intervals get defaults
- Invalid TOML syntax returns a parse error with line number

### HTTP Proxy (Go — `internal/proxy/`)

- Routes request to correct local service based on hostname match
- Routes request to correct local service based on path prefix match
- Returns 502 when local service is unreachable
- Preserves request headers when proxying
- Preserves response headers when returning
- Handles binary response bodies correctly (raw bytes preserved through proxy)
- Times out if local service doesn't respond within `proxy_timeout` (default: 25s)

### systemd Integration (Go — `internal/service/`)

- Generates valid systemd unit file content
- Unit file contains correct `ExecStart` path
- Unit file references correct config directory
- Install command detects if service already exists
- Uninstall command handles service not existing gracefully

### Tunnel Config Management (Go — CLI)

- `tunnel add` appends a new tunnel entry to existing config
- `tunnel add` with duplicate name returns an error
- `tunnel remove` removes the specified tunnel entry
- `tunnel remove` with nonexistent name returns an error
- Config file remains valid TOML after `tunnel add`
- Config file remains valid TOML after `tunnel remove`
- `tunnel list` displays all configured tunnels

### Subdomain Routing (TypeScript — proxy middleware)

- Host matching `*.BASE_DOMAIN` is identified as a proxy request
- Host exactly equal to `BASE_DOMAIN` is NOT a proxy request (falls through)
- Host not ending with `BASE_DOMAIN` is NOT a proxy request (falls through)
- Extracts tunnel ID from single-level subdomain: `my-homelab.kagami.myworkers.dev` → `my-homelab`
- Extracts tunnel ID from multi-level subdomain: `api.my-homelab.kagami.myworkers.dev` → `my-homelab`
- Extracts tunnel ID from deeply nested subdomain: `a.b.c.my-homelab.kagami.myworkers.dev` → `my-homelab`
- Preserves the full original Host header when forwarding to the DO

### Registration API (TypeScript — `/_kagami/register`)

- Valid project secret + valid tunnel_id returns 201 with machine_id and secret
- Missing `Authorization` header returns 401
- Invalid project secret returns 401
- Missing `tunnel_id` in body returns 400
- Duplicate `tunnel_id` returns 409
- Generated machine secret is cryptographically random and unique

### Machine Secret Validation (TypeScript — `/_kagami/connect`)

- Valid machine secret for existing tunnel_id is accepted (WebSocket upgraded)
- Missing `X-Kagami-Secret` header returns 401
- Invalid machine secret returns 401
- Tunnel ID not found in D1 returns 401
- Missing `X-Kagami-Tunnel-ID` header returns 400
- Non-WebSocket request to `/_kagami/connect` returns 400
- `last_seen_at` is updated on successful connect

### Machine Management (TypeScript — `/_kagami/machines`)

- `GET /_kagami/machines` with valid project secret returns list of machines
- `GET /_kagami/machines` with invalid project secret returns 401
- `DELETE /_kagami/machines/:id` with valid project secret removes the machine
- `DELETE /_kagami/machines/:id` for nonexistent machine returns 404
- After deletion, agent cannot connect with the deleted machine's secret

### Body Size Enforcement (TypeScript — DO)

- Request body at max size is accepted and proxied
- Request body exceeding max size returns 413
- Request with no Content-Length but body exceeding max size returns 413 (read and check)

## Integration Tests

### Registration + Connection Flow

**Given** a deployed Worker with a valid project secret
**When** `POST /_kagami/register` is called with the project secret and a tunnel_id
**Then** a 201 response is returned with a machine secret
**And** the machine appears in `GET /_kagami/machines`

**Given** a registered machine
**When** the agent connects via WebSocket to `/_kagami/connect` with the machine secret
**Then** the WebSocket is accepted and the agent receives no immediate error

**Given** a registered machine
**When** an agent connects with an incorrect machine secret
**Then** the WebSocket is rejected with 401

### Agent ↔ DO Connection

**Given** a connected agent
**When** the agent sends a ping message
**Then** the DO responds with a pong message

**Given** a connected agent
**When** the agent disconnects
**Then** subsequent HTTP requests to the DO return 502

### HTTP Request Proxying (End-to-End)

**Given** a connected agent with a tunnel configured for `localhost:8080`
**And** a local HTTP server running on port 8080
**When** an HTTP GET request arrives at the Worker on a matching subdomain
**Then** the request is forwarded to localhost:8080
**And** the response from localhost:8080 is returned to the original caller with correct status, headers, and body

**Given** a connected agent
**When** an HTTP POST request with a JSON body arrives
**Then** the request body is correctly forwarded to the local service
**And** the response body is correctly returned

**Given** a connected agent
**When** an HTTP request arrives but the local service is down
**Then** the agent returns an error response
**And** the DO returns 502 to the caller

**Given** a connected agent
**When** an HTTP request arrives but the local service takes longer than 25s
**Then** the agent sends a 504 error response to the DO before the DO's 30s timeout
**And** the caller receives 504

**Given** a connected agent
**When** a request with a body larger than 10MB arrives
**Then** the DO returns 413 without forwarding to the agent

### Chunked Transfer

**Given** a connected agent
**When** a request with a body larger than the chunk size but under max body size arrives
**Then** the DO sends the request as multiple chunked frames
**And** the agent reassembles the chunks and proxies the complete request to the local service

**Given** a connected agent
**When** a local service returns a response with a body larger than the chunk size
**Then** the agent sends the response as multiple chunked frames
**And** the DO reassembles the chunks and returns the complete response to the caller

### Multi-Tunnel Routing

**Given** an agent with two tunnels configured:
  - `api.my-homelab.kagami.myworkers.dev` → `localhost:8080`
  - `admin.my-homelab.kagami.myworkers.dev` → `localhost:3000`
**When** a request arrives with Host `api.my-homelab.kagami.myworkers.dev`
**Then** it is routed to `localhost:8080`

**When** a request arrives with Host `admin.my-homelab.kagami.myworkers.dev`
**Then** it is routed to `localhost:3000`

**When** a request arrives with an unrecognized hostname
**Then** it returns 404

### Reconnection

**Given** a connected agent
**When** the WebSocket connection drops unexpectedly
**Then** the agent reconnects with exponential backoff
**And** after reconnection, HTTP requests are proxied successfully again

### Machine Revocation

**Given** a connected agent
**When** the machine is deleted via `DELETE /_kagami/machines/:id`
**And** the agent's WebSocket disconnects and it attempts to reconnect
**Then** the reconnection is rejected with 401

## Error Scenarios

- Agent connects with incorrect machine secret → Worker rejects with 401 before reaching DO
- Unregistered tunnel_id attempts connection → Worker rejects with 401
- Two agents try to connect to the same DO simultaneously → second connection replaces first, old WebSocket is closed
- HTTP request body exceeds maximum size → DO returns 413
- Malformed JSON message received by DO → connection not terminated, message ignored with error logged
- Malformed JSON message received by agent → message ignored with error logged
- Agent config file is missing → clear error message with path to expected config
- Agent config file has invalid TOML → clear error message with line number
- Network partition: agent can't reach Cloudflare → exponential backoff reconnection
- DO receives HTTP request during agent reconnection window → 502 response
- `kagami init` with invalid Worker URL → clear error message
- `kagami init` with invalid project secret → clear error from Worker (401)
- `kagami init` with duplicate tunnel_id → clear error from Worker (409)
