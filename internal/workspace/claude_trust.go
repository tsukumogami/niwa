package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// noticeClaudeTrustOverlay is the one-time notice key for instance trust that
// is on only because the workspace overlay set [claude] trust_instances. It
// is shown by the first create or apply in the workspace that leaves an
// instance trusted.
const noticeClaudeTrustOverlay = "claude-trust-overlay"

// deliverClaudeTrust brings the instance root's Claude Code trust in line with
// [claude] trust_instances and returns the grant to record in the instance
// state, an undo for this run's write, and warnings for the deferred block.
//
// Create and Apply call it after the pipeline has succeeded, right before the
// state is saved, so no failure but the save itself can follow a trust write.
// The caller runs undo if the save fails, so a write is never left without
// the record that lets destroy withdraw it. undo returns a warning, empty
// when the write was taken back. It is non-nil whenever this run changed the
// current config file, which includes setting the flag again on an entry an
// earlier grant already recorded (a flag removed by hand). It takes back only
// that change. Two retractions are not rolled back on a failed save: the
// flag removed from the old file after CLAUDE_CONFIG_DIR changed, and the
// flag removed because the option is off. Both are benign: the state still
// on disk names the old entry, so a later Withdraw of it finds the flag gone
// and does nothing, and the next apply converges.
//
//   - DeveloperHome empty: nothing outside the instance is touched, and the
//     existing record is carried forward.
//   - A review-sourced record is the review command's, so it is carried
//     forward untouched whatever the option says.
//   - On: claudetrust.Ensure records the grant, retracting a record left in
//     another config file first. A record for a different path (the instance
//     moved) is dropped with a warning and its entry left alone.
//   - Off with a workspace record: the flag is retracted and the record
//     cleared. A skipped retraction keeps the record for the next apply.
func (a *Applier) deliverClaudeTrust(instanceRoot string, on bool, existing *claudetrust.Grant) (grant *claudetrust.Grant, undo func() string, warnings []string) {
	if a.DeveloperHome == "" {
		return existing, nil, nil
	}
	if existing != nil && existing.Source == claudetrust.SourceReview {
		return existing, nil, nil
	}

	if on {
		env := claudetrust.Env{Home: a.DeveloperHome, ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR")}
		out := claudetrust.Ensure(env, instanceRoot, existing, claudetrust.SourceWorkspace)
		if out.Warning != "" {
			warnings = append(warnings, out.Warning)
		}
		if !out.Changed || out.Grant == nil {
			return out.Grant, nil, warnings
		}
		// Only what this run wrote is undone: a created entry goes, a flag
		// set on an entry that was already there is retracted.
		g, how := out.Grant, claudetrust.Retract
		if out.Created {
			how = claudetrust.Destroy
		}
		return g, func() string { return claudetrust.Withdraw(g, g.Key, how) }, warnings
	}

	if existing == nil {
		return nil, nil, nil
	}
	key, err := claudetrust.Key(instanceRoot)
	if err != nil {
		return existing, nil, []string{fmt.Sprintf("Claude trust entry %q in %s left in place: %v", existing.Key, existing.ConfigFile, err)}
	}
	if existing.Key != key {
		// The same call Ensure makes for a moved instance: the old entry
		// can't be proven this instance's, so it stays and the record goes.
		return nil, nil, []string{fmt.Sprintf(
			"the recorded Claude trust entry %q in %s is for a different path than this instance (%s); leaving it in place, remove it by hand if it is stale",
			existing.Key, existing.ConfigFile, key)}
	}
	if w := claudetrust.Withdraw(existing, key, claudetrust.Retract); w != "" {
		return existing, nil, []string{w}
	}
	return nil, nil, nil
}

// undoClaudeTrust runs deliverClaudeTrust's undo after the state save failed,
// warning when the write can't be taken back.
func (a *Applier) undoClaudeTrust(undo func() string) {
	if undo == nil {
		return
	}
	if w := undo(); w != "" {
		a.Reporter.Warn("the instance state was not saved, and the Claude trust written this run could not be removed: %s", w)
	}
}

// discloseOverlayTrust prints the one-time notice that instance trust is on
// because of the workspace overlay, and records it in the workspace root's
// disclosed notices. It runs after the state save, so the notice is shown
// only for trust that stays recorded.
//
// The notice is due when trust is on only because of the overlay, the
// instance holds a workspace grant, and the workspace hasn't been told yet;
// whether this run wrote the entry doesn't matter, so an instance trusted
// before the notice existed still gets it once. A base config that sets the
// key never gets the notice. When the root state exists but can't be read,
// the notice is neither shown nor recorded, so a damaged file is never
// replaced by one holding only the notice.
//
// In a single-instance layout there is no workspace root state to record
// the notice in, so it isn't persisted; there it is shown only by a run
// that wrote trust (wrote), rather than by every apply.
func (a *Applier) discloseOverlayTrust(workspaceRoot string, fromOverlay, wrote bool, grant *claudetrust.Grant) {
	if !fromOverlay || grant == nil || grant.Source != claudetrust.SourceWorkspace {
		return
	}
	if !workspaceRootHoldsState(workspaceRoot) {
		if wrote {
			a.deferOverlayTrustNotice(grant)
		}
		return
	}
	// The root state may have been rewritten since it was loaded, by this
	// run's other disclosures, so it is read again rather than reused.
	rootState, err := LoadState(workspaceRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
	if rootState != nil && sliceContains(rootState.DisclosedNotices, noticeClaudeTrustOverlay) {
		return
	}
	a.deferOverlayTrustNotice(grant)
	saveWorkspaceRootDisclosures(workspaceRoot, rootState, []string{noticeClaudeTrustOverlay})
}

// deferOverlayTrustNotice queues the overlay trust notice for grant.
func (a *Applier) deferOverlayTrustNotice(grant *claudetrust.Grant) {
	a.Reporter.Defer("note: instance trust is on because the workspace overlay sets [claude] trust_instances. "+
		"niwa marked %s trusted in %s, so Claude Code will apply the instance's project permissions, hooks and MCP servers without asking. "+
		"Set [claude] trust_instances = false in the workspace config to turn it off.", grant.Key, grant.ConfigFile)
}

// saveInstanceState saves the instance state through the test seam, when one
// is set.
func (a *Applier) saveInstanceState(dir string, state *InstanceState) error {
	if a.saveState != nil {
		return a.saveState(dir, state)
	}
	return SaveState(dir, state)
}
