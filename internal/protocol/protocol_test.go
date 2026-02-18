package protocol

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestEncodeFrame_HttpRequest(t *testing.T) {
	t.Parallel()

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-1"},
		Method:        "POST",
		Host:          "api.my-homelab.kagami.dev",
		Path:          "/users",
		Query:         "page=2&limit=10",
		Headers:       map[string][]string{"Content-Type": {"application/json"}},
	}
	body := []byte(`{"name":"alice"}`)

	frame, err := EncodeFrame(header, body)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	// Verify frame structure: 4-byte length prefix + JSON header + body.
	if len(frame) < 4 {
		t.Fatal("frame shorter than 4 bytes")
	}

	headerLen := binary.BigEndian.Uint32(frame[:4])
	headerJSON := frame[4 : 4+headerLen]
	bodyBytes := frame[4+headerLen:]

	// Header length matches actual JSON bytes.
	var decoded HttpRequestHeader
	if err := json.Unmarshal(headerJSON, &decoded); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if decoded.Type != "http_request" {
		t.Errorf("type = %q, want %q", decoded.Type, "http_request")
	}
	if decoded.ID != "req-1" {
		t.Errorf("id = %q, want %q", decoded.ID, "req-1")
	}
	if decoded.Method != "POST" {
		t.Errorf("method = %q, want %q", decoded.Method, "POST")
	}
	if decoded.Host != "api.my-homelab.kagami.dev" {
		t.Errorf("host = %q, want %q", decoded.Host, "api.my-homelab.kagami.dev")
	}
	if decoded.Path != "/users" {
		t.Errorf("path = %q, want %q", decoded.Path, "/users")
	}
	if decoded.Query != "page=2&limit=10" {
		t.Errorf("query = %q, want %q", decoded.Query, "page=2&limit=10")
	}

	// Body preserved.
	if !bytes.Equal(bodyBytes, body) {
		t.Errorf("body = %q, want %q", bodyBytes, body)
	}
}

func TestDecodeFrame_Valid(t *testing.T) {
	t.Parallel()

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-2"},
		Method:        "GET",
		Host:          "test.kagami.dev",
		Path:          "/health",
		Query:         "",
		Headers:       map[string][]string{},
	}
	body := []byte("hello world")

	encoded, err := EncodeFrame(header, body)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	f, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}

	var decoded HttpRequestHeader
	if err := json.Unmarshal(f.Header, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != "http_request" {
		t.Errorf("type = %q, want %q", decoded.Type, "http_request")
	}
	if decoded.ID != "req-2" {
		t.Errorf("id = %q, want %q", decoded.ID, "req-2")
	}
	if !bytes.Equal(f.Body, body) {
		t.Errorf("body = %q, want %q", f.Body, body)
	}
}

func TestDecodeFrame_UnknownType(t *testing.T) {
	t.Parallel()

	headerJSON := []byte(`{"type":"unknown_msg","id":"x"}`)
	frame := make([]byte, 4+len(headerJSON))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(headerJSON)))
	copy(frame[4:], headerJSON)

	f, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("DecodeFrame should succeed; parsing type is separate: %v", err)
	}

	_, err = ParseHeaderType(f.Header)
	if err == nil {
		t.Fatal("ParseHeaderType should return error for unknown type")
	}
}

func TestRoundTrip_BinaryBody(t *testing.T) {
	t.Parallel()

	// Generate random binary data.
	original := make([]byte, 1024)
	if _, err := rand.Read(original); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}

	header := &HttpResponseHeader{
		MessageHeader: MessageHeader{Type: "http_response", ID: "resp-1"},
		Status:        200,
		Headers:       map[string][]string{"Content-Type": {"application/octet-stream"}},
	}

	encoded, err := EncodeFrame(header, original)
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	f, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}

	if !bytes.Equal(f.Body, original) {
		t.Error("round-tripped binary body does not match original")
	}
}

