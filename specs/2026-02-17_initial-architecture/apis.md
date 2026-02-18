# API Definitions

## Kagami Routes (`/_kagami/*`)

Management and agent connection endpoints, mounted via `kagami.routes` in the user's Hono app. All `/_kagami/*` routes are only reachable on the base domain — subdomain requests are intercepted by the proxy middleware before reaching these routes.

### Machine Registration

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/_kagami/register` | Register a new machine |

**Request headers**:
- `Authorization: Bearer <project_secret>`

**Request body** (`application/json`):
```
{ tunnel_id: string, hostname?: string, os?: string }
```

**Flow**:
1. Validate `Authorization` header against `KAGAMI_PROJECT_SECRET` env var
2. Validate `tunnel_id` is present and not empty
3. Generate a unique machine ID (UUID) and a random per-machine secret
4. Hash the secret (SHA-256 via Web Crypto API)
5. Insert into D1: `machines(id, tunnel_id, secret_hash, hostname, os, registered_at)`
6. Return the plaintext secret to the caller (shown once)

**Response** (`201 Created`):
```
{ machine_id: string, tunnel_id: string, secret: string }
```

**Error responses**:
- `401 Unauthorized` — missing or invalid project secret
- `400 Bad Request` — missing `tunnel_id`
- `409 Conflict` — `tunnel_id` already registered. To re-register, delete the machine first and register again.

### Agent WebSocket Connect

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/_kagami/connect` | WebSocket upgrade for agent connection |

**Request headers**:
- `Upgrade: websocket`
- `X-Kagami-Tunnel-ID: <tunnel_id>` — identifies which DO to connect to
- `X-Kagami-Secret: <machine_secret>` — per-machine secret from registration

**Flow**:
1. Validate presence of required headers and WebSocket upgrade
2. Query D1: `SELECT secret_hash FROM machines WHERE tunnel_id = ?`
3. Validate secret against stored hash
4. Update `last_seen_at` in D1
5. Route to DO: `env.TUNNEL.idFromName(tunnelId)`
6. DO accepts WebSocket via `this.ctx.acceptWebSocket(server)` (hibernatable)
7. Returns `101 Switching Protocols`

**Error responses**:
- `401 Unauthorized` — missing/invalid secret or tunnel_id not found: `{ "error": "unauthorized", "message": "Invalid credentials" }`
- `400 Bad Request` — missing headers or not a WebSocket upgrade: `{ "error": "bad_request", "message": "Missing required headers or not a WebSocket upgrade" }`

### Machine Management

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/_kagami/machines` | List all registered machines |
| `DELETE` | `/_kagami/machines/:id` | Revoke a machine |

**Request headers** (both endpoints):
- `Authorization: Bearer <project_secret>`

**GET `/_kagami/machines` response** (`200 OK`):
```
{ machines: [{ id, tunnel_id, registered_at, last_seen_at, hostname, os }] }
```

**DELETE `/_kagami/machines/:id` flow**:
1. Validate project secret
2. Delete from D1: `DELETE FROM machines WHERE id = ?`
3. Return `204 No Content`

**Error responses**:
- `401 Unauthorized` — missing or invalid project secret
- `404 Not Found` — machine ID not found (for DELETE)

### Health Check

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/_kagami/health` | Health check, returns `200 OK` |

## Proxy Middleware

The proxy middleware (`kagami.proxy`) runs as Hono middleware on all requests. It intercepts subdomain requests and proxies them to the appropriate DO.

### Routing Algorithm

1. Extract `Host` header from request
2. Check if Host ends with `BASE_DOMAIN` (from `KAGAMI_BASE_DOMAIN` env var) and has at least one label to the left
3. If **no match**: call `next()` — request falls through to management routes or user routes
4. If **match**: extract tunnel ID as the **rightmost subdomain label** before `BASE_DOMAIN`
   - `my-homelab.kagami.myworkers.dev` → tunnel ID `my-homelab`
   - `api.my-homelab.kagami.myworkers.dev` → tunnel ID `my-homelab`
   - `a.b.c.my-homelab.kagami.myworkers.dev` → tunnel ID `my-homelab`
