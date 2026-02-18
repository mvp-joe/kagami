/**
 * Worker integration tests for kagami.
 *
 * Tests management API endpoints, subdomain routing (proxy middleware),
 * and connect endpoint validation using Hono's app.request() with a
 * mock D1 binding.
 */

import { describe, it, expect } from "vitest";
import { Hono } from "hono";
import { extractTunnelId, createProxyMiddleware } from "../middleware/proxy.js";
import { createManagementRoutes } from "../routes/management.js";
import { createConnectRoutes } from "../routes/connect.js";
import { hashSecret, parseSecretHash } from "../lib/auth.js";
import type { KagamiConfig, KagamiEnv } from "../types.js";

/**
 * Build kagami routes + proxy the same way createKagami() does, but without
 * importing from index.ts (which re-exports TunnelDO and triggers the
 * `cloudflare:workers` import that is unavailable in plain Vitest).
 */
function buildTestKagami(config?: KagamiConfig) {
  const routes = new Hono<{ Bindings: KagamiEnv }>();
  routes.route("/", createManagementRoutes());
  routes.route("/", createConnectRoutes(config));

  return {
    routes,
    proxy: createProxyMiddleware(config),
  };
}

// ---------------------------------------------------------------------------
// Mock D1 — in-memory implementation of the D1Database binding interface
// ---------------------------------------------------------------------------

interface MockRow {
  [key: string]: unknown;
}

/**
 * Minimal in-memory D1 mock that supports the SQL operations used by kagami.
 *
 * Tracks a single `machines` table and supports:
 *   INSERT, SELECT (with WHERE tunnel_id= or no filter), DELETE (with WHERE id=),
 *   UPDATE (with WHERE tunnel_id=)
 */
function createMockD1() {
  const machines: MockRow[] = [];

  function parseInsert(query: string, params: unknown[]) {
    // INSERT INTO machines (id, tunnel_id, secret_hash, hostname, os, registered_at) VALUES (?, ?, ?, ?, ?, ?)
    const [id, tunnelId, secretHash, hostname, os, registeredAt] = params;
    machines.push({
      id,
      tunnel_id: tunnelId,
      secret_hash: secretHash,
      hostname: hostname ?? null,
      os: os ?? null,
      registered_at: registeredAt,
      last_seen_at: null,
    });
    return { meta: { changes: 1 }, results: [] };
  }

  function parseSelect(query: string, params: unknown[]) {
    const lowerQ = query.toLowerCase();
    if (lowerQ.includes("where tunnel_id")) {
      const tunnelId = params[0];
      const found = machines.filter((m) => m.tunnel_id === tunnelId);
      return { results: found };
    }
    if (lowerQ.includes("where id")) {
      const id = params[0];
      const found = machines.filter((m) => m.id === id);
      return { results: found };
    }
    // No WHERE — return all
    return { results: [...machines] };
  }

  function parseDelete(query: string, params: unknown[]) {
    const id = params[0];
    const idx = machines.findIndex((m) => m.id === id);
    if (idx === -1) {
      return { meta: { changes: 0 }, results: [] };
    }
    machines.splice(idx, 1);
    return { meta: { changes: 1 }, results: [] };
  }

  function parseUpdate(query: string, params: unknown[]) {
    const lowerQ = query.toLowerCase();
    if (lowerQ.includes("set last_seen_at")) {
      const [lastSeenAt, tunnelId] = params;
      const machine = machines.find((m) => m.tunnel_id === tunnelId);
      if (machine) {
        machine.last_seen_at = lastSeenAt;
      }
      return { meta: { changes: machine ? 1 : 0 }, results: [] };
    }
    return { meta: { changes: 0 }, results: [] };
  }

  function createStatement(query: string) {
    let boundParams: unknown[] = [];

    const statement = {
      bind(...params: unknown[]) {
        boundParams = params;
        return statement;
      },
      async run() {
        return execute();
      },
      async first<T = MockRow>(): Promise<T | null> {
        const result = execute();
        return (result.results[0] as T) ?? null;
      },
      async all() {
        return execute();
      },
    };

    function execute() {
      const trimmed = query.trim().toLowerCase();
      if (trimmed.startsWith("insert")) {
        return parseInsert(query, boundParams);
      }
      if (trimmed.startsWith("select")) {
        return parseSelect(query, boundParams);
      }
      if (trimmed.startsWith("delete")) {
        return parseDelete(query, boundParams);
      }
      if (trimmed.startsWith("update")) {
        return parseUpdate(query, boundParams);
      }
      throw new Error(`Unsupported query: ${query}`);
    }

    return statement;
  }

  return {
    prepare: createStatement,
    _machines: machines, // exposed for test inspection
  } as unknown as D1Database & { _machines: MockRow[] };
}

