package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// mirrorFixture is a workspace root with one instance whose clone of "app"
// borrows from a mirror that doesn't exist yet, a bare remote to restore it
// from, and a home for the mirrors.
type mirrorFixture struct {
	home, root, instance, remote string
	classified                   []ClassifiedRepo
}

func mirrorGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

func newMirrorFixture(t *testing.T) mirrorFixture {
	t.Helper()
	base := t.TempDir()
	f := mirrorFixture{
		home:   filepath.Join(base, "home"),
		root:   filepath.Join(base, "ws"),
		remote: filepath.Join(base, "remote.git"),
	}
	f.instance = filepath.Join(f.root, "inst")
	clone := filepath.Join(f.instance, "tools", "app")
	for _, d := range []string{f.home, filepath.Join(clone, ".git", "objects", "info")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(base, "work")
	mirrorGit(t, base, "init", "--quiet", "--bare", f.remote)
	mirrorGit(t, base, "init", "--quiet", work)
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mirrorGit(t, work, "add", "a.txt")
	mirrorGit(t, work, "commit", "--quiet", "-m", "first")
	mirrorGit(t, work, "push", "--quiet", f.remote, "HEAD:refs/heads/main")
	mirrorGit(t, f.remote, "symbolic-ref", "HEAD", "refs/heads/main")

	alternates := mirror.StablePath(f.home, f.root, "app") + "/objects\n"
	if err := os.WriteFile(filepath.Join(clone, ".git", "objects", "info", "alternates"), []byte(alternates), 0o644); err != nil {
		t.Fatal(err)
	}
	f.classified = []ClassifiedRepo{{
		Repo:  github.Repo{Name: "app", CloneURL: "file://" + f.remote},
		Group: "tools",
	}}
	return f
}

func (f mirrorFixture) applier(stderr *bytes.Buffer) *Applier {
	return &Applier{DeveloperHome: f.home, Reporter: NewReporterWithTTY(stderr, false)}
}

// logLines decodes the mirror log, one JSON object per line.
func (f mirrorFixture) logLines(t *testing.T) []map[string]any {
	t.Helper()
	path := filepath.Join(filepath.Dir(mirror.StablePath(f.home, f.root, "app")), ".meta", "mirror.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the mirror log: %v", err)
	}
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines
}

func TestEnsureMirrors_LogsMirrorCommandOrThePipelinesOwn(t *testing.T) {
	for _, c := range []struct{ mirrorCommand, want string }{
		{"", "apply"},
		{"from-hook", "from-hook"},
	} {
		t.Run(c.want, func(t *testing.T) {
			f := newMirrorFixture(t)
			var stderr bytes.Buffer
			a := f.applier(&stderr)
			a.MirrorCommand = c.mirrorCommand
			a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")

			lines := f.logLines(t)
			if len(lines) != 2 {
				t.Fatalf("the log has %d lines, want a check and a restore line: %v", len(lines), lines)
			}
			for _, line := range lines {
				if line["command"] != c.want || line["result"] != "restored" {
					t.Fatalf("log line %v, want command %q and result restored", line, c.want)
				}
			}
			if got := stderr.String(); !strings.Contains(got, "warning: mirror app: fault missing, result restored") {
				t.Fatalf("stderr = %q, want the restore warning", got)
			}
		})
	}
}

func TestEnsureMirrors_MirrorLockWaitBoundsABusyRestore(t *testing.T) {
	f := newMirrorFixture(t)
	release, err := filelock.TryFileLock(mirror.LockPath(f.home, f.root, "app"), "test holder")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	var results []mirror.Result
	var stderr bytes.Buffer
	a := f.applier(&stderr)
	a.MirrorLockWait = time.Second
	a.MirrorResults = &results
	start := time.Now()
	a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")
	if took := time.Since(start); took >= 5*time.Second {
		t.Fatalf("the busy restore took %s, want under 5s", took)
	}
	if len(results) != 1 || results[0].RepoID != "app" || results[0].Outcome != mirror.OutcomeBusy {
		t.Fatalf("MirrorResults = %+v, want one busy result for app", results)
	}
}

func TestEnsureMirrors_MirrorResultsHoldsOnlyWhatWasntHealthy(t *testing.T) {
	f := newMirrorFixture(t)
	var results []mirror.Result
	var stderr bytes.Buffer
	a := f.applier(&stderr)
	a.MirrorResults = &results
	a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "create")
	if len(results) != 1 || results[0].Outcome != mirror.OutcomeRestored || results[0].Fault != mirror.FaultMissing {
		t.Fatalf("MirrorResults after a restore = %+v, want one missing/restored result", results)
	}

	// A new Applier, as a new command would have: the mirror is healthy now.
	var healthy []mirror.Result
	b := f.applier(&stderr)
	b.MirrorResults = &healthy
	b.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")
	if len(healthy) != 0 {
		t.Fatalf("MirrorResults for a healthy mirror = %+v, want none", healthy)
	}
	lines := f.logLines(t)
	if last := lines[len(lines)-1]; last["result"] != "healthy" || last["event"] != "check" {
		t.Fatalf("the last log line is %v, want a healthy check", last)
	}
}
