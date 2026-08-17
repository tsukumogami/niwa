package workspace

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// CodexPayloadDirName is the directory niwa owns inside an instance, and the
// name every cloned repository links to. Codex discovers a `.codex` directory
// during its walk and reads general config and skills from it, which is what
// makes one directory enough to carry the whole payload.
const CodexPayloadDirName = ".codex"

// codexPayloadConfigName is the general-config file inside the payload. Its one
// required job is declaring the context budget (Decision 2 and "The byte
// budget"); niwa writes nothing else into it -- no credentials, no login method,
// no hook definitions or hook state (Decisions 5 and 6).
const codexPayloadConfigName = "config.toml"

// codexPayloadSkillsDirName holds one symlink per configured plugin, each
// pointing at a whole installed plugin tree (Decision 3A).
const codexPayloadSkillsDirName = "skills"

// The byte budget. Codex spends one `project_doc_max_bytes` counter across the
// whole discovery chain, outermost-first, and truncation is a raw byte cut with
// no marker in the text and nothing on stderr -- so an under-declared budget
// silently eats the innermost layer, the one closest to the work. The design
// asks for "generous headroom rather than a tested-once number": the sizing
// inputs are read at apply time, and a committed context file in a repository
// subdirectory can grow between applies with no signal at all. The margin is
// what covers that window; the next apply re-sizes.
const (
	// codexBudgetHeadroomFactor multiplies the largest chain this apply
	// measured. A file would have to quadruple between applies before the
	// declared budget stopped covering it.
	codexBudgetHeadroomFactor = 4
	// codexBudgetFloor is the smallest budget niwa will declare, four times
	// Codex's own 32768 default. It keeps a workspace whose chain is currently
	// tiny (or empty) from declaring a value that a single new context file
	// would overrun.
	codexBudgetFloor = 131072
	// codexBudgetGranularity rounds the declared value up to a whole multiple,
	// so the number in the file is a round one and small content edits do not
	// churn the payload on every apply.
	codexBudgetGranularity = 65536
)

// claudePluginsRoot resolves Claude Code's user-global plugin directory, where
// a github-sourced marketplace's clone lives. It is a package variable so tests
// can point it at a fixture tree instead of the developer's real home, matching
// the seam pattern used for the plugin pre-warm.
var claudePluginsRoot = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "plugins"), nil
}

// MissingPluginRoot records a configured plugin whose installed tree was not on
// disk when the payload was written, so no skills link was created for it.
//
// This is reported, never swallowed. A Claude session self-heals by installing
// the plugin at startup; a Codex session has no equivalent, and the pre-warm
// that populates the tree is explicitly best-effort (skippable, dependent on the
// `claude` binary, able to fail or time out). So "the next apply will repair it"
// is only true once the install itself succeeds -- which is exactly what this
// report tells the developer to go and do. A silent dangling symlink would say
// nothing at all (Decision 3A, driver D4).
type MissingPluginRoot struct {
	// Plugin is the configured plugin entry, as written in the config.
	Plugin string
	// Path is where niwa expected to find the tree. When the marketplace
	// manifest itself could not be read, this is the marketplace root -- the
	// outermost path that was already missing, since the plugin's own
	// subdirectory is declared inside the manifest that is absent.
	Path string
	// Reason is what went wrong, in a form that names the next action.
	Reason string
}

func (m MissingPluginRoot) String() string {
	return fmt.Sprintf("plugin %q: no skills link written -- expected its installed tree at %s (%s); Codex sessions in this instance get none of its skills until it is installed and `niwa apply` re-runs", m.Plugin, m.Path, m.Reason)
}

// CodexPayloadResult reports what one payload write produced.
type CodexPayloadResult struct {
	// WrittenFiles are the regular files the payload owns, for managed-file
	// tracking. The skills symlinks are deliberately absent: they are
	// reconciled against the configured plugin set by this writer itself, and
	// hashing a link that points at a directory is not a thing the managed-file
	// pipeline can do.
	WrittenFiles []string
	// SkillLinks are the plugin symlinks present after reconciliation.
	SkillLinks []string
	// Budget is the value declared in config.toml, in bytes.
	Budget int
	// MissingRoots are the configured plugins that got no link. The caller
	// reports every one of them.
	MissingRoots []MissingPluginRoot
	// Warnings are payload-level anomalies that are not missing roots (an entry
	// in the skills directory that niwa did not write, for instance).
	Warnings []string
}

