package relay

import (
	"bytes"
	"io"
	"regexp"
	"sync"
)

// urlQuery matches an http(s) or hls:// URL followed by a query string.
var urlQuery = regexp.MustCompile(`((?:hls|https?)://[^\s?#'"]+)\?[^\s'"]*`)

// maxPendingLine bounds the buffer for a line that never ends.
const maxPendingLine = 64 << 10

// redactingWriter forwards Streamlink's stderr line by line with URL query
// strings removed: resolved HLS URLs carry short-lived tokens that must not
// reach container logs. Scheme, host and path stay for diagnosis.
type redactingWriter struct {
	mu      sync.Mutex
	out     io.Writer
	pending []byte
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		w.emit(w.pending[:i+1])
		w.pending = w.pending[i+1:]
	}
	if len(w.pending) > maxPendingLine {
		w.emit(w.pending)
		w.pending = nil
	}
	// Report the full length: a short write would make exec's copier abort
	// and the child could block on a full stderr pipe.
	return len(p), nil
}

// Flush writes out a final line that had no trailing newline.
func (w *redactingWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.emit(w.pending)
		w.pending = nil
	}
}

func (w *redactingWriter) emit(line []byte) {
	_, _ = w.out.Write(urlQuery.ReplaceAll(line, []byte("$1?<redacted>")))
}
