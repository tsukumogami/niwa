// Package resolve drives the vault resolution stage of niwa apply.
// It sits between parse and merge in the apply pipeline and turns
// every MaybeSecret{Plain: "vault://..."} slot in a workspace config
// (or global override) into a MaybeSecret{Secret: v, Token: t} by
// calling the configured provider.
//
// The package exists as a sub-package of internal/vault rather than
// as a file inside internal/vault itself because:
//
//   - internal/config imports internal/vault for vault.VersionToken
//     (which MaybeSecret.Token carries).
//   - The resolver needs to walk *config.WorkspaceConfig structs, so
//     it must import internal/config.
//
// Placing resolver code in internal/vault would create an import
// cycle (vault -> config -> vault). A sub-package is the idiomatic
// Go fix: resolve imports both vault and config, while vault stays
// free of any config dependency.
//
// Design reference: docs/designs/DESIGN-vault-integration.md
// Decision 2 (resolve-before-merge), Issue 4 in PLAN-vault-integration.md.
package resolve

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// ResolveOptions tune resolver behavior for a single niwa apply.
type ResolveOptions struct {
	// Registry, when non-nil, overrides vault.DefaultRegistry for
	// opening providers. Tests use this to register the fake backend
	// without mutating the process-wide default registry.
	//
	// Production callers leave this nil; the resolver uses
	// vault.DefaultRegistry and the backends that registered into it
	// via package init().
	Registry *vault.Registry

	// TeamBundle is an already-opened bundle for the workspace's team
	// providers. When nil, the resolver builds one from cfg.Vault.
	// Callers wiring ResolveWorkspace and ResolveGlobalOverride from
	// the same apply pipeline may construct the bundles externally to
	// run R12 collision and shadow-detection checks before resolution.
	//
	// The caller owns bundle lifetime: the resolver does not call
	// CloseAll on supplied bundles.
	TeamBundle *vault.Bundle

	// PersonalBundle is the equivalent for ResolveGlobalOverride.
	PersonalBundle *vault.Bundle

	// SourceFile is the path attribution used when the resolver
	// constructs a bundle internally. Non-load-bearing; appears only
	// in error messages for user orientation.
	SourceFile string

	// Stderr is the resolver's diagnostic sink. The resolver currently
	// writes nothing to it: a shortfall is recorded as a mark on the
	// value rather than announced here, and reporting happens once,
	// post-merge, where the whole picture is available. The field
	// stays because that silence is a contract several tests assert
	// against, not an accident.
	Stderr interface{ Write(p []byte) (int, error) }

	// Keys is the run's key-report collector, threaded here so the whole
	// resolve stage holds the same sink the applier and the command
	// surface hold.
	//
	// The resolver deliberately does not record into it. A shortfall it
	// marks is a fact about one configuration layer, and layers merge
	// last-wins: a key the team config could not resolve may be supplied
	// by the personal overlay immediately afterwards. Recording at mark
	// time would put that key in the report with a value sitting in the
	// merged config beside it. The single recording point is the
	// post-merge walk in internal/workspace, which reads the same marks
	// off the config that survived the merge.
	Keys *keyreport.Collector
}

// BuildBundle opens a vault.Bundle from a config.VaultRegistry. It
// accepts both the anonymous-singular shape (cfg.Provider) and the
// named-multiple shape (cfg.Providers). A nil or empty registry
// yields an empty Bundle — still valid to pass to CloseAll.
//
// sourceFile is attribution string passed through to each ProviderSpec
// for error-message purposes (e.g., "workspace.toml", "global-overlay").
//
// BuildBundle returns the concrete *vault.Bundle so callers can call
// Names() for R12 collision detection and pass it to ResolveWorkspace
// / ResolveGlobalOverride via ResolveOptions.
func BuildBundle(ctx context.Context, reg *vault.Registry, vr *config.VaultRegistry, sourceFile string) (*vault.Bundle, error) {
	if reg == nil {
		reg = vault.DefaultRegistry
	}

	specs := specsFromRegistry(vr, sourceFile)
	// Registry.Build returns an empty Bundle for an empty spec slice,
	// which is exactly the passthrough behavior we want.
	bundle, err := reg.Build(ctx, specs)
	if err != nil {
		return nil, fmt.Errorf("vault: building provider bundle from %s: %w", sourceFile, err)
	}
	return bundle, nil
}

