package removal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixture is a workspace root with one instance holding one clone of a
// local bare remote, reached by file URL.
type fixture struct {
	root, home, instance, clone, remoteURL string
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{
		root: filepath.Join(base, "ws"),
		home: filepath.Join(base, "home"),
	}
	f.instance = filepath.Join(f.root, "ws-1")
	f.clone = filepath.Join(f.instance, "tools", "app")
	for _, d := range []string{f.home, filepath.Join(f.instance, "tools")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	remote := filepath.Join(base, "remote", "app.git")
	gitRun(t, base, "init", "-q", "--bare", remote)
	seed := filepath.Join(base, "seed")
	gitRun(t, base, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "README")
	gitRun(t, seed, "commit", "-q", "-m", "init")
	gitRun(t, seed, "push", "-q", remote, "main")
	f.remoteURL = "file://" + remote
	gitRun(t, base, "clone", "-q", f.remoteURL, f.clone)
	return f
}

func (f fixture) policy(t *testing.T) Policy {
	t.Helper()
	e := NewEnv(f.root, f.home, os.Getenv("PATH"), WithFileRemotes())
	t.Cleanup(e.Close)
	return Policy{
		Env:       e,
		Processes: &Listing{},
		CloneURL: func(name string) (string, bool) {
			if name == "app" {
				return f.remoteURL, true
			}
			return "", false
		},
	}
}

func (f fixture) unit() Unit {
	return Unit{Name: "ws-1", InstancePath: f.instance, Liveness: LivenessGone}
}

func TestEvaluateReapsACleanPushedClone(t *testing.T) {
	f := newFixture(t)
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	if v.Kind != Reap {
		t.Fatalf("verdict %s, checks %+v; want reap", v.Kind, v.Checks)
	}
}

func TestEvaluateKeepsAnUnpushedBranch(t *testing.T) {
	f := newFixture(t)
	gitRun(t, f.clone, "checkout", "-q", "-b", "never-pushed")
	if err := os.WriteFile(filepath.Join(f.clone, "work.txt"), []byte("w\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.clone, "add", "work.txt")
	gitRun(t, f.clone, "commit", "-q", "-m", "work")

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	first, ok := v.First()
	if v.Kind != Keep || !ok {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	if first.Name != CheckRepository || !strings.Contains(first.Evidence, "branch never-pushed") {
		t.Fatalf("first keeping check %+v; want the repository check naming the branch", first)
	}
}

func TestEvaluateKeepsAStatusEntry(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.clone, "notes.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckRepository || !strings.Contains(first.Evidence, "notes.txt") {
		t.Fatalf("verdict %s %+v; want keep naming notes.txt", v.Kind, v.Checks)
	}
}

func TestEvaluateKeepsWhenTheCloneURLIsUnreachable(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	p.CloneURL = func(string) (string, bool) { return "file:///nonexistent/app.git", true }
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || !strings.Contains(first.Evidence, "clone URL unreachable") {
		t.Fatalf("verdict %s %+v; want keep for an unreachable clone URL", v.Kind, v.Checks)
	}
}

func TestEvaluateRefusesAFileRemoteWithoutTheTestOption(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	e := NewEnv(f.root, f.home, os.Getenv("PATH"))
	t.Cleanup(e.Close)
	p.Env = e
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || !strings.Contains(first.Evidence, "clone URL unreachable") {
		t.Fatalf("verdict %s %+v; want keep: git refuses the file transport", v.Kind, v.Checks)
	}
}

func TestEvaluateKeepsARepositoryWithoutACloneURL(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	p.CloneURL = func(string) (string, bool) { return "", false }
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || first.Paths[0] != "tools/app" || !strings.Contains(first.Evidence, "no clone URL for this repository") {
		t.Fatalf("verdict %s %+v; want keep naming the repository and its missing clone URL", v.Kind, v.Checks)
	}
}

func TestEvaluateJudgesEveryGitEntry(t *testing.T) {
	f := newFixture(t)
	nested := filepath.Join(f.clone, "vendor", "lib")
	gitRun(t, f.clone, "init", "-q", nested)
	gitRun(t, nested, "commit", "-q", "--allow-empty", "-m", "nested work")
	// The outer clone ignores the nested repository, so its status is clean
	// and only the walk can find it.
	if err := os.WriteFile(filepath.Join(f.clone, ".git", "info", "exclude"), []byte("vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.instance, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	var named []string
	for _, c := range v.Checks {
		named = append(named, c.Paths...)
		if strings.Contains(c.Evidence, "does not judge") {
			t.Errorf("a git entry went unjudged: %+v", c)
		}
	}
	for _, want := range []string{".git", "tools/app/vendor/lib"} {
		found := false
		for _, n := range named {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("keeping checks %+v don't name %s", v.Checks, want)
		}
	}
}

func TestEvaluateRefusesAProgramNamingKeyAndRunsNothingThere(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	hook := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.clone, "config", "core.fsmonitor", hook)
	gitRun(t, f.clone, "config", "Credential.Helper", hook)
	if err := os.WriteFile(filepath.Join(f.clone, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	first, _ := v.First()
	if v.Kind != Keep || !strings.Contains(first.Evidence, "repository configuration names a program: core.fsmonitor") {
		t.Fatalf("verdict %s %+v; want keep naming core.fsmonitor", v.Kind, v.Checks)
	}
	if len(v.Checks) != 1 {
		t.Errorf("checks %+v: nothing past the configuration read should have run", v.Checks)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the hook the repository named ran")
	}
}

func TestEvaluateTimesOutAHangingConfigurationRead(t *testing.T) {
	f := newFixture(t)
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	gitRun(t, f.clone, "config", "include.path", fifo)
	prev := configReadTimeout
	configReadTimeout = 500 * time.Millisecond
	t.Cleanup(func() { configReadTimeout = prev })
	start := time.Now()
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("a hanging read took %s", d)
	}
}

func TestEvaluateCarriesTheLivenessVerdict(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	for _, c := range []struct {
		l      Liveness
		reason string
	}{
		{LivenessLive, ""},
		{LivenessUnreadable, "could not read it"},
		{LivenessUnknown, ""},
	} {
		u := f.unit()
		u.Liveness, u.LivenessReason = c.l, c.reason
		v := Evaluate(context.Background(), u, p)
		first, _ := v.First()
		if v.Kind != Keep || first.Name != CheckLiveness {
			t.Errorf("liveness %d: verdict %s %+v; want keep by liveness", c.l, v.Kind, v.Checks)
		}
		if c.reason != "" && first.Evidence != c.reason {
			t.Errorf("liveness %d: evidence %q, want %q", c.l, first.Evidence, c.reason)
		}
	}
}

func TestEvaluateRunsEveryCheckAfterOneKeeps(t *testing.T) {
	f := newFixture(t)
	gitRun(t, f.clone, "checkout", "-q", "-b", "never-pushed")
	gitRun(t, f.clone, "commit", "-q", "--allow-empty", "-m", "work")
	u := f.unit()
	u.Liveness = LivenessLive
	v := Evaluate(context.Background(), u, f.policy(t))
	if len(v.Checks) < 2 || v.Checks[0].Name != CheckLiveness || v.Checks[1].Name != CheckRepository {
		t.Fatalf("checks %+v; want liveness first and the repository check after it", v.Checks)
	}
}

func TestEvaluateKeepsOnAWorkspaceConfigurationError(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	p.ConfigError = os.ErrNotExist
	v := Evaluate(context.Background(), f.unit(), p)
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
}

func TestEvaluateLeavesTheCloneUnchanged(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(filepath.Join(f.clone, "README"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.instance)
	Evaluate(context.Background(), f.unit(), f.policy(t))
	if after := snapshot(t, f.instance); after != before {
		t.Fatalf("the instance changed under a read-only evaluation:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// snapshot lists every entry under dir with its size, mode and contents.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		b.WriteString(rel + " " + info.Mode().String() + " " + info.ModTime().String())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			b.WriteString(" " + string(data))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
