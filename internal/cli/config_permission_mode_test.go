package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

// permissionModeConfigPath points XDG_CONFIG_HOME at a fresh temp directory and
// returns the host config path the setter will write.
func permissionModeConfigPath(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// writeHostConfig seeds the host config with raw TOML.
func writeHostConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readHostConfig(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// TestConfigSetPermissionMode_KeepsOtherGlobalKeys is the main path: the value
// lands under [global], the developer's other dispatch defaults survive the
// read-modify-write, and the output names the file.
func TestConfigSetPermissionMode_KeepsOtherGlobalKeys(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_model = \"opus\"\ndefault_dispatch_harness = \"codex\"\n")

	out, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{"auto"})
	if err != nil {
		t.Fatalf("config set dispatch-permission-mode auto: %v", err)
	}
	if want := "Dispatch permission mode set to auto in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	body := readHostConfig(t, path)
	if !strings.Contains(body, `dispatch_permission_mode = "auto"`) {
		t.Errorf("%s does not carry dispatch_permission_mode = \"auto\":\n%s", path, body)
	}
	for _, kept := range []string{`dispatch_model = "opus"`, `default_dispatch_harness = "codex"`} {
		if !strings.Contains(body, kept) {
			t.Errorf("%s lost %s:\n%s", path, kept, body)
		}
	}
}

// TestConfigSetPermissionMode_CreatesTheFile covers a machine that has never
// had a niwa config.
func TestConfigSetPermissionMode_CreatesTheFile(t *testing.T) {
	path := permissionModeConfigPath(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s already exists (err=%v)", path, err)
	}

	out, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{"bypassPermissions"})
	if err != nil {
		t.Fatalf("config set dispatch-permission-mode bypassPermissions: %v", err)
	}
	if want := "Dispatch permission mode set to bypassPermissions in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	body := readHostConfig(t, path)
	if !strings.Contains(body, "[global]") || !strings.Contains(body, `dispatch_permission_mode = "bypassPermissions"`) {
		t.Errorf("%s does not carry the setting under [global]:\n%s", path, body)
	}
}

// TestConfigSetPermissionMode_SecondSetReplaces checks a later set replaces the
// earlier value rather than adding a second key, and that the value is trimmed.
func TestConfigSetPermissionMode_SecondSetReplaces(t *testing.T) {
	path := permissionModeConfigPath(t)

	if _, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{"auto"}); err != nil {
		t.Fatalf("first set: %v", err)
	}
	out, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{"  plan  "})
	if err != nil {
		t.Fatalf("second set: %v", err)
	}
	if want := "Dispatch permission mode set to plan in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	body := readHostConfig(t, path)
	if n := strings.Count(body, "dispatch_permission_mode"); n != 1 {
		t.Errorf("%s carries dispatch_permission_mode %d times, want once:\n%s", path, n, body)
	}
	if !strings.Contains(body, `dispatch_permission_mode = "plan"`) || strings.Contains(body, `"auto"`) {
		t.Errorf("%s was not replaced with plan:\n%s", path, body)
	}
}

// TestConfigSetPermissionMode_RejectsInvalidWithoutWriting holds the file
// byte-identical when the value is outside the accepted set, including a case
// mismatch, and carries the accessor's error naming every accepted value.
func TestConfigSetPermissionMode_RejectsInvalidWithoutWriting(t *testing.T) {
	for _, arg := range []string{"bogus", "Auto", "bypass"} {
		t.Run(arg, func(t *testing.T) {
			path := permissionModeConfigPath(t)
			seed := "[global]\ndispatch_model = \"opus\"\ndispatch_permission_mode = \"default\"\n"
			writeHostConfig(t, path, seed)

			out, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{arg})
			if err == nil {
				t.Fatalf("config set dispatch-permission-mode %s succeeded, want a rejection", arg)
			}
			if out != "" {
				t.Errorf("a rejected value printed to stdout: %q", out)
			}
			msg := err.Error()
			if !strings.Contains(msg, "dispatch_permission_mode") || !strings.Contains(msg, strconv.Quote(arg)) {
				t.Errorf("rejection %q does not name the key and the value %q", msg, arg)
			}
			for _, m := range config.DispatchPermissionModes {
				if !strings.Contains(msg, m) {
					t.Errorf("rejection %q does not name the accepted value %q", msg, m)
				}
			}
			if got := readHostConfig(t, path); got != seed {
				t.Fatalf("the rejected value rewrote %s:\nbefore: %q\nafter:  %q", path, seed, got)
			}
		})
	}
}

// TestConfigSetPermissionMode_RejectsEmptyArgument keeps a scripted
// `niwa config set dispatch-permission-mode "$MODE"` with MODE unset from
// reporting success, and points at unset as the way to clear the setting.
func TestConfigSetPermissionMode_RejectsEmptyArgument(t *testing.T) {
	for _, arg := range []string{"", "   ", "\t\n"} {
		t.Run("arg="+strconv.Quote(arg), func(t *testing.T) {
			path := permissionModeConfigPath(t)
			seed := "[global]\ndispatch_permission_mode = \"auto\"\n"
			writeHostConfig(t, path, seed)

			_, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{arg})
			if err == nil {
				t.Fatalf("config set dispatch-permission-mode %q succeeded, want a rejection", arg)
			}
			if !strings.Contains(err.Error(), "niwa config unset dispatch-permission-mode") {
				t.Errorf("rejection does not name the command that clears the setting: %v", err)
			}
			if got := readHostConfig(t, path); got != seed {
				t.Fatalf("the empty argument rewrote %s:\nbefore: %q\nafter:  %q", path, seed, got)
			}
		})
	}
}

