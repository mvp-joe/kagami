/**
 * Wire protocol types and serialization for kagami tunnels.
 *
 * All messages are sent as WebSocket binary frames:
 *   [4-byte header length (uint32 BE)][JSON header][raw body bytes]
 *
 * Control messages (ping, pong, error) have no body.
 * Large bodies are chunked across multiple frames.
 */

// --- Message Header Types ---

export interface MessageHeader {
  type:
    | "http_request"
    | "http_response"
    | "http_body_chunk"
    | "ping"
    | "pong"
    | "error";
  id: string;
}

export interface HttpRequestHeader extends MessageHeader {
  type: "http_request";
  method: string;
  host: string;
  path: string;
  query: string;
  headers: Record<string, string[]>;
  chunked?: boolean;
}

export interface HttpResponseHeader extends MessageHeader {
  type: "http_response";
  status: number;
  headers: Record<string, string[]>;
  chunked?: boolean;
}

export interface HttpBodyChunkHeader extends MessageHeader {
  type: "http_body_chunk";
  final: boolean;
}

export interface PingHeader extends MessageHeader {
  type: "ping";
}

export interface PongHeader extends MessageHeader {
  type: "pong";
}

export interface ErrorHeader extends MessageHeader {
  type: "error";
  code: string;
  message: string;
}

export type TunnelHeader =
  | HttpRequestHeader
  | HttpResponseHeader
  | HttpBodyChunkHeader
  | PingHeader
  | PongHeader
  | ErrorHeader;

// --- Constants ---

/** Default chunk size for splitting large bodies (512 KB). */
export const DEFAULT_CHUNK_SIZE = 524288;

// --- Serialization / Deserialization ---

/**
 * Encode a header + optional body into a binary wire frame.
 *
 * Frame format:
 *   [4-byte header length (uint32 BE)][JSON header (UTF-8)][body (raw bytes)]
 */
export function encodeFrame(
  header: TunnelHeader,
  body?: Uint8Array,
): ArrayBuffer {
  const encoder = new TextEncoder();
  const headerBytes = encoder.encode(JSON.stringify(header));
  const headerLen = headerBytes.byteLength;
  const bodyLen = body ? body.byteLength : 0;

  const frame = new ArrayBuffer(4 + headerLen + bodyLen);
  const view = new DataView(frame);

  // 4-byte header length, big-endian
  view.setUint32(0, headerLen, false);

  // JSON header bytes
  const frameBytes = new Uint8Array(frame);
  frameBytes.set(headerBytes, 4);

  // Body bytes (if any)
  if (body && bodyLen > 0) {
    frameBytes.set(body, 4 + headerLen);
  }

  return frame;
}

/**
 * Decode a binary wire frame into header + body.
 *
 * Throws if the frame is malformed (too short, bad header length,
 * missing required fields).
 */
export function decodeFrame(data: ArrayBuffer): {
  header: TunnelHeader;
  body: Uint8Array;
} {
  if (data.byteLength < 4) {
    throw new Error("Frame too short: missing header length");
  }

  const view = new DataView(data);
  const headerLen = view.getUint32(0, false);

  if (4 + headerLen > data.byteLength) {
    throw new Error(
      `Header length ${headerLen} exceeds frame size ${data.byteLength}`,
    );
  }

  const decoder = new TextDecoder();
  const headerBytes = new Uint8Array(data, 4, headerLen);
  const headerJson = decoder.decode(headerBytes);

  let parsed: unknown;
  try {
    parsed = JSON.parse(headerJson);
  } catch {
    throw new Error("Invalid JSON in frame header");
  }

  if (typeof parsed !== "object" || parsed === null) {
    throw new Error("Frame header is not a JSON object");
  }

  const obj = parsed as Record<string, unknown>;

  if (typeof obj.type !== "string") {
    throw new Error("Missing or invalid 'type' field in frame header");
  }

  if (typeof obj.id !== "string") {
    throw new Error("Missing or invalid 'id' field in frame header");
  }

  const header = obj as unknown as TunnelHeader;
  const body = new Uint8Array(data, 4 + headerLen);

  return { header, body };
}

// --- Chunking ---

/** Result of chunking a message: the initial frame plus any continuation frames. */
export interface ChunkedFrames {
  /** All frames in order. First is the initial http_request/http_response, rest are http_body_chunk. */
  frames: ArrayBuffer[];
}

/**
 * Encode a header + body, splitting into chunked frames if the body
 * exceeds chunkSize.
 *
 * For small bodies (<= chunkSize), returns a single frame with no
 * chunked flag. For large bodies, returns:
 *   1. Initial frame with `chunked: true` and first chunk as body
 *   2. N-1 continuation `http_body_chunk` frames
 *   3. Final continuation frame with `final: true`
 */
export function encodeChunked(
  header: HttpRequestHeader | HttpResponseHeader,
  body: Uint8Array,
  chunkSize: number = DEFAULT_CHUNK_SIZE,
): ChunkedFrames {
  if (body.byteLength <= chunkSize) {
    // Single frame, no chunked flag
    return { frames: [encodeFrame(header, body)] };
  }

  const frames: ArrayBuffer[] = [];
  let offset = 0;

  // First frame: initial header with chunked: true + first chunk
  const firstChunk = body.slice(offset, offset + chunkSize);
  offset += chunkSize;
  const chunkedHeader = { ...header, chunked: true as const };
  frames.push(encodeFrame(chunkedHeader, firstChunk));

  // Continuation frames
  while (offset < body.byteLength) {
    const end = Math.min(offset + chunkSize, body.byteLength);
    const chunk = body.slice(offset, end);
    const isFinal = end >= body.byteLength;

    const chunkHeader: HttpBodyChunkHeader = {
      type: "http_body_chunk",
      id: header.id,
      final: isFinal,
    };

    frames.push(encodeFrame(chunkHeader, chunk));
    offset = end;
  }

  return { frames };
}

/**
 * Reassemble chunked body data from multiple frames.
 *
 * Takes an array of body Uint8Arrays (in order: first chunk from initial
 * frame, then each continuation chunk) and concatenates them.
 */
export function reassembleChunks(chunks: Uint8Array[]): Uint8Array {
  if (chunks.length === 0) {
    return new Uint8Array(0);
  }

  if (chunks.length === 1) {
    return chunks[0];
  }

  let totalLen = 0;
  for (const chunk of chunks) {
    totalLen += chunk.byteLength;
  }

  const result = new Uint8Array(totalLen);
  let offset = 0;
  for (const chunk of chunks) {
    result.set(chunk, offset);
    offset += chunk.byteLength;
  }

  return result;
}
