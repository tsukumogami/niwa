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
	"github.com/tsukumogami/niwa/internal/config"
)

// noticeClaudeTrustOverlay is the one-time notice key for instance trust that
// is on only because the workspace overlay set [claude] trust_instances. It
// is shown by the first create or apply in the workspace that leaves an
// instance trusted.
const noticeClaudeTrustOverlay = "claude-trust-overlay"

// noticeClaudeTrustOverlayRepos is the one-time notice key for repository
// directory trust that is on only because of the overlay. A workspace told
// before niwa trusted repository directories holds only
// noticeClaudeTrustOverlay, so it gets this notice once, the first time an
// instance in it holds a workspace grant for a repository directory. The
// full notice records both keys when it already names repository
// directories.
const noticeClaudeTrustOverlayRepos = "claude-trust-overlay-repos"

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
//
// A review instance (see isReviewInstance) gets no repository-directory
// trust whatever the option says: its workspace repository grants are
// retracted as the off switch retracts them, and none is written.
func (a *Applier) deliverClaudeTrust(instanceRoot string, on bool, repos []string, existing *InstanceState) claudeTrustDelivery {
	var rootPrior *claudetrust.Grant
	var repoPrior map[string]*claudetrust.Grant
	review := false
	if existing != nil {
		rootPrior = existing.ClaudeTrust
		repoPrior = existing.ClaudeTrustRepos
		review = existing.isReviewInstance()
	}

	var d claudeTrustDelivery
	root, undo, wrote, warnings := a.deliverRootClaudeTrust(instanceRoot, on, rootPrior)
	d.root = root
	d.wrote = wrote
	d.warnings = warnings
	if undo != nil {
		d.undos = append(d.undos, undo)
	}

	repoGrants, repoUndos, repoWarnings := a.deliverRepoClaudeTrust(instanceRoot, on && !review, repos, repoPrior)
	d.repos = repoGrants
	d.undos = append(d.undos, repoUndos...)
	d.wrote = d.wrote || len(repoUndos) > 0
	d.warnings = append(d.warnings, repoWarnings...)
	return d
}

// trustInstancesOn reports whether [claude] trust_instances is on in cfg, the
// merged config: set, and true.
func trustInstancesOn(cfg *config.WorkspaceConfig) bool {
	return cfg != nil && cfg.Claude.TrustInstances != nil && *cfg.Claude.TrustInstances
}

// claudeEnabled reports whether Claude's own gate, [claude] enabled (per
// repository, then workspace), is open for repoName. It decides which
// repository directories niwa trusts for Claude Code and which repository
// settings carry the approval list, so both follow that gate and no other
// agent's. Trust is a Claude Code feature by definition, the way
// [claude] trust_instances is, which is why the gate key is named here.
func claudeEnabled(cfg *config.WorkspaceConfig, repoName string) bool {
	return AgentEnabled(cfg, repoName, claudeGateKey)
}

// isReviewInstance reports whether s is the state of an instance niwa watch
// staged a review in: the ReviewInstance marker, or a review-sourced grant
// for the instance root, which only the review command records.
func (s *InstanceState) isReviewInstance() bool {
	return s.ReviewInstance || (s.ClaudeTrust != nil && s.ClaudeTrust.Source == claudetrust.SourceReview)
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
	// The record goes even for a created grant. Keeping it would make every
	// later apply with the option off strip the flag again, including trust a
	// developer granted by hand after niwa withdrew its own. The cost: an
	// entry niwa created can stay in the config without the flag, a leftover
	// that trusts nothing, and destroying the instance no longer removes it.
	key, err := claudetrust.Key(instanceRoot)
	keep, w := settleGrant(existing, key, err, claudetrust.Retract)
	if w != "" {
		warnings = []string{w}
	}
	if keep {
		return existing, nil, false, warnings
	}
	return nil, nil, false, warnings
}

