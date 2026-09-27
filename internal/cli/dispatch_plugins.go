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

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// prewarmCmdTimeout bounds a single `claude plugin` invocation during dispatch
// pre-warming. A marketplace add performs a network clone; without a bound a stuck
// fetch would hang the whole dispatch. On timeout the command is killed and the
// best-effort caller proceeds to launch -- the worker still installs from settings
// on startup. It is generous (clones are normally seconds) so a slow-but-working
// network is not cut off.
const prewarmCmdTimeout = 120 * time.Second

// configurePluginAutoInstall wires the plugin opt-out and the pre-warm seam
// onto an Applier. Every CLI surface that constructs an Applier (apply, create,
// reset, ...) must call this helper, so both behave the same regardless of
// which command surfaced the rank-2 notice.
//
// flagOptOut is the per-invocation --no-install-plugins value; the persistent
// auto_install_plugins = false global-config setting is OR'd in here so callers
// don't have to load GlobalConfig twice. The opt-out gates two things at once:
// the embedded niwa plugin's install, and the pre-warming of the workspace's
// declared marketplaces.
//
// What is NOT wired here is the embedded plugin's installer. It used to arrive
// as a function field, because internal/plugin imported internal/workspace and
// the cli was the only place that could see both; internal/plugin is a leaf
// now, so the pipeline calls it directly and there is nothing to inject.
func configurePluginAutoInstall(applier *workspace.Applier, flagOptOut bool) {
	skipFromGlobal := false
	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil {
		skipFromGlobal = globalCfg.SkipPluginInstall()
	}
	applier.SkipPluginInstall = flagOptOut || skipFromGlobal
	applier.PrewarmDeclaredPlugins = prewarmDeclaredPlugins
}

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
// It is best-effort. skipInstall (the same opt-out that gates the embedded
// plugin's install, already OR'd with the global auto_install_plugins setting by
// the caller) short-circuits it. Every other failure (claude absent, CLI error,
// unreadable settings)
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
	//
	// The registration decides which commit the plugins below install from, so it
	// carries the ref niwa resolved: `<repo>#<ref>` for a release or explicit pin,
	// the bare repo for track = "main".
	//
	// Scope is local, and must never be user (the CLI's default). Claude Code keeps
	// one registration and one clone per marketplace name for the whole HOME, and it
	// refuses to add a network source that differs from a user-scope declaration of
	// the same name. A user-scope pin written by one instance would therefore block
	// every later release and every bare add, from any workspace, until someone edits
	// ~/.claude/settings.json by hand. Local-scope declarations are per project and
	// are not compared against each other, so instances can hold different pins; the
	// plugin cache is split by version, so each keeps its own installed version.
	//
	// When the HOME already declares the marketplace at user scope (every pre-warm
	// before this change wrote such a declaration), a pinned add is refused at any
	// scope. niwa does not remove or rewrite that declaration -- `marketplace remove`
	// uninstalls the marketplace's plugins from every project in the HOME -- so it
	// reports the pin it could not apply and installs from the existing registration.
	pinned := map[string]bool{}
	for _, name := range sortedKeys(marketplaceNames(settings.ExtraKnownMarketplaces)) {
		mkt := settings.ExtraKnownMarketplaces[name]
		if mkt.Source.Source != "github" || mkt.Source.Repo == "" {
			continue
		}
		target := marketplaceAddTarget(mkt.Source)
		err := runClaudePluginCmd(context.Background(), instanceRoot, "marketplace", "add", target, "--scope", "local")
		switch {
		case err == nil:
			pinned[name] = mkt.Source.Ref != ""
		case mkt.Source.Ref != "" && isDeclaredSourceConflict(err):
			warnPrewarm(reporter, "marketplace %q: pin %s not applied: this HOME already declares %q in ~/.claude/settings.json with a different source, and Claude Code refuses a per-instance pin while that declaration exists. Plugins install from the registered source instead. Removing that declaration lets instances pin, but it also uninstalls %q's plugins from every project until each is re-applied", name, mkt.Source.Ref, name, name)
		case mkt.Source.Ref != "":
			warnPrewarm(reporter, "marketplace %q: pin %s not applied: pre-warming %s failed: %v; plugins install from whatever is registered, or on startup", name, mkt.Source.Ref, target, err)
		default:
			warnPrewarm(reporter, "pre-warming marketplace %q (%s): %v; it will install on startup instead", name, target, err)
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
	//
	// `install` does nothing for a plugin this instance already has, so for a
	// marketplace just pinned above the install is followed by `update`, which moves
	// the instance's copy to the pinned version (a no-op when it is already there).
	// It runs straight after this instance's own add because the marketplace clone
	// is shared: another instance's add can move it between runs. track = "main"
	// marketplaces keep install-only behaviour, so an apply does not start silently
	// upgrading them to the branch tip.
	for _, plugin := range sortedKeys(pluginNames(settings.EnabledPlugins)) {
		if err := runClaudePluginCmd(context.Background(), instanceRoot, "install", plugin, "--scope", "local"); err != nil {
			warnPrewarm(reporter, "pre-warming plugin %q: %v; it will install on startup instead", plugin, err)
			continue
		}
		if !pinned[pluginMarketplace(plugin)] {
			continue
		}
		if err := runClaudePluginCmd(context.Background(), instanceRoot, "update", plugin, "--scope", "local"); err != nil {
			warnPrewarm(reporter, "moving plugin %q to its pinned version: %v", plugin, err)
		}
	}
}

// isDeclaredSourceConflict reports whether a failed `marketplace add` was Claude
// Code refusing a source that differs from the one already declared for that
// marketplace name. It keys on the CLI's wording, so a rewording degrades the
// warning to the generic "pin not applied" form rather than hiding it.
func isDeclaredSourceConflict(err error) bool {
	return strings.Contains(err.Error(), "differs from the one declared")
}

// pluginMarketplace returns the marketplace half of a `<plugin>@<marketplace>`
// identifier, or "" when there is none.
func pluginMarketplace(plugin string) string {
	if i := strings.LastIndexByte(plugin, '@'); i >= 0 {
		return plugin[i+1:]
	}
	return ""
}

// warnPrewarm emits a best-effort warning, tolerating a nil reporter (the seam
// contract allows a nil reporter, mirroring the notice emitters in
// internal/workspace).
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
// lookClaude reports the path to the Claude Code binary. Two callers want it by
// name rather than by declaration: the plugin prewarm below, which drives
// Claude Code's own `plugin` subcommand and answers for a capability the table
// declares no other agent can receive, and `niwa watch`, whose review
// continuation is Claude Code harness surface. In both the agent is not a choice
// being made at a call site -- it is the only agent the capability exists for --
// which is why this sits here rather than on the launch path, where the agent is
// resolved rather than assumed.
var lookClaude = func() (string, error) { return lookAgentBinary(claudeLaunchSpec().Binary) }

// claudeLaunchSpec is Claude Code's own launch description, for the two paths
// above that drive Claude Code specifically rather than whichever agent a
// workspace resolves to. It reads the same table the dispatch path reads, so
// there is still exactly one place that says how Claude Code is launched and
// what its management verbs are.
func claudeLaunchSpec() agentplan.LaunchSpec {
	spec, _ := agentplan.For(agent.AgentClaude).LaunchSpec()
	return spec
}

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
// reads back: the plugin/marketplace, remote-control, and keep-alive
// declarations niwa materialized. Unknown fields are ignored.
type instanceSettings struct {
	EnabledPlugins         map[string]bool             `json:"enabledPlugins"`
	ExtraKnownMarketplaces map[string]marketplaceEntry `json:"extraKnownMarketplaces"`
	// RemoteControlAtStartup mirrors the Claude Code settings key. It is non-nil
	// only when a downstream [claude.settings] explicitly set it, which is how the
	// dispatch remote-control resolver tells "downstream decided" from "unset".
	RemoteControlAtStartup *bool `json:"remoteControlAtStartup"`
	// KeepAliveOnDispatch mirrors the niwa-defined settings key (Claude Code
	// ignores it). Non-nil only when a downstream [claude.settings] explicitly
	// set it; the dispatch keep-alive resolver reads it as the downstream layer
	// between the --keep-alive flag and the host default.
	KeepAliveOnDispatch *bool `json:"keepAliveOnDispatch"`
}

type marketplaceEntry struct {
	Source marketplaceSource `json:"source"`
}

// marketplaceSource is the subset of the Claude Code marketplace source shape (emitted
// by mapMarketplaceSourceWithIndex in internal/workspace) that pre-warming needs:
// Source is the kind ("github", "directory", ...), Repo is set for github sources,
// and Ref is the pin niwa resolved for them (empty when the marketplace tracks its
// default branch). The path field directory sources carry is omitted: those are
// already on disk and are never pre-warmed.
type marketplaceSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
}

// marketplaceAddTarget is the argument `claude plugin marketplace add` takes for a
// github source: `<repo>#<ref>` when a ref is pinned, the bare repo otherwise.
func marketplaceAddTarget(src marketplaceSource) string {
	if src.Ref == "" {
		return src.Repo
	}
	return src.Repo + "#" + src.Ref
}

// readInstanceSettings reads the dispatched instance's Claude settings from
// <instancePath>/.claude/settings.json. The instance root receives settings.json
// (RootSettingsMaterializer writes it there) -- the settings.local.json variant
// is for per-repo dirs, never the root, so it is not consulted here. Returns an
// error when the file is absent or not valid JSON; callers treat any error as
// "nothing to pre-warm."
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