// CodexBudgetInputs are the files whose sizes set the declared budget.
type CodexBudgetInputs struct {
	// ComposedFiles are the Codex context documents this apply wrote: the
	// instance-root and group `AGENTS.md` files and every repository's
	// `AGENTS.override.md`. Only the largest matters -- a session reads one
	// chain, not all of them.
	ComposedFiles []string
	// RepoDirs are the cloned repository roots. Each is scanned for committed
	// context files in subdirectories *below* the root, which is the part of a
	// chain niwa does not write and cannot bound: they are read after the
	// override and therefore pay for its bytes first.
	RepoDirs []string
}

// InstallCodexPayload writes `<instanceRoot>/.codex`: the general config
// declaring the context budget, and one symlink per configured plugin pointing
// at that plugin's whole installed tree.
//
// The unit of delivery is the plugin directory, not the skill directory, and
// nothing about it is transformed. Skill namespacing derives from the nearest
// `plugin.json` above the skill on disk and Codex canonicalizes a symlinked
// skill path before probing for it, so a symlinked plugin tree yields the same
// `<plugin>:<skill>` names the plugin cache does -- for free, with no
// frontmatter rewriting and no variable substitution. Copying skills loose would
// lose the namespace and orphan every plugin-root `references/` and `scripts/`
// file the skills point at (Decision 3A).
//
// Every apply rewrites the config and reconciles the link set against the
// configured plugins, so a de-configured plugin's link goes away and a link
// whose target niwa owns is repaired rather than duplicated.
func InstallCodexPayload(cfg *config.WorkspaceConfig, instanceRoot string, repoIndex map[string]string, budget CodexBudgetInputs) (*CodexPayloadResult, error) {
	effective := MergeInstanceOverrides(cfg)

	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating Codex payload directory: %w", err)
	}

	declared := codexBudgetFor(budget)
	configPath := filepath.Join(payloadDir, codexPayloadConfigName)
	if err := os.WriteFile(configPath, []byte(renderCodexPayloadConfig(declared)), 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", configPath, err)
	}

	result := &CodexPayloadResult{
		WrittenFiles: []string{configPath},
		Budget:       declared,
	}

	roots, missing := resolveCodexPluginRoots(effective.Plugins, effective.Claude.Marketplaces, repoIndex)
	result.MissingRoots = missing

	links, warnings, err := reconcileCodexSkillLinks(filepath.Join(payloadDir, codexPayloadSkillsDirName), roots)
	if err != nil {
		return nil, err
	}
	result.SkillLinks = links
	result.Warnings = warnings

	return result, nil
}

// renderCodexPayloadConfig renders the payload's config.toml. The comment is
// part of the deliverable: the file is regenerated on every apply, and the
// number in it is derived rather than chosen, so a reader who finds it needs to
// know both facts before editing it.
func renderCodexPayloadConfig(budget int) string {
	var b strings.Builder
	b.WriteString("# " + CodexGenerationMarker + "\n")
	b.WriteString("#\n")
	b.WriteString("# project_doc_max_bytes is one counter spent across the whole context chain,\n")
	b.WriteString("# root-first, and Codex truncates in silence: no marker in the text, nothing on\n")
	b.WriteString("# stderr. Under-declaring it therefore costs the innermost layer -- the context\n")
	b.WriteString("# closest to the work -- with no signal that anything was lost. niwa sizes this\n")
	b.WriteString("# value from the largest chain it measured at apply time and multiplies it for\n")
	b.WriteString("# headroom, so a context file that grows between applies stays covered.\n")
	fmt.Fprintf(&b, "project_doc_max_bytes = %d\n", budget)
	return b.String()
}

// codexBudgetFor sizes the declared budget from what is on disk: the largest
// composed document, plus the committed context files a session would read
// after it in the same chain, times a headroom factor and floored.
//
// The two terms add because they are spent from the same counter in the same
// walk. The composed override is read first (it sits at the repository root),
// so its bytes come out of the budget before any subdirectory file gets a
// chance; sizing on either term alone would leave the other one paying.
func codexBudgetFor(in CodexBudgetInputs) int {
	largest := 0
	for _, path := range in.ComposedFiles {
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			if n := int(fi.Size()); n > largest {
				largest = n
			}
		}
	}

	deepest := 0
	for _, dir := range in.RepoDirs {
		if n := committedContextBytesBelow(dir); n > deepest {
			deepest = n
		}
	}

	budget := (largest + deepest) * codexBudgetHeadroomFactor
	if budget < codexBudgetFloor {
		budget = codexBudgetFloor
	}
	if rem := budget % codexBudgetGranularity; rem != 0 {
		budget += codexBudgetGranularity - rem
	}
	return budget
}

