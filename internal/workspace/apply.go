package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/guardrail"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

// Applier orchestrates the apply pipeline.
type Applier struct {
	GitHubClient    github.Client
	Cloner          *Cloner
	Materializers   []Materializer
	NoPull          bool
	AllowDirty      bool
	GlobalConfigDir string // empty string means global config not registered

	// Reporter receives all progress and diagnostic output for this applier.
	// NewApplier initializes it with NewReporter(os.Stderr). Callers may
	// replace it (e.g., with NewReporterWithTTY) before calling Apply or Create.
	Reporter *Reporter

	// AllowMissingSecrets threads through to the vault resolver's
	// ResolveOptions.AllowMissing. When true, missing vault keys are
	// downgraded to empty MaybeSecret values with a stderr warning.
	// The CLI --allow-missing-secrets flag (Issue 10) populates this
	// field; default false preserves the strict-apply behavior.
	AllowMissingSecrets bool

	// AllowPlaintextSecrets threads through to the public-repo
	// plaintext-secrets guardrail
	// (internal/guardrail.CheckGitHubPublicRemoteSecrets). When true,
	// the guardrail downgrades a blocking error to a loud stderr
	// warning and allows the apply to proceed. One-shot by contract:
	// no state is written, so the next apply re-runs the check. The
	// CLI --allow-plaintext-secrets flag (Issue 10) populates this
	// field; default false preserves the block-on-public-GitHub
	// behavior.
	AllowPlaintextSecrets bool

	// ConfigSourceURL is the original source URL used to clone the workspace
	// config (e.g., from RegistryEntry.Source or --from at init time). When
	// set and no OverlayURL is stored in InstanceState, runPipeline uses this
	// URL for convention overlay discovery (derives "<org>/<repo>-overlay").
	// Empty string disables convention discovery.
	ConfigSourceURL string

	// cloneOrSync is the function used to clone or sync the overlay repo.
	// Defaults to CloneOrSyncOverlay. Overridable in tests.
	cloneOrSync func(url, dir string) (bool, error)

	// headSHA is the function used to read the HEAD commit SHA of a repo.
	// Defaults to HeadSHA. Overridable in tests.
	headSHA func(dir string) (string, error)

	// vaultRegistry overrides vault.DefaultRegistry for vault bundle building.
	// Nil means use the process-wide DefaultRegistry (production behaviour).
	// Tests set this to a fresh *vault.Registry with the fake backend registered
	// so they can inject known secrets without touching DefaultRegistry.
	vaultRegistry *vault.Registry
}

// applierWriter is an io.Writer that always delegates to whatever
// a.Reporter is at the time of the Write call. This lets callers
// replace a.Reporter after construction (e.g., to inject a TTY-aware
// reporter) without leaving materializer Stderr fields pointing at a
// stale reporter instance.
type applierWriter struct{ a *Applier }

func (aw *applierWriter) Write(p []byte) (int, error) {
	s := string(p)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s != "" {
		aw.a.Reporter.Log("%s", s)
	}
	return len(p), nil
}

// NewApplier creates an Applier with the given GitHub client.
func NewApplier(gh github.Client) *Applier {
	a := &Applier{
		GitHubClient: gh,
		Cloner:       &Cloner{},
		Reporter:     NewReporter(os.Stderr),
		cloneOrSync:  CloneOrSyncOverlay,
		headSHA:      HeadSHA,
	}
	aw := &applierWriter{a: a}
	a.Materializers = []Materializer{
		&HooksMaterializer{},
		&SettingsMaterializer{},
		&EnvMaterializer{Stderr: aw},
		&FilesMaterializer{Stderr: aw},
	}
	return a
}

// DefaultMaxRepos is the threshold for auto-discovered repos per source.
// When an org returns more repos than this limit and the source has no
// explicit repos list, discovery fails with a clear error.
const DefaultMaxRepos = 10

// GlobalConfigOverrideFile is the filename for global config overrides in
// the global config repo.
const GlobalConfigOverrideFile = "niwa.toml"

// noticeProviderShadow is the one-time notice key for the personal-overlay
// provider shadow disclosure. After the first apply that detects a provider
// shadow, the notice is recorded in DisclosedNotices and suppressed on
// subsequent runs.
const noticeProviderShadow = "provider-shadow"

// cloneWorkers is the maximum number of repos cloned concurrently.
const cloneWorkers = 8

// cloneJob carries per-repo inputs to a clone worker.
type cloneJob struct {
	cr            ClassifiedRepo
	cloneURL      string
	branch        string
	targetDir     string
	defaultBranch string
	noPull        bool
}

// cloneResult carries per-repo outputs back to the orchestrator.
type cloneResult struct {
	name      string
	cloneURL  string // echoed from job so the orchestrator can populate RepoState.URL
	targetDir string // echoed from job so the orchestrator can call repoAlreadyCloned
	cloned    bool
	syncWarn  string // non-empty if sync produced a deferred warning
	err       error  // non-nil on clone failure; does not include sync errors
}

// pipelineOpts configures shared pipeline behavior for Create vs Apply.
type pipelineOpts struct {
	existingState    *InstanceState
	skipGlobal       bool
	overlayURL       string   // from InstanceState.OverlayURL (empty = no overlay URL in state)
	noOverlay        bool     // from InstanceState.NoOverlay
	configSourceURL  string   // original source URL for convention overlay discovery
	disclosedNotices []string // workspace-root-level notices already shown to the user
}

// pipelineResult holds the outputs of the shared pipeline.
type pipelineResult struct {
	classified   []ClassifiedRepo
	repoStates   map[string]RepoState
	managedFiles []ManagedFile
	warnings     []string
	// shadows carries the personal-overlay-vs-team key conflicts
	// detected during this apply invocation. Empty when no overlay
	// is active or no keys overlapped. Persisted into
	// InstanceState.Shadows by Create/Apply.
	shadows          []Shadow
	overlayURL       string   // set when convention discovery succeeds; empty otherwise
	overlayCommit    string   // HEAD SHA when overlayURL was set; empty otherwise
	disclosedNotices []string // one-time notices emitted during this run
}

