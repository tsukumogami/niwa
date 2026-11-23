package cli

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"

	"github.com/tsukumogami/niwa/internal/removal"
)

// maxScrubLineBytes bounds how much of one line a scrubLineWriter keeps once
// the line is scrubbed. The line is cut to its limit when it is printed
// anyway, so the rest of a longer line is dropped.
const maxScrubLineBytes = 4096

// scrubLineSlack is how far past maxScrubLineBytes a scrubLineWriter holds a
// line as written, so a token that crosses maxScrubLineBytes is scrubbed
// whole before the line is cut there. Together they bound what one line
// costs in memory.
const scrubLineSlack = maxScrubLineBytes

// scrubLineWriter passes what is written to it on to w a line at a time,
// each line through removal.ScrubEvidence and cut to max characters. It is
// for output niwa hands on rather than writes itself (the on-reap command's
// standard output, the warnings a teardown reports), which can carry
// whatever a unit holds. Flush prints a last line that has no newline.
//
// Like ScrubEvidence it scrubs before it cuts: cutting a line first could
// leave the front of a token that crosses the cut, too short for any pattern
// to know it.
type scrubLineWriter struct {
	mu  sync.Mutex
	w   io.Writer
	max int
	buf []byte
	// full is set once the line passed what the writer holds: buf is then
	// the line scrubbed and cut to maxScrubLineBytes, and the rest of the
	// line is dropped until its newline.
	full bool
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
	if len(s.buf) == 0 && !s.full {
		return nil
	}
	return s.emit()
}

// hold keeps p as part of the current line, as written, up to
// maxScrubLineBytes plus scrubLineSlack. When the line goes past that, the
// held text is scrubbed and then cut (see settle), and the rest of the line
// is dropped.
func (s *scrubLineWriter) hold(p []byte) {
	if s.full {
		return
	}
	room := maxScrubLineBytes + scrubLineSlack - len(s.buf)
	if len(p) <= room {
		s.buf = append(s.buf, p...)
		return
	}
	s.buf = append(s.buf, p[:room]...)
	s.settle()
}

// settle scrubs the held text of a line that goes on past what the writer
// holds, then cuts the scrubbed result to maxScrubLineBytes.
//
// The held text ends where the writer stopped taking the line, which can be
// inside a word, and the text before it can collapse to almost nothing
// (white space, escapes) once it is scrubbed. The word cut there can be the
// front of a secret no pattern knows yet: a token too short for its shape,
// or a URL's userinfo with no @ after it. So the scrubbed text is cut back
// to its last space and the word after it is dropped whole; with no space,
// all of the held text goes. Only a word of maxScrubLineBytes or more made
// of token characters alone is kept, since every pattern knows a token by
// then and the cut leaves only the word's start.
func (s *scrubLineWriter) settle() {
	clean := removal.ScrubEvidence(string(s.buf), math.MaxInt)
	start := strings.LastIndexByte(clean, ' ') + 1
	if word := clean[start:]; len(word) < maxScrubLineBytes || tokenRunStart(word) > 0 {
		clean = strings.TrimSuffix(clean[:start], " ")
	}
	s.buf = append(s.buf[:0], removal.CutText(clean, maxScrubLineBytes)...)
	s.full = true
}

// tokenRunStart is where the run of token characters that s ends with
// starts: the letters, digits and +=_- that base64 and the token shapes
// removal.ScrubEvidence knows are written in.
func tokenRunStart(s string) int {
	i := len(s)
	for i > 0 && tokenRunByte(s[i-1]) {
		i--
	}
	return i
}

// tokenRunByte reports whether c is a letter, a digit or one of +=_-.
func tokenRunByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	return c == '+' || c == '=' || c == '_' || c == '-'
}

// emit prints the current line, scrubbed, and starts the next.
func (s *scrubLineWriter) emit() error {
	line := string(s.buf)
	s.buf = s.buf[:0]
	s.full = false
	_, err := fmt.Fprintln(s.w, removal.ScrubEvidence(line, s.max))
	return err
}
