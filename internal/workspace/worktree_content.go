package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/gitexclude"
	"github.com/tsukumogami/niwa/internal/keyreport"
)

// worktreeApplyEvent is the worktree-lifecycle event run by ApplyToWorktree on
// both `niwa worktree create` and `niwa worktree apply`. create internally runs
// the apply path, so a single event covers both (mirroring how instance create
// runs the apply pipeline).
const worktreeApplyEvent = "apply"

// worktreeRulesFile is the per-worktree rules import file. A worktree, when
// launched as its own Claude Code project root, does not inherit the instance
// root's .claude/rules/ (rules load for the launched root only, not walked-up
// parents). So the worktree needs its own import pointing at the instance's
// workspace-context.md (and overlay/global where present).
const worktreeRulesFile = ".claude/rules/worktree-imports.md"

// worktreeContextHeading marks the generated purpose/branch section appended
// to the worktree's CLAUDE.local.md. It is a stable sentinel so the section
// can be replaced idempotently on re-apply rather than duplicated.
const worktreeContextHeading = "## Worktree Context (niwa worktree)"

// repoMaterializeInputs bundles the inputs the per-repo materializer loop needs.
// Both the instance apply pipeline (apply.go) and ApplyToWorktree construct one
// of these and call runRepoMaterializers, so the two paths share the exact same
// materializer invocation logic (no forked installer).
type repoMaterializeInputs struct {
	Cfg                   *config.WorkspaceConfig
	ConfigDir             string
	RepoName              string
	RepoDir               string
	DiscoveredHooks       config.HooksConfig
	DiscoveredEnv         *DiscoveredEnv
	RepoIndex             map[string]string
	SourceTuples          map[string][]SourceEntry
	AllowPlaintextSecrets bool
	Stderr                io.Writer
	// GlobalEnvExamplePolicy is the resolved personal/global .env.example
	// failure policy for the active workspace. nil when no global override is
	// loaded on this path (the resolver treats nil as "no global rung").
	GlobalEnvExamplePolicy *config.EnvExamplePolicy
	// GlobalEnvOutput is the resolved personal/global secret-output target
	// declaration. Empty when no global override is loaded on this path.
	GlobalEnvOutput config.OutputTargets
	// WorktreeDelegation carries the apply-time worktree-integration decision
	// (probe result + niwa absolute path). nil installs neither hook nor deny.
	WorktreeDelegation *WorktreeDelegation
	// InheritedEnv, when non-nil, is the clone's already-materialized env from
	// which the SettingsMaterializer resolves [claude.env] promoted keys instead
	// of re-resolving secrets. The worktree path sets this (see ApplyToWorktree);
	// the instance apply path leaves it nil so promotion resolves from config.
	InheritedEnv map[string]string
	// InheritedUnresolved is the worktree path's unresolved-key set, recovered
	// from the records in the clone's materialized env file. It rides alongside
	// InheritedEnv for the same reason: this path holds no marks to read.
	InheritedUnresolved map[string]unresolvedEnvKey
	// Keys collects the declared keys this repo could not supply. nil disables
	// collection; the worktree path leaves it nil because it renders no report.
	Keys *keyreport.Collector
	// StrictSecrets is the run's resolved strictness, threaded here for the
	// promote branch alone -- promotion happens per-repo, after the applier's
	// post-merge gate has already passed. The worktree path leaves it false
	// and must keep doing so: it re-materializes from an already-written file
	// and resolves nothing, so there is nothing there to be strict about.
	StrictSecrets bool
}

