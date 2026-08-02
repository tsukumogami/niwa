package promptcapture

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// chunked feeds src in fixed-size pieces so a test can drive the reader at any
// chunk boundary. Chunk size one is the interesting case: it forces every
// cross-chunk state (the held marker prefix, the carriage-return flag, and the
// deferred paste break) to actually survive a read boundary.
type chunked struct {
	src  []byte
	size int
	pos  int
}

func (c *chunked) Read(p []byte) (int, error) {
	if c.pos >= len(c.src) {
		return 0, io.EOF
	}
	n := c.size
	if n > len(p) {
		n = len(p)
	}
	if c.pos+n > len(c.src) {
		n = len(c.src) - c.pos
	}
	copy(p, c.src[c.pos:c.pos+n])
	c.pos += n
	return n, nil
}

func paste(s string) string { return pasteStart + s + pasteEnd }

const bigLimit = 1 << 20

// runAll drives read at several chunk sizes, including one, and requires the
// same result from each.
func runAll(t *testing.T, input string, backstop int) (string, error, string) {
	t.Helper()
	var (
		firstText string
		firstErr  error
		firstOut  string
	)
	for i, size := range []int{1, 3, 7, 4096} {
		var out bytes.Buffer
		got, err := read(&chunked{src: []byte(input), size: size}, &out, backstop)
		if i == 0 {
			firstText, firstErr, firstOut = got, err, out.String()
			continue
		}
		if got != firstText || !errors.Is(err, firstErr) && err != firstErr {
			t.Fatalf("chunk size %d disagreed with chunk size 1: got (%q, %v), want (%q, %v)",
				size, got, err, firstText, firstErr)
		}
	}
	return firstText, firstErr, firstOut
}

func TestPastedLineBreaksDoNotSubmit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
	}{
		{"line feeds", paste("alpha\nbeta\ngamma") + "\r"},
		{"carriage returns", paste("alpha\rbeta\rgamma") + "\r"},
		{"crlf pairs", paste("alpha\r\nbeta\r\ngamma") + "\r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err, _ := runAll(t, tc.input, bigLimit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "alpha\nbeta\ngamma" {
				t.Fatalf("got %q, want %q", got, "alpha\nbeta\ngamma")
			}
		})
	}
}

// A line break outside a paste submits. This pins the degradation on terminals
// that do not delimit pasted blocks, so a change to it is deliberate.
func TestUndelimitedLineBreakSubmits(t *testing.T) {
	got, err, _ := runAll(t, "alpha\rbeta\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "alpha" {
		t.Fatalf("got %q, want %q -- undelimited input submits at the first break", got, "alpha")
	}
}

func TestAnnotatedPasteGetsExactlyOneBreak(t *testing.T) {
	got, err, _ := runAll(t, paste("stack trace")+"my note\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "stack trace\nmy note"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestBarePasteGainsNoTrailingNewline(t *testing.T) {
	got, err, _ := runAll(t, paste("stack trace")+"\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "stack trace" {
		t.Fatalf("got %q, want %q -- the deferred break must not fire without typed text", got, "stack trace")
	}
}

func TestPastedControlBytesSurviveIntoPayloadButNotTheTranscript(t *testing.T) {
	payload := "red \x1b[31mtext\x1b[0m and a bell \x07 and \x03 and \x1c"
	got, err, out := runAll(t, paste(payload)+"\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != payload {
		t.Fatalf("payload altered:\n got %q\nwant %q", got, payload)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Fatalf("transcript carries an escape byte: %q", out)
	}
	for _, b := range []byte{0x07, 0x03, 0x1c} {
		if bytes.IndexByte([]byte(out), b) >= 0 {
			t.Fatalf("transcript carries raw control byte %#x: %q", b, out)
		}
	}
}

func TestManualNewlineDoesNotSubmit(t *testing.T) {
	got, err, _ := runAll(t, "one\ntwo\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "one\ntwo" {
		t.Fatalf("got %q, want %q", got, "one\ntwo")
	}
}

func TestEndOfInputSubmitsNonEmptyAndFinishesEmpty(t *testing.T) {
	got, err, _ := runAll(t, "work\x04", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "work" {
		t.Fatalf("got %q, want %q", got, "work")
	}

	_, err, _ = runAll(t, "\x04", bigLimit)
	if !errors.Is(err, ErrEndOfInput) {
		t.Fatalf("got %v, want ErrEndOfInput", err)
	}

	_, err, _ = runAll(t, "", bigLimit)
	if !errors.Is(err, ErrEndOfInput) {
		t.Fatalf("stream end on an empty buffer: got %v, want ErrEndOfInput", err)
	}
}

func TestCancelIsDistinctFromEndOfInput(t *testing.T) {
	_, err, _ := runAll(t, "half typed\x03", bigLimit)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("got %v, want ErrCanceled", err)
	}
}

func TestBackstopAppendIsRefusedInFull(t *testing.T) {
	backstop := 16
	big := strings.Repeat("x", backstop+50)
	var out bytes.Buffer
	c := &capture{w: &out, backstop: backstop}
	for _, b := range []byte(paste(big)) {
		c.step(b)
	}
	if len(c.buf) != 0 {
		t.Fatalf("buffer retained %d bytes of an over-bound paste; want none", len(c.buf))
	}
	if !strings.Contains(strings.ToLower(out.String()), "nothing from it was kept") {
		t.Fatalf("refusal did not say the input was not retained: %q", out.String())
	}
	// The backstop is a memory bound, not a product limit: it must not read as
	// a size ceiling or hand out size advice.
	low := strings.ToLower(out.String())
	for _, forbidden := range []string{"limit", "too large", "too long", "shorten", "re-select", "reference its path"} {
		if strings.Contains(low, forbidden) {
			t.Errorf("backstop refusal contains %q, which describes a size limit: %q", forbidden, out.String())
		}
	}
}

func TestBannerIsHumanReadableBeforeAnyInput(t *testing.T) {
	var out bytes.Buffer
	_, _ = read(strings.NewReader(""), &out, bigLimit)
	stripped := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\n' {
			return -1
		}
		return r
	}, out.String())
	if strings.TrimSpace(stripped) == "" {
		t.Fatal("nothing human-readable was rendered before the first read")
	}
	if !strings.Contains(stripped, "Enter") || !strings.Contains(stripped, "cancel") {
		t.Fatalf("banner does not name the gestures: %q", stripped)
	}
}