// ---------------------------------------------------------------------------
// Mock DurableObjectNamespace — minimal stub for proxy and connect tests
// ---------------------------------------------------------------------------

function createMockDONamespace() {
  return {
    idFromName(name: string) {
      return { name } as unknown as DurableObjectId;
    },
    get(_id: DurableObjectId) {
      return {
        fetch(request: Request) {
          // Return 502 by default (no agent connected)
          return Response.json(
            { error: "tunnel_offline", message: "Agent is not connected" },
            { status: 502 },
          );
        },
      } as unknown as DurableObjectStub;
    },
  } as unknown as DurableObjectNamespace;
}

// ---------------------------------------------------------------------------
// Test app factory — creates a Hono app wired like the example worker
// ---------------------------------------------------------------------------

const PROJECT_SECRET = "test-secret-123";
const BASE_DOMAIN = "kagami.myworkers.dev";

function createTestApp(overrides?: Partial<KagamiEnv>) {
  const kagami = buildTestKagami();
  const app = new Hono<{ Bindings: KagamiEnv }>();

  // Wire up like the example worker
  app.use("*", kagami.proxy);
  app.route("/_kagami", kagami.routes);

  // Default 404 for unmatched routes (same as a real worker)
  app.all("*", (c) => c.text("Not found", 404));

  // Build the env bindings
  const db = createMockD1();
  const env: KagamiEnv = {
    KAGAMI_DB: db,
    KAGAMI_PROJECT_SECRET: PROJECT_SECRET,
    KAGAMI_BASE_DOMAIN: BASE_DOMAIN,
    TUNNEL: createMockDONamespace() as unknown as KagamiEnv["TUNNEL"],
    ...overrides,
  };

  return { app, env, db, kagami };
}

/** Helper to make authenticated requests with the project secret. */
function authHeaders(extra?: Record<string, string>): Record<string, string> {
  return {
    Authorization: `Bearer ${PROJECT_SECRET}`,
    ...extra,
  };
}

// ---------------------------------------------------------------------------
// Subdomain Routing — pure function tests for extractTunnelId
// ---------------------------------------------------------------------------

describe("Subdomain Routing (extractTunnelId)", () => {
  const baseDomain = "kagami.myworkers.dev";

  it("identifies *.BASE_DOMAIN as a proxy request", () => {
    const result = extractTunnelId("my-homelab.kagami.myworkers.dev", baseDomain);
    expect(result).not.toBeNull();
  });

  it("host exactly equal to BASE_DOMAIN is NOT a proxy request", () => {
    const result = extractTunnelId("kagami.myworkers.dev", baseDomain);
    expect(result).toBeNull();
  });

  it("host not ending with BASE_DOMAIN is NOT a proxy request", () => {
    expect(extractTunnelId("other.example.com", baseDomain)).toBeNull();
    expect(extractTunnelId("notmyworkers.dev", baseDomain)).toBeNull();
    expect(extractTunnelId("fakekagami.myworkers.dev", baseDomain)).toBeNull();
  });

  it("extracts tunnel ID from single-level subdomain", () => {
    const result = extractTunnelId("my-homelab.kagami.myworkers.dev", baseDomain);
    expect(result).toBe("my-homelab");
  });

  it("extracts tunnel ID from multi-level subdomain", () => {
    const result = extractTunnelId("api.my-homelab.kagami.myworkers.dev", baseDomain);
    expect(result).toBe("my-homelab");
  });

  it("extracts tunnel ID from deeply nested subdomain", () => {
    const result = extractTunnelId("a.b.c.my-homelab.kagami.myworkers.dev", baseDomain);
    expect(result).toBe("my-homelab");
  });

  it("is case insensitive for host and base domain", () => {
    expect(
      extractTunnelId("My-Homelab.KAGAMI.MyWorkers.Dev", baseDomain),
    ).toBe("my-homelab");
  });

  it("strips port from host before matching", () => {
    expect(
      extractTunnelId("my-homelab.kagami.myworkers.dev:8787", baseDomain),
    ).toBe("my-homelab");
  });
});

