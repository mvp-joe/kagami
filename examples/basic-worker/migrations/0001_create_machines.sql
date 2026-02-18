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
