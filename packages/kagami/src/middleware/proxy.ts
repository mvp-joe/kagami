/**
 * Subdomain proxy middleware for kagami.
 *
 * Runs on all requests. Checks if the Host header matches *.BASE_DOMAIN.
 * If a subdomain is present, extracts the tunnel ID (rightmost label before
 * BASE_DOMAIN) and routes the request to the corresponding TunnelDO.
 * If no subdomain, calls next() to let management or user routes handle it.
 *
 * Routing algorithm:
 * 1. Extract Host header
 * 2. Check if Host ends with BASE_DOMAIN and has at least one label to the left
 * 3. No match -> next()
 * 4. Match -> tunnel ID is the rightmost subdomain label before BASE_DOMAIN
 *    - my-homelab.kagami.myworkers.dev -> my-homelab
 *    - api.my-homelab.kagami.myworkers.dev -> my-homelab
 *    - a.b.c.my-homelab.kagami.myworkers.dev -> my-homelab
 * 5. Route to DO via idFromName(tunnelId)
 * 6. Forward full original request to DO
 */

import type { MiddlewareHandler } from "hono";
import type { KagamiConfig, KagamiEnv } from "../types.js";
import { DEFAULT_CHUNK_SIZE } from "../protocol.js";
import { DEFAULT_MAX_BODY_SIZE } from "../lib/constants.js";

/**
 * Extract the tunnel ID from a host string given a base domain.
 * Returns null if the host does not have a subdomain under the base domain.
 *
 * The tunnel ID is the rightmost subdomain label immediately before the base domain.
 */
export function extractTunnelId(
  host: string,
  baseDomain: string,
): string | null {
  // Strip port from host if present
  const hostWithoutPort = host.split(":")[0];
  const normalizedBase = baseDomain.toLowerCase();
  const normalizedHost = hostWithoutPort.toLowerCase();

  // Host must end with .BASE_DOMAIN (with the dot prefix)
  if (!normalizedHost.endsWith(`.${normalizedBase}`)) {
    return null;
  }

  // Extract the subdomain portion (everything before .BASE_DOMAIN)
  const subdomainPortion = normalizedHost.slice(
    0,
    normalizedHost.length - normalizedBase.length - 1,
  );

  if (subdomainPortion.length === 0) {
    return null;
  }

  // Tunnel ID is the rightmost label in the subdomain portion
  const labels = subdomainPortion.split(".");
  return labels[labels.length - 1];
}

/**
 * Create the subdomain proxy middleware.
 *
 * Injects X-Kagami-Max-Body-Size and X-Kagami-Chunk-Size headers
 * on the internal fetch to the DO so it knows the configured limits.
 */
export function createProxyMiddleware(
  config?: KagamiConfig,
): MiddlewareHandler<{
  Bindings: KagamiEnv;
}> {
  const maxBodySize = config?.maxBodySize ?? DEFAULT_MAX_BODY_SIZE;
  const chunkSize = config?.chunkSize ?? DEFAULT_CHUNK_SIZE;

  return async (c, next) => {
    const host = c.req.header("Host");
    if (!host) {
      await next();
      return;
    }

    const baseDomain = c.env.KAGAMI_BASE_DOMAIN;
    const tunnelId = extractTunnelId(host, baseDomain);

    if (!tunnelId) {
      // Not a subdomain request -- fall through to management/user routes
      await next();
      return;
    }

    // Route to the TunnelDO
    const doId = c.env.TUNNEL.idFromName(tunnelId);
    const stub = c.env.TUNNEL.get(doId);

    // Clone request with config headers for the DO
    const headers = new Headers(c.req.raw.headers);
    headers.set("X-Kagami-Max-Body-Size", maxBodySize.toString());
    headers.set("X-Kagami-Chunk-Size", chunkSize.toString());

    const doRequest = new Request(c.req.raw, { headers });
    const response = await stub.fetch(doRequest);
    return response;
  };
}