// runRepoMaterializers runs the given materializers for a single repo against
// in.RepoDir. It merges discovered hooks beneath explicit config (matching the
// apply pipeline), builds the MaterializeContext, and skips the hooks/settings
// materializers when claude is disabled for the repo. Returns the list of
// written files. This is the single shared materializer path used by both the
// instance apply pipeline and ApplyToWorktree.
func runRepoMaterializers(materializers []Materializer, in repoMaterializeInputs) ([]string, []string, error) {
	effective := MergeOverrides(in.Cfg, in.RepoName)

	// Merge discovered hooks as base; explicit config entries run first per event.
	if len(in.DiscoveredHooks) > 0 {
		merged := make(config.HooksConfig, len(in.DiscoveredHooks)+len(effective.Claude.Hooks))
		// Start with discovered hooks (converted to relative paths).
		for event, entries := range in.DiscoveredHooks {
			var relEntries []config.HookEntry
			for _, entry := range entries {
				relScripts := make([]string, 0, len(entry.Scripts))
				for _, absPath := range entry.Scripts {
					rel, err := filepath.Rel(in.ConfigDir, absPath)
					if err != nil {
						return nil, nil, fmt.Errorf("materializer hooks: computing relative path for %s: %w", absPath, err)
					}
					relScripts = append(relScripts, rel)
				}
				relEntries = append(relEntries, config.HookEntry{
					Matcher: entry.Matcher,
					Scripts: relScripts,
				})
			}
			merged[event] = relEntries
		}
		// Explicit config runs before discovered hooks for the same event
		// and must not silently discard user-authored discovered hooks.
		//
		// Dedup by resolved script path: a script present in BOTH a declared
		// [[claude.hooks.<event>]] entry AND auto-discovered under
		// hooks/<event>/ must register exactly once, keeping the declared
		// entry (which carries the matcher). Without this, the same script
		// materializes twice and the discovered copy — having no matcher —
		// fires on every tool call.
		for event, entries := range effective.Claude.Hooks {
			existing, ok := merged[event]
			if !ok {
				merged[event] = entries
				continue
			}
			// Collect the resolved paths declared for this event.
			declaredPaths := make(map[string]bool)
			for _, e := range entries {
				for _, s := range e.Scripts {
					declaredPaths[resolveHookScriptPath(in.ConfigDir, s)] = true
				}
			}
			// Keep only discovered scripts not already declared.
			deduped := make([]config.HookEntry, 0, len(existing))
			for _, de := range existing {
				kept := make([]string, 0, len(de.Scripts))
				for _, s := range de.Scripts {
					if !declaredPaths[resolveHookScriptPath(in.ConfigDir, s)] {
						kept = append(kept, s)
					}
				}
				if len(kept) > 0 {
					deduped = append(deduped, config.HookEntry{Matcher: de.Matcher, Scripts: kept})
				}
			}
			// Declared entries first (retaining their matcher), then any
			// discovered entries that weren't also declared.
			merged[event] = append(append([]config.HookEntry(nil), entries...), deduped...)
		}
		effective.Claude.Hooks = merged
	}

	mctx := &MaterializeContext{
		Config:                in.Cfg,
		Effective:             effective,
		RepoName:              in.RepoName,
		RepoDir:               in.RepoDir,
		ConfigDir:             in.ConfigDir,
		DiscoveredEnv:         in.DiscoveredEnv,
		RepoIndex:             in.RepoIndex,
		SourceTuples:          in.SourceTuples,
		AllowPlaintextSecrets: in.AllowPlaintextSecrets,
		Stderr:                in.Stderr,

		GlobalEnvExamplePolicy: in.GlobalEnvExamplePolicy,
		GlobalEnvOutput:        in.GlobalEnvOutput,
		WorktreeDelegation:     in.WorktreeDelegation,
		InheritedEnv:           in.InheritedEnv,
		InheritedUnresolved:    in.InheritedUnresolved,
		Keys:                   in.Keys,
		StrictSecrets:          in.StrictSecrets,
	}

	var written []string
	claudeOn := ClaudeEnabled(in.Cfg, in.RepoName)
	for _, m := range materializers {
		// Skip hooks and settings materializers when claude is disabled.
		if !claudeOn && (m.Name() == "hooks" || m.Name() == "settings") {
			continue
		}

		files, err := m.Materialize(mctx)
		if err != nil {
			return nil, nil, fmt.Errorf("materializer %s for repo %s: %w", m.Name(), in.RepoName, err)
		}
		written = append(written, files...)
	}
	return written, mctx.EnvOutputs, nil
}

// resolveHookScriptPath resolves a hook script reference to a cleaned absolute
// path for dedup comparison. Discovered-hook scripts and relative declared
// scripts are joined against configDir; absolute scripts (global-config hooks)
// are cleaned as-is. It is comparison-only — the returned path is never written.
func resolveHookScriptPath(configDir, script string) string {
	if filepath.IsAbs(script) {
		return filepath.Clean(script)
	}
	return filepath.Clean(filepath.Join(configDir, script))
}

// repoEnvConfigured reports whether a repo would have any env output to
// materialize, using the SAME config-only structural check ResolveEnvVars /
// EnvMaterializer use to decide emptiness (counts of files/vars/secrets/repo
// files / .env.example vars). It performs NO secret resolution and reads no
// secret bytes -- it only inspects which inputs are declared.
//
// This is the distinguisher inheritEnvOutputs needs for R8: a repo that has env
// configured but is missing a clone target is an error (the clone was not
// applied), whereas a repo with no env configured at all legitimately produced
// no output, so a missing target is not an error.
func repoEnvConfigured(effectiveEnv config.EnvConfig, discovered *DiscoveredEnv, repoName string) bool {
	files := effectiveEnv.Files
	if len(files) == 0 && discovered != nil && discovered.WorkspaceFile != "" {
		files = []string{discovered.WorkspaceFile}
	}
	hasVars := len(effectiveEnv.Vars.Values) > 0 || len(effectiveEnv.Secrets.Values) > 0
	hasRepoFile := discovered != nil && discovered.RepoFiles != nil && discovered.RepoFiles[repoName] != ""
	return len(files) > 0 || hasVars || hasRepoFile
}

