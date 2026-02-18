# Implementation Plan

## Phase 1: Repository Scaffolding

- [x] Initialize git repository
- [x] Create `go.mod` with module path `github.com/jward/kagami`
- [x] Create `packages/kagami/` directory with `package.json`, `tsconfig.json`
- [x] Install package dependencies: `hono`, `@cloudflare/workers-types`
- [x] Install dev dependencies: `wrangler`, `vitest`, `typescript`
- [x] Create `examples/basic-worker/` with `package.json`, `tsconfig.json`, `wrangler.toml`
- [x] Create Go directory structure: `cmd/kagami/`, `internal/config/`, `internal/tunnel/`, `internal/proxy/`, `internal/protocol/`, `internal/service/`
- [x] Create `kagami.example.toml` at repo root
- [x] Create `README.md` with project overview, architecture diagram, quickstart
- [x] Create `.gitignore` for Go binaries, `node_modules`, `wrangler` output, `.dev.vars`

## Phase 2: Wire Protocol

- [x] Define TypeScript message types in `packages/kagami/src/protocol.ts`
- [x] Implement TypeScript message serialization/deserialization helpers
- [x] Implement TypeScript chunking logic (split body into chunks, reassemble chunks)
- [x] Define Go message types in `internal/protocol/messages.go`
- [x] Implement Go message serialization/deserialization
- [x] Implement Go chunking logic (split body into chunks, reassemble chunks)
- [x] Write unit tests for both TypeScript and Go protocol handling
- [x] Write unit tests for chunking (both sides)
- [x] Verify JSON output is identical between TypeScript and Go implementations

## Phase 3: Cloudflare Worker Package + Durable Object

- [x] Create D1 migration SQL in `examples/basic-worker/migrations/`
- [x] Implement `createKagami()` factory in `packages/kagami/src/index.ts`
- [x] Implement `TunnelDO` class with WebSocket Hibernation API
- [x] Implement proxy middleware: subdomain detection, tunnel ID extraction, DO routing
- [x] Implement body size enforcement in DO before framing onto WebSocket
- [x] Implement HTTP request → DO → WebSocket framing → pending response correlation
- [x] Implement chunked request framing in DO (large bodies split across frames)
- [x] Implement chunked response reassembly in DO
- [x] Implement timeout handling (30s default) with 504 response
- [x] Implement agent disconnect detection with 502 for pending/new requests
- [x] Implement `webSocketMessage` handler for response correlation
- [x] Implement `webSocketClose` handler for cleanup
- [x] Implement `POST /_kagami/register` — validate project secret, generate machine secret, store in D1
- [x] Implement `GET /_kagami/connect` — validate machine secret via D1, route WebSocket to DO
- [x] Implement `GET /_kagami/machines` — list machines from D1 (project secret protected)
- [x] Implement `DELETE /_kagami/machines/:id` — revoke machine from D1 (project secret protected)
- [x] Implement `GET /_kagami/health` — health check
- [x] Wire up example Worker in `examples/basic-worker/src/index.ts`
- [x] Write Worker integration tests with Miniflare/Vitest

## Phase 4: Go Agent Core

- [x] Implement TOML config parsing in `internal/config/`
- [x] Implement config validation (required fields, valid addresses)
- [x] Implement WebSocket client in `internal/tunnel/` using `github.com/coder/websocket`
- [x] Implement connection lifecycle: connect, authenticate (machine secret), keepalive ping loop
- [x] Implement exponential backoff reconnection on disconnect
- [x] Implement message receive loop: parse incoming `http_request` messages
- [x] Implement chunked request reassembly in agent (buffer chunks by request ID)
- [x] Implement HTTP reverse proxy in `internal/proxy/` using `net/http/httputil.ReverseProxy`
- [x] Implement request routing: match incoming request to correct tunnel config by hostname
- [x] Implement response framing: local HTTP response → `http_response` message → send on WebSocket
- [x] Implement chunked response framing in agent (large response bodies split across frames)
- [x] Wire everything together in `cmd/kagami/main.go` with `kagami run` subcommand
- [x] Write unit tests for config, protocol, proxy, and tunnel packages

## Phase 5: CLI & systemd

- [x] Set up CLI framework (cobra or similar) in `cmd/kagami/`
- [x] Implement `kagami run` — foreground daemon entry point
- [x] Implement `kagami project-secret` — generate random project secret, print with setup instructions
- [x] Implement `kagami init` — prompt for Worker URL, project secret, machine name; call registration API; write config
- [x] Implement `kagami install` — generate systemd unit, copy to `/etc/systemd/system/`, enable
- [x] Implement `kagami uninstall` — stop, disable, remove unit file
- [x] Implement `kagami start` / `stop` / `restart` — wrappers around `systemctl`
- [x] Implement `kagami status` — show connection state and configured tunnels
- [x] Implement `kagami tunnel list` — display configured tunnels from TOML
- [x] Implement `kagami tunnel add` — append tunnel entry to config
- [x] Implement `kagami tunnel remove` — remove tunnel entry from config
- [x] Write unit tests for `tunnel add/remove` (config file modification, duplicate handling, TOML validity)

## Phase 6: End-to-End Testing & Polish

- [ ] Deploy example Worker to Cloudflare (dev environment)
- [ ] Test full flow: `kagami init` → register → `kagami run` → connect → external HTTP request → local service → response
- [ ] Test reconnection after network disruption
- [ ] Test multi-tunnel routing (multiple local services)
- [ ] Test agent behavior when local service is down
- [ ] Test chunking with large request/response bodies
- [ ] Test body size limit enforcement (413 response)
- [ ] Test machine revocation (delete machine, verify agent can't reconnect)
- [ ] Test systemd install/uninstall cycle
- [x] Add structured logging to agent (slog, JSON format for systemd journal)
- [x] Add graceful shutdown handling (SIGTERM/SIGINT — complete in-flight requests, close WebSocket cleanly)
- [x] Review and finalize README with real quickstart instructions

## Notes

- The kagami package should have no opinion on the user's project structure — it provides components, not a deployment.
- Config file permissions: `/etc/kagami/kagami.toml` should be readable only by root/kagami user since it contains the machine secret.
- macOS / launchd support is planned for v2 as a fast-follow.
