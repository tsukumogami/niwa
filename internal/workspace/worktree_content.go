package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/gitexclude"
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
		for event, entries := range effective.Claude.Hooks {
			if existing, ok := merged[event]; ok {
				merged[event] = append(entries, existing...)
			} else {
				merged[event] = entries
			}
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
	if !ClaudeEnabled(cfg, repo) {
		// Claude content is disabled for this repo; install only the
		// worktree-context layer so the worktree still records its purpose.
		return installWorktreeContextLayer(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch)
	}

	var written []string

	// 1. Owning repo's content (CLAUDE.local.md + subdir content), targeted at
	//    the worktree root. Same function the instance apply path calls.
	result, err := InstallRepoContentTo(cfg, configDir, opts.OverlayDir, instanceRoot, worktreePath, group, repo)
	if err != nil {
		return nil, fmt.Errorf("installing repo content into worktree: %w", err)
	}
	written = append(written, result.WrittenFiles...)

	// 2. Repo materializers (settings, files, hooks) targeted at the worktree.
	//    Same shared loop the instance apply path uses, but with the
	//    EnvMaterializer dropped: a worktree does not re-resolve secrets, it
	//    inherits the clone's already-materialized env output (step 2b below).
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
	cloneRepoDir := filepath.Join(instanceRoot, group, repo)
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
	layerFiles, err := installWorktreeContextLayer(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return nil, err
	}
	written = append(written, layerFiles...)

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

// installWorktreeContextLayer writes the worktree-specific section to
// <worktree>/CLAUDE.local.md. The section is delimited by a stable heading so a
// re-apply replaces it in place rather than appending a duplicate (idempotent).
// purpose is interpolated only into file content, never a filesystem path.
//
// When [claude.content.worktree].source is configured, the section body is
// rendered from that template (expanded with the worktree variables) in-memory
// via renderWorktreeLayerBody -> renderContentFile (the same containment-checked
// read+expand core as installContentFile, but no transient file is written).
// When unset, the generated default purpose/branch body is used — the Stage-1
// behavior, unchanged.
//
// The CLAUDE.local.md target is computed from worktreePath alone (at the
// worktree root) and verified to stay within the worktree via checkContainment,
// matching the containment discipline of the other content installers.
func installWorktreeContextLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, repo, purpose, branch string) ([]string, error) {
	target := filepath.Join(worktreePath, "CLAUDE.local.md")
	if err := checkContainment(target, worktreePath); err != nil {
		return nil, fmt.Errorf("worktree context layer: %w", err)
	}

	body, err := renderWorktreeLayerBody(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch)
	if err != nil {
		return nil, err
	}
	section := worktreeContextHeading + "\n\n" + body

	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading worktree CLAUDE.local.md: %w", err)
	}

	merged := stripWorktreeContextSection(string(existing))
	if len(merged) > 0 {
		// Separate prior content from the appended section with a blank line.
		for len(merged) > 0 && (merged[len(merged)-1] == '\n') {
			merged = merged[:len(merged)-1]
		}
		merged += "\n\n"
	}
	merged += section

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("creating worktree dir: %w", err)
	}
	if err := os.WriteFile(target, []byte(merged), 0o644); err != nil {
		return nil, fmt.Errorf("writing worktree CLAUDE.local.md: %w", err)
	}
	return []string{target}, nil
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

// stripWorktreeContextSection removes a previously-appended worktree-context
// section (from worktreeContextHeading to end of file) so a re-apply replaces
// it rather than appending a duplicate. Content before the heading is preserved.
func stripWorktreeContextSection(content string) string {
	idx := strings.Index(content, worktreeContextHeading)
	if idx < 0 {
		return content
	}
	return content[:idx]
}