func TestForgedPasteEndIsCharacterized(t *testing.T) {
	// A payload containing the end marker closes the block early; the remainder
	// is treated as typed input and its first break submits. Same failure mode
	// as a terminal without paste delimiters, by a different route.
	got, err, _ := runAll(t, pasteStart+"before"+pasteEnd+"after\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "before\nafter" {
		t.Fatalf("got %q; the forged-marker behavior changed and should be a deliberate decision", got)
	}
}

func TestLongSingleLineHasNoPerLineLimit(t *testing.T) {
	line := strings.Repeat("x", 130433)
	got, err, _ := runAll(t, paste(line)+"\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(line) {
		t.Fatalf("captured %d bytes of a %d-byte single line", len(got), len(line))
	}
}

func TestEscapeThatIsNotAMarkerIsPayload(t *testing.T) {
	got, err, _ := runAll(t, paste("\x1b[31mred\x1b[0m")+"\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "\x1b[31mred\x1b[0m" {
		t.Fatalf("got %q, want the escape sequence preserved", got)
	}
}

// Backspacing over a typo must erase in place rather than printing a status
// line per keystroke. An earlier version printed "[N bytes]" on every delete,
// which turned correcting a five-character typo into a column of byte counts
// and fragmented the text being typed.
func TestSingleCharacterDeleteErasesInPlace(t *testing.T) {
	var out bytes.Buffer
	c := &capture{w: &out, backstop: bigLimit}
	for _, b := range []byte("thihs") {
		c.step(b)
	}
	out.Reset()

	c.step(0x7f) // backspace
	c.step(0x7f)

	got := out.String()
	if strings.Contains(got, "bytes]") {
		t.Fatalf("a single-character delete printed a status line: %q", got)
	}
	if got != "\b \b\b \b" {
		t.Fatalf("got %q, want two in-place erases", got)
	}
	if string(c.buf) != "thi" {
		t.Fatalf("buffer is %q, want %q", c.buf, "thi")
	}
}

// A delete that reaches back past what is on the current line cannot be undone
// with backspaces, so it falls back to one status line. Word and line kills do
// the same, because erasing them in place would need a real line editor.
func TestLargeDeletesFallBackToOneStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  byte
	}{
		{"word kill", 0x17},
		{"line kill", 0x15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			c := &capture{w: &out, backstop: bigLimit}
			for _, b := range []byte("some words here") {
				c.step(b)
			}
			out.Reset()

			c.step(tc.key)

			got := out.String()
			if !strings.Contains(got, "bytes]") {
				t.Fatalf("%s did not report the new size: %q", tc.name, got)
			}
			if strings.Count(got, "bytes]") != 1 {
				t.Fatalf("%s printed %d status lines, want 1: %q", tc.name, strings.Count(got, "bytes]"), got)
			}
		})
	}
}

// Deleting back into a pasted block cannot be erased on screen either: the
// paste rendered as a summary, not as its own text, so there is nothing there to
// back over.
func TestDeleteAfterPasteDoesNotEraseIntoTheSummary(t *testing.T) {
	var out bytes.Buffer
	c := &capture{w: &out, backstop: bigLimit}
	for _, b := range []byte(paste("line one\nline two")) {
		c.step(b)
	}
	out.Reset()

	c.step(0x7f) // backspace with nothing echoed on the current line

	got := out.String()
	if strings.Contains(got, "\b") {
		t.Fatalf("backspaced into a paste summary: %q", got)
	}
	if !strings.Contains(got, "bytes]") {
		t.Fatalf("no size feedback after deleting into a paste: %q", got)
	}
}