// ---------------------------------------------------------------------------
// Subdomain Routing — middleware integration (Host header forwarding)
// ---------------------------------------------------------------------------

describe("Subdomain Routing (middleware)", () => {
  it("preserves the full original Host header when forwarding to the DO", async () => {
    let capturedHost: string | null = null;

    const kagami = buildTestKagami();
    const app = new Hono<{ Bindings: KagamiEnv }>();

    // Use the proxy middleware
    app.use("*", kagami.proxy);
    app.all("*", (c) => c.text("fallthrough"));

    // Create a DO namespace mock that captures the host header
    const doNamespace = {
      idFromName(name: string) {
        return { name } as unknown as DurableObjectId;
      },
      get(_id: DurableObjectId) {
        return {
          fetch(request: Request) {
            capturedHost = request.headers.get("Host");
            return new Response("ok from DO");
          },
        } as unknown as DurableObjectStub;
      },
    } as unknown as KagamiEnv["TUNNEL"];

    const env: KagamiEnv = {
      KAGAMI_DB: createMockD1(),
      KAGAMI_PROJECT_SECRET: PROJECT_SECRET,
      KAGAMI_BASE_DOMAIN: BASE_DOMAIN,
      TUNNEL: doNamespace,
    };

    const originalHost = "api.my-homelab.kagami.myworkers.dev";
    const res = await app.request("http://localhost/test", {
      headers: { Host: originalHost },
    }, env);

    expect(res.status).toBe(200);
    expect(capturedHost).toBe(originalHost);
  });

  it("falls through when Host matches exactly BASE_DOMAIN", async () => {
    const { app, env } = createTestApp();
    const res = await app.request("http://localhost/", {
      headers: { Host: BASE_DOMAIN },
    }, env);

    // Should hit the fallthrough 404, not the proxy
    expect(res.status).toBe(404);
  });

  it("falls through when Host is unrelated to BASE_DOMAIN", async () => {
    const { app, env } = createTestApp();
    const res = await app.request("http://localhost/", {
      headers: { Host: "other.example.com" },
    }, env);

    expect(res.status).toBe(404);
  });
});

// ---------------------------------------------------------------------------
// Health Check
// ---------------------------------------------------------------------------

describe("Health Check", () => {
  it("GET /_kagami/health returns 200", async () => {
    const { app, env } = createTestApp();
    const res = await app.request("http://localhost/_kagami/health", {
      headers: { Host: BASE_DOMAIN },
    }, env);

    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body).toEqual({ status: "ok" });
  });
});

// ---------------------------------------------------------------------------
// Registration API (/_kagami/register)
// ---------------------------------------------------------------------------