// specsFromRegistry converts a config.VaultRegistry into the slice of
// ProviderSpec values expected by Registry.Build. The function is
// tolerant of a nil vr and produces a zero-length slice.
func specsFromRegistry(vr *config.VaultRegistry, sourceFile string) []vault.ProviderSpec {
	if vr == nil {
		return nil
	}
	var specs []vault.ProviderSpec
	if vr.Provider != nil {
		specs = append(specs, vault.ProviderSpec{
			Name:   "",
			Kind:   vr.Provider.Kind,
			Config: vault.ProviderConfig(vr.Provider.Config),
			Source: sourceFile,
		})
	}
	for name, p := range vr.Providers {
		// Providers may be declared with backend-specific keys only;
		// copy Kind from the outer struct and merge Config as-is.
		cfg := vault.ProviderConfig(p.Config)
		// Make sure the provider has its own name in its config for
		// backends that care (the fake backend reads config["name"]).
		if cfg == nil {
			cfg = vault.ProviderConfig{}
		}
		if _, has := cfg["name"]; !has {
			cfg["name"] = name
		}
		specs = append(specs, vault.ProviderSpec{
			Name:   name,
			Kind:   p.Kind,
			Config: cfg,
			Source: sourceFile,
		})
	}
	return specs
}

// CheckProviderNameCollision enforces R12 (personal-overlay add-only
// semantics for providers). If any name present in personal is also
// present in team, the function returns an error wrapping
// vault.ErrProviderNameCollision that names every colliding entry.
//
// The empty string IS a valid provider name (the anonymous singular
// shape); a collision on "" is just as actionable as a named one,
// and the error message reflects that.
func CheckProviderNameCollision(team, personal *vault.Bundle) error {
	if team == nil || personal == nil {
		return nil
	}
	// R12 compares NAMED providers only. Anonymous providers (empty-
	// string name) are file-scoped by D-9: each config file's
	// [vault.provider] is independent of the other file's. Two files
	// can each declare an anonymous provider without conflict because
	// their vault:// URIs resolve within their own file's context
	// before the merge runs (D-6 resolve-before-merge ordering).
	teamNames := map[string]bool{}
	for _, n := range team.Names() {
		if n == "" {
			continue // anonymous — file-scoped, not a shared name
		}
		teamNames[n] = true
	}
	var colliding []string
	for _, n := range personal.Names() {
		if n == "" {
			continue // anonymous — file-scoped
		}
		if teamNames[n] {
			colliding = append(colliding, n)
		}
	}
	if len(colliding) == 0 {
		return nil
	}
	// R9 remediation: name each colliding provider and point the
	// user at the per-key override path.
	return fmt.Errorf(
		"%w: personal overlay redeclares team-defined provider(s) %v; "+
			"the personal overlay may ADD new provider names but not REPLACE "+
			"team-declared ones. To override a single value, use [env.secrets] "+
			"in the personal overlay with a different provider (or inline plaintext)",
		vault.ErrProviderNameCollision, colliding,
	)
}

