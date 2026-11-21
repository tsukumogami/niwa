package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/pluginstore"
)

// This file resolves the workspace's configured plugins to the installed trees
// a skills delivery is made from, and installs that delivery into a repository.
//
// The resolution is niwa's own, end to end. A repository-sourced marketplace
// resolves through the workspace's own clone, as it always has; a github-sourced
// one is fetched into a niwa-owned directory inside the instance with the same
// tarball primitives the config snapshot uses. Nothing here reads another tool's
// user-global state: an earlier attempt resolved github-sourced marketplaces out
// of Claude Code's plugin directory, which made a Codex session's skills depend
// on Claude Code being installed and having fetched the marketplace first -- and
// reported that dependency as a warning whose suggested fix a machine without
// Claude Code could not carry out.

// marketplaceContentDirName is the directory under an instance's own .niwa
// where the content of a remote marketplace is kept. It sits beside the other
// niwa-owned instance state rather than in a repository, because it is one
// instance's cache and not something any working tree should see.
const marketplaceContentDirName = "marketplaces"

// marketplaceContentRoot is where fetched marketplace content lives for one
// instance.
func marketplaceContentRoot(instanceRoot string) string {
	return filepath.Join(instanceRoot, ".niwa", marketplaceContentDirName)
}

// marketplaceManifestPath is the manifest inside a marketplace tree. It is the
// file that says which plugins the marketplace declares and where each one's
// source directory sits, and its presence is what makes a fetched tree usable.
func marketplaceManifestPath(root string) string {
	return filepath.Join(root, ".claude-plugin", "marketplace.json")
}

// MissingPluginRoot records a configured plugin whose installed tree was not
// available when the skills delivery was declared, so nothing was delivered for
// it.
//
// This is reported, never swallowed. A session that is short a plugin's skills
// has no way to notice on its own, and a silent gap is indistinguishable from a
// plugin that has no skills. The report names the path that was expected and
// what went wrong, so the next action is in the message rather than inferred
// from it.
type MissingPluginRoot struct {
	// Plugin is the configured plugin entry, as written in the config.
	Plugin string
	// Path is where niwa expected to find the tree. When the marketplace
	// itself could not be resolved, this is the marketplace root -- the
	// outermost path that was already missing, since the plugin's own
	// subdirectory is declared inside the manifest that is absent.
	Path string
	// Reason is what went wrong, in a form that names the next action.
	Reason string
}

// String renders the report. It is careful about what it claims: what was not
// delivered is the plugin's tree, and an agent that resolves the same plugin
// through its own plugin system is unaffected. Overstating this would tell a
// Claude-only workspace its skills are missing when they are not.
func (m MissingPluginRoot) String() string {
	return fmt.Sprintf("plugin %q: no skills tree delivered -- expected it at %s (%s); an agent that reads skills from a delivered tree gets none of this plugin's for now", m.Plugin, m.Path, m.Reason)
}

// PluginSkillsInputs is what resolving the configured plugins to deliverable
// trees needs from one apply.
type PluginSkillsInputs struct {
	// InstanceRoot is the prepared instance, which owns the marketplace
	// content directory.
	InstanceRoot string

	// Plugins are the configured plugin entries, in "name" or "name@marketplace"
	// form.
	Plugins []string

	// Marketplaces are the configured marketplaces the plugins resolve through.
	Marketplaces config.MarketplaceConfigs

	// RepoIndex maps a managed repo name to its on-disk clone, for resolving a
	// repo:-sourced marketplace.
	RepoIndex map[string]string

	// Fetcher fetches a remote marketplace's content. Nil means this call does
	// no network work: an already-fetched marketplace still resolves from the
	// instance's own content directory, and one that has never been fetched is
	// reported as missing rather than guessed at. The worktree path passes nil
	// for exactly that reason -- it re-delivers from what the instance apply
	// already fetched.
	Fetcher FetchClient

	// Home is the developer's home, where the host plugin store lives. Empty
	// (the unit suites) keeps every marketplace in the instance, as does an
	// instance whose parent is not a workspace root.
	Home string

	// SharingOff is set when the workspace sets share_plugin_releases =
	// false: a marketplace with no entry is fetched into the instance at its
	// ref, while an existing link is still kept and Ensured, and repair and
	// the sweep still run. The zero value is sharing on.
	SharingOff bool

	// Unrestored maps a release id the store's repair pass refused or could
	// not restore in this command to the reason. A link of this instance to
	// one is reported with that reason rather than tried a second time in
	// the same command; the next command's repair tries again.
	Unrestored map[string]string

	// Notices receives a record each time the host plugin store can't be
	// used and a marketplace is fetched into the instance instead, naming the
	// store, its condition and the marketplace. Nil records nothing.
	Notices *fallbacknotice.Collector
}