// A newline the developer types with Ctrl-J must reach the screen as a real
// line break. Rendering it through the neutralizer produces "^J" followed by a
// bare carriage return, which parks the cursor at column zero of the same line:
// subsequent typing overwrites what is already there, and the eventual submit
// looks like it deleted the rest of the line.
func TestTypedNewlineEchoesAsARealLineBreak(t *testing.T) {
	var out bytes.Buffer
	c := &capture{w: &out, backstop: bigLimit}
	for _, b := range []byte("one") {
		c.step(b)
	}
	out.Reset()

	c.step(0x0a) // Ctrl-J
	for _, b := range []byte("two") {
		c.step(b)
	}

	got := out.String()
	if strings.Contains(got, "^J") {
		t.Fatalf("a typed newline was rendered as caret notation: %q", got)
	}
	if !strings.Contains(got, "\r\n") {
		t.Fatalf("a typed newline did not move to the next line: %q", got)
	}
	// A carriage return that is not part of a line break would return the
	// cursor to column zero of the current line and overwrite it.
	if idx := strings.Index(got, "\r"); idx >= 0 && !strings.HasPrefix(got[idx:], "\r\n") {
		t.Fatalf("bare carriage return in the transcript: %q", got)
	}
	if string(c.buf) != "one\ntwo" {
		t.Fatalf("payload is %q, want %q", c.buf, "one\ntwo")
	}
}

// The payload was always correct; only the display was wrong. Pin that so a
// future change to the echo cannot quietly alter what is sent.
func TestTypedNewlineSubmitsBothLines(t *testing.T) {
	got, err, _ := runAll(t, "first\nsecond\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "first\nsecond" {
		t.Fatalf("got %q, want %q", got, "first\nsecond")
	}
}

// A terminal configured to distinguish a modified Enter -- which is what Claude
// Code's terminal setup does -- reports it as an escape sequence rather than a
// carriage return. Every modifier combination means newline; bare Enter still
// submits.
//
// Before this was handled, the sequence landed in the payload as literal escape
// bytes and Alt+Enter submitted early leaving a stray escape behind, so a
// developer with a configured terminal silently sent garbage to the worker.
func TestModifiedEnterInsertsANewline(t *testing.T) {
	for name, seq := range map[string]string{
		"alt+enter":                   "\x1b\r",
		"esc then ctrl-j":             "\x1b\n",
		"kitty shift+enter":           "\x1b[13;2u",
		"kitty ctrl+enter":            "\x1b[13;5u",
		"kitty super+shift+enter":     "\x1b[13;10u",
		"modifyOtherKeys shift+enter": "\x1b[27;2;13~",
		"modifyOtherKeys ctrl+enter":  "\x1b[27;5;13~",
	} {
		t.Run(name, func(t *testing.T) {
			got, err, _ := runAll(t, "one"+seq+"two\r", bigLimit)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "one\ntwo" {
				t.Fatalf("got %q, want %q -- %s must insert a newline, not submit or leak bytes", got, "one\ntwo", name)
			}
		})
	}
}

// Bare Enter is a lone carriage return and must keep submitting.
func TestBareEnterStillSubmits(t *testing.T) {
	got, err, _ := runAll(t, "done\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "done" {
		t.Fatalf("got %q, want %q", got, "done")
	}
}

// Inside a paste every byte is payload, including sequences that would mean
// something outside it. A pasted log carrying a modified-Enter encoding must
// reach the worker verbatim.
func TestModifiedEnterInsideAPasteStaysLiteral(t *testing.T) {
	payload := "before\x1b[13;2uafter"
	got, err, _ := runAll(t, paste(payload)+"\r", bigLimit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != payload {
		t.Fatalf("got %q, want the sequence preserved verbatim inside the paste", got)
	}
}

// An escape run that is not a recognized sequence must not stall the reader or
// vanish. A pasted log is full of them.
func TestUnrecognizedEscapeRunsAreReplayedAsPayload(t *testing.T) {
	for _, seq := range []string{
		"\x1b[31m",       // an ordinary colour code
		"\x1b[13;2x",     // kitty shape, wrong terminator
		"\x1b[27;2;99~",  // modifyOtherKeys shape, not Enter
		"\x1b]0;title\a", // an operating-system command
	} {
		got, err, _ := runAll(t, "a"+seq+"b\r", bigLimit)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", seq, err)
		}
		if got != "a"+seq+"b" {
			t.Fatalf("%q: got %q, want the run preserved", seq, got)
		}
	}
}
