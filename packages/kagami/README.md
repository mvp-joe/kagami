# kagami

Cloudflare Worker package for [Kagami](../../README.md) tunnels. Provides Hono routes, proxy middleware, and a Durable Object class that relays HTTP requests to a connected Go agent over WebSocket.

## Installation

```bash
npm install kagami
```

## Usage

```typescript
import { Hono } from 'hono';
import { createKagami, TunnelDO } from 'kagami';

const app = new Hono();
const kagami = createKagami();

// Subdomain requests (*.BASE_DOMAIN) are proxied to the matching DO
app.use('*', kagami.proxy);

// Management routes (register, connect, machines, health)
app.route('/_kagami', kagami.routes);

// Your own routes
app.get('/', (c) => c.text('My app'));

export { TunnelDO };
export default app;
```

## Configuration

`createKagami()` accepts an optional config object:

```typescript
createKagami({
  maxBodySize: 10 * 1024 * 1024, // Max request body size in bytes (default: 10MB)
  chunkSize: 512 * 1024,          // WebSocket frame chunk size in bytes (default: 512KB)
});
```

## Required Worker Bindings

Your `wrangler.toml` must include these bindings:

| Binding | Type | Description |
|---------|------|-------------|
| `TUNNEL` | Durable Object namespace | Points to the `TunnelDO` class |
| `KAGAMI_DB` | D1 database | Machine registry |
| `KAGAMI_PROJECT_SECRET` | Secret | Admin key for registration and machine management |
| `KAGAMI_BASE_DOMAIN` | Variable | Base domain for subdomain routing (e.g., `kagami.myworkers.dev`) |

```toml
[durable_objects]
bindings = [{ name = "TUNNEL", class_name = "TunnelDO" }]

[[d1_databases]]
binding = "KAGAMI_DB"
database_name = "kagami"
database_id = "<your-database-id>"

[vars]
KAGAMI_BASE_DOMAIN = "kagami.myworkers.dev"

[[migrations]]
tag = "v1"
new_classes = ["TunnelDO"]
```

## D1 Migration

Create the D1 database and apply the migration:

```bash
wrangler d1 create kagami
wrangler d1 migrations apply kagami
```

The migration creates a `machines` table for the machine registry. See `examples/basic-worker/migrations/0001_create_machines.sql` for the schema.

## Management Endpoints

All mounted under `/_kagami/`:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/_kagami/register` | Project secret | Register a new machine |
| `GET` | `/_kagami/connect` | Machine secret | WebSocket upgrade for agent |
| `GET` | `/_kagami/machines` | Project secret | List registered machines |
| `DELETE` | `/_kagami/machines/:id` | Project secret | Revoke a machine |
| `GET` | `/_kagami/health` | None | Health check |

## License

Apache License 2.0