// ResolvePluginTrees resolves every configured plugin to the tree its skills are
// delivered from, fetching the content of a github-sourced marketplace into the
// instance when it is not already there.
//
// It returns the resolvable trees in a stable order, and one report per plugin
// that did not resolve. A marketplace that cannot be fetched is a report rather
// than an error: an apply run offline still has to prepare everything else, and
// the report says exactly what the session will be short.
func ResolvePluginTrees(ctx context.Context, in PluginSkillsInputs) ([]agentplan.PluginTree, []MissingPluginRoot) {
	if len(in.Plugins) == 0 {
		return nil, nil
	}

	roots, errs := marketplaceRoots(ctx, in)

	var trees []agentplan.PluginTree
	var missing []MissingPluginRoot
	claimed := make(map[string]string, len(in.Plugins))

	for _, entry := range in.Plugins {
		pluginName, marketplaceName := splitPluginEntry(entry)
		if pluginName == "" {
			continue
		}

		root, reason := resolvePluginRoot(pluginName, marketplaceName, roots, errs)
		if reason.Reason != "" {
			reason.Plugin = entry
			missing = append(missing, reason)
			continue
		}

		if prior, dup := claimed[pluginName]; dup {
			if prior != root {
				missing = append(missing, MissingPluginRoot{
					Plugin: entry,
					Path:   root,
					Reason: fmt.Sprintf("another configured plugin already delivers %q from %s", pluginName, prior),
				})
			}
			continue
		}
		claimed[pluginName] = root
		trees = append(trees, agentplan.PluginTree{Name: pluginName, Root: root})
	}

	sort.Slice(trees, func(i, j int) bool { return trees[i].Name < trees[j].Name })
	return trees, missing
}

// marketplaceRoots maps each configured marketplace's registration name to its
// root directory inside this instance, alongside the resolution failures. The
// registration name is the same one the settings writer uses, so a plugin entry
// naming a marketplace cannot mean one thing to one writer and something else
// here.
func marketplaceRoots(ctx context.Context, in PluginSkillsInputs) (map[string]string, map[string]string) {
	roots := make(map[string]string, len(in.Marketplaces))
	errs := make(map[string]string, len(in.Marketplaces))

	for _, mc := range in.Marketplaces {
		source := mc.Source
		name, err := marketplaceRegistrationName(source, in.RepoIndex)
		if err != nil {
			// Key the failure by the source: without a name there is nothing
			// else to key it by, and a plugin entry naming this marketplace is
			// looked up by name anyway -- it falls through to "no configured
			// marketplace", which is the honest answer.
			errs[source] = err.Error()
			continue
		}
		if name == "" {
			continue
		}

		if strings.HasPrefix(source, repoRefPrefix) {
			resolved, resolveErr := ResolveMarketplaceSource(source, in.RepoIndex)
			if resolveErr != nil {
				errs[name] = resolveErr.Error()
				continue
			}
			// The manifest path is <root>/.claude-plugin/marketplace.json; two
			// Dir calls walk back up to the root, exactly as the settings
			// writer does.
			roots[name] = filepath.Dir(filepath.Dir(resolved))
			continue
		}

		root, fetchErr := ensureMarketplaceContent(ctx, in, name, mc)
		if fetchErr != nil {
			errs[name] = fetchErr.Error()
			continue
		}
		roots[name] = root
	}

	return roots, errs
}

