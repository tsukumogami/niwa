package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// noticeClaudeTrustOverlay is the one-time notice key for instance trust that
// is on only because the workspace overlay set [claude] trust_instances. It
// is shown by the first create or apply in the workspace that leaves an
// instance trusted.
const noticeClaudeTrustOverlay = "claude-trust-overlay"

// claudeTrustDelivery is what one run's trust step settled on: the grants to
// record in the instance state, the undos for this run's writes, and the
// warnings for the deferred block.
type claudeTrustDelivery struct {
	// root is the instance root's grant, recorded as InstanceState.ClaudeTrust.
	root *claudetrust.Grant
	// repos are the repository directories' grants, keyed by instance-relative
	// path and recorded as InstanceState.ClaudeTrustRepos. nil when empty.
	repos map[string]*claudetrust.Grant
	// undos take back, one each, the writes this run made to the current
	// config file. Each returns a warning, empty when the write was undone.
	undos []func() string
	// wrote reports that this run changed the current config file.
	wrote    bool
	warnings []string
}

// deliverClaudeTrust brings Claude Code trust for the instance root and for
// each repository directory in repos (instance-relative, slash-separated
// paths of the repositories niwa materialized for Claude) in line with
// [claude] trust_instances. existing is the instance's state before this run,
// nil for a new instance.
//
// Create and Apply call it after the pipeline has succeeded, right before the
// state is saved, so no failure but the save itself can follow a trust write.
// The caller runs the undos if the save fails, so a write is never left
// without the record that lets destroy withdraw it.
//
// One race is known and not handled here: the instance state is saved without
// any lock, so two applies running at once on the same instance can each save
// a state built before the other's, and the later save can drop a grant the
// earlier one recorded. The unsynchronized save predates trust. The entry is
// then left with no record: later applies take it for trust someone else
// granted, and destroying the instance leaves it behind.
func (a *Applier) deliverClaudeTrust(instanceRoot string, on bool, repos []string, existing *InstanceState) claudeTrustDelivery {
	var rootPrior *claudetrust.Grant
	var repoPrior map[string]*claudetrust.Grant
	if existing != nil {
		rootPrior = existing.ClaudeTrust
		repoPrior = existing.ClaudeTrustRepos
	}

	var d claudeTrustDelivery
	root, undo, wrote, warnings := a.deliverRootClaudeTrust(instanceRoot, on, rootPrior)
	d.root = root
	d.wrote = wrote
	d.warnings = warnings
	if undo != nil {
		d.undos = append(d.undos, undo)
	}

	repoGrants, repoUndos, repoWarnings := a.deliverRepoClaudeTrust(instanceRoot, on, repos, repoPrior)
	d.repos = repoGrants
	d.undos = append(d.undos, repoUndos...)
	d.wrote = d.wrote || len(repoUndos) > 0
	d.warnings = append(d.warnings, repoWarnings...)
	return d
}

