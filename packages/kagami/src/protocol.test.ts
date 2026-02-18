import { describe, it, expect } from "vitest";
import {
  encodeFrame,
  decodeFrame,
  encodeChunked,
  reassembleChunks,
  DEFAULT_CHUNK_SIZE,
  type HttpRequestHeader,
  type HttpResponseHeader,
  type PingHeader,
  type PongHeader,
  type HttpBodyChunkHeader,
} from "./protocol.js";

// --- Helpers ---

/** Create a Uint8Array filled with a repeating byte pattern of the given length. */
function makeBody(length: number, seed: number = 0xab): Uint8Array {
  const buf = new Uint8Array(length);
  for (let i = 0; i < length; i++) {
    buf[i] = (seed + i) & 0xff;
  }
  return buf;
}

/** Build a minimal HttpRequestHeader for testing. */
function makeRequestHeader(
  overrides?: Partial<HttpRequestHeader>,
): HttpRequestHeader {
  return {
    type: "http_request",
    id: "req-001",
    method: "GET",
    host: "api.my-homelab.kagami.example.com",
    path: "/users",
    query: "page=1",
    headers: { "content-type": ["application/json"] },
    ...overrides,
  };
}

/** Build a minimal HttpResponseHeader for testing. */
function makeResponseHeader(
  overrides?: Partial<HttpResponseHeader>,
): HttpResponseHeader {
  return {
    type: "http_response",
    id: "req-001",
    status: 200,
    headers: { "content-type": ["application/json"] },
    ...overrides,
  };
}

// --- encodeFrame / decodeFrame ---

