package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/envformat"
	"github.com/tsukumogami/niwa/internal/gitexclude"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
)

// secretFileMode is the permission bits used for any file that may
// contain resolved secret material. The materializers write every
// file with this mode unconditionally so non-vault configs also get
// the tighter permissions (this fixes a pre-existing 0o644 bug where
// env and settings files were world-readable).
const secretFileMode os.FileMode = 0o600

// maybeSecretString returns the plaintext string of m, revealing
// the secret bytes when m carries a resolved Secret. This is the
// materializer counterpart to MaybeSecret.String (which redacts
// secrets to "***"); it is used only inside the write path where
// the plaintext must reach the destination file.
//
// Callers must not retain the returned string past the short-lived
// write operation that needs plaintext: it carries a copy of the
// underlying buffer from reveal.UnsafeReveal.
func maybeSecretString(m config.MaybeSecret) string {
	if m.IsSecret() {
		return string(reveal.UnsafeReveal(m.Secret))
	}
	return m.Plain
}

// injectLocalInfix ensures the given filename contains ".local". If
// the filename already contains ".local" (as a substring of the
// basename), it is returned unchanged; otherwise localRename is used
// to insert ".local" before the extension. This enforces the
// invariant that every materialized file matches the *.local*
// gitignore pattern, even when the user-written destination path did
// not include the infix.
func injectLocalInfix(filename string) string {
	if strings.Contains(filename, ".local") {
		return filename
	}
	return localRename(filename)
}

// DiscoveredEnv holds auto-discovered env file paths at workspace and repo
// levels. These are used as fallbacks when no explicit [env].files are
// configured.
type DiscoveredEnv struct {
	WorkspaceFile string            // auto-discovered workspace.env path (empty if none)
	RepoFiles     map[string]string // repoName -> auto-discovered env file path
}

// MaterializeContext holds the state needed by materializers when installing
// configuration into a repository directory.
type MaterializeContext struct {
	Config         *config.WorkspaceConfig
	Effective      EffectiveConfig
	RepoName       string
	RepoDir        string
	ConfigDir      string
	InstalledHooks map[string][]InstalledHookEntry // event -> installed hook entries, populated by hooks materializer
	DiscoveredEnv  *DiscoveredEnv                  // auto-discovered env files, may be nil
	RepoIndex      map[string]string               // repo name -> on-disk path, for marketplace resolution

	// AllowPlaintextSecrets, when true, downgrades every .env.example pre-pass
	// fail to warn for this run. Populated from Applier.AllowPlaintextSecrets
	// via apply.go. Each downgrade emits a per-key audit diagnostic.
	AllowPlaintextSecrets bool

	// GlobalEnvExamplePolicy is the resolved personal/global .env.example
	// failure policy for the active workspace (the flattened GlobalOverride
	// EnvExamplePolicy). It is the broadest category rung consulted by
	// EffectiveEnvExamplePolicy in the pre-pass. nil means no global rung is
	// configured (the resolver treats nil as "skip this rung").
	GlobalEnvExamplePolicy *config.EnvExamplePolicy

	// GlobalEnvOutput is the resolved personal/global secret-output target
	// declaration (the flattened GlobalOverride EnvOutput). It is the broadest
	// rung consulted by EffectiveEnvOutput. Empty means no global rung is set.
	GlobalEnvOutput config.OutputTargets

	// EnvOutputs accumulates the repo-relative paths of the secret-output
	// targets the EnvMaterializer wrote, so the apply/worktree caller can pass
	// them to gitexclude.EnsureRepoExclude as ignore coverage for any custom
	// target name not matched by the base "*.local*" pattern.
	EnvOutputs []string

	// SourceTuples, when non-nil, is populated by materializers with
	// the per-file list of SourceEntry tuples describing which inputs
	// contributed bytes to each written file. apply.go wires a shared
	// map across the materializer loop and consumes it after all
	// materializers run to compute ManagedFile.SourceFingerprint.
	// Materializers must not mutate entries already present for a
	// key; tests relying on map identity pass a nil map explicitly.
	SourceTuples map[string][]SourceEntry

	// EnvExampleVars holds key-value pairs loaded from .env.example during the
	// pre-pass. Nil when the feature is disabled, file is absent, or pre-pass
	// failed. ResolveEnvVars seeds vars from this before other layers so
	// .env.example is the lowest-priority source.
	EnvExampleVars map[string]string
	// EnvExampleSources holds the SourceEntry tuples for the .env.example layer,
	// parallel to EnvExampleVars. Populated when EnvExampleVars is set.
	EnvExampleSources []SourceEntry

	// Stderr, if non-nil, receives diagnostic warnings emitted during
	// materialization. When nil, warnings fall back to os.Stderr.
	// apply.go wires a.Reporter.Writer() so warnings clear the spinner
	// before printing.
	Stderr io.Writer

	// WorktreeDelegation carries the apply-time worktree-integration decision
	// (probe result + niwa absolute path), computed ONCE per apply and threaded
	// to every repo's SettingsMaterializer. nil installs neither hook nor deny.
	// See WorktreeDelegation for the supported/unsupported branch contract.
	WorktreeDelegation *WorktreeDelegation
}

// recordSources appends the given SourceEntry slice to the context's
// SourceTuples map keyed by path. It is a no-op when the map is nil
// (tests that exercise a materializer in isolation commonly leave it
// nil because they only care about file contents). Splitting the
// append from the call site keeps each materializer free of nil checks.
func (c *MaterializeContext) recordSources(path string, sources []SourceEntry) {
	if c == nil || c.SourceTuples == nil || len(sources) == 0 {
		return
	}
	// Copy so callers can reuse their slice after calling
	// recordSources; the materializer pipeline holds these values
	// until state is written.
	entries := make([]SourceEntry, len(sources))
	copy(entries, sources)
	c.SourceTuples[path] = append(c.SourceTuples[path], entries...)
}

// InstalledHookEntry holds the installed paths for a single hook entry,
// preserving the optional matcher from the source HookEntry.
type InstalledHookEntry struct {
	Matcher string
	Paths   []string
}

// Materializer is the interface for components that install workspace
// configuration artifacts into a repository directory.
type Materializer interface {
	Name() string
	Materialize(ctx *MaterializeContext) ([]string, error)
}