// ResolveWorkspace returns a new *config.WorkspaceConfig with every
// MaybeSecret{Plain: "vault://..."} resolved to MaybeSecret{Secret, Token}.
// The input cfg is never mutated.
//
// Behavior summary:
//
//   - Plaintext values in env.vars / claude.env.vars are preserved as
//     {Plain: "..."}.
//   - Plaintext values in env.secrets / claude.env.secrets are auto-
//     wrapped into secret.Value so downstream redaction still applies.
//     Plain is cleared; Secret is populated.
//   - vault:// URIs in any MaybeSecret slot are parsed and dispatched
//     to the bundle's provider. On success, Plain is cleared and
//     Secret/Token are populated. Values are registered on the
//     ctx-attached Redactor for log scrubbing.
//   - Missing keys with Optional (?required=false) silently downgrade
//     to empty MaybeSecret{}, unmarked. That is a deliberate empty,
//     not a shortfall.
//   - A shortfall — key not found on a reachable provider, provider
//     unreachable, client not installed, or a reference naming a
//     provider the active bundle does not declare — produces a marked
//     empty MaybeSecret rather than an error. Fatality is decided
//     later, post-merge, against the whole configuration.
//   - Configuration defects still propagate as errors: a malformed
//     reference, an unsupported reference version, an undecodable
//     body, a missing required field.
//
// The caller owns the bundle: ResolveWorkspace does not call CloseAll.
// When opts.TeamBundle is nil, ResolveWorkspace builds a bundle
// internally and closes it on return. Callers that want to apply R12
// checks before resolution must pass a pre-built bundle in TeamBundle.
func ResolveWorkspace(ctx context.Context, cfg *config.WorkspaceConfig, opts ResolveOptions) (*config.WorkspaceConfig, error) {
	if cfg == nil {
		return nil, nil
	}

	bundle := opts.TeamBundle
	closeOwned := false
	if bundle == nil {
		sourceFile := opts.SourceFile
		if sourceFile == "" {
			sourceFile = "workspace config"
		}
		built, err := BuildBundle(ctx, opts.Registry, cfg.Vault, sourceFile)
		if err != nil {
			return nil, err
		}
		bundle = built
		closeOwned = true
	}
	if closeOwned {
		defer func() { _ = bundle.CloseAll() }()
	}

	// Deep-copy the WorkspaceConfig down to every map containing
	// MaybeSecret so we do not mutate the input. The shallow fields
	// (Workspace, Sources, Groups, Repos keys, Vault) are fine to
	// share; the resolver never modifies them.
	out := deepCopyWorkspaceConfig(cfg)

	w := walker{
		ctx:    ctx,
		bundle: bundle,
		opts:   opts,
	}

	if err := w.walkEnv("env", &out.Env); err != nil {
		return nil, err
	}
	if err := w.walkClaudeEnv("claude.env", &out.Claude.Env); err != nil {
		return nil, err
	}
	if err := w.walkSettings("claude.settings", out.Claude.Settings); err != nil {
		return nil, err
	}
	if err := w.walkFilesKeys("files", out.Files); err != nil {
		return nil, err
	}
	for name, ov := range out.Repos {
		if err := w.walkEnv(fmt.Sprintf("repos.%s.env", name), &ov.Env); err != nil {
			return nil, err
		}
		if ov.Claude != nil {
			if err := w.walkClaudeEnv(fmt.Sprintf("repos.%s.claude.env", name), &ov.Claude.Env); err != nil {
				return nil, err
			}
			if err := w.walkSettings(fmt.Sprintf("repos.%s.claude.settings", name), ov.Claude.Settings); err != nil {
				return nil, err
			}
		}
		if err := w.walkFilesKeys(fmt.Sprintf("repos.%s.files", name), ov.Files); err != nil {
			return nil, err
		}
		out.Repos[name] = ov
	}
	if err := w.walkEnv("instance.env", &out.Instance.Env); err != nil {
		return nil, err
	}
	if out.Instance.Claude != nil {
		if err := w.walkClaudeEnv("instance.claude.env", &out.Instance.Claude.Env); err != nil {
			return nil, err
		}
		if err := w.walkSettings("instance.claude.settings", out.Instance.Claude.Settings); err != nil {
			return nil, err
		}
	}
	if err := w.walkFilesKeys("instance.files", out.Instance.Files); err != nil {
		return nil, err
	}

	return out, nil
}

