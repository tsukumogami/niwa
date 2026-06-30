package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// prewarmCmdTimeout bounds a single `claude plugin` invocation during dispatch
// pre-warming. A marketplace add performs a network clone; without a bound a stuck
// fetch would hang the whole dispatch. On timeout the command is killed and the
// best-effort caller proceeds to launch -- the worker still installs from settings
// on startup. It is generous (clones are normally seconds) so a slow-but-working
// network is not cut off.
const prewarmCmdTimeout = 120 * time.Second

// prewarmDeclaredPlugins resolves an instance's workspace-declared Claude
// marketplaces and plugins to disk so the FIRST Claude session started in the
// instance finds them already installed when it enumerates skills. It is the
// cli-side implementation wired onto workspace.Applier.PrewarmDeclaredPlugins (see
// configurePluginAutoInstall), called from the provisioning pipeline after settings
// are materialized -- so every entry path benefits: `niwa dispatch`, `niwa create`
// + a manual `claude` launch, and `niwa apply`.
//
// It closes the race where a github-sourced marketplace (e.g. shirabe) is cloned
// asynchronously during a session's own Claude startup and finishes AFTER skill
// enumeration, leaving that marketplace's skills uninvocable for the whole session.
// Pre-warming runs that clone synchronously here so the session finds it on disk.
//
// The instance's just-written .claude/settings.json is the source of truth: it is
// the materialized, post-overlay-merge set of marketplaces/plugins, so reading it
// back keeps this self-contained and needs no extra config plumbing from the caller.
//
// It is best-effort. skipInstall (the same opt-out that gates InstallNiwaPlugin,
// already OR'd with the global auto_install_plugins setting by the caller) short-
// circuits it. Every other failure (claude absent, CLI error, unreadable settings)
// is a warning, never fatal: Claude still installs from settings.json at startup, so
// pre-warming only removes the race -- a provision must never be less robust than
// before when the plugin CLI is unavailable. reporter may be nil.
func prewarmDeclaredPlugins(instanceRoot string, reporter *workspace.Reporter, skipInstall bool) {
	if skipInstall {
		return
	}

	settings, err := readInstanceSettings(instanceRoot)
	if err != nil {
		// No settings file or unparseable: nothing to pre-warm. The session's own
		// startup install remains the fallback.
		return
	}

	// 1. Clone the github-sourced marketplaces -- the ones that require a network
	// fetch and therefore race. Directory/local sources are already on disk and
	// never race, so they are skipped.
	for _, name := range sortedKeys(marketplaceNames(settings.ExtraKnownMarketplaces)) {
		mkt := settings.ExtraKnownMarketplaces[name]
		if mkt.Source.Source != "github" || mkt.Source.Repo == "" {
			continue
		}
		// Only the repo is needed: pre-warming clones the marketplace to disk to
		// remove the network step from the session's startup. The exact ref/version
		// pin stays governed by the instance settings.json read at startup --
		// `claude plugin marketplace add` has no ref option anyway.
		if err := runClaudePluginCmd(context.Background(), instanceRoot, "marketplace", "add", mkt.Source.Repo); err != nil {
			warnPrewarm(reporter, "pre-warming marketplace %q (%s): %v; it will install on startup instead", name, mkt.Source.Repo, err)
		}
	}

	// 2. Install the enabled plugins so the plugin cache is populated before the
	// session enumerates skills -- the step that actually closes the race, since a
	// `marketplace add` clones the marketplace but does NOT populate the per-plugin
	// cache the first enumeration reads.
	//
	// Scope is local, not project. `--scope project` would re-serialize the instance's
	// .claude/settings.json -- the file niwa materializes and fingerprints as a managed
	// file -- so the next `niwa apply` reports it "modified outside niwa" (#179), even
	// though niwa already wrote the same enablement. `--scope local` writes the
	// enablement to .claude/settings.local.json instead, which niwa does not manage at
	// the instance root, so the managed settings.json is left byte-identical. Like
	// project scope, local scope is project-bound (cwd = instance), so it does not leak
	// enablement into the user's other projects -- the reason #178 avoided `--scope
	// user`. The plugin cache and the installed_plugins record (keyed on the instance
	// projectPath) are populated identically to project scope, so the race fix holds.
	for _, plugin := range sortedKeys(pluginNames(settings.EnabledPlugins)) {
		if err := runClaudePluginCmd(context.Background(), instanceRoot, "install", plugin, "--scope", "local"); err != nil {
			warnPrewarm(reporter, "pre-warming plugin %q: %v; it will install on startup instead", plugin, err)
		}
	}
}