// HooksMaterializer installs hook scripts from the config directory into a
// repository's .claude/hooks/ directory structure. It reads the merged hooks
// from EffectiveConfig and copies each script file to the target location.
type HooksMaterializer struct{}

// Name returns the materializer identifier.
func (h *HooksMaterializer) Name() string {
	return "hooks"
}

// Materialize copies hook scripts into {repoDir}/.claude/hooks/{event}/ and
// sets them executable. It populates ctx.InstalledHooks with the mapping of
// event names to installed script paths and returns the list of all written
// file paths.
func (h *HooksMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	hooks := ctx.Effective.Claude.Hooks
	if len(hooks) == 0 {
		return nil, nil
	}

	installed := make(map[string][]InstalledHookEntry, len(hooks))
	var written []string

	for event, entries := range hooks {
		for _, entry := range entries {
			var installedPaths []string
			for _, scriptPath := range entry.Scripts {
				var src string
				if filepath.IsAbs(scriptPath) {
					// Absolute paths originate from global config hooks; they are
					// pre-validated by ParseGlobalConfigOverride and resolved in
					// MergeGlobalOverride so no containment check is needed.
					src = scriptPath
				} else {
					src = filepath.Join(ctx.ConfigDir, scriptPath)
					if err := checkContainment(src, ctx.ConfigDir); err != nil {
						return nil, fmt.Errorf("hook script %q: %w", scriptPath, err)
					}
				}

				targetDir := filepath.Join(ctx.RepoDir, ".claude", "hooks", event)
				target := filepath.Join(targetDir, localRename(filepath.Base(scriptPath)))

				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					return nil, fmt.Errorf("creating hooks directory %s: %w", targetDir, err)
				}

				data, err := os.ReadFile(src)
				if err != nil {
					return nil, fmt.Errorf("reading hook script %s: %w", src, err)
				}

				if err := os.WriteFile(target, data, 0o644); err != nil {
					return nil, fmt.Errorf("writing hook script %s: %w", target, err)
				}

				if err := os.Chmod(target, 0o755); err != nil {
					return nil, fmt.Errorf("setting executable permission on %s: %w", target, err)
				}

				// Record the hook-script source bytes so fingerprinting
				// treats an upstream script edit as a rotation rather
				// than local drift.
				sum := sha256.Sum256(data)
				ctx.recordSources(target, []SourceEntry{{
					Kind:         SourceKindPlaintext,
					SourceID:     scriptPath,
					VersionToken: "sha256:" + hex.EncodeToString(sum[:]),
				}})

				installedPaths = append(installedPaths, target)
				written = append(written, target)
			}
			installed[event] = append(installed[event], InstalledHookEntry{
				Matcher: entry.Matcher,
				Paths:   installedPaths,
			})
		}
	}

	ctx.InstalledHooks = installed
	return written, nil
}

// permissionsMapping translates niwa permission values to Claude Code
// settings.local.json permission mode strings.
var permissionsMapping = map[string]string{
	"bypass": "bypassPermissions",
	"ask":    "askPermissions",
}

// hookEventMapping translates snake_case hook event names used in niwa config
// to PascalCase event names expected by Claude Code settings.
var hookEventMapping = map[string]string{
	"pre_tool_use":  "PreToolUse",
	"post_tool_use": "PostToolUse",
	"stop":          "Stop",
	"notification":  "Notification",
}

// Worktree-delegation event and tool names used in the per-repo
// settings.local.json. These ride the existing snake->Pascal hook event
// pipeline (the names are already Pascal-cased here, so snakeToPascal is a
// no-op for them) and the new permissions.deny capability.
const (
	// worktreeCreateEvent / worktreeRemoveEvent are the Claude Code hook event
	// names installed when the harness supports per-repo worktree hooks. Both
	// run an absolute-path "niwa worktree from-hook" command directly (no shim).
	worktreeCreateEvent = "WorktreeCreate"
	worktreeRemoveEvent = "WorktreeRemove"

	// worktreeFromHookCommandSuffix is the niwa subcommand the hook invokes. The
	// full command is "<abs-niwa> " + this suffix; abs-niwa is resolved at apply
	// time via os.Executable().
	worktreeFromHookCommandSuffix = "worktree from-hook"

	// denyEnterWorktree / denyExitWorktree are the Claude Code tool names denied
	// when the harness does NOT support the worktree hooks. Denying these steers
	// agents to `niwa worktree create` instead of a competing bare checkout.
	denyEnterWorktree = "EnterWorktree"
	denyExitWorktree  = "ExitWorktree"
)

// WorktreeDelegation carries the apply-time decision for the per-repo worktree
// integration, computed once per apply (not per repo) and threaded into each
// repo's MaterializeContext. Hook and deny are MUTUALLY EXCLUSIVE: when
// Supported is true the materializer writes the WorktreeCreate/WorktreeRemove
// hook entries; when false it writes permissions.deny instead. The zero value
// (nil pointer on the context) installs neither, leaving settings unchanged.
type WorktreeDelegation struct {
	// Supported is the harness probe result (SupportsWorktreeHooks). true =>
	// write hooks; false => write permissions.deny.
	Supported bool
	// NiwaPath is the absolute path of the running niwa binary
	// (os.Executable()). Used to build the hook command
	// "<NiwaPath> worktree from-hook". Required when Supported is true.
	NiwaPath string
}

// worktreeFromHookCommand returns the absolute-path hook command string Claude
// invokes directly, e.g. "/abs/niwa worktree from-hook". The niwa path is
// slash-normalized so the JSON command is stable across platforms.
func worktreeFromHookCommand(niwaPath string) string {
	return filepath.ToSlash(niwaPath) + " " + worktreeFromHookCommandSuffix
}

