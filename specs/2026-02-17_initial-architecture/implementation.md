# Implementation Plan

## Phase 1: Repository Scaffolding

- [ ] Initialize git repository
- [ ] Create `go.mod` with module path `github.com/jward/kagami`
- [ ] Create `packages/kagami/` directory with `package.json`, `tsconfig.json`
- [ ] Install package dependencies: `hono`, `@cloudflare/workers-types`
- [ ] Install dev dependencies: `wrangler`, `vitest`, `typescript`
- [ ] Create `examples/basic-worker/` with `package.json`, `tsconfig.json`, `wrangler.toml`
- [ ] Create Go directory structure: `cmd/kagami/`, `internal/config/`, `internal/tunnel/`, `internal/proxy/`, `internal/protocol/`, `internal/service/`
- [ ] Create `kagami.example.toml` at repo root
- [ ] Create `README.md` with project overview, architecture diagram, quickstart
- [ ] Create `.gitignore` for Go binaries, `node_modules`, `wrangler` output, `.dev.vars`

## Phase 2: Wire Protocol

- [ ] Define TypeScript message types in `packages/kagami/src/protocol.ts`
- [ ] Implement TypeScript message serialization/deserialization helpers
- [ ] Implement TypeScript chunking logic (split body into chunks, reassemble chunks)
- [ ] Define Go message types in `internal/protocol/messages.go`
- [ ] Implement Go message serialization/deserialization
- [ ] Implement Go chunking logic (split body into chunks, reassemble chunks)
- [ ] Write unit tests for both TypeScript and Go protocol handling
- [ ] Write unit tests for chunking (both sides)
- [ ] Verify JSON output is identical between TypeScript and Go implementations

## Phase 3: Cloudflare Worker Package + Durable Object

- [ ] Create D1 migration SQL in `examples/basic-worker/migrations/`
- [ ] Implement `createKagami()` factory in `packages/kagami/src/index.ts`
- [ ] Implement `TunnelDO` class with WebSocket Hibernation API
- [ ] Implement proxy middleware: subdomain detection, tunnel ID extraction, DO routing
- [ ] Implement body size enforcement in DO before framing onto WebSocket
- [ ] Implement HTTP request → DO → WebSocket framing → pending response correlation
- [ ] Implement chunked request framing in DO (large bodies split across frames)
- [ ] Implement chunked response reassembly in DO
- [ ] Implement timeout handling (30s default) with 504 response
- [ ] Implement agent disconnect detection with 502 for pending/new requests
- [ ] Implement `webSocketMessage` handler for response correlation
- [ ] Implement `webSocketClose` handler for cleanup
- [ ] Implement `POST /_kagami/register` — validate project secret, generate machine secret, store in D1
- [ ] Implement `GET /_kagami/connect` — validate machine secret via D1, route WebSocket to DO
- [ ] Implement `GET /_kagami/machines` — list machines from D1 (project secret protected)
- [ ] Implement `DELETE /_kagami/machines/:id` — revoke machine from D1 (project secret protected)
- [ ] Implement `GET /_kagami/health` — health check
- [ ] Wire up example Worker in `examples/basic-worker/src/index.ts`
- [ ] Write Worker integration tests with Miniflare/Vitest

## Phase 4: Go Agent Core

- [ ] Implement TOML config parsing in `internal/config/`
- [ ] Implement config validation (required fields, valid addresses)
- [ ] Implement WebSocket client in `internal/tunnel/` using `github.com/coder/websocket`
- [ ] Implement connection lifecycle: connect, authenticate (machine secret), keepalive ping loop
- [ ] Implement exponential backoff reconnection on disconnect
- [ ] Implement message receive loop: parse incoming `http_request` messages
- [ ] Implement chunked request reassembly in agent (buffer chunks by request ID)
- [ ] Implement HTTP reverse proxy in `internal/proxy/` using `net/http/httputil.ReverseProxy`
- [ ] Implement request routing: match incoming request to correct tunnel config by hostname
- [ ] Implement response framing: local HTTP response → `http_response` message → send on WebSocket
- [ ] Implement chunked response framing in agent (large response bodies split across frames)
- [ ] Wire everything together in `cmd/kagami/main.go` with `kagami run` subcommand
- [ ] Write unit tests for config, protocol, proxy, and tunnel packages

## Phase 5: CLI & systemd

- [ ] Set up CLI framework (cobra or similar) in `cmd/kagami/`
- [ ] Implement `kagami run` — foreground daemon entry point
- [ ] Implement `kagami project-secret` — generate random project secret, print with setup instructions
- [ ] Implement `kagami init` — prompt for Worker URL, project secret, machine name; call registration API; write config
- [ ] Implement `kagami install` — generate systemd unit, copy to `/etc/systemd/system/`, enable
- [ ] Implement `kagami uninstall` — stop, disable, remove unit file
- [ ] Implement `kagami start` / `stop` / `restart` — wrappers around `systemctl`
- [ ] Implement `kagami status` — show connection state and configured tunnels
- [ ] Implement `kagami tunnel list` — display configured tunnels from TOML
- [ ] Implement `kagami tunnel add` — append tunnel entry to config
- [ ] Implement `kagami tunnel remove` — remove tunnel entry from config
- [ ] Write unit tests for `tunnel add/remove` (config file modification, duplicate handling, TOML validity)

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
- [ ] Add structured logging to agent (slog, JSON format for systemd journal)
- [ ] Add graceful shutdown handling (SIGTERM/SIGINT — complete in-flight requests, close WebSocket cleanly)
- [ ] Review and finalize README with real quickstart instructions

## Notes

- The kagami package should have no opinion on the user's project structure — it provides components, not a deployment.
- Config file permissions: `/etc/kagami/kagami.toml` should be readable only by root/kagami user since it contains the machine secret.
- macOS / launchd support is planned for v2 as a fast-follow.
