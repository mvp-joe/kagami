/**
 * Agent WebSocket connection endpoint.
 *
 * GET /_kagami/connect
 *
 * Validates the machine secret (via X-Kagami-Tunnel-ID and X-Kagami-Secret headers)
 * against D1, then routes the WebSocket upgrade to the appropriate TunnelDO.
 */

import { Hono } from "hono";
import type { KagamiEnv } from "../types.js";
import { hashSecret, parseSecretHash, timingSafeEqual } from "../lib/auth.js";

export function createConnectRoutes(): Hono<{ Bindings: KagamiEnv }> {
  const app = new Hono<{ Bindings: KagamiEnv }>();

  app.get("/connect", async (c) => {
    // Validate WebSocket upgrade
    const upgradeHeader = c.req.header("Upgrade");
    if (!upgradeHeader || upgradeHeader.toLowerCase() !== "websocket") {
      return c.json(
        {
          error: "bad_request",
          message: "Missing required headers or not a WebSocket upgrade",
        },
        400,
      );
    }

    // Validate required headers
    const tunnelId = c.req.header("X-Kagami-Tunnel-ID");
    const secret = c.req.header("X-Kagami-Secret");

    if (!tunnelId) {
      return c.json(
        {
          error: "bad_request",
          message: "Missing required headers or not a WebSocket upgrade",
        },
        400,
      );
    }

    if (!secret) {
      return c.json(
        { error: "unauthorized", message: "Invalid credentials" },
        401,
      );
    }

    // Query D1 for secret_hash
    const machine = await c.env.KAGAMI_DB.prepare(
      "SELECT secret_hash FROM machines WHERE tunnel_id = ?",
    )
      .bind(tunnelId)
      .first<{ secret_hash: string }>();

    if (!machine) {
      return c.json(
        { error: "unauthorized", message: "Invalid credentials" },
        401,
      );
    }

    // Parse stored salt:hash, re-hash with same salt, compare timing-safe
    let storedParts: { salt: string; hash: string };
    try {
      storedParts = parseSecretHash(machine.secret_hash);
    } catch {
      return c.json(
        { error: "unauthorized", message: "Invalid credentials" },
        401,
      );
    }

    const { hash: providedHash } = await hashSecret(secret, storedParts.salt);
    if (!timingSafeEqual(providedHash, storedParts.hash)) {
      return c.json(
        { error: "unauthorized", message: "Invalid credentials" },
        401,
      );
    }

    // Update last_seen_at
    await c.env.KAGAMI_DB.prepare(
      "UPDATE machines SET last_seen_at = ? WHERE tunnel_id = ?",
    )
      .bind(new Date().toISOString(), tunnelId)
      .run();

    // Route to DO
    const doId = c.env.TUNNEL.idFromName(tunnelId);
    const stub = c.env.TUNNEL.get(doId);

    return stub.fetch(c.req.raw);
  });

  return app;
}