// ResolveGlobalOverride performs the same walk over a
// *config.GlobalConfigOverride (both the flat [global] block and every
// [workspaces.<scope>] sub-block). The personal-overlay bundle is
// built from gco.Global.Vault — file-local scoping means per-workspace
// blocks share that bundle rather than declaring their own.
//
// The caller owns the bundle. When opts.PersonalBundle is nil,
// ResolveGlobalOverride builds a bundle internally and closes it on
// return.
func ResolveGlobalOverride(ctx context.Context, gco *config.GlobalConfigOverride, opts ResolveOptions) (*config.GlobalConfigOverride, error) {
	if gco == nil {
		return nil, nil
	}

	bundle := opts.PersonalBundle
	closeOwned := false
	if bundle == nil {
		sourceFile := opts.SourceFile
		if sourceFile == "" {
			sourceFile = "global overlay"
		}
		built, err := BuildBundle(ctx, opts.Registry, gco.Global.Vault, sourceFile)
		if err != nil {
			return nil, err
		}
		bundle = built
		closeOwned = true
	}
	if closeOwned {
		defer func() { _ = bundle.CloseAll() }()
	}

	out := deepCopyGlobalConfigOverride(gco)

	w := walker{
		ctx:    ctx,
		bundle: bundle,
		opts:   opts,
	}

	if err := w.walkGlobalOverride("global", &out.Global); err != nil {
		return nil, err
	}
	for name, ov := range out.Workspaces {
		if err := w.walkGlobalOverride(fmt.Sprintf("workspaces.%s", name), &ov); err != nil {
			return nil, err
		}
		out.Workspaces[name] = ov
	}

	return out, nil
}

// walker holds per-call resolve state to keep function signatures tidy.
type walker struct {
	ctx    context.Context
	bundle *vault.Bundle
	opts   ResolveOptions
}

// declaration is what the workspace config says about a key,
// independent of whether a value was resolved for it. The walker
// carries it alongside each value so a mark can record the declared
// level and description at the point of failure — after the merge,
// the requirement sub-tables are still available but the association
// with a specific resolution attempt is not.
type declaration struct {
	Level       config.RequirementLevel
	Description string
}

// declarationFor locates a key in the table's requirement sub-tables.
// A key may legitimately appear in none of them, in which case the
// zero declaration is returned. Sub-tables are checked strongest-first
// so a key listed twice reports the level that governs it.
func declarationFor(t config.EnvVarsTable, key string) declaration {
	if desc, ok := t.Required[key]; ok {
		return declaration{Level: config.LevelRequired, Description: desc}
	}
	if desc, ok := t.Recommended[key]; ok {
		return declaration{Level: config.LevelRecommended, Description: desc}
	}
	if desc, ok := t.Optional[key]; ok {
		return declaration{Level: config.LevelOptional, Description: desc}
	}
	return declaration{}
}

// walkEnv resolves an EnvConfig in place. Secrets sub-table values
// that don't start with vault:// are still wrapped in secret.Value
// for redaction purposes.
func (w *walker) walkEnv(prefix string, env *config.EnvConfig) error {
	if env == nil {
		return nil
	}
	if err := w.walkTable(prefix+".vars", env.Vars, false); err != nil {
		return err
	}
	return w.walkTable(prefix+".secrets", env.Secrets, true)
}

func (w *walker) walkClaudeEnv(prefix string, env *config.ClaudeEnvConfig) error {
	if env == nil {
		return nil
	}
	if err := w.walkTable(prefix+".vars", env.Vars, false); err != nil {
		return err
	}
	return w.walkTable(prefix+".secrets", env.Secrets, true)
}

// walkTable iterates a table's MaybeSecret map, resolving vault:// URIs
// and auto-wrapping plaintext when isSecretsTable is true. The whole
// table is passed rather than just its Values map so a shortfall can
// pick up the key's declared level and description from the sibling
// requirement sub-tables.
//
// t is taken by value; the maps inside it are shared, so writing back
// into t.Values mutates the caller's table as before.
func (w *walker) walkTable(prefix string, t config.EnvVarsTable, isSecretsTable bool) error {
	for key, ms := range t.Values {
		resolved, err := w.resolveOne(prefix+"."+key, ms, isSecretsTable, declarationFor(t, key))
		if err != nil {
			return err
		}
		t.Values[key] = resolved
	}
	return nil
}