describe("Registration API (/_kagami/register)", () => {
  it("valid project secret + valid tunnel_id returns 201 with machine_id and secret", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "my-homelab" }),
    }, env);

    expect(res.status).toBe(201);
    const body = await res.json();
    expect(body.machine_id).toBeDefined();
    expect(body.tunnel_id).toBe("my-homelab");
    expect(body.secret).toBeDefined();
    expect(body.secret).toMatch(/^kgm_mach_/);
  });

  it("missing Authorization header returns 401", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "my-homelab" }),
    }, env);

    expect(res.status).toBe(401);
  });

  it("invalid project secret returns 401", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        Authorization: "Bearer wrong-secret",
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "my-homelab" }),
    }, env);

    expect(res.status).toBe(401);
  });

  it("missing tunnel_id in body returns 400", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({}),
    }, env);

    expect(res.status).toBe(400);
  });

  it("empty tunnel_id returns 400", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "   " }),
    }, env);

    expect(res.status).toBe(400);
  });

  it("duplicate tunnel_id returns 409", async () => {
    const { app, env } = createTestApp();

    // Register the first machine
    await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "my-homelab" }),
    }, env);

    // Try to register again with the same tunnel_id
    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "my-homelab" }),
    }, env);

    expect(res.status).toBe(409);
  });

  it("generated machine secret is cryptographically random and unique", async () => {
    const { app, env } = createTestApp();

    // Register two machines
    const res1 = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "machine-1" }),
    }, env);

    const res2 = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "machine-2" }),
    }, env);

    const body1 = await res1.json();
    const body2 = await res2.json();

    // Secrets should be different
    expect(body1.secret).not.toBe(body2.secret);

    // Secrets should have sufficient length (kgm_mach_ + 32 hex chars from UUID)
    expect(body1.secret.length).toBeGreaterThan(40);
    expect(body2.secret.length).toBeGreaterThan(40);
  });

  it("stores hashed secret, not plaintext", async () => {
    const { app, env, db } = createTestApp();

    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "hash-test" }),
    }, env);

    const body = await res.json();
    const stored = db._machines.find((m) => m.tunnel_id === "hash-test");

    // The stored hash should NOT equal the plaintext secret
    expect(stored!.secret_hash).not.toBe(body.secret);

    // The stored hash should be in salt:hash format
    const storedHash = stored!.secret_hash as string;
    expect(storedHash).toContain(":");
    const { salt, hash } = parseSecretHash(storedHash);
    expect(salt.length).toBeGreaterThan(0);
    expect(hash.length).toBe(64); // SHA-256 hex = 64 chars

    // Re-hashing with the same salt should produce the same hash
    const { hash: expectedHash } = await hashSecret(body.secret, salt);
    expect(hash).toBe(expectedHash);
  });
});

// ---------------------------------------------------------------------------
// Machine Secret Validation (/_kagami/connect)
// ---------------------------------------------------------------------------

describe("Machine Secret Validation (/_kagami/connect)", () => {
  /** Helper: register a machine and return its secret and tunnel_id */
  async function registerMachine(
    app: Hono<{ Bindings: KagamiEnv }>,
    env: KagamiEnv,
    tunnelId: string = "test-tunnel",
  ) {
    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: tunnelId }),
    }, env);

    return res.json() as Promise<{
      machine_id: string;
      tunnel_id: string;
      secret: string;
    }>;
  }

  it("missing X-Kagami-Secret header returns 401", async () => {
    const { app, env } = createTestApp();
    await registerMachine(app, env);

    const res = await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        Upgrade: "websocket",
        "X-Kagami-Tunnel-ID": "test-tunnel",
      },
    }, env);

    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthorized");
  });

  it("invalid machine secret returns 401", async () => {
    const { app, env } = createTestApp();
    await registerMachine(app, env);

    const res = await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        Upgrade: "websocket",
        "X-Kagami-Tunnel-ID": "test-tunnel",
        "X-Kagami-Secret": "wrong-secret",
      },
    }, env);

    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthorized");
  });

  it("tunnel ID not found in D1 returns 401", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        Upgrade: "websocket",
        "X-Kagami-Tunnel-ID": "nonexistent-tunnel",
        "X-Kagami-Secret": "some-secret",
      },
    }, env);

    expect(res.status).toBe(401);
    const body = await res.json();
    expect(body.error).toBe("unauthorized");
  });

  it("missing X-Kagami-Tunnel-ID header returns 400", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        Upgrade: "websocket",
        "X-Kagami-Secret": "some-secret",
      },
    }, env);

    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error).toBe("bad_request");
  });

  it("non-WebSocket request to /_kagami/connect returns 400", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        "X-Kagami-Tunnel-ID": "test-tunnel",
        "X-Kagami-Secret": "some-secret",
      },
    }, env);

    expect(res.status).toBe(400);
    const body = await res.json();
    expect(body.error).toBe("bad_request");
    expect(body.message).toMatch(/WebSocket/i);
  });

  it("valid credentials update last_seen_at in D1", async () => {
    // We cannot complete a real WebSocket upgrade in unit tests, but we can
    // verify that the connect route attempts to update last_seen_at
    // by checking the D1 mock state after a successful auth path.
    //
    // The connect route will validate credentials successfully, update
    // last_seen_at, then attempt to forward to the DO stub. The DO stub
    // will return something (our mock returns 502). We verify the
    // last_seen_at was updated in D1 even though the WS upgrade
    // didn't truly happen.

    const { app, env, db } = createTestApp();
    const machine = await registerMachine(app, env, "seen-test");

    // Verify last_seen_at is null initially
    const before = db._machines.find((m) => m.tunnel_id === "seen-test");
    expect(before!.last_seen_at).toBeNull();

    // Make a connect request with valid credentials
    // (the DO mock will handle the actual WS part)
    await app.request("http://localhost/_kagami/connect", {
      headers: {
        Host: BASE_DOMAIN,
        Upgrade: "websocket",
        "X-Kagami-Tunnel-ID": "seen-test",
        "X-Kagami-Secret": machine.secret,
      },
    }, env);

    // Check that last_seen_at was updated
    const after = db._machines.find((m) => m.tunnel_id === "seen-test");
    expect(after!.last_seen_at).not.toBeNull();
    expect(typeof after!.last_seen_at).toBe("string");
  });
});