// deliverRootClaudeTrust brings the instance root's Claude Code trust in line
// with [claude] trust_instances and returns the grant to record in the
// instance state, an undo for this run's write, whether this run changed the
// current config file (wrote), and warnings for the deferred block.
//
// undo returns a warning, empty when the write was taken back. It is non-nil
// whenever this run changed the current config file, which includes setting
// the flag again on an entry an earlier grant already recorded (a flag
// removed by hand). It takes back only that change. Two retractions are not
// rolled back on a failed save: the flag removed from the old file after
// CLAUDE_CONFIG_DIR changed, and the flag removed because the option is off.
// Both are benign: the state still on disk names the old entry, so a later
// Withdraw of it finds the flag gone and does nothing, and the next apply
// converges.
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
func (a *Applier) deliverRootClaudeTrust(instanceRoot string, on bool, existing *claudetrust.Grant) (grant *claudetrust.Grant, undo func() string, wrote bool, warnings []string) {
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

// deliverRepoClaudeTrust is deliverRootClaudeTrust for the repository
// directories niwa cloned into the instance. A session started inside one is
// keyed by the repository, not by the instance root, so each gets its own
// grant. repos are the instance-relative paths to trust when on is set;
// existing are the grants recorded before this run, by the same paths. It
// returns the grants to record (nil when there are none), an undo per write
// this run made to the current config file, and warnings.
//
// Each repository follows the root's rules: Ensure when on, with its moved,
// malformed and config-file-change handling; a review-sourced record carried
// untouched; an empty DeveloperHome carrying every record forward. A
// workspace record for a directory that no longer gets trust, because the
// option is off, the repository left the config, or Claude was turned off
// for it, is retracted once and dropped, and kept only when the retraction
// was skipped, so the next apply retries it.
func (a *Applier) deliverRepoClaudeTrust(instanceRoot string, on bool, repos []string, existing map[string]*claudetrust.Grant) (grants map[string]*claudetrust.Grant, undos []func() string, warnings []string) {
	if a.DeveloperHome == "" {
		return existing, nil, nil
	}

	grants = map[string]*claudetrust.Grant{}
	wanted := map[string]bool{}
	if on {
		for _, rel := range repos {
			wanted[rel] = true
		}
	}

	for _, rel := range sortedGrantPaths(existing) {
		g := existing[rel]
		switch {
		case g.Source == claudetrust.SourceReview:
			grants[rel] = g
		case wanted[rel]:
			// Ensure below takes it as the prior record.
		default:
			keep, w := retractRepoClaudeTrust(instanceRoot, rel, g)
			if w != "" {
				warnings = append(warnings, w)
			}
			if keep {
				grants[rel] = g
			}
		}
	}

	if on {
		env := claudetrust.EnvFor(a.DeveloperHome)
		for _, rel := range repos {
			prior := existing[rel]
			if prior != nil && prior.Source == claudetrust.SourceReview {
				continue
			}
			out := claudetrust.Ensure(env, filepath.Join(instanceRoot, filepath.FromSlash(rel)), prior, claudetrust.SourceWorkspace)
			if out.Warning != "" {
				warnings = append(warnings, out.Warning)
			}
			if out.Grant != nil {
				grants[rel] = out.Grant
			}
			if out.Changed && out.Grant != nil {
				undos = append(undos, out.Undo)
			}
		}
	}

	if len(grants) == 0 {
		grants = nil
	}
	return grants, undos, warnings
}

// retractRepoClaudeTrust withdraws the flag that g, the workspace record for
// the repository directory rel, names, once that directory no longer gets
// trust. keep reports that the record must stay for the next apply to retry:
// the retraction was skipped, or a directory that exists couldn't be
// resolved. A record that names a path outside the instance, a directory
// that no longer exists, a different key (the instance moved) or a malformed
// grant is dropped with a warning and its entry left alone, since niwa can't
// prove the entry is this repository's.
func retractRepoClaudeTrust(instanceRoot, rel string, g *claudetrust.Grant) (keep bool, warning string) {
	instanceKey, err := claudetrust.Key(instanceRoot)
	if err != nil {
		return true, fmt.Sprintf("Claude trust entry %q in %s left in place: %v", g.Key, g.ConfigFile, err)
	}
	key, err := repoTrustKey(instanceRoot, instanceKey, rel)
	switch {
	case errors.Is(err, errRepoTrustPathRefused):
		return false, fmt.Sprintf("ignoring the Claude trust record for repository %q: %v; the entry %q in %s was left in place", rel, err, g.Key, g.ConfigFile)
	case err != nil:
		return true, fmt.Sprintf("Claude trust entry %q in %s left in place: %v", g.Key, g.ConfigFile, err)
	case key == "":
		return false, fmt.Sprintf(
			"the recorded Claude trust entry %q in %s is for the repository directory %s, which no longer exists; leaving it in place, remove it by hand if it is stale",
			g.Key, g.ConfigFile, rel)
	case g.Key != key:
		return false, claudetrust.MovedWarning(g, key)
	case g.ValidateShape() != nil:
		return false, claudetrust.MalformedWarning(g, key)
	}
	if w := claudetrust.Withdraw(g, key, claudetrust.Retract); w != "" {
		return true, w
	}
	return false, ""
}

// errRepoTrustPathRefused marks a recorded repository path that names no
// directory inside the instance. Nothing acts on a record carrying one.
var errRepoTrustPathRefused = errors.New("not a directory inside the instance")

// repoTrustKey returns the projects key a recorded repository grant must
// name: the resolved path of <instanceRoot>/<rel>. rel comes from instance
// state, which anyone controlling the instance can edit, so it must be a
// clean relative path with no ".." element, and the directory it resolves to
// must sit beneath instanceKey, the instance root's own key. Anything else
// fails with errRepoTrustPathRefused, so a crafted record can't name another
// project's key. A directory that no longer exists gives "" and no error.
func repoTrustKey(instanceRoot, instanceKey, rel string) (string, error) {
	p := filepath.FromSlash(rel)
	switch {
	case rel == "" || filepath.IsAbs(p) || strings.HasPrefix(rel, "/"):
		return "", fmt.Errorf("%w: %q is not a relative path", errRepoTrustPathRefused, rel)
	case slices.Contains(strings.Split(filepath.ToSlash(p), "/"), ".."):
		return "", fmt.Errorf("%w: %q contains a .. element", errRepoTrustPathRefused, rel)
	case p == "." || filepath.Clean(p) != p:
		return "", fmt.Errorf("%w: %q is not a clean path", errRepoTrustPathRefused, rel)
	}
	key, err := claudetrust.Key(filepath.Join(instanceRoot, p))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	within, err := filepath.Rel(instanceKey, key)
	if err != nil || within == "." || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q resolves to %s, outside %s", errRepoTrustPathRefused, rel, key, instanceKey)
	}
	return key, nil
}

