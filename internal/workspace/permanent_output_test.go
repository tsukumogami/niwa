package workspace

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// spinnerDelimiter is the byte sequence the Reporter writes ahead of every
// spinner frame (doTick) and again when tearing the spinner down
// (stopSpinner): carriage return plus the "erase to end of line" CSI.
const spinnerDelimiter = "\r\x1b[K"

// permanentOutput reduces a Reporter's raw byte stream to the text still on
// the operator's screen once the command has finished — the durable output.
//
// This helper exists because the obvious assertion does not work. Off a TTY,
// Status is a no-op and the raw stream contains only permanent lines, so
// strings.Contains is honest. On a TTY it is not: Status renders each line as
// a spinner frame, and the frame that happens to be live when the next Log
// call arrives has already been written to the stream even though stopSpinner
// immediately erases it. So on a TTY, raw output from a failing script
// contains one arbitrary line of that script's output — which line depends on
// goroutine scheduling — and a naive strings.Contains assertion passes against
// behavior that shows the operator nothing.
//
// The reduction rule follows from how the Reporter writes: a permanent line is
// always newline-terminated, a spinner frame never is, and every spinner frame
// is preceded by spinnerDelimiter. So split on the delimiter and, within each
// segment, keep everything up to and including the last newline; the
// unterminated tail of a segment is a spinner frame (or a partial write) and
// is dropped.
func permanentOutput(raw string) string {
	var b strings.Builder
	for _, seg := range strings.Split(raw, spinnerDelimiter) {
		if i := strings.LastIndex(seg, "\n"); i >= 0 {
			b.WriteString(seg[:i+1])
		}
	}
	return b.String()
}

// syncBuffer is a mutex-guarded bytes.Buffer. Reporter writes from its spinner
// goroutine as well as from the caller's goroutine, so tests that read the
// buffer use this rather than a bare bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestPermanentOutput verifies the reduction helper itself: durable Log lines
// survive, transient spinner frames do not.
func TestPermanentOutput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "non-TTY stream is all permanent",
			raw:  "created ws (1 repo)\nwarning: something\n",
			want: "created ws (1 repo)\nwarning: something\n",
		},
		{
			name: "spinner frames are dropped",
			raw:  spinnerDelimiter + "⠋ cloning" + spinnerDelimiter + "⠙ cloning" + spinnerDelimiter,
			want: "",
		},
		{
			name: "log after spinner teardown survives",
			raw:  spinnerDelimiter + "⠋ cloning" + spinnerDelimiter + "cloned myapp\n",
			want: "cloned myapp\n",
		},
		{
			name: "consecutive logs in one segment all survive",
			raw:  spinnerDelimiter + "first\nsecond\n",
			want: "first\nsecond\n",
		},
		{
			name: "unterminated trailing frame is dropped but earlier line kept",
			raw:  "done\n" + spinnerDelimiter + "⠹ working",
			want: "done\n",
		},
		{
			name: "empty",
			raw:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permanentOutput(tt.raw); got != tt.want {
				t.Errorf("permanentOutput(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
