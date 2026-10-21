package cli

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
)

// TestConfigSetAuthorityLevel_WritesUnderGlobal is the main path: the value
// lands under [global] in $XDG_CONFIG_HOME/niwa/config.toml, the developer's
// other keys survive the read-modify-write, and the output names the file.
func TestConfigSetAuthorityLevel_WritesUnderGlobal(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_permission_mode = \"auto\"\n")

	out, err := runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{"strict"})
	if err != nil {
		t.Fatalf("config set authority-level strict: %v", err)
	}
	if want := "Authority level set to strict in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	body := readHostConfig(t, path)
	global := strings.Index(body, "[global]")
	key := strings.Index(body, `authority_level = "strict"`)
	if global < 0 || key < global {
		t.Errorf("%s does not carry authority_level = \"strict\" under [global]:\n%s", path, body)
	}
	if !strings.Contains(body, `dispatch_permission_mode = "auto"`) {
		t.Errorf("%s lost dispatch_permission_mode:\n%s", path, body)
	}

	loaded, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatalf("loading the host config back: %v", err)
	}
	got, err := loaded.AuthorityLevel()
	if err != nil || got == nil || *got != authority.Strict {
		t.Fatalf("AuthorityLevel() after set = %v, %v; want strict", got, err)
	}
}

// TestConfigSetAuthorityLevel_CreatesTheFileAndTrims covers a machine that has
// never had a niwa config, and a value typed with surrounding whitespace.
func TestConfigSetAuthorityLevel_CreatesTheFileAndTrims(t *testing.T) {
	path := permissionModeConfigPath(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s already exists (err=%v)", path, err)
	}

	out, err := runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{"  supervised "})
	if err != nil {
		t.Fatalf("config set authority-level supervised: %v", err)
	}
	if want := "Authority level set to supervised in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if body := readHostConfig(t, path); !strings.Contains(body, `authority_level = "supervised"`) {
		t.Errorf("%s does not carry the trimmed value:\n%s", path, body)
	}
}

// TestConfigSetAuthorityLevel_RejectsInvalidWithoutWriting holds the file
// byte-identical when the value is outside the three names, including a case
// mismatch, and the command exits non-zero naming the key, the value and the
// accepted names.
func TestConfigSetAuthorityLevel_RejectsInvalidWithoutWriting(t *testing.T) {
	for _, arg := range []string{"loose", "Strict", "STRICT", "bypass"} {
		t.Run(arg, func(t *testing.T) {
			path := permissionModeConfigPath(t)
			seed := "[global]\ndispatch_model = \"opus\"\nauthority_level = \"supervised\"\n"
			writeHostConfig(t, path, seed)

			out, err := runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{arg})
			if err == nil {
				t.Fatalf("config set authority-level %s succeeded, want a rejection", arg)
			}
			if out != "" {
				t.Errorf("a rejected value printed to stdout: %q", out)
			}
			var ie *config.InvalidAuthorityLevelError
			if !errors.As(err, &ie) {
				t.Errorf("rejection %T is not an *InvalidAuthorityLevelError", err)
			}
			msg := err.Error()
			for _, part := range append([]string{"[global] authority_level", strconv.Quote(arg)}, authority.Names()...) {
				if !strings.Contains(msg, part) {
					t.Errorf("rejection %q does not name %q", msg, part)
				}
			}
			if got := readHostConfig(t, path); got != seed {
				t.Fatalf("the rejected value rewrote %s:\nbefore: %q\nafter:  %q", path, seed, got)
			}
		})
	}
}

// TestConfigSetAuthorityLevel_RejectsEmptyArgument keeps a scripted
// `niwa config set authority-level "$LEVEL"` with LEVEL unset from reporting
// success, and leaves no file behind on a machine that had none.
func TestConfigSetAuthorityLevel_RejectsEmptyArgument(t *testing.T) {
	for _, arg := range []string{"", "   "} {
		t.Run("arg="+strconv.Quote(arg), func(t *testing.T) {
			path := permissionModeConfigPath(t)
			_, err := runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{arg})
			if err == nil {
				t.Fatalf("config set authority-level %q succeeded, want a rejection", arg)
			}
			if !strings.Contains(err.Error(), "niwa config unset authority-level") {
				t.Errorf("rejection does not name the command that clears the setting: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("%s exists after a rejected empty argument (err=%v)", path, err)
			}
		})
	}
}

