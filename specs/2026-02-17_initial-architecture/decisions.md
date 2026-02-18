# Architectural Decisions

## 2026-02-17: One Durable Object Per Machine (Digital Twin Model)

**Context:** Could use one DO per service, one DO per tunnel entry, or one DO per machine. One DO per service means multiple WebSocket connections from the agent. One DO per machine means the DO handles routing to multiple local services through a single connection.

**Decision:** One DO instance per machine. The DO is a "digital twin" — it represents the entire machine, not individual services. The agent opens a single WebSocket to its twin. Routing between multiple local services happens at the agent level based on hostname/path matching.

**Consequences:**
- (+) Single WebSocket connection per machine, simpler connection management
- (+) DO hibernation is maximally effective (one connection to hibernate)
- (+) Conceptually clean: the DO mirrors the machine
- (-) Agent must handle routing logic locally (trivial with config)
- (-) Single point of failure per machine (acceptable for the use case)

## 2026-02-17: WebSocket Hibernation for Cost Efficiency

**Context:** Durable Objects charge for wall-clock duration while active. A tunnel agent maintains a persistent WebSocket that could keep the DO alive 24/7. WebSocket Hibernation allows the DO to be evicted from memory while keeping the WebSocket connected — Cloudflare's infrastructure maintains the connection and wakes the DO on activity.

**Decision:** Use the Hibernation API (`this.ctx.acceptWebSocket(server)`) for the agent WebSocket. This works because the DO is the server (agent connects to it). In-memory state (pending request map) is safe because Cloudflare keeps the DO active for the duration of any in-flight `fetch()` call — and every proxied HTTP request is an active `fetch()`.

**Consequences:**
- (+) Dramatically reduces cost — DO is only billed for active compute time
- (+) Idle tunnels cost essentially nothing
- (+) In-memory pending request map is safe — DO cannot hibernate while `fetch()` is awaiting
- (-) Constructor runs on every wake, so keep it lightweight
- (-) Future WebSocket passthrough will need durable storage for correlation across hibernation cycles

## 2026-02-17: Two-Tier Authentication with D1 Machine Registry

**Context:** Considered single shared secret, JWT, and HMAC challenge-response. A single shared secret is simple but means all machines share one key — no way to revoke a single machine, no machine registry. JWT adds complexity (key management, token refresh). HMAC adds a handshake step. The real need is: a project-level admin key for management, and per-machine secrets for agent connections, with a registry to track and revoke machines.

**Decision:** Two-tier auth model. A project secret (`KAGAMI_PROJECT_SECRET`) is generated via `kagami project-secret` and set as a Wrangler secret on the Worker. During `kagami init`, the agent presents the project secret to register itself via `POST /_kagami/register`. The Worker generates a unique per-machine secret, hashes it, stores the hash and machine info in D1, and returns the plaintext secret to the agent (shown once). The agent stores it in its local TOML config. On WebSocket connect, the agent presents its per-machine secret. The Worker validates it against D1 before routing to the DO. The project secret also protects management APIs (list machines, revoke).

**Consequences:**
- (+) Per-machine secrets — revoking one machine doesn't affect others
- (+) Machine registry in D1 — list all connected machines, track last-seen, etc.
- (+) Project secret is the admin key — protects registration and management APIs
- (+) Auth at Worker level prevents unauthorized DO activation (cost protection)
- (-) Adds D1 as a dependency
- (-) Registration step required before agent can connect (one-time during `kagami init`)
- (-) D1 lookup on every WebSocket connect (acceptable — connects are infrequent)

## 2026-02-17: NPM Package Distribution

**Context:** Three distribution models considered: (A) clone and deploy a standalone Worker from the repo, (B) Go CLI generates a Worker project, (C) NPM package that users integrate into their own Worker. Option A is rigid — users can't add their own routes or embed kagami in an existing app. Option B mixes concerns (Go binary generating JS code) and is hard to update. Option C is the most composable — users `npm install kagami` and wire it into their Hono app alongside their own functionality.

