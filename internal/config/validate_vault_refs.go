package config

import (
	"fmt"
	"sort"
	"strings"
)

// vaultURIPrefix is the prefix that identifies a vault reference. It
// duplicates internal/vault.vaultScheme to avoid an import cycle — the
// config layer must not depend on internal/vault beyond the types it
// already references through MaybeSecret's unexported members.
const vaultURIPrefix = "vault://"

// hasVaultPrefix reports whether s is a vault:// reference.
func hasVaultPrefix(s string) bool {
	return strings.HasPrefix(s, vaultURIPrefix)
}

// validateNoVaultRefs performs the post-parse walk that rejects
// vault:// URIs in contexts where PRD R3 forbids them:
//
//   - [claude.content.*] source paths (never a secret)
//   - [env.files] source paths (file references, not values)
//   - [vault.providers.*] / [vault.provider] config field values (can't
//     self-reference)
//   - Identifier fields: workspace name, source org, repo URL,
//     group name, repo override names, provider names.
//
// It also validates that every vault://<name>/key reference used in a
// MaybeSecret value refers to a provider declared in the same file.
// Cross-file references are NOT checked here; that's the resolver's
// job in Issue 4.
//
// Errors returned from this function are suitable for direct return
// from Parse; they include the offending URI and the field where it
// was found so users can correlate them with their config.
func validateNoVaultRefs(cfg *WorkspaceConfig) error {
	// Identifier fields: refuse vault:// outright.
	if hasVaultPrefix(cfg.Workspace.Name) {
		return fmt.Errorf("workspace.name must not be a vault:// reference: %q", cfg.Workspace.Name)
	}
	for i, src := range cfg.Sources {
		if hasVaultPrefix(src.Org) {
			return fmt.Errorf("sources[%d].org must not be a vault:// reference: %q", i, src.Org)
		}
	}
	for name, ov := range cfg.Repos {
		if hasVaultPrefix(name) {
			return fmt.Errorf("repos: key %q must not be a vault:// reference", name)
		}
		if hasVaultPrefix(ov.URL) {
			return fmt.Errorf("repos.%s.url must not be a vault:// reference: %q", name, ov.URL)
		}
		if hasVaultPrefix(ov.Group) {
			return fmt.Errorf("repos.%s.group must not be a vault:// reference: %q", name, ov.Group)
		}
	}
	for name := range cfg.Groups {
		if hasVaultPrefix(name) {
			return fmt.Errorf("groups: key %q must not be a vault:// reference", name)
		}
	}

	// [claude.content.*] source paths.
	if err := checkContentSourcesForVault(&cfg.Claude.Content); err != nil {
		return err
	}

	// [env.files] source paths.
	if err := checkEnvFilesForVault("env.files", cfg.Env.Files); err != nil {
		return err
	}
	for name, ov := range cfg.Repos {
		if err := checkEnvFilesForVault(fmt.Sprintf("repos.%s.env.files", name), ov.Env.Files); err != nil {
			return err
		}
	}
	if err := checkEnvFilesForVault("instance.env.files", cfg.Instance.Env.Files); err != nil {
		return err
	}

	// [vault.provider*] backend-specific config fields must not hold
	// vault:// URIs (no self-referential bootstrap).
	if cfg.Vault != nil {
		if cfg.Vault.Provider != nil {
			if err := checkProviderConfigForVault("vault.provider", cfg.Vault.Provider); err != nil {
				return err
			}
		}
		for pname, p := range cfg.Vault.Providers {
			if err := checkProviderConfigForVault(fmt.Sprintf("vault.providers.%s", pname), &p); err != nil {
				return err
			}
		}
	}

	// Same-file provider-name reference validation. If a vault:// URI
	// names a provider that isn't declared in this file's [vault]
	// block, reject it immediately.
	known := cfg.Vault.KnownProviderNames()
	if err := walkVaultRefsForUnknownProvider(cfg, known); err != nil {
		return err
	}

	return nil
}

// vaultRegistryShape captures the declared shape of a file's [vault]
// block so mismatch errors can name what's declared and what the author
// should switch to. It is computed once per file and passed to the
// walker's check closure rather than recomputed per URI.
type vaultRegistryShape struct {
	// hasAnon is true when the file declares [vault.provider] (the
	// anonymous single-provider shape).
	hasAnon bool
	// namedProviders lists the names declared under
	// [vault.providers.*], sorted for stable error wording.
	namedProviders []string
}

