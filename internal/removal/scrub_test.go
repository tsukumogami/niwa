package removal

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/scrub"
)

// TestScrubForwardersMatchTheScrubPackage: removal's scrub names give what
// internal/scrub gives, so a caller sees the same text through either.
func TestScrubForwardersMatchTheScrubPackage(t *testing.T) {
	token := "ghp_" + strings.Repeat("A1b2", 9)
	for _, in := range []string{
		"plain text",
		"a\x1b[31mred\x1b[0m b" + string(rune(0x202e)) + "c\x00d",
		"https://user:pass@host/r token=abc " + token,
		strings.Repeat("é", 300),
	} {
		if got, want := ScrubEvidence(in, 200), scrub.ScrubEvidence(in, 200); got != want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", in, got, want)
		}
		if got, want := StripBidi(in), scrub.StripBidi(in); got != want {
			t.Errorf("StripBidi(%q) = %q, want %q", in, got, want)
		}
		if got, want := StripControls(in), scrub.StripControls(in); got != want {
			t.Errorf("StripControls(%q) = %q, want %q", in, got, want)
		}
		if got, want := CutText(in, 10), scrub.CutText(in, 10); got != want {
			t.Errorf("CutText(%q) = %q, want %q", in, got, want)
		}
	}
	if Redacted != scrub.Redacted {
		t.Errorf("Redacted = %q, want %q", Redacted, scrub.Redacted)
	}
}
