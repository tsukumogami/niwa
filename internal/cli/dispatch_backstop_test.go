package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/promptcapture"
)

// TestCaptureBackstopClearsTheExecCap is the cross-package half of the
// backstop's derivation.
//
// The capture's memory backstop is expressed as a multiple of the largest
// single argv string the operating system accepts, but promptcapture must not
// depend on internal/cli to learn that number -- a terminal reader has no
// business knowing about execve, and the dependency direction would be
// backwards. So the arithmetic appears in both packages and this test is what
// keeps them from drifting: it fails if either the multiple or the exec cap
// moves such that the backstop stops clearing its floor.
//
// The floor exists because a backstop tuned down far enough stops being a
// process-safety bound and becomes the user-facing size wall this feature was
// written to remove.
func TestCaptureBackstopClearsTheExecCap(t *testing.T) {
	const floorMultiple = 64

	got := promptcapture.MaxBufferBytes()
	if want := floorMultiple * maxArgStringBytes; got < want {
		t.Errorf("capture backstop is %d bytes, below the %dx floor over maxArgStringBytes (%d); "+
			"a backstop this low reinstates a size limit the developer can hit",
			got, floorMultiple, want)
	}

	// Sanity: the backstop must also clear the largest payload the requirements
	// name -- a full continuous-integration log at roughly 582 KB -- with room
	// for several of them, since the bound is cumulative across pastes.
	const fullCILog = 582 * 1024
	if got < 10*fullCILog {
		t.Errorf("capture backstop is %d bytes, under ten times a full CI log (%d); "+
			"the bound is cumulative, so pasting a large log a few times would hit it",
			got, fullCILog)
	}
}
