/**
 * Kagami -- Local-proxy tunnel package for Cloudflare Workers.
 *
 * Exports the main factory function and the Durable Object class
 * for integration into a user's Hono Worker application.
 *
 * @example
 * ```typescript
 * import { createKagami, TunnelDO } from 'kagami';
 *
 * const kagami = createKagami();
 * app.use('*', kagami.proxy);
 * app.route('/_kagami', kagami.routes);
 *
 * export { TunnelDO };
 * export default app;
 * ```
 */

import { Hono } from "hono";
import type { MiddlewareHandler } from "hono";
import type { KagamiConfig, KagamiEnv } from "./types.js";
import { createManagementRoutes } from "./routes/management.js";
import { createConnectRoutes } from "./routes/connect.js";
import { createProxyMiddleware } from "./middleware/proxy.js";

export { TunnelDO } from "./tunnel-do.js";
export type { KagamiConfig, KagamiEnv } from "./types.js";

/** Return type of createKagami() */
export interface Kagami {
  /** Management routes: register, connect, machines, health */
  routes: Hono<{ Bindings: KagamiEnv }>;
  /** Subdomain proxy middleware */
  proxy: MiddlewareHandler<{ Bindings: KagamiEnv }>;
}

/**
 * Create kagami routes and proxy middleware.
 *
 * @param config - Optional configuration for body size limits and chunk size.
 *                 Passed through to the TunnelDO via custom request headers.
 */
export function createKagami(config?: KagamiConfig): Kagami {
  const routes = new Hono<{ Bindings: KagamiEnv }>();

  // Mount management routes (health, register, machines)
  routes.route("/", createManagementRoutes());

  // Mount connect route (passes config to DO via headers)
  routes.route("/", createConnectRoutes(config));

  return {
    routes,
    proxy: createProxyMiddleware(config),
  };
}
