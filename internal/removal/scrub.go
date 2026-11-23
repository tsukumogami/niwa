package removal

import "github.com/tsukumogami/niwa/internal/scrub"

// The scrubbing itself lives in internal/scrub, a leaf package that imports
// nothing from niwa, so packages that must not import removal can scrub too.
// These forward to it under the names removal's callers already use.

// Redacted is what ScrubEvidence prints in place of a secret it finds.
const Redacted = scrub.Redacted

// StripBidi turns every Unicode bidirectional control into a space. See
// scrub.StripBidi.
func StripBidi(s string) string { return scrub.StripBidi(s) }

// StripControls makes s safe for one terminal line. See scrub.StripControls.
func StripControls(s string) string { return scrub.StripControls(s) }

// CutText cuts s to at most max bytes, with "..." marking a cut. See
// scrub.CutText.
func CutText(s string, max int) string { return scrub.CutText(s, max) }

// ScrubEvidence makes a unit-sourced string safe to print, cut to max
// characters after it is scrubbed. See scrub.ScrubEvidence.
func ScrubEvidence(s string, max int) string { return scrub.ScrubEvidence(s, max) }