// inheritEnvOutputs copies the instance clone's already-materialized env output
// file(s) into a worktree's config-resolved target paths, byte-for-byte, with
// no secret resolution and no network access. It is the single primitive that
// produces a worktree's env (used by worktree create, worktree apply, and the
// niwa apply worktree-refresh fan-out).
//
// For each target resolved from config.EffectiveEnvOutput, both the clone
// source path and the worktree dest path pass through safeTargetPath (the target
// set is config-derived and treated as untrusted, so a crafted ../ or symlinked
// target.Path cannot read outside the clone nor write outside the worktree). The
// clone source is stat'd; a missing source is the R8 condition only when the
// repo has env configured (see repoEnvConfigured) -- a repo with no env at all
// is not an error and copies nothing.
//
// For custom (non-"*.local*") target names the primitive reproduces the
// EnvMaterializer's fail-closed ordering: it refuses on a non-git worktree
// (IsGitRepo) and asserts git-exclude coverage BEFORE writing, so a custom-named
// secret never lands git-visible. Parent dirs are created 0700 and files written
// at secretFileMode (0600), matching the clone's secrecy posture.
//
// It returns the written worktree target paths (for the content file list) and
// the custom target names that need re-asserting in the caller's excludeExtras
// union.
func inheritEnvOutputs(cloneRepoDir, worktreeDir string, cfg *config.WorkspaceConfig, repo string, globalEnvOutput config.OutputTargets, effectiveEnv config.EnvConfig, discovered *DiscoveredEnv) (written []string, customNames []string, err error) {
	targets := config.EffectiveEnvOutput(globalEnvOutput, cfg, repo)
	configured := repoEnvConfigured(effectiveEnv, discovered, repo)

	// The clone repo dir must exist to inherit from. safeTargetPath resolves the
	// clone source against this root via EvalSymlinks, which fails ENOENT on a
	// missing root, so handle absence up front: a missing clone dir means the
	// repo's env was never materialized. That is R8 when env is configured, and
	// a no-op (nothing to inherit) when it is not.
	if _, statErr := os.Stat(cloneRepoDir); statErr != nil {
		if os.IsNotExist(statErr) {
			if configured {
				return nil, nil, fmt.Errorf("repo %s: clone directory %s does not exist; run `niwa apply` to materialize the instance environment first", repo, cloneRepoDir)
			}
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("repo %s: stating clone directory %s: %w", repo, cloneRepoDir, statErr)
	}

	// Validate every source and dest path and collect custom (non-"*.local*")
	// names up front, then establish exclude coverage for the whole set BEFORE
	// any write -- mirroring EnvMaterializer so no secret file lands ahead of its
	// exclude line.
	type plannedTarget struct {
		srcAbs  string
		destAbs string
		relPath string
	}
	var planned []plannedTarget
	var customPatterns []string
	for _, tgt := range targets {
		srcAbs, err := safeTargetPath(cloneRepoDir, tgt.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("env inherit source %q for repo %s: %w", tgt.Path, repo, err)
		}
		destAbs, err := safeTargetPath(worktreeDir, tgt.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("env inherit dest %q for repo %s: %w", tgt.Path, repo, err)
		}

		info, statErr := os.Stat(srcAbs)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				if configured {
					// R8: the repo has env configured but the clone holds no
					// materialized output to inherit (e.g. env enabled after the
					// last apply, or the clone was never applied).
					return nil, nil, fmt.Errorf("repo %s: no materialized env output at %s to inherit; run `niwa apply` to materialize the instance environment first", repo, srcAbs)
				}
				// Repo has no env configured at all: nothing to copy, not an error.
				continue
			}
			return nil, nil, fmt.Errorf("repo %s: stating clone env output %q: %w", repo, srcAbs, statErr)
		}
		if info.IsDir() {
			return nil, nil, fmt.Errorf("repo %s: clone env output %q is a directory, not a file", repo, srcAbs)
		}

		planned = append(planned, plannedTarget{srcAbs: srcAbs, destAbs: destAbs, relPath: tgt.Path})
		if !matchedByBasePattern(tgt.Path) {
			customPatterns = append(customPatterns, tgt.Path)
		}
	}

	if len(customPatterns) > 0 {
		if !gitexclude.IsGitRepo(worktreeDir) {
			return nil, nil, fmt.Errorf("repo %s: custom secret-output target requires a git repository to guarantee git invisibility, but %s is not a git repository", repo, worktreeDir)
		}
		if err := gitexclude.EnsureRepoExclude(worktreeDir, customPatterns...); err != nil {
			return nil, nil, fmt.Errorf("repo %s: recording git exclude coverage for inherited custom secret-output targets: %w", repo, err)
		}
	}

	for _, p := range planned {
		data, err := os.ReadFile(p.srcAbs)
		if err != nil {
			return nil, nil, fmt.Errorf("repo %s: reading clone env output %q: %w", repo, p.srcAbs, err)
		}
		if err := os.MkdirAll(filepath.Dir(p.destAbs), 0o700); err != nil {
			return nil, nil, fmt.Errorf("repo %s: creating parent dir for inherited env output %q: %w", repo, p.relPath, err)
		}
		if err := os.WriteFile(p.destAbs, data, secretFileMode); err != nil {
			return nil, nil, fmt.Errorf("repo %s: writing inherited env output %q: %w", repo, p.relPath, err)
		}
		written = append(written, p.destAbs)
	}

	return written, customPatterns, nil
}

