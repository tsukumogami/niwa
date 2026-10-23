package cli

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestConfigSetAuthorityLevel(t *testing.T) {
	path := permissionModeConfigPath(t)
	writeHostConfig(t, path, "[global]\ndispatch_model = \"opus\"\n")

	out, err := runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{" strict "})
	if err != nil {
		t.Fatalf("config set authority-level strict: %v", err)
	}
	if want := "Authority level set to strict in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	body := readHostConfig(t, path)
	if !strings.Contains(body, `authority_level = "strict"`) || !strings.Contains(body, `dispatch_model = "opus"`) {
		t.Errorf("config:\n%s", body)
	}

	if _, err := runConfigDefaultHarness(t, configUnsetAuthorityLevelCmd.RunE, nil); err != nil {
		t.Fatal(err)
	}
	if body := readHostConfig(t, path); strings.Contains(body, "authority_level") {
		t.Errorf("unset left the key:\n%s", body)
	}
	out, err = runConfigDefaultHarness(t, configUnsetAuthorityLevelCmd.RunE, nil)
	if err != nil || out != "No authority level set.\n" {
		t.Errorf("second unset: %q, %v", out, err)
	}
}

// TestConfigSetAuthority_RefusalsLeaveTheFile: a bad value exits non-zero
// with the accepted values and the file is byte-identical afterwards.
func TestConfigSetAuthority_RefusalsLeaveTheFile(t *testing.T) {
	path := permissionModeConfigPath(t)
	const original = "# hand-written\n[global]\nauthority_ceiling = [\"merge-pr\"]\n"
	cases := []struct {
		name string
		run  func(*testing.T) (string, error)
		want string
	}{
		{"bogus level", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{"bogus"})
		}, "autonomous, supervised, strict"},
		{"blank level", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityLevel, []string{"  "})
		}, "niwa config unset authority-level"},
		{"unknown authority", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"merge-pr,nope"})
		}, "merge-pr, close-issue, cut-release"},
		{"authority a newer niwa knows", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"teardown"})
		}, `"teardown"`},
		{"trailing empty entry", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"merge-pr,"})
		}, `empty entry in "merge-pr,"`},
		{"leading empty entry", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{",merge-pr"})
		}, `empty entry in ",merge-pr"`},
		{"none among others", func(t *testing.T) (string, error) {
			return runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"none,merge-pr"})
		}, `"none"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeHostConfig(t, path, original)
			_, err := c.run(t)
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q lacks %q", err, c.want)
			}
			if body := readHostConfig(t, path); body != original {
				t.Errorf("file changed:\n%s", body)
			}
		})
	}
}

func TestConfigSetAuthorityCeiling(t *testing.T) {
	path := permissionModeConfigPath(t)

	out, err := runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"cut-release, merge-pr", " merge-pr "})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Authority ceiling set to merge-pr, cut-release in " + path + "\n"; out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if body := readHostConfig(t, path); !strings.Contains(body, `authority_ceiling = ["merge-pr", "cut-release"]`) {
		t.Errorf("config:\n%s", body)
	}

	if _, err := runConfigDefaultHarness(t, configUnsetAuthorityCeilingCmd.RunE, nil); err != nil {
		t.Fatal(err)
	}
	if body := readHostConfig(t, path); strings.Contains(body, "authority_ceiling") {
		t.Errorf("unset left the key:\n%s", body)
	}
}

// TestConfigSetAuthorityCeiling_NoneSurvivesARewrite is the empty-list round
// trip: `none` writes an empty list, and an unrelated `config set` keeps it.
func TestConfigSetAuthorityCeiling_NoneSurvivesARewrite(t *testing.T) {
	path := permissionModeConfigPath(t)
	if _, err := runConfigDefaultHarness(t, runConfigSetAuthorityCeiling, []string{"none"}); err != nil {
		t.Fatal(err)
	}
	if body := readHostConfig(t, path); !strings.Contains(body, "authority_ceiling = []") {
		t.Fatalf("config:\n%s", body)
	}
	if _, err := runConfigDefaultHarness(t, runConfigSetPermissionMode, []string{"auto"}); err != nil {
		t.Fatal(err)
	}
	if body := readHostConfig(t, path); !strings.Contains(body, "authority_ceiling = []") {
		t.Fatalf("an unrelated config set dropped the empty ceiling:\n%s", body)
	}
	g, err := config.LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	c, err := g.AuthorityCeiling()
	if err != nil || c == nil || len(*c) != 0 {
		t.Errorf("AuthorityCeiling = %v, %v; want present and empty", c, err)
	}
}
