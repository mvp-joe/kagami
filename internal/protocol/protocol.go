// Package protocol defines the wire protocol for WebSocket binary
// frames. Mirrors the TypeScript implementation in
// packages/kagami/src/protocol.ts. Frame format:
// [4-byte header length][JSON header][raw body bytes].
// Handles serialization, deserialization, and chunking for bodies
// exceeding the 1 MiB frame limit.
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// ChunkSize is the default maximum body size per frame (512 KB).
const ChunkSize = 524288

// Known message types for validation.
var knownTypes = map[string]bool{
	"http_request":    true,
	"http_response":   true,
	"http_body_chunk": true,
	"ping":            true,
	"pong":            true,
	"error":           true,
}

// EncodeFrame serializes a JSON-marshalable header and optional body
// into a binary wire frame: [4-byte header length (uint32 BE)][JSON header][body].
func EncodeFrame(header any, body []byte) ([]byte, error) {
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal header: %w", err)
	}

	headerLen := len(headerJSON)
	frame := make([]byte, 4+headerLen+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(headerLen))
	copy(frame[4:4+headerLen], headerJSON)
	copy(frame[4+headerLen:], body)
	return frame, nil
}

// DecodeFrame splits a binary wire frame into its JSON header bytes and
// raw body bytes. Returns an error if the frame is too short or the
// header length exceeds the frame.
func DecodeFrame(data []byte) (Frame, error) {
	if len(data) < 4 {
		return Frame{}, errors.New("frame too short: missing header length")
	}

	headerLen := binary.BigEndian.Uint32(data[:4])
	if int(headerLen) > len(data)-4 {
		return Frame{}, fmt.Errorf("header length %d exceeds remaining frame size %d", headerLen, len(data)-4)
	}

	header := data[4 : 4+headerLen]
	var body []byte
	if remaining := data[4+headerLen:]; len(remaining) > 0 {
		body = remaining
	}

	return Frame{Header: header, Body: body}, nil
}

// ParseHeaderType extracts the "type" field from raw JSON header bytes
// and validates it against known message types.
func ParseHeaderType(header []byte) (string, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(header, &envelope); err != nil {
		return "", fmt.Errorf("unmarshal header type: %w", err)
	}
	if envelope.Type == "" {
		return "", errors.New("missing type field in header")
	}
	if !knownTypes[envelope.Type] {
		return "", fmt.Errorf("unknown message type: %q", envelope.Type)
	}
	return envelope.Type, nil
}

// ChunkBody splits a large body into multiple encoded wire frames ready
// for WebSocket transmission. The first frame carries the original header
// (with Chunked set to true) and the first body segment. Subsequent
// frames use HttpBodyChunkHeader with the same message ID. The last
// chunk has Final set to true.
//
// If the body fits within chunkSize, a single encoded frame is returned
// with the header unmodified (Chunked remains false/omitted).
//
// The header must be an *HttpRequestHeader or *HttpResponseHeader.
// The caller's header struct is not mutated.
func ChunkBody(header any, body []byte, chunkSize int) ([][]byte, error) {
	if chunkSize <= 0 {
		return nil, errors.New("chunk size must be positive")
	}

	if len(body) <= chunkSize {
		encoded, err := EncodeFrame(header, body)
		if err != nil {
			return nil, err
		}
		return [][]byte{encoded}, nil
	}

	// Copy the header and set Chunked flag, extract the message ID.
	var (
		chunkedHeader any
		msgID         string
	)
	switch h := header.(type) {
	case *HttpRequestHeader:
		cp := *h
		cp.Chunked = true
		chunkedHeader = &cp
		msgID = h.ID
	case *HttpResponseHeader:
		cp := *h
		cp.Chunked = true
		chunkedHeader = &cp
		msgID = h.ID
	default:
		return nil, fmt.Errorf("unsupported header type for chunking: %T", header)
	}

	var frames [][]byte

	// First frame: original header (with chunked) + first chunk.
	firstChunk := body[:chunkSize]
	encoded, err := EncodeFrame(chunkedHeader, firstChunk)
	if err != nil {
		return nil, err
	}
	frames = append(frames, encoded)

	// Continuation chunks.
	remaining := body[chunkSize:]
	for len(remaining) > 0 {
		end := min(chunkSize, len(remaining))
		chunk := remaining[:end]
		remaining = remaining[end:]

		chunkHeader := &HttpBodyChunkHeader{
			MessageHeader: MessageHeader{
				Type: "http_body_chunk",
				ID:   msgID,
			},
			Final: len(remaining) == 0,
		}

		encoded, err := EncodeFrame(chunkHeader, chunk)
		if err != nil {
			return nil, err
		}
		frames = append(frames, encoded)
	}

	return frames, nil
}

// ReassembleChunks concatenates body byte slices into the original
// complete body. The receiver decodes each wire frame and passes
// the body portions directly.
func ReassembleChunks(chunks [][]byte) []byte {
	size := 0
	for _, c := range chunks {
		size += len(c)
	}
	result := make([]byte, 0, size)
	for _, c := range chunks {
		result = append(result, c...)
	}
	return result
}
