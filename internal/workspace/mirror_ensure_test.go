package workspace

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	// The healthy check is followed by the refresh every apply makes.
	lines := f.logLines(t)
	if check := lines[len(lines)-2]; check["result"] != "healthy" || check["event"] != "check" {
		t.Fatalf("the log ends %v, want a healthy check before the refresh", lines[len(lines)-2:])
	}
}

// twoInstances makes the fixture's workspace hold two instances, inst and
// inst-2, as a root-level apply finds them, each with a clone of app that
// borrows from the mirror, and returns their roots.
func (f mirrorFixture) twoInstances(t *testing.T) []string {
	t.Helper()
	second := filepath.Join(f.root, "inst-2")
	info := filepath.Join(second, "tools", "app", ".git", "objects", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	alternates := mirror.StablePath(f.home, f.root, "app") + "/objects\n"
	if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(alternates), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := []string{f.instance, second}
	for _, root := range roots {
		if err := SaveState(root, &InstanceState{
			SchemaVersion: SchemaVersion,
			InstanceName:  filepath.Base(root),
			Root:          root,
			Repos:         map[string]RepoState{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return roots
}

// appLines are the mirror log's lines for app, or none when there's no log.
func (f mirrorFixture) appLines(t *testing.T) []map[string]any {
	t.Helper()
	path := filepath.Join(filepath.Dir(mirror.StablePath(f.home, f.root, "app")), ".meta", "mirror.log")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	var app []map[string]any
	for _, line := range f.logLines(t) {
		if line["mirror"] == "app" {
			app = append(app, line)
		}
	}
	return app
}

func TestEnsureMirrors_OneCommandTriesAFailingMirrorOnce(t *testing.T) {
	f := newMirrorFixture(t)
	roots := f.twoInstances(t)
	// A URL that names no remote: the restore fails at the clone.
	f.classified[0].Repo.CloneURL = "file://" + filepath.Join(t.TempDir(), "absent.git")

	var results []mirror.Result
	var stderr bytes.Buffer
	a := f.applier(&stderr)
	a.MirrorResults = &results
	for _, root := range roots {
		a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, root, "apply")
	}

	lines := f.appLines(t)
	if len(lines) != 2 || lines[0]["event"] != "check" || lines[1]["event"] != "restore" {
		t.Fatalf("log lines for app = %v, want one check and one restore line", lines)
	}
	for _, line := range lines {
		if line["result"] != "restore-failed" {
			t.Fatalf("log line %v, want result restore-failed", line)
		}
	}
	if len(results) != 1 || results[0].Outcome != mirror.OutcomeRestoreFailed {
		t.Fatalf("MirrorResults = %+v, want one restore-failed result", results)
	}
	if n := strings.Count(stderr.String(), "warning: mirror app:"); n != 1 {
		t.Fatalf("stderr carries %d mirror warnings, want 1: %q", n, stderr.String())
	}

	// The next command is a fresh Applier: it checks the mirror again.
	b := f.applier(&stderr)
	b.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, roots[0], "apply")
	if after := f.appLines(t); len(after) <= len(lines) || after[len(lines)]["event"] != "check" {
		t.Fatalf("the next command logged %v after %d lines, want a new check line", after, len(lines))
	}
}

func TestEnsureMirrors_OneCommandWaitsOnABusyMirrorOnce(t *testing.T) {
	f := newMirrorFixture(t)
	roots := f.twoInstances(t)
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
	for _, root := range roots {
		a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, root, "apply")
	}
	if took := time.Since(start); took >= 1800*time.Millisecond {
		t.Fatalf("two passes took %s, want under 1.8s: the second pass waited again", took)
	}
	if len(results) != 1 || results[0].Outcome != mirror.OutcomeBusy {
		t.Fatalf("MirrorResults = %+v, want one busy result", results)
	}
}

// build restores the fixture's mirror with a throwaway Applier, as the first
// command after the borrowers appear would.
func (f mirrorFixture) build(t *testing.T) {
	t.Helper()
	var stderr bytes.Buffer
	f.applier(&stderr).ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")
	if lines := f.appLines(t); len(lines) != 2 || lines[1]["result"] != "restored" {
		t.Fatalf("building the mirror logged %v", lines)
	}
}

// pushCommit adds a commit to main on the fixture's remote and returns it.
func (f mirrorFixture) pushCommit(t *testing.T, content string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	mirrorGit(t, filepath.Dir(work), "clone", "--quiet", f.remote, work)
	if err := os.WriteFile(filepath.Join(work, content+".txt"), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mirrorGit(t, work, "add", content+".txt")
	mirrorGit(t, work, "commit", "--quiet", "-m", content)
	mirrorGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/main")
	out, err := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// mirrorMain is what main names in the mirror's store.
func (f mirrorFixture) mirrorMain(t *testing.T) string {
	t.Helper()
	stable := mirror.StablePath(f.home, f.root, "app")
	out, err := exec.Command("git", "--git-dir", stable, "rev-parse", "refs/heads/main").Output()
	if err != nil {
		t.Fatalf("reading main in the mirror: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestEnsureMirrors_RefreshesAHealthyMirrorUnderItsCommand(t *testing.T) {
	for _, c := range []struct{ mirrorCommand, want string }{
		{"", "apply"},
		{"dispatch", "dispatch"},
		{"from-hook", "from-hook"},
	} {
		t.Run(c.want, func(t *testing.T) {
			f := newMirrorFixture(t)
			f.build(t)
			tip := f.pushCommit(t, "new")
			logged := len(f.appLines(t))

			var stderr bytes.Buffer
			a := f.applier(&stderr)
			a.MirrorCommand = c.mirrorCommand
			a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")

			if got := f.mirrorMain(t); got != tip {
				t.Fatalf("main in the mirror = %s, want the fetched %s", got, tip)
			}
			lines := f.appLines(t)[logged:]
			if len(lines) != 2 || lines[0]["event"] != "check" || lines[1]["event"] != "refresh" {
				t.Fatalf("log gained %v, want a check and a refresh line", lines)
			}
			for _, line := range lines {
				if line["command"] != c.want || line["result"] != "healthy" || line["fault"] != "" {
					t.Fatalf("log line %v, want command %q, no fault, result healthy", line, c.want)
				}
			}
			if strings.Contains(stderr.String(), "mirror") {
				t.Fatalf("stderr = %q, want no mirror warning", stderr.String())
			}
		})
	}
}

func TestEnsureMirrors_OneCommandRefreshesAMirrorOnce(t *testing.T) {
	f := newMirrorFixture(t)
	roots := f.twoInstances(t)
	f.build(t)
	f.pushCommit(t, "new")
	logged := len(f.appLines(t))

	var stderr bytes.Buffer
	a := f.applier(&stderr)
	for _, root := range roots {
		a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, root, "apply")
	}
	lines := f.appLines(t)[logged:]
	if len(lines) != 2 || lines[0]["event"] != "check" || lines[1]["event"] != "refresh" {
		t.Fatalf("two instance passes logged %v, want one check and one refresh line", lines)
	}
}

func TestEnsureMirrors_AFailedRefreshWarnsAndIsNotCollected(t *testing.T) {
	f := newMirrorFixture(t)
	f.build(t)
	if err := os.Rename(f.remote, f.remote+".gone"); err != nil {
		t.Fatal(err)
	}

	var results []mirror.Result
	var stderr bytes.Buffer
	a := f.applier(&stderr)
	a.MirrorResults = &results
	a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")

	var warnings []string
	for _, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if strings.Contains(line, "mirror") {
			warnings = append(warnings, line)
		}
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], "warning: mirror app: refresh failed:") {
		t.Fatalf("stderr mirror lines = %q, want one starting %q", warnings, "warning: mirror app: refresh failed:")
	}
	if len(results) != 0 {
		t.Fatalf("MirrorResults = %+v, want nothing for a healthy mirror", results)
	}
}

// replaceLogWithADirectory makes every write to the fixture's mirror log
// fail, as a log the user can't write would.
func (f mirrorFixture) replaceLogWithADirectory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(mirror.StablePath(f.home, f.root, "app")), ".meta", "mirror.log")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// mirrorLines are the lines of stderr that mention a mirror.
func mirrorLines(stderr string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if strings.Contains(line, "mirror") {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestReportMirrorResult_ALogThatCantBeWritten(t *testing.T) {
	logErr := errors.New("open /x/mirror.log: is a directory")
	const failed = "warning: mirror app: writing the mirror log failed: open /x/mirror.log: is a directory\n"
	for _, c := range []struct {
		name string
		res  mirror.Result
		want string
	}{
		{"healthy", mirror.Result{RepoID: "app", Outcome: mirror.OutcomeHealthy, LogErr: logErr}, failed},
		{"restored", mirror.Result{RepoID: "app", Fault: mirror.FaultMissing, Outcome: mirror.OutcomeRestored, LogErr: logErr},
			"warning: mirror app: fault missing, result restored\n" + failed},
		{"restored with a working log", mirror.Result{RepoID: "app", Fault: mirror.FaultMissing, Outcome: mirror.OutcomeRestored},
			"warning: mirror app: fault missing, result restored\n"},
		{"healthy with a working log", mirror.Result{RepoID: "app", Outcome: mirror.OutcomeHealthy}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stderr bytes.Buffer
			ReportMirrorResult(NewReporterWithTTY(&stderr, false), c.res)
			if got := stderr.String(); got != c.want {
				t.Fatalf("printed %q, want %q", got, c.want)
			}
		})
	}
}

func TestEnsureMirrors_ALogThatCantBeWrittenIsWarnedAndCollectedOnce(t *testing.T) {
	f := newMirrorFixture(t)
	f.build(t)
	path := f.replaceLogWithADirectory(t)

	var results []mirror.Result
	var stderr bytes.Buffer
	a := f.applier(&stderr)
	a.MirrorResults = &results
	a.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")

	lines := mirrorLines(stderr.String())
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "warning: mirror app: writing the mirror log failed:") {
		t.Fatalf("stderr mirror lines = %q, want one log-failed warning", lines)
	}
	if len(results) != 1 || results[0].Outcome != mirror.OutcomeHealthy || results[0].LogErr == nil {
		t.Fatalf("MirrorResults = %+v, want the healthy result with LogErr once", results)
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		t.Fatalf("the directory at the log's path was moved or removed: %v (%v)", info, err)
	}

	// With the log writable again, a healthy mirror is neither warned about
	// nor collected.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	var healthy []mirror.Result
	stderr.Reset()
	b := f.applier(&stderr)
	b.MirrorResults = &healthy
	b.ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")
	if lines := mirrorLines(stderr.String()); len(lines) != 0 || len(healthy) != 0 {
		t.Fatalf("a healthy mirror with a working log printed %q and collected %+v", lines, healthy)
	}
}

func TestReportMirrorResult_SilentForAHealthyMirror(t *testing.T) {
	var stderr bytes.Buffer
	ReportMirrorResult(NewReporterWithTTY(&stderr, false), mirror.Result{RepoID: "app", Outcome: mirror.OutcomeHealthy})
	if stderr.Len() != 0 {
		t.Fatalf("a healthy result printed %q", stderr.String())
	}
}

func TestReportMirrorResult_NamesEachUnrecoverableRef(t *testing.T) {
	missing := strings.Repeat("a", 40)
	own := strings.Repeat("b", 40)
	res := mirror.Result{
		RepoID: "app", Fault: mirror.FaultMissing, Outcome: mirror.OutcomeRestored,
		Unrecoverable: []mirror.LostRef{
			{Instance: "/ws/myws/tools/app", Repo: "app", Ref: "refs/remotes/origin/topic", MissingCommit: missing, RemoteTracking: true,
				OwnCommits: []string{}, LostFiles: []mirror.LostFile{}},
			{Instance: "/ws/myws/tools/app", Repo: "app", Ref: "refs/heads/keep", MissingCommit: missing,
				OwnCommits: []string{own}, LostFiles: []mirror.LostFile{{Path: "added-topic.txt"}}},
		},
	}
	var stderr bytes.Buffer
	ReportMirrorResult(NewReporterWithTTY(&stderr, false), res)
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("printed %d lines, want 3:\n%s", len(lines), stderr.String())
	}
	if lines[0] != "warning: mirror app: fault missing, result restored" {
		t.Fatalf("line 1 = %q", lines[0])
	}
	for _, want := range []string{
		"warning: mirror app:", "/ws/myws/tools/app", "refs/heads/keep", missing,
		"pushed and later deleted from the remote", "no longer serves it",
		"unpushed commits are intact: " + own, "added-topic.txt (not in the working tree)",
	} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("line 2 = %q, want it to carry %q", lines[1], want)
		}
	}
	if strings.HasPrefix(lines[2], "warning:") {
		t.Fatalf("line 3 = %q, want no warning prefix", lines[2])
	}
	for _, want := range []string{"/ws/myws/tools/app", "refs/remotes/origin/topic", missing, "git fetch --prune"} {
		if !strings.Contains(lines[2], want) {
			t.Fatalf("line 3 = %q, want it to carry %q", lines[2], want)
		}
	}

	stderr.Reset()
	res.Unrecoverable = nil
	ReportMirrorResult(NewReporterWithTTY(&stderr, false), res)
	if got := stderr.String(); got != "warning: mirror app: fault missing, result restored\n" {
		t.Fatalf("a restore with nothing lost printed %q", got)
	}
}
