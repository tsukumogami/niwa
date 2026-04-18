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
	"os"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// ResolveOptions tune resolver behavior for a single niwa apply.
type ResolveOptions struct {
	// AllowMissing, when true, downgrades missing-key errors to an
	// empty MaybeSecret and emits a stderr warning. The CLI flag
	// --allow-missing-secrets threads through this field.
	AllowMissing bool

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

	// Stderr, when non-nil, receives AllowMissing warnings. Tests
	// capture it to a *bytes.Buffer; the default is os.Stderr.
	Stderr interface{ Write(p []byte) (int, error) }
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
//     to empty MaybeSecret{}.
//   - Missing keys without Optional when opts.AllowMissing is true
//     downgrade to empty MaybeSecret{} and emit a stderr warning
//     naming the key and provider.
//   - All other errors propagate.
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
		stderr: opts.Stderr,
	}
	if w.stderr == nil {
		w.stderr = os.Stderr
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
		stderr: opts.Stderr,
	}
	if w.stderr == nil {
		w.stderr = os.Stderr
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
	stderr interface{ Write(p []byte) (int, error) }
}

// walkEnv resolves an EnvConfig in place. Secrets sub-table values
// that don't start with vault:// are still wrapped in secret.Value
// for redaction purposes.
func (w *walker) walkEnv(prefix string, env *config.EnvConfig) error {
	if env == nil {
		return nil
	}
	if err := w.walkTable(prefix+".vars", env.Vars.Values, false); err != nil {
		return err
	}
	return w.walkTable(prefix+".secrets", env.Secrets.Values, true)
}

func (w *walker) walkClaudeEnv(prefix string, env *config.ClaudeEnvConfig) error {
	if env == nil {
		return nil
	}
	if err := w.walkTable(prefix+".vars", env.Vars.Values, false); err != nil {
		return err
	}
	return w.walkTable(prefix+".secrets", env.Secrets.Values, true)
}

// walkTable iterates a MaybeSecret map, resolving vault:// URIs and
// auto-wrapping plaintext when isSecretsTable is true.
func (w *walker) walkTable(prefix string, values map[string]config.MaybeSecret, isSecretsTable bool) error {
	for key, ms := range values {
		resolved, err := w.resolveOne(prefix+"."+key, ms, isSecretsTable)
		if err != nil {
			return err
		}
		values[key] = resolved
	}
	return nil
}

// walkSettings resolves a SettingsConfig in place. Settings keys are
// treated as non-secrets-table: plaintext stays plaintext, vault://
// values are resolved into Secret.
func (w *walker) walkSettings(prefix string, settings config.SettingsConfig) error {
	for key, ms := range settings {
		resolved, err := w.resolveOne(prefix+"."+key, ms, false)
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
//     success populate Secret/Token and clear Plain. On miss with
//     Optional or AllowMissing, return empty MaybeSecret{}.
//   - Plain is a plaintext literal in a secrets table: wrap into
//     secret.Value, clear Plain.
//   - Plain is a plaintext literal in a non-secrets table: leave
//     untouched.
//
// location is the dotted TOML path used in error messages (e.g.,
// "env.secrets.GH_TOKEN"). The resolver registers resolved values on
// the ctx-attached Redactor so log scrubbing applies to any error the
// caller raises afterward.
func (w *walker) resolveOne(location string, ms config.MaybeSecret, isSecretsTable bool) (config.MaybeSecret, error) {
	// A value already resolved by an earlier pass (shouldn't happen
	// in practice but guard anyway) is left alone.
	if ms.IsSecret() {
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
		// Unknown provider name at resolve time is actionable as a
		// key-not-found problem from the user's perspective: they
		// referenced a provider not declared in the active config.
		// We return a wrapped ErrKeyNotFound so downstream
		// diagnostics treat it as a missing reference.
		label := ref.ProviderName
		if label == "" {
			label = "(anonymous)"
		}
		return config.MaybeSecret{}, fmt.Errorf(
			"vault: %s references provider %q but it is not declared in the active bundle: %w",
			location, label, vault.ErrKeyNotFound,
		)
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
		if w.opts.AllowMissing {
			fmt.Fprintf(w.stderr,
				"warning: vault: %s: key %q not found via provider %q; downgrading to empty (--allow-missing-secrets)\n",
				location, ref.Key, providerLabel(ref.ProviderName))
			return config.MaybeSecret{}, nil
		}
		// R9 remediation: name the provider and key, distinguish
		// failure modes, and point to US-9 paths.
		return config.MaybeSecret{}, fmt.Errorf(
			"vault: %s: key %q not found via provider %q; "+
				"declare it in the provider or mark the ref ?required=false "+
				"(or re-run with --allow-missing-secrets to downgrade): %w",
			location, ref.Key, providerLabel(ref.ProviderName), resolveErr,
		)
	}

	// Unreachable backend (auth failure, network, etc.). Wrap with
	// secret.Errorf because the provider's error chain may already
	// carry fragments registered on the redactor.
	if errors.Is(resolveErr, vault.ErrProviderUnreachable) {
		return config.MaybeSecret{}, secret.Errorf(
			"vault: %s: provider %q unreachable while resolving key %q: %w",
			location, providerLabel(ref.ProviderName), ref.Key, resolveErr,
		)
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
