//go:build !niwa_functional

package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/testhook"
)

func TestBorrowReleased_FalseInAReleaseBuild(t *testing.T) {
	if borrowReleased {
		t.Fatal("borrowReleased is true in a build without the niwa_functional tag")
	}
}

// No setting a user can write turns borrowing on in a release build.
func TestBorrowEnabled_FalseForEveryComboInAReleaseBuild(t *testing.T) {
	for _, c := range shareGitHistoryCombos() {
		cfg := comboConfig(c.repo, c.ws)
		for _, repo := range []string{"app", "unnamed"} {
			if borrowEnabled(cfg, repo) {
				t.Errorf("borrowEnabled(%s) with repo=%s workspace=%s is true in a release build",
					repo, sghState(c.repo), sghState(c.ws))
			}
		}
	}
	if borrowEnabled(nil, "app") {
		t.Error("borrowEnabled with a nil config is true in a release build")
	}
}

// A create in a release build makes full clones and seeds no mirror, both
// with share_git_history set nowhere and with it set to true at both levels.
func TestReleaseBuild_CreateNeitherBorrowsNorSeedsAMirror(t *testing.T) {
	sb := newBorrowSandbox(t)
	for name, extra := range map[string]string{
		"unset": "",
		"on at both levels": "share_git_history = true\n\n" +
			"[repos.app]\nshare_git_history = true\n\n" +
			"[repos.lib]\nshare_git_history = true\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := sb.create(t, extra)
			if c.err != nil {
				t.Fatalf("Create: %v", c.err)
			}
			for _, repo := range borrowRepos {
				if borrowEnabled(c.cfg, repo) {
					t.Errorf("borrowEnabled(%s) is true in a release build", repo)
				}
			}
			c.requireNoBorrowing(t)
		})
	}
}

// The three test-build variables do nothing in a release build: a create
// with them set makes the same clones and succeeds as one without them, and
// writes no probe file.
func TestReleaseBuild_TheTestHookVariablesDoNotChangeACreate(t *testing.T) {
	sb := newBorrowSandbox(t)
	extra := "share_git_history = true\n"

	plain := sb.create(t, extra)
	if plain.err != nil {
		t.Fatalf("Create without the variables: %v", plain.err)
	}
	want := plain.clones(t)

	probe := filepath.Join(t.TempDir(), "git-env-probe.jsonl")
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+testhook.LabelBorrowClone)
	t.Setenv("NIWA_TEST_MIRROR_LOCK_WAIT", "1ms")
	t.Setenv("NIWA_TEST_GIT_ENV_PROBE", probe)

	hooked := sb.create(t, extra)
	if hooked.err != nil {
		t.Fatalf("Create with the variables: %v, want it to succeed as it does without them", hooked.err)
	}
	if got := hooked.clones(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("clones with the variables = %v, want %v as without them", got, want)
	}
	hooked.requireNoBorrowing(t)
	if _, err := os.Lstat(probe); !os.IsNotExist(err) {
		t.Fatalf("a release build wrote the probe file %s (%v)", probe, err)
	}
}
