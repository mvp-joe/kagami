# Dependencies

## NPM Package (`packages/kagami/`)

| Package | Version | Purpose |
|---------|---------|---------|
| `hono` | `^4.x` | HTTP framework for route and middleware composition |
| `@cloudflare/workers-types` | latest | TypeScript types for Workers runtime (D1, DO, etc.) |

Dev dependencies:

| Package | Version | Purpose |
|---------|---------|---------|
| `wrangler` | `^4.x` | Cloudflare Workers CLI (dev, test) |
| `vitest` | `^3.x` | Test runner |
| `typescript` | `^5.x` | TypeScript compiler |

## Go Agent

| Package | Version | Purpose |
|---------|---------|---------|
| `github.com/coder/websocket` | `^1.x` | WebSocket client (modern, context-aware) |
| `github.com/BurntSushi/toml` | `^1.x` | TOML config parsing |
| `github.com/spf13/cobra` | `^1.x` | CLI framework with subcommand support |

## Cloudflare Platform Services

| Service | Purpose |
|---------|---------|
| Durable Objects | One DO per machine (digital twin), WebSocket hibernation |
| D1 | Machine registry — stores registered machines and hashed secrets |

## Rationale

**hono** over raw Worker fetch handler: Hono provides clean routing, middleware, and context helpers. Minimal overhead on Workers. The kagami package exports Hono routes and middleware that users compose into their own app.

**github.com/coder/websocket** over gorilla/websocket: More modern API, uses `context.Context` for cancellation, supports `net/http` hijacking. gorilla/websocket is in maintenance mode. Originally authored as `nhooyr.io/websocket`, now maintained by Coder at `github.com/coder/websocket`.

**BurntSushi/toml** over other TOML parsers: The reference TOML parser for Go, written by the TOML spec author. Most complete and correct implementation.

**cobra** over alternatives: Industry standard for Go CLIs (kubectl, docker, hugo all use it). Clean subcommand model fits kagami's command structure.

**D1** over KV or Durable Object storage: D1 provides relational queries (list all machines, lookup by tunnel_id), SQL migrations, and is the natural fit for a machine registry. KV lacks indexing. DO storage is per-DO and can't query across all machines.