// TestConfigUnsetAuthorityLevel_RemovesTheKeyAndKeepsOthers covers the way
// back out, and a second unset with nothing left to remove.
func TestConfigUnsetAuthorityLevel_RemovesTheKeyAndKeepsOthers(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_model = \"opus\"\nauthority_level = \"strict\"\n")

	out, err := runConfigDefaultHarness(t, runConfigUnsetAuthorityLevel, nil)
	if err != nil {
		t.Fatalf("config unset authority-level: %v", err)
	}
	if want := "Authority level removed.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	body := readHostConfig(t, path)
	if strings.Contains(body, "authority_level") {
		t.Errorf("%s still carries authority_level:\n%s", path, body)
	}
	if !strings.Contains(body, `dispatch_model = "opus"`) {
		t.Errorf("%s lost dispatch_model:\n%s", path, body)
	}

	out, err = runConfigDefaultHarness(t, runConfigUnsetAuthorityLevel, nil)
	if err != nil {
		t.Fatalf("second config unset authority-level: %v", err)
	}
	if want := "No authority level set.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

// TestConfigUnsetAuthorityLevel_AbsentExitsZero covers unset on a machine with
// no config file at all; it must not create one.
func TestConfigUnsetAuthorityLevel_AbsentExitsZero(t *testing.T) {
	path := permissionModeConfigPath(t)
	out, err := runConfigDefaultHarness(t, runConfigUnsetAuthorityLevel, nil)
	if err != nil {
		t.Fatalf("config unset authority-level with nothing set: %v", err)
	}
	if want := "No authority level set.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unset with nothing set created %s (err=%v)", path, err)
	}
}

// TestConfigUnsetAuthorityLevel_ClearsAnInvalidValue keeps unset usable as the
// way out of a value that would stop every dispatch.
func TestConfigUnsetAuthorityLevel_ClearsAnInvalidValue(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\nauthority_level = \"STRICT\"\n")

	out, err := runConfigDefaultHarness(t, runConfigUnsetAuthorityLevel, nil)
	if err != nil {
		t.Fatalf("config unset authority-level over an invalid value: %v", err)
	}
	if want := "Authority level removed.\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if body := readHostConfig(t, path); strings.Contains(body, "STRICT") {
		t.Errorf("%s still carries the invalid value:\n%s", path, body)
	}
}

// TestConfigAuthorityLevelSubcommandsAreRegistered pins the surface a
// developer types, including the TOML spelling as an alias.
func TestConfigAuthorityLevelSubcommandsAreRegistered(t *testing.T) {
	for _, tc := range []struct {
		parent *cobra.Command
		name   string
	}{
		{configSetCmd, "set"},
		{configUnsetCmd, "unset"},
	} {
		var found *cobra.Command
		for _, sub := range tc.parent.Commands() {
			if sub.Name() == "authority-level" {
				found = sub
			}
		}
		if found == nil {
			t.Fatalf("niwa config %s has no authority-level subcommand", tc.name)
		}
		if !found.HasAlias("authority_level") {
			t.Errorf("niwa config %s authority-level does not accept the TOML spelling authority_level", tc.name)
		}
	}
}

// TestConfigAuthorityLevelHelpListsLevelsAndOrder pins what the long help must
// tell a reader before they pick a value.
func TestConfigAuthorityLevelHelpListsLevelsAndOrder(t *testing.T) {
	long := configSetAuthorityLevelCmd.Long
	for _, name := range authority.Names() {
		if !strings.Contains(long, name) {
			t.Errorf("long help does not list %q", name)
		}
	}
	for _, part := range []string{"[workspace].authority_level", "[global].authority_level", "--authority", "narrowest"} {
		if !strings.Contains(long, part) {
			t.Errorf("long help does not mention %q", part)
		}
	}
}
