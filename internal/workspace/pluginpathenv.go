package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/pluginrecord"
)

// PluginInstallPathFunc resolves an installed Claude plugin (by its registry
// key or bare name) to the absolute path of its cache/install directory. It
// returns ok=false when the plugin is not installed. It is the lookup seam used
// by resolvePluginPathEnv: production wires it to the real
// installed_plugins.json registry (see defaultPluginInstallPath); tests inject
// a fake so the resolution logic can be exercised without a real ~/.claude.
type PluginInstallPathFunc func(pluginKey string) (installPath string, ok bool)

// resolvePluginPathEnv turns the workspace's [[claude.plugin_path_env]] bindings
// into a map of environment variable name -> resolved absolute script path,
// suitable for injection into the materialized Claude env of every provisioned
// instance.
//
// It is the fail-safe core of Issue 4's cross-layer wiring: for each binding it
// looks up the declared plugin's install dir, joins the declared plugin-relative
// path, and confirms the result stays inside the install dir (both lexically AND
// after resolving symlinks) AND exists on disk. A binding that fails ANY of those
// checks is omitted from the result (never injected as a partial or out-of-tree
// value), so a hook that reads the missing variable sees an empty value and
// no-ops rather than executing an untrusted fallback. Because resolution runs on
// every provision, the value refreshes after a plugin version bump changes the
// install dir.
//
// lookup is the plugin-install-dir resolver (nil yields an empty map: nothing
// can be resolved). The returned map is nil when no binding resolves, so the
// caller injects nothing.
func resolvePluginPathEnv(bindings []config.PluginPathEnvBinding, lookup PluginInstallPathFunc) map[string]string {
	if len(bindings) == 0 || lookup == nil {
		return nil
	}

	out := make(map[string]string)
	for _, b := range bindings {
		if b.Name == "" || b.Plugin == "" || b.Path == "" {
			continue
		}
		installDir, ok := lookup(b.Plugin)
		if !ok || installDir == "" {
			// Plugin absent (or stale before the next provision): fail safe.
			continue
		}
		resolved, ok := confineToPluginDir(installDir, b.Path)
		if !ok {
			continue
		}
		// The resolved file must exist. A missing script (plugin layout drift,
		// or a stale install) fails safe to an absent variable.
		if info, err := os.Stat(resolved); err != nil || info.IsDir() {
			continue
		}
		// confineToPluginDir is a LEXICAL check: it cannot catch a symlink
		// planted INSIDE the install dir that points outside it, because
		// os.Stat above follows the link. Re-verify containment on the fully
		// symlink-resolved paths (both the install dir and the candidate). Any
		// EvalSymlinks error or an escape past the resolved install dir fails
		// safe, so an out-of-tree link is never injected.
		if _, ok := confineResolvedSymlinks(installDir, resolved); !ok {
			continue
		}
		out[b.Name] = resolved
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// injectPluginPathEnv writes the resolved name -> path pairs into the effective
// workspace config's [claude.env].vars table, so the existing Claude-env
// materialization pipeline carries them into the env block of BOTH the
// instance-root settings.json (via MergeInstanceOverrides) and each repo's
// settings.local.json (via MergeOverrides) — every surface a niwa-materialized
// hook might read. Injecting into the shared effectiveCfg (rather than only one
// materializer) is what lets a single call site cover create/apply/dispatch.
//
// It is a no-op when env is empty (nothing resolved), so an unresolvable binding
// leaves no variable behind — the fail-safe the hook relies on. Values are
// plaintext paths (not secrets).
//
// An operator-declared env var of the same name is NEVER overwritten: injection
// runs after the effective-config merge (and after checkRequiredKeys), so a
// binding whose name collides with a key already present in [claude.env].vars is
// skipped, leaving the operator's value authoritative. Silently clobbering an
// operator-set variable would be a surprising last-write-wins side effect of an
// unrelated plugin binding.
func injectPluginPathEnv(cfg *config.WorkspaceConfig, env map[string]string) {
	if cfg == nil || len(env) == 0 {
		return
	}
	if cfg.Claude.Env.Vars.Values == nil {
		cfg.Claude.Env.Vars.Values = make(map[string]config.MaybeSecret, len(env))
	}
	for name, path := range env {
		if _, exists := cfg.Claude.Env.Vars.Values[name]; exists {
			// Operator already declared this name; do not overwrite it.
			continue
		}
		cfg.Claude.Env.Vars.Values[name] = config.MaybeSecret{Plain: path}
	}
}

// confineToPluginDir joins a plugin-relative path onto the plugin install dir
// and confirms the cleaned result stays within it, so a "../" component in the
// declared path cannot point the resolved value outside the plugin cache (the
// trust boundary the design pins). Returns the confined absolute path and
// ok=true on success; ok=false when the path escapes.
//
// This is a LEXICAL check only: it operates on cleaned path strings and does NOT
// resolve symlinks. A symlink planted inside the install dir that points outside
// passes this check, so callers that will trust the resolved path MUST also run
// confineResolvedSymlinks once the target is known to exist on disk (see
// resolvePluginPathEnv).
func confineToPluginDir(installDir, relPath string) (string, bool) {
	if filepath.IsAbs(relPath) {
		return "", false
	}
	cleanDir := filepath.Clean(installDir)
	joined := filepath.Clean(filepath.Join(cleanDir, relPath))
	if !pathContained(cleanDir, joined) {
		return "", false
	}
	return joined, true
}

// pathContained reports whether target is dir itself or lies beneath it. It uses
// a separator-boundary check on the relative path so a sibling directory (e.g.
// "/plugins-evil" against "/plugins") is not mistaken for a child.
func pathContained(dir, target string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// confineResolvedSymlinks re-verifies containment AFTER resolving symlinks on
// both the install dir and the candidate path, closing the gap left by
// confineToPluginDir's lexical-only check: a symlink planted inside the install
// dir that points outside passes the lexical check but escapes once followed.
// The candidate must already exist on disk (filepath.EvalSymlinks fails on a
// missing path). Returns the fully symlink-resolved candidate and ok=true only
// when it still lies within the resolved install dir; any EvalSymlinks error or
// an escape yields ok=false so the caller fails safe and omits the binding.
func confineResolvedSymlinks(installDir, candidate string) (string, bool) {
	realDir, err := filepath.EvalSymlinks(installDir)
	if err != nil {
		return "", false
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	if !pathContained(realDir, realCandidate) {
		return "", false
	}
	return realCandidate, true
}

// defaultPluginInstallPath is the production PluginInstallPathFunc. It reads
// Claude Code's global installed_plugins registry and returns the InstallPath of
// the record whose plugin key matches pluginKey — either exactly (the full
// "<plugin>@<marketplace>" key) or by bare plugin name (the segment before the
// "@"). It is fail-safe: a missing/malformed registry, no match, or an ambiguous
// bare-name match returns ok=false so the caller omits the variable.
func defaultPluginInstallPath(pluginKey string) (string, bool) {
	reg, err := pluginrecord.Load()
	if err != nil || reg == nil {
		return "", false
	}
	return resolvePluginInstallDir(reg, pluginKey)
}

// resolvePluginInstallDir resolves pluginKey against the registry map to a
// single install dir. It fails safe when a BARE declared name matches more than
// one DISTINCT installed plugin (different install dirs shipped under different
// marketplace keys, e.g. "work-summary@shirabe" and "work-summary@evil"):
// because reg.Plugins iteration order is unspecified, returning the first match
// would resolve non-deterministically to whichever the map happened to yield, so
// the ambiguous binding is omitted instead. A full "<plugin>@<marketplace>" key
// matches exactly one registry key and never trips the ambiguity guard.
func resolvePluginInstallDir(reg *pluginrecord.Registry, pluginKey string) (string, bool) {
	var found string
	matched := false
	for key, records := range reg.Plugins {
		if !pluginKeyMatches(key, pluginKey) {
			continue
		}
		// A key's records are scopes/versions of the same plugin key; take its
		// first non-empty install dir (the prior within-key behavior).
		keyPath := ""
		for _, rec := range records {
			if rec.InstallPath != "" {
				keyPath = rec.InstallPath
				break
			}
		}
		if keyPath == "" {
			continue
		}
		if matched && keyPath != found {
			// Two distinct installed plugins matched a bare name: fail safe.
			return "", false
		}
		found = keyPath
		matched = true
	}
	if !matched {
		return "", false
	}
	return found, true
}

// pluginKeyMatches reports whether a registry plugin key (e.g.
// "work-summary@shirabe") satisfies a declared plugin reference. A declared
// reference matches either the full key or just the plugin-name segment before
// the "@", so config can name the plugin with or without its marketplace.
func pluginKeyMatches(registryKey, declared string) bool {
	if registryKey == declared {
		return true
	}
	name := registryKey
	if at := strings.LastIndexByte(registryKey, '@'); at >= 0 {
		name = registryKey[:at]
	}
	return name == declared
}
