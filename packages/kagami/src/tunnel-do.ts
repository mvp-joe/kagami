/**
 * TunnelDO -- Durable Object class for kagami tunnels.
 *
 * One instance per machine, named by tunnel ID via idFromName().
 * Uses WebSocket Hibernation API for cost efficiency.
 *
 * Responsibilities:
 * - Accept agent WebSocket connection
 * - Relay external HTTP requests to agent via binary-framed WebSocket messages
 * - Correlate responses by request ID
 * - Handle chunking for large bodies
 * - Enforce body size limits
 * - Return 502 when agent is not connected
 *
 * The DO's fetch() serves two purposes:
 * 1. Agent WebSocket upgrade (from connect route) -- check for Upgrade header
 * 2. HTTP proxy request (from proxy middleware) -- frame and send to agent
 *
 * Config values (maxBodySize, chunkSize) are passed via custom request headers
 * from the proxy middleware and connect route:
 *   X-Kagami-Max-Body-Size
 *   X-Kagami-Chunk-Size
 */

import { DurableObject } from "cloudflare:workers";
import type {
  TunnelHeader,
  HttpResponseHeader,
  HttpBodyChunkHeader,
  HttpRequestHeader,
  PongHeader,
  ErrorHeader,
} from "./protocol.js";
import {
  encodeFrame,
  encodeChunked,
  decodeFrame,
  reassembleChunks,
  DEFAULT_CHUNK_SIZE,
} from "./protocol.js";
import { DEFAULT_MAX_BODY_SIZE } from "./lib/constants.js";

/** Default request timeout: 30 seconds */
const REQUEST_TIMEOUT_MS = 30_000;

/** Tag used to identify the agent WebSocket via Hibernation API */
const AGENT_WS_TAG = "agent";

/** Pending request entry: holds the resolver and timeout handle. */
interface PendingRequest {
  resolve: (response: Response) => void;
  timeout: ReturnType<typeof setTimeout>;
}

export class TunnelDO extends DurableObject {
  /** Map of in-flight request IDs to their pending response resolvers. */
  private pendingRequests = new Map<string, PendingRequest>();

  /** Map of request IDs to buffered body chunks (for chunked responses). */
  private chunkBuffers = new Map<string, Uint8Array[]>();

  /** Map of request IDs to response headers (for chunked responses). */
  private chunkedResponseHeaders = new Map<string, HttpResponseHeader>();

  /**
   * Get the current agent WebSocket, if connected.
   * Uses the Hibernation API tag to find it.
   */
  private getAgentWebSocket(): WebSocket | null {
    const sockets = this.ctx.getWebSockets(AGENT_WS_TAG);
    return sockets.length > 0 ? sockets[0] : null;
  }

  /**
   * Handle incoming fetch requests.
   * - WebSocket upgrade requests: accept agent connection
   * - HTTP requests: proxy to agent via WebSocket
   */
  async fetch(request: Request): Promise<Response> {
    const upgradeHeader = request.headers.get("Upgrade");
    if (upgradeHeader && upgradeHeader.toLowerCase() === "websocket") {
      return this.handleAgentConnect(request);
    }
    return this.handleHttpProxy(request);
  }

  /**
   * Accept a new agent WebSocket connection.
   * If an existing agent is connected, close the old connection first
   * (replace, don't reject -- see decisions.md).
   */
  private handleAgentConnect(_request: Request): Response {
    // Close any existing agent connection
    const existingWs = this.getAgentWebSocket();
    if (existingWs) {
      try {
        existingWs.close(1000, "Replaced by new agent connection");
      } catch {
        // Old socket may already be closed; ignore
      }
    }

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    // Accept with Hibernation API, tagged so we can find it later
    this.ctx.acceptWebSocket(server, [AGENT_WS_TAG]);

    return new Response(null, { status: 101, webSocket: client });
  }