// walkSettings resolves a SettingsConfig in place. Settings keys are
// treated as non-secrets-table: plaintext stays plaintext, vault://
// values are resolved into Secret.
//
// Settings have no requirement sub-tables, so an unresolved settings
// key is marked with the zero declaration.
func (w *walker) walkSettings(prefix string, settings config.SettingsConfig) error {
	for key, ms := range settings {
		resolved, err := w.resolveOne(prefix+"."+key, ms, false, declaration{})
		if err != nil {
			return err
		}
		settings[key] = resolved
	}
	return nil
}

// walkFilesKeys resolves vault:// references that may appear in files
// map KEYS (sources). See the file-keys notes in validate_vault_refs.go.
// In practice a vault-backed files key is unusual, but R3 permits it,
// and walking here keeps the resolver complete — we promote such keys
// by keeping the map-level structure and replacing the key with the
// resolved plaintext. That string is NOT secret (files keys are paths,
// not stored values), but to keep the walk simple we leave the map
// structure untouched; materializers later handle MaybeSecret-based
// keys through their own resolution.
//
// For v1, the simplest correct behavior: do nothing here. Files keys
// are strings, not MaybeSecret, and Issue 4's scope is MaybeSecret
// resolution. Future work can revisit if we choose to support
// vault-keyed files mappings end-to-end.
func (w *walker) walkFilesKeys(_ string, _ map[string]string) error {
	return nil
}

// walkGlobalOverride handles one GlobalOverride block (flat-global or
// per-workspace). The structure mirrors EnvConfig + Claude + Files but
// has no Repos/Instance nesting.
func (w *walker) walkGlobalOverride(prefix string, g *config.GlobalOverride) error {
	if g == nil {
		return nil
	}
	if err := w.walkEnv(prefix+".env", &g.Env); err != nil {
		return err
	}
	if g.Claude != nil {
		if err := w.walkClaudeEnv(prefix+".claude.env", &g.Claude.Env); err != nil {
			return err
		}
		if err := w.walkSettings(prefix+".claude.settings", g.Claude.Settings); err != nil {
			return err
		}
	}
	return w.walkFilesKeys(prefix+".files", g.Files)
}