// readCloneEnvOutput reads the instance clone's already-materialized env output
// file(s) for a repo and merges them into a single key->value map. It is the
// promoted-key counterpart to inheritEnvOutputs: the worktree path resolves
// [claude.env] promoted keys from this inherited env rather than re-resolving
// secrets (the worktree apply runs no vault / machine-identity sync, so a
// promoted key sourced from a secret is absent from the static config it sees).
//
// Every source path passes through safeTargetPath (the config-derived target set
// is untrusted), so a crafted ../ or symlinked target.Path cannot read outside
// the clone. A missing clone dir or a missing/dir output file yields no keys
// rather than an error: the caller resolves promoted keys against the result and
// surfaces a genuinely-absent key as the promote error, while inheritEnvOutputs
// owns the friendlier R8 "run niwa apply first" message for a missing clone. The
// returned map is always non-nil so it can signal "worktree path" to the
// SettingsMaterializer even when empty.
//
// The second return is the unresolved-key set recovered from the file's
// records. It is what lets the worktree half of the promote branch tell a key
// the instance apply deliberately omitted from one that was never there — the
// clone's file is the only evidence of that decision this path can see.
//
// That recovery is a trust boundary, and it is worth naming: a repository can
// write its own environment file, so a crafted record can move a key out of the
// promote branch's hard error and into its tolerated branch. The effect is
// bounded to degradation — the key is dropped rather than promoted, no value is
// invented — and envformat.ParseRecord revalidates the key and description
// before either reaches this map.
func readCloneEnvOutput(cloneRepoDir string, cfg *config.WorkspaceConfig, repo string, globalEnvOutput config.OutputTargets) (map[string]string, map[string]unresolvedEnvKey, error) {
	out := map[string]string{}
	unresolved := map[string]unresolvedEnvKey{}
	if _, err := os.Stat(cloneRepoDir); err != nil {
		if os.IsNotExist(err) {
			return out, unresolved, nil
		}
		return nil, nil, fmt.Errorf("repo %s: stating clone directory %s: %w", repo, cloneRepoDir, err)
	}

	for _, tgt := range config.EffectiveEnvOutput(globalEnvOutput, cfg, repo) {
		srcAbs, err := safeTargetPath(cloneRepoDir, tgt.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("repo %s: clone env output %q: %w", repo, tgt.Path, err)
		}
		info, statErr := os.Stat(srcAbs)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, nil, fmt.Errorf("repo %s: stating clone env output %q: %w", repo, srcAbs, statErr)
		}
		if info.IsDir() {
			continue
		}
		parsed, records, err := parseEnvFileWithRecords(srcAbs)
		if err != nil {
			return nil, nil, fmt.Errorf("repo %s: parsing clone env output %q: %w", repo, srcAbs, err)
		}
		for k, v := range parsed {
			out[k] = v
			// A later target holding a real assignment for the key wins over an
			// earlier target's record, mirroring how the values themselves merge.
			delete(unresolved, k)
		}
		for k, rec := range records {
			if _, ok := out[k]; ok {
				continue
			}
			unresolved[k] = unresolvedEnvKey{Record: rec}
		}
	}
	return out, unresolved, nil
}