**Decision:** Distribute the Cloudflare-side components as an NPM package. The package exports `createKagami()` (returns Hono routes and proxy middleware) and the `TunnelDO` class. Users mount kagami into their own Worker app. The mono-repo contains the NPM package source, the Go agent, and an example Worker project showing integration.

**Consequences:**
- (+) Composable — kagami lives alongside user's own routes and middleware
- (+) Easy to update — `npm update kagami`
- (+) Users control their own `wrangler.toml`, deployment config, and domain setup
- (+) No opinion on the user's project structure
- (-) Users must wire up bindings (DO namespace, D1, env vars) in their own `wrangler.toml`
- (-) Slightly more setup than a turnkey deploy (mitigated by example project and docs)

## 2026-02-17: Hybrid Binary Wire Protocol (JSON Header + Raw Body)

**Context:** Considered pure JSON (with base64 body), pure binary framing, MessagePack, and Protobuf. Pure JSON with base64 adds ~33% overhead on every body and requires encode/decode on both sides. Kagami is a transport — it should shuttle bytes, not interpret them. Pure binary is hardest to debug. A hybrid approach gets the best of both worlds.

**Decision:** WebSocket binary frames with a hybrid format: `[4-byte header length][JSON metadata][raw body bytes]`. The JSON header carries small, structured metadata (type, id, method, path, host, query, status, headers). The body is raw bytes with zero transformation — no base64, no encoding. Control messages (ping, pong, error) have no body. Bodies exceeding the WebSocket message size limit are chunked across multiple frames using `http_body_chunk` continuation messages.

**Consequences:**
- (+) Zero overhead on body — raw bytes pass through untouched
- (+) JSON header is still human-readable in logs and debuggable
- (+) No base64 encode/decode on either side
- (+) Headers preserved as structured JSON; body preserved as raw bytes
- (+) Simple to implement: read 4 bytes → read N bytes of JSON → remaining bytes are body
- (+) Chunking handles large bodies transparently
- (-) Slightly more complex frame parsing than pure JSON (trivial — 4 byte length prefix)
- (-) Not as easy to inspect in basic WebSocket tools as pure JSON text frames

## 2026-02-17: TOML Configuration Format

**Context:** YAML is most common in the tunnel tool space (ngrok, cloudflared). JSON lacks comments. TOML is gaining adoption (frp migrated from INI to TOML).

**Decision:** TOML. The `[[tunnel]]` array-of-tables syntax is a natural fit for multiple tunnel definitions. Type-safe, unambiguous, supports comments.

**Consequences:**
- (+) No implicit type coercion footguns (YAML's `NO` → `false` problem)
- (+) Clean syntax for repeated sections (`[[tunnel]]`)
- (+) Comments for self-documenting config files
- (-) Less familiar than YAML to some users
- (-) Slightly smaller ecosystem of tooling vs YAML

## 2026-02-17: Concurrent Agent Connections — Replace, Don't Reject

**Context:** What happens when a second kagami agent connects to a DO that already has an active agent WebSocket? This can happen during agent restarts, network flaps, or misconfigurations.

**Decision:** The second connection replaces the first. The DO closes the old WebSocket and accepts the new one. This fits the digital twin model — there is exactly one active agent per machine at any time.

**Consequences:**
- (+) Clean recovery from network flaps — agent reconnects and takes over immediately
- (+) No stale connection locking out a legitimate agent
- (-) If two different machines accidentally share the same tunnel ID, they will fight for the connection (acceptable — misconfiguration)

## 2026-02-17: Single Binary with Subcommands

**Context:** Could have separate `kagami` (CLI) and `kagamid` (daemon) binaries, or a single binary with subcommands. cloudflared uses the single-binary approach (`cloudflared tunnel run`, `cloudflared service install`).

**Decision:** Single `kagami` binary. `kagami run` is the daemon entrypoint. Other subcommands handle config, systemd, and status. systemd calls `kagami run` via `ExecStart`.

**Consequences:**
- (+) Single binary to distribute, install, update
- (+) Consistent UX — one tool for everything
- (+) systemd unit simply calls `kagami run`
- (-) Binary is slightly larger than a minimal daemon-only binary (negligible)
