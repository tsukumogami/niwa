package cli

import (
	"fmt"
	"io"

	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// wireKeyReport attaches a fresh key collector and a fresh fallback-notice
// collector to the applier and returns the function that renders them, the
// key report first. Callers defer the returned function immediately, so the
// report is emitted whichever way the run ends: an apply that succeeds with
// keys missing and one that fails outright both leave the user with the same
// enumeration, and on the failure path the instance directory is already gone
// by the time anything could have been read back off disk.
func wireKeyReport(applier *workspace.Applier, w io.Writer) func() {
	keys := keyreport.New()
	applier.Keys = keys
	notices := fallbacknotice.New(nil)
	applier.Notices = notices
	return func() {
		renderKeyReport(w, keys)
		renderNotices(w, notices)
	}
}

// renderKeyReport writes the terminal rendering of a run's unresolved keys, or
// nothing at all when the run supplied everything it declared.
func renderKeyReport(w io.Writer, c *keyreport.Collector) {
	if text := keyreport.RenderText(c.Report()); text != "" {
		fmt.Fprint(w, text)
	}
}

// renderNotices writes the terminal rendering of what the run's store
// fallback served, missed or could not write, or nothing at all when the run
// needed no fallback.
func renderNotices(w io.Writer, c *fallbacknotice.Collector) {
	if text := c.RenderText(); text != "" {
		fmt.Fprint(w, text)
	}
}