// snakeToPascal converts a snake_case string to PascalCase as a fallback when
// the event name is not in hookEventMapping.
func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// BuildSettingsConfig holds the inputs for buildSettingsDoc, which produces
// the JSON document written to settings.local.json (repos) or settings.json
// (instance root).
type BuildSettingsConfig struct {
	Settings               config.SettingsConfig
	InstalledHooks         map[string][]InstalledHookEntry
	ResolvedEnvVars        map[string]string
	Plugins                []string
	Marketplaces           []config.MarketplaceConfig
	RepoIndex              map[string]string
	BaseDir                string // for computing relative hook paths
	IncludeGitInstructions *bool
	UseAbsolutePaths       bool // use absolute paths for hooks (instance root)
	// Reports, when non-nil, collects human-readable notices produced while
	// building the document — currently release-tracking fallbacks for github
	// marketplaces with no stable release. The caller surfaces these to the
	// user; leaving it nil discards them.
	Reports *[]string

	// WorktreeDelegation, when non-nil, installs the worktree-delegation
	// integration into this document. Supported => WorktreeCreate/WorktreeRemove
	// hook entries (absolute-path "niwa worktree from-hook" commands).
	// Unsupported => permissions.deny: ["EnterWorktree","ExitWorktree"]. Hook and
	// deny are mutually exclusive; nil installs neither.
	WorktreeDelegation *WorktreeDelegation

	// SessionHooks, when non-nil, installs the workspace-root
	// SessionStart/SessionEnd hook entries (the ephemeral-session integration).
	// Each is a single command piping the Claude hook JSON on stdin to an
	// absolute-path "niwa instance from-hook"; both carry a generous timeout so
	// the clone + vault cost of `niwa create` does not trip the harness timeout.
	// nil installs neither entry, leaving the hooks block untouched.
	SessionHooks *SessionHooks
}

// SessionHooks carries the inputs for the workspace-root
// SessionStart/SessionEnd hook entries. Command is the full command string the
// hook runs (an absolute-path "niwa instance from-hook"); TimeoutSeconds is the
// per-command timeout (Claude Code's hook `timeout` field, in seconds) chosen
// large enough to absorb `niwa create`'s clone + vault cost.
type SessionHooks struct {
	Command        string
	TimeoutSeconds int
}

// Session hook event names written into the workspace-root settings.json. These
// are the Claude Code SessionStart/SessionEnd events the ephemeral-session
// integration rides; the names are already Pascal-cased.
const (
	sessionStartEvent = "SessionStart"
	sessionEndEvent   = "SessionEnd"
)

// buildSettingsDoc produces the map[string]any JSON document for Claude Code
// settings. It handles permissions, hooks, env, enabledPlugins,
// extraKnownMarketplaces, and includeGitInstructions blocks.
func buildSettingsDoc(cfg BuildSettingsConfig) (map[string]any, error) {
	doc := make(map[string]any)

	// Build permissions block from settings. maybeSecretString
	// reveals secret-backed values (rare for permissions, but a
	// user may back any SettingsConfig value via vault://) and
	// returns the literal plaintext otherwise.
	//
	// The permissions block may carry two independent keys: defaultMode (from
	// the user's settings) and deny (from the worktree-delegation fallback). They
	// are emitted into the SAME permissions map so a deny fallback never clobbers
	// a configured defaultMode and vice versa.
	var permissions map[string]any
	if perm, ok := cfg.Settings["permissions"]; ok {
		permStr := maybeSecretString(perm)
		mapped, known := permissionsMapping[permStr]
		if !known {
			return nil, fmt.Errorf("unknown permissions value %q", permStr)
		}
		permissions = map[string]any{
			"defaultMode": mapped,
		}
	}

	// Worktree delegation: emit EITHER the hook entries OR the permissions.deny
	// entries, never both (they are mutually exclusive — a deny blocks the tool
	// before the hook would run). The hook entries are appended to the hooks
	// block built below; the deny entries are merged into the permissions map
	// here so they coexist with any configured defaultMode.
	if wd := cfg.WorktreeDelegation; wd != nil && !wd.Supported {
		if permissions == nil {
			permissions = make(map[string]any)
		}
		permissions["deny"] = []any{denyEnterWorktree, denyExitWorktree}
	}

	if permissions != nil {
		doc["permissions"] = permissions
	}

	// Build hooks block from installed hooks.
	hooksDoc := make(map[string]any, len(cfg.InstalledHooks))
	if len(cfg.InstalledHooks) > 0 {
		// Sort event names for deterministic output.
		events := make([]string, 0, len(cfg.InstalledHooks))
		for event := range cfg.InstalledHooks {
			events = append(events, event)
		}
		sort.Strings(events)

		for _, event := range events {
			installedEntries := cfg.InstalledHooks[event]
			pascalEvent, ok := hookEventMapping[event]
			if !ok {
				pascalEvent = snakeToPascal(event)
			}

			var eventEntries []map[string]any
			for _, ie := range installedEntries {
				hookCommands := make([]map[string]string, 0, len(ie.Paths))
				for _, absPath := range ie.Paths {
					var cmdPath string
					if cfg.UseAbsolutePaths {
						cmdPath = filepath.ToSlash(absPath)
					} else {
						rel, err := filepath.Rel(cfg.BaseDir, absPath)
						if err != nil {
							return nil, fmt.Errorf("computing relative path for hook %s: %w", absPath, err)
						}
						cmdPath = filepath.ToSlash(rel)
					}
					hookCommands = append(hookCommands, map[string]string{
						"type":    "command",
						"command": cmdPath,
					})
				}
				entry := map[string]any{
					"hooks": hookCommands,
				}
				if ie.Matcher != "" {
					entry["matcher"] = ie.Matcher
				}
				eventEntries = append(eventEntries, entry)
			}
			hooksDoc[pascalEvent] = eventEntries
		}
	}

	// Worktree delegation (supported branch): append the WorktreeCreate and
	// WorktreeRemove hook entries, each an absolute-path "niwa worktree from-hook"
	// command Claude invokes directly. These ride the same hooks-block shape as
	// the installed-script hooks above; the event names are already Pascal-cased.
	// The deny branch is handled in the permissions block above and is mutually
	// exclusive with this one.
	if wd := cfg.WorktreeDelegation; wd != nil && wd.Supported {
		command := worktreeFromHookCommand(wd.NiwaPath)
		worktreeEntry := []map[string]any{
			{
				"hooks": []map[string]string{
					{"type": "command", "command": command},
				},
			},
		}
		hooksDoc[worktreeCreateEvent] = worktreeEntry
		hooksDoc[worktreeRemoveEvent] = worktreeEntry
	}

	// Session hooks (ephemeral-session integration): emit the SessionStart and
	// SessionEnd entries, each a single command piping stdin to "niwa instance
	// from-hook" with a generous timeout. The timeout absorbs niwa create's
	// clone + vault cost so the harness does not kill the hook mid-provision.
	if sh := cfg.SessionHooks; sh != nil && sh.Command != "" {
		sessionEntry := []map[string]any{
			{
				"hooks": []map[string]any{
					{
						"type":    "command",
						"command": sh.Command,
						"timeout": sh.TimeoutSeconds,
					},
				},
			},
		}
		hooksDoc[sessionStartEvent] = sessionEntry
		hooksDoc[sessionEndEvent] = sessionEntry
	}

	if len(hooksDoc) > 0 {
		doc["hooks"] = hooksDoc
	}

	// Build env block from resolved env vars.
	if len(cfg.ResolvedEnvVars) > 0 {
		envDoc := make(map[string]any, len(cfg.ResolvedEnvVars))
		envKeys := make([]string, 0, len(cfg.ResolvedEnvVars))
		for k := range cfg.ResolvedEnvVars {
			envKeys = append(envKeys, k)
		}
		sort.Strings(envKeys)
		for _, k := range envKeys {
			envDoc[k] = cfg.ResolvedEnvVars[k]
		}
		doc["env"] = envDoc
	}

	// includeGitInstructions (optional).
	if cfg.IncludeGitInstructions != nil {
		doc["includeGitInstructions"] = *cfg.IncludeGitInstructions
	}

	// enabledPlugins from plugins list.
	if len(cfg.Plugins) > 0 {
		pluginsDoc := make(map[string]any, len(cfg.Plugins))
		for _, plugin := range cfg.Plugins {
			pluginsDoc[plugin] = true
		}
		doc["enabledPlugins"] = pluginsDoc
	}

	// extraKnownMarketplaces from marketplaces list.
	if len(cfg.Marketplaces) > 0 {
		mkts := make(map[string]any, len(cfg.Marketplaces))
		for _, mc := range cfg.Marketplaces {
			source := mc.Source
			name, entry, report, err := mapMarketplaceSourceWithIndex(source, cfg.RepoIndex, mc.AutoUpdate, mc.Track)
			if err != nil {
				return nil, fmt.Errorf("marketplace %q: %w", source, err)
			}
			if report != "" && cfg.Reports != nil {
				*cfg.Reports = append(*cfg.Reports, report)
			}
			if name != "" {
				mkts[name] = entry
			}
		}
		if len(mkts) > 0 {
			doc["extraKnownMarketplaces"] = mkts
		}
	}

	return doc, nil
}

