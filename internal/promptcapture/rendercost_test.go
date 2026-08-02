package promptcapture

import (
	"bytes"
	"strings"
	"testing"
)

// countingWriter records how many Write calls reach it. Unbuffered, the
// capture issues one per echoed byte; on a terminal that does not delimit
// pastes every pasted byte arrives as typed input, so a large paste becomes
// hundreds of thousands of syscalls.
type countingWriter struct {
	writes int
	bytes  int
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.writes++
	w.bytes += len(p)
	return len(p), nil
}

func TestTranscriptWritesAreBatchedNotPerByte(t *testing.T) {
	const n = 614400
	payload := pasteStart + strings.Repeat("x", n) + pasteEnd + "\r"

	var cw countingWriter
	if _, err := read(&chunked{src: []byte(payload), size: 4096}, &cw, bigLimit); err != nil {
		t.Fatalf("read: %v", err)
	}

	// One write per hundred input bytes is a generous ceiling: the real figure
	// is one flush per 4096-byte read chunk plus a handful of records.
	if max := n / 100; cw.writes > max {
		t.Errorf("transcript issued %d writes for %d input bytes; want at most %d",
			cw.writes, n, max)
	}
}

// TestTranscriptWritesAreBatchedForTypedInput covers the path that actually
// hurts: on a terminal without paste delimiters there is no paste record, just
// one echo per byte.
func TestTranscriptWritesAreBatchedForTypedInput(t *testing.T) {
	const n = 200000
	payload := strings.Repeat("x", n) + "\r"

	var cw countingWriter
	if _, err := read(&chunked{src: []byte(payload), size: 4096}, &cw, bigLimit); err != nil {
		t.Fatalf("read: %v", err)
	}

	if max := n / 100; cw.writes > max {
		t.Errorf("transcript issued %d writes for %d typed bytes; want at most %d",
			cw.writes, n, max)
	}
}

// TestPasteRenderingIsBoundedByDisplayWidth pins the property the bounded
// record promises: the work done to render a paste tracks the terminal width,
// not the payload. A single 600 KB line must not be converted to a rune slice
// to print 100 columns of it.
func TestPasteRenderingIsBoundedByDisplayWidth(t *testing.T) {
	small := bytes.Repeat([]byte("a"), 200)
	large := bytes.Repeat([]byte("a"), 614400)

	var smallOut, largeOut bytes.Buffer
	(&capture{w: &smallOut, backstop: bigLimit}).renderPaste(small)
	(&capture{w: &largeOut, backstop: bigLimit}).renderPaste(large)

	// The rendered record for a 3000x larger payload differs only in the byte
	// count it reports, so its length must stay within a few bytes.
	if d := largeOut.Len() - smallOut.Len(); d > 16 {
		t.Errorf("rendered record grew by %d bytes for a 3000x larger paste; "+
			"the record is supposed to be bounded by display width", d)
	}
}

// TestRenderLineNeutralizesOnlyWhatItPrints guards the cheap-path helper: a
// long line must not be neutralized in full before being cut to width.
func TestRenderLineNeutralizesOnlyWhatItPrints(t *testing.T) {
	line := bytes.Repeat([]byte{0x01}, 100000) // each byte renders as two chars
	got := renderLine(line)
	if len(got) > displayWidth*4 {
		t.Errorf("renderLine produced %d bytes; want at most %d", len(got), displayWidth*4)
	}
	if !strings.HasPrefix(got, "^A") {
		t.Errorf("renderLine = %q, want it to start with the neutralized control", got[:8])
	}
}

// TestTruncateForDisplayKeepsRunesWhole is the property the rune-slice
// conversion used to provide, restated against the walking implementation.
func TestTruncateForDisplayKeepsRunesWhole(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"héllo wörld", 6, "hél..."},
		{"日本語のテキスト", 5, "日本..."},
		{"abc", 2, "ab"},
	} {
		if got := truncateForDisplay(tc.in, tc.width); got != tc.want {
			t.Errorf("truncateForDisplay(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
	}
}

// TestPerByteCostIsIndependentOfWhatIsAlreadyEntered is R37's actual
// invariant. It is stated over allocation rather than elapsed time because
// this feature's history already contains one bogus superlinear measurement
// that came from timing a harness rather than the code.
func TestPerByteCostIsIndependentOfWhatIsAlreadyEntered(t *testing.T) {
	perByte := func(n int) float64 {
		payload := pasteStart + strings.Repeat("x", n) + pasteEnd + "\r"
		var out bytes.Buffer
		allocs := testing.AllocsPerRun(3, func() {
			out.Reset()
			if _, err := read(&chunked{src: []byte(payload), size: 4096}, &out, bigLimit); err != nil {
				t.Fatalf("read: %v", err)
			}
		})
		return allocs / float64(n)
	}

	small := perByte(61440)
	large := perByte(614400)
	if small == 0 {
		t.Fatal("measured zero allocation per byte; the probe is not measuring anything")
	}
	// Upper bound only. The requirement is that per-byte cost must not GROW
	// with what is already entered; coming in cheaper at scale is amortized
	// buffer growth doing its job, not a regression.
	if ratio := large / small; ratio > 1.5 {
		t.Errorf("allocation per byte grew %.2fx between 60 KB and 600 KB; "+
			"per-byte cost must not depend on how much is already entered", ratio)
	}
}
