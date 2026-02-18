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
 * - Stream response bodies back to callers (no full-body buffering)
 * - Return 502 when agent is not connected
 *
 * The DO's fetch() serves two purposes:
 * 1. Agent WebSocket upgrade (from connect route) -- check for Upgrade header
 * 2. HTTP proxy request (from proxy middleware) -- frame and send to agent
 *
 * Config values are passed via custom request headers from the proxy middleware:
 *   X-Kagami-Chunk-Size
 */

import { DurableObject } from "cloudflare:workers";
import type {
  TunnelHeader,
  HttpResponseHeader,
  HttpBodyChunkHeader,
  HttpRequestHeader,
  ErrorHeader,
} from "./protocol.js";
import {
  encodeFrame,
  decodeFrame,
  DEFAULT_CHUNK_SIZE,
} from "./protocol.js";

/** Default request timeout: 30 seconds */
const REQUEST_TIMEOUT_MS = 30_000;

/** Tag used to identify the agent WebSocket via Hibernation API */
const AGENT_WS_TAG = "agent";

/** Pending request entry: holds the resolver and timeout handle. */
interface PendingRequest {
  resolve: (response: Response) => void;
  timeout: ReturnType<typeof setTimeout>;
}

/** Active response writer for a chunked response stream. */
interface ResponseWriter {
  writer: WritableStreamDefaultWriter<Uint8Array>;
  timeout: ReturnType<typeof setTimeout>;
}

export class TunnelDO extends DurableObject {
  /** Map of in-flight request IDs to their pending response resolvers. */
  private pendingRequests = new Map<string, PendingRequest>();

  /** Map of request IDs to active streaming response writers. */
  private responseWriters = new Map<string, ResponseWriter>();

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

    // Auto-respond to text "ping" with "pong" without waking the DO.
    this.ctx.setWebSocketAutoResponse(
      new WebSocketRequestResponsePair("ping", "pong"),
    );