describe("encodeFrame + decodeFrame", () => {
  it("encoding HttpRequestHeader + body produces a valid binary frame", () => {
    const header = makeRequestHeader();
    const body = new TextEncoder().encode('{"hello":"world"}');

    const frame = encodeFrame(header, body);

    // Frame is an ArrayBuffer
    expect(frame).toBeInstanceOf(ArrayBuffer);

    // First 4 bytes are the header length (uint32 BE)
    const view = new DataView(frame);
    const headerLen = view.getUint32(0, false);
    expect(headerLen).toBeGreaterThan(0);

    // Remaining bytes after header should equal body length
    expect(frame.byteLength).toBe(4 + headerLen + body.byteLength);

    // JSON header should be valid and match input
    const headerBytes = new Uint8Array(frame, 4, headerLen);
    const parsedHeader = JSON.parse(new TextDecoder().decode(headerBytes));
    expect(parsedHeader.type).toBe("http_request");
    expect(parsedHeader.id).toBe("req-001");
    expect(parsedHeader.method).toBe("GET");

    // Body bytes should match
    const bodyBytes = new Uint8Array(frame, 4 + headerLen);
    expect(bodyBytes).toEqual(body);
  });

  it("decoding a binary frame produces correct HttpResponseHeader and raw body bytes", () => {
    const header = makeResponseHeader({ status: 201 });
    const body = new TextEncoder().encode("response body");

    const frame = encodeFrame(header, body);
    const decoded = decodeFrame(frame);

    expect(decoded.header.type).toBe("http_response");
    expect(decoded.header.id).toBe("req-001");
    const respHeader = decoded.header as HttpResponseHeader;
    expect(respHeader.status).toBe(201);
    expect(respHeader.headers["content-type"]).toEqual(["application/json"]);
    expect(new TextDecoder().decode(decoded.body)).toBe("response body");
  });

  it("round-trips binary body data through encode/decode preserving exact bytes", () => {
    const header = makeRequestHeader({ id: "bin-001" });
    const body = makeBody(1024);

    const frame = encodeFrame(header, body);
    const decoded = decodeFrame(frame);

    expect(decoded.body).toEqual(body);
  });

  it("rejects frames with missing 'type' field in JSON header", () => {
    // Manually build a frame with a header missing `type`
    const headerJson = JSON.stringify({ id: "no-type" });
    const headerBytes = new TextEncoder().encode(headerJson);
    const frame = new ArrayBuffer(4 + headerBytes.byteLength);
    new DataView(frame).setUint32(0, headerBytes.byteLength, false);
    new Uint8Array(frame).set(headerBytes, 4);

    expect(() => decodeFrame(frame)).toThrow(/type/i);
  });

  it("rejects frames with missing 'id' field in JSON header", () => {
    const headerJson = JSON.stringify({ type: "ping" });
    const headerBytes = new TextEncoder().encode(headerJson);
    const frame = new ArrayBuffer(4 + headerBytes.byteLength);
    new DataView(frame).setUint32(0, headerBytes.byteLength, false);
    new Uint8Array(frame).set(headerBytes, 4);

    expect(() => decodeFrame(frame)).toThrow(/id/i);
  });

  it("rejects frames where header length exceeds frame size", () => {
    // Create a frame that claims a header length larger than the actual data
    const frame = new ArrayBuffer(8); // only 4 bytes of "data" after length
    new DataView(frame).setUint32(0, 100, false); // claims 100 bytes of header

    expect(() => decodeFrame(frame)).toThrow(/exceeds frame size/);
  });

  it("handles frame with zero-length body", () => {
    const header = makeRequestHeader({ id: "empty-body" });
    const frame = encodeFrame(header); // no body argument

    const decoded = decodeFrame(frame);
    expect(decoded.header.type).toBe("http_request");
    expect(decoded.body.byteLength).toBe(0);
  });

  it("control messages (ping) round-trip with no body", () => {
    const header: PingHeader = { type: "ping", id: "ping-001" };
    const frame = encodeFrame(header);
    const decoded = decodeFrame(frame);

    expect(decoded.header.type).toBe("ping");
    expect(decoded.header.id).toBe("ping-001");
    expect(decoded.body.byteLength).toBe(0);
  });

  it("control messages (pong) round-trip with no body", () => {
    const header: PongHeader = { type: "pong", id: "pong-001" };
    const frame = encodeFrame(header);
    const decoded = decodeFrame(frame);

    expect(decoded.header.type).toBe("pong");
    expect(decoded.header.id).toBe("pong-001");
    expect(decoded.body.byteLength).toBe(0);
  });

  it("header length field accurately reflects the JSON header byte length", () => {
    const header = makeRequestHeader({
      id: "len-check",
      headers: { "x-custom": ["value-with-utf8-\u00e9"] },
    });
    const frame = encodeFrame(header);
    const view = new DataView(frame);
    const headerLen = view.getUint32(0, false);

    const expectedBytes = new TextEncoder().encode(JSON.stringify(header));
    expect(headerLen).toBe(expectedBytes.byteLength);
  });
});

// --- Chunking ---