// ---------------------------------------------------------------------------
// Machine Management (/_kagami/machines)
// ---------------------------------------------------------------------------

describe("Machine Management (/_kagami/machines)", () => {
  /** Helper: register a machine via the API */
  async function registerMachine(
    app: Hono<{ Bindings: KagamiEnv }>,
    env: KagamiEnv,
    tunnelId: string,
  ) {
    const res = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: tunnelId }),
    }, env);
    return res.json() as Promise<{
      machine_id: string;
      tunnel_id: string;
      secret: string;
    }>;
  }

  it("GET /_kagami/machines with valid project secret returns list of machines", async () => {
    const { app, env } = createTestApp();

    // Register two machines
    await registerMachine(app, env, "machine-a");
    await registerMachine(app, env, "machine-b");

    const res = await app.request("http://localhost/_kagami/machines", {
      headers: { ...authHeaders(), Host: BASE_DOMAIN },
    }, env);

    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.machines).toHaveLength(2);

    // Verify fields are present
    const machine = body.machines[0];
    expect(machine.id).toBeDefined();
    expect(machine.tunnel_id).toBeDefined();
    expect(machine.registered_at).toBeDefined();
    expect(machine).toHaveProperty("last_seen_at");
    expect(machine).toHaveProperty("hostname");
    expect(machine).toHaveProperty("os");
  });

  it("GET /_kagami/machines with no project secret returns 401", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/machines", {
      headers: { Host: BASE_DOMAIN },
    }, env);

    expect(res.status).toBe(401);
  });

  it("GET /_kagami/machines with invalid project secret returns 401", async () => {
    const { app, env } = createTestApp();

    const res = await app.request("http://localhost/_kagami/machines", {
      headers: {
        Authorization: "Bearer wrong-secret",
        Host: BASE_DOMAIN,
      },
    }, env);

    expect(res.status).toBe(401);
  });

  it("DELETE /_kagami/machines/:id with valid project secret removes the machine", async () => {
    const { app, env } = createTestApp();
    const machine = await registerMachine(app, env, "to-delete");

    // Delete the machine
    const deleteRes = await app.request(
      `http://localhost/_kagami/machines/${machine.machine_id}`,
      {
        method: "DELETE",
        headers: { ...authHeaders(), Host: BASE_DOMAIN },
      },
      env,
    );

    expect(deleteRes.status).toBe(204);

    // Verify it's gone from the list
    const listRes = await app.request("http://localhost/_kagami/machines", {
      headers: { ...authHeaders(), Host: BASE_DOMAIN },
    }, env);

    const body = await listRes.json();
    expect(body.machines).toHaveLength(0);
  });

  it("DELETE /_kagami/machines/:id for nonexistent machine returns 404", async () => {
    const { app, env } = createTestApp();

    const res = await app.request(
      "http://localhost/_kagami/machines/nonexistent-id",
      {
        method: "DELETE",
        headers: { ...authHeaders(), Host: BASE_DOMAIN },
      },
      env,
    );

    expect(res.status).toBe(404);
    const body = await res.json();
    expect(body.error).toBe("not_found");
  });

  it("DELETE /_kagami/machines/:id with invalid secret returns 401", async () => {
    const { app, env } = createTestApp();
    const machine = await registerMachine(app, env, "auth-check");

    const res = await app.request(
      `http://localhost/_kagami/machines/${machine.machine_id}`,
      {
        method: "DELETE",
        headers: {
          Authorization: "Bearer wrong",
          Host: BASE_DOMAIN,
        },
      },
      env,
    );

    expect(res.status).toBe(401);
  });

  it("after deletion, machine no longer appears in listing", async () => {
    const { app, env } = createTestApp();
    const m1 = await registerMachine(app, env, "keep-me");
    const m2 = await registerMachine(app, env, "delete-me");

    // Delete m2
    await app.request(
      `http://localhost/_kagami/machines/${m2.machine_id}`,
      {
        method: "DELETE",
        headers: { ...authHeaders(), Host: BASE_DOMAIN },
      },
      env,
    );

    // List should only contain m1
    const listRes = await app.request("http://localhost/_kagami/machines", {
      headers: { ...authHeaders(), Host: BASE_DOMAIN },
    }, env);

    const body = await listRes.json();
    expect(body.machines).toHaveLength(1);
    expect(body.machines[0].tunnel_id).toBe("keep-me");
  });
});

