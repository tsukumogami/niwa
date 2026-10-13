package workspace

import (
	"fmt"
	"os"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// noticeClaudeTrustOverlay is the one-time notice key for instance trust that
// is on only because the workspace overlay set [claude] trust_instances. It
// is shown by the first create or apply in the workspace that writes trust.
const noticeClaudeTrustOverlay = "claude-trust-overlay"

// deliverClaudeTrust brings the instance root's Claude Code trust in line with
// [claude] trust_instances and returns the grant to record in the instance
// state, an undo for this run's write, and warnings for the deferred block.
//
// Create and Apply call it after the pipeline has succeeded, right before the
// state is saved, so no failure but the save itself can follow a trust write.
// undo is non-nil exactly when this run wrote trust into the current config
// file; the caller runs it if the save fails, so a write is never left
// without the record that lets destroy withdraw it.
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
// only for trust that stays written. wrote reports that this run wrote trust;
// a base config that sets the key never gets the notice.
func (a *Applier) discloseOverlayTrust(workspaceRoot string, disclosed []string, fromOverlay, wrote bool, grant *claudetrust.Grant) {
	if !fromOverlay || !wrote || grant == nil || sliceContains(disclosed, noticeClaudeTrustOverlay) {
		return
	}
	a.Reporter.Defer("note: instance trust is on because the workspace overlay sets [claude] trust_instances. "+
		"niwa marked %s trusted in %s, so Claude Code will apply the instance's project permissions, hooks and MCP servers without asking. "+
		"Set [claude] trust_instances = false in the workspace config to turn it off.", grant.Key, grant.ConfigFile)
	// The root state may have been rewritten since it was loaded, by this
	// run's other disclosures, so it is read again rather than reused.
	rootState, _ := LoadState(workspaceRoot)
	saveWorkspaceRootDisclosures(workspaceRoot, rootState, []string{noticeClaudeTrustOverlay})
}

// saveInstanceState saves the instance state through the test seam, when one
// is set.
func (a *Applier) saveInstanceState(dir string, state *InstanceState) error {
	if a.saveState != nil {
		return a.saveState(dir, state)
	}
	return SaveState(dir, state)
}
