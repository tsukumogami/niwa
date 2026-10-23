package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const authorityOverlayURL = "acme/ws-overlay"

type policyFixture struct {
	workspace string // body appended to a minimal workspace.toml
	overlay   string // workspace-overlay.toml body; "" means no overlay
	machine   string // config.toml body; "" means none
}

// setupPolicyWorkspace builds a workspace root with the three layers and
// changes into it.
func setupPolicyWorkspace(t *testing.T, f policyFixture) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[workspace]\nname = \"ws\"\n" + f.workspace
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if f.overlay != "" {
		if err := workspace.SaveState(root, &workspace.InstanceState{OverlayURL: authorityOverlayURL}); err != nil {
			t.Fatal(err)
		}
		dir, err := config.OverlayDir(authorityOverlayURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(f.overlay), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if f.machine != "" {
		path, err := config.GlobalConfigPath()
		if err != nil {
			t.Fatal(err)
		}
		writeHostConfig(t, path, f.machine)
	}
	t.Chdir(root)
	return root
}

func runPolicy(t *testing.T, jsonOut bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	saved := authorityPolicyJSON
	authorityPolicyJSON = jsonOut
	defer func() { authorityPolicyJSON = saved }()
	var out, errb bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	if argErr := authorityPolicyCmd.Args(cmd, args); argErr != nil {
		return "", "", argErr
	}
	runErr := runAuthorityPolicy(cmd, args)
	return out.String(), errb.String(), runErr
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var xe *exitCodeError
	if !errors.As(err, &xe) {
		t.Fatalf("error %v carries no exit code", err)
	}
	return xe.code
}

const goldenPolicyCLI = `{
  "schema": "niwa.authority.policy/v1",
  "workspace": {
    "level": "supervised",
    "ceiling": [
      "merge-pr",
      "close-issue",
      "cut-release"
    ]
  },
  "overlay": {
    "level": null,
    "ceiling": [
      "merge-pr",
      "close-issue"
    ]
  },
  "machine": {
    "level": "strict",
    "ceiling": [
      "merge-pr"
    ]
  },
  "level_ceiling": "strict",
  "level_ceiling_source": "machine",
  "grant_ceiling": [
    "merge-pr"
  ],
  "grant_ceiling_source": "machine",
  "roles": [
    {
      "name": "coordinator",
      "authorities": [
        "merge-pr",
        "close-issue",
        "cut-release"
      ],
      "outside_ceiling": [
        "close-issue",
        "cut-release"
      ]
    }
  ],
  "no_flag_dispatch": {
    "level": "strict",
    "level_source": "machine",
    "grant": []
  },
  "warnings": []
}
`

func TestAuthorityPolicy_JSONGolden(t *testing.T) {
	setupPolicyWorkspace(t, policyFixture{
		workspace: "[authority]\nlevel = \"supervised\"\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n" +
			"[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n",
		overlay: "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n",
		machine: "[global]\nauthority_level = \"strict\"\nauthority_ceiling = [\"merge-pr\"]\n",
	})
	out, stderr, err := runPolicy(t, true)
	if err != nil {
		t.Fatalf("policy --json: %v\n%s", err, stderr)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("not valid JSON:\n%s", out)
	}
	if out != goldenPolicyCLI {
		t.Errorf("document =\n%s\nwant\n%s", out, goldenPolicyCLI)
	}
	text, _, err := runPolicy(t, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"workspace: level supervised; ceiling merge-pr, close-issue, cut-release",
		"overlay:   level not set; ceiling merge-pr, close-issue",
		"machine:   level strict; ceiling merge-pr",
		"level ceiling: strict from machine",
		"grant ceiling: merge-pr from machine",
		"coordinator: merge-pr, close-issue (outside ceiling), cut-release (outside ceiling)",
		"no-flag dispatch: level strict from machine; grant none",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestAuthorityPolicy_LevelSources(t *testing.T) {
	cases := []struct {
		name string
		f    policyFixture
		want string
	}{
		{"built-in", policyFixture{}, "level ceiling: autonomous from built-in"},
		{"workspace", policyFixture{workspace: "[authority]\nlevel = \"supervised\"\n"}, "level ceiling: supervised from workspace"},
		{"overlay narrowed", policyFixture{workspace: "[authority]\nlevel = \"autonomous\"\n", overlay: "[authority]\nlevel = \"strict\"\n"}, "level ceiling: strict from overlay"},
		{"overlay wider does not widen", policyFixture{workspace: "[authority]\nlevel = \"strict\"\n", overlay: "[authority]\nlevel = \"autonomous\"\n"}, "level ceiling: strict from workspace"},
		{"machine narrowed", policyFixture{workspace: "[authority]\nlevel = \"autonomous\"\n", machine: "[global]\nauthority_level = \"supervised\"\n"}, "level ceiling: supervised from machine"},
		{"overlay ceiling narrowed", policyFixture{workspace: "[authority]\nceiling = [\"merge-pr\", \"close-issue\"]\n", overlay: "[authority]\nceiling = [\"merge-pr\", \"cut-release\"]\n"}, "grant ceiling: merge-pr from overlay"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setupPolicyWorkspace(t, c.f)
			out, _, err := runPolicy(t, false)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("output lacks %q:\n%s", c.want, out)
			}
		})
	}
}

func TestAuthorityPolicy_Warnings(t *testing.T) {
	t.Run("overlay ceiling names an authority the base lacks", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{
			workspace: "[authority]\nceiling = [\"merge-pr\"]\n",
			overlay:   "[authority]\nceiling = [\"cut-release\"]\n",
		})
		out, stderr, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 0 {
			t.Fatalf("exit %d: %v", code, err)
		}
		if strings.Count(stderr, "warning:") != 1 || !strings.Contains(stderr, "cut-release") || !strings.Contains(stderr, "overlay") {
			t.Errorf("stderr = %q", stderr)
		}
		if !strings.Contains(out, "grant ceiling: (empty) from overlay") {
			t.Errorf("cut-release became grantable:\n%s", out)
		}
	})
	t.Run("overlay roles table", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{
			workspace: "[authority]\nceiling = [\"merge-pr\"]\n",
			overlay:   "[authority.roles.x]\nauthorities = [\"merge-pr\"]\n",
		})
		out, stderr, err := runPolicy(t, true)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(stderr, "[authority.roles]") != 1 {
			t.Errorf("stderr = %q", stderr)
		}
		if strings.Contains(out, `"name": "x"`) {
			t.Errorf("the overlay role took effect:\n%s", out)
		}
	})
	t.Run("unknown names dropped", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{
			workspace: "[authority]\nceiling = [\"merge-pr\", \"teardown\"]\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"teardown\"]\n",
		})
		out, stderr, err := runPolicy(t, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(stderr, `"teardown"`) != 2 {
			t.Errorf("stderr = %q", stderr)
		}
		if !strings.Contains(out, "coordinator: merge-pr\n") {
			t.Errorf("output:\n%s", out)
		}
	})
}

