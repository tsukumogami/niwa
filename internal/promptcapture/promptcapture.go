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
	"bufio"
	"bytes"
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

	// maxBufferBytes is a memory backstop, NOT a prompt ceiling. There is no
	// prompt ceiling: a prompt too large to travel as one argv element is
	// written to a file by the launcher and the worker gets a pointer, so the
	// capture never refuses on size the developer could care about.
	//
	// It is stated as a derivation with a floor rather than a round literal so
	// it cannot be quietly tuned back down into the wall this feature removed.
	// The multiplicand is the largest single argv string Linux accepts
	// (MAX_ARG_STRLEN, 32 pages, minus the NUL); internal/cli owns that number
	// for its own purposes and a test there asserts this constant still clears
	// 64x it. Duplicating one arithmetic expression is the price of not giving
	// a terminal reader a dependency on an exec fact.
	backstopMultiple = 64
	maxBufferBytes   = backstopMultiple * (32*4096 - 1)

	displayWidth = 100
)

// Read runs the capture against the real terminal, reading standard input and
// rendering to standard error. Standard output is left alone so a caller can
// redirect it.
//
// No size is surfaced to the developer at any point. The only bound is
// maxBufferBytes, a memory backstop far above anything a paste produces.
func Read() (string, error) {
	restore, err := enterRaw()
	if err != nil {
		return "", err
	}
	defer restore()
	return read(stdin(), stderr(), maxBufferBytes)
}

// read is the capture core. Tests drive it with an ordinary reader and writer,
// at any chunk size, without a terminal.
// backstop is a parameter only so tests can drive the refusal path without
// allocating eight megabytes. Production always passes maxBufferBytes.
func read(r io.Reader, w io.Writer, backstop int) (string, error) {
	// The transcript is buffered and flushed once per read chunk. Unbuffered,
	// every echoed byte is its own write syscall -- and on a terminal that does
	// not delimit pastes, every byte of a paste arrives as typed input, so a
	// 582 KB paste becomes ~582,000 syscalls. Flushing at the read boundary
	// costs nothing in latency: that is where the terminal hands us input
	// anyway.
	bw := bufio.NewWriter(w)
	c := &capture{w: bw, backstop: backstop}
	c.banner()
	bw.Flush()

	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; i++ {
			done, text, cerr := c.step(buf[i])
			if done {
				bw.Flush()
				return text, cerr
			}
		}
		bw.Flush()
		if err != nil {
			if errors.Is(err, io.EOF) {
				text, cerr := c.endOfInput()
				bw.Flush()
				return text, cerr
			}
			return "", err
		}
	}
}

type capture struct {
	w        io.Writer
	backstop int

	buf []byte // the payload

	// Three pieces of state that must survive a read boundary. They are where
	// the correctness risk lives, which is why the tests drive every path
	// through them at chunk size one.
	held         []byte // a partial paste marker, not yet known to be one
	lastWasCR    bool   // a carriage return ended the previous chunk, inside a paste
	pendingBreak bool   // a paste ended mid-line; insert one newline before the next append

	inPaste     bool
	pasteBuf    []byte
	pasteDenied bool // this paste crossed the backstop; retain none of it

	// backstopHit is edge-triggered so the refusal is reported once per
	// crossing rather than once per refused byte. On a terminal without paste
	// delimiters every byte of a paste arrives as typed input, so a per-byte
	// report would emit millions of lines and stall the capture.
	backstopHit bool

	// echoedOnLine counts characters echoed since the last thing that moved the
	// cursor to a fresh line. A single-character delete can only be erased in
	// place while this is positive; past that the character is not on screen
	// where a backspace could reach it.
	echoedOnLine int
}

// maxHeld bounds how long a byte run is withheld while it might still complete
// a recognized sequence. A pasted log is full of escape bytes and none of them
// should stall the reader.
const maxHeld = 16

type seqKind int

const (
	seqNone    seqKind = iota // definitely not a recognized sequence
	seqPartial                // could still become one; keep holding
	seqPasteStart
	seqPasteEnd
	seqNewline
)

// classify decides what a withheld escape run is.
//
// Inside a paste only the end marker is recognized: every other byte is payload,
// including escape sequences that would mean something outside. A pasted log
// carrying a modified-Enter encoding must arrive at the worker verbatim.
func classify(s string, inPaste bool) seqKind {
	if inPaste {
		switch {
		case s == pasteEnd:
			return seqPasteEnd
		case strings.HasPrefix(pasteEnd, s):
			return seqPartial
		}
		return seqNone
	}

	switch {
	case s == pasteStart:
		return seqPasteStart
	case s == pasteEnd:
		return seqPasteEnd
	case strings.HasPrefix(pasteStart, s) || strings.HasPrefix(pasteEnd, s):
		return seqPartial
	}

	// Alt+Enter, and Esc-then-Enter, which are the same two bytes.
	if s == "\x1b\r" || s == "\x1b\n" {
		return seqNewline
	}
	if s == "\x1b" {
		return seqPartial
	}
	return classifyCSIEnter(s)
}