// ---------------------------------------------------------------------------
// Body Size Enforcement (TunnelDO)
// ---------------------------------------------------------------------------

describe("Body Size Enforcement", () => {
  // We test body size enforcement by configuring a low maxBodySize and
  // making proxy requests through the middleware. The DO mock is replaced
  // with one that simulates the TunnelDO's body size check.

  function createBodySizeTestApp(maxBodySize: number) {
    const kagami = buildTestKagami({ maxBodySize });
    const app = new Hono<{ Bindings: KagamiEnv }>();

    app.use("*", kagami.proxy);
    app.route("/_kagami", kagami.routes);
    app.all("*", (c) => c.text("Not found", 404));

    // Create a DO namespace that simulates body size enforcement
    let capturedMaxBodySize: number | null = null;

    const doNamespace = {
      idFromName(name: string) {
        return { name } as unknown as DurableObjectId;
      },
      get(_id: DurableObjectId) {
        return {
          async fetch(request: Request) {
            const maxSizeHeader = request.headers.get("X-Kagami-Max-Body-Size");
            capturedMaxBodySize = maxSizeHeader
              ? parseInt(maxSizeHeader, 10)
              : null;

            // Simulate the DO's body size check
            const limit = capturedMaxBodySize ?? 10 * 1024 * 1024;

            // Check Content-Length first
            const contentLength = request.headers.get("Content-Length");
            if (contentLength) {
              const length = parseInt(contentLength, 10);
              if (!isNaN(length) && length > limit) {
                return Response.json(
                  {
                    error: "payload_too_large",
                    message: "Request body exceeds maximum size",
                  },
                  { status: 413 },
                );
              }
            }

            // Read the body to check actual size
            if (request.body) {
              const body = await request.arrayBuffer();
              if (body.byteLength > limit) {
                return Response.json(
                  {
                    error: "payload_too_large",
                    message: "Request body exceeds maximum size",
                  },
                  { status: 413 },
                );
              }
            }

            return new Response("proxied ok");
          },
        } as unknown as DurableObjectStub;
      },
    } as unknown as KagamiEnv["TUNNEL"];

    const env: KagamiEnv = {
      KAGAMI_DB: createMockD1(),
      KAGAMI_PROJECT_SECRET: PROJECT_SECRET,
      KAGAMI_BASE_DOMAIN: BASE_DOMAIN,
      TUNNEL: doNamespace,
    };

    return { app, env, getCapturedMaxBodySize: () => capturedMaxBodySize };
  }

  it("request body at max size is accepted", async () => {
    const maxSize = 1024; // 1 KB
    const { app, env } = createBodySizeTestApp(maxSize);

    const body = new Uint8Array(maxSize); // exactly at limit
    const res = await app.request("http://localhost/test", {
      method: "POST",
      headers: {
        Host: "my-tunnel.kagami.myworkers.dev",
        "Content-Length": maxSize.toString(),
      },
      body: body,
    }, env);

    expect(res.status).toBe(200);
  });

  it("request body exceeding max size returns 413", async () => {
    const maxSize = 1024; // 1 KB
    const { app, env } = createBodySizeTestApp(maxSize);

    const body = new Uint8Array(maxSize + 1); // one byte over
    const res = await app.request("http://localhost/test", {
      method: "POST",
      headers: {
        Host: "my-tunnel.kagami.myworkers.dev",
        "Content-Length": (maxSize + 1).toString(),
      },
      body: body,
    }, env);

    expect(res.status).toBe(413);
    const json = await res.json();
    expect(json.error).toBe("payload_too_large");
  });

  it("passes configured maxBodySize to DO via X-Kagami-Max-Body-Size header", async () => {
    const maxSize = 2048;
    const { app, env, getCapturedMaxBodySize } =
      createBodySizeTestApp(maxSize);

    await app.request("http://localhost/test", {
      method: "GET",
      headers: { Host: "my-tunnel.kagami.myworkers.dev" },
    }, env);

    expect(getCapturedMaxBodySize()).toBe(maxSize);
  });
});