// ensureMarketplaceContent makes a github-sourced marketplace's content
// available inside the instance and returns its root, the path
// <instance>/.niwa/marketplaces/<name>.
//
// The entry at that path is classified with Lstat before anything is resolved
// or fetched, and an existing one is kept: an instance stays on the release it
// was created with. A real directory holding a manifest (an instance from
// before the host store, or one that fell back to its own copy) is used as it
// is. A link into the host plugin store is kept on the release it names,
// which Ensure checks is still held. Only when there is no entry is the ref
// picked and, with the store in use and sharing on, resolved to a commit and
// held once on the host, the instance getting a link to it; otherwise the
// content is fetched into the instance at the unresolved ref, as before the
// store existed. When the store can't be used (it can't be read, written or
// created, or can't take the release), the content is fetched into the
// instance at the resolved commit instead, and the fallback is recorded in
// in.Notices with the store's path and its condition.
//
// Re-fetching on every apply would put a network round trip in front of every
// preparation and would swap the tree under a running session for no benefit:
// the content is pinned by the configured track, and removing the entry is how
// a workspace asks for it again.
func ensureMarketplaceContent(ctx context.Context, in PluginSkillsInputs, name string, mc config.MarketplaceConfig) (string, error) {
	if in.InstanceRoot == "" {
		return "", fmt.Errorf("no instance to fetch marketplace %q into", name)
	}

	contentRoot := marketplaceContentRoot(in.InstanceRoot)
	dest := filepath.Join(contentRoot, name)
	// The containment check is on the entry's own path, never on a link's
	// target: a healthy link into the host store points outside the instance
	// by design.
	if name == "." || name == ".." || filepath.Dir(dest) != filepath.Clean(contentRoot) {
		return "", fmt.Errorf("marketplace %q: name escapes the instance content directory", name)
	}

	info, lerr := os.Lstat(dest)
	switch {
	case lerr == nil && info.Mode()&os.ModeSymlink != 0:
		return keepMarketplaceLink(ctx, in, name, dest)
	case lerr == nil && info.IsDir():
		if err := checkContainment(dest, contentRoot); err != nil {
			return "", fmt.Errorf("marketplace %q: name escapes the instance content directory", name)
		}
		if _, err := os.Stat(marketplaceManifestPath(dest)); err == nil {
			return dest, nil
		}
	case lerr != nil && !os.IsNotExist(lerr):
		return "", fmt.Errorf("marketplace %q: inspecting %s: %w", name, dest, lerr)
	}

	if in.Fetcher == nil {
		return "", fmt.Errorf("marketplace %q has not been fetched into this instance, and this run does no fetching", name)
	}

	owner, repo, ok := splitGitHubSource(mc.Source)
	if !ok {
		return "", fmt.Errorf("marketplace source %q is not an \"org/repo\" reference", mc.Source)
	}

	ref := marketplaceContentRef(mc)
	if store, ok := pluginStoreFor(in); ok && !in.SharingOff {
		commit, err := resolveMarketplaceCommit(ctx, in.Fetcher, owner, repo, ref)
		if err != nil {
			return "", fmt.Errorf("resolving marketplace %q at %s: %w", mc.Source, ref, err)
		}
		release, valid := pluginstore.NewRelease(owner, repo, commit, ref, releaseTag(ref))
		if valid {
			if err := clearStaleMarketplaceDir(dest); err != nil {
				return "", fmt.Errorf("replacing marketplace %q: %w", name, err)
			}
			if err := os.MkdirAll(contentRoot, 0o755); err != nil {
				return "", fmt.Errorf("creating %s: %w", contentRoot, err)
			}
			_, cond, err := store.Ensure(ctx, release, dest, nil, nil)
			if cond == pluginstore.CondNone {
				if err != nil {
					return "", fmt.Errorf("holding marketplace %q at %s: %w", mc.Source, ref, err)
				}
				return dest, nil
			}
			// The store can't be used: fall back to a copy in this instance,
			// at the same commit, so the instance holds what the store would
			// have, and say so only once the instance really holds its own
			// copy.
			root, err := fetchMarketplaceIntoInstance(ctx, in.Fetcher, mc.Source, owner, repo, commit, name, dest)
			if err == nil {
				in.Notices.PluginStoreFallback(pluginstore.Dir(store.Home, store.WorkspaceRoot), string(cond), name)
			}
			return root, err
		}
	}

	return fetchMarketplaceIntoInstance(ctx, in.Fetcher, mc.Source, owner, repo, ref, name, dest)
}

