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

	// AllowPlaintextSecrets, when true, bypasses the public-remote guardrail for
	// .env.example probable-secret keys. Populated from Applier.AllowPlaintextSecrets
	// via apply.go. Does not bypass the basic probable-secret classification.
	AllowPlaintextSecrets bool

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
}

// stderr returns the writer to use for diagnostic output, defaulting to
// os.Stderr when no writer has been wired into the context.
func (c *MaterializeContext) stderr() io.Writer {
	if c != nil && c.Stderr != nil {
		return c.Stderr
	}
	return os.Stderr
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
	Marketplaces           []string
	RepoIndex              map[string]string
	BaseDir                string // for computing relative hook paths
	IncludeGitInstructions *bool
	UseAbsolutePaths       bool // use absolute paths for hooks (instance root)
}

// buildSettingsDoc produces the map[string]any JSON document for Claude Code
// settings. It handles permissions, hooks, env, enabledPlugins,
// extraKnownMarketplaces, and includeGitInstructions blocks.
func buildSettingsDoc(cfg BuildSettingsConfig) (map[string]any, error) {
	doc := make(map[string]any)

	// Build permissions block from settings. maybeSecretString
	// reveals secret-backed values (rare for permissions, but a
	// user may back any SettingsConfig value via vault://) and
	// returns the literal plaintext otherwise.
	if perm, ok := cfg.Settings["permissions"]; ok {
		permStr := maybeSecretString(perm)
		mapped, known := permissionsMapping[permStr]
		if !known {
			return nil, fmt.Errorf("unknown permissions value %q", permStr)
		}
		doc["permissions"] = map[string]any{
			"defaultMode": mapped,
		}
	}

	// Build hooks block from installed hooks.
	if len(cfg.InstalledHooks) > 0 {
		hooksDoc := make(map[string]any, len(cfg.InstalledHooks))

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
		for _, source := range cfg.Marketplaces {
			name, entry, err := mapMarketplaceSourceWithIndex(source, cfg.RepoIndex)
			if err != nil {
				return nil, fmt.Errorf("marketplace %q: %w", source, err)
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

	if len(settings) == 0 && len(hooks) == 0 && len(resolvedEnv) == 0 &&
		len(plugins) == 0 && len(marketplaces) == 0 {
		return nil, nil
	}

	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings:        settings,
		InstalledHooks:  hooks,
		ResolvedEnvVars: resolvedEnv,
		Plugins:         plugins,
		Marketplaces:    marketplaces,
		RepoIndex:       ctx.RepoIndex,
		BaseDir:         ctx.RepoDir,
	})
	if err != nil {
		return nil, err
	}

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

	// Check that the repo's .gitignore has a *.local* pattern. Surface any
	// warnings but do not fail — settings.local.json has already been written.
	gitignoreWarnings := CheckGitignore(ctx.RepoDir, ctx.RepoName)
	for _, w := range gitignoreWarnings {
		fmt.Fprintf(ctx.stderr(), "warning: %s\n", w)
	}

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
// files, and writes the result to {repoDir}/.local.env. Returns the list of
// written file paths, or nil if there is nothing to write.
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

	// Build output with sorted keys for deterministic output.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteString("# Generated by niwa - do not edit manually\n")
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(vars[k])
		buf.WriteByte('\n')
	}

	target := filepath.Join(ctx.RepoDir, ".local.env")
	if err := os.WriteFile(target, []byte(buf.String()), secretFileMode); err != nil {
		return nil, fmt.Errorf("writing env file: %w", err)
	}

	ctx.recordSources(target, sources)

	return []string{target}, nil
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

// materializeFile copies a single file from the config directory to the repo.
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

	if err := checkContainment(targetPath, ctx.RepoDir); err != nil {
		return nil, fmt.Errorf("file destination %q: %w", dest, err)
	}

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating directory %s: %w", targetDir, err)
	}

	if err := os.WriteFile(targetPath, data, secretFileMode); err != nil {
		return nil, fmt.Errorf("writing file %s: %w", targetPath, err)
	}

	// Record the source (path + content-hash) for fingerprinting.
	// Hashing the already-loaded bytes avoids a second file read.
	sum := sha256.Sum256(data)
	ctx.recordSources(targetPath, []SourceEntry{{
		Kind:         SourceKindPlaintext,
		SourceID:     src,
		VersionToken: "sha256:" + hex.EncodeToString(sum[:]),
	}})

	return []string{targetPath}, nil
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

		if err := checkContainment(targetPath, ctx.RepoDir); err != nil {
			return fmt.Errorf("file destination %q: %w", targetPath, err)
		}

		targetDir := filepath.Dir(targetPath)
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", targetDir, err)
		}

		if err := os.WriteFile(targetPath, data, secretFileMode); err != nil {
			return fmt.Errorf("writing %s: %w", targetPath, err)
		}

		// Record (source-rel-path, content-hash) so fingerprinting
		// treats each file in the walked tree as its own source.
		// Using the path relative to srcDir keeps the SourceID stable
		// across platforms and independent of ctx.ConfigDir.
		sum := sha256.Sum256(data)
		relFromConfig := filepath.Join(strings.TrimSuffix(src, "/"), rel)
		ctx.recordSources(targetPath, []SourceEntry{{
			Kind:         SourceKindPlaintext,
			SourceID:     relFromConfig,
			VersionToken: "sha256:" + hex.EncodeToString(sum[:]),
		}})

		written = append(written, targetPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory %s: %w", src, err)
	}

	return written, nil
}
