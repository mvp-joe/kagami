# Database Schema

## D1 — Machine Registry

Kagami uses a D1 database (binding: `KAGAMI_DB`) to store registered machines and their secrets.

### Entity Relationship

```
┌──────────────────────┐
│       machines       │
├──────────────────────┤
│ id          TEXT PK  │
│ tunnel_id   TEXT UQ  │
│ secret_hash TEXT     │
│ hostname    TEXT     │
│ os          TEXT     │
│ registered_at TEXT   │
│ last_seen_at  TEXT   │
└──────────────────────┘
```

### machines

| Field | Type | References | Notes |
|-------|------|------------|-------|
| `id` | `TEXT` | PK | UUID, generated on registration |
| `tunnel_id` | `TEXT` | UNIQUE | User-chosen machine name (e.g., `my-homelab`). Maps to DO via `idFromName(tunnel_id)`. |
| `secret_hash` | `TEXT` | — | Hashed per-machine secret (SHA-256 with salt via Web Crypto API) |
| `hostname` | `TEXT` | — | Machine hostname for display (nullable, provided during registration) |
| `os` | `TEXT` | — | Machine OS for display (nullable, provided during registration) |
| `registered_at` | `TEXT` | — | ISO 8601 timestamp of registration |
| `last_seen_at` | `TEXT` | — | ISO 8601 timestamp of last WebSocket connect (nullable, updated on connect) |

### Migration (v1)

```sql
CREATE TABLE machines (
  id TEXT PRIMARY KEY,
  tunnel_id TEXT NOT NULL UNIQUE,
  secret_hash TEXT NOT NULL,
  hostname TEXT,
  os TEXT,
  registered_at TEXT NOT NULL,
  last_seen_at TEXT
);

CREATE INDEX idx_machines_tunnel_id ON machines(tunnel_id);
```