// ---------------------------------------------------------------------------
// Registration + list integration flow
// ---------------------------------------------------------------------------

describe("Registration + Management Integration Flow", () => {
  it("registered machine appears in GET /_kagami/machines", async () => {
    const { app, env } = createTestApp();

    // Register
    const regRes = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({
        tunnel_id: "flow-test",
        hostname: "my-server",
        os: "linux",
      }),
    }, env);

    expect(regRes.status).toBe(201);
    const registered = await regRes.json();

    // List
    const listRes = await app.request("http://localhost/_kagami/machines", {
      headers: { ...authHeaders(), Host: BASE_DOMAIN },
    }, env);

    const body = await listRes.json();
    expect(body.machines).toHaveLength(1);
    expect(body.machines[0].id).toBe(registered.machine_id);
    expect(body.machines[0].tunnel_id).toBe("flow-test");
    expect(body.machines[0].hostname).toBe("my-server");
    expect(body.machines[0].os).toBe("linux");
    expect(body.machines[0].last_seen_at).toBeNull();
  });

  it("register + delete + list returns empty", async () => {
    const { app, env } = createTestApp();

    // Register
    const regRes = await app.request("http://localhost/_kagami/register", {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/json",
        Host: BASE_DOMAIN,
      },
      body: JSON.stringify({ tunnel_id: "ephemeral" }),
    }, env);

    const registered = await regRes.json();

    // Delete
    const deleteRes = await app.request(
      `http://localhost/_kagami/machines/${registered.machine_id}`,
      {
        method: "DELETE",
        headers: { ...authHeaders(), Host: BASE_DOMAIN },
      },
      env,
    );
    expect(deleteRes.status).toBe(204);

    // List — should be empty
    const listRes = await app.request("http://localhost/_kagami/machines", {
      headers: { ...authHeaders(), Host: BASE_DOMAIN },
    }, env);

    const body = await listRes.json();
    expect(body.machines).toHaveLength(0);
  });
});