describe("encodeChunked", () => {
  it("small body produces a single frame with no chunked field", () => {
    const header = makeResponseHeader({ id: "small-001" });
    const body = makeBody(100);

    const { frames } = encodeChunked(header, body);

    expect(frames).toHaveLength(1);

    // Decode and verify no chunked flag
    const decoded = decodeFrame(frames[0]);
    const respHeader = decoded.header as HttpResponseHeader;
    expect(respHeader.chunked).toBeUndefined();
    expect(decoded.body).toEqual(body);
  });

  it("body exactly at chunk size produces a single frame", () => {
    const chunkSize = 256;
    const header = makeResponseHeader({ id: "exact-001" });
    const body = makeBody(chunkSize);

    const { frames } = encodeChunked(header, body, chunkSize);

    expect(frames).toHaveLength(1);
    const decoded = decodeFrame(frames[0]);
    expect((decoded.header as HttpResponseHeader).chunked).toBeUndefined();
    expect(decoded.body).toEqual(body);
  });

  it("large body is split into correct number of chunks", () => {
    const chunkSize = 256;
    const bodySize = 700; // 256 + 256 + 188 = 3 frames
    const header = makeResponseHeader({ id: "chunk-001" });
    const body = makeBody(bodySize);

    const { frames } = encodeChunked(header, body, chunkSize);

    // 1 initial + 2 continuation = 3 frames
    expect(frames).toHaveLength(3);

    // First frame: http_response with chunked: true
    const first = decodeFrame(frames[0]);
    expect(first.header.type).toBe("http_response");
    expect((first.header as HttpResponseHeader).chunked).toBe(true);
    expect(first.body.byteLength).toBe(chunkSize);

    // Second frame: http_body_chunk with final: false
    const second = decodeFrame(frames[1]);
    expect(second.header.type).toBe("http_body_chunk");
    expect((second.header as HttpBodyChunkHeader).final).toBe(false);
    expect(second.body.byteLength).toBe(chunkSize);

    // Third frame: http_body_chunk with final: true
    const third = decodeFrame(frames[2]);
    expect(third.header.type).toBe("http_body_chunk");
    expect((third.header as HttpBodyChunkHeader).final).toBe(true);
    expect(third.body.byteLength).toBe(bodySize - 2 * chunkSize);
  });

  it("continuation frames share the same id as the initial frame", () => {
    const chunkSize = 100;
    const header = makeRequestHeader({ id: "shared-id-001" });
    const body = makeBody(250);

    const { frames } = encodeChunked(header, body, chunkSize);

    for (const frame of frames) {
      const decoded = decodeFrame(frame);
      expect(decoded.header.id).toBe("shared-id-001");
    }
  });

  it("uses DEFAULT_CHUNK_SIZE when no chunkSize is specified", () => {
    expect(DEFAULT_CHUNK_SIZE).toBe(524288);

    const header = makeResponseHeader({ id: "default-chunk" });
    // Body just over 512KB
    const body = makeBody(DEFAULT_CHUNK_SIZE + 100);

    const { frames } = encodeChunked(header, body);

    // Should produce 2 frames: initial (512KB) + final (100 bytes)
    expect(frames).toHaveLength(2);
  });
});

describe("reassembleChunks", () => {
  it("reassembling all chunks produces the original body exactly", () => {
    const chunkSize = 256;
    const body = makeBody(700);
    const header = makeResponseHeader({ id: "reassemble-001" });

    const { frames } = encodeChunked(header, body, chunkSize);

    // Extract body from each frame
    const chunks: Uint8Array[] = frames.map((f) => decodeFrame(f).body);

    const reassembled = reassembleChunks(chunks);
    expect(reassembled).toEqual(body);
  });

  it("reassembling a single chunk returns the original body", () => {
    const body = makeBody(100);
    const reassembled = reassembleChunks([body]);
    expect(reassembled).toEqual(body);
  });

  it("reassembling zero chunks returns empty Uint8Array", () => {
    const reassembled = reassembleChunks([]);
    expect(reassembled.byteLength).toBe(0);
  });

  it("reassembles a large body (multiple of chunk size) correctly", () => {
    const chunkSize = 200;
    const body = makeBody(600); // exactly 3 chunks
    const header = makeResponseHeader({ id: "exact-multi" });

    const { frames } = encodeChunked(header, body, chunkSize);
    expect(frames).toHaveLength(3);

    const chunks = frames.map((f) => decodeFrame(f).body);
    const reassembled = reassembleChunks(chunks);
    expect(reassembled).toEqual(body);
  });

  it("round-trips a body through chunking and reassembly with binary data", () => {
    const chunkSize = 128;
    // Generate a body with all possible byte values
    const body = new Uint8Array(256);
    for (let i = 0; i < 256; i++) {
      body[i] = i;
    }

    const header = makeRequestHeader({ id: "binary-roundtrip" });
    const { frames } = encodeChunked(header, body, chunkSize);

    const chunks = frames.map((f) => decodeFrame(f).body);
    const reassembled = reassembleChunks(chunks);

    expect(reassembled).toEqual(body);
  });
});