func TestControlMessages_NoBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header any
	}{
		{
			name:   "ping",
			header: &PingHeader{MessageHeader: MessageHeader{Type: "ping", ID: "p-1"}},
		},
		{
			name:   "pong",
			header: &PongHeader{MessageHeader: MessageHeader{Type: "pong", ID: "p-1"}},
		},
		{
			name: "error",
			header: &ErrorHeader{
				MessageHeader: MessageHeader{Type: "error", ID: "e-1"},
				Code:          "timeout",
				Message:       "request timed out",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := EncodeFrame(tc.header, nil)
			if err != nil {
				t.Fatalf("EncodeFrame: %v", err)
			}

			f, err := DecodeFrame(encoded)
			if err != nil {
				t.Fatalf("DecodeFrame: %v", err)
			}

			if f.Body != nil {
				t.Errorf("expected nil body for control message, got %d bytes", len(f.Body))
			}

			msgType, err := ParseHeaderType(f.Header)
			if err != nil {
				t.Fatalf("ParseHeaderType: %v", err)
			}
			if msgType != tc.name {
				t.Errorf("type = %q, want %q", msgType, tc.name)
			}
		})
	}
}

func TestDecodeFrame_ZeroLengthBody(t *testing.T) {
	t.Parallel()

	header := &HttpResponseHeader{
		MessageHeader: MessageHeader{Type: "http_response", ID: "resp-empty"},
		Status:        204,
		Headers:       map[string][]string{},
	}

	encoded, err := EncodeFrame(header, []byte{})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	f, err := DecodeFrame(encoded)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}

	// Zero-length body: remaining bytes after header are empty.
	if f.Body != nil {
		t.Errorf("expected nil body for zero-length body, got %d bytes", len(f.Body))
	}
}

func TestHeaderLength_AccurateSize(t *testing.T) {
	t.Parallel()

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-len"},
		Method:        "GET",
		Host:          "test.kagami.dev",
		Path:          "/",
		Query:         "",
		Headers:       map[string][]string{},
	}

	encoded, err := EncodeFrame(header, []byte("body"))
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}

	headerLen := binary.BigEndian.Uint32(encoded[:4])

	// Marshal independently to verify.
	expectedJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	if int(headerLen) != len(expectedJSON) {
		t.Errorf("header length = %d, want %d (actual JSON bytes)", headerLen, len(expectedJSON))
	}
}

func TestDecodeFrame_TooShort(t *testing.T) {
	t.Parallel()

	_, err := DecodeFrame([]byte{0, 0})
	if err == nil {
		t.Fatal("expected error for frame shorter than 4 bytes")
	}
}

func TestDecodeFrame_HeaderLenExceedsFrame(t *testing.T) {
	t.Parallel()

	// Header length says 100 but frame only has 4 bytes total (0 remaining).
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, 100)

	_, err := DecodeFrame(frame)
	if err == nil {
		t.Fatal("expected error for header length exceeding frame")
	}
}

// --- Chunking tests ---

// decodeWireFrames is a test helper that decodes encoded wire frames
// returned by ChunkBody into Frame structs for inspection.
func decodeWireFrames(t *testing.T, wireFrames [][]byte) []Frame {
	t.Helper()
	frames := make([]Frame, len(wireFrames))
	for i, wf := range wireFrames {
		f, err := DecodeFrame(wf)
		if err != nil {
			t.Fatalf("DecodeFrame on wire frame %d: %v", i, err)
		}
		frames[i] = f
	}
	return frames
}

// extractBodies is a test helper that decodes wire frames and returns
// only the body slices, for use with ReassembleChunks.
func extractBodies(t *testing.T, wireFrames [][]byte) [][]byte {
	t.Helper()
	bodies := make([][]byte, len(wireFrames))
	for i, wf := range wireFrames {
		f, err := DecodeFrame(wf)
		if err != nil {
			t.Fatalf("DecodeFrame on wire frame %d: %v", i, err)
		}
		bodies[i] = f.Body
	}
	return bodies
}

