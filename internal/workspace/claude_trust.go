package workspace

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// noticeClaudeTrustOverlay is the one-time notice key for instance trust that
// is on only because the workspace overlay set [claude] trust_instances. It
// is shown by the first create or apply in the workspace that leaves an
// instance trusted.
const noticeClaudeTrustOverlay = "claude-trust-overlay"

// deliverClaudeTrust brings the instance root's Claude Code trust in line with
// [claude] trust_instances and returns the grant to record in the instance
// state, an undo for this run's write, whether this run changed the current
// config file (wrote), and warnings for the deferred block.
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
// One race is known and not handled here: the instance state is saved without
// any lock, so two applies running at once on the same instance can each save
// a state built before the other's, and the later save can drop the grant the
// earlier one recorded. The unsynchronized save predates trust. The entry is
// then left with no record: later applies take it for trust someone else
// granted, and destroying the instance leaves it behind.
//
//   - DeveloperHome empty: nothing outside the instance is touched, and the
//     existing record is carried forward.
//   - A review-sourced record is the review command's, so it is carried
//     forward untouched whatever the option says.
//   - On: claudetrust.Ensure records the grant, retracting a record left in
//     another config file first. A record for a different path (the instance
//     moved) is dropped with a warning and its entry left alone.
//   - Off with a workspace record: the flag is retracted once and the record
//     cleared, whatever its mode. A skipped retraction keeps the record for
//     the next apply. A record for a different path is dropped with a
//     warning as Ensure does, and so is a malformed one (unknown mode or
//     source, a bad config_file), so it isn't kept and warned about on every
//     apply.
func (a *Applier) deliverClaudeTrust(instanceRoot string, on bool, existing *claudetrust.Grant) (grant *claudetrust.Grant, undo func() string, wrote bool, warnings []string) {
	if a.DeveloperHome == "" {
		return existing, nil, false, nil
	}
	if existing != nil && existing.Source == claudetrust.SourceReview {
		return existing, nil, false, nil
	}

	if on {
		out := claudetrust.Ensure(claudetrust.EnvFor(a.DeveloperHome), instanceRoot, existing, claudetrust.SourceWorkspace)
		if out.Warning != "" {
			warnings = append(warnings, out.Warning)
		}
		if !out.Changed || out.Grant == nil {
			return out.Grant, nil, false, warnings
		}
		return out.Grant, out.Undo, true, warnings
	}

	if existing == nil {
		return nil, nil, false, nil
	}
	key, err := claudetrust.Key(instanceRoot)
	if err != nil {
		return existing, nil, false, []string{fmt.Sprintf("Claude trust entry %q in %s left in place: %v", existing.Key, existing.ConfigFile, err)}
	}
	if existing.Key != key {
		// The same call Ensure makes for a moved instance: the old entry
		// can't be proven this instance's, so it stays and the record goes.
		return nil, nil, false, []string{claudetrust.MovedWarning(existing, key)}
	}
	if existing.ValidateShape() != nil {
		return nil, nil, false, []string{claudetrust.MalformedWarning(existing, key)}
	}
	if w := claudetrust.Withdraw(existing, key, claudetrust.Retract); w != "" {
		return existing, nil, false, []string{w}
	}
	// The record goes even for a created grant. Keeping it would make every
	// later apply with the option off strip the flag again, including trust a
	// developer granted by hand after niwa withdrew its own. The cost: an
	// entry niwa created can stay in the config without the flag, a leftover
	// that trusts nothing, and destroying the instance no longer removes it.
	return nil, nil, false, nil
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
