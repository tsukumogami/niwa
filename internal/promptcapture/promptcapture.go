// Package promptcapture reads an interactive multiline prompt from the
// terminal.
//
// It is deliberately not part of internal/tui: every file in that package is a
// copy of tsukumogami/tsuku's under a standing byte-equivalence obligation, and
// this reader is niwa-only.
//
// The package keeps two outputs strictly separate. The payload is the bytes the
// developer submitted, preserved exactly apart from the line-break
// normalization documented on Read. The transcript is what reaches the screen,
// and every byte of it passes through neutralize first, so pasted content can
// never act on the terminal. Nothing that is rendered determines what is sent.
package promptcapture

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrCanceled reports that the developer abandoned the capture. Callers treat
// it as a deliberate user action, matching tui.ErrCanceled's contract: report
// it without dispatching, create nothing, exit non-zero.
var ErrCanceled = errors.New("promptcapture: canceled")

// ErrEndOfInput reports end of input on an empty buffer. End of input on a
// non-empty buffer submits instead, so a developer reaching for the key the
// older workaround taught them does not lose their paste.
var ErrEndOfInput = errors.New("promptcapture: end of input on an empty buffer")

const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"

	// retentionMultiple bounds how far above the ceiling the buffer may grow so
	// the developer can delete down to a submittable size. An append that would
	// exceed it is refused in full rather than truncated: a partially retained
	// log looks complete and is not.
	retentionMultiple = 2

	displayWidth = 100
)

// Read runs the capture against the real terminal, reading standard input and
// rendering to standard error. Standard output is left alone so a caller can
// redirect it.
//
// limit is the largest prompt the caller will accept, in bytes. It is a
// parameter rather than a package constant because the ceiling is derived by the
// caller from the platform's argument limit and the caller's own reserve.
func Read(limit int) (string, error) {
	restore, err := enterRaw()
	if err != nil {
		return "", err
	}
	defer restore()
	return read(stdin(), stderr(), limit)
}

// read is the capture core. Tests drive it with an ordinary reader and writer,
// at any chunk size, without a terminal.
func read(r io.Reader, w io.Writer, limit int) (string, error) {
	c := &capture{
		w:         w,
		limit:     limit,
		retention: limit * retentionMultiple,
	}
	c.banner()

	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; i++ {
			done, text, cerr := c.step(buf[i])
			if done {
				return text, cerr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return c.endOfInput()
			}
			return "", err
		}
	}
}

type capture struct {
	w         io.Writer
	limit     int
	retention int

	buf []byte // the payload

	// Three pieces of state that must survive a read boundary. They are where
	// the correctness risk lives, which is why the tests drive every path
	// through them at chunk size one.
	held         []byte // a partial paste marker, not yet known to be one
	lastWasCR    bool   // a carriage return ended the previous chunk, inside a paste
	pendingBreak bool   // a paste ended mid-line; insert one newline before the next append

	inPaste     bool
	pasteBuf    []byte
	pasteDenied bool // this paste exceeded the retention bound; retain none of it

	overCeiling bool
}

// step consumes one byte. It reports done when the capture has an outcome.
func (c *capture) step(b byte) (done bool, text string, err error) {
	// Marker matching takes precedence over everything, because a marker byte
	// is also a plausible payload byte and only its context decides.
	if len(c.held) > 0 || b == 0x1b {
		c.held = append(c.held, b)
		s := string(c.held)
		switch {
		case s == pasteStart:
			c.held = nil
			c.inPaste = true
			c.lastWasCR = false
			c.pasteBuf = nil
			c.pasteDenied = false
			return false, "", nil
		case s == pasteEnd:
			c.held = nil
			c.closePaste()
			return false, "", nil
		case strings.HasPrefix(pasteStart, s) || strings.HasPrefix(pasteEnd, s):
			return false, "", nil
		}
		// Not a marker after all. Replay the held bytes as ordinary input and
		// fall through to handle nothing further; the current byte is already
		// among them.
		held := c.held
		c.held = nil
		for _, hb := range held {
			if done, text, err := c.ordinary(hb); done {
				return done, text, err
			}
		}
		return false, "", nil
	}
	return c.ordinary(b)
}

func (c *capture) ordinary(b byte) (bool, string, error) {
	if c.inPaste {
		c.appendPasted(b)
		return false, "", nil
	}

	switch b {
	case 0x0d: // Enter submits.
		return c.submit()
	case 0x04: // End of input: submit if there is anything, otherwise finish.
		if len(c.buf) == 0 {
			return true, "", ErrEndOfInput
		}
		return c.submit()
	case 0x03: // Interrupt arrives as a byte because raw mode clears ISIG.
		return true, "", ErrCanceled
	case 0x0a: // Ctrl-J inserts a newline without submitting.
		c.appendTyped('\n')
	case 0x7f, 0x08:
		c.deleteRune()
	case 0x17:
		c.deleteWord()
	case 0x15:
		c.deleteLine()
	default:
		c.appendTyped(b)
	}
	return false, "", nil
}

func (c *capture) submit() (bool, string, error) {
	if c.overCeiling {
		c.refuse()
		return false, "", nil
	}
	return true, string(c.buf), nil
}

func (c *capture) endOfInput() (string, error) {
	if len(c.buf) == 0 {
		return "", ErrEndOfInput
	}
	if c.overCeiling {
		// A stream that ends while the buffer is unsubmittable is not a submit.
		return "", ErrCanceled
	}
	return string(c.buf), nil
}