// FindRepoGroup resolves the group a repo belongs to by scanning the instance
// layout (<instanceRoot>/<group>/<repo>) two levels deep. The on-disk layout is
// the ground truth: niwa apply already cloned the repo into its group directory,
// regardless of how the group was determined (explicit override or group
// filter). Returns an error if the repo is not found under any group.
func FindRepoGroup(instanceRoot, repoName string) (string, error) {
	topEntries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance root %s: %w", instanceRoot, err)
	}
	for _, top := range topEntries {
		if !top.IsDir() || top.Name() == ".niwa" {
			continue
		}
		groupDir := filepath.Join(instanceRoot, top.Name())
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() && sub.Name() == repoName {
				return top.Name(), nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace %s", repoName, instanceRoot)
}

// WorktreeApplyOptions carries the inputs ApplyToWorktree needs that are not
// derivable from the worktree path alone.
type WorktreeApplyOptions struct {
	// Exempt, when non-nil, receives the paths this apply refused to write
	// because the worktree checkout commits its own file at one of niwa's
	// names. The instance apply path passes one so its managed-file cleanup
	// leaves those paths alone; the standalone `niwa worktree apply` path
	// persists no managed-file record and passes none, so there is nothing for
	// it to exempt from.
	Exempt *[]string
	// OverlayDir is the local clone path of the overlay repo when one is
	// active, used to append overlay content / resolve overlay-sourced repo
	// content. Empty when no overlay is active.
	//
	// Overlay and global CLAUDE imports for the worktree rules file are
	// resolved from the instance root (where apply materializes them), not
	// from these opts — see installWorktreeRulesImport.
	OverlayDir string
	// Materializers is the set of repo materializers to run against the
	// worktree. When nil, the default set is used (the same set the apply
	// pipeline wires).
	Materializers []Materializer
	// AllowPlaintextSecrets mirrors Applier.AllowPlaintextSecrets.
	AllowPlaintextSecrets bool
	// Stderr receives diagnostic warnings during materialization. When nil,
	// materializers fall back to os.Stderr.
	Stderr io.Writer
	// GlobalEnvExamplePolicy is the resolved personal/global .env.example
	// failure policy for the active workspace, threaded into the pre-pass so
	// the worktree path applies the same policy as the instance apply path.
	// nil when no global override is available (the resolver treats nil as
	// "no global rung").
	GlobalEnvExamplePolicy *config.EnvExamplePolicy
	// GlobalEnvOutput is the resolved personal/global secret-output target
	// declaration, threaded so the worktree path resolves the same targets as
	// the instance apply path. Empty when no global override is available.
	GlobalEnvOutput config.OutputTargets
	// WorktreeDelegation carries the apply-time worktree-integration decision
	// (probe result + niwa fallback path) so a worktree's settings record the
	// same hook or deny entries as the clone it was made from. Without it the
	// two configurations drift: the clone carries one of them and the worktree
	// carries neither. nil installs neither, which is the pre-Decision-9
	// behavior, so a caller that does not set it is unaffected.
	// See DESIGN-niwa-default-worktree.md Decision 9.
	WorktreeDelegation *WorktreeDelegation
}

// ApplyToWorktree installs, into worktreePath, the same class of CLAUDE
// accessories a repo checkout receives from `niwa apply`, plus a worktree
// rules import (so the launched worktree sees workspace context) and a
// purpose/branch layer. It reuses the existing installers — InstallRepoContentTo
// and the shared runRepoMaterializers — rather than forking a parallel
// installer, so worktree and instance content cannot drift.
//
// It is idempotent: re-running overwrites the repo content, re-points the rules
// import (no duplicate @import lines), and replaces the worktree-context section
// rather than appending a second copy.
//
// instanceRoot is the workspace instance root; configDir is the snapshot/config
// directory whose content sources are resolved (the same configDir the apply
// pipeline uses). group is the repo's group; repo is the repo name; purpose and
// branch describe the worktree. purpose is treated strictly as content data —
// it is never interpolated into a filesystem path.
//
// Returns the list of files written.
func ApplyToWorktree(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, group, repo, purpose, branch string, opts WorktreeApplyOptions) ([]string, error) {
	// Every enumerated agent's plan is produced, exactly as on the instance
	// path: a worktree is prepared for both, and which documents each one
	// receives is the declaration table's answer rather than a caller's choice.
	// A producer is what turns an agent into declared writes, so the installers
	// below never see the agent as anything they could branch on.
	if !ClaudeEnabled(cfg, repo) {
		// Claude content is disabled for this repo; install only the
		// worktree-context layer so the worktree still records its purpose.
		var written, excludes []string
		for _, ag := range agent.All() {
			layerFiles, layerExcludes, err := installWorktreeContextLayer(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch, agentplan.For(ag), opts.Stderr, opts.Exempt)
			if err != nil {
				return nil, err
			}
			written = append(written, layerFiles...)
			excludes = append(excludes, layerExcludes...)
		}
		// This branch takes none of the materialization below, so the coverage
		// for what it did write is recorded here. A worktree that reads dirty
		// is one the teardown refuses to reclaim, and the gate above is no
		// reason to leave that behind.
		if len(excludes) > 0 {
			if err := gitexclude.EnsureRepoExclude(worktreePath, excludes...); err != nil {
				return nil, fmt.Errorf("recording git exclude coverage for worktree %s: %w", repo, err)
			}
		}
		return written, nil
	}

	var written []string
	var contentExcludes []string

	// 1. Owning repo's content, targeted at the worktree root. Same function the
	//    instance apply path calls, so worktree and instance content cannot
	//    drift on sources, composition, or the ownership rule.
	for _, ag := range agent.All() {
		result, err := InstallRepoContentTo(cfg, configDir, opts.OverlayDir, instanceRoot, worktreePath, group, repo, agentplan.For(ag))
		if err != nil {
			return nil, fmt.Errorf("installing repo content into worktree: %w", err)
		}
		written = append(written, result.WrittenFiles...)
		contentExcludes = append(contentExcludes, result.Excludes...)
		reportWorktreeContentWarnings(opts.Stderr, worktreePath, result.Warnings)
		collectExempt(opts.Exempt, result.Exempt)
	}

	// 1b. The workspace's declared plugin skills, delivered into the worktree
	//     for the same reason the content above is: a session opened here finds
	//     this directory first and reads nothing above it. Resolution passes no
	//     fetcher -- the worktree path re-delivers what the instance apply
	//     already fetched rather than reaching for the network on every worktree.
	pluginTrees, missingPlugins := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instanceRoot,
		Plugins:      MergeInstanceOverrides(cfg).Plugins,
		Marketplaces: cfg.Claude.Marketplaces,
		RepoIndex:    instanceRepoIndex(instanceRoot),
	})
	var skillReports []string
	for _, m := range missingPlugins {
		skillReports = append(skillReports, m.String())
	}
	for _, ag := range agent.All() {
		skills, err := InstallRepoSkills(worktreePath, pluginTrees, agentplan.For(ag))
		if err != nil {
			return nil, fmt.Errorf("delivering plugin skills into worktree: %w", err)
		}
		skillReports = append(skillReports, skills.Warnings...)
		contentExcludes = append(contentExcludes, skills.Excludes...)
	}
	reportWorktreeWarnings(opts.Stderr, worktreePath, skillReports)

	// 2. Repo materializers (settings, files, hooks) targeted at the worktree.
	//    Same shared loop the instance apply path uses, but with the
	//    EnvMaterializer dropped: a worktree does not re-resolve secrets, it
	//    inherits the clone's already-materialized env output (step 2b below).
	//
	//    Secret-ref safety on the standalone path: removing the worktree path's
	//    resolve+merge means cfg here is overlay-merged but UNRESOLVED, so any
	//    vault:// ref in it is still a literal "vault://..." Plain string. This is
	//    safe for the settings/files materializers because neither can write that
	//    literal to disk:
	//      - FilesMaterializer: [files] is map[string]string (source->dest PATHS),
	//        never MaybeSecret; it copies file BYTES from the config snapshot, so a
	//        vault:// string could only be a (non-existent) source path that fails
	//        at read time -- it is never emitted as a value.
	//      - SettingsMaterializer: the only settings value that reaches disk is the
	//        "permissions" key, constrained to "bypass"/"ask". An unresolved
	//        vault:// permissions value is rejected by buildSettingsDoc ("unknown
	//        permissions value") BEFORE any write, so it fails closed rather than
	//        landing a literal vault:// in settings.local.json. Every other
	//        settings key is dropped by buildSettingsDoc and never written.
	//    Env is the only materializer whose values can be secret-backed, and it is
	//    handled by inherit-by-copy (step 2b), not re-resolution. The standalone
	//    worktree path therefore writes no unresolved vault:// to disk.
	materializers := opts.Materializers
	if materializers == nil {
		materializers = worktreeRepoMaterializers(opts.Stderr)
	}

	discoveredHooks, _ := DiscoverHooks(configDir)
	wsEnvFile, repoEnvFiles, _ := DiscoverEnvFiles(configDir)
	relWsEnv := wsEnvFile
	if relWsEnv != "" {
		if r, err := filepath.Rel(configDir, relWsEnv); err == nil {
			relWsEnv = r
		}
	}
	repoIndex := map[string]string{repo: worktreePath}

	// The SettingsMaterializer resolves [claude.env] promoted keys, but the
	// worktree path's cfg is unresolved (no vault / machine-identity sync ran),
	// so a promoted key backed by a secret is absent from it. Inherit those
	// values from the instance clone's already-materialized env — the same
	// source inheritEnvOutputs copies in step 2b — so the settings file carries
	// the promoted key rather than failing to resolve it. A non-nil (possibly
	// empty) map signals the worktree path to the materializer.
	cloneRepoDir := filepath.Join(instanceRoot, group, repo)
	inheritedEnv, inheritedUnresolved, err := readCloneEnvOutput(cloneRepoDir, cfg, repo, opts.GlobalEnvOutput)
	if err != nil {
		return nil, fmt.Errorf("reading clone env for worktree promote inheritance: %w", err)
	}

	matFiles, envOutputs, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:             cfg,
		ConfigDir:       configDir,
		RepoName:        repo,
		RepoDir:         worktreePath,
		DiscoveredHooks: discoveredHooks,
		DiscoveredEnv: &DiscoveredEnv{
			WorkspaceFile: relWsEnv,
			RepoFiles:     repoEnvFiles,
		},
		RepoIndex:             repoIndex,
		SourceTuples:          map[string][]SourceEntry{},
		AllowPlaintextSecrets: opts.AllowPlaintextSecrets,
		Stderr:                opts.Stderr,

		GlobalEnvExamplePolicy: opts.GlobalEnvExamplePolicy,
		GlobalEnvOutput:        opts.GlobalEnvOutput,
		WorktreeDelegation:     opts.WorktreeDelegation,
		InheritedEnv:           inheritedEnv,
		InheritedUnresolved:    inheritedUnresolved,
	})
	if err != nil {
		return nil, err
	}
	written = append(written, matFiles...)

	// 2b. Env inherit: copy the clone's already-materialized env output file(s)
	//     into the worktree's config-resolved targets, byte-for-byte. The
	//     worktree path NEVER resolves secrets; it mirrors the clone. The
	//     primitive establishes git-exclude coverage for custom target names
	//     before writing (fail-closed) and reports those names so the
	//     re-assert below carries them in the unioned exclude block.
	effectiveEnv := MergeOverrides(cfg, repo).Env
	envInherited, envCustomNames, err := inheritEnvOutputs(
		cloneRepoDir, worktreePath, cfg, repo, opts.GlobalEnvOutput, effectiveEnv,
		&DiscoveredEnv{WorkspaceFile: relWsEnv, RepoFiles: repoEnvFiles},
	)
	if err != nil {
		return nil, err
	}
	written = append(written, envInherited...)
	envOutputs = append(envOutputs, envCustomNames...)

	// Record git-ignore coverage for any custom secret-output target names so
	// they stay invisible to the worktree's git status, matching the instance
	// apply path's end state. The materializer already established coverage
	// before writing; this re-asserts the full set idempotently.
	//
	// worktreeRulesFile (.claude/rules/worktree-imports.md) is the one
	// niwa-authored worktree file under .claude/ whose name carries no ".local"
	// infix, so the base "*.local*" pattern does not cover it. Without explicit
	// coverage a freshly created worktree reads dirty to `git status
	// --porcelain`, which makes the non-force from-hook teardown log-and-retain
	// every delegated worktree (orphan accumulation). It is added here as an
	// extra pattern — scoped to this exact path rather than widening the global
	// niwaExcludePatterns — so genuine user-authored .claude/ files still show.
	excludeExtras := append([]string{worktreeRulesFile}, envOutputs...)
	excludeExtras = append(excludeExtras, contentExcludes...)
	if err := gitexclude.EnsureRepoExclude(worktreePath, excludeExtras...); err != nil {
		return nil, fmt.Errorf("recording git exclude coverage for worktree %s: %w", repo, err)
	}

	// 3. Worktree rules import: an absolute @import to the instance's
	//    workspace-context.md, plus overlay/global where present. Reuses the
	//    same write/append helpers the instance root uses.
	rulesFiles, err := installWorktreeRulesImport(instanceRoot, worktreePath)
	if err != nil {
		return nil, err
	}
	written = append(written, rulesFiles...)

	// 4. Worktree-specific layer naming the purpose and branch (or the
	//    configured [claude.content.worktree] template, when set).
	for _, ag := range agent.All() {
		layerFiles, _, err := installWorktreeContextLayer(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch, agentplan.For(ag), opts.Stderr, opts.Exempt)
		if err != nil {
			return nil, err
		}
		written = append(written, layerFiles...)
	}

	// 5. Worktree-event hooks, run on create/apply. Analog of the instance
	//    setup-script run: discovered from <configDir>/worktree-hooks/ and
	//    executed against the worktree, with worktree context in the env.
	if err := runWorktreeHooks(configDir, worktreePath, repo, purpose, branch, opts.Stderr); err != nil {
		return nil, err
	}

	return written, nil
}

