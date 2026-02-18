/**
 * Management routes for kagami (mounted at /_kagami/).
 *
 * Endpoints:
 * - POST /register     -- Register a new machine (project secret required)
 * - GET  /machines     -- List registered machines (project secret required)
 * - DELETE /machines/:id -- Revoke a machine (project secret required)
 * - GET  /health       -- Health check
 */

import { Hono } from "hono";
import type { KagamiEnv } from "../types.js";
import type { RegisterMachineRequest } from "../types.js";
import { validateProjectSecret, hashSecret, formatSecretHash } from "../lib/auth.js";

export function createManagementRoutes(): Hono<{ Bindings: KagamiEnv }> {
  const app = new Hono<{ Bindings: KagamiEnv }>();

  // --- Health Check ---

  app.get("/health", (c) => {
    return c.json({ status: "ok" });
  });

  // --- Register Machine ---

  app.post("/register", async (c) => {
    const authError = validateProjectSecret(c);
    if (authError) return authError;

    let body: RegisterMachineRequest;
    try {
      body = await c.req.json<RegisterMachineRequest>();
    } catch {
      return c.json(
        { error: "bad_request", message: "Invalid JSON body" },
        400,
      );
    }

    if (
      !body.tunnel_id ||
      typeof body.tunnel_id !== "string" ||
      body.tunnel_id.trim() === ""
    ) {
      return c.json(
        { error: "bad_request", message: "Missing or empty tunnel_id" },
        400,
      );
    }

    const tunnelId = body.tunnel_id.trim();

    // Check for duplicate tunnel_id
    const existing = await c.env.KAGAMI_DB.prepare(
      "SELECT id FROM machines WHERE tunnel_id = ?",
    )
      .bind(tunnelId)
      .first();

    if (existing) {
      return c.json(
        {
          error: "conflict",
          message:
            "tunnel_id already registered. Delete the machine first to re-register.",
        },
        409,
      );
    }

    // Generate machine ID and secret
    const machineId = crypto.randomUUID();
    const secret = `kgm_mach_${crypto.randomUUID().replace(/-/g, "")}`;
    const { hash, salt } = await hashSecret(secret);
    const secretHash = formatSecretHash(salt, hash);
    const registeredAt = new Date().toISOString();

    // Insert into D1
    await c.env.KAGAMI_DB.prepare(
      "INSERT INTO machines (id, tunnel_id, secret_hash, hostname, os, registered_at) VALUES (?, ?, ?, ?, ?, ?)",
    )
      .bind(
        machineId,
        tunnelId,
        secretHash,
        body.hostname ?? null,
        body.os ?? null,
        registeredAt,
      )
      .run();

    return c.json(
      {
        machine_id: machineId,
        tunnel_id: tunnelId,
        secret,
      },
      201,
    );
  });

  // --- List Machines ---

  app.get("/machines", async (c) => {
    const authError = validateProjectSecret(c);
    if (authError) return authError;

    const { results } = await c.env.KAGAMI_DB.prepare(
      "SELECT id, tunnel_id, registered_at, last_seen_at, hostname, os FROM machines ORDER BY registered_at DESC",
    ).all();

    return c.json({
      machines: results.map((row) => ({
        id: row.id as string,
        tunnel_id: row.tunnel_id as string,
        registered_at: row.registered_at as string,
        last_seen_at: (row.last_seen_at as string) ?? null,
        hostname: (row.hostname as string) ?? null,
        os: (row.os as string) ?? null,
      })),
    });
  });

  // --- Delete Machine ---

  app.delete("/machines/:id", async (c) => {
    const authError = validateProjectSecret(c);
    if (authError) return authError;

    const machineId = c.req.param("id");

    const result = await c.env.KAGAMI_DB.prepare(
      "DELETE FROM machines WHERE id = ?",
    )
      .bind(machineId)
      .run();

    if (result.meta.changes === 0) {
      return c.json(
        { error: "not_found", message: "Machine not found" },
        404,
      );
    }

    return c.body(null, 204);
  });

  return app;
}