// appendPasted accumulates a byte inside a paste block, normalizing line breaks
// to a single line feed. Terminals differ in which byte they deliver for a
// pasted break; preserving them exactly would hand the worker a stack trace
// whose lines are separated by carriage returns, which renders as one
// overwritten line.
func (c *capture) appendPasted(b byte) {
	if c.pasteDenied {
		return
	}
	switch b {
	case '\r':
		c.pasteBuf = append(c.pasteBuf, '\n')
		c.lastWasCR = true
	case '\n':
		if c.lastWasCR {
			c.lastWasCR = false
			return
		}
		c.pasteBuf = append(c.pasteBuf, '\n')
	default:
		c.pasteBuf = append(c.pasteBuf, b)
		c.lastWasCR = false
	}
	if len(c.buf)+len(c.pasteBuf) > c.retention {
		c.pasteDenied = true
		c.pasteBuf = nil
	}
}

func (c *capture) closePaste() {
	c.inPaste = false
	c.lastWasCR = false

	if c.pasteDenied {
		c.pasteDenied = false
		c.pasteBuf = nil
		fmt.Fprintf(c.w, "\r\n[input too large to hold; nothing was kept. Limit is %d bytes. Write the detail to a file and reference its path from a shorter prompt.]\r\n", c.limit)
		return
	}

	pasted := c.pasteBuf
	c.pasteBuf = nil
	if len(pasted) == 0 {
		return
	}

	c.flushPendingBreak()
	c.buf = append(c.buf, pasted...)

	// A paste that ended mid-line owes a break to whatever is typed next. Adding
	// it now would give a bare paste a trailing newline it never had, so the
	// obligation is deferred.
	if pasted[len(pasted)-1] != '\n' {
		c.pendingBreak = true
	}

	c.renderPaste(pasted)
	c.checkCeiling()
}

func (c *capture) appendTyped(b byte) {
	if len(c.buf)+1 > c.retention {
		c.refuseUnretained()
		return
	}
	c.flushPendingBreak()
	c.buf = append(c.buf, b)
	fmt.Fprint(c.w, neutralize([]byte{b}))
	if b == '\n' {
		fmt.Fprint(c.w, "\r")
	}
	c.checkCeiling()
}

func (c *capture) flushPendingBreak() {
	if c.pendingBreak {
		c.pendingBreak = false
		c.buf = append(c.buf, '\n')
		fmt.Fprint(c.w, "\r\n")
	}
}

// checkCeiling runs after the append, never before it. Appending nothing would
// be simpler and wrong: the buffer could never exceed the limit, so the
// unsubmittable mark could never be set, deletion could never clear it, and the
// message could not name a size larger than the limit. It would also discard
// exactly what the developer needs to keep.
func (c *capture) checkCeiling() {
	over := len(c.buf) > c.limit
	if over && !c.overCeiling {
		c.overCeiling = true
		c.refuse()
		return
	}
	c.overCeiling = over
}

func (c *capture) refuse() {
	fmt.Fprintf(c.w, "\r\n[%d bytes entered, limit is %d. Delete some, or write the detail to a file and reference its path from a shorter prompt.]\r\n", len(c.buf), c.limit)
}

func (c *capture) refuseUnretained() {
	fmt.Fprintf(c.w, "\r\n[input too large to hold; nothing was kept. Limit is %d bytes. Write the detail to a file and reference its path from a shorter prompt.]\r\n", c.limit)
}

func (c *capture) deleteRune() {
	if len(c.buf) == 0 {
		return
	}
	i := len(c.buf) - 1
	for i > 0 && c.buf[i]&0xc0 == 0x80 {
		i--
	}
	c.buf = c.buf[:i]
	c.redrawAfterDelete()
}

func (c *capture) deleteWord() {
	i := len(c.buf)
	for i > 0 && isSpace(c.buf[i-1]) {
		i--
	}
	for i > 0 && !isSpace(c.buf[i-1]) {
		i--
	}
	c.buf = c.buf[:i]
	c.redrawAfterDelete()
}

func (c *capture) deleteLine() {
	i := len(c.buf)
	if i > 0 && c.buf[i-1] == '\n' {
		i--
	}
	for i > 0 && c.buf[i-1] != '\n' {
		i--
	}
	c.buf = c.buf[:i]
	c.redrawAfterDelete()
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

func (c *capture) redrawAfterDelete() {
	c.pendingBreak = false
	fmt.Fprintf(c.w, "\r\n[%d bytes]\r\n", len(c.buf))
	was := c.overCeiling
	c.overCeiling = len(c.buf) > c.limit
	if was && !c.overCeiling {
		fmt.Fprint(c.w, "[within the limit again]\r\n")
	}
}

func (c *capture) banner() {
	fmt.Fprintf(c.w,
		"Paste or type the task, then press Enter to dispatch.\r\n"+
			"Ctrl-J for a newline, Ctrl-C to cancel. Limit %d bytes.\r\n",
		c.limit)
}

// renderPaste emits a bounded record rather than replaying the payload. The
// developer gets the extent and the identity of what they pasted at a cost
// proportional to terminal width, not to payload size -- and a large paste does
// not scroll away the failure they were looking at.
func (c *capture) renderPaste(pasted []byte) {
	lines := strings.Split(strings.TrimSuffix(string(pasted), "\n"), "\n")
	fmt.Fprintf(c.w, "\r\n[pasted %d line(s), %d bytes]\r\n", len(lines), len(pasted))
	fmt.Fprintf(c.w, "  %s\r\n", truncateForDisplay(neutralize([]byte(lines[0])), displayWidth))
	if len(lines) > 2 {
		fmt.Fprintf(c.w, "  ... %d more line(s)\r\n", len(lines)-2)
	}
	if len(lines) > 1 {
		last := lines[len(lines)-1]
		fmt.Fprintf(c.w, "  %s\r\n", truncateForDisplay(neutralize([]byte(last)), displayWidth))
	}
}