// describeShape inspects cfg.Vault and returns the declared shape. A
// nil or empty registry yields the zero shape (neither anon nor named);
// callers should handle the "no providers declared" case separately.
func describeShape(cfg *WorkspaceConfig) vaultRegistryShape {
	shape := vaultRegistryShape{}
	if cfg.Vault == nil {
		return shape
	}
	shape.hasAnon = cfg.Vault.Provider != nil
	if len(cfg.Vault.Providers) > 0 {
		shape.namedProviders = make([]string, 0, len(cfg.Vault.Providers))
		for name := range cfg.Vault.Providers {
			shape.namedProviders = append(shape.namedProviders, name)
		}
		sort.Strings(shape.namedProviders)
	}
	return shape
}

// checkContentSourcesForVault walks [claude.content.*] entries and
// rejects any source that begins with vault://.
func checkContentSourcesForVault(c *ContentConfig) error {
	if c == nil {
		return nil
	}
	if hasVaultPrefix(c.Workspace.Source) {
		return fmt.Errorf("claude.content.workspace.source must not be a vault:// reference: %q", c.Workspace.Source)
	}
	for name, entry := range c.Groups {
		if hasVaultPrefix(entry.Source) {
			return fmt.Errorf("claude.content.groups.%s.source must not be a vault:// reference: %q", name, entry.Source)
		}
	}
	for name, entry := range c.Repos {
		if hasVaultPrefix(entry.Source) {
			return fmt.Errorf("claude.content.repos.%s.source must not be a vault:// reference: %q", name, entry.Source)
		}
		for sub, src := range entry.Subdirs {
			if hasVaultPrefix(src) {
				return fmt.Errorf("claude.content.repos.%s.subdirs.%s must not be a vault:// reference: %q", name, sub, src)
			}
		}
	}
	return nil
}

// checkEnvFilesForVault rejects vault:// URIs in an Env.Files slice.
// Env files are read from disk; a vault URI has no meaning there.
func checkEnvFilesForVault(prefix string, files []string) error {
	for _, f := range files {
		if hasVaultPrefix(f) {
			return fmt.Errorf("%s must not contain a vault:// reference: %q", prefix, f)
		}
	}
	return nil
}

// checkProviderConfigForVault rejects vault:// values in a provider's
// backend-specific config map. Providers cannot be configured using
// other providers' secrets (bootstrap hazard).
func checkProviderConfigForVault(prefix string, p *VaultProviderConfig) error {
	if p == nil {
		return nil
	}
	if hasVaultPrefix(p.Kind) {
		return fmt.Errorf("%s.kind must not be a vault:// reference: %q", prefix, p.Kind)
	}
	return checkAnyMapForVault(prefix, p.Config)
}

// checkAnyMapForVault recursively scans a map[string]any (and its
// nested maps / slices) for vault:// string values.
func checkAnyMapForVault(prefix string, m map[string]any) error {
	for k, v := range m {
		if err := checkAnyForVault(prefix+"."+k, v); err != nil {
			return err
		}
	}
	return nil
}