// Create creates a new workspace instance under workspaceRoot, runs the full
// pipeline (discover, classify, clone, install content), assigns an instance
// number, and writes fresh state. Returns the instance directory path.
// instanceName is the directory name for the new instance (e.g. "myws-2");
// cfg.Workspace.Name is the config identity and must not be mutated by the
// caller — it is used for personal overlay scope lookup and InstanceState.ConfigName.
func (a *Applier) Create(ctx context.Context, cfg *config.WorkspaceConfig, configDir, workspaceRoot, instanceName string) (string, error) {
	now := time.Now()

	instanceRoot := filepath.Join(workspaceRoot, instanceName)
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		return "", fmt.Errorf("creating instance directory: %w", err)
	}

	// Ensure the instance root's .gitignore covers *.local*. The
	// materializers always emit files with the ".local" infix so
	// this single pattern is sufficient; running create twice on
	// the same instance is a no-op after the first run.
	if err := EnsureInstanceGitignore(instanceRoot); err != nil {
		_ = os.RemoveAll(instanceRoot)
		return "", fmt.Errorf("preparing instance .gitignore: %w", err)
	}

	// Load init-time state from the workspace root. `niwa init` writes
	// overlay URL, SkipGlobal, and NoOverlay to {workspaceRoot}/.niwa/instance.json
	// before any instance directory exists. Reading it here lets create honour
	// everything init discovered (overlay clone, flags) without re-running
	// discovery and without depending on workspace repos being cloned.
	initState, _ := LoadState(workspaceRoot)

	var initOverlayURL string
	var initNoOverlay bool
	var initSkipGlobal bool
	var initDisclosedNotices []string
	if initState != nil {
		initOverlayURL = initState.OverlayURL
		initNoOverlay = initState.NoOverlay
		initSkipGlobal = initState.SkipGlobal
		initDisclosedNotices = initState.DisclosedNotices
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState:    nil,
		overlayURL:       initOverlayURL,
		noOverlay:        initNoOverlay,
		skipGlobal:       initSkipGlobal,
		configSourceURL:  a.ConfigSourceURL,
		disclosedNotices: initDisclosedNotices,
	})
	if err != nil {
		_ = os.RemoveAll(instanceRoot)
		return "", err
	}

	instanceNumber := instanceNumberFromName(cfg.Workspace.Name, instanceName)

	saveWorkspaceRootDisclosures(workspaceRoot, initState, result.disclosedNotices)

	configName := cfg.Workspace.Name
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   instanceName,
		InstanceNumber: instanceNumber,
		Root:           instanceRoot,
		Created:        now,
		LastApplied:    now,
		ManagedFiles:   result.managedFiles,
		Repos:          result.repoStates,
		Shadows:        result.shadows,
		OverlayURL:     result.overlayURL,
		OverlayCommit:  result.overlayCommit,
	}

	if err := SaveState(instanceRoot, state); err != nil {
		return "", fmt.Errorf("saving instance state: %w", err)
	}

	n := len(result.repoStates)
	if n == 1 {
		a.Reporter.Log("created %s (1 repo) → %s", instanceName, instanceRoot)
	} else {
		a.Reporter.Log("created %s (%d repos) → %s", instanceName, n, instanceRoot)
	}
	for _, w := range result.warnings {
		a.Reporter.DeferWarn("%s", w)
	}
	a.Reporter.FlushDeferred()

	return instanceRoot, nil
}

// Apply runs the full apply pipeline on an existing instance: discover repos,
// classify, clone, install content, clean up removed repos, and update state.
func (a *Applier) Apply(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
	now := time.Now()

	// Auto-pull config from origin if it's a git repo with a remote.
	// Moved here from cli/apply.go so that the Reporter routes output.
	if syncErr := SyncConfigDir(configDir, a.Reporter, a.AllowDirty); syncErr != nil {
		return syncErr
	}

	// Ensure the instance root's .gitignore covers *.local*. Applier.
	// Create already runs this during initial scaffolding, but an
	// instance created before this guard landed won't have the file;
	// running it here closes the upgrade-path gap. The helper is
	// idempotent, so no-op on subsequent applies.
	if err := EnsureInstanceGitignore(instanceRoot); err != nil {
		return fmt.Errorf("preparing instance .gitignore: %w", err)
	}

	// Load existing state (required for Apply).
	existingState, err := LoadState(instanceRoot)
	if err != nil {
		return fmt.Errorf("loading existing state: %w", err)
	}

	// Load workspace root state for workspace-level disclosures. The workspace
	// root is the parent of the instance directory. Errors are silently ignored:
	// a missing workspace root state just means no notices have been disclosed yet.
	workspaceRoot := filepath.Dir(instanceRoot)
	wsRootState, _ := LoadState(workspaceRoot)

	// Check drift on existing managed files before overwriting.
	for _, mf := range existingState.ManagedFiles {
		drift, err := CheckDrift(mf)
		if err != nil {
			a.Reporter.DeferWarn("could not check drift for %s: %v", mf.Path, err)
			continue
		}
		if drift.Drifted() && !drift.FileRemoved {
			a.Reporter.DeferWarn("managed file %s has been modified outside niwa", mf.Path)
		}
	}

	var wsDisclosedNotices []string
	if wsRootState != nil {
		wsDisclosedNotices = wsRootState.DisclosedNotices
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState:    existingState,
		skipGlobal:       existingState.SkipGlobal,
		overlayURL:       existingState.OverlayURL,
		noOverlay:        existingState.NoOverlay,
		configSourceURL:  a.ConfigSourceURL,
		disclosedNotices: wsDisclosedNotices,
	})
	if err != nil {
		return err
	}

	// Emit `rotated <path>` to stderr for every managed file whose
	// SourceFingerprint changed against the previous state AND whose
	// change includes a vault source token flip. First-time
	// materializations (no prior state entry for the path) are NOT
	// rotations — they're the initial write of that file. See PRD
	// Rotation AC.
	emitRotatedFiles(existingState, result, a.Reporter.Writer())

	// Clean up managed files from previous state that are no longer produced.
	a.cleanRemovedFiles(existingState, result)

	// Clean up empty group directories for repos that were removed.
	a.cleanRemovedGroupDirs(existingState, result, instanceRoot)

	// Determine overlay fields for the final state. Convention discovery in
	// runPipeline returns the discovered URL/commit via pipelineResult; carry
	// those forward into the final SaveState. If not updated, preserve the
	// values from existingState. NoOverlay is never cleared by apply.
	finalOverlayURL := existingState.OverlayURL
	finalOverlayCommit := existingState.OverlayCommit
	if result.overlayURL != "" {
		finalOverlayURL = result.overlayURL
		finalOverlayCommit = result.overlayCommit
	}

	saveWorkspaceRootDisclosures(workspaceRoot, wsRootState, result.disclosedNotices)

	configName := cfg.Workspace.Name
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   cfg.Workspace.Name,
		InstanceNumber: existingState.InstanceNumber,
		Root:           instanceRoot,
		Created:        existingState.Created,
		LastApplied:    now,
		SkipGlobal:     existingState.SkipGlobal,
		NoOverlay:      existingState.NoOverlay,
		OverlayURL:     finalOverlayURL,
		OverlayCommit:  finalOverlayCommit,
		ManagedFiles:   result.managedFiles,
		Repos:          result.repoStates,
		Shadows:        result.shadows,
	}

	if err := SaveState(instanceRoot, state); err != nil {
		return fmt.Errorf("saving instance state: %w", err)
	}

	n := len(result.repoStates)
	if n == 1 {
		a.Reporter.Log("applied %s (1 repo)", filepath.Base(instanceRoot))
	} else {
		a.Reporter.Log("applied %s (%d repos)", filepath.Base(instanceRoot), n)
	}
	for _, w := range result.warnings {
		a.Reporter.DeferWarn("%s", w)
	}
	a.Reporter.FlushDeferred()

	return nil
}

