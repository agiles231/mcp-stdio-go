package transport

import (
	"encoding/json"
	"io"
	"sync"
)

// Stdio is a JSON-RPC transport that frames messages as discrete JSON
// values over an io.Reader and io.Writer. It is intentionally ignorant
// of JSON-RPC and MCP semantics: it moves opaque JSON in and out, and
// nothing else in the codebase may touch the underlying stdout
type Stdio struct {
	r   io.Reader
	dec *json.Decoder
	w   io.Writer
	mu  sync.Mutex
}

func New(r io.Reader, w io.Writer) *Stdio {
	return &Stdio{
		r:   r,
		dec: json.NewDecoder(r),
		w:   w,
	}
}

func (s *Stdio) Close() error {
	if c, ok := s.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// Read returns the raw bytes of the next JSON value from the input
// Returns io.EOF when the stream closes cleanly.
func (s *Stdio) Read() (json.RawMessage, error) {
	var raw json.RawMessage
	if err := s.dec.Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// Write marshals v and emits it as a single newline-terminated message.
// Safe for concurrent use.
func (s *Stdio) Write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	// TODO: writes are intentionally unbuffered for low request/response
	// latency. Revisit buffered+flushed writes only if a high-throughput,
	// non-request/response use case emerges where syscall amortization
	// would actually matter.
	_, err = s.w.Write(b)
	return err
}