// warnPrewarm emits a best-effort warning, tolerating a nil reporter (the seam
// contract allows a nil reporter, mirroring InstallNiwaPlugin).
func warnPrewarm(reporter *workspace.Reporter, format string, a ...any) {
	if reporter != nil {
		reporter.Warn(format, a...)
	}
}

// runClaudePluginCmd runs `claude plugin <args...>` with the working directory set
// to dir (so `--scope local` targets the instance). It is a package variable so
// tests can record the issued commands without a real claude install, mirroring the
// lookClaude/dispatchAttach seam pattern in dispatch.go. Output is folded into the
// returned error so a failure surfaces a useful message in the caller's warning.
var runClaudePluginCmd = func(ctx context.Context, dir string, args ...string) error {
	bin, err := lookClaude()
	if err != nil {
		return err
	}
	// Bound each invocation so a hung network clone (marketplace add) cannot stall
	// dispatch indefinitely; on timeout the command is killed and the best-effort
	// caller falls back to the worker's own startup install.
	ctx, cancel := context.WithTimeout(ctx, prewarmCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"plugin"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			return fmt.Errorf("%w: %s", err, trimmed)
		}
		return err
	}
	return nil
}

// instanceSettings is the narrow projection of .claude/settings.json this package
// reads back: just the plugin/marketplace declarations niwa materialized. Unknown
// fields are ignored.
type instanceSettings struct {
	EnabledPlugins         map[string]bool             `json:"enabledPlugins"`
	ExtraKnownMarketplaces map[string]marketplaceEntry `json:"extraKnownMarketplaces"`
	// RemoteControlAtStartup mirrors the Claude Code settings key. It is non-nil
	// only when a downstream [claude.settings] explicitly set it, which is how the
	// dispatch remote-control resolver tells "downstream decided" from "unset".
	RemoteControlAtStartup *bool `json:"remoteControlAtStartup"`
}

type marketplaceEntry struct {
	Source marketplaceSource `json:"source"`
}

// marketplaceSource is the subset of the Claude Code marketplace source shape (emitted
// by mapMarketplaceSourceWithIndex in internal/workspace) that pre-warming needs:
// Source is the kind ("github", "directory", ...) and Repo is set for github sources.
// The ref/path fields the emitter also writes are intentionally omitted -- pre-warming
// only clones github marketplaces to disk; everything else is governed by the
// settings.json the worker reads at startup.
type marketplaceSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo"`
}

// readInstanceSettings reads the dispatched instance's Claude settings from
// <instancePath>/.claude/settings.json. The instance root receives settings.json
// (per InstallWorkspaceRootSettings; see internal/workspace/permissions.go) -- the
// settings.local.json variant is for per-repo dirs, never the root, so it is not
// consulted here. Returns an error when the file is absent or not valid JSON;
// callers treat any error as "nothing to pre-warm."
func readInstanceSettings(instancePath string) (*instanceSettings, error) {
	data, err := os.ReadFile(filepath.Join(instancePath, ".claude", "settings.json"))
	if err != nil {
		return nil, err
	}
	var s instanceSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing settings.json: %w", err)
	}
	return &s, nil
}

func marketplaceNames(m map[string]marketplaceEntry) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	return names
}

func pluginNames(m map[string]bool) []string {
	names := make([]string, 0, len(m))
	for k, enabled := range m {
		if enabled {
			names = append(names, k)
		}
	}
	return names
}

// sortedKeys returns the input sorted, so the issued commands are deterministic
// (stable warnings and testable ordering).
func sortedKeys(names []string) []string {
	sort.Strings(names)
	return names
}