// classifyCSIEnter recognizes a modified Enter reported by a terminal that has
// been configured to distinguish it -- which is what Claude Code's terminal
// setup does, so a developer who has run it will reach for Shift+Enter here.
//
// Two encodings are in use. The kitty keyboard protocol reports Enter as
// CSI 13 ; <modifiers> u, and xterm's modifyOtherKeys reports it as
// CSI 27 ; <modifiers> ; 13 ~. Any modifier combination counts: shift, alt,
// control, super, and any mix of them all mean "newline, not submit". Bare
// Enter arrives as a single carriage return and is not matched here, so it
// still submits.
//
// These are recognized only if they arrive. Asking for them means emitting a
// protocol-enable sequence, which is capability negotiation -- something this
// capture deliberately does not do, and which would add a third piece of
// terminal state to restore.
func classifyCSIEnter(s string) seqKind {
	const (
		kitty  = "\x1b[13;"
		xterm  = "\x1b[27;"
		xtermK = "13~"
	)
	switch {
	case strings.HasPrefix(kitty, s) || strings.HasPrefix(xterm, s):
		return seqPartial

	case strings.HasPrefix(s, kitty):
		rest := s[len(kitty):]
		if rest == "" {
			return seqPartial
		}
		if d := strings.TrimRight(rest, "u"); allDigits(d) && d != "" {
			if strings.HasSuffix(rest, "u") {
				return seqNewline
			}
			return seqPartial
		}
		return seqNone

	case strings.HasPrefix(s, xterm):
		rest := s[len(xterm):]
		mods, tail, split := strings.Cut(rest, ";")
		if !split {
			if allDigits(rest) {
				return seqPartial
			}
			return seqNone
		}
		if !allDigits(mods) || mods == "" {
			return seqNone
		}
		switch {
		case tail == xtermK:
			return seqNewline
		case strings.HasPrefix(xtermK, tail):
			return seqPartial
		}
		return seqNone
	}
	return seqNone
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// step consumes one byte. It reports done when the capture has an outcome.
func (c *capture) step(b byte) (done bool, text string, err error) {
	// Sequence matching takes precedence, because every byte in a recognized
	// sequence is also a plausible payload byte and only context decides.
	if len(c.held) > 0 || b == 0x1b {
		c.held = append(c.held, b)
		s := string(c.held)

		switch classify(s, c.inPaste) {
		case seqPasteStart:
			c.held = nil
			c.inPaste = true
			c.lastWasCR = false
			c.pasteBuf = nil
			c.pasteDenied = false
			return false, "", nil
		case seqPasteEnd:
			c.held = nil
			c.closePaste()
			return false, "", nil
		case seqNewline:
			c.held = nil
			c.appendTyped('\n')
			return false, "", nil
		case seqPartial:
			if len(c.held) < maxHeld {
				return false, "", nil
			}
			// Too long to be anything we know; fall through and replay.
		}

		// Not a recognized sequence. Replay the held bytes as ordinary input;
		// the current byte is already among them.
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
	return true, string(c.buf), nil
}

func (c *capture) endOfInput() (string, error) {
	if len(c.buf) == 0 {
		return "", ErrEndOfInput
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
	if len(c.buf)+len(c.pasteBuf) > c.backstop {
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
		c.refuseUnretained()
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
	c.echoedOnLine = 0
}

func (c *capture) appendTyped(b byte) {
	if len(c.buf)+1 > c.backstop {
		c.refuseUnretained()
		return
	}
	c.backstopHit = false
	c.flushPendingBreak()
	c.buf = append(c.buf, b)

	// A newline the developer typed is echoed as an actual line break, not
	// through the neutralizer. Neutralizing it renders "^J" and then the
	// trailing carriage return puts the cursor back at column zero of the SAME
	// line, so the next thing typed overwrites what is already there -- and a
	// later submit looks like it deleted the rest of the line.
	//
	// The neutralizer exists to stop PASTED bytes from acting on the terminal.
	// A break the developer asked for is not that: moving to the next line is
	// exactly what they requested, and it cannot move the cursor up, left, or
	// over existing text.
	if b == '\n' {
		fmt.Fprint(c.w, "\r\n")
		c.echoedOnLine = 0
		return
	}

	rendered := neutralize([]byte{b})
	fmt.Fprint(c.w, rendered)
	c.echoedOnLine += len(rendered)
}

func (c *capture) flushPendingBreak() {
	if c.pendingBreak {
		c.pendingBreak = false
		c.buf = append(c.buf, '\n')
		fmt.Fprint(c.w, "\r\n")
		c.echoedOnLine = 0
	}
}

// refuseUnretained reports a backstop crossing. It names no byte ceiling and
// gives no size advice, because under the spill there is nothing for the
// developer to do about size: what they get instead is their state -- nothing
// from this input survived, what came before is intact, and the capture is
// still open.
//
// Edge-triggered: repeated crossings while already over report nothing.
func (c *capture) refuseUnretained() {
	if c.backstopHit {
		return
	}
	c.backstopHit = true
	fmt.Fprint(c.w, "\r\n[not retained: the capture ran out of room to hold this input in memory. "+
		"Nothing from it was kept, and what you entered before is unchanged. The capture is still open.]\r\n")
	c.echoedOnLine = 0
}

func (c *capture) deleteRune() {
	if len(c.buf) == 0 {
		return
	}
	i := len(c.buf) - 1
	for i > 0 && c.buf[i]&0xc0 == 0x80 {
		i--
	}
	removed := neutralize(c.buf[i:])
	c.buf = c.buf[:i]
	c.afterDelete(len(removed))
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
	c.afterDelete(-1)
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
	c.afterDelete(-1)
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

// afterDelete gives the developer feedback proportional to what was removed.
//
// erasable is the number of rendered columns the deletion took off the current
// visual line, or -1 when the deletion was too large to undo on screen. A single
// character that is still on the line is erased in place with backspaces, which
// is what a terminal does and what the developer expects; anything larger, or
// anything reaching back past the current line, gets one status line instead.
//
// Printing a status line on every keystroke -- which an earlier version did --
// turns backspacing over a typo into a column of byte counts and fragments the
// text being typed.
func (c *capture) afterDelete(erasable int) {
	c.pendingBreak = false

	if erasable > 0 && erasable <= c.echoedOnLine {
		for i := 0; i < erasable; i++ {
			fmt.Fprint(c.w, "\b \b")
		}
		c.echoedOnLine -= erasable
	} else {
		fmt.Fprintf(c.w, "\r\n[%d bytes]\r\n", len(c.buf))
		c.echoedOnLine = 0
	}

	// A deletion that brings the buffer back under re-arms the backstop, so a
	// later crossing reports again.
	if len(c.buf) <= c.backstop {
		c.backstopHit = false
	}
}

func (c *capture) banner() {
	fmt.Fprint(c.w,
		"Paste or type the task, then press Enter to dispatch.\r\n"+
			"Ctrl-J for a newline, Ctrl-C to cancel.\r\n")
}

// renderPaste emits a bounded record rather than replaying the payload. The
// developer gets the extent and the identity of what they pasted at a cost
// proportional to terminal width, not to payload size -- and a large paste does
// not scroll away the failure they were looking at.
func (c *capture) renderPaste(pasted []byte) {
	// Locate the first and last line by scanning for separators rather than
	// splitting. strings.Split over a 582 KB log copies the whole payload and
	// builds a slice header per line, all to print at most two of them.
	body := bytes.TrimSuffix(pasted, []byte("\n"))
	count := bytes.Count(body, []byte("\n")) + 1

	first := body
	if i := bytes.IndexByte(body, '\n'); i >= 0 {
		first = body[:i]
	}

	fmt.Fprintf(c.w, "\r\n[pasted %d line(s), %d bytes]\r\n", count, len(pasted))
	fmt.Fprintf(c.w, "  %s\r\n", renderLine(first))
	if count > 2 {
		fmt.Fprintf(c.w, "  ... %d more line(s)\r\n", count-2)
	}
	if count > 1 {
		last := body
		if i := bytes.LastIndexByte(body, '\n'); i >= 0 {
			last = body[i+1:]
		}
		fmt.Fprintf(c.w, "  %s\r\n", renderLine(last))
	}
}

// renderLine neutralizes only as much of a line as the display can hold. The
// cost is proportional to displayWidth, not to the line, which is what the
// bounded-record promise actually requires.
func renderLine(line []byte) string {
	return truncateForDisplay(neutralize(truncateBytesForNeutralize(line, displayWidth)), displayWidth)
}

// MaxBufferBytes exposes the memory backstop so a test in the package that owns
// the exec cap can assert this constant still clears its floor. It is not a
// prompt limit and no caller should treat it as one: nothing about a prompt's
// size is the caller's decision any more.
func MaxBufferBytes() int { return maxBufferBytes }