// emitReports writes each report line to w (falling back to os.Stderr when w
// is nil). Used to surface marketplace release-tracking notices to the user.
func emitReports(w io.Writer, reports []string) {
	if len(reports) == 0 {
		return
	}
	if w == nil {
		w = os.Stderr
	}
	for _, r := range reports {
		fmt.Fprintln(w, "niwa: "+r)
	}
}

// resolveClaudeEnvVars resolves the claude.env promote + inline vars into a
// single map. Returns nil if there are no env vars to resolve. The
// second return is the ordered list of SourceEntry tuples describing
// the inputs that contributed bytes: forwarded from ResolveEnvVars
// when any keys are promoted, plus inline vars.
func resolveClaudeEnvVars(ctx *MaterializeContext) (map[string]string, []SourceEntry, error) {
	claudeEnv := ctx.Effective.Claude.Env
	hasEnv := len(claudeEnv.Promote) > 0 || len(claudeEnv.Vars.Values) > 0
	if !hasEnv {
		return nil, nil, nil
	}

	envResult := make(map[string]string)
	var sources []SourceEntry

	// Step 1: resolve promoted keys from the env pipeline.
	if len(claudeEnv.Promote) > 0 {
		resolvedEnv, envSources, err := ResolveEnvVars(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving env for promote: %w", err)
		}
		if resolvedEnv == nil {
			resolvedEnv = map[string]string{}
		}
		for _, key := range claudeEnv.Promote {
			val, found := resolvedEnv[key]
			if !found {
				return nil, nil, fmt.Errorf("claude.env: promoted key %q not found in resolved env vars", key)
			}
			envResult[key] = val
		}
		// Every env source contributes to the rollup because any
		// plaintext-file rotation upstream can change promoted
		// values. Pruning to only the promoted keys would require
		// re-parsing each source file to know which keys it supplied;
		// keeping the full list is simpler and correct — the
		// fingerprint rolls up inputs, not per-key derivations.
		sources = append(sources, envSources...)
	}

	// Step 2: overlay inline vars (inline wins over promoted). Post-
	// resolver (Issue 4) a MaybeSecret may carry a resolved Secret;
	// String redacts those to "***", which is wrong for a file
	// materializer. maybeSecretString reaches through reveal.
	// UnsafeReveal for secret-bearing entries and returns m.Plain
	// otherwise.
	for _, k := range sortedKeys(claudeEnv.Vars.Values) {
		v := claudeEnv.Vars.Values[k]
		envResult[k] = maybeSecretString(v)
		sources = append(sources, sourceForMaybeSecret("workspace.toml:claude.env.vars."+k, v))
	}

	if len(envResult) == 0 {
		return nil, nil, nil
	}
	return envResult, sources, nil
}

// SettingsMaterializer generates the .claude/settings.local.json file from
// effective settings and installed hooks.
type SettingsMaterializer struct{}

// Name returns the materializer identifier.
func (s *SettingsMaterializer) Name() string {
	return "settings"
}