// keepMarketplaceLink keeps an instance's existing link at dest on the release
// it names. A link into a host plugin store is Ensured: a held release is
// relinked to this store's path if the link names another, and a missing one
// is fetched back only for a declared source whose commit belongs to the
// source's own history. A link that isn't a release link, and any link on a
// run that does no fetching, is used only if the manifest resolves through
// it.
func keepMarketplaceLink(ctx context.Context, in PluginSkillsInputs, name, dest string) (string, error) {
	if store, ok := pluginStoreFor(in); ok && in.Fetcher != nil {
		if target, err := os.Readlink(dest); err == nil {
			if release, ok := pluginstore.ParseLink(target); ok {
				if reason, failed := in.Unrestored[release.ID()]; failed {
					return "", fmt.Errorf("marketplace %q: release %s was not restored: %s", name, release.Label(), reason)
				}
				_, cond, err := store.Ensure(ctx, release, dest, declaredMarketplaceSources(in.Marketplaces), marketplaceReachable(in.Fetcher))
				switch {
				case err != nil && cond == pluginstore.CondNone:
					return "", fmt.Errorf("marketplace %q: keeping release %s: %w", name, release.Label(), err)
				case cond != pluginstore.CondNone:
					return "", fmt.Errorf("marketplace %q: the plugin store at %s is %s: %v", name, pluginstore.Dir(store.Home, store.WorkspaceRoot), cond, err)
				}
				return dest, nil
			}
		}
	}
	if _, err := os.Stat(marketplaceManifestPath(dest)); err == nil {
		return dest, nil
	}
	return "", fmt.Errorf("marketplace %q: the link at %s does not lead to a marketplace", name, dest)
}

// clearStaleMarketplaceDir removes a real directory at dest that holds no
// manifest, what a fetch interrupted before the host store existed could
// leave, so a link can take its place. Anything else at dest is left alone.
func clearStaleMarketplaceDir(dest string) error {
	info, err := os.Lstat(dest)
	if err != nil || !info.IsDir() {
		return nil
	}
	if _, err := os.Stat(marketplaceManifestPath(dest)); err == nil {
		return nil
	}
	return os.RemoveAll(dest)
}

// fetchMarketplaceIntoInstance fetches the marketplace's tarball at ref into
// a real directory at dest, the per-instance copy the host store replaces.
func fetchMarketplaceIntoInstance(ctx context.Context, fetcher FetchClient, source, owner, repo, ref, name, dest string) (string, error) {
	body, _, status, _, err := fetcher.FetchTarball(ctx, owner, repo, ref, "")
	if body != nil {
		defer body.Close()
	}
	if err != nil {
		return "", fmt.Errorf("fetching marketplace %q at %s: %w", source, ref, err)
	}
	if status != 200 {
		return "", fmt.Errorf("fetching marketplace %q at %s: GitHub returned %d", source, ref, status)
	}

	// Extract into a staging directory and promote it only on success, so a
	// failed fetch never leaves half a marketplace where the next apply would
	// find it and treat it as complete.
	staging := dest + ".staging"
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clearing staging directory for marketplace %q: %w", name, err)
	}
	if err := github.ExtractSubpath(body, "", staging); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("extracting marketplace %q: %w", source, err)
	}
	if _, err := os.Stat(marketplaceManifestPath(staging)); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("marketplace %q carries no .claude-plugin/marketplace.json", source)
	}
	if err := os.RemoveAll(dest); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("replacing marketplace %q: %w", name, err)
	}
	if err := os.Rename(staging, dest); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("promoting marketplace %q: %w", name, err)
	}

	return dest, nil
}

// marketplaceContentRef is the git ref the marketplace's content is fetched at,
// following the same track semantics the registration writer applies: the
// latest stable release by default, the default branch on "main", and anything
// else verbatim. Fetching a different revision from the one Claude registers
// would give the two agents different versions of the same marketplace.
func marketplaceContentRef(mc config.MarketplaceConfig) string {
	switch mc.Track {
	case "", trackRelease:
		if tag, ok := resolveLatestStableRelease(mc.Source); ok {
			return tag
		}
		return "HEAD"
	case trackMain:
		return "HEAD"
	default:
		return mc.Track
	}
}

