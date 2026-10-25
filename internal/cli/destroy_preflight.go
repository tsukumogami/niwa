package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// destroyKept is one instance destroy's preflight found still needed, with
// every reason it found, in the order it found them.
type destroyKept struct {
	Instance string
	Reasons  []string
}

// destroyPreflight finds the instances among instanceDirs that something
// still needs, before destroy prompts or removes anything. Two things keep an
// instance: a Claude session mapped to it that Claude Code lists as running,
// and the workspace's [session.before_remove] command, asked once per
// instance under step. Every reason is collected, not only the first, so a
// refusal can say everything that stands in the way at once.
//
// Claude Code's sessions are listed at most once, and only when some instance
// has a mapped Claude session: an instance nobody mapped a session to has no
// owner the listing could report. A listing that fails is itself a reason for
// each instance that has a mapped session, since niwa can't then tell whether
// that session is running.
//
// It prints nothing; the caller reports the result.
func destroyPreflight(workspaceRoot string, instanceDirs []string, step string, list claudeSessionLister) []destroyKept {
	reasons := make(map[string][]string, len(instanceDirs))

	owners := owningClaudeMappings(workspace.NewReporter(io.Discard), workspaceRoot, instanceDirs)
	if len(owners) > 0 {
		rows, err := list(context.Background())
		if err != nil {
			seen := map[string]bool{}
			for _, m := range owners {
				dir := filepath.Clean(m.InstancePath)
				if seen[dir] {
					continue
				}
				seen[dir] = true
				reasons[dir] = append(reasons[dir], fmt.Sprintf("could not list Claude Code's sessions to check whether its session is running (%v)", err))
			}
		} else {
			byID := make(map[string]claudeSessionRow, len(rows))
			for _, r := range rows {
				if r.SessionID != "" {
					byID[r.SessionID] = r
				}
			}
			for _, m := range owners {
				row, listed := byID[m.SessionID]
				if !listed || !row.running() {
					continue
				}
				dir := filepath.Clean(m.InstancePath)
				reasons[dir] = append(reasons[dir], runningSessionReason(row, m))
			}
		}
	}

	gate := newRemovalGate(workspaceRoot, step, io.Discard)
	if gate.active() {
		ids := map[string][]string{}
		for path, sids := range sessionIDsByInstance(workspaceRoot) {
			ids[filepath.Clean(path)] = append(ids[filepath.Clean(path)], sids...)
		}
		for _, d := range instanceDirs {
			dir := filepath.Clean(d)
			if allowed, reason := gate.check(dir, ids[dir]); !allowed {
				reasons[dir] = append(reasons[dir], reason)
			}
		}
	}

	var kept []destroyKept
	for _, d := range instanceDirs {
		dir := filepath.Clean(d)
		if len(reasons[dir]) > 0 {
			kept = append(kept, destroyKept{Instance: dir, Reasons: reasons[dir]})
		}
	}
	return kept
}

// runningSessionReason names a running session by the short id Claude Code's
// own verbs take, and by its session name when the mapping recorded one.
func runningSessionReason(row claudeSessionRow, m workspace.SessionMapping) string {
	id := row.ID
	if !watch.IsSafeHandle(id) {
		// The id comes from another program's output; fall back to a prefix
		// of the mapped id rather than print something unusable.
		id = m.SessionID
		if len(id) > 8 {
			id = id[:8]
		}
	}
	name := ""
	if m.SessionName != "" {
		name = " (" + sanitizeRemovalGateText(m.SessionName, 80) + ")"
	}
	return fmt.Sprintf("Claude session %s%s is still running", id, name)
}

// reportDestroyKept lists every kept instance with its reasons. With force
// the reasons are printed as overridden; without it, as what stopped the
// destroy.
func reportDestroyKept(w io.Writer, kept []destroyKept, force bool) {
	for _, k := range kept {
		name := sanitizeRemovalGateText(filepath.Base(k.Instance), 80)
		if force {
			fmt.Fprintf(w, "niwa: --force overrides what still needs %s:\n", name)
		} else {
			fmt.Fprintf(w, "niwa: %s is still needed:\n", name)
		}
		for _, r := range k.Reasons {
			fmt.Fprintf(w, "  - %s\n", r)
		}
	}
}