// defaultRepoMaterializers returns the canonical repo-materializer set
// (HooksMaterializer, SettingsMaterializer, EnvMaterializer, FilesMaterializer)
// in canonical order. It is the single source of the materializer list for the
// whole package: NewApplier wires it for the instance apply pipeline, and the
// worktree path falls back to it when no override is supplied, so a worktree
// install matches a repo install and the two paths cannot drift. Adding a
// materializer here reaches both paths.
func defaultRepoMaterializers(stderr io.Writer) []Materializer {
	return []Materializer{
		&HooksMaterializer{},
		&SettingsMaterializer{},
		&EnvMaterializer{Stderr: stderr},
		&FilesMaterializer{Stderr: stderr},
	}
}

// worktreeRepoMaterializers returns the materializer set run against a worktree:
// the canonical set MINUS the EnvMaterializer. A worktree does not re-resolve
// secrets; its env is produced by inheritEnvOutputs (byte-copy from the clone),
// so running the env materializer here would re-introduce the live-resolution
// fork this design removes. Settings, files, and hooks still materialize.
func worktreeRepoMaterializers(stderr io.Writer) []Materializer {
	return []Materializer{
		&HooksMaterializer{},
		&SettingsMaterializer{},
		&FilesMaterializer{Stderr: stderr},
	}
}