    return new Response(null, { status: 101, webSocket: client });
  }

  /**
   * Proxy an external HTTP request to the agent via the WebSocket.
   *
   * 1. Check agent is connected -- reject with 502 if not
   * 2. Stream the request body as binary frames to the agent
   * 3. Await the response with timeout
   */
  private async handleHttpProxy(request: Request): Promise<Response> {
    // Read config from headers (set by proxy middleware)
    const chunkSize = parseInt(
      request.headers.get("X-Kagami-Chunk-Size") ?? "",
      10,
    ) || DEFAULT_CHUNK_SIZE;

    // --- Agent connection check (before reading body) ---
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
        this.abortResponseWriter(requestId);

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

    // Stream the request body to the agent
    try {
      await this.streamRequestBody(
        request,
        requestHeader,
        agentWs,
        chunkSize,
      );
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
   * Stream the request body to the agent in chunked frames.
   *
   * Reads the request body in chunkSize-sized pieces, sending each as a
   * wire frame as soon as it's read. Holds at most one chunk in memory.
   *
   * For requests with no body or a body that fits in a single chunk, sends
   * a single non-chunked http_request frame. For larger bodies, sends an
   * initial chunked http_request frame followed by http_body_chunk continuation
   * frames.
   */
  private async streamRequestBody(
    request: Request,
    requestHeader: HttpRequestHeader,
    agentWs: WebSocket,
    chunkSize: number,
  ): Promise<void> {
    // No body for these methods
    if (
      request.method === "GET" ||
      request.method === "HEAD" ||
      request.method === "OPTIONS" ||
      !request.body
    ) {
      agentWs.send(encodeFrame(requestHeader, new Uint8Array(0)));
      return;
    }

    const reader = request.body.getReader();
    let buffer = new Uint8Array(0);
    let sentInitial = false;

    try {
      while (true) {
        const { done, value } = await reader.read();

        if (value) {
          // Append to buffer
          buffer = concatUint8Arrays(buffer, value);
        }

        if (done) {
          if (!sentInitial) {
            // Entire body fits in one frame (or is empty) — send non-chunked.
            agentWs.send(encodeFrame(requestHeader, buffer));
          } else {
            // Send final continuation chunk.
            const chunkHeader: HttpBodyChunkHeader = {
              type: "http_body_chunk",
              id: requestHeader.id,
              final: true,
            };
            agentWs.send(encodeFrame(chunkHeader, buffer));
          }
          break;
        }

        // Flush full chunks from the buffer.
        while (buffer.byteLength >= chunkSize) {
          const chunk = buffer.slice(0, chunkSize);
          buffer = buffer.slice(chunkSize);

          if (!sentInitial) {
            // First chunk — send as chunked http_request.
            const chunkedHeader: HttpRequestHeader = {
              ...requestHeader,
              chunked: true,
            };
            agentWs.send(encodeFrame(chunkedHeader, chunk));
            sentInitial = true;
          } else {
            // Continuation chunk (not final — there's more data or buffer remaining).
            const chunkHeader: HttpBodyChunkHeader = {
              type: "http_body_chunk",
              id: requestHeader.id,
              final: false,
            };
            agentWs.send(encodeFrame(chunkHeader, chunk));
          }
        }
      }
    } finally {
      reader.releaseLock();
    }
  }

  // --- WebSocket Hibernation API handlers ---

  /**
   * Handle incoming WebSocket messages from the agent.
   *
   * Parses binary frames and dispatches based on message type:
   * - http_response: resolve pending request (or start streaming for chunked)
   * - http_body_chunk: write chunk to stream, close on final
   * - Malformed messages: log and ignore (don't terminate connection)
   */
  async webSocketMessage(
    _ws: WebSocket,
    message: ArrayBuffer | string,
  ): Promise<void> {
    // Text messages are handled by setWebSocketAutoResponse (ping/pong).
    // The auto-response may still trigger the handler in some edge cases.
    if (typeof message === "string") {
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
   * If the response is chunked, create a TransformStream, resolve the pending
   * promise immediately with the readable side, write the first chunk, and
   * store the writer for subsequent chunks.
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
      // Create a streaming response: resolve immediately with the readable side.
      const { readable, writable } = new TransformStream<Uint8Array>();
      const writer = writable.getWriter();

      const responseHeaders = this.buildResponseHeaders(header.headers);

      // Resolve the pending request with the streaming response now.
      clearTimeout(pending.timeout);
      this.pendingRequests.delete(header.id);
      pending.resolve(
        new Response(readable, {
          status: header.status,
          headers: responseHeaders,
        }),
      );

      // Write the first chunk.
      writer.write(body);

      // Store the writer with a timeout for cleanup.
      const timeout = setTimeout(() => {
        this.abortResponseWriter(header.id);
      }, REQUEST_TIMEOUT_MS);

      this.responseWriters.set(header.id, { writer, timeout });
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
   * Writes the chunk body to the stored response writer. On final chunk,
   * closes the writer to signal stream completion.
   */
  private handleBodyChunk(
    header: HttpBodyChunkHeader,
    body: Uint8Array,
  ): void {
    const rw = this.responseWriters.get(header.id);
    if (!rw) {
      // No active writer (timed out, aborted, or protocol error)
      return;
    }

    rw.writer.write(body);

    if (header.final) {
      clearTimeout(rw.timeout);
      rw.writer.close();
      this.responseWriters.delete(header.id);
    }
  }

  /**
   * Abort and clean up a response writer for the given request ID.
   */
  private abortResponseWriter(requestId: string): void {
    const rw = this.responseWriters.get(requestId);
    if (rw) {
      clearTimeout(rw.timeout);
      try {
        rw.writer.abort("timeout or disconnect");
      } catch {
        // Writer may already be closed; ignore
      }
      this.responseWriters.delete(requestId);
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
   * Cleans up timeouts, response writers, and chunk buffers.
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
    }
    this.pendingRequests.clear();

    // Abort all active response writers
    for (const [id, rw] of this.responseWriters) {
      clearTimeout(rw.timeout);
      try {
        rw.writer.abort("agent disconnected");
      } catch {
        // Writer may already be closed; ignore
      }
    }
    this.responseWriters.clear();
  }
}

/** Concatenate two Uint8Arrays into a new Uint8Array. */
function concatUint8Arrays(a: Uint8Array, b: Uint8Array): Uint8Array {
  const result = new Uint8Array(a.byteLength + b.byteLength);
  result.set(a, 0);
  result.set(b, a.byteLength);
  return result;
}