// TestConfigSetPermissionMode_RejectsEmptyArgumentWithoutCreatingFile checks
// the empty case leaves no file behind on a machine that had none.
func TestConfigSetPermissionMode_RejectsEmptyArgumentWithoutCreatingFile(t *testing.T) {
	path := permissionModeConfigPath(t)
	if _, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{""}); err == nil {
		t.Fatal("config set dispatch-permission-mode \"\" succeeded, want a rejection")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists after a rejected empty argument (err=%v)", path, err)
	}
}

// TestConfigUnsetPermissionMode_RemovesTheKeyAndKeepsOthers covers the way
// back out.
func TestConfigUnsetPermissionMode_RemovesTheKeyAndKeepsOthers(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_model = \"opus\"\ndispatch_permission_mode = \"auto\"\n")

	out, err := runConfigDefaultHarness(t, runConfigUnsetPermissionMode, nil)
	if err != nil {
		t.Fatalf("config unset dispatch-permission-mode: %v", err)
	}
	if want := "Dispatch permission mode removed.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	body := readHostConfig(t, path)
	if strings.Contains(body, "dispatch_permission_mode") {
		t.Errorf("%s still carries dispatch_permission_mode:\n%s", path, body)
	}
	if !strings.Contains(body, `dispatch_model = "opus"`) {
		t.Errorf("%s lost dispatch_model:\n%s", path, body)
	}

	// Run again with the key now absent: success, and it says so.
	out, err = runConfigDefaultHarness(t, runConfigUnsetPermissionMode, nil)
	if err != nil {
		t.Fatalf("second config unset dispatch-permission-mode: %v", err)
	}
	if want := "No dispatch permission mode set.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// TestConfigUnsetPermissionMode_AbsentExitsZero covers unset on a machine with
// no config file at all.
func TestConfigUnsetPermissionMode_AbsentExitsZero(t *testing.T) {
	path := permissionModeConfigPath(t)

	out, err := runConfigDefaultHarness(t, runConfigUnsetPermissionMode, nil)
	if err != nil {
		t.Fatalf("config unset dispatch-permission-mode with nothing set: %v", err)
	}
	if want := "No dispatch permission mode set.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unset with nothing set created %s (err=%v)", path, err)
	}
}

// TestConfigUnsetPermissionMode_ClearsAnInvalidValue keeps unset usable as the
// way out of a value that is stopping every dispatch.
func TestConfigUnsetPermissionMode_ClearsAnInvalidValue(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_permission_mode = \"atuo\"\n")

	out, err := runConfigDefaultHarness(t, runConfigUnsetPermissionMode, nil)
	if err != nil {
		t.Fatalf("config unset dispatch-permission-mode over an invalid value: %v", err)
	}
	if want := "Dispatch permission mode removed.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if body := readHostConfig(t, path); strings.Contains(body, "atuo") {
		t.Errorf("%s still carries the invalid value:\n%s", path, body)
	}
}

// TestConfigPermissionModeHelpListsValuesAndPrecedence pins what the long help
// must tell a reader before they pick a value.
func TestConfigPermissionModeHelpListsValuesAndPrecedence(t *testing.T) {
	long := configSetPermissionModeCmd.Long
	for _, m := range config.DispatchPermissionModes {
		if !strings.Contains(long, m) {
			t.Errorf("long help does not list %q", m)
		}
	}
	for _, rung := range []string{"--permission-mode", "[global].dispatch_permission_mode", "bypass"} {
		if !strings.Contains(long, rung) {
			t.Errorf("long help does not describe the precedence rung %q", rung)
		}
	}
	flag := strings.Index(long, "niwa dispatch --permission-mode")
	machine := strings.Index(long, "[global].dispatch_permission_mode        this machine")
	if flag < 0 || machine < 0 || flag > machine {
		t.Errorf("long help does not list the flag above the machine setting:\n%s", long)
	}
}

// TestConfigPermissionModeSubcommandsAreRegistered pins the surface a developer
// types, including the TOML spelling as an alias.
func TestConfigPermissionModeSubcommandsAreRegistered(t *testing.T) {
	for _, tc := range []struct {
		parent *cobra.Command
		name   string
	}{
		{configSetCmd, "set"},
		{configUnsetCmd, "unset"},
	} {
		var found *cobra.Command
		for _, sub := range tc.parent.Commands() {
			if sub.Name() == "dispatch-permission-mode" {
				found = sub
			}
		}
		if found == nil {
			t.Fatalf("niwa config %s has no dispatch-permission-mode subcommand", tc.name)
		}
		if !found.HasAlias("dispatch_permission_mode") {
			t.Errorf("niwa config %s dispatch-permission-mode does not accept the TOML spelling dispatch_permission_mode", tc.name)
		}
	}
}
