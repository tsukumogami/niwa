package cli

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/tsukumogami/niwa/internal/removal"
)

// maxScrubLineBytes bounds how much of one line a scrubLineWriter holds
// while it waits for the line's end. The rest of a longer line is dropped:
// the line is cut to its limit when it is printed anyway.
const maxScrubLineBytes = 4096

// scrubLineWriter passes what is written to it on to w a line at a time,
// each line through removal.ScrubEvidence and cut to max characters. It is
// for output niwa hands on rather than writes itself (the on-reap command's
// standard output, the warnings a teardown reports), which can carry
// whatever a unit holds. Flush prints a last line that has no newline.
type scrubLineWriter struct {
	mu  sync.Mutex
	w   io.Writer
	max int
	buf []byte
}

func newScrubLineWriter(w io.Writer, max int) *scrubLineWriter {
	return &scrubLineWriter{w: w, max: max}
}

// Write takes p whole, printing each line it ends. It always reports p as
// written, so a command writing into it never sees a short write.
func (s *scrubLineWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	var err error
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			s.hold(p)
			break
		}
		s.hold(p[:i])
		if e := s.emit(); e != nil && err == nil {
			err = e
		}
		p = p[i+1:]
	}
	return n, err
}

// Flush prints the line held so far, if any.
func (s *scrubLineWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return nil
	}
	return s.emit()
}

// hold keeps p as part of the current line, up to maxScrubLineBytes.
func (s *scrubLineWriter) hold(p []byte) {
	if room := maxScrubLineBytes - len(s.buf); len(p) > room {
		p = p[:room]
	}
	s.buf = append(s.buf, p...)
}

// emit prints the current line, scrubbed, and starts the next.
func (s *scrubLineWriter) emit() error {
	line := string(s.buf)
	s.buf = s.buf[:0]
	_, err := fmt.Fprintln(s.w, removal.ScrubEvidence(line, s.max))
	return err
}