// Materialize builds and writes {repoDir}/.claude/settings.local.json from
// the effective settings and installed hooks. It returns the list of written
// file paths, or nil if there is nothing to write.
func (s *SettingsMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	settings := ctx.Effective.Claude.Settings
	hooks := ctx.InstalledHooks
	plugins := ctx.Effective.Plugins
	marketplaces := ctx.Effective.Claude.Marketplaces

	// Resolve env vars.
	resolvedEnv, envSources, err := resolveClaudeEnvVars(ctx)
	if err != nil {
		return nil, err
	}

	// The worktree-delegation decision (computed once per apply, threaded via the
	// context) is itself enough to require a settings file even when nothing else
	// is configured: it writes either the WorktreeCreate/WorktreeRemove hooks or
	// the permissions.deny fallback.
	if len(settings) == 0 && len(hooks) == 0 && len(resolvedEnv) == 0 &&
		len(plugins) == 0 && len(marketplaces) == 0 && ctx.WorktreeDelegation == nil {
		return nil, nil
	}

	var reports []string
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings:           settings,
		InstalledHooks:     hooks,
		ResolvedEnvVars:    resolvedEnv,
		Plugins:            plugins,
		Marketplaces:       marketplaces,
		RepoIndex:          ctx.RepoIndex,
		BaseDir:            ctx.RepoDir,
		UseAbsolutePaths:   true,
		Reports:            &reports,
		WorktreeDelegation: ctx.WorktreeDelegation,
	})
	if err != nil {
		return nil, err
	}
	emitReports(ctx.Stderr, reports)

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling settings: %w", err)
	}
	// Append trailing newline for clean file output.
	data = append(data, '\n')

	claudeDir := filepath.Join(ctx.RepoDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating .claude directory: %w", err)
	}

	target := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(target, data, secretFileMode); err != nil {
		return nil, fmt.Errorf("writing settings file: %w", err)
	}

	// Merge settings-key sources with env sources so rotating either
	// input shifts the file's SourceFingerprint. Settings keys are
	// rare vault consumers but R3 allows it, so the fingerprint
	// tracks them consistently.
	sources := envSources
	for _, k := range sortedKeysSettings(settings) {
		v := settings[k]
		sources = append(sources, sourceForMaybeSecret("workspace.toml:claude.settings."+k, v))
	}
	ctx.recordSources(target, sources)

	return []string{target}, nil
}

// sortedKeysSettings is the SettingsConfig counterpart to sortedKeys,
// kept separate because Go generics would force a wider refactor for
// negligible reuse.
func sortedKeysSettings(m config.SettingsConfig) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EnvMaterializer generates a .local.env file in the repository directory from
// explicit env config, discovered env files, and inline variables.
//
// Stderr, if non-nil, receives diagnostic warnings emitted during the
// .env.example pre-pass (absent file is silent; symlinks, parse errors, and
// undeclared-but-safe keys emit warnings). When nil, warnings go to os.Stderr.
// Tests set this to a bytes.Buffer to capture output.
type EnvMaterializer struct {
	Stderr io.Writer
}

// Name returns the materializer identifier.
func (e *EnvMaterializer) Name() string {
	return "env"
}

// stderr returns the writer to use for diagnostic output, defaulting to
// os.Stderr when no writer has been wired into the struct.
func (e *EnvMaterializer) stderr() io.Writer {
	if e.Stderr != nil {
		return e.Stderr
	}
	return os.Stderr
}