  /**
   * Proxy an external HTTP request to the agent via the WebSocket.
   *
   * 1. Check body size against maxBodySize -- reject with 413 if exceeded
   * 2. Check agent is connected -- reject with 502 if not
   * 3. Frame the request as binary message(s)
   * 4. Await the response with timeout
   */
  private async handleHttpProxy(request: Request): Promise<Response> {
    // Read config from headers (set by proxy middleware / connect route)
    const maxBodySize = parseInt(
      request.headers.get("X-Kagami-Max-Body-Size") ?? "",
      10,
    ) || DEFAULT_MAX_BODY_SIZE;
    const chunkSize = parseInt(
      request.headers.get("X-Kagami-Chunk-Size") ?? "",
      10,
    ) || DEFAULT_CHUNK_SIZE;

    // --- Body size enforcement ---
    const body = await this.readAndEnforceBodySize(request, maxBodySize);
    if (body instanceof Response) {
      return body; // 413 error response
    }

    // --- Agent connection check ---
    const agentWs = this.getAgentWebSocket();
    if (!agentWs) {
      return Response.json(
        { error: "tunnel_offline", message: "Agent is not connected" },
        { status: 502 },
      );
    }

    // --- Frame and send the request ---
    const requestId = crypto.randomUUID();
    const url = new URL(request.url);

    // Build multi-value headers map, excluding internal kagami headers.
    // Headers.entries() joins multi-value headers with ", " which breaks
    // Set-Cookie. We handle Set-Cookie separately via getSetCookie().
    const headers: Record<string, string[]> = {};
    for (const [key, value] of request.headers.entries()) {
      const lowerKey = key.toLowerCase();
      if (lowerKey.startsWith("x-kagami-")) continue;
      if (lowerKey === "set-cookie") continue; // handled below
      if (!headers[key]) {
        headers[key] = [];
      }
      headers[key].push(value);
    }

    // Preserve individual Set-Cookie values (getSetCookie returns each
    // Set-Cookie header separately, avoiding the ", " join problem)
    const setCookieValues = request.headers.getSetCookie();
    if (setCookieValues.length > 0) {
      headers["set-cookie"] = setCookieValues;
    }

    const requestHeader: HttpRequestHeader = {
      type: "http_request",
      id: requestId,
      method: request.method,
      host: request.headers.get("Host") ?? url.host,
      path: url.pathname,
      query: url.search ? url.search.slice(1) : "",
      headers,
    };

    // Create promise for response correlation
    const responsePromise = new Promise<Response>((resolve) => {
      const timeout = setTimeout(() => {
        // Timeout: clean up and resolve with 504
        this.pendingRequests.delete(requestId);
        this.chunkBuffers.delete(requestId);
        this.chunkedResponseHeaders.delete(requestId);

        // Notify agent about the timeout
        const errorHeader: ErrorHeader = {
          type: "error",
          id: requestId,
          code: "timeout",
          message: "Request timed out after 30 seconds",
        };
        try {
          const ws = this.getAgentWebSocket();
          if (ws) {
            ws.send(encodeFrame(errorHeader));
          }
        } catch {
          // Agent may have disconnected; ignore
        }

        resolve(
          Response.json(
            { error: "timeout", message: "Agent did not respond in time" },
            { status: 504 },
          ),
        );
      }, REQUEST_TIMEOUT_MS);

      this.pendingRequests.set(requestId, { resolve, timeout });
    });

    // Send the framed request to the agent
    try {
      const { frames } = encodeChunked(requestHeader, body, chunkSize);
      for (const frame of frames) {
        agentWs.send(frame);
      }
    } catch {
      // Failed to send (agent disconnected between check and send)
      const pending = this.pendingRequests.get(requestId);
      if (pending) {
        clearTimeout(pending.timeout);
        this.pendingRequests.delete(requestId);
      }
      return Response.json(
        { error: "tunnel_offline", message: "Agent is not connected" },
        { status: 502 },
      );
    }

    return responsePromise;
  }