5. Route to DO: `env.TUNNEL.idFromName(tunnelId)`
6. Forward the **full original Host header** to the DO (and through to the agent) for local routing

### Body Size Enforcement

Before framing the request onto the WebSocket, the DO checks the request body size against `maxBodySize` (default: 10MB, configurable via `createKagami()`). If the body exceeds the limit, the DO returns `413 Payload Too Large` without forwarding to the agent.

### Response Codes

**Response when agent disconnected**: `502 Bad Gateway` with JSON body `{ "error": "tunnel_offline", "message": "Agent is not connected" }`

**Response on timeout**: `504 Gateway Timeout` with JSON body `{ "error": "timeout", "message": "Agent did not respond in time" }`

**Response when body too large**: `413 Payload Too Large` with JSON body `{ "error": "payload_too_large", "message": "Request body exceeds maximum size" }`

**Note on CORS**: CORS is a passthrough concern. CORS preflight requests (`OPTIONS` with `Origin` and `Access-Control-Request-Method` headers) are forwarded to the local service like any other HTTP request. The local service is responsible for returning appropriate CORS headers. Kagami does not intercept or modify CORS-related headers.

## Durable Object Internal Protocol

The DO communicates with the agent exclusively over WebSocket binary frames (JSON header + raw body). See [interface.md](./interface.md) for the frame format and type definitions.

### Message Types: DO → Agent

| Type | When | Contents |
|------|------|----------|
| `http_request` | External HTTP request received | method, host, path, query, headers, body (possibly chunked) |
| `http_body_chunk` | Continuation of a chunked request | body chunk, final flag |
| `pong` | In response to agent ping | — |
| `error` | Request timeout or internal error | code, message |

### Message Types: Agent → DO

| Type | When | Contents |
|------|------|----------|
| `http_response` | Local service responded | status, headers, body (possibly chunked) |
| `http_body_chunk` | Continuation of a chunked response | body chunk, final flag |
| `ping` | Periodic keepalive | — |

### Request Lifecycle in the DO

1. `fetch()` called with external HTTP request
2. Check body size against `maxBodySize` — reject with 413 if exceeded
3. Generate unique request ID (`crypto.randomUUID()`)
4. Create a Promise and store its resolver in a pending requests map keyed by ID
5. Frame the request as binary frame(s):
   - If body fits in a single WebSocket message: one `http_request` frame
   - If body exceeds chunk size: `http_request` frame with `chunked: true` + first chunk, followed by `http_body_chunk` frames, final chunk has `final: true`
6. Await the Promise with a timeout (default: 30s)
7. When `webSocketMessage()` receives an `http_response` with matching ID, buffer body chunks if chunked, then resolve the Promise
8. Return the HTTP response to the caller

### Timeout Handling

- Default request timeout: 30 seconds (DO side)
- Default proxy timeout: 25 seconds (agent side, less than DO timeout)
- Agent timeout: agent sends an `http_response` with status 504 back to the DO before the DO's 30s timeout fires. Caller sees a 504 from the agent with a clear message.
- DO timeout: if the agent doesn't respond at all within 30s (e.g., agent hung or crashed mid-request), the DO resolves the pending Promise with a 504 response and sends an `error` message to the agent.
- On agent disconnect: reject all pending Promises with 502 responses.

### Hibernation Considerations

- Pending requests map is **in-memory only** — this is safe for HTTP proxying
- The DO cannot hibernate while a `fetch()` handler is awaiting a response — Cloudflare keeps the DO active for all in-flight `fetch()` calls
- Every proxied HTTP request holds a `fetch()` open, so the pending map is guaranteed to be in memory for the request's lifetime
- On wake from hibernation, the pending requests map starts empty (correct — no in-flight requests when idle)
- Agent WebSocket is still connected after wake (Cloudflare maintains it)
- **Future note:** WebSocket passthrough will need durable storage for cross-hibernation correlation