// committedContextBytesBelow sums the sizes of committed context files in
// subdirectories below repoDir. The file at the root itself is excluded: it is
// inlined into the composed override, so its bytes are already counted there.
//
// Dot-directories are skipped, which keeps `.git` (and the payload link a later
// issue plants) out of the walk. The sum is an upper bound -- a session walks
// one path down, not every path -- and an upper bound is the right side to err
// on for a budget.
func committedContextBytesBelow(repoDir string) int {
	total := 0
	contextName := agent.AgentCodex.RootContextFileName()

	_ = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree contributes nothing measurable; the headroom
			// factor is what covers what we cannot see.
			return nil //nolint:nilerr // best-effort sizing walk
		}
		if d.IsDir() {
			if path != repoDir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != contextName || filepath.Dir(path) == repoDir {
			return nil
		}
		if fi, statErr := d.Info(); statErr == nil && fi.Mode().IsRegular() {
			total += int(fi.Size())
		}
		return nil
	})

	return total
}

// codexPluginRoot pairs the link name niwa writes with the installed tree it
// points at.
type codexPluginRoot struct {
	// name is the plugin's own name, used as the link name. Namespacing does
	// not depend on it -- Codex reads that from the plugin manifest inside the
	// target -- so the name is here for a human reading the payload.
	name string
	// root is the absolute path of the whole plugin tree.
	root string
}

// resolveCodexPluginRoots resolves every configured plugin to its installed
// tree, returning the resolvable ones and a report for each one that is not.
//
// Resolution is per marketplace kind, because niwa's plugin model is name-based
// end to end: it registers marketplaces and plugins in Claude's settings and
// shells out to the Claude CLI to install them, and the only on-disk path it
// computes today is a repository-sourced marketplace's root. Both kinds end at
// the same join, though -- marketplace root plus the plugin's declared source
// directory -- so the two differ only in how the marketplace root is found: a
// repository-sourced one resolves through the workspace's own clone, a
// github-sourced one through Claude Code's user-global plugin directory, where
// `claude plugin marketplace add` puts the clone.
func resolveCodexPluginRoots(plugins []string, marketplaces config.MarketplaceConfigs, repoIndex map[string]string) ([]codexPluginRoot, []MissingPluginRoot) {
	if len(plugins) == 0 {
		return nil, nil
	}

	marketplaceRoots, marketplaceErrs := codexMarketplaceRoots(marketplaces, repoIndex)

	var roots []codexPluginRoot
	var missing []MissingPluginRoot
	claimed := make(map[string]string, len(plugins))

	for _, entry := range plugins {
		pluginName, marketplaceName := splitPluginEntry(entry)
		if pluginName == "" {
			continue
		}

		root, reason := resolvePluginRoot(pluginName, marketplaceName, marketplaceRoots, marketplaceErrs)
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
					Reason: fmt.Sprintf("another configured plugin already links %q to %s", pluginName, prior),
				})
			}
			continue
		}
		claimed[pluginName] = root
		roots = append(roots, codexPluginRoot{name: pluginName, root: root})
	}

	sort.Slice(roots, func(i, j int) bool { return roots[i].name < roots[j].name })
	return roots, missing
}