// resolveOne applies the resolver's contract to a single MaybeSecret.
//
// Contract:
//
//   - Empty Plain: leave untouched (zero MaybeSecret is "empty
//     non-secret"). Do not auto-wrap empty values.
//   - Plain starts with vault://: parse, dispatch to provider, on
//     success populate Secret/Token and clear Plain. On a shortfall,
//     return an empty MaybeSecret carrying an Unresolved mark — except
//     for an Optional (?required=false) miss, which returns a bare
//     empty MaybeSecret and stays silent.
//   - Plain is a plaintext literal in a secrets table: wrap into
//     secret.Value, clear Plain.
//   - Plain is a plaintext literal in a non-secrets table: leave
//     untouched.
//
// location is the dotted TOML path used in error messages (e.g.,
// "env.secrets.GH_TOKEN"). decl carries the key's declared level and
// description, which ride along on any mark this call produces. The
// resolver registers resolved values on the ctx-attached Redactor so
// log scrubbing applies to any error the caller raises afterward.
func (w *walker) resolveOne(location string, ms config.MaybeSecret, isSecretsTable bool, decl declaration) (config.MaybeSecret, error) {
	// A value already resolved by an earlier pass (shouldn't happen
	// in practice but guard anyway) is left alone.
	if ms.IsSecret() {
		return ms, nil
	}
	// An already-marked value is left alone. This is not defensive
	// tidiness: the workspace-overlay layer is resolved against its
	// own provider bundle and then merged into the base config, which
	// the pipeline resolves again as a whole. Without this guard the
	// second pass would look at the overlay's marked values with the
	// wrong bundle in hand, and a mark that correctly said "provider
	// unreachable" would be overwritten by one saying "undeclared
	// provider" — a worse answer produced by re-asking a question
	// already answered.
	if ms.IsUnresolved() {
		return ms, nil
	}
	if ms.Plain == "" {
		return ms, nil
	}
	if !isVaultURI(ms.Plain) {
		if !isSecretsTable {
			return ms, nil
		}
		// Auto-wrap plaintext in secrets table. See DESIGN Decision 1
		// "Resolver auto-wraps *.secrets plaintext".
		val := secret.New([]byte(ms.Plain), secret.Origin{
			ProviderName: "",
			Key:          location,
		})
		w.registerOnRedactor(val)
		return config.MaybeSecret{Secret: val}, nil
	}

	mode := vault.ParseAnonymous
	if w.bundle.HasNamedProviders() {
		mode = vault.ParseNamed
	}
	ref, err := vault.ParseRef(ms.Plain, mode)
	if err != nil {
		return config.MaybeSecret{}, fmt.Errorf("vault: %s: %w", location, err)
	}

	provider, err := w.bundle.Get(ref.ProviderName)
	if err != nil {
		// The reference names a provider the merged configuration does
		// not declare. This is emphatically NOT key-not-found: no
		// provider was asked, so nothing establishes that a reachable
		// backend lacks the key. Classifying it as key-not-found would
		// make it fatal under the strict-when-reachable rule, which is
		// exactly the case a contributor with no vault configuration
		// hits on their first run.
		return config.MaybeSecret{Unresolved: &config.Unresolved{
			Cause:       config.CauseUndeclaredProvider,
			Level:       decl.Level,
			Description: decl.Description,
		}}, nil
	}

	val, token, resolveErr := provider.Resolve(w.ctx, ref)
	if resolveErr == nil {
		w.registerOnRedactor(val)
		return config.MaybeSecret{Secret: val, Token: token}, nil
	}

	// Missing key: the provider knows the key doesn't exist.
	if errors.Is(resolveErr, vault.ErrKeyNotFound) {
		if ref.Optional {
			// ?required=false downgrades silently.
			return config.MaybeSecret{}, nil
		}
		// A reachable provider that does not hold the key. This is the
		// one cause that keeps a required key fatal, decided later by
		// the post-merge required-key check rather than here.
		return config.MaybeSecret{Unresolved: &config.Unresolved{
			Cause:        config.CauseKeyNotFound,
			Level:        decl.Level,
			Description:  decl.Description,
			ProviderKind: provider.Kind(),
		}}, nil
	}

	// Client binary absent from the host. Tested before the broader
	// unreachable sentinel, which it wraps.
	if errors.Is(resolveErr, vault.ErrClientNotInstalled) {
		return config.MaybeSecret{Unresolved: &config.Unresolved{
			Cause:        config.CauseClientNotInstalled,
			Level:        decl.Level,
			Description:  decl.Description,
			ProviderKind: provider.Kind(),
		}}, nil
	}

	// Unreachable backend (auth failure, network, etc.).
	if errors.Is(resolveErr, vault.ErrProviderUnreachable) {
		return config.MaybeSecret{Unresolved: &config.Unresolved{
			Cause:        config.CauseProviderUnreachable,
			Level:        decl.Level,
			Description:  decl.Description,
			ProviderKind: provider.Kind(),
		}}, nil
	}

	// Any other error path.
	return config.MaybeSecret{}, secret.Errorf(
		"vault: %s: resolving %q via provider %q: %w",
		location, ref.Key, providerLabel(ref.ProviderName), resolveErr,
	)
}

// registerOnRedactor adds v's bytes to the ctx-scoped Redactor, if
// any. The secret's bytes themselves are the smallest fragment we
// can register; shorter than 6 bytes is refused by the Redactor
// (silent no-op). We do NOT error on that case because the secret
// value is already opaque via secret.Value; the only downside is
// that logs may contain the fragment verbatim, and that's a
// provider-side choice.
func (w *walker) registerOnRedactor(v secret.Value) {
	r := secret.RedactorFrom(w.ctx)
	if r == nil {
		return
	}
	r.RegisterValue(v)
}

// providerLabel returns a human-readable provider name, substituting
// "(anonymous)" for the empty-string anonymous-singular.
func providerLabel(name string) string {
	if name == "" {
		return "(anonymous)"
	}
	return name
}

// isVaultURI reports whether s carries the vault:// prefix. The
// prefix check is duplicated here and in internal/config to keep the
// resolver in lock-step with the config-layer validator.
func isVaultURI(s string) bool {
	return len(s) >= len("vault://") && s[:len("vault://")] == "vault://"
}
