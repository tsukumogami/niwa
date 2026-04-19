package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// pipelineOpts configures shared pipeline behavior for Create vs Apply.
type pipelineOpts struct {
	existingState   *InstanceState
	skipGlobal      bool
	overlayURL      string // from InstanceState.OverlayURL (empty = no overlay URL in state)
	noOverlay       bool   // from InstanceState.NoOverlay
	configSourceURL string // original source URL for convention overlay discovery
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
	shadows       []Shadow
	overlayURL    string // set when convention discovery succeeds; empty otherwise
	overlayCommit string // HEAD SHA when overlayURL was set; empty otherwise
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
	if initState != nil {
		initOverlayURL = initState.OverlayURL
		initNoOverlay = initState.NoOverlay
		initSkipGlobal = initState.SkipGlobal
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState:   nil,
		overlayURL:      initOverlayURL,
		noOverlay:       initNoOverlay,
		skipGlobal:      initSkipGlobal,
		configSourceURL: a.ConfigSourceURL,
	})
	if err != nil {
		_ = os.RemoveAll(instanceRoot)
		return "", err
	}

	for _, w := range result.warnings {
		a.Reporter.Warn("%s", w)
	}

	instanceNumber, err := NextInstanceNumber(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("determining instance number: %w", err)
	}

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

	// Check drift on existing managed files before overwriting.
	for _, mf := range existingState.ManagedFiles {
		drift, err := CheckDrift(mf)
		if err != nil {
			a.Reporter.Warn("could not check drift for %s: %v", mf.Path, err)
			continue
		}
		if drift.Drifted() && !drift.FileRemoved {
			a.Reporter.Warn("managed file %s has been modified outside niwa", mf.Path)
		}
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState:   existingState,
		skipGlobal:      existingState.SkipGlobal,
		overlayURL:      existingState.OverlayURL,
		noOverlay:       existingState.NoOverlay,
		configSourceURL: a.ConfigSourceURL,
	})
	if err != nil {
		return err
	}

	for _, w := range result.warnings {
		a.Reporter.Warn("%s", w)
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

	return nil
}

// runPipeline executes the shared pipeline steps: discover repos, classify,
// clone, and install content. It returns the pipeline results without writing
// state.
func (a *Applier) runPipeline(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string, now time.Time, opts *pipelineOpts) (*pipelineResult, error) {
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
					a.Reporter.Warn("workspace overlay has new commits since last apply (was %s, now %s)",
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
		a.Reporter.Log("synced config")
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
	for _, sh := range providerShadows {
		label := sh.Name
		if label == "" {
			label = "(anonymous)"
		}
		a.Reporter.Log("shadowed provider %q [personal-overlay shadows team: team=%s, personal=%s]",
			label, "workspace.toml", "niwa.toml")
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
			a.Reporter.Log("shadowed %s %q [personal-overlay shadows team: team=%s, personal=%s]",
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

	// Step 3: Create group directories and clone repos.
	repoStates := map[string]RepoState{}
	for _, cr := range classified {
		groupDir := filepath.Join(instanceRoot, cr.Group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating group directory %s: %w", groupDir, err)
		}

		cloneURL := RepoCloneURL(effectiveCfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL)
		branch := RepoCloneBranch(effectiveCfg, cr.Repo.Name)

		targetDir := filepath.Join(groupDir, cr.Repo.Name)
		a.Reporter.Status(fmt.Sprintf("cloning %s...", cr.Repo.Name))
		cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch, a.Reporter)
		if err != nil {
			a.Reporter.Warn("failed to clone %s", cr.Repo.Name)
			return nil, fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
		}
		if cloned {
			a.Reporter.Log("cloned %s into %s", cr.Repo.Name, targetDir)
		} else if !a.NoPull {
			a.Reporter.Status(fmt.Sprintf("syncing %s...", cr.Repo.Name))
			defaultBranch := DefaultBranch(effectiveCfg, cr.Repo.Name)
			result, syncErr := SyncRepo(ctx, targetDir, defaultBranch, a.Reporter)
			switch result.Action {
			case "pulled":
				a.Reporter.Log("pulled %s (%d commits)", cr.Repo.Name, result.Commits)
			case "up-to-date":
				a.Reporter.Log("skipped %s (up to date)", cr.Repo.Name)
			case "fetch-failed":
				a.Reporter.Warn("could not fetch %s: %s", cr.Repo.Name, result.Reason)
			case "skipped":
				a.Reporter.Log("skipped %s (%s)", cr.Repo.Name, result.Reason)
			}
			if syncErr != nil {
				a.Reporter.Warn("sync failed for %s: %v", cr.Repo.Name, syncErr)
			}
		} else {
			a.Reporter.Log("skipped %s (already exists)", cr.Repo.Name)
		}

		repoStates[cr.Repo.Name] = RepoState{
			URL:    cloneURL,
			Cloned: cloned || repoAlreadyCloned(targetDir),
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
			a.Reporter.Log("skipped content for %s (claude = false)", cr.Repo.Name)
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
				a.Reporter.Warn("setup script %s/%s failed for %s: %v",
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
		classified:    classified,
		repoStates:    repoStates,
		managedFiles:  managedFiles,
		warnings:      allWarnings,
		shadows:       pipelineShadows,
		overlayURL:    pipelineOverlayURL,
		overlayCommit: pipelineOverlayCommit,
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
				a.Reporter.Warn("could not remove managed file %s: %v", mf.Path, err)
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