// codexMarketplaceRoots maps each configured marketplace's registration name to
// its root directory on disk, alongside the resolution failures. The
// registration name is the same one the Claude settings writer uses, so the two
// agents cannot disagree about which marketplace a plugin entry names.
func codexMarketplaceRoots(marketplaces config.MarketplaceConfigs, repoIndex map[string]string) (map[string]string, map[string]string) {
	roots := make(map[string]string, len(marketplaces))
	errs := make(map[string]string, len(marketplaces))

	for _, mc := range marketplaces {
		source := mc.Source
		name, err := marketplaceRegistrationName(source, repoIndex)
		if err != nil {
			// Key the failure by the source: without a name there is nothing
			// else to key it by, and a plugin entry naming this marketplace is
			// looked up by name anyway -- it will fall through to "no
			// configured marketplace", which is the honest answer here.
			errs[source] = err.Error()
			continue
		}
		if name == "" {
			continue
		}

		if strings.HasPrefix(source, repoRefPrefix) {
			resolved, resolveErr := ResolveMarketplaceSource(source, repoIndex)
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

		// A github-sourced marketplace is cloned into Claude Code's user-global
		// plugin directory, keyed by the same registration name.
		pluginsRoot, rootErr := claudePluginsRoot()
		if rootErr != nil {
			errs[name] = rootErr.Error()
			continue
		}
		roots[name] = filepath.Join(pluginsRoot, "marketplaces", name)
	}

	return roots, errs
}

// resolvePluginRoot finds one plugin's installed tree. A non-empty Reason in the
// returned MissingPluginRoot means no link should be written; the Plugin field
// is filled in by the caller, which holds the entry as configured.
func resolvePluginRoot(pluginName, marketplaceName string, marketplaceRoots, marketplaceErrs map[string]string) (string, MissingPluginRoot) {
	candidates := marketplaceRoots
	if marketplaceName != "" {
		root, ok := marketplaceRoots[marketplaceName]
		if !ok {
			reason := fmt.Sprintf("marketplace %q is not configured in this workspace", marketplaceName)
			if err, failed := marketplaceErrs[marketplaceName]; failed {
				reason = fmt.Sprintf("marketplace %q did not resolve: %s", marketplaceName, err)
			}
			return "", MissingPluginRoot{Path: "(unresolved)", Reason: reason}
		}
		candidates = map[string]string{marketplaceName: root}
	}

	// A bare entry (no "@marketplace") is searched across every configured
	// marketplace, in a stable order so the same config always resolves the
	// same way.
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
				Reason: "the plugin is declared but not installed (the pre-warm that installs it is best-effort, and can be skipped, absent, or timed out)",
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
// marketplace manifest niwa already opens for the marketplace name. It returns
// ("", false) when the manifest is missing, unparseable, declares no plugin by
// that name, or declares a source niwa cannot turn into a local directory (an
// external source object, for instance) -- every one of which is a case the
// caller reports rather than guesses through.
func readPluginSourceDir(marketplaceRoot, pluginName string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(marketplaceRoot, ".claude-plugin", "marketplace.json"))
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
// marketplace name. Entries take the form "name@marketplace" or a bare "name",
// matching what the Claude settings writer accepts.
func splitPluginEntry(entry string) (string, string) {
	entry = strings.TrimSpace(entry)
	name, marketplace, found := strings.Cut(entry, "@")
	if !found {
		return name, ""
	}
	return name, marketplace
}

// reconcileCodexSkillLinks brings the skills directory to exactly one symlink
// per resolvable plugin. It creates missing links, retargets links that point
// somewhere other than the current install root, and removes links for plugins
// the config no longer declares -- so three applies leave the same set as one.
//
// Entries that are not symlinks are left alone and reported. niwa owns this
// directory, so such an entry means something unexpected happened; deleting a
// real directory to make room for a link is not a call this writer should make
// on its own.
func reconcileCodexSkillLinks(skillsDir string, roots []codexPluginRoot) ([]string, []string, error) {
	desired := make(map[string]string, len(roots))
	for _, r := range roots {
		desired[r.name] = r.root
	}

	if len(desired) == 0 {
		// Nothing to link. Remove any links a previous apply left behind, then
		// leave the directory in place if anything else is in it.
		if err := pruneCodexSkillLinks(skillsDir, desired); err != nil {
			return nil, nil, err
		}
		return nil, nil, nil
	}

	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating Codex skills directory: %w", err)
	}
	if err := pruneCodexSkillLinks(skillsDir, desired); err != nil {
		return nil, nil, err
	}

	var links []string
	var warnings []string
	for _, r := range roots {
		linkPath := filepath.Join(skillsDir, r.name)

		info, err := os.Lstat(linkPath)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			target, readErr := os.Readlink(linkPath)
			if readErr == nil && target == r.root {
				links = append(links, linkPath)
				continue
			}
			// A link niwa owns whose target moved (or went stale): retarget it.
			if rmErr := os.Remove(linkPath); rmErr != nil {
				return nil, nil, fmt.Errorf("replacing stale skills link %s: %w", linkPath, rmErr)
			}
		case err == nil:
			warnings = append(warnings, fmt.Sprintf("skills entry %s is not a niwa-written symlink; leaving it as it is and writing no link for plugin %q", linkPath, r.name))
			continue
		case !os.IsNotExist(err):
			return nil, nil, fmt.Errorf("inspecting skills link %s: %w", linkPath, err)
		}

		if err := os.Symlink(r.root, linkPath); err != nil {
			return nil, nil, fmt.Errorf("linking plugin %q into the Codex payload: %w", r.name, err)
		}
		links = append(links, linkPath)
	}

	return links, warnings, nil
}

// pruneCodexSkillLinks removes symlinks in skillsDir whose name is not in the
// desired set. It only ever removes symlinks: a de-configured plugin's link is
// niwa's to clean up, but anything else in the directory is not.
func pruneCodexSkillLinks(skillsDir string, desired map[string]string) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading Codex skills directory: %w", err)
	}

	for _, e := range entries {
		if _, keep := desired[e.Name()]; keep {
			continue
		}
		info, lstatErr := os.Lstat(filepath.Join(skillsDir, e.Name()))
		if lstatErr != nil || info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if rmErr := os.Remove(filepath.Join(skillsDir, e.Name())); rmErr != nil {
			return fmt.Errorf("removing de-configured skills link %s: %w", e.Name(), rmErr)
		}
	}

	return nil
}
