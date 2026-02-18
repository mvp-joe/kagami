/**
 * Management API types for kagami.
 *
 * These types define the request/response shapes for the
 * management endpoints under /_kagami/.
 */

import type { TunnelDO } from "./tunnel-do.js";

/** POST /_kagami/register - request body */
export interface RegisterMachineRequest {
  tunnel_id: string;
  hostname?: string;
  os?: string;
}

/** POST /_kagami/register - response body */
export interface RegisterMachineResponse {
  machine_id: string;
  tunnel_id: string;
  secret: string;
}

/** GET /_kagami/machines - response body */
export interface ListMachinesResponse {
  machines: MachineInfo[];
}

/** Machine info returned by management API */
export interface MachineInfo {
  id: string;
  tunnel_id: string;
  registered_at: string;
  last_seen_at: string | null;
  hostname: string | null;
  os: string | null;
}

/** Configuration for the kagami package */
export interface KagamiConfig {
  /** Max request body size in bytes (default: 10MB), enforced at DO */
  maxBodySize?: number;
  /** WebSocket frame body chunk size (default: 512KB) */
  chunkSize?: number;
}

/** Required Worker env bindings for kagami */
export interface KagamiEnv {
  TUNNEL: DurableObjectNamespace<TunnelDO>;
  KAGAMI_DB: D1Database;
  KAGAMI_PROJECT_SECRET: string;
  KAGAMI_BASE_DOMAIN: string;
}