// sortedGrantPaths returns the paths in grants that hold a record, sorted, so
// every pass over the repository grants acts, and warns, in the same order.
func sortedGrantPaths(grants map[string]*claudetrust.Grant) []string {
	paths := make([]string, 0, len(grants))
	for rel, g := range grants {
		if g != nil {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	return paths
}

// undoClaudeTrust runs deliverClaudeTrust's undos after the state save
// failed, latest write first, warning for each write that can't be taken
// back.
func (a *Applier) undoClaudeTrust(undos []func() string) {
	for i := len(undos) - 1; i >= 0; i-- {
		if w := undos[i](); w != "" {
			a.Reporter.Warn("the instance state was not saved, and the Claude trust written this run could not be removed: %s", w)
		}
	}
}

// discloseOverlayTrust prints the one-time notice that instance trust is on
// because of the workspace overlay, and records it in the workspace root's
// disclosed notices. It runs after the state save, so the notice is shown
// only for trust that stays recorded.
//
// The notice is due when trust is on only because of the overlay, the
// instance holds a workspace grant for its root or for a repository
// directory, and the workspace hasn't been told yet; whether this run wrote
// the entry doesn't matter, so an instance trusted before the notice existed
// still gets it once. A base config that sets the key never gets the notice.
// When the root state exists but can't be read, the notice is neither shown
// nor recorded, so a damaged file is never replaced by one holding only the
// notice.
//
// In a single-instance layout there is no workspace root state to record
// the notice in, so it isn't persisted; there it is shown only by a run
// that wrote trust (wrote), rather than by every apply.
func (a *Applier) discloseOverlayTrust(workspaceRoot string, fromOverlay, wrote bool, root *claudetrust.Grant, repos map[string]*claudetrust.Grant) {
	if !fromOverlay {
		return
	}
	if root != nil && root.Source != claudetrust.SourceWorkspace {
		root = nil
	}
	var repoGrants []*claudetrust.Grant
	for _, rel := range sortedGrantPaths(repos) {
		if g := repos[rel]; g.Source == claudetrust.SourceWorkspace {
			repoGrants = append(repoGrants, g)
		}
	}
	if root == nil && len(repoGrants) == 0 {
		return
	}
	if !workspaceRootHoldsState(workspaceRoot) {
		if wrote {
			a.deferOverlayTrustNotice(root, repoGrants)
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
	a.deferOverlayTrustNotice(root, repoGrants)
	saveWorkspaceRootDisclosures(workspaceRoot, rootState, []string{noticeClaudeTrustOverlay})
}

// deferOverlayTrustNotice queues the overlay trust notice for root, the
// instance root's grant or nil when it holds none, and repos, the repository
// directories' workspace grants.
func (a *Applier) deferOverlayTrustNotice(root *claudetrust.Grant, repos []*claudetrust.Grant) {
	const head = "note: instance trust is on because the workspace overlay sets [claude] trust_instances. "
	const tail = "Set [claude] trust_instances = false in the workspace config to turn it off."
	const repoSettings = "including each repository's own committed .claude settings and hooks. "
	dirs := fmt.Sprintf("%d repository directories", len(repos))
	if len(repos) == 1 {
		dirs = "1 repository directory"
	}
	switch {
	case len(repos) == 0:
		a.Reporter.Defer(head+
			"niwa marked %s trusted in %s, so Claude Code will apply the instance's project permissions, hooks and MCP servers without asking. "+
			tail, root.Key, root.ConfigFile)
	case root == nil:
		a.Reporter.Defer(head+
			"niwa marked %s in the instance trusted in %s, so Claude Code will apply the project permissions, hooks and MCP servers there without asking, "+
			repoSettings+tail, dirs, repos[0].ConfigFile)
	default:
		a.Reporter.Defer(head+
			"niwa marked %s and %s in it trusted in %s, so Claude Code will apply the instance's project permissions, hooks and MCP servers without asking, "+
			repoSettings+tail, root.Key, dirs, root.ConfigFile)
	}
}

// saveInstanceState saves the instance state through the test seam, when one
// is set.
func (a *Applier) saveInstanceState(dir string, state *InstanceState) error {
	if a.saveState != nil {
		return a.saveState(dir, state)
	}
	return SaveState(dir, state)
}
