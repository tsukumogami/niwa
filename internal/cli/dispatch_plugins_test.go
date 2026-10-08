package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// writeInstanceSettings writes a .claude/settings.json under a fresh temp instance
// dir and returns the instance path.
func writeInstanceSettings(t *testing.T, body string) string {
	t.Helper()
	instance := t.TempDir()
	claudeDir := filepath.Join(instance, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	return instance
}

// recordPluginCalls swaps runClaudePluginCmd for a recorder and restores it on
// cleanup. err is returned from every invocation (nil for success).
func recordPluginCalls(t *testing.T, err error) *[][]string {
	t.Helper()
	var calls [][]string
	prev := runClaudePluginCmd
	runClaudePluginCmd = func(_ context.Context, dir string, args ...string) error {
		calls = append(calls, append([]string{dir}, args...))
		return err
	}
	t.Cleanup(func() { runClaudePluginCmd = prev })
	return &calls
}

// The settings fixtures below mirror the shape niwa emits in
// mapMarketplaceSourceWithIndex (internal/workspace): github sources carry
// source+repo (+ref when pinned), directory sources carry
// source+path. If that emitted shape changes, these fixtures (and the reader in
// dispatch_plugins.go) must change together.

func TestPrewarm_GithubMarketplacesAndPlugins(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"shirabe@shirabe": true, "tsukumogami@tsukumogami": true},
	  "extraKnownMarketplaces": {
	    "shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe", "ref": "v0.13.0"}},
	    "tsukumogami": {"source": {"source": "directory", "path": "/local/tools"}}
	  }
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	want := [][]string{
		{instance, "marketplace", "add", "tsukumogami/shirabe#v0.13.0", "--scope", "local"},
		{instance, "install", "shirabe@shirabe", "--scope", "local"},
		{instance, "update", "shirabe@shirabe", "--scope", "local"},
		{instance, "install", "tsukumogami@tsukumogami", "--scope", "local"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls =\n  %v\nwant\n  %v", *calls, want)
	}
}

// TestPrewarm_MarketplaceAddCarriesResolvedRef guards #327: the marketplace
// registration decides which commit the plugins install from, so a pinned ref in
// settings.json must reach `claude plugin marketplace add` as `<repo>#<ref>`, and
// an unpinned entry (track = "main" writes no ref) must register the bare repo.
func TestPrewarm_MarketplaceAddCarriesResolvedRef(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "extraKnownMarketplaces": {
	    "koto": {"source": {"source": "github", "repo": "tsukumogami/koto", "ref": "v0.13.0"}},
	    "shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}},
	    "slashy": {"source": {"source": "github", "repo": "acme/slashy", "ref": "release/2.x"}}
	  }
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	want := [][]string{
		{instance, "marketplace", "add", "tsukumogami/koto#v0.13.0", "--scope", "local"},
		{instance, "marketplace", "add", "tsukumogami/shirabe", "--scope", "local"},
		{instance, "marketplace", "add", "acme/slashy#release/2.x", "--scope", "local"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls =\n  %v\nwant\n  %v", *calls, want)
	}
}

// TestPrewarm_NeverTouchesUserScope guards the scope half of #327. Claude Code
// keeps one registration per marketplace name for the whole HOME and refuses any
// source that differs from a user-scope declaration, so a user-scope write by one
// instance would lock every other workspace on the machine to that source. Every
// command the pre-install issues must name --scope local, none may name user, and
// nothing may be written under $HOME.
func TestPrewarm_NeverTouchesUserScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"koto-skills@koto": true, "shirabe@shirabe": true, "tools@tools": true},
	  "extraKnownMarketplaces": {
	    "koto": {"source": {"source": "github", "repo": "tsukumogami/koto", "ref": "v0.13.0"}},
	    "shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}},
	    "tools": {"source": {"source": "directory", "path": "/local/tools"}}
	  }
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	if len(*calls) == 0 {
		t.Fatal("pre-warm issued no commands; the scope assertions below would pass vacuously")
	}
	for _, c := range *calls {
		args := c[1:]
		scope := ""
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "--scope" {
				scope = args[i+1]
			}
		}
		if scope != "local" {
			t.Errorf("command %v: --scope = %q, want \"local\" (the CLI's default is user)", args, scope)
		}
		for _, a := range args {
			if a == "user" || a == "--scope=user" {
				t.Errorf("command %v names user scope", args)
			}
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("reading HOME: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("pre-warm wrote under HOME: %v", entries)
	}
}