// ResolveEnvVars merges env files, inline vars, and discovered files into a
// single key-value map. This is the canonical env resolution function used by
// both the EnvMaterializer (to write .local.env) and the SettingsMaterializer
// (to look up promoted keys).
//
// Alongside the resolved map, ResolveEnvVars returns the ordered list
// of SourceEntry tuples describing each input that contributed bytes.
// Callers that write a file from this map pass the slice to
// MaterializeContext.recordSources so the apply pipeline can build a
// SourceFingerprint for the written file. Plaintext-file inputs
// contribute a SHA-256 content-hash VersionToken; inline TOML entries
// contribute the redactor-fed plaintext bytes of the resolved
// MaybeSecret; vault-resolved MaybeSecrets contribute the provider-
// opaque VersionToken.Token carried on the MaybeSecret.Token field.
func ResolveEnvVars(ctx *MaterializeContext) (map[string]string, []SourceEntry, error) {
	envCfg := ctx.Effective.Env
	discovered := ctx.DiscoveredEnv

	files := envCfg.Files
	if len(files) == 0 && discovered != nil && discovered.WorkspaceFile != "" {
		files = []string{discovered.WorkspaceFile}
	}

	hasVars := len(envCfg.Vars.Values) > 0 || len(envCfg.Secrets.Values) > 0
	hasRepoFile := discovered != nil && discovered.RepoFiles != nil && discovered.RepoFiles[ctx.RepoName] != ""

	if len(files) == 0 && !hasVars && !hasRepoFile && len(ctx.EnvExampleVars) == 0 {
		return nil, nil, nil
	}

	vars := make(map[string]string)
	var sources []SourceEntry

	// Seed from .env.example pre-pass (lowest-priority layer).
	maps.Copy(vars, ctx.EnvExampleVars)
	sources = append(sources, ctx.EnvExampleSources...)

	for _, f := range files {
		src := filepath.Join(ctx.ConfigDir, f)
		if err := checkContainment(src, ctx.ConfigDir); err != nil {
			return nil, nil, fmt.Errorf("env file %q: %w", f, err)
		}

		parsed, err := parseEnvFile(src)
		if err != nil {
			return nil, nil, fmt.Errorf("reading env file %s: %w", f, err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
		hash, err := HashFile(src)
		if err != nil {
			return nil, nil, fmt.Errorf("hashing env file %s: %w", f, err)
		}
		sources = append(sources, SourceEntry{
			Kind:         SourceKindPlaintext,
			SourceID:     f,
			VersionToken: hash,
		})
	}

	// Post-resolver, MaybeSecret.Secret holds the resolved plaintext
	// wrapped in secret.Value. Its String method redacts to "***" by
	// design; for the env materializer we need the actual bytes to
	// land in the .local.env file, so reach through reveal.
	// UnsafeReveal. Plain values pass through unchanged.
	//
	// Every vars/secrets entry is recorded as a SourceEntry so the
	// fingerprint rolls up the post-resolve state. Vault-resolved
	// MaybeSecrets carry a Token field copied from
	// vault.VersionToken; plaintext entries synthesize a content-hash
	// VersionToken from the materialized string so the rollup can
	// tell inline-plaintext rotations from file-source rotations.
	for _, k := range sortedKeys(envCfg.Vars.Values) {
		v := envCfg.Vars.Values[k]
		vars[k] = maybeSecretString(v)
		sources = append(sources, sourceForMaybeSecret("workspace.toml:env.vars."+k, v))
	}
	for _, k := range sortedKeys(envCfg.Secrets.Values) {
		v := envCfg.Secrets.Values[k]
		vars[k] = maybeSecretString(v)
		sources = append(sources, sourceForMaybeSecret("workspace.toml:env.secrets."+k, v))
	}

	if hasRepoFile {
		repoEnvPath := discovered.RepoFiles[ctx.RepoName]
		parsed, err := parseEnvFile(repoEnvPath)
		if err != nil {
			return nil, nil, fmt.Errorf("reading discovered repo env file %s: %w", repoEnvPath, err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
		hash, err := HashFile(repoEnvPath)
		if err != nil {
			return nil, nil, fmt.Errorf("hashing discovered repo env file %s: %w", repoEnvPath, err)
		}
		sources = append(sources, SourceEntry{
			Kind:         SourceKindPlaintext,
			SourceID:     repoEnvPath,
			VersionToken: hash,
		})
	}

	if len(vars) == 0 {
		return nil, nil, nil
	}

	return vars, sources, nil
}

// sortedKeys returns the keys of a MaybeSecret map in lexical order.
// Used to make sources/provenance lists deterministic regardless of
// Go's map-iteration randomization.
func sortedKeys(m map[string]config.MaybeSecret) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sourceForMaybeSecret builds a SourceEntry for a MaybeSecret value.
//
// For vault-resolved MaybeSecrets (ms.IsSecret() && ms.Token.Token != ""),
// the SourceEntry uses the vault kind with the provider-opaque
// VersionToken.Token and Provenance. The SourceID is the value's
// secret.Origin ProviderName + "/" + Key (consistent with Issue 5's
// Infisical backend), falling back to the supplied location-based
// fallback when Origin is empty.
//
// For plaintext (or auto-wrapped plaintext in secrets tables), the
// VersionToken is a SHA-256 hex of the resolved plaintext bytes. That
// bytestream is already present in memory via maybeSecretString in
// the caller, but computing the hash here keeps the plaintext local
// to this function — no callers of SourceEntry ever see the bytes.
func sourceForMaybeSecret(fallbackLocation string, ms config.MaybeSecret) SourceEntry {
	if ms.IsSecret() && ms.Token.Token != "" {
		origin := ms.Secret.Origin()
		sourceID := fmt.Sprintf("%s/%s", origin.ProviderName, origin.Key)
		// An unset Origin yields "/", which is useless for the user.
		// Fall back to the caller-supplied location in that case.
		if origin.ProviderName == "" && origin.Key == "" {
			sourceID = fallbackLocation
		}
		return SourceEntry{
			Kind:         SourceKindVault,
			SourceID:     sourceID,
			VersionToken: ms.Token.Token,
			Provenance:   ms.Token.Provenance,
		}
	}
	// Plaintext (literal or auto-wrapped in a *.secrets table). The
	// VersionToken hashes the revealed bytes so two configs with the
	// same plaintext produce the same rollup; this lets status
	// detect plaintext rotation (the hash changes when a user edits
	// the TOML value) without persisting the plaintext itself.
	plain := maybeSecretString(ms)
	sum := sha256.Sum256([]byte(plain))
	return SourceEntry{
		Kind:         SourceKindPlaintext,
		SourceID:     fallbackLocation,
		VersionToken: hex.EncodeToString(sum[:]),
	}
}

// Materialize reads env files and inline vars, merges them with discovered env
// files, and writes the result to each resolved secret-output target for the
// repo (defaulting to {repoDir}/.local.env in dotenv form). Returns the list of
// written file paths, or nil if there is nothing to write.
//
// Ordering is security-load-bearing: for every custom target name (one not
// matched by the managed "*.local*" base pattern) git-ignore coverage is
// recorded BEFORE any secret bytes are written, and a custom target on a
// non-git tree -- where coverage cannot be confirmed -- is a fail-closed
// refusal rather than a silent unprotected write.
func (e *EnvMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	if err := e.runEnvExamplePrePass(ctx); err != nil {
		return nil, err
	}

	vars, sources, err := ResolveEnvVars(ctx)
	if err != nil {
		return nil, err
	}
	if len(vars) == 0 {
		return nil, nil
	}

	// Build an ordered key-value slice (sorted keys) shared by every writer so
	// output is deterministic and the default dotenv target stays byte-identical
	// to niwa's historical .local.env.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kvs := make([]envformat.KV, 0, len(keys))
	for _, k := range keys {
		kvs = append(kvs, envformat.KV{Key: k, Value: vars[k]})
	}

	targets := config.EffectiveEnvOutput(ctx.GlobalEnvOutput, ctx.Config, ctx.RepoName)

	// Validate every target path and collect the custom (non-"*.local*") names
	// up front, then establish coverage for the whole set before any write so
	// no secret file lands ahead of its exclude line.
	absTargets := make([]string, len(targets))
	var customPatterns []string
	for i, tgt := range targets {
		abs, err := safeTargetPath(ctx.RepoDir, tgt.Path)
		if err != nil {
			return nil, fmt.Errorf("env output target %q for repo %s: %w", tgt.Path, ctx.RepoName, err)
		}
		absTargets[i] = abs
		if !matchedByBasePattern(tgt.Path) {
			customPatterns = append(customPatterns, tgt.Path)
		}
	}
	if len(customPatterns) > 0 {
		if !gitexclude.IsGitRepo(ctx.RepoDir) {
			return nil, fmt.Errorf("repo %s: custom secret-output target requires a git repository to guarantee git invisibility, but %s is not a git repository", ctx.RepoName, ctx.RepoDir)
		}
		if err := gitexclude.EnsureRepoExclude(ctx.RepoDir, customPatterns...); err != nil {
			return nil, fmt.Errorf("repo %s: recording git exclude coverage for custom secret-output targets: %w", ctx.RepoName, err)
		}
	}

	var written []string
	for i, tgt := range targets {
		abs := absTargets[i]
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			return nil, fmt.Errorf("creating parent dir for env output %q: %w", tgt.Path, err)
		}
		data, err := envformat.Marshal(string(tgt.Format), kvs)
		if err != nil {
			return nil, fmt.Errorf("serializing env output %q for repo %s: %w", tgt.Path, ctx.RepoName, err)
		}
		if err := os.WriteFile(abs, data, secretFileMode); err != nil {
			return nil, fmt.Errorf("writing env output %q: %w", tgt.Path, err)
		}
		ctx.recordSources(abs, sources)
		// Only custom names (not already matched by the base "*.local*"
		// pattern) need an extra exclude entry. Recording the default
		// .local.env here would add a redundant exclude line and change the
		// managed block for repos that configured nothing -- so a repo with no
		// env_output keeps the exact pre-feature exclude coverage.
		if !matchedByBasePattern(tgt.Path) {
			ctx.EnvOutputs = append(ctx.EnvOutputs, tgt.Path)
		}
		written = append(written, abs)
	}

	return written, nil
}

// matchedByBasePattern reports whether a target path is already covered by the
// managed "*.local*" base exclude pattern (its base name contains ".local").
// Such targets need no extra ignore entry; everything else is a "custom" name.
func matchedByBasePattern(p string) bool {
	return strings.Contains(filepath.Base(p), ".local")
}

// safeTargetPath validates an operator-supplied, repo-relative target path and
// returns its absolute location under repoDir. A workspace config is untrusted
// input, so the guard rejects absolute paths, paths that escape the repo after
// cleaning, and paths that escape via a symlinked parent directory (resolved
// with EvalSymlinks). It fails closed -- callers must not write when it errors.
func safeTargetPath(repoDir, target string) (string, error) {
	if target == "" {
		return "", fmt.Errorf("target path is empty")
	}
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("target path must be relative, got absolute path")
	}
	clean := filepath.Clean(target)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target path escapes the repository")
	}
	joined := filepath.Join(repoDir, clean)

	rootResolved, err := filepath.EvalSymlinks(repoDir)
	if err != nil {
		return "", fmt.Errorf("resolving repo dir: %w", err)
	}
	// Resolve symlinks on the deepest existing ancestor of the target (the
	// target file itself may not exist yet) and assert it stays within the repo.
	ancestor := joined
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			break
		}
		ancestor = parent
	}
	ancestorResolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", fmt.Errorf("resolving target ancestor: %w", err)
	}
	rel, err := filepath.Rel(rootResolved, ancestorResolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("target path escapes the repository via a symlink")
	}
	return joined, nil
}