// splitGitHubSource splits an "org/repo" marketplace source. Anything else --
// a bare name, a URL, a deeper path -- is not one, and the caller reports it
// rather than fetching something it guessed at.
func splitGitHubSource(source string) (owner, repo string, ok bool) {
	parts := strings.SplitN(source, "/", 3)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// resolvePluginRoot finds one plugin's tree. A non-empty Reason in the returned
// MissingPluginRoot means nothing should be delivered; the Plugin field is
// filled in by the caller, which holds the entry as configured.
func resolvePluginRoot(pluginName, marketplaceName string, roots, errs map[string]string) (string, MissingPluginRoot) {
	candidates := roots
	if marketplaceName != "" {
		root, ok := roots[marketplaceName]
		if !ok {
			reason := fmt.Sprintf("marketplace %q is not configured in this workspace", marketplaceName)
			if err, failed := errs[marketplaceName]; failed {
				reason = fmt.Sprintf("marketplace %q did not resolve: %s", marketplaceName, err)
			}
			return "", MissingPluginRoot{Path: "(unresolved)", Reason: reason}
		}
		candidates = map[string]string{marketplaceName: root}
	}

	// A bare entry (no "@marketplace") is searched across every configured
	// marketplace, in a stable order so the same configuration always resolves
	// the same way.
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)

	var lastReason MissingPluginRoot
	for _, name := range names {
		marketplaceRoot := candidates[name]
		source, ok := readPluginSourceDir(marketplaceRoot, pluginName)
		if !ok {
			lastReason = MissingPluginRoot{
				Path:   marketplaceRoot,
				Reason: fmt.Sprintf("marketplace %q is not on disk, or its manifest declares no plugin named %q", name, pluginName),
			}
			continue
		}

		root := filepath.Join(marketplaceRoot, source)
		if err := checkContainment(root, marketplaceRoot); err != nil {
			return "", MissingPluginRoot{
				Path:   root,
				Reason: fmt.Sprintf("marketplace %q declares a source directory outside its own tree", name),
			}
		}

		fi, statErr := os.Stat(root)
		if statErr != nil || !fi.IsDir() {
			lastReason = MissingPluginRoot{
				Path:   root,
				Reason: fmt.Sprintf("marketplace %q declares the plugin, but its source directory is not there", name),
			}
			continue
		}
		return root, MissingPluginRoot{}
	}

	if lastReason.Reason == "" {
		lastReason = MissingPluginRoot{
			Path:   "(unresolved)",
			Reason: "no configured marketplace declares a plugin by this name",
		}
	}
	return "", lastReason
}

