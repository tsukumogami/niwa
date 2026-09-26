package promptcapture

import (
	"bytes"
	"strings"
	"testing"
)

// sizeSample spans the old ceiling on both sides. 131,071 is the largest single
// argv string Linux accepts; every one of these must now be captured and
// returned, because the launcher routes an oversized prompt to a file rather
// than the capture refusing it.
var sizeSample = []int{0, 1, 131070, 131071, 131072, 614400}

func TestEverySizeIsAcceptedAndReturnedVerbatim(t *testing.T) {
	for _, n := range sizeSample {
		body := strings.Repeat("x", n)
		var out bytes.Buffer
		got, err := read(&chunked{src: []byte(paste(body) + "\r"), size: 4096}, &out, maxBufferBytes)
		if err != nil {
			t.Fatalf("n=%d: read: %v", n, err)
		}
		if got != body {
			t.Errorf("n=%d: returned %d bytes, want %d", n, len(got), n)
		}
	}
}

// TestNoSizeIsEverSurfaced is the R43 lint. It covers the accepted-input paths
// and the banner written before the first read; the backstop refusal has its
// own test and is not exercised here.
func TestNoSizeIsEverSurfaced(t *testing.T) {
	forbidden := []string{"limit", "too long", "too large"}

	// The banner, before any input at all.
	var banner bytes.Buffer
	(&capture{w: &banner, backstop: maxBufferBytes}).banner()
	for _, f := range forbidden {
		if strings.Contains(strings.ToLower(banner.String()), f) {
			t.Errorf("banner contains %q: %q", f, banner.String())
		}
	}

	for _, n := range sizeSample {
		var out bytes.Buffer
		if _, err := read(&chunked{src: []byte(paste(strings.Repeat("x", n)) + "\r"), size: 4096}, &out, maxBufferBytes); err != nil {
			t.Fatalf("n=%d: read: %v", n, err)
		}
		low := strings.ToLower(out.String())
		for _, f := range forbidden {
			if strings.Contains(low, f) {
				t.Errorf("n=%d: transcript contains %q", n, f)
			}
		}
	}
}

// TestRepeatedLargePastesAreAllAccepted is the case a smaller bound would have
// broken: the bound is cumulative, so pasting a whole CI log twice must not
// refuse the second one.
func TestRepeatedLargePastesAreAllAccepted(t *testing.T) {
	const (
		each   = 614400
		rounds = 6
	)
	var input strings.Builder
	for i := 0; i < rounds; i++ {
		input.WriteString(paste(strings.Repeat("x", each)))
	}
	input.WriteString("\r")

	var out bytes.Buffer
	got, err := read(&chunked{src: []byte(input.String()), size: 4096}, &out, maxBufferBytes)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) < each*rounds {
		t.Errorf("captured %d bytes of %d pasted; a cumulative bound refused part of it",
			len(got), each*rounds)
	}
	if strings.Contains(strings.ToLower(out.String()), "not retained") {
		t.Error("a 3.6 MB cumulative paste hit the backstop; it is supposed to sit far above that")
	}
}

// TestBackstopIsDerivedWithAFloor pins the magnitude against its derivation
// rather than a copied literal, so it cannot be quietly tuned back down into
// the wall this feature removed.
func TestBackstopIsDerivedWithAFloor(t *testing.T) {
	const maxArgString = 32*4096 - 1
	if backstopMultiple < 64 {
		t.Errorf("backstopMultiple = %d, want at least 64", backstopMultiple)
	}
	if maxBufferBytes != backstopMultiple*maxArgString {
		t.Errorf("maxBufferBytes = %d, want backstopMultiple * %d", maxBufferBytes, maxArgString)
	}
	if maxBufferBytes < 64*maxArgString {
		t.Errorf("maxBufferBytes = %d is below the 64x floor (%d)", maxBufferBytes, 64*maxArgString)
	}
}

// TestBackstopRefusalIsReportedOncePerCrossing is why the flag is
// edge-triggered. On a terminal without paste delimiters every byte arrives as
// typed input, so a per-byte report would emit one line per refused byte.
func TestBackstopRefusalIsReportedOncePerCrossing(t *testing.T) {
	const backstop = 32
	var out bytes.Buffer
	c := &capture{w: &out, backstop: backstop}
	for _, b := range []byte(strings.Repeat("x", backstop+500)) {
		c.step(b)
	}

	if n := strings.Count(out.String(), "not retained"); n != 1 {
		t.Errorf("emitted %d refusals for 500 refused bytes; want exactly 1", n)
	}

	// Deleting back under re-arms it, so a later crossing reports again.
	for i := 0; i < 10; i++ {
		c.step(0x7f)
	}
	for _, b := range []byte(strings.Repeat("y", 200)) {
		c.step(b)
	}
	if n := strings.Count(out.String(), "not retained"); n != 2 {
		t.Errorf("after deleting back under, a second crossing emitted %d total refusals; want 2", n)
	}
}

// TestSubmitIsNeverBlockedBySize is the inverse of the deleted ceiling test:
// there is no size at which the submit gesture stops returning.
func TestSubmitIsNeverBlockedBySize(t *testing.T) {
	var out bytes.Buffer
	c := &capture{w: &out, backstop: maxBufferBytes}
	for _, b := range []byte(paste(strings.Repeat("x", 614400))) {
		c.step(b)
	}
	done, text, err := c.step(0x0d)
	if !done {
		t.Fatal("submit did not return on a 600 KB buffer")
	}
	if err != nil {
		t.Fatalf("submit returned %v", err)
	}
	if len(text) != 614400 {
		t.Errorf("submitted %d bytes, want 614400", len(text))
	}
}
