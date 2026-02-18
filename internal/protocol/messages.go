package protocol

// MessageHeader is the JSON header envelope for all wire messages.
type MessageHeader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// HttpRequestHeader is the JSON header for an HTTP request from DO to agent.
// The raw HTTP body follows as bytes after the JSON header in the frame.
type HttpRequestHeader struct {
	MessageHeader
	Method  string              `json:"method"`
	Host    string              `json:"host"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Headers map[string][]string `json:"headers"`
	Chunked bool                `json:"chunked,omitempty"`
}

// HttpResponseHeader is the JSON header for an HTTP response from agent to DO.
// The raw HTTP body follows as bytes after the JSON header in the frame.
type HttpResponseHeader struct {
	MessageHeader
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Chunked bool                `json:"chunked,omitempty"`
}

// HttpBodyChunkHeader is a continuation chunk for a chunked request/response.
type HttpBodyChunkHeader struct {
	MessageHeader
	Final bool `json:"final"`
}

// PingHeader is a keepalive from agent to DO. No body.
type PingHeader struct {
	MessageHeader
}

// PongHeader is a keepalive response from DO to agent. No body.
type PongHeader struct {
	MessageHeader
}

// ErrorHeader signals an error. No body.
type ErrorHeader struct {
	MessageHeader
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Frame represents a decoded wire frame: JSON header + raw body bytes.
type Frame struct {
	Header []byte // raw JSON header bytes
	Body   []byte // raw body bytes (may be nil for control messages)
}