func TestAuthorityPolicy_Refusals(t *testing.T) {
	t.Run("invalid value", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{workspace: "[authority]\nlevel = \"bogus\"\n"})
		_, _, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 1 {
			t.Fatalf("exit %d", code)
		}
		msg := err.Error()
		for _, want := range []string{"[authority] level", `"bogus"`, "autonomous, supervised, strict"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q lacks %q", msg, want)
			}
		}
	})
	t.Run("wrong type and unknown key", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{workspace: "[authority]\nceiling = \"merge-pr\"\nceilng = []\n"})
		_, _, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 1 || !strings.Contains(err.Error(), "[authority] ceiling") || !strings.Contains(err.Error(), `"ceilng"`) {
			t.Fatalf("exit %d: %v", code, err)
		}
	})
	t.Run("machine wrong type", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{machine: "[global]\nauthority_ceiling = \"x\"\n"})
		_, _, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 1 || !strings.Contains(err.Error(), "[global] authority_ceiling") {
			t.Fatalf("exit %d: %v", code, err)
		}
	})
	t.Run("recorded overlay missing", func(t *testing.T) {
		root := setupPolicyWorkspace(t, policyFixture{})
		if err := workspace.SaveState(root, &workspace.InstanceState{OverlayURL: authorityOverlayURL}); err != nil {
			t.Fatal(err)
		}
		_, _, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 1 || !strings.Contains(err.Error(), "niwa apply") {
			t.Fatalf("exit %d: %v", code, err)
		}
	})
	t.Run("outside a workspace", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		t.Chdir(t.TempDir())
		_, _, err := runPolicy(t, false)
		if code := exitCode(t, err); code != 1 || !strings.Contains(err.Error(), "not inside a niwa workspace") {
			t.Fatalf("exit %d: %v", code, err)
		}
	})
	t.Run("usage", func(t *testing.T) {
		setupPolicyWorkspace(t, policyFixture{})
		_, _, err := runPolicy(t, false, "extra")
		if code := exitCode(t, err); code != 2 {
			t.Fatalf("exit %d: %v", code, err)
		}
		flagErr := authorityPolicyCmd.FlagErrorFunc()(authorityPolicyCmd, errors.New("unknown flag: --bogus"))
		if code := exitCode(t, flagErr); code != 2 {
			t.Errorf("a flag error exits %d, want 2", code)
		}
	})
}

func TestAuthorityHelpListsPolicy(t *testing.T) {
	if !strings.Contains(authorityCmd.UsageString(), "policy") {
		t.Errorf("niwa authority --help does not list policy:\n%s", authorityCmd.UsageString())
	}
}
