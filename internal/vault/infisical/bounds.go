package infisical

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Deadlines for the calls that sit on the provisioning path. Nothing
// on that path had a bound before, so a CLI that waited on a prompt
// or a server that accepted the connection and never answered could
// hang a session-start hook indefinitely. Read these only through
// callBound, which applies the test override.
const (
	// exportTimeout bounds one `infisical export` subprocess.
	exportTimeout = 30 * time.Second
	// probeTimeout bounds one session probe (`infisical login status`
	// or the equivalent) run to classify a failed export.
	probeTimeout = 15 * time.Second
	// loginTimeout bounds niwa's own universal-auth login request.
	loginTimeout = 30 * time.Second
)

// Output caps for every subprocess defaultCommander runs. The deadline
// alone bounds how long a CLI can write, not how much it writes in that
// time, so without these a runaway CLI could fill memory before its
// deadline. Variables only so tests can shrink them.
var (
	// maxStdoutBytes caps the captured stdout. Past it, the rest is
	// discarded and Run reports errOutputTooLarge.
	maxStdoutBytes = 32 << 20
	// maxStderrBytes caps the captured stderr. Past it, the rest is
	// discarded silently, along with the line the cap cut (see
	// wholeLines), except for the first server status line, which the
	// buffer keeps looking for past the cap (see keepStatusLine):
	// otherwise a status pushed past the cap would read as no server
	// response and a refusal could be served stale.
	maxStderrBytes = 1 << 20
)

// errOutputTooLarge is the error Run returns, alongside the process's
// real exit code and the truncated stdout, when stdout passed
// maxStdoutBytes.
var errOutputTooLarge = errors.New("infisical: CLI output exceeded the size cap")

// cappedBuffer keeps the first limit bytes written to it and discards
// the rest, recording that it did. Write never fails, so the child
// never sees a broken pipe and exits the way it would have otherwise.
//
// With keepStatusLine set, the buffer also reads every line past the
// cap, retaining none of it, and remembers the first one that is a
// server status line (`Response Code: <n>`). wholeLines appends that
// line, rebuilt from the parsed number, so the classifier still sees
// the server's answer however much the CLI printed before it.
type cappedBuffer struct {
	limit     int
	buf       []byte
	truncated bool

	keepStatusLine bool
	// pending holds the start of the current line past the cap, up to
	// maxStatusLineLen bytes; a longer line can't be a status line and
	// is skipped to its end (skipping).
	pending  []byte
	skipping bool
	// status is the first status past the cap, 0 until one is seen.
	status int
}

// maxStatusLineLen bounds the bytes cappedBuffer holds for one line
// past the cap. A status line is the prefix plus a few digits.
const maxStatusLineLen = 64

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := b.limit - len(b.buf)
	if room >= len(p) {
		b.buf = append(b.buf, p...)
		return n, nil
	}
	if room > 0 {
		b.buf = append(b.buf, p[:room]...)
		p = p[room:]
	}
	if !b.truncated {
		b.truncated = true
		// The line the cap cut continues past it: skip its rest
		// unless the cap fell exactly on a line boundary.
		b.skipping = len(b.buf) > 0 && b.buf[len(b.buf)-1] != '\n'
	}
	if b.keepStatusLine {
		b.scanPastCap(p)
	}
	return n, nil
}

// scanPastCap feeds bytes past the cap through the status-line search.
func (b *cappedBuffer) scanPastCap(p []byte) {
	for len(p) > 0 && b.status == 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if !b.skipping {
			if len(b.pending)+len(chunk) > maxStatusLineLen {
				b.pending, b.skipping = b.pending[:0], true
			} else {
				b.pending = append(b.pending, chunk...)
			}
		}
		if i < 0 {
			return
		}
		if !b.skipping {
			b.status = parseStatusLine(string(b.pending))
		}
		b.pending, b.skipping = b.pending[:0], false
		p = p[i+1:]
	}
}

// wholeLines returns the kept bytes, minus the last line when the cap cut
// it short. A secret the cap split in half wouldn't match the scrubber's
// fragments, so a text stream that gets interpolated into messages
// (stderr) must never end in a cut line. When keepStatusLine found a
// status line past the cap, it follows as one more line.
func (b *cappedBuffer) wholeLines() []byte {
	if !b.truncated {
		return b.buf
	}
	var out []byte
	if i := bytes.LastIndexByte(b.buf, '\n'); i >= 0 {
		out = b.buf[:i+1]
	}
	status := b.status
	if status == 0 && b.keepStatusLine && !b.skipping {
		// The stream ended without a newline after its last line.
		status = parseStatusLine(string(b.pending))
	}
	if status > 0 {
		out = append(out[:len(out):len(out)], fmt.Sprintf("%s%d\n", responseCodePrefix, status)...)
	}
	return out
}

// testTimeoutEnv names the test-only override for every deadline in
// the const block above.
// It exists so functional tests can exercise a timeout without
// waiting 30 seconds per scenario.
const testTimeoutEnv = "NIWA_TEST_VAULT_TIMEOUT"

// minTestTimeout is the floor below which the override is ignored. A
// bound that short would turn every call into a timeout.
const minTestTimeout = 50 * time.Millisecond

// errCallDeadline is the cause attached to the deadline each call
// site derives. Checking for it (rather than for
// context.DeadlineExceeded) keeps a caller's own deadline or
// cancellation from being reported as this call's timeout.
var errCallDeadline = errors.New("infisical: call deadline reached")

// overrideWarning guards the once-per-run warning printed while the
// override is in effect. warningOut is where it goes; tests swap both.
var (
	overrideWarning sync.Once
	warningOut      io.Writer = os.Stderr
)

// callBound returns the deadline to apply to a call whose default is
// def. NIWA_TEST_VAULT_TIMEOUT replaces def only when it parses as a
// Go duration of at least minTestTimeout and shorter than def, so a
// stray setting can shorten a bound but never lengthen one. While the
// override is in effect, one warning goes to stderr per run.
func callBound(def time.Duration) time.Duration {
	raw := os.Getenv(testTimeoutEnv)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < minTestTimeout || d >= def {
		return def
	}
	overrideWarning.Do(func() {
		fmt.Fprintf(warningOut,
			"niwa: warning: %s=%s is shortening vault call deadlines; it is meant for tests only\n",
			testTimeoutEnv, d)
	})
	return d
}

// withCallDeadline derives the context a bounded call runs under. The
// returned context carries errCallDeadline as its cause once the
// bound elapses, which is what callTimedOut looks for.
func withCallDeadline(ctx context.Context, bound time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, bound, errCallDeadline)
}

// callTimedOut reports whether a bounded call ran out of time: either
// the deadline withCallDeadline set on callCtx fired, or the
// subprocess exited but something it left behind kept its output
// pipes open until WaitDelay cut them (exec.ErrWaitDelay). A
// cancellation or deadline inherited from the caller's own context
// is not a timeout of this call.
//
// Call it right after the call returns, before any other branch on
// its result: a timed-out subprocess comes back as a killed process
// with exit code -1, which would otherwise read as a generic failure.
func callTimedOut(callCtx context.Context, err error) bool {
	if errors.Is(context.Cause(callCtx), errCallDeadline) {
		return true
	}
	return errors.Is(err, exec.ErrWaitDelay) && callCtx.Err() == nil
}