func checkAnyForVault(field string, v any) error {
	switch val := v.(type) {
	case string:
		if hasVaultPrefix(val) {
			return fmt.Errorf("%s must not be a vault:// reference: %q", field, val)
		}
	case map[string]any:
		return checkAnyMapForVault(field, val)
	case []any:
		for i, item := range val {
			if err := checkAnyForVault(fmt.Sprintf("%s[%d]", field, i), item); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkVaultRefsForUnknownProvider walks every MaybeSecret slot in cfg
// whose Plain value is a vault:// URI and verifies the referenced
// provider name is declared in the same file. If known is empty and a
// vault:// URI is encountered, returns an error naming the URI and its
// location. The resolver (Issue 4) is responsible for validating that
// the URI's grammar is well-formed; this function only extracts the
// provider-name segment.
func walkVaultRefsForUnknownProvider(cfg *WorkspaceConfig, known map[string]bool) error {
	shape := describeShape(cfg)
	check := func(location, uri string) error {
		if !hasVaultPrefix(uri) {
			return nil
		}
		if len(known) == 0 {
			return fmt.Errorf(
				"%s references %q but the config declares no [vault] providers",
				location, uri,
			)
		}

		// Anonymous-shape files use path-form URIs
		// (vault://[<path.../>]<key>). Any leading segments are a
		// folder path the backend interprets; no provider-name
		// validation applies. Syntax-level errors (empty segments,
		// fragments, etc.) are caught later by ParseRef at resolve
		// time. See DESIGN-vault-integration.md Decision 7.
		if shape.hasAnon {
			return nil
		}

		// Named-shape files require vault://<name>/<key> with <name>
		// matching a declared provider. Reject bare keys, nested
		// slashes, and unknown names at config-load time so the error
		// surfaces before any resolver work.
		rest := strings.TrimPrefix(uri, vaultURIPrefix)
		if q := strings.IndexByte(rest, '?'); q >= 0 {
			rest = rest[:q]
		}
		slashCount := strings.Count(rest, "/")
		name := extractProviderName(uri)

		if slashCount == 0 {
			return fmt.Errorf(
				"%s: vault URI %q uses anonymous form (no provider "+
					"name) but this file declares named providers "+
					"[%s]. Use a named URI like %q, or switch the "+
					"vault declaration to [vault.provider] to enable "+
					"folder-path URIs.",
				location, uri,
				strings.Join(shape.namedProviders, ", "),
				"vault://<name>/<key>",
			)
		}
		if slashCount > 1 {
			return fmt.Errorf(
				"%s: vault URI %q has nested slashes; named-provider "+
					"URIs accept only vault://<name>/<key>. Declared "+
					"providers in this file: [%s]. Switch the "+
					"declaration to [vault.provider] for folder-path "+
					"URIs.",
				location, uri,
				strings.Join(shape.namedProviders, ", "),
			)
		}
		if !known[name] {
			return fmt.Errorf(
				"%s: vault URI %q references unknown provider %q. "+
					"Declared providers in this file: [%s]. Fix the "+
					"provider name, or switch the declaration to "+
					"[vault.provider] to enable folder-path URIs.",
				location, uri, name,
				strings.Join(shape.namedProviders, ", "),
			)
		}
		return nil
	}

	checkEnvMap := func(prefix string, env EnvConfig) error {
		for k, v := range env.Vars.Values {
			if err := check(fmt.Sprintf("%s.vars.%s", prefix, k), v.Plain); err != nil {
				return err
			}
		}
		for k, v := range env.Secrets.Values {
			if err := check(fmt.Sprintf("%s.secrets.%s", prefix, k), v.Plain); err != nil {
				return err
			}
		}
		return nil
	}

	checkClaudeEnv := func(prefix string, env ClaudeEnvConfig) error {
		for k, v := range env.Vars.Values {
			if err := check(fmt.Sprintf("%s.vars.%s", prefix, k), v.Plain); err != nil {
				return err
			}
		}
		for k, v := range env.Secrets.Values {
			if err := check(fmt.Sprintf("%s.secrets.%s", prefix, k), v.Plain); err != nil {
				return err
			}
		}
		return nil
	}

	if err := checkEnvMap("env", cfg.Env); err != nil {
		return err
	}
	if err := checkClaudeEnv("claude.env", cfg.Claude.Env); err != nil {
		return err
	}
	for k, v := range cfg.Claude.Settings {
		if err := check(fmt.Sprintf("claude.settings.%s", k), v.Plain); err != nil {
			return err
		}
	}
	for name, ov := range cfg.Repos {
		if err := checkEnvMap(fmt.Sprintf("repos.%s.env", name), ov.Env); err != nil {
			return err
		}
		if ov.Claude != nil {
			if err := checkClaudeEnv(fmt.Sprintf("repos.%s.claude.env", name), ov.Claude.Env); err != nil {
				return err
			}
			for k, v := range ov.Claude.Settings {
				if err := check(fmt.Sprintf("repos.%s.claude.settings.%s", name, k), v.Plain); err != nil {
					return err
				}
			}
		}
	}
	if err := checkEnvMap("instance.env", cfg.Instance.Env); err != nil {
		return err
	}
	if cfg.Instance.Claude != nil {
		if err := checkClaudeEnv("instance.claude.env", cfg.Instance.Claude.Env); err != nil {
			return err
		}
		for k, v := range cfg.Instance.Claude.Settings {
			if err := check(fmt.Sprintf("instance.claude.settings.%s", k), v.Plain); err != nil {
				return err
			}
		}
	}
	// [files] source KEYS are plain strings; per R3 they may be
	// vault:// references. Per the same-file rule we still validate
	// that the referenced provider is declared here.
	for k := range cfg.Files {
		if err := check(fmt.Sprintf("files[%q]", k), k); err != nil {
			return err
		}
	}
	for name, ov := range cfg.Repos {
		for k := range ov.Files {
			if err := check(fmt.Sprintf("repos.%s.files[%q]", name, k), k); err != nil {
				return err
			}
		}
	}
	for k := range cfg.Instance.Files {
		if err := check(fmt.Sprintf("instance.files[%q]", k), k); err != nil {
			return err
		}
	}

	return nil
}

// extractProviderName returns the provider segment of a vault:// URI.
// For vault://name/key this is "name"; for vault://key (no separator)
// this is "". The function assumes the caller has already confirmed the
// vault:// prefix. Invalid URIs surface as the resolver's problem
// (Issue 4); this helper is intentionally lenient.
func extractProviderName(uri string) string {
	rest := strings.TrimPrefix(uri, vaultURIPrefix)
	// Strip query string for this extraction; we don't care about it.
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		rest = rest[:q]
	}
	// vault://name/key — the first "/" separates name from key.
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	// vault://key — anonymous provider.
	return ""
}
