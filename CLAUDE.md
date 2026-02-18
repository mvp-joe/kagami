# Kagami

Local-proxy tunnel tool: Go agent ↔ Cloudflare Durable Object ↔ external HTTP. NPM package for the Worker side, single Go binary for the agent side.

## Architecture

- **NPM package** (`packages/kagami/`) — Hono routes + proxy middleware + TunnelDO class. Users integrate into their own Worker.
- **Go agent** (`cmd/kagami/`) — Single binary with subcommands. Connects outbound via WebSocket to the DO.
- **D1** — Machine registry. Per-machine secrets hashed and stored during registration.
- **Wire protocol** — WebSocket binary frames: `[4-byte header length][JSON header][raw body bytes]`. Chunking for bodies > 1 MiB.

## Key Directories

| Path | Purpose |
|------|---------|
| `packages/kagami/src/` | NPM package source (TypeScript) |
| `packages/kagami/src/protocol.ts` | Wire protocol types + serialization |
| `packages/kagami/src/tunnel-do.ts` | Durable Object class |
| `packages/kagami/src/middleware/proxy.ts` | Subdomain proxy middleware |
| `packages/kagami/src/routes/` | Management + connect endpoints |
| `examples/basic-worker/` | Example Worker showing integration |
| `cmd/kagami/` | Go binary entry point |
| `internal/config/` | TOML config parsing + validation |
| `internal/tunnel/` | WebSocket client, reconnection |
| `internal/proxy/` | HTTP reverse proxy to local services |
| `internal/protocol/` | Wire protocol (Go, mirrors TS) |
| `internal/service/` | systemd unit file generation |
| `specs/` | Feature specifications |

## Commands

```bash
# TypeScript (NPM package)
cd packages/kagami && npm install && npm test

# Go agent
go build -o kagami ./cmd/kagami
go test ./internal/...

# Example worker
cd examples/basic-worker && npm install && wrangler dev
```

## Conventions

- **Config**: TOML at `/etc/kagami/kagami.toml`. `--config` flag overrides.
- **Auth**: Two-tier. Project secret (`KAGAMI_PROJECT_SECRET` env) for admin/registration. Per-machine secret (in D1) for agent connections.
- **Routing**: Host matching `*.KAGAMI_BASE_DOMAIN` → proxy. Tunnel ID = rightmost subdomain label before BASE_DOMAIN. Full Host forwarded to agent.
- **Worker bindings**: `TUNNEL` (DO namespace), `KAGAMI_DB` (D1), `KAGAMI_PROJECT_SECRET` (secret), `KAGAMI_BASE_DOMAIN` (var).
- **Management routes**: All under `/_kagami/` prefix.
- **Body limits**: Enforced at DO (default 10MB). Agent-side services are trusted.
- **Go style**: `internal/` packages, cobra CLI, slog for logging, `net/http/httputil.ReverseProxy` for proxying.

## Spec

Full specification at `specs/2026-02-17_initial-architecture/`. Start with `overview.md`.
