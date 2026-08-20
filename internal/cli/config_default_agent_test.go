package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// runConfigDefaultAgent invokes one of the two default-agent subcommands with a
// fresh cobra.Command so its output streams are isolated.
func runConfigDefaultAgent(t *testing.T, run func(*cobra.Command, []string) error, args []string) (stdout string, err error) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	runErr := run(cmd, args)
	return out.String(), runErr
}

// TestConfigSetDefaultAgent_WritesTheHostConfig is the point of the command:
// the value lands in the developer's own config file, which niwa never
// re-materializes from anywhere. A workspace's .niwa/ is frequently a snapshot
// replaced wholesale on refresh, so a setting written there would go away
// without saying so.
func TestConfigSetDefaultAgent_WritesTheHostConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"codex"}); err != nil {
		t.Fatalf("config set default-agent codex: %v", err)
	}

	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("loading the host config back: %v", err)
	}
	if got := loaded.DefaultAgent(); got != "codex" {
		t.Fatalf("host default_agent = %q, want %q", got, "codex")
	}

	// And it survives a re-read as TOML rather than only in memory.
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if !strings.Contains(string(body), "default_agent") {
		t.Fatalf("%s does not carry default_agent:\n%s", path, body)
	}
}

// TestConfigSetDefaultAgent_WritesNothingInsideAWorkspaceSnapshot is the
// failure this command exists to avoid: a setting that survives the command and
// not the next apply. A workspace's .niwa/ is materialized from a source repo
// and replaced wholesale on refresh, so the command must leave it alone --
// including when it is run from inside a workspace.
func TestConfigSetDefaultAgent_WritesNothingInsideAWorkspaceSnapshot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	chdir(t, root)

	stateDir := filepath.Join(root, config.ConfigDir)
	before, err := snapshotDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"codex"}); err != nil {
		t.Fatalf("config set default-agent codex: %v", err)
	}

	after, err := snapshotDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the command wrote inside the workspace's %s:\nbefore: %v\nafter:  %v", config.ConfigDir, before, after)
	}

	// And the file it did write is outside the workspace entirely.
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, root) {
		t.Fatalf("the host config path %s is inside the workspace root %s", path, root)
	}
}

// snapshotDir records every file under dir with its contents, so a test can
// assert nothing beneath it changed.
func snapshotDir(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		out[path] = string(body)
		return nil
	})
	return out, err
}

// TestConfigSetDefaultAgent_ReportsWhereItWrote holds the one thing a developer
// needs from the output. The whole reason this command exists is that guessing
// which file holds the setting is the trap, so the file it wrote is named.
func TestConfigSetDefaultAgent_ReportsWhereItWrote(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}

	out, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"codex"})
	if err != nil {
		t.Fatalf("config set default-agent codex: %v", err)
	}
	if !strings.Contains(out, path) {
		t.Errorf("output does not name the file it wrote (%s):\n%s", path, out)
	}
	// A machine-wide setting that a workspace outranks has to say so here, or
	// the developer reads "set to codex" and then watches claude launch.
	if !strings.Contains(out, "default_agent") {
		t.Errorf("output does not mention that a workspace default_agent still wins:\n%s", out)
	}
}

// TestConfigSetDefaultAgent_RejectsUnknownAgentWithoutWriting keeps the command
// on the same closed set as every other source, and keeps a typo from leaving a
// value behind that only fails later, at the next dispatch.
func TestConfigSetDefaultAgent_RejectsUnknownAgentWithoutWriting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"gemini"})
	if err == nil {
		t.Fatal("config set default-agent gemini succeeded, want a rejection")
	}
	for _, want := range agent.All() {
		if !strings.Contains(err.Error(), string(want)) {
			t.Errorf("rejection %q does not name the accepted value %q", err, want)
		}
	}

	loaded, lErr := config.LoadGlobalConfig()
	if lErr != nil {
		t.Fatalf("loading the host config back: %v", lErr)
	}
	if got := loaded.DefaultAgent(); got != "" {
		t.Fatalf("a rejected value was written anyway: default_agent = %q", got)
	}
}

// TestConfigSetDefaultAgent_PreservesOtherSettings guards the read-modify-write.
// The host config holds several unrelated dispatch defaults, and setting one
// must not drop the rest.
func TestConfigSetDefaultAgent_PreservesOtherSettings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	globalCfg.Global.DispatchModel = "opus"
	globalCfg.GlobalConfig = config.GlobalConfigSource{Repo: "myorg/my-config"}
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobalConfigTo(path, globalCfg); err != nil {
		t.Fatal(err)
	}

	if _, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"codex"}); err != nil {
		t.Fatalf("config set default-agent codex: %v", err)
	}

	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Global.DispatchModel != "opus" {
		t.Errorf("dispatch_model = %q, want it preserved as %q", loaded.Global.DispatchModel, "opus")
	}
	if loaded.GlobalConfig.Repo != "myorg/my-config" {
		t.Errorf("global_config.repo = %q, want it preserved", loaded.GlobalConfig.Repo)
	}
	if loaded.DefaultAgent() != "codex" {
		t.Errorf("default_agent = %q, want codex", loaded.DefaultAgent())
	}
}

// TestConfigUnsetDefaultAgent_ClearsTheSetting covers the way back out.
func TestConfigUnsetDefaultAgent_ClearsTheSetting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := runConfigDefaultAgent(t, runConfigSetDefaultAgent, []string{"codex"}); err != nil {
		t.Fatalf("config set default-agent codex: %v", err)
	}
	if _, err := runConfigDefaultAgent(t, runConfigUnsetDefaultAgent, nil); err != nil {
		t.Fatalf("config unset default-agent: %v", err)
	}

	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.DefaultAgent(); got != "" {
		t.Fatalf("default_agent = %q after unset, want it cleared", got)
	}
}

// TestConfigUnsetDefaultAgent_SaysNothingWasSet keeps the no-op path quiet and
// successful rather than an error, matching `config unset global`.
func TestConfigUnsetDefaultAgent_SaysNothingWasSet(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, err := runConfigDefaultAgent(t, runConfigUnsetDefaultAgent, nil)
	if err != nil {
		t.Fatalf("config unset default-agent with nothing set: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "no machine-wide default agent") {
		t.Errorf("output does not say nothing was set:\n%s", out)
	}
}

// TestConfigDefaultAgentSubcommandsAreRegistered pins the surface a developer
// actually types, including the TOML spelling as an alias.
func TestConfigDefaultAgentSubcommandsAreRegistered(t *testing.T) {
	for _, tc := range []struct {
		parent *cobra.Command
		name   string
	}{
		{configSetCmd, "set"},
		{configUnsetCmd, "unset"},
	} {
		var found *cobra.Command
		for _, sub := range tc.parent.Commands() {
			if sub.Name() == "default-agent" {
				found = sub
			}
		}
		if found == nil {
			t.Fatalf("niwa config %s has no default-agent subcommand", tc.name)
		}
		if !found.HasAlias("default_agent") {
			t.Errorf("niwa config %s default-agent does not accept the TOML spelling default_agent", tc.name)
		}
	}
}
