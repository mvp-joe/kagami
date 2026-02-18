/**
 * Agent WebSocket connection endpoint.
 *
 * GET /_kagami/connect
 *
 * Validates the machine secret (via X-Kagami-Tunnel-ID and X-Kagami-Secret headers)
 * against D1, then routes the WebSocket upgrade to the appropriate TunnelDO.
 */

import { Hono } from "hono";
import type { KagamiConfig, KagamiEnv } from "../types.js";
import { hashSecret, parseSecretHash, timingSafeEqual } from "../lib/auth.js";
import { DEFAULT_CHUNK_SIZE } from "../protocol.js";
import { DEFAULT_MAX_BODY_SIZE } from "../lib/constants.js";

export function createConnectRoutes(
  config?: KagamiConfig,
): Hono<{ Bindings: KagamiEnv }> {
  const maxBodySize = config?.maxBodySize ?? DEFAULT_MAX_BODY_SIZE;
  const chunkSize = config?.chunkSize ?? DEFAULT_CHUNK_SIZE;
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

    // Clone request with config headers for the DO
    const headers = new Headers(c.req.raw.headers);
    headers.set("X-Kagami-Max-Body-Size", maxBodySize.toString());
    headers.set("X-Kagami-Chunk-Size", chunkSize.toString());

    const doRequest = new Request(c.req.raw, { headers });
    return stub.fetch(doRequest);
  });

  return app;
}