func TestChunkBody_BelowThreshold(t *testing.T) {
	t.Parallel()

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-small"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	body := make([]byte, 100) // well under chunk size

	wireFrames, err := ChunkBody(header, body, ChunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	if len(wireFrames) != 1 {
		t.Fatalf("expected 1 frame, got %d", len(wireFrames))
	}

	frames := decodeWireFrames(t, wireFrames)

	// Verify chunked field is NOT set (omitted from JSON).
	var decoded HttpRequestHeader
	if err := json.Unmarshal(frames[0].Header, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Chunked {
		t.Error("expected chunked to be false/omitted for small body")
	}

	if !bytes.Equal(frames[0].Body, body) {
		t.Error("body mismatch in single frame")
	}
}

func TestChunkBody_LargeBody_CorrectChunkCount(t *testing.T) {
	t.Parallel()

	chunkSize := 100
	bodySize := 350 // 100 + 100 + 100 + 50 = 4 chunks total
	body := make([]byte, bodySize)
	for i := range body {
		body[i] = byte(i % 256)
	}

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-big"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	// Expected: ceil(350/100) = 4 frames (1 initial + 3 continuation).
	expectedFrames := 4
	if len(wireFrames) != expectedFrames {
		t.Fatalf("expected %d frames, got %d", expectedFrames, len(wireFrames))
	}
}

func TestChunkBody_FirstFrame_ChunkedTrue(t *testing.T) {
	t.Parallel()

	chunkSize := 100
	body := make([]byte, 250)

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-chunk"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	frames := decodeWireFrames(t, wireFrames)

	// First frame should have chunked:true and be an http_request.
	var firstHeader HttpRequestHeader
	if err := json.Unmarshal(frames[0].Header, &firstHeader); err != nil {
		t.Fatalf("unmarshal first frame header: %v", err)
	}
	if !firstHeader.Chunked {
		t.Error("first frame should have chunked=true")
	}
	if firstHeader.Type != "http_request" {
		t.Errorf("first frame type = %q, want %q", firstHeader.Type, "http_request")
	}
	if len(frames[0].Body) != chunkSize {
		t.Errorf("first frame body = %d bytes, want %d", len(frames[0].Body), chunkSize)
	}
}

func TestChunkBody_ContinuationFrames(t *testing.T) {
	t.Parallel()

	chunkSize := 100
	body := make([]byte, 350)

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-cont"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	frames := decodeWireFrames(t, wireFrames)

	// frames[1], [2], [3] are continuation chunks.
	for i := 1; i < len(frames); i++ {
		var chunkHdr HttpBodyChunkHeader
		if err := json.Unmarshal(frames[i].Header, &chunkHdr); err != nil {
			t.Fatalf("unmarshal chunk %d header: %v", i, err)
		}
		if chunkHdr.Type != "http_body_chunk" {
			t.Errorf("chunk %d type = %q, want %q", i, chunkHdr.Type, "http_body_chunk")
		}
		if chunkHdr.ID != "req-cont" {
			t.Errorf("chunk %d id = %q, want %q", i, chunkHdr.ID, "req-cont")
		}

		isFinal := i == len(frames)-1
		if chunkHdr.Final != isFinal {
			t.Errorf("chunk %d final = %v, want %v", i, chunkHdr.Final, isFinal)
		}
	}
}

func TestChunkBody_Reassemble(t *testing.T) {
	t.Parallel()

	chunkSize := 100
	body := make([]byte, 350)
	for i := range body {
		body[i] = byte(i % 256)
	}

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-reasm"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	bodies := extractBodies(t, wireFrames)
	reassembled := ReassembleChunks(bodies)
	if !bytes.Equal(reassembled, body) {
		t.Error("reassembled body does not match original")
	}
}

func TestChunkBody_ExactMultiple(t *testing.T) {
	t.Parallel()

	chunkSize := 100
	body := make([]byte, 300) // exactly 3 chunks
	for i := range body {
		body[i] = byte(i % 256)
	}

	header := &HttpResponseHeader{
		MessageHeader: MessageHeader{Type: "http_response", ID: "resp-exact"},
		Status:        200,
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	if len(wireFrames) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(wireFrames))
	}

	bodies := extractBodies(t, wireFrames)
	reassembled := ReassembleChunks(bodies)
	if !bytes.Equal(reassembled, body) {
		t.Error("reassembled body does not match original")
	}
}

func TestChunkBody_ResponseHeader(t *testing.T) {
	t.Parallel()

	chunkSize := 50
	body := make([]byte, 120)

	header := &HttpResponseHeader{
		MessageHeader: MessageHeader{Type: "http_response", ID: "resp-chunk"},
		Status:        200,
		Headers:       map[string][]string{"Content-Type": {"text/plain"}},
	}

	wireFrames, err := ChunkBody(header, body, chunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	frames := decodeWireFrames(t, wireFrames)

	// First frame is http_response with chunked:true.
	var first HttpResponseHeader
	if err := json.Unmarshal(frames[0].Header, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !first.Chunked {
		t.Error("first frame should have chunked=true")
	}
	if first.Type != "http_response" {
		t.Errorf("first frame type = %q, want %q", first.Type, "http_response")
	}
	if first.Status != 200 {
		t.Errorf("status = %d, want 200", first.Status)
	}
}

func TestChunkBody_DoesNotMutateCallerHeader(t *testing.T) {
	t.Parallel()

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-nomut"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/upload",
		Query:         "",
		Headers:       map[string][]string{},
	}

	body := make([]byte, 250)
	_, err := ChunkBody(header, body, 100)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	if header.Chunked {
		t.Error("ChunkBody mutated caller's header: Chunked should still be false")
	}
}

func TestParseHeaderType_ValidTypes(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"http_request", "http_response", "http_body_chunk", "ping", "pong", "error"} {
		hdr, _ := json.Marshal(MessageHeader{Type: typ, ID: "x"})
		got, err := ParseHeaderType(hdr)
		if err != nil {
			t.Errorf("ParseHeaderType(%q): %v", typ, err)
		}
		if got != typ {
			t.Errorf("ParseHeaderType(%q) = %q", typ, got)
		}
	}
}

func TestParseHeaderType_Unknown(t *testing.T) {
	t.Parallel()

	hdr := []byte(`{"type":"foo_bar","id":"x"}`)
	_, err := ParseHeaderType(hdr)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestParseHeaderType_MissingType(t *testing.T) {
	t.Parallel()

	hdr := []byte(`{"id":"x"}`)
	_, err := ParseHeaderType(hdr)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestParseHeaderType_InvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := ParseHeaderType([]byte(`{broken`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestChunkBody_WithDefaultChunkSize(t *testing.T) {
	t.Parallel()

	// Create a body larger than ChunkSize (512KB).
	body := make([]byte, ChunkSize+1000)
	for i := range body {
		body[i] = byte(i % 256)
	}

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-default"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/big",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, ChunkSize)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	if len(wireFrames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(wireFrames))
	}

	bodies := extractBodies(t, wireFrames)
	reassembled := ReassembleChunks(bodies)
	if !bytes.Equal(reassembled, body) {
		t.Error("reassembled body does not match original")
	}
}

func TestChunkBody_BoundaryExact(t *testing.T) {
	t.Parallel()

	// Body exactly at chunk size -- should produce a single frame, no chunking.
	body := make([]byte, 100)

	header := &HttpRequestHeader{
		MessageHeader: MessageHeader{Type: "http_request", ID: "req-boundary"},
		Method:        "POST",
		Host:          "test.kagami.dev",
		Path:          "/",
		Query:         "",
		Headers:       map[string][]string{},
	}

	wireFrames, err := ChunkBody(header, body, 100)
	if err != nil {
		t.Fatalf("ChunkBody: %v", err)
	}

	if len(wireFrames) != 1 {
		t.Fatalf("expected 1 frame for body == chunkSize, got %d", len(wireFrames))
	}

	frames := decodeWireFrames(t, wireFrames)
	var decoded HttpRequestHeader
	if err := json.Unmarshal(frames[0].Header, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Chunked {
		t.Error("body exactly at chunk size should not be chunked")
	}
}
