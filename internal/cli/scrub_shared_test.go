package cli

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
)

// TestScrubEvidenceSharesTheGateControlRemoval shows removal.ScrubEvidence
// and the removal gate's sanitizer remove controls the same way. It runs
// over the golden inputs that hold no escape sequence (ScrubEvidence removes
// a whole sequence, the gate only its ESC) and that the gate doesn't cut (the
// two cut differently), and that carry no secret.
func TestScrubEvidenceSharesTheGateControlRemoval(t *testing.T) {
	compared := 0
	for _, tc := range sanitizeGoldenCases {
		if strings.Contains(tc.in, "\x1b") || sanitizeRemovalGateText(tc.in, 1<<20) != tc.gate {
			continue
		}
		compared++
		t.Run(tc.name, func(t *testing.T) {
			if got := removal.ScrubEvidence(tc.in, tc.max); got != tc.gate {
				t.Fatalf("ScrubEvidence(%q, %d) = %q, want the gate's %q", tc.in, tc.max, got, tc.gate)
			}
		})
	}
	if compared < 10 {
		t.Fatalf("compared %d golden inputs; the filter left too few", compared)
	}
}