  /**
   * Read the request body and enforce the max body size limit.
   *
   * Returns the body as Uint8Array if within limits, or a 413 Response if exceeded.
   * Handles both Content-Length header and streaming bodies.
   */
  private async readAndEnforceBodySize(
    request: Request,
    maxBodySize: number,
  ): Promise<Uint8Array | Response> {
    // No body for these methods
    if (
      request.method === "GET" ||
      request.method === "HEAD" ||
      request.method === "OPTIONS"
    ) {
      return new Uint8Array(0);
    }

    if (!request.body) {
      return new Uint8Array(0);
    }

    // Check Content-Length header first for a fast reject
    const contentLength = request.headers.get("Content-Length");
    if (contentLength) {
      const length = parseInt(contentLength, 10);
      if (!isNaN(length) && length > maxBodySize) {
        return Response.json(
          {
            error: "payload_too_large",
            message: "Request body exceeds maximum size",
          },
          { status: 413 },
        );
      }
    }

    // Read the body incrementally, enforcing the limit for streaming bodies
    const reader = request.body.getReader();
    const chunks: Uint8Array[] = [];
    let totalSize = 0;

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        totalSize += value.byteLength;
        if (totalSize > maxBodySize) {
          reader.cancel();
          return Response.json(
            {
              error: "payload_too_large",
              message: "Request body exceeds maximum size",
            },
            { status: 413 },
          );
        }

        chunks.push(value);
      }
    } finally {
      reader.releaseLock();
    }

    return reassembleChunks(chunks);
  }

  // --- WebSocket Hibernation API handlers ---

  /**
   * Handle incoming WebSocket messages from the agent.
   *
   * Parses binary frames and dispatches based on message type:
   * - http_response: resolve pending request (or start chunked buffering)
   * - http_body_chunk: buffer chunks, resolve on final
   * - ping: respond with pong
   * - Malformed messages: log and ignore (don't terminate connection)
   */
  async webSocketMessage(
    ws: WebSocket,
    message: ArrayBuffer | string,
  ): Promise<void> {
    // Only handle binary messages
    if (typeof message === "string") {
      console.error("TunnelDO: Received unexpected text message from agent");
      return;
    }

    let header: TunnelHeader;
    let body: Uint8Array;

    try {
      const decoded = decodeFrame(message);
      header = decoded.header;
      body = decoded.body;
    } catch (err) {
      console.error(
        "TunnelDO: Failed to decode frame from agent:",
        err instanceof Error ? err.message : err,
      );
      return;
    }

    switch (header.type) {
      case "http_response":
        this.handleHttpResponse(header, body);
        break;

      case "http_body_chunk":
        this.handleBodyChunk(header, body);
        break;

      case "ping":
        this.handlePing(ws, header.id);
        break;

      default:
        console.error(
          `TunnelDO: Unexpected message type from agent: ${header.type}`,
        );
        break;
    }
  }

  /**
   * Handle an http_response message from the agent.
   *
   * If the response is chunked, store the header and buffer the first chunk.
   * If not chunked, resolve the pending request immediately.
   */
  private handleHttpResponse(
    header: HttpResponseHeader,
    body: Uint8Array,
  ): void {
    const pending = this.pendingRequests.get(header.id);
    if (!pending) {
      // No pending request for this ID (timed out or already resolved)
      return;
    }

    if (header.chunked) {
      // Start buffering chunks
      this.chunkedResponseHeaders.set(header.id, header);
      this.chunkBuffers.set(header.id, [body]);
      return;
    }

    // Non-chunked: resolve immediately
    clearTimeout(pending.timeout);
    this.pendingRequests.delete(header.id);

    const responseHeaders = this.buildResponseHeaders(header.headers);
    pending.resolve(
      new Response(body.byteLength > 0 ? body : null, {
        status: header.status,
        headers: responseHeaders,
      }),
    );
  }

  /**
   * Handle an http_body_chunk message from the agent.
   *
   * Buffers chunks by request ID. When the final chunk is received,
   * reassembles the body and resolves the pending request.
   */
  private handleBodyChunk(
    header: HttpBodyChunkHeader,
    body: Uint8Array,
  ): void {
    const pending = this.pendingRequests.get(header.id);
    if (!pending) {
      // No pending request (timed out or resolved)
      this.chunkBuffers.delete(header.id);
      this.chunkedResponseHeaders.delete(header.id);
      return;
    }

    const chunks = this.chunkBuffers.get(header.id);
    if (!chunks) {
      // Received chunk without initial response -- protocol error, ignore
      console.error(
        `TunnelDO: Received body chunk for ${header.id} without initial response`,
      );
      return;
    }

    chunks.push(body);

    if (header.final) {
      // Reassemble and resolve
      clearTimeout(pending.timeout);
      this.pendingRequests.delete(header.id);

      const responseHeader = this.chunkedResponseHeaders.get(header.id);
      this.chunkBuffers.delete(header.id);
      this.chunkedResponseHeaders.delete(header.id);

      const fullBody = reassembleChunks(chunks);
      const status = responseHeader?.status ?? 200;
      const responseHeaders = this.buildResponseHeaders(
        responseHeader?.headers ?? {},
      );

      pending.resolve(
        new Response(fullBody.byteLength > 0 ? fullBody : null, {
          status,
          headers: responseHeaders,
        }),
      );
    }
  }

  /**
   * Handle a ping message from the agent.
   * Respond with a pong using the same ID.
   */
  private handlePing(ws: WebSocket, id: string): void {
    const pongHeader: PongHeader = {
      type: "pong",
      id,
    };
    try {
      ws.send(encodeFrame(pongHeader));
    } catch {
      // Socket may be closing; ignore
    }
  }

  /**
   * Convert multi-value header map to a Headers object.
   * Wire format: Record<string, string[]>
   */
  private buildResponseHeaders(
    headerMap: Record<string, string[]>,
  ): Headers {
    const headers = new Headers();
    for (const [key, values] of Object.entries(headerMap)) {
      for (const value of values) {
        headers.append(key, value);
      }
    }
    return headers;
  }

  /**
   * Handle agent WebSocket close.
   *
   * Marks the agent as disconnected and rejects all pending requests with 502.
   */
  async webSocketClose(
    _ws: WebSocket,
    _code: number,
    _reason: string,
    _wasClean: boolean,
  ): Promise<void> {
    this.rejectAllPending();
  }

  /**
   * Handle agent WebSocket error.
   *
   * Treats the same as close: reject all pending requests with 502.
   */
  async webSocketError(
    _ws: WebSocket,
    _error: unknown,
  ): Promise<void> {
    this.rejectAllPending();
  }

  /**
   * Reject all pending requests with 502 (agent disconnected).
   * Cleans up timeouts and chunk buffers.
   */
  private rejectAllPending(): void {
    for (const [id, pending] of this.pendingRequests) {
      clearTimeout(pending.timeout);
      pending.resolve(
        Response.json(
          { error: "tunnel_offline", message: "Agent is not connected" },
          { status: 502 },
        ),
      );
      this.chunkBuffers.delete(id);
      this.chunkedResponseHeaders.delete(id);
    }
    this.pendingRequests.clear();
  }
}
