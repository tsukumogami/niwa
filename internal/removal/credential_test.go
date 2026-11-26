package removal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// credentialEnv is a pass environment whose home holds globalConfig as its
// .gitconfig, and include as a file the global one may include.
func credentialEnv(t *testing.T, globalConfig, include string) *Env {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home, root := newScratchFixture(t)
	if globalConfig != "" {
		writeFile(t, filepath.Join(home, ".gitconfig"), globalConfig)
	}
	if include != "" {
		writeFile(t, filepath.Join(home, "included.gitconfig"), include)
	}
	e := NewEnv(root, home, os.Getenv("PATH"))
	t.Cleanup(e.Close)
	return e
}

func TestCredentialHelpersRead(t *testing.T) {
	cases := []struct {
		name, global, include string
		want                  []string
	}{
		{"no global file", "", "", nil},
		{"no helper", "[user]\n\tname = dana\n", "", nil},
		{"one helper", "[credential]\n\thelper = osxkeychain\n", "", []string{"osxkeychain"}},
		{"several with a reset", "[credential]\n\thelper = a\n\thelper =\n\thelper = b\n\thelper = c --flag\n", "", []string{"b", "c --flag"}},
		{"scoped to a URL", "[credential \"https://github.com\"]\n\thelper = scoped\n", "", nil},
		{"only in an included file", "[include]\n\tpath = included.gitconfig\n", "[credential]\n\thelper = included\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := credentialEnv(t, c.global, c.include)
			if e.credErr != nil {
				t.Fatalf("read failed: %v", e.credErr)
			}
			if !slices.Equal(e.credHelpers, c.want) {
				t.Fatalf("helpers %q, want %q", e.credHelpers, c.want)
			}
		})
	}
}

func TestCredentialReadRunsWithItsOwnEnvironment(t *testing.T) {
	e := &Env{Root: "/ws", Home: "/h", PATH: "/usr/bin", Git: "/usr/bin/git", Scratch: &Scratch{Dir: "/h/.niwa/w-x/scratch/p"}}
	env := e.credentialReadEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") {
			t.Fatalf("the read hides the global file: %q", env)
		}
	}
	for _, want := range []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES=/ws:/h/.niwa/w-x/scratch/p", "HOME=/h"} {
		if !slices.Contains(env, want) {
			t.Errorf("the read's environment lacks %s: %q", want, env)
		}
	}
}

func TestACredentialReadThatFailsKeepsEveryUnit(t *testing.T) {
	e := credentialEnv(t, "", "")
	// The directory the read runs from fails its check: the read refuses
	// to run, and the failure keeps a unit with nothing in it at all.
	if err := os.Chmod(filepath.Join(e.Scratch.Dir, scratchHelperRead), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := e.readCredentialHelpers(context.Background())
	if err == nil {
		t.Fatal("the read ran from a directory with mode 755")
	}
	e.credErr = err
	instance := filepath.Join(e.Root, "ws-1")
	if err := os.MkdirAll(filepath.Join(instance, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), Unit{InstancePath: instance, Liveness: LivenessGone}, Policy{Env: e, Processes: &Listing{}})
	first, ok := v.First()
	if v.Kind != Keep || !ok || first.Name != CheckRepository || !strings.Contains(first.Evidence, "credential-helper read failed") {
		t.Fatalf("verdict %s %+v; want every unit kept on the failed read", v.Kind, v.Checks)
	}
}

func TestCredentialReadIgnoresARepositoryWhereItRuns(t *testing.T) {
	e := credentialEnv(t, "", "")
	// A repository planted above the read's directory names a helper; the
	// ceiling and the empty directory keep the read from finding it, and
	// --global reads only the global file anyway.
	gitRun(t, e.Scratch.Dir, "init", "-q")
	gitRun(t, e.Scratch.Dir, "config", "credential.helper", "planted")
	helpers, err := e.readCredentialHelpers(context.Background())
	if err != nil || len(helpers) != 0 {
		t.Fatalf("helpers %q, err %v; want none", helpers, err)
	}
}
