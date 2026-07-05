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
// path, and confirms the result stays inside the install dir AND exists on disk.
// A binding that fails ANY of those checks is omitted from the result (never
// injected as a partial or out-of-tree value), so a hook that reads the missing
// variable sees an empty value and no-ops rather than executing an untrusted
// fallback. Because resolution runs on every provision, the value refreshes
// after a plugin version bump changes the install dir.
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
func injectPluginPathEnv(cfg *config.WorkspaceConfig, env map[string]string) {
	if cfg == nil || len(env) == 0 {
		return
	}
	if cfg.Claude.Env.Vars.Values == nil {
		cfg.Claude.Env.Vars.Values = make(map[string]config.MaybeSecret, len(env))
	}
	for name, path := range env {
		cfg.Claude.Env.Vars.Values[name] = config.MaybeSecret{Plain: path}
	}
}

// confineToPluginDir joins a plugin-relative path onto the plugin install dir
// and confirms the cleaned result stays within it, so a "../" component in the
// declared path cannot point the resolved value outside the plugin cache (the
// trust boundary the design pins). Returns the confined absolute path and
// ok=true on success; ok=false when the path escapes.
func confineToPluginDir(installDir, relPath string) (string, bool) {
	if filepath.IsAbs(relPath) {
		return "", false
	}
	cleanDir := filepath.Clean(installDir)
	joined := filepath.Clean(filepath.Join(cleanDir, relPath))
	rel, err := filepath.Rel(cleanDir, joined)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return joined, true
}

// defaultPluginInstallPath is the production PluginInstallPathFunc. It reads
// Claude Code's global installed_plugins registry and returns the InstallPath of
// the first record whose plugin key matches pluginKey — either exactly (the full
// "<plugin>@<marketplace>" key) or by bare plugin name (the segment before the
// "@"). It is fail-safe: a missing/malformed registry, or no match, returns
// ok=false so the caller omits the variable.
func defaultPluginInstallPath(pluginKey string) (string, bool) {
	reg, err := pluginrecord.Load()
	if err != nil || reg == nil {
		return "", false
	}
	for key, records := range reg.Plugins {
		if !pluginKeyMatches(key, pluginKey) {
			continue
		}
		for _, rec := range records {
			if rec.InstallPath != "" {
				return rec.InstallPath, true
			}
		}
	}
	return "", false
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