// parseEnvFile reads a file and parses KEY=VALUE lines. Lines starting with #
// and blank lines are skipped.
func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[key] = value
	}
	return vars, nil
}

// localRename inserts ".local" before the file extension. Files without an
// extension get ".local" appended. This ensures distributed files match the
// *.local* gitignore pattern in target repos.
//
//	"design.md"    -> "design.local.md"
//	"config.json"  -> "config.local.json"
//	"Makefile"     -> "Makefile.local"
//	".eslintrc"    -> ".eslintrc.local"
func localRename(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" || ext == filename {
		// No extension or dotfile where the whole name is the "extension" (e.g., ".eslintrc").
		return filename + ".local"
	}
	base := strings.TrimSuffix(filename, ext)
	if base == "" {
		// Dotfile like ".eslintrc" where Ext returns the whole name.
		return filename + ".local"
	}
	return base + ".local" + ext
}

// FilesMaterializer copies arbitrary files from the config directory into a
// repository directory. It reads source-to-destination mappings from the
// effective config and applies .local renaming when the destination is a
// directory (ends with /).
//
// Stderr, if non-nil, receives one-line notices when the materializer
// rewrites a user-written destination to include the ".local" infix.
// When nil, notices go to os.Stderr. Tests set this to a bytes.Buffer
// to capture output.
type FilesMaterializer struct {
	Stderr io.Writer

	// noticed tracks destination paths that already emitted a .local
	// injection notice for the current Materialize call, so a repeated
	// [files] entry for the same destination does not re-notify.
	noticed map[string]bool
}

// Name returns the materializer identifier.
func (f *FilesMaterializer) Name() string {
	return "files"
}

// stderr returns the writer to use for diagnostic notices, defaulting
// to os.Stderr when no writer has been wired into the struct.
func (f *FilesMaterializer) stderr() io.Writer {
	if f.Stderr != nil {
		return f.Stderr
	}
	return os.Stderr
}

// noteLocalInfix emits a one-line notice that the user-written
// destination path was rewritten to include ".local". It is a no-op
// when original == rewritten (the path already contained the infix)
// or when the same rewritten destination has already been noticed in
// this Materialize call.
func (f *FilesMaterializer) noteLocalInfix(original, rewritten string) {
	if original == rewritten {
		return
	}
	if f.noticed[rewritten] {
		return
	}
	if f.noticed == nil {
		f.noticed = map[string]bool{}
	}
	f.noticed[rewritten] = true
	fmt.Fprintf(f.stderr(),
		"note: files destination %q rewritten to %q for .local infix (secret-bearing files must match *.local* gitignore pattern)\n",
		original, rewritten)
}

// Materialize copies files from configDir to repoDir based on effective file
// mappings. Returns the list of written file paths.
func (f *FilesMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	files := ctx.Effective.Files
	if len(files) == 0 {
		return nil, nil
	}

	// Reset the per-call notice dedup set so a fresh Materialize
	// run starts from a clean slate; the same struct is reused across
	// repos in Applier.runPipeline.
	f.noticed = map[string]bool{}

	var written []string

	// Sort source keys for deterministic output.
	sources := make([]string, 0, len(files))
	for src := range files {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	for _, src := range sources {
		dest := files[src]
		if dest == "" {
			continue // removed by per-repo override
		}

		isDir := strings.HasSuffix(src, "/")

		if isDir {
			w, err := f.materializeDir(ctx, src, dest)
			if err != nil {
				return nil, err
			}
			written = append(written, w...)
		} else {
			w, err := f.materializeFile(ctx, src, dest)
			if err != nil {
				return nil, err
			}
			written = append(written, w...)
		}
	}

	return written, nil
}

// writeManagedFile is the shared copy core for distributed files: it validates
// that targetPath stays within ctx.RepoDir, creates parent directories, writes
// data with the restrictive secretFileMode, and records (sourceID, content
// hash) for drift fingerprinting. The per-level rename strategy (the per-repo
// .local infix vs. verbatim at the non-repo levels) is applied by the caller
// before it computes targetPath; this helper is rename-agnostic so the repo and
// non-repo paths share one implementation of the actual file I/O and tracking.
func (ctx *MaterializeContext) writeManagedFile(targetPath, sourceID string, data []byte) error {
	if err := checkContainment(targetPath, ctx.RepoDir); err != nil {
		return fmt.Errorf("file destination %q: %w", targetPath, err)
	}

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", targetDir, err)
	}

	if err := os.WriteFile(targetPath, data, secretFileMode); err != nil {
		return fmt.Errorf("writing file %s: %w", targetPath, err)
	}

	// Record the source (path + content-hash) for fingerprinting.
	// Hashing the already-loaded bytes avoids a second file read.
	sum := sha256.Sum256(data)
	ctx.recordSources(targetPath, []SourceEntry{{
		Kind:         SourceKindPlaintext,
		SourceID:     sourceID,
		VersionToken: "sha256:" + hex.EncodeToString(sum[:]),
	}})
	return nil
}