// TestPrewarm_UpdatesOnlyPinnedMarketplacesAfterASuccessfulAdd: `install` is a
// no-op for a plugin the instance already has, so a pinned marketplace's plugins
// get a follow-up `update` that moves them to the pin on re-apply. Unpinned
// (track = "main") marketplaces keep install-only behaviour, and a refused add
// gets no update: the registered source is not the pin, and updating would move
// the instance to whatever that source currently offers.
func TestPrewarm_UpdatesOnlyPinnedMarketplacesAfterASuccessfulAdd(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"koto-skills@koto": true, "shirabe@shirabe": true, "held@held": true},
	  "extraKnownMarketplaces": {
	    "koto": {"source": {"source": "github", "repo": "tsukumogami/koto", "ref": "v0.13.0"}},
	    "shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}},
	    "held": {"source": {"source": "github", "repo": "acme/held", "ref": "v2.0.0"}}
	  }
	}`)
	var calls [][]string
	prev := runClaudePluginCmd
	runClaudePluginCmd = func(_ context.Context, _ string, args ...string) error {
		calls = append(calls, args)
		if len(args) > 2 && args[0] == "marketplace" && args[2] == "acme/held#v2.0.0" {
			return errors.New("exit status 1: Cannot add marketplace \"held\": its network source differs from the one declared for it in settings")
		}
		return nil
	}
	t.Cleanup(func() { runClaudePluginCmd = prev })

	prewarmDeclaredPlugins(instance, nil, false)

	var updates []string
	for _, c := range calls {
		if c[0] == "update" {
			updates = append(updates, c[1])
		}
	}
	if want := []string{"koto-skills@koto"}; !reflect.DeepEqual(updates, want) {
		t.Errorf("updated plugins = %v, want %v", updates, want)
	}
}

// TestPrewarm_RefusedPinIsReportedByName: when the HOME already declares the
// marketplace with another source, Claude Code refuses the pinned add. That must
// never be silent: an error-level line in the deferred summary block names the
// marketplace, the pin not applied and, for a declared-source conflict, the
// one-time remedy. Each wording Claude Code has used for the refusal must reach
// the remedy; any other failure gets the generic form.
func TestPrewarm_RefusedPinIsReportedByName(t *testing.T) {
	remedy := []string{"error: ", `marketplace "koto"`, "pin v0.13.0 not applied", "already declares", "claude plugin marketplace remove koto", "niwa apply", "uninstalls"}
	cases := []struct {
		name     string
		addErr   string
		wantText []string
		notText  []string
	}{
		{
			name:     "declared-source conflict, older wording",
			addErr:   "exit status 1: Cannot add marketplace \"koto\": its network source differs from the one declared for it in settings",
			wantText: remedy,
		},
		{
			name:     "declared-source conflict, extraKnownMarketplaces wording",
			addErr:   "exit status 1: Failed to add marketplace: Cannot add marketplace \"koto\": its source doesn't match its extraKnownMarketplaces entry in user or managed settings; add it from the source that entry lists, or change the entry.",
			wantText: remedy,
		},
		{
			name:     "declared-source conflict, typographic apostrophe",
			addErr:   "exit status 1: Cannot add marketplace \"koto\": its source doesn\u2019t match its extraKnownMarketplaces entry in user or managed settings",
			wantText: remedy,
		},
		{
			name:     "any other failure",
			addErr:   "exit status 128: could not resolve host",
			wantText: []string{"error: ", `marketplace "koto"`, "pin v0.13.0 not applied", "could not resolve host"},
			notText:  []string{"already declares", "marketplace remove"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := writeInstanceSettings(t, `{
			  "enabledPlugins": {"koto-skills@koto": true},
			  "extraKnownMarketplaces": {
			    "koto": {"source": {"source": "github", "repo": "tsukumogami/koto", "ref": "v0.13.0"}}
			  }
			}`)
			prev := runClaudePluginCmd
			runClaudePluginCmd = func(_ context.Context, _ string, args ...string) error {
				if args[0] == "marketplace" {
					return errors.New(tc.addErr)
				}
				return nil
			}
			t.Cleanup(func() { runClaudePluginCmd = prev })
			var buf bytes.Buffer

			reporter := workspace.NewReporter(&buf)

			prewarmDeclaredPlugins(instance, reporter, false)

			if buf.Len() != 0 {
				t.Errorf("unapplied pin reported inline instead of in the summary block:\n%s", buf.String())
			}
			reporter.FlushDeferred()
			for _, want := range tc.wantText {
				if !strings.Contains(buf.String(), want) {
					t.Errorf("report missing %q:\n%s", want, buf.String())
				}
			}
			for _, unwanted := range tc.notText {
				if strings.Contains(buf.String(), unwanted) {
					t.Errorf("report unexpectedly contains %q:\n%s", unwanted, buf.String())
				}
			}
			if strings.Contains(buf.String(), "warning: ") {
				t.Errorf("unapplied pin reported as a warning, want error level:\n%s", buf.String())
			}
		})
	}
}

// TestPrewarm_InstallsAtLocalScopeNotProject guards the #179 fix: the install must
// use `--scope local`, never `--scope project`. Project scope re-serializes the
// instance's .claude/settings.json -- the file niwa fingerprints as managed -- so the
// next `niwa apply` falsely reports it "modified outside niwa". Local scope writes the
// enablement to the unmanaged settings.local.json instead while still populating the
// plugin cache, so the race fix holds without dirtying the managed file.
func TestPrewarm_InstallsAtLocalScopeNotProject(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"shirabe@shirabe": true}
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	for _, c := range *calls {
		if len(c) >= 2 && c[1] == "install" {
			scope := c[len(c)-1]
			if scope == "project" {
				t.Errorf("install must not use --scope project (dirties niwa-managed settings.json); call %v", c)
			}
			if scope != "local" {
				t.Errorf("install must use --scope local, got %q in call %v", scope, c)
			}
		}
	}
}

func TestPrewarm_SkipsDisabledPlugins(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"on@mkt": true, "off@mkt": false}
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	want := [][]string{{instance, "install", "on@mkt", "--scope", "local"}}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("disabled plugin should be skipped; calls = %v, want %v", *calls, want)
	}
}

func TestPrewarm_SkipsDirectoryMarketplaces(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "extraKnownMarketplaces": {
	    "tsukumogami": {"source": {"source": "directory", "path": "/local/tools"}}
	  }
	}`)
	calls := recordPluginCalls(t, nil)

	prewarmDeclaredPlugins(instance, nil, false)

	for _, c := range *calls {
		if len(c) >= 2 && c[1] == "marketplace" {
			t.Errorf("directory marketplace should not be added, got call %v", c)
		}
	}
}

