package workspace

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// Reporter writes progress and log output to an underlying writer. It
// supports two modes:
//
//   - TTY mode (isTTY == true): Status calls write a carriage-return-
//     rewritten status line with no newline; Log/Warn clear that line
//     before appending a permanent line.
//   - Non-TTY mode (isTTY == false): Status is a no-op; Log/Warn append
//     lines directly with no ANSI sequences.
type Reporter struct {
	w          io.Writer
	isTTY      bool
	needsClear bool
}

// NewReporter constructs a Reporter whose TTY mode is detected from w.
// If w is an *os.File, term.IsTerminal is called on its file descriptor.
// Otherwise isTTY defaults to false.
func NewReporter(w io.Writer) *Reporter {
	isTTY := false
	if f, ok := w.(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
	}
	return &Reporter{w: w, isTTY: isTTY}
}

// NewReporterWithTTY constructs a Reporter with the given TTY flag,
// bypassing auto-detection. Used when the caller already knows the
// terminal state (e.g., after checking --no-progress or piping).
func NewReporterWithTTY(w io.Writer, isTTY bool) *Reporter {
	return &Reporter{w: w, isTTY: isTTY}
}

// Status writes a transient status line on TTY output. The line is
// rewritten on the same row using a carriage return and ANSI erase-to-
// end-of-line sequence so it does not scroll. On non-TTY output this
// method is a no-op.
func (r *Reporter) Status(msg string) {
	if !r.isTTY {
		return
	}
	fmt.Fprintf(r.w, "\r\033[K%s", msg)
	r.needsClear = true
}

// Log writes a permanent log line. On TTY output, if a Status line is
// currently displayed it is cleared first. The format string follows
// fmt.Sprintf conventions; a newline is always appended.
func (r *Reporter) Log(format string, a ...any) {
	if r.isTTY && r.needsClear {
		fmt.Fprint(r.w, "\r\033[K")
		r.needsClear = false
	}
	fmt.Fprintf(r.w, format+"\n", a...)
}

// Warn writes a permanent warning line. It behaves like Log but
// prepends "warning: " to the format string.
func (r *Reporter) Warn(format string, a ...any) {
	r.Log("warning: "+format, a...)
}

// Writer returns an io.Writer whose Write calls are routed through Log.
// Each Write invocation is treated as a single log message (trailing
// newlines in the input are stripped to avoid double-newlines).
func (r *Reporter) Writer() io.Writer {
	return &logWriter{r: r}
}

// logWriter adapts io.Writer calls to Reporter.Log.
type logWriter struct {
	r *Reporter
}

func (lw *logWriter) Write(p []byte) (int, error) {
	// Strip a trailing newline to avoid double-newlines since Log appends one.
	s := string(p)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s != "" {
		lw.r.Log("%s", s)
	}
	return len(p), nil
}