// readPluginSourceDir reads the plugin's declared source directory from the
// marketplace manifest. It returns ("", false) when the manifest is missing,
// unparseable, declares no plugin by that name, or declares a source niwa
// cannot turn into a local directory (an external source object, for instance)
// -- every one of which is a case the caller reports rather than guesses past.
func readPluginSourceDir(marketplaceRoot, pluginName string) (string, bool) {
	data, err := os.ReadFile(marketplaceManifestPath(marketplaceRoot))
	if err != nil {
		return "", false
	}

	var manifest struct {
		Plugins []struct {
			Name   string          `json:"name"`
			Source json.RawMessage `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false
	}

	for _, p := range manifest.Plugins {
		if p.Name != pluginName {
			continue
		}
		if len(p.Source) == 0 {
			// An omitted source means the marketplace root is the plugin root.
			return ".", true
		}
		var source string
		if err := json.Unmarshal(p.Source, &source); err != nil {
			return "", false
		}
		if source == "" {
			return ".", true
		}
		return source, true
	}

	return "", false
}

// splitPluginEntry splits a configured plugin entry into its plugin name and
// marketplace name. The marketplace half is read with pluginMarketplace, the
// same parse the root-hoisting filter uses, so the two cannot disagree about
// which marketplace an entry binds to.
func splitPluginEntry(entry string) (string, string) {
	entry = strings.TrimSpace(entry)
	marketplace := pluginMarketplace(entry)
	if marketplace == "" {
		return strings.TrimSuffix(entry, "@"), ""
	}
	return entry[:len(entry)-len(marketplace)-1], marketplace
}

// instanceRepoIndex reads an instance's own layout back into the repo-name to
// clone-directory map that marketplace resolution needs: one entry per
// `<instanceRoot>/<group>/<repo>` directory.
//
// The instance apply builds the same map from the repositories it just
// classified, which is the better source when there is one. This exists for the
// worktree path, which prepares one worktree of one repository and never
// classifies the rest -- and still has to resolve a marketplace that lives in a
// sibling repository. Dot-named groups are skipped, which keeps niwa's own
// .niwa out of the walk, and a dot-named repo directory counts only when the
// instance state declares it (isRepoDirName), as the apply's classified set
// would hold it.
func instanceRepoIndex(instanceRoot string) map[string]string {
	index := map[string]string{}
	groups, err := os.ReadDir(instanceRoot)
	if err != nil {
		return index
	}
	declared := declaredRepoNames(instanceRoot)
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, g.Name())
		repos, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, r := range repos {
			if !r.IsDir() || !isRepoDirName(r.Name(), declared) {
				continue
			}
			if _, taken := index[r.Name()]; taken {
				continue
			}
			index[r.Name()] = filepath.Join(groupDir, r.Name())
		}
	}
	return index
}

// RepoSkillsResult reports what one repository's skills delivery produced.
type RepoSkillsResult struct {
	// Delivered are the paths the delivery wrote, one per plugin tree.
	Delivered []string

	// Excludes are the git-exclude patterns the delivery implies, relative to
	// the working tree it landed in.
	Excludes []string

	// Warnings are what the user needs to hear about the delivery: an entry in
	// the skills directory niwa did not put there, most of all.
	Warnings []string
}

// InstallRepoSkills delivers the resolved plugin trees into one repository (or
// worktree) for one agent.
//
// The producer decides the layout, whether the trees are delivered at all, and
// which names belong in the directory afterwards. This function reconciles what
// is there against that set and executes the plan; like the content installer,
// it never learns which agent it is delivering for.
func InstallRepoSkills(repoDir string, trees []agentplan.PluginTree, producer agentplan.Producer) (*RepoSkillsResult, error) {
	in := agentplan.SkillsInputs{Dir: repoDir, Plugins: trees}

	result := &RepoSkillsResult{}

	warnings, err := reconcileSkillsDir(producer.SkillsReconcileSpec(in))
	if err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, warnings...)

	// There is deliberately no containment pass over the plan here, unlike the
	// content installer's. A delivery is a link out of the working tree by
	// design, so the containment check -- which resolves symlinks on the way to
	// its answer -- would read every successful second apply as an escape. What
	// keeps a delivery inside the tree is the producer's own rule that a
	// delivery name is a single path element, applied where the path is built.
	plan, err := producer.SkillsPlan(in)
	if err != nil {
		return nil, err
	}
	delivered, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, err
	}
	result.Delivered = delivered
	result.Excludes = excludes

	return result, nil
}

// reconcileSkillsDir removes the deliveries in the skills directory that no
// longer belong there, so three applies leave the same set as one and a plugin
// dropped from the configuration stops reaching sessions.
//
// It removes only what niwa delivered: a symlink, which nothing else plants at
// these names, and a directory carrying the delivery sentinel. Anything else is
// left exactly as it is and reported -- the directory is niwa's, so an entry it
// did not write means something unexpected happened, and deleting it to make
// room is not a call this reconciliation should make on its own.
func reconcileSkillsDir(spec agentplan.SkillsReconcileSpec) ([]string, error) {
	if spec.Dir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(spec.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills directory %s: %w", spec.Dir, err)
	}

	keep := make(map[string]bool, len(spec.Keep))
	for _, name := range spec.Keep {
		keep[name] = true
	}

	var warnings []string
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		path := filepath.Join(spec.Dir, e.Name())
		info, lstatErr := os.Lstat(path)
		if lstatErr != nil {
			return nil, fmt.Errorf("inspecting %s: %w", path, lstatErr)
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
		case info.IsDir() && treeCopyIsNiwas(path, spec.Marker):
		default:
			warnings = append(warnings, fmt.Sprintf("%s is not something niwa delivered; leaving it as it is", path))
			continue
		}
		if rmErr := os.RemoveAll(path); rmErr != nil {
			return nil, fmt.Errorf("removing de-configured delivery %s: %w", path, rmErr)
		}
	}

	return warnings, nil
}

// pluginStoreFor returns the host plugin store this resolution places into,
// and whether there is one. There is one when the developer's home is known
// and the instance's parent is a workspace root that holds its config: a
// single-instance layout keeps its marketplaces in the instance and writes
// nothing under ~/.niwa for them.
//
// share_plugin_releases does not change its answer. The setting governs
// placing new marketplaces only, which ensureMarketplaceContent decides on
// its own; the kept-link Ensure, repair and the sweep also ask this function,
// and an instance linked before the setting was turned off has to keep being
// Ensured and repaired, and its releases kept from the sweep, while it
// exists.
func pluginStoreFor(in PluginSkillsInputs) (pluginstore.Store, bool) {
	if in.Home == "" || in.InstanceRoot == "" {
		return pluginstore.Store{}, false
	}
	workspaceRoot := filepath.Dir(in.InstanceRoot)
	if !workspaceRootHoldsState(workspaceRoot) {
		return pluginstore.Store{}, false
	}
	fetcher := in.Fetcher
	return pluginstore.Store{
		Home:          in.Home,
		WorkspaceRoot: workspaceRoot,
		Fetch: func(ctx context.Context, owner, repo, commit string) (io.ReadCloser, error) {
			if fetcher == nil {
				return nil, fmt.Errorf("this run does no fetching")
			}
			body, _, status, _, err := fetcher.FetchTarball(ctx, owner, repo, commit, "")
			if err != nil {
				if body != nil {
					body.Close()
				}
				return nil, err
			}
			if status != 200 || body == nil {
				if body != nil {
					body.Close()
				}
				return nil, fmt.Errorf("GitHub returned %d", status)
			}
			return body, nil
		},
	}, true
}

// commitPattern is a full commit id as the commits endpoint returns it.
var commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// resolveMarketplaceCommit takes the ref to the commit it names, with one
// small request, so the release is keyed and fetched by the commit and the
// content matches its key even if the ref moves meanwhile.
func resolveMarketplaceCommit(ctx context.Context, fetcher FetchClient, owner, repo, ref string) (string, error) {
	oid, _, status, err := fetcher.HeadCommit(ctx, owner, repo, ref, "")
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("GitHub returned %d", status)
	}
	if !commitPattern.MatchString(oid) {
		return "", fmt.Errorf("GitHub answered with something that is not a commit id")
	}
	return strings.ToLower(oid), nil
}

// releaseTag is ref when it is a stable release tag, the one shape a message
// names, and "" otherwise.
func releaseTag(ref string) string {
	if _, ok := parseStableSemver(ref); ok {
		return ref
	}
	return ""
}

// declaredMarketplaceSources is the set of github marketplace sources the
// effective config declares, lowercased "owner/repo". A release a link names
// is fetched back only for one of them.
func declaredMarketplaceSources(mcs config.MarketplaceConfigs) map[string]bool {
	declared := map[string]bool{}
	for _, mc := range mcs {
		if strings.HasPrefix(mc.Source, repoRefPrefix) {
			continue
		}
		if owner, repo, ok := splitGitHubSource(mc.Source); ok {
			declared[strings.ToLower(owner)+"/"+strings.ToLower(repo)] = true
		}
	}
	return declared
}

// commitComparer is the part of the GitHub client the reachability check
// uses beyond FetchClient.
type commitComparer interface {
	CompareCommits(ctx context.Context, owner, repo, base, head string) (github.Ancestry, error)
}

// marketplaceReachable is the check a commit taken from a link must pass
// before it is fetched: a tag or branch tip `git ls-remote` of the source
// lists, or an ancestor of one by the compare endpoint.
func marketplaceReachable(fetcher FetchClient) pluginstore.Reachable {
	var behind pluginstore.Behind
	if cmp, ok := fetcher.(commitComparer); ok {
		behind = func(ctx context.Context, owner, repo, base, commit string) (bool, error) {
			a, err := cmp.CompareCommits(ctx, owner, repo, base, commit)
			if err != nil {
				return false, err
			}
			return a == github.AncestryAncestor, nil
		}
	}
	return pluginstore.NewReachable(lsRemoteTips, behind)
}

// lsRemoteTips lists the source's refs with `git ls-remote`, annotated tags
// peeled, so a tag names the commit it points at.
func lsRemoteTips(ctx context.Context, owner, repo string) (map[string]string, error) {
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "https://github.com/"+owner+"/"+repo).Output()
	if err != nil {
		return nil, fmt.Errorf("listing the refs of %s/%s: %w", owner, repo, err)
	}
	tips := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !commitPattern.MatchString(fields[0]) {
			continue
		}
		name, commit := fields[1], strings.ToLower(fields[0])
		if peeled, ok := strings.CutSuffix(name, "^{}"); ok {
			tips[peeled] = commit
			continue
		}
		if _, seen := tips[name]; !seen {
			tips[name] = commit
		}
	}
	return tips, nil
}

// repairPluginStore runs the host plugin store's repair pass for the
// workspace this instance belongs to, once per command per workspace, and
// reports what it did through the run's notices: the terminal for apply and
// create, dispatch's stderr, and the SessionStart hook's context. It restores
// every release an instance on disk links to that is missing, unmarked, or
// changed after it was placed, so a deleted or tampered release comes back
// with nobody acting and no command run inside the instances it serves. It
// writes only inside the store.
//
// A run that does no fetching, or a layout with no store, has nothing to
// repair.
func (a *Applier) repairPluginStore(ctx context.Context, in PluginSkillsInputs, notices *fallbacknotice.Collector) {
	if in.Fetcher == nil {
		return
	}
	store, ok := pluginStoreFor(in)
	if !ok {
		return
	}
	dir := pluginstore.Dir(store.Home, store.WorkspaceRoot)
	if a.pluginStoresRepaired[dir] {
		return
	}
	if a.pluginStoresRepaired == nil {
		a.pluginStoresRepaired = map[string]bool{}
	}
	if a.pluginUnrestored == nil {
		a.pluginUnrestored = map[string]string{}
	}
	a.pluginStoresRepaired[dir] = true

	res := store.Repair(ctx, declaredMarketplaceSources(in.Marketplaces), marketplaceReachable(in.Fetcher))
	record := func(outcome fallbacknotice.ReleaseOutcome, entries []pluginstore.RepairEntry, unrestored bool) {
		for _, e := range entries {
			notices.PluginRelease(fallbacknotice.ReleaseNote{
				Outcome:   outcome,
				Release:   e.Release.Name(),
				Instances: e.Instances,
				Reason:    e.Reason,
			})
			if unrestored {
				a.pluginUnrestored[e.Release.ID()] = e.Reason
			}
		}
	}
	record(fallbacknotice.ReleaseRestored, res.Restored, false)
	record(fallbacknotice.ReleaseTampered, res.Tampered, false)
	record(fallbacknotice.ReleaseRefused, res.Refused, true)
	record(fallbacknotice.ReleaseUndeclared, res.Undeclared, false)
	record(fallbacknotice.ReleaseNotRestored, res.Failed, true)
	// The scan error is kept rather than printed: the sweep, which meets the
	// same failure, says it once (see sweepPluginStore).
	if res.ScanErr != nil {
		if a.pluginScanErr == nil {
			a.pluginScanErr = map[string]error{}
		}
		a.pluginScanErr[dir] = res.ScanErr
	}
}

// pluginScanWarning is repair's line for an instance listing that failed.
const pluginScanWarning = "plugin store: not every instance could be listed for its release links: %v"

// sweepPluginStore runs the host plugin store's sweep for the workspace this
// instance belongs to, once per command per store, after this instance is
// linked. It deletes the held releases no instance on disk links to, except
// the newest of each source, and logs each one it removed.
//
// A sweep that deleted nothing because the scan of the instances or the
// store failed is reported through the run's notices, naming the path, and
// for a store that couldn't be used, the store and its condition, so it
// reaches the hook's context as well as the terminal; repair's own warning
// about the same listing is then not printed. When the sweep succeeds,
// repair's kept warning is printed here. The sweep never fetches, so it runs
// on a run that does no fetching too.
func (a *Applier) sweepPluginStore(ctx context.Context, in PluginSkillsInputs, notices *fallbacknotice.Collector) {
	store, ok := pluginStoreFor(in)
	if !ok {
		return
	}
	dir := pluginstore.Dir(store.Home, store.WorkspaceRoot)
	if a.pluginStoresSwept[dir] {
		return
	}
	if a.pluginStoresSwept == nil {
		a.pluginStoresSwept = map[string]bool{}
	}
	a.pluginStoresSwept[dir] = true

	res, err := store.Sweep(ctx, nil)
	scanErr := a.pluginScanErr[dir]
	delete(a.pluginScanErr, dir)
	if err != nil {
		reason := err.Error()
		var serr *pluginstore.SweepError
		if errors.As(err, &serr) && !serr.ScanFailed() {
			reason = fmt.Sprintf("the plugin store at %s is %s: %s", dir, serr.Cond, err.Error())
		}
		notices.PluginSweepStopped(reason, len(res.Removed))
	} else if scanErr != nil && a.Reporter != nil {
		a.Reporter.Warn(pluginScanWarning, scanErr)
	}
	if a.Reporter != nil {
		for _, r := range res.Removed {
			a.Reporter.Log("plugin store: removed unused release %s", r.Name())
		}
	}
}

// flushPluginScanWarnings prints repair's warning for every store whose scan
// error no sweep has taken, and forgets it. runPipeline defers it, so a pass
// that repairs and then returns before the sweep still prints the warning
// once.
func (a *Applier) flushPluginScanWarnings() {
	dirs := make([]string, 0, len(a.pluginScanErr))
	for dir := range a.pluginScanErr {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if a.Reporter != nil {
			a.Reporter.Warn(pluginScanWarning, a.pluginScanErr[dir])
		}
		delete(a.pluginScanErr, dir)
	}
}