// materializeFile copies a single file from the config directory to the repo,
// applying the per-repo .local rename strategy.
func (f *FilesMaterializer) materializeFile(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcPath := filepath.Join(ctx.ConfigDir, src)
	if err := checkContainment(srcPath, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("file source %q: %w", src, err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", src, err)
	}

	var targetPath string
	if strings.HasSuffix(dest, "/") {
		// Directory destination: auto-rename with .local.
		targetDir := filepath.Join(ctx.RepoDir, dest)
		targetPath = filepath.Join(targetDir, localRename(filepath.Base(src)))
	} else {
		// Explicit filename: inject .local before the extension if
		// the user-written path doesn't already contain it. An
		// injected .local takes precedence over the user's literal
		// basename so every materialized file matches the *.local*
		// gitignore pattern maintained by `niwa create`.
		destDir, destBase := filepath.Split(dest)
		rewritten := injectLocalInfix(destBase)
		if rewritten != destBase {
			f.noteLocalInfix(dest, filepath.Join(destDir, rewritten))
		}
		targetPath = filepath.Join(ctx.RepoDir, destDir, rewritten)
	}

	if err := ctx.writeManagedFile(targetPath, src, data); err != nil {
		return nil, err
	}
	return []string{targetPath}, nil
}

// materializeVerbatimFiles copies the given source-to-destination mappings into
// ctx.RepoDir VERBATIM: the destination name the author wrote is used as-is, with
// no .local infix inserted. It is the non-repo (workspace-root and instance-root)
// counterpart to FilesMaterializer's per-repo .local copy, sharing the same
// containment, write, and source-recording core (writeManagedFile). Source and
// destination containment are enforced exactly as the repo path enforces them.
// Empty-string destinations are skipped (removal semantics). Returns the written
// paths so the caller can track them as managed files.
func materializeVerbatimFiles(ctx *MaterializeContext, files map[string]string) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	// Sort source keys for deterministic output.
	sources := make([]string, 0, len(files))
	for src := range files {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	var written []string
	for _, src := range sources {
		dest := files[src]
		if dest == "" {
			continue // removed via empty-string override
		}
		var (
			w   []string
			err error
		)
		if strings.HasSuffix(src, "/") {
			w, err = materializeVerbatimDir(ctx, src, dest)
		} else {
			w, err = materializeVerbatimFile(ctx, src, dest)
		}
		if err != nil {
			return nil, err
		}
		written = append(written, w...)
	}
	return written, nil
}

// materializeVerbatimFile copies a single file verbatim (no .local rename).
func materializeVerbatimFile(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcPath := filepath.Join(ctx.ConfigDir, src)
	if err := checkContainment(srcPath, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("file source %q: %w", src, err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", src, err)
	}

	// Verbatim: a directory destination (trailing /) places the file under its
	// own basename unchanged; an explicit destination is used as written.
	var targetPath string
	if strings.HasSuffix(dest, "/") {
		targetPath = filepath.Join(ctx.RepoDir, dest, filepath.Base(src))
	} else {
		targetPath = filepath.Join(ctx.RepoDir, dest)
	}

	if err := ctx.writeManagedFile(targetPath, src, data); err != nil {
		return nil, err
	}
	return []string{targetPath}, nil
}

// materializeVerbatimDir walks a source directory and copies each file verbatim
// (no .local rename), preserving directory structure under dest.
func materializeVerbatimDir(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcDir := filepath.Join(ctx.ConfigDir, strings.TrimSuffix(src, "/"))
	if err := checkContainment(srcDir, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("directory source %q: %w", src, err)
	}

	var written []string
	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		targetPath := filepath.Join(ctx.RepoDir, strings.TrimSuffix(dest, "/"), rel)
		sourceID := filepath.Join(strings.TrimSuffix(src, "/"), rel)
		if err := ctx.writeManagedFile(targetPath, sourceID, data); err != nil {
			return err
		}
		written = append(written, targetPath)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walking directory %s: %w", src, walkErr)
	}
	return written, nil
}

// materializeDir walks a source directory and copies each file to the
// destination, preserving directory structure and applying .local renaming.
func (f *FilesMaterializer) materializeDir(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcDir := filepath.Join(ctx.ConfigDir, strings.TrimSuffix(src, "/"))
	if err := checkContainment(srcDir, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("directory source %q: %w", src, err)
	}

	var written []string

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		// Apply .local renaming to every file so the output matches
		// the *.local* gitignore pattern regardless of how the user
		// wrote the destination. injectLocalInfix is a no-op when
		// the basename already contains ".local".
		dir := filepath.Dir(rel)
		baseName := filepath.Base(rel)
		renamed := injectLocalInfix(baseName)
		if renamed != baseName {
			var originalRel, rewrittenRel string
			if dir == "." {
				originalRel = filepath.Join(dest, baseName)
				rewrittenRel = filepath.Join(dest, renamed)
			} else {
				originalRel = filepath.Join(dest, dir, baseName)
				rewrittenRel = filepath.Join(dest, dir, renamed)
			}
			f.noteLocalInfix(originalRel, rewrittenRel)
		}
		var targetPath string
		if strings.HasSuffix(dest, "/") {
			if dir == "." {
				targetPath = filepath.Join(ctx.RepoDir, dest, renamed)
			} else {
				targetPath = filepath.Join(ctx.RepoDir, dest, dir, renamed)
			}
		} else {
			if dir == "." {
				targetPath = filepath.Join(ctx.RepoDir, dest, renamed)
			} else {
				targetPath = filepath.Join(ctx.RepoDir, dest, dir, renamed)
			}
		}

		// Record the source relative to srcDir so the SourceID is stable
		// across platforms and independent of ctx.ConfigDir.
		relFromConfig := filepath.Join(strings.TrimSuffix(src, "/"), rel)
		if err := ctx.writeManagedFile(targetPath, relFromConfig, data); err != nil {
			return err
		}

		written = append(written, targetPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory %s: %w", src, err)
	}

	return written, nil
}