// installWorktreeRulesImport writes <worktree>/.claude/rules/worktree-imports.md
// with an absolute @import to the instance's workspace-context.md, then appends
// overlay/global imports when those files exist at the instance root. Uses the
// same writeWorkspaceRulesFile / appendToWorkspaceRulesFile helpers the instance
// root uses, so the worktree's import file has the identical shape.
func installWorktreeRulesImport(instanceRoot, worktreePath string) ([]string, error) {
	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}

	rulesPath := filepath.Join(worktreePath, worktreeRulesFile)

	contextPath := filepath.Join(absInstance, workspaceContextFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		return nil, fmt.Errorf("writing worktree rules import: %w", err)
	}

	// Overlay/global imports, when those files exist at the instance root.
	overlayPath := filepath.Join(absInstance, overlayClaudeFile)
	if _, statErr := os.Stat(overlayPath); statErr == nil {
		if err := appendToWorkspaceRulesFile(rulesPath, overlayPath); err != nil {
			return nil, fmt.Errorf("adding overlay import to worktree rules: %w", err)
		}
	}
	globalPath := filepath.Join(absInstance, globalClaudeFile)
	if _, statErr := os.Stat(globalPath); statErr == nil {
		if err := appendToWorkspaceRulesFile(rulesPath, globalPath); err != nil {
			return nil, fmt.Errorf("adding global import to worktree rules: %w", err)
		}
	}

	return []string{rulesPath}, nil
}

// installWorktreeContextLayer writes the worktree-specific section to the
// worktree's context document. The section is delimited by a stable heading so
// a re-apply replaces it in place rather than appending a duplicate
// (idempotent). purpose is interpolated only into file content, never a
// filesystem path.
//
// When [claude.content.worktree].source is configured, the section body is
// rendered from that template (expanded with the worktree variables) in-memory
// via renderWorktreeLayerBody -> renderContentFile (the same containment-checked
// read+expand core every other content layer resolves its source through).
// When unset, the generated default purpose/branch body is used — the Stage-1
// behavior, unchanged.
//
// The target is the producer's, computed from worktreePath alone (at the
// worktree root) and verified to stay within the worktree via checkContainment,
// matching the containment discipline of the other content installers. Under an
// agent that does not receive worktree-level context (Codex) the producer
// declares nothing, so niwa writes no context file into the worktree and the
// git working tree stays clean.
func installWorktreeContextLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, repo, purpose, branch string, producer agentplan.Producer, stderr io.Writer, exempt *[]string) ([]string, []string, error) {
	body, err := renderWorktreeLayerBody(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return nil, nil, err
	}

	// The probe is taken here, after the repository-level install for the same
	// worktree has run, because whether this section joins a document that
	// already exists or has to stand as one is decided by what that install
	// wrote.
	probe, err := probeContextTree(producer.ContextProbeSpec(worktreePath))
	if err != nil {
		return nil, nil, err
	}

	plan, err := producer.WorktreeContextPlan(agentplan.WorktreeContextInputs{
		Dir:     worktreePath,
		Heading: worktreeContextHeading,
		Body:    []byte(body),
		Probe:   probe,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := checkPlanContainment(plan, worktreePath); err != nil {
		return nil, nil, fmt.Errorf("worktree context layer: %w", err)
	}

	written, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, nil, err
	}
	reportWorktreeWarnings(stderr, worktreePath, plan.Warnings)
	collectExempt(exempt, plan.Exempt)
	return written, excludes, nil
}

// collectExempt appends paths to a caller-supplied exemption list, if the
// caller keeps one. A caller that persists no managed-file record has nothing
// to exempt a path from, and passes nil.
func collectExempt(sink *[]string, paths []string) {
	if sink == nil {
		return
	}
	*sink = append(*sink, paths...)
}

// reportWorktreeContentWarnings renders a content install's warnings for one
// worktree. Every refusal names its path: the standalone worktree path has no
// deferred-warning reporter, and a quiet skip is the silent failure the
// ownership rule exists to prevent.
func reportWorktreeContentWarnings(stderr io.Writer, worktreePath string, warnings []ContentWarning) {
	messages := make([]string, 0, len(warnings))
	for _, w := range warnings {
		messages = append(messages, w.Message)
	}
	reportWorktreeWarnings(stderr, worktreePath, messages)
}

// reportWorktreeWarnings writes one line per warning, prefixed with the
// worktree they are about.
func reportWorktreeWarnings(stderr io.Writer, worktreePath string, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "worktree %s: %s\n", worktreePath, w)
	}
}

