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
// source+repo (+ref, which pre-warming ignores), directory sources carry
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
		{instance, "marketplace", "add", "tsukumogami/shirabe"},
		{instance, "install", "shirabe@shirabe", "--scope", "local"},
		{instance, "install", "tsukumogami@tsukumogami", "--scope", "local"},
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Errorf("calls =\n  %v\nwant\n  %v", *calls, want)
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
// pre-warming and the whole fix becomes a no-op. It must be wired alongside
// InstallNiwaPlugin for every Applier the cli constructs.
func TestConfigurePluginAutoInstall_WiresPrewarm(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate from the host's global config
	var applier workspace.Applier
	configurePluginAutoInstall(&applier, false)

	if applier.PrewarmDeclaredPlugins == nil {
		t.Error("configurePluginAutoInstall must wire PrewarmDeclaredPlugins (nil would no-op the fix)")
	}
	if applier.InstallNiwaPlugin == nil {
		t.Error("configurePluginAutoInstall must still wire InstallNiwaPlugin")
	}
	if applier.SkipPluginInstall {
		t.Error("SkipPluginInstall should be false with flagOptOut=false and no global opt-out")
	}
}