// settleGrant withdraws g, the recorded grant for one directory, with how.
// It is the ladder every withdrawal of a recorded grant on apply goes
// through, so the instance root's and the repository directories' can't
// drift apart: the root's on the off switch, and each repository
// directory's, through settleRepoGrant, on the off switch, on leaving the
// config, in a review instance and on destroy. key is the directory's key
// now, and keyErr the error computing it.
//
// keep reports that the record must stay so a later run retries: the
// directory couldn't be resolved, or the withdrawal was skipped. A record for
// a different key (the directory moved) or a malformed one is dropped with a
// warning and its entry left alone, since niwa can't prove that entry is the
// directory's.
func settleGrant(g *claudetrust.Grant, key string, keyErr error, how claudetrust.WithdrawMode) (keep bool, warning string) {
	switch {
	case keyErr != nil:
		return true, fmt.Sprintf("Claude trust entry %q in %s left in place: %v", g.Key, g.ConfigFile, keyErr)
	case g.Key != key:
		return false, claudetrust.MovedWarning(g, key)
	case g.ValidateShape() != nil:
		return false, claudetrust.MalformedWarning(g, key)
	}
	if w := claudetrust.Withdraw(g, key, how); w != "" {
		return true, w
	}
	return false, ""
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
// option is off, the repository left the config, Claude was turned off for
// it, or the instance is a review instance, is retracted once and dropped,
// and kept only when the retraction was skipped, so the next apply retries
// it.
//
// A repository is trusted only when its directory resolves inside the
// instance, the check destroy makes before withdrawing a repository grant
// (repoTrustKey). A repository directory, or the group directory above it,
// that is a symlink leading out of the instance would otherwise get an entry
// that no destroy or later apply could ever withdraw, so it is skipped with
// a warning and nothing is written for it. The key that check returns is
// the path Ensure trusts, so the directory checked is the directory trusted.
func (a *Applier) deliverRepoClaudeTrust(instanceRoot string, on bool, repos []string, existing map[string]*claudetrust.Grant) (grants map[string]*claudetrust.Grant, undos []func() string, warnings []string) {
	if a.DeveloperHome == "" {
		return existing, nil, nil
	}

	grants = map[string]*claudetrust.Grant{}
	instanceKey, keyErr := claudetrust.Key(instanceRoot)
	if keyErr != nil {
		// Nothing under the instance can be checked, so nothing is written
		// or withdrawn, and every record waits for the next apply.
		for _, rel := range sortedGrantPaths(existing) {
			g := existing[rel]
			grants[rel] = g
			if g.Source != claudetrust.SourceReview {
				warnings = append(warnings, fmt.Sprintf("Claude trust entry %q in %s left in place: %v", g.Key, g.ConfigFile, keyErr))
			}
		}
		if on && len(repos) > 0 {
			warnings = append(warnings, fmt.Sprintf("Claude trust for the repository directories in %s not written: %v", instanceRoot, keyErr))
		}
		return nilIfNoGrants(grants), nil, warnings
	}

	wanted := map[string]bool{}
	if on {
		for _, rel := range repos {
			wanted[rel] = true
		}
	}

	settle := func(rel string, g *claudetrust.Grant) {
		keep, w := settleRepoGrant(instanceRoot, instanceKey, rel, g, claudetrust.Retract)
		if w != "" {
			warnings = append(warnings, w)
		}
		if keep {
			grants[rel] = g
		}
	}

	for _, rel := range sortedGrantPaths(existing) {
		g := existing[rel]
		switch {
		case g.Source == claudetrust.SourceReview:
			grants[rel] = g
		case wanted[rel]:
			// The loop below takes it as the prior record.
		default:
			settle(rel, g)
		}
	}

	if on {
		env := claudetrust.EnvFor(a.DeveloperHome)
		for _, rel := range repos {
			prior := existing[rel]
			if prior != nil && prior.Source == claudetrust.SourceReview {
				continue
			}
			key, err := repoTrustKey(instanceRoot, instanceKey, rel)
			if err != nil || key == "" {
				if prior != nil {
					// The record's own ladder names the repository and
					// decides whether the record stays.
					settle(rel, prior)
					continue
				}
				reason := "its directory does not exist"
				if err != nil {
					reason = err.Error()
				}
				warnings = append(warnings, fmt.Sprintf("Claude trust for repository %q not written: %s", rel, reason))
				continue
			}
			out := claudetrust.Ensure(env, key, prior, claudetrust.SourceWorkspace)
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

	return nilIfNoGrants(grants), undos, warnings
}

// nilIfNoGrants returns grants, or nil when it is empty, so a state with no
// repository records writes no key.
func nilIfNoGrants(grants map[string]*claudetrust.Grant) map[string]*claudetrust.Grant {
	if len(grants) == 0 {
		return nil
	}
	return grants
}

// settleRepoGrant is settleGrant for g, the record for the repository
// directory rel: it computes the key g must name with repoTrustKey, so a
// record naming a path outside the instance is refused before anything acts
// on it. instanceKey is the instance root's key. keep reports that the
// record must stay for a later run to retry. A refused path, or a directory
// that no longer exists, has its record dropped with a warning and its
// entry left alone, since niwa can't prove the entry is this repository's.
func settleRepoGrant(instanceRoot, instanceKey, rel string, g *claudetrust.Grant, how claudetrust.WithdrawMode) (keep bool, warning string) {
	key, err := repoTrustKey(instanceRoot, instanceKey, rel)
	switch {
	case errors.Is(err, errRepoTrustPathRefused):
		return false, fmt.Sprintf("Claude trust entry %q in %s left in place: refusing the record for repository %q: %v", g.Key, g.ConfigFile, rel, err)
	case err == nil && key == "":
		return false, repoGoneWarning(g, rel)
	}
	return settleGrant(g, key, err, how)
}

// repoGoneWarning is the warning for g, the record for the repository
// directory rel, once that directory no longer exists: the entry can't be
// checked against it any more, so it is left for the developer to remove.
func repoGoneWarning(g *claudetrust.Grant, rel string) string {
	return fmt.Sprintf(
		"the recorded Claude trust entry %q in %s is for the repository directory %s, which no longer exists; leaving it in place, remove it by hand if it is stale",
		g.Key, g.ConfigFile, rel)
}

// WithdrawRepoClaudeTrust withdraws every workspace grant state records for
// a repository directory in the instance at instanceDir, as destroying the
// instance would: the whole entry for a created grant, only the flag for a
// flag-only one. It runs when niwa watch takes the instance over for a
// review, which must get no repository-directory trust. Each record is
// checked against the resolved path of <instanceDir>/<path> first, and a
// path outside the instance is refused. Records that were withdrawn, or
// can't be acted on, leave state.ClaudeTrustRepos; a record whose withdrawal
// was skipped stays so destroy retries it. It returns one warning per record
// left behind and saves nothing.
func WithdrawRepoClaudeTrust(instanceDir string, state *InstanceState) []string {
	if state == nil || len(state.ClaudeTrustRepos) == 0 {
		return nil
	}
	var warnings []string
	instanceKey, keyErr := claudeTrustKey(instanceDir)
	kept := map[string]*claudetrust.Grant{}
	for _, rel := range sortedGrantPaths(state.ClaudeTrustRepos) {
		g := state.ClaudeTrustRepos[rel]
		if g.Source != claudetrust.SourceWorkspace {
			kept[rel] = g
			continue
		}
		var keep bool
		var w string
		if keyErr != nil {
			keep, w = settleGrant(g, "", keyErr, claudetrust.Destroy)
		} else {
			keep, w = settleRepoGrant(instanceDir, instanceKey, rel, g, claudetrust.Destroy)
		}
		if w != "" {
			warnings = append(warnings, w)
		}
		if keep {
			kept[rel] = g
		}
	}
	state.ClaudeTrustRepos = nilIfNoGrants(kept)
	return warnings
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
//
// The full notice is recorded as noticeClaudeTrustOverlay, together with
// noticeClaudeTrustOverlayRepos when it names repository directories. A
// workspace that holds only the first key was told about the instance root
// alone, by a niwa that didn't trust repository directories yet; once an
// instance in it holds a repository grant, it gets the repository notice
// once and the second key is recorded.
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
	var told []string
	if rootState != nil {
		told = rootState.DisclosedNotices
	}
	switch {
	case !sliceContains(told, noticeClaudeTrustOverlay):
		a.deferOverlayTrustNotice(root, repoGrants)
		keys := []string{noticeClaudeTrustOverlay}
		if len(repoGrants) > 0 {
			keys = append(keys, noticeClaudeTrustOverlayRepos)
		}
		saveWorkspaceRootDisclosures(workspaceRoot, rootState, keys)
	case len(repoGrants) > 0 && !sliceContains(told, noticeClaudeTrustOverlayRepos):
		a.deferOverlayRepoTrustNotice(repoGrants)
		saveWorkspaceRootDisclosures(workspaceRoot, rootState, []string{noticeClaudeTrustOverlayRepos})
	}
}

// deferOverlayRepoTrustNotice queues the notice for a workspace already told
// that the overlay turned instance trust on, now that niwa also trusts the
// repository directories cloned into its instances. repos are one
// instance's repository grants.
func (a *Applier) deferOverlayRepoTrustNotice(repos []*claudetrust.Grant) {
	dirs := fmt.Sprintf("%d repository directories", len(repos))
	if len(repos) == 1 {
		dirs = "1 repository directory"
	}
	a.Reporter.Defer("note: instance trust, on because the workspace overlay sets [claude] trust_instances, now covers the repository directories cloned into each instance as well as its root. "+
		"niwa marked %s in this instance trusted in %s, so Claude Code will apply the project permissions, hooks and MCP servers there without asking, "+
		"including each repository's own committed .claude settings and hooks. "+
		"Set [claude] trust_instances = false in the workspace config to turn it off.", dirs, repos[0].ConfigFile)
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