// worktreeLayerVars builds the template variable map for the worktree layer.
// It extends the instance content variables ({workspace}/{workspace_name}) with
// the worktree-specific {purpose}/{branch}/{repo_name}/{worktree_path}. purpose
// is data interpolated into content only; it is never used to build a path.
func worktreeLayerVars(cfg *config.WorkspaceConfig, instanceRoot, worktreePath, repo, purpose, branch string) (map[string]string, error) {
	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}
	absWorktree, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil, fmt.Errorf("resolving worktree path: %w", err)
	}
	return map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{purpose}":        purpose,
		"{branch}":         branch,
		"{repo_name}":      repo,
		"{worktree_path}":  absWorktree,
	}, nil
}

// renderWorktreeLayerBody produces the body of the worktree-context section.
// When [claude.content.worktree].source is set, the body is rendered from that
// template via the shared containment-checked renderContentFile (expandVars +
// checkContainment on the SOURCE path) with the worktree variable map. When
// unset, the generated default purpose/branch body is returned — the Stage-1
// behavior, unchanged.
//
// The template is rendered entirely in memory: renderContentFile reads the
// source, runs the same symlink-aware containment check the instance content
// path uses, and expands the variables, so a crafted source still cannot escape
// its directory. No transient file is written into the worktree. purpose is
// only ever expanded into content, never a path component.
func renderWorktreeLayerBody(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, repo, purpose, branch string) (string, error) {
	source := cfg.Claude.Content.Worktree.Source
	if source == "" {
		// Stage-1 default: generated purpose/branch section, unchanged.
		return fmt.Sprintf("This is a niwa worktree of repo %q.\n\n- Purpose: %s\n- Branch: %s\n",
			repo, purpose, branch), nil
	}

	vars, err := worktreeLayerVars(cfg, instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return "", err
	}

	contentRoot := contentDirRoot(cfg, configDir)
	rendered, err := renderContentFile(contentRoot, source, vars)
	if err != nil {
		return "", fmt.Errorf("rendering worktree layer template: %w", err)
	}

	// Normalize a trailing newline so the spliced section ends cleanly.
	out := rendered
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

// runWorktreeHooks discovers worktree-event hook scripts from
// <configDir>/worktree-hooks/ and runs the scripts registered for the apply
// event against the worktree. It is the worktree analog of the instance hook
// surface: scripts come from the workspace config repo the operator already
// trusts (same provenance as DiscoverHooks / setup scripts; no new external
// input). Scripts run with the worktree as the working directory and the
// worktree context exported as environment (NIWA_WORKTREE_*), so a hook can act
// on purpose/branch/repo without parsing files.
//
// Scripts run in lexical order; the first non-zero exit stops the run and is
// surfaced as an error (mirroring the setup-script contract). A missing
// worktree-hooks/ directory or no scripts for the event is a no-op.
func runWorktreeHooks(configDir, worktreePath, repo, purpose, branch string, stderr io.Writer) error {
	if stderr == nil {
		stderr = os.Stderr
	}

	hooks, err := DiscoverWorktreeHooks(configDir)
	if err != nil {
		return fmt.Errorf("discovering worktree hooks: %w", err)
	}

	entries := hooks[worktreeApplyEvent]
	if len(entries) == 0 {
		return nil
	}

	// Collect script paths in lexical order for a deterministic run order.
	var scripts []string
	for _, entry := range entries {
		scripts = append(scripts, entry.Scripts...)
	}
	sort.Strings(scripts)

	for _, scriptPath := range scripts {
		info, err := os.Stat(scriptPath)
		if err != nil {
			return fmt.Errorf("worktree hook %s: stat: %w", scriptPath, err)
		}
		if info.Mode()&0o111 == 0 {
			// Match the setup-script policy: warn and skip non-executable files
			// rather than failing the apply.
			fmt.Fprintf(stderr, "worktree hook %s: not executable (chmod +x to enable); skipping\n", scriptPath)
			continue
		}

		cmd := exec.Command(scriptPath)
		cmd.Dir = worktreePath
		cmd.Stdout = stderr
		cmd.Stderr = stderr
		// purpose is exported as content data only; the worktree dir name and
		// cmd.Dir are derived from worktreePath, never from purpose.
		cmd.Env = append(os.Environ(),
			"NIWA_WORKTREE_PATH="+worktreePath,
			"NIWA_WORKTREE_REPO="+repo,
			"NIWA_WORKTREE_PURPOSE="+purpose,
			"NIWA_WORKTREE_BRANCH="+branch,
		)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("worktree hook %s failed: %w", scriptPath, err)
		}
	}

	return nil
}