func TestPrewarm_OptOutShortCircuits(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"shirabe@shirabe": true},
	  "extraKnownMarketplaces": {"shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}}}
	}`)
	calls := recordPluginCalls(t, nil)

	// skipInstall=true (the opt-out the caller computes from --no-install-plugins +
	// auto_install_plugins) must issue no plugin commands.
	prewarmDeclaredPlugins(instance, nil, true)

	if len(*calls) != 0 {
		t.Errorf("opt-out should issue no plugin commands, got %v", *calls)
	}
}

func TestPrewarm_MissingSettingsIsNoOp(t *testing.T) {
	calls := recordPluginCalls(t, nil)

	// A temp dir with no .claude/settings.json.
	prewarmDeclaredPlugins(t.TempDir(), nil, false)

	if len(*calls) != 0 {
		t.Errorf("missing settings should issue no commands, got %v", *calls)
	}
}

func TestPrewarm_ExecFailureIsNonFatalAndWarns(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "enabledPlugins": {"shirabe@shirabe": true},
	  "extraKnownMarketplaces": {"shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}}}
	}`)
	calls := recordPluginCalls(t, errors.New("boom"))
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	// Must not panic and must attempt both the marketplace add and the install
	// even though the first call fails.
	prewarmDeclaredPlugins(instance, reporter, false)

	if len(*calls) != 2 {
		t.Errorf("expected 2 attempts despite failures, got %d: %v", len(*calls), *calls)
	}
	if !strings.Contains(buf.String(), "pre-warming") {
		t.Errorf("expected a warning mentioning pre-warming, got %q", buf.String())
	}
}

func TestPrewarm_NilReporterDoesNotPanic(t *testing.T) {
	instance := writeInstanceSettings(t, `{
	  "extraKnownMarketplaces": {"shirabe": {"source": {"source": "github", "repo": "tsukumogami/shirabe"}}}
	}`)
	recordPluginCalls(t, errors.New("boom"))

	// A nil reporter (allowed by the seam contract) must not panic on warning.
	prewarmDeclaredPlugins(instance, nil, false)
}

// TestConfigurePluginAutoInstall_WiresPrewarm guards the load-bearing wiring: if the
// PrewarmDeclaredPlugins seam is left nil, the provisioning pipeline silently skips
// pre-warming and the whole fix becomes a no-op. It must be wired for every Applier
// the cli constructs.
func TestConfigurePluginAutoInstall_WiresPrewarm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from the host's global config
	var applier workspace.Applier
	configurePluginAutoInstall(&applier, false)

	if applier.PrewarmDeclaredPlugins == nil {
		t.Error("configurePluginAutoInstall must wire PrewarmDeclaredPlugins (nil would no-op the fix)")
	}
	if applier.SkipPluginInstall {
		t.Error("SkipPluginInstall should be false with flagOptOut=false and no global opt-out")
	}
}

// TestIsDeclaredSourceConflict covers each wording Claude Code has used to refuse
// a marketplace add whose source differs from the declared one, plus failures
// that must not be mistaken for that refusal.
func TestIsDeclaredSourceConflict(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"older wording", `Cannot add marketplace "x": its network source differs from the one declared for it in settings`, true},
		{"extraKnownMarketplaces wording", `Cannot add marketplace "x": its source doesn't match its extraKnownMarketplaces entry in user or managed settings`, true},
		{"typographic apostrophe", "Cannot add marketplace \"x\": its source doesn\u2019t match its extraKnownMarketplaces entry", true},
		{"network failure", "exit status 128: could not resolve host", false},
		{"different casing is not matched", "its source DOESN'T MATCH ITS EXTRAKNOWNMARKETPLACES ENTRY", false},
		{"unrelated mismatch", "checksum doesn't match the expected value", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeclaredSourceConflict(errors.New(tc.msg)); got != tc.want {
				t.Errorf("isDeclaredSourceConflict(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}