// runPipeline executes the shared pipeline steps: discover repos, classify,
// clone, and install content. It returns the pipeline results without writing
// state.
func (a *Applier) runPipeline(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string, now time.Time, opts *pipelineOpts) (*pipelineResult, error) {
	// Step 0: Inject channel hooks into cfg.Claude.Hooks so HooksMaterializer
	// writes them per-repo. Must run before any per-repo processing so that
	// every repo in the workspace receives the channel hook scripts.
	// cfg is a pointer; injectChannelHooks mutates its Hooks map in place.
	injectChannelHooks(cfg)

	// overlayDir is the local clone path of the overlay repo when one is active.
	// It is local to this pipeline run; downstream steps that need it receive it
	// as a function argument rather than reading it from the Applier.
	var overlayDir string

	var writtenFiles []string
	var allWarnings []string
	// sourceTuples aggregates per-file source provenance across every
	// materializer call. The key is the absolute on-disk path of the
	// written file; the value is the ordered list of SourceEntry
	// tuples reported by the materializer that produced it. Used in
	// Step 7 below to populate ManagedFile.Sources and compute
	// SourceFingerprint. Files not recorded here (content files,
	// hook scripts, etc.) get empty Sources and an empty
	// SourceFingerprint, which is semantically "no upstream to
	// attribute" — drift for those files is pure content-hash drift.
	sourceTuples := map[string][]SourceEntry{}

	// Multi-org auth: read the local credential file once for the whole
	// pipeline. authEntries is consumed by every layer that builds a vault
	// bundle — the overlay (Step 0.6), the team config (Step 6), and the
	// personal global overlay (Step 6). The file lives at
	// ~/.config/niwa/provider-auth.toml (same directory that holds the
	// global config clone). When the file is absent, authEntries is nil
	// and every injection call is a no-op (single-org path unchanged).
	// newDisclosures collects one-time notice keys first shown during this run.
	// Returned in pipelineResult so callers can merge them into DisclosedNotices.
	var newDisclosures []string

	var authEntries []ProviderAuthEntry
	if niwaConfigDir, err := NiwaConfigDir(); err == nil {
		entries, err := LoadProviderAuth(niwaConfigDir)
		if err != nil {
			return nil, err
		}
		authEntries = entries
	}

	// Step 0.5: overlay sync and merge — determine and sync the workspace overlay.
	// This must run BEFORE discoverAllRepos so that the merged config (base +
	// overlay) feeds into discovery — overlay sources can then contribute repos.
	// Three branches based on NoOverlay and OverlayURL from instance state:
	//   1. NoOverlay=true  → skip entirely; overlayDir stays empty.
	//   2. OverlayURL set  → sync existing clone; hard error on sync failure.
	//   3. Neither         → attempt convention discovery from ConfigSourceURL.
	var pipelineOverlayURL string    // non-empty when convention discovery sets OverlayURL
	var pipelineOverlayCommit string // non-empty when convention discovery sets OverlayCommit
	if !opts.noOverlay {
		switch {
		case opts.overlayURL != "":
			// Branch 2: OverlayURL was set in state — sync the existing clone.
			dir, dirErr := config.OverlayDir(opts.overlayURL)
			if dirErr != nil {
				return nil, fmt.Errorf("resolving overlay directory: %w", dirErr)
			}
			_, syncErr := a.cloneOrSync(opts.overlayURL, dir)
			if syncErr != nil {
				// Any sync failure is a hard error when OverlayURL is registered.
				return nil, fmt.Errorf("workspace overlay sync failed. Use --no-overlay to skip.")
			}
			// Sync succeeded. Check whether the overlay has advanced.
			sha, shaErr := a.headSHA(dir)
			if shaErr == nil && opts.existingState != nil && opts.existingState.OverlayCommit != "" {
				if sha != opts.existingState.OverlayCommit {
					a.Reporter.DeferWarn("workspace overlay has new commits since last apply (was %s, now %s)",
						opts.existingState.OverlayCommit[:min(7, len(opts.existingState.OverlayCommit))],
						sha[:min(7, len(sha))],
					)
				}
			}
			overlayDir = dir

		case opts.configSourceURL != "":
			// Branch 3: Convention discovery from ConfigSourceURL.
			conventionURL, ok := config.DeriveOverlayURL(opts.configSourceURL)
			if ok {
				dir, dirErr := config.OverlayDir(conventionURL)
				if dirErr == nil {
					wasCloneAttempt, cloneErr := a.cloneOrSync(conventionURL, dir)
					if cloneErr != nil {
						if wasCloneAttempt {
							// Fresh clone failed: overlay repo likely doesn't exist — skip silently.
							break
						}
						// Pull failed on a previously-cloned overlay: hard error.
						return nil, fmt.Errorf("workspace overlay sync failed. Use --no-overlay to skip.")
					}
					// Clone/sync succeeded. Record URL and commit to return via
					// pipelineResult; Apply() will write them in the final SaveState.
					sha, _ := a.headSHA(dir)
					pipelineOverlayURL = conventionURL
					pipelineOverlayCommit = sha
					overlayDir = dir
				}
			}
		}
	}

	// Determine the active overlay URL for repo filtering below.
	// Either branch 2 (opts.overlayURL) or branch 3 (pipelineOverlayURL) may have set it.
	activeOverlayURL := opts.overlayURL
	if activeOverlayURL == "" {
		activeOverlayURL = pipelineOverlayURL
	}

	// Step 0.6: Parse and merge the overlay config when overlayDir is set.
	// The merged config replaces cfg for all subsequent pipeline steps, including
	// discoverAllRepos, so overlay sources contribute repos to discovery.
	//
	// Vault resolution for the overlay's env happens here (per-layer isolation,
	// R23): the overlay's [vault.provider] resolves its own [env.secrets] before
	// the overlay is merged into the base config. This mirrors how the personal
	// overlay (global config) resolves its own secrets against its own provider
	// bundle before MergeGlobalOverride runs.
	if overlayDir != "" {
		overlayTOML := filepath.Join(overlayDir, "workspace-overlay.toml")
		overlay, parseErr := config.ParseOverlay(overlayTOML)
		if parseErr != nil {
			if errors.Is(parseErr, os.ErrNotExist) {
				return nil, fmt.Errorf("workspace overlay is missing workspace-overlay.toml")
			}
			return nil, fmt.Errorf("parsing workspace overlay: %w", parseErr)
		}

		// Inject machine-identity tokens into the overlay's [vault.provider]
		// before building its bundle. Without this, multi-org users whose
		// secrets live in the workspace overlay fall through to the CLI
		// session for that provider.
		if err := injectProviderTokens(ctx, authEntries, overlay.Vault); err != nil {
			return nil, err
		}

		// Build the overlay's own vault bundle. A nil Vault field produces an
		// empty bundle, which is still valid — overlay env without vault:// refs
		// passes through unchanged; any vault:// ref without a declared provider
		// fails with a clear "provider not declared" error.
		overlayVaultBundle, bundleErr := resolve.BuildBundle(ctx, a.vaultRegistry, overlay.Vault, "workspace-overlay.toml")
		if bundleErr != nil {
			return nil, fmt.Errorf("building overlay vault bundle: %w", bundleErr)
		}
		defer overlayVaultBundle.CloseAll()

		// Resolve the overlay's env (top-level and per-repo) against the overlay's
		// own vault bundle. Wrap in a temporary WorkspaceConfig so ResolveWorkspace
		// can be reused without duplicating the resolution walker. Both overlay.Env
		// and overlay.Repos carry the same types as WorkspaceConfig fields, so
		// direct assignment works.
		tmpCfg := &config.WorkspaceConfig{
			Env:   overlay.Env,
			Repos: overlay.Repos,
		}
		resolvedTmp, resolveErr := resolve.ResolveWorkspace(ctx, tmpCfg, resolve.ResolveOptions{
			AllowMissing: a.AllowMissingSecrets,
			TeamBundle:   overlayVaultBundle,
		})
		if resolveErr != nil {
			return nil, fmt.Errorf("resolving overlay vault references: %w", resolveErr)
		}
		overlay.Env = resolvedTmp.Env
		overlay.Repos = resolvedTmp.Repos

		mergedWS, mergeErr := MergeWorkspaceOverlay(cfg, overlay, overlayDir)
		if mergeErr != nil {
			return nil, fmt.Errorf("merging workspace overlay: %w", mergeErr)
		}
		cfg = mergedWS
	}

	// Step 1: Discover repos from all sources (base + overlay after merge).
	allRepos, err := a.discoverAllRepos(ctx, cfg.Sources)
	if err != nil {
		return nil, err
	}

	// Filter the overlay repo itself out of allRepos. When the overlay repo lives
	// in the same org as the workspace source, the org-level auto-scan picks it up
	// as a regular workspace repo. Leaving it in causes spurious output like
	// "skipped fake-dot-niwa-overlay (dirty working tree)" — R22 requires zero
	// output referencing the overlay in standard apply mode.
	if overlayRepoName, ok := config.OverlayRepoName(activeOverlayURL); ok {
		filtered := allRepos[:0]
		for _, r := range allRepos {
			if r.Name != overlayRepoName {
				filtered = append(filtered, r)
			}
		}
		allRepos = filtered
	}

	// Step 2: Classify repos into groups.
	classified, classifyWarnings, err := Classify(allRepos, cfg.Groups)
	if err != nil {
		return nil, fmt.Errorf("classifying repos: %w", err)
	}
	allWarnings = append(allWarnings, classifyWarnings...)

	// Step 2.1: Inject explicit repos (url + group set, not from sources).
	classified, explicitWarnings, err := InjectExplicitRepos(classified, cfg.Repos, cfg.Groups)
	if err != nil {
		return nil, err
	}
	allWarnings = append(allWarnings, explicitWarnings...)

	// Step 2.5: Warn about unknown repo names in [repos] overrides.
	discoveredNames := make([]string, len(allRepos))
	for i, r := range allRepos {
		discoveredNames[i] = r.Name
	}
	known := KnownRepoNames(cfg, discoveredNames)
	allWarnings = append(allWarnings, WarnUnknownRepos(cfg, known)...)

	// Step 2a: Sync global config repo if registered and not skipped.
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		a.Reporter.Status("syncing config...")
		if syncErr := SyncConfigDir(a.GlobalConfigDir, a.Reporter, a.AllowDirty); syncErr != nil {
			a.Reporter.Warn("could not sync config: %v", syncErr)
			return nil, fmt.Errorf("syncing global config: %w", syncErr)
		}
	}

	// Steps 3a–3c: Parse the global config override, then run the
	// vault resolver stage against BOTH team (ws) and personal
	// (overlay) layers before the merge. The resolver must run
	// before merge so that each layer's provider bundle is built
	// from its own [vault] block only (file-local scoping, D-6 in
	// the vault-integration design). Resolving post-merge would
	// flatten provider declarations and make R12 collision
	// detection impossible.
	//
	// cfg remains the original for per-instance reads; effectiveCfg
	// carries the merge.
	effectiveCfg := cfg

	// Build a redactor for this apply invocation and attach it to
	// ctx so every secret.Errorf / Wrap call downstream scrubs
	// resolved values automatically.
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	var globalOverride *config.GlobalConfigOverride
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		overridePath := filepath.Join(a.GlobalConfigDir, GlobalConfigOverrideFile)
		data, readErr := os.ReadFile(overridePath)
		if readErr == nil {
			parsed, parseErr := config.ParseGlobalConfigOverride(data)
			if parseErr != nil {
				return nil, fmt.Errorf("parsing global config override: %w", parseErr)
			}
			globalOverride = parsed
		} else if !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("reading global config override: %w", readErr)
		}
	}

	// R5 enforcement: a workspace with more than one [[sources]] block
	// AND an active personal overlay must declare [workspace].vault_scope
	// to disambiguate which [workspaces.<scope>] block applies. Single-
	// source or zero-source workspaces are unaffected. Runs before the
	// resolver builds bundles so we fail fast without issuing provider
	// RPCs.
	if err := CheckVaultScopeAmbiguity(cfg, globalOverride); err != nil {
		return nil, err
	}

	// Multi-org auth: inject machine-identity tokens into the team config
	// and personal global overlay vault registries. authEntries was loaded
	// at the top of runPipeline so the overlay layer (Step 0.6) could see it.
	if len(authEntries) > 0 {
		if err := injectProviderTokens(ctx, authEntries, cfg.Vault); err != nil {
			return nil, err
		}
		if globalOverride != nil {
			if err := injectProviderTokens(ctx, authEntries, globalOverride.Global.Vault); err != nil {
				return nil, err
			}
		}
	}

	// Build provider bundles from each layer independently. Bundle
	// lifetime is scoped to this apply: defer CloseAll so providers
	// shut down cleanly even on error paths (R29 no-disk-cache).
	teamBundle, err := resolve.BuildBundle(ctx, a.vaultRegistry, cfg.Vault, "workspace config")
	if err != nil {
		return nil, err
	}
	defer teamBundle.CloseAll()

	// BuildBundle is a safe no-op for a nil config.VaultRegistry, so
	// we always call it regardless of whether globalOverride is
	// present; that keeps the defer and the R12 check uniform.
	var overlayRegistry *config.VaultRegistry
	if globalOverride != nil {
		overlayRegistry = globalOverride.Global.Vault
	}
	personalBundle, err := resolve.BuildBundle(ctx, a.vaultRegistry, overlayRegistry, "global overlay")
	if err != nil {
		return nil, err
	}
	defer personalBundle.CloseAll()

	// Provider-level shadow detection (informational). Fires BEFORE
	// the R12 hard error so the user sees the shadow diagnostic
	// even though apply will fail on the collision. The
	// CheckProviderNameCollision call below is the enforcement; this
	// call only emits stderr. The shadow record itself is synthesized
	// at a higher layer (pipelineResult.shadows) from
	// DetectShadows; vault.ProviderShadow is not persisted as a
	// workspace.Shadow today because the pipeline rejects the apply
	// before it could reach SaveState.
	providerShadows := vault.DetectProviderShadows(teamBundle, personalBundle)
	if len(providerShadows) > 0 && !sliceContains(opts.disclosedNotices, noticeProviderShadow) {
		for _, sh := range providerShadows {
			label := sh.Name
			if label == "" {
				label = "(anonymous)"
			}
			a.Reporter.Defer("shadowed provider %q [personal-overlay shadows team: team=%s, personal=%s]",
				label, "workspace.toml", "niwa.toml")
		}
		newDisclosures = append(newDisclosures, noticeProviderShadow)
	}

	// R12 enforcement: personal overlay MUST NOT redeclare any
	// provider name present in the team bundle. This is checked
	// here (not in the resolver) because only this call site has
	// both bundles in scope.
	if err := resolve.CheckProviderNameCollision(teamBundle, personalBundle); err != nil {
		return nil, err
	}

	// R14/R30 enforcement: a workspace config repo with a public
	// GitHub remote MUST NOT carry plaintext values in its *.secrets
	// tables. The guardrail takes the pre-resolve cfg on purpose —
	// the resolver auto-wraps plaintext secrets-table values into
	// secret.Value (so downstream redaction/mode-0o600/Error wrapping
	// still apply even under --allow-plaintext-secrets), which means
	// the post-resolve cfg no longer distinguishes "was plaintext" from
	// "was a vault ref". The original cfg still has Plain populated
	// for plaintext entries and Secret populated only for vault-resolved
	// ones, which is exactly the signal the guardrail reads.
	if err := guardrail.CheckGitHubPublicRemoteSecrets(configDir, cfg, a.AllowPlaintextSecrets, a.Reporter.Writer()); err != nil {
		return nil, err
	}

	// Env/files/settings shadow detection. Pure function over the
	// parsed team config and the parsed (pre-resolve) overlay so no
	// vault calls are needed. Emitted to stderr immediately so the
	// user sees the override-visibility hints during apply; also
	// collected for persistence into InstanceState.Shadows below.
	var pipelineShadows []Shadow
	if globalOverride != nil {
		pipelineShadows = DetectShadows(cfg, globalOverride)
		for _, sh := range pipelineShadows {
			a.Reporter.Defer("shadowed %s %q [personal-overlay shadows team: team=%s, personal=%s]",
				sh.Kind, sh.Name, sh.TeamSource, sh.PersonalSource)
		}
	}

	// Resolve the team workspace config.
	resolvedCfg, err := resolve.ResolveWorkspace(ctx, cfg, resolve.ResolveOptions{
		AllowMissing: a.AllowMissingSecrets,
		TeamBundle:   teamBundle,
	})
	if err != nil {
		return nil, err
	}
	effectiveCfg = resolvedCfg

	// Resolve the personal overlay, then merge it into the team
	// workspace. The merge happens AFTER resolution so that R8
	// team_only enforcement in MergeGlobalOverride sees the
	// overlay's resolved MaybeSecret values, not pre-resolve URIs.
	if globalOverride != nil {
		resolvedOverride, err := resolve.ResolveGlobalOverride(ctx, globalOverride, resolve.ResolveOptions{
			AllowMissing:   a.AllowMissingSecrets,
			PersonalBundle: personalBundle,
		})
		if err != nil {
			return nil, err
		}
		flattened := ResolveGlobalOverride(resolvedOverride, cfg.Workspace.Name)
		merged, err := MergeGlobalOverride(resolvedCfg, flattened, a.GlobalConfigDir)
		if err != nil {
			return nil, err
		}
		effectiveCfg = merged
	}

	// Post-merge required/recommended enforcement (PRD R33/R34). The
	// required check is NOT downgraded by AllowMissingSecrets; the
	// resolver already turned missing vault-backed keys into empty
	// MaybeSecret values when the flag is set, and checkRequiredKeys
	// catches those empty values via the required list.
	if err := checkRequiredKeys(effectiveCfg, a.Reporter.Writer()); err != nil {
		return nil, err
	}

	// Step 3: Create group directories and clone repos concurrently.
	//
	// Group directory creation runs sequentially (fast local I/O; repos in the
	// same group share a dir, so deduplication would be needed if parallelized).
	// Cloning then runs via a bounded worker pool — see cloneWorker for the
	// concurrency model. Both channels must be buffered to len(classified), not
	// to cloneWorkers: workers must always be able to send without blocking so
	// the cancel-then-drain path in the orchestrator cannot deadlock.
	//
	// If a partially-written .git dir survives a cancelled niwa apply, the next
	// apply will attempt git fetch on it via SyncRepo. Remove the affected repo
	// directory and re-run apply to recover.
	repoStates := map[string]RepoState{}
	for _, cr := range classified {
		groupDir := filepath.Join(instanceRoot, cr.Group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating group directory %s: %w", groupDir, err)
		}
	}

	{
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		total := len(classified)
		jobs := make(chan cloneJob, total)
		results := make(chan cloneResult, total)

		workers := min(cloneWorkers, total)
		for range workers {
			go a.cloneWorker(ctx, jobs, results)
		}
		for _, cr := range classified {
			groupDir := filepath.Join(instanceRoot, cr.Group)
			jobs <- cloneJob{
				cr:            cr,
				cloneURL:      RepoCloneURL(effectiveCfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL),
				branch:        RepoCloneBranch(effectiveCfg, cr.Repo.Name),
				targetDir:     filepath.Join(groupDir, cr.Repo.Name),
				defaultBranch: DefaultBranch(effectiveCfg, cr.Repo.Name),
				noPull:        a.NoPull,
			}
		}
		close(jobs)

		a.Reporter.Status(fmt.Sprintf("cloning repos... (0/%d done)", total))
		var cloneErr error
		for done := 0; done < total; done++ {
			r := <-results
			if r.err != nil && cloneErr == nil {
				cloneErr = fmt.Errorf("cloning repo %s: %w", r.name, r.err)
				cancel()
			}
			if cloneErr == nil {
				if r.syncWarn != "" {
					a.Reporter.DeferWarn("%s", r.syncWarn)
				}
				repoStates[r.name] = RepoState{
					URL:    r.cloneURL,
					Cloned: r.cloned || repoAlreadyCloned(r.targetDir),
				}
			}
			a.Reporter.Status(fmt.Sprintf("cloning repos... (%d/%d done)", done+1, total))
		}
		if cloneErr != nil {
			return nil, cloneErr
		}
	}

	// Step 4: Install workspace-level CLAUDE.md.
	wsFiles, err := InstallWorkspaceContent(effectiveCfg, configDir, instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("installing workspace content: %w", err)
	}
	writtenFiles = append(writtenFiles, wsFiles...)

	// Build repo name -> on-disk path index (used by marketplace resolution
	// and materializers).
	repoIndex := make(map[string]string, len(classified))
	for _, cr := range classified {
		repoIndex[cr.Repo.Name] = filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
	}

	// Step 4.5: Install workspace-root context and settings.
	// Context file uses @import for level-scoped visibility.
	// Settings uses settings.json (not .local) since instance root is non-git.

	// Workspace context must be installed first so @workspace-context.md is
	// present in CLAUDE.md before overlay and global imports are injected.
	// This ensures the three-way ordering (@workspace-context.md →
	// @CLAUDE.overlay.md → @CLAUDE.global.md) is established on first apply.
	ctxFiles, err := InstallWorkspaceContext(effectiveCfg, classified, instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("installing workspace context: %w", err)
	}
	writtenFiles = append(writtenFiles, ctxFiles...)

	// Overlay CLAUDE.overlay.md is injected after @workspace-context.md
	// (now present) so that @CLAUDE.global.md (added at step 5c) ends up
	// after both — completing the required import order.
	if overlayDir != "" {
		overlayCtxPath, overlayCtxErr := InstallOverlayClaudeContent(overlayDir, instanceRoot)
		if overlayCtxErr != nil {
			return nil, fmt.Errorf("installing overlay claude content: %w", overlayCtxErr)
		}
		if overlayCtxPath != "" {
			writtenFiles = append(writtenFiles, overlayCtxPath)
		}
	}

	rootSettingsFiles, err := InstallWorkspaceRootSettings(effectiveCfg, configDir, instanceRoot, repoIndex)
	if err != nil {
		return nil, fmt.Errorf("installing workspace root settings: %w", err)
	}
	writtenFiles = append(writtenFiles, rootSettingsFiles...)

	// Step 4.75: Install channel infrastructure (sessions dir, sessions.json,
	// .mcp.json, ## Channels section) when [channels.mesh] is configured.
	if err := InstallChannelInfrastructure(effectiveCfg, instanceRoot, &writtenFiles); err != nil {
		return nil, fmt.Errorf("installing channel infrastructure: %w", err)
	}

	// Step 5: Install group-level CLAUDE.md files.
	installedGroups := map[string]bool{}
	for _, cr := range classified {
		if installedGroups[cr.Group] {
			continue
		}
		installedGroups[cr.Group] = true

		groupFiles, err := InstallGroupContent(effectiveCfg, configDir, instanceRoot, cr.Group)
		if err != nil {
			return nil, fmt.Errorf("installing group content for %q: %w", cr.Group, err)
		}
		writtenFiles = append(writtenFiles, groupFiles...)
	}

	// Step 5c: Install global CLAUDE.md content if global config is active.
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		globalFiles, err := InstallGlobalClaudeContent(a.GlobalConfigDir, instanceRoot)
		if err != nil {
			return nil, fmt.Errorf("installing global claude content: %w", err)
		}
		writtenFiles = append(writtenFiles, globalFiles...)
	}

	// Step 6: Install repo-level CLAUDE.local.md files (and subdirectories).
	// Skip repos with claude = false.
	for _, cr := range classified {
		if !ClaudeEnabled(effectiveCfg, cr.Repo.Name) {
			continue
		}

		result, err := InstallRepoContent(effectiveCfg, configDir, overlayDir, instanceRoot, cr.Group, cr.Repo.Name)
		if err != nil {
			return nil, fmt.Errorf("installing repo content for %q: %w", cr.Repo.Name, err)
		}
		for _, w := range result.Warnings {
			allWarnings = append(allWarnings, w.String())
		}
		writtenFiles = append(writtenFiles, result.WrittenFiles...)
	}

	// Step 6.5: Run materializers (hooks, settings, env) for each repo.
	discoveredHooks, _ := DiscoverHooks(configDir)
	wsEnvFile, repoEnvFiles, _ := DiscoverEnvFiles(configDir)

	// Convert discovered env paths to relative (the env materializer joins
	// file paths with configDir, so they must be relative).
	relWsEnv := wsEnvFile
	if relWsEnv != "" {
		if r, err := filepath.Rel(configDir, relWsEnv); err == nil {
			relWsEnv = r
		}
	}

	discoveredEnv := &DiscoveredEnv{
		WorkspaceFile: relWsEnv,
		RepoFiles:     repoEnvFiles,
	}

	for _, cr := range classified {
		effective := MergeOverrides(effectiveCfg, cr.Repo.Name)

		// Merge discovered hooks as base, explicit config wins per event.
		if len(discoveredHooks) > 0 {
			merged := make(config.HooksConfig, len(discoveredHooks)+len(effective.Claude.Hooks))
			// Start with discovered hooks (converted to relative paths).
			for event, entries := range discoveredHooks {
				var relEntries []config.HookEntry
				for _, entry := range entries {
					relScripts := make([]string, 0, len(entry.Scripts))
					for _, absPath := range entry.Scripts {
						rel, err := filepath.Rel(configDir, absPath)
						if err != nil {
							return nil, fmt.Errorf("materializer hooks: computing relative path for %s: %w", absPath, err)
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
			// Explicit config overrides per event.
			for event, entries := range effective.Claude.Hooks {
				merged[event] = entries
			}
			effective.Claude.Hooks = merged
		}

		repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
		mctx := &MaterializeContext{
			Config:                effectiveCfg,
			Effective:             effective,
			RepoName:              cr.Repo.Name,
			RepoDir:               repoDir,
			ConfigDir:             configDir,
			DiscoveredEnv:         discoveredEnv,
			RepoIndex:             repoIndex,
			SourceTuples:          sourceTuples,
			AllowPlaintextSecrets: a.AllowPlaintextSecrets,
			Stderr:                a.Reporter.Writer(),
		}

		claudeOn := ClaudeEnabled(effectiveCfg, cr.Repo.Name)
		for _, m := range a.Materializers {
			// Skip hooks and settings materializers when claude is disabled.
			if !claudeOn && (m.Name() == "hooks" || m.Name() == "settings") {
				continue
			}

			files, err := m.Materialize(mctx)
			if err != nil {
				return nil, fmt.Errorf("materializer %s for repo %s: %w", m.Name(), cr.Repo.Name, err)
			}
			writtenFiles = append(writtenFiles, files...)
		}
	}

	// Step 6.75: Run repo-provided setup scripts.
	for _, cr := range classified {
		setupDir := ResolveSetupDir(effectiveCfg, cr.Repo.Name)
		repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
		result := RunSetupScripts(repoDir, setupDir, a.Reporter)

		if result.Disabled || result.Skipped {
			continue
		}

		for _, sr := range result.Scripts {
			if sr.Error != nil {
				a.Reporter.DeferWarn("setup script %s/%s failed for %s: %v",
					setupDir, sr.Name, cr.Repo.Name, sr.Error)
			}
		}
	}

	// Step 7: Build managed files with hashes and per-source
	// provenance. Sources are populated by materializers via
	// MaterializeContext.SourceTuples; files written outside the
	// materializer pipeline (content files, workspace CLAUDE.md,
	// etc.) have no recorded sources, which is fine — their drift
	// check falls back to the ContentHash-only path.
	managedFiles := make([]ManagedFile, 0, len(writtenFiles))
	for _, path := range writtenFiles {
		hash, err := HashFile(path)
		if err != nil {
			return nil, fmt.Errorf("hashing managed file %s: %w", path, err)
		}
		sources := sourceTuples[path]
		mf := ManagedFile{
			Path:        path,
			ContentHash: hash,
			Generated:   now,
			Sources:     sources,
		}
		if len(sources) > 0 {
			mf.SourceFingerprint = ComputeSourceFingerprint(sources)
		}
		managedFiles = append(managedFiles, mf)
	}

	return &pipelineResult{
		classified:       classified,
		repoStates:       repoStates,
		managedFiles:     managedFiles,
		warnings:         allWarnings,
		shadows:          pipelineShadows,
		overlayURL:       pipelineOverlayURL,
		overlayCommit:    pipelineOverlayCommit,
		disclosedNotices: newDisclosures,
	}, nil
}

// cleanRemovedFiles deletes managed files from the previous state that are
// no longer present in the current pipeline result.
func (a *Applier) cleanRemovedFiles(existingState *InstanceState, result *pipelineResult) {
	currentFiles := make(map[string]bool, len(result.managedFiles))
	for _, mf := range result.managedFiles {
		currentFiles[mf.Path] = true
	}

	for _, mf := range existingState.ManagedFiles {
		if !currentFiles[mf.Path] {
			if err := os.Remove(mf.Path); err != nil && !os.IsNotExist(err) {
				a.Reporter.DeferWarn("could not remove managed file %s: %v", mf.Path, err)
			}
		}
	}
}

// cleanRemovedGroupDirs removes empty group directories for repos that
// existed in the previous state but are no longer present.
func (a *Applier) cleanRemovedGroupDirs(existingState *InstanceState, result *pipelineResult, instanceRoot string) {
	// Build set of current group names.
	currentGroups := make(map[string]bool)
	for _, cr := range result.classified {
		currentGroups[cr.Group] = true
	}

	// Check previous classified repos by looking at managed file paths
	// to infer group directories. We check if group dirs that held repos
	// in the previous state are now empty.
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == StateDir {
			continue
		}
		if currentGroups[name] {
			continue
		}
		groupDir := filepath.Join(instanceRoot, name)
		// Only remove if empty.
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		if len(subEntries) == 0 {
			os.Remove(groupDir)
		}
	}
}

// instanceNumberFromName derives the InstanceNumber to store from the instance
// directory name. For the base instance (name == configName) it returns 1.
// For numbered instances (configName-N) it returns N. For custom-suffix names
// (configName-hotfix) it returns 0 so the status display omits the number.
func instanceNumberFromName(configName, instanceName string) int {
	if instanceName == configName {
		return 1
	}
	if strings.HasPrefix(instanceName, configName+"-") {
		suffix := instanceName[len(configName)+1:]
		if n, err := strconv.Atoi(suffix); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// sliceContains reports whether s contains elem.
func sliceContains(s []string, elem string) bool {
	for _, v := range s {
		if v == elem {
			return true
		}
	}
	return false
}

// saveWorkspaceRootDisclosures merges new notice keys into the workspace root
// state file. It is a best-effort call: errors are silently ignored because a
// failed write only means the notice reappears on the next run — a recoverable
// annoyance rather than a data-loss scenario.
func saveWorkspaceRootDisclosures(workspaceRoot string, existing *InstanceState, newNotices []string) {
	if len(newNotices) == 0 {
		return
	}
	s := existing
	if s == nil {
		s = &InstanceState{}
	}
	s.DisclosedNotices = mergeDisclosedNotices(s.DisclosedNotices, newNotices)
	_ = SaveState(workspaceRoot, s)
}

// emitRotatedFiles prints `rotated <path>` to stderrOut for every
// managed file in the new pipeline result whose SourceFingerprint
// differs from the previous state's entry for the same path AND whose
// difference is driven by at least one vault SourceEntry with a
// changed VersionToken (i.e., the upstream provider returned a new
// revision for a key this file depends on).
//
// Brand-new files (no entry in the previous state for the path) are
// SKIPPED: a first-time materialization is not a rotation.
// Plaintext-only changes (content edits in env.files, for example)
// also don't count as rotations — they surface through drift, not
// rotation. This mirrors the semantics described in PRD-vault-
// integration §Rotation AC and matches the user-visible `vault-rotated`
// output emitted by `niwa status --check-vault`.
func emitRotatedFiles(prev *InstanceState, result *pipelineResult, stderrOut io.Writer) {
	if prev == nil {
		return
	}

	// Index previous managed files by path for O(1) lookup. A nil
	// result or empty ManagedFiles list yields an empty map, in which
	// case the loop below finds no matches and emits nothing — the
	// correct "no rotations to report" path.
	prevByPath := make(map[string]ManagedFile, len(prev.ManagedFiles))
	for _, mf := range prev.ManagedFiles {
		prevByPath[mf.Path] = mf
	}

	for _, mfNew := range result.managedFiles {
		mfOld, existed := prevByPath[mfNew.Path]
		if !existed {
			// First-time materialization, not a rotation.
			continue
		}
		if mfNew.SourceFingerprint == mfOld.SourceFingerprint {
			continue
		}
		if !hasVaultRotation(mfOld.Sources, mfNew.Sources) {
			continue
		}
		fmt.Fprintf(stderrOut, "rotated %s\n", mfNew.Path)
	}
}

// hasVaultRotation reports whether any vault SourceEntry in newSources
// has a different VersionToken than the same-SourceID entry in
// oldSources. A new vault source (not present in oldSources) also
// counts as a rotation relative to the previous state because the file
// is now backed by a vault key it wasn't backed by before.
//
// Only vault sources drive rotation output. A plaintext-source change
// (different file content hash) is drift, not rotation, and is
// reported via the existing drift-check path in niwa status.
func hasVaultRotation(oldSources, newSources []SourceEntry) bool {
	oldTokens := make(map[string]string, len(oldSources))
	for _, s := range oldSources {
		if s.Kind == SourceKindVault {
			oldTokens[s.SourceID] = s.VersionToken
		}
	}
	for _, s := range newSources {
		if s.Kind != SourceKindVault {
			continue
		}
		oldToken, existed := oldTokens[s.SourceID]
		if !existed {
			return true
		}
		if oldToken != s.VersionToken {
			return true
		}
	}
	return false
}

// repoAlreadyCloned checks if a directory has a .git marker, indicating
// it was previously cloned.
func repoAlreadyCloned(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// cloneWorker pulls jobs from the jobs channel until it is closed, clones or
// syncs each repo, and sends a cloneResult for each. Workers use a no-op
// reporter so all Reporter calls remain on the orchestrator goroutine.
func (a *Applier) cloneWorker(ctx context.Context, jobs <-chan cloneJob, results chan<- cloneResult) {
	noop := NewReporterWithTTY(io.Discard, false)
	for job := range jobs {
		cloned, err := a.Cloner.CloneWithBranch(ctx, job.cloneURL, job.targetDir, job.branch, noop)
		if err != nil {
			results <- cloneResult{
				name:      job.cr.Repo.Name,
				cloneURL:  job.cloneURL,
				targetDir: job.targetDir,
				err:       err,
			}
			continue
		}
		var syncWarn string
		if !cloned && !job.noPull {
			result, syncErr := SyncRepo(ctx, job.targetDir, job.defaultBranch, noop)
			if result.Action == "fetch-failed" {
				syncWarn = fmt.Sprintf("could not fetch %s: %s", job.cr.Repo.Name, result.Reason)
			}
			if syncErr != nil {
				if syncWarn != "" {
					syncWarn += "; "
				}
				syncWarn += fmt.Sprintf("sync failed for %s: %v", job.cr.Repo.Name, syncErr)
			}
		}
		results <- cloneResult{
			name:      job.cr.Repo.Name,
			cloneURL:  job.cloneURL,
			targetDir: job.targetDir,
			cloned:    cloned,
			syncWarn:  syncWarn,
		}
	}
}

// discoverAllRepos collects repos from all sources, enforcing per-source
// thresholds and detecting cross-source duplicate repo names.
func (a *Applier) discoverAllRepos(ctx context.Context, sources []config.SourceConfig) ([]github.Repo, error) {
	var allRepos []github.Repo
	seen := map[string]string{} // repo name -> source org (for duplicate detection)

	for _, source := range sources {
		repos, err := a.discoverRepos(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("discovering repos for org %q: %w", source.Org, err)
		}

		for _, r := range repos {
			if prevOrg, exists := seen[r.Name]; exists {
				return nil, fmt.Errorf(
					"duplicate repo name %q found in orgs %q and %q; rename or use explicit repos lists to resolve",
					r.Name, prevOrg, source.Org,
				)
			}
			seen[r.Name] = source.Org
		}

		allRepos = append(allRepos, repos...)
	}

	return allRepos, nil
}

func (a *Applier) discoverRepos(ctx context.Context, source config.SourceConfig) ([]github.Repo, error) {
	// If the source specifies explicit repos, build the list directly
	// without calling the GitHub API.
	if len(source.Repos) > 0 {
		repos := make([]github.Repo, len(source.Repos))
		for i, name := range source.Repos {
			repos[i] = github.Repo{
				Name:     name,
				SSHURL:   fmt.Sprintf("git@github.com:%s/%s.git", source.Org, name),
				CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", source.Org, name),
			}
		}
		return repos, nil
	}

	// Auto-discover via API.
	repos, err := a.GitHubClient.ListRepos(ctx, source.Org)
	if err != nil {
		return nil, err
	}

	maxRepos := source.MaxRepos
	if maxRepos == 0 {
		maxRepos = DefaultMaxRepos
	}

	if len(repos) > maxRepos {
		return nil, fmt.Errorf(
			"org %q has %d repos, which exceeds the max_repos threshold of %d; "+
				"set max_repos to a higher value in [[sources]] or provide an explicit repos list",
			source.Org, len(repos), maxRepos,
		)
	}

	return repos, nil
}
