package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const passSessionID = "44444444-4444-4444-4444-444444444444"

func passGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// passFixture is a workspace whose configuration names one repository by a
// local file URL, with a mapped ephemeral instance holding a clone of it and
// no job entry, so its session reads as deleted.
type passFixture struct {
	root, jobsDir, inst, clone string
	stderr                     *bytes.Buffer
	destroyed                  *[]string
}

func newPassFixture(t *testing.T) passFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := canonicalTempDir(t)
	remote := filepath.Join(base, "remote", "app.git")
	passGit(t, base, "init", "-q", "--bare", remote)
	seed := filepath.Join(base, "seed")
	passGit(t, base, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	passGit(t, seed, "add", "README")
	passGit(t, seed, "commit", "-q", "-m", "init")
	passGit(t, seed, "push", "-q", remote, "main")

	root := filepath.Join(base, "ws")
	writeTestFile(t, filepath.Join(root, config.ConfigDir, config.ConfigFile),
		"[workspace]\nname = \"test-ws\"\n\n[groups.tools]\n\n[repos.app]\nurl = \"file://"+remote+"\"\ngroup = \"tools\"\n")
	inst := makeReapInstance(t, root, "test-ws-pass")
	clone := filepath.Join(inst, "tools", "app")
	passGit(t, base, "clone", "-q", "file://"+remote, clone)
	mapEphemeral(t, root, passSessionID, inst, true)

	prevEnv := newRemovalEnv
	newRemovalEnv = func(r, home, pathEnv string) *removal.Env {
		return removal.NewEnv(r, home, pathEnv, removal.WithFileRemotes())
	}
	t.Cleanup(func() { newRemovalEnv = prevEnv })
	var stderr bytes.Buffer
	prevStderr := reapStderr
	reapStderr = &stderr
	t.Cleanup(func() { reapStderr = prevStderr })

	return passFixture{root: root, jobsDir: t.TempDir(), inst: inst, clone: clone, stderr: &stderr, destroyed: stubDestroyAll(t)}
}

func (f passFixture) commitUnpushed(t *testing.T, branch string) {
	t.Helper()
	passGit(t, f.clone, "checkout", "-q", "-b", branch)
	passGit(t, f.clone, "commit", "-q", "--allow-empty", "-m", "work")
}

func TestPass_KeepsADeletedSessionsInstanceHoldingAnUnpushedCommit(t *testing.T) {
	f := newPassFixture(t)
	f.commitUnpushed(t, "never-pushed")

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(*f.destroyed) != 0 {
		t.Fatalf("reaped %d, destroyed %v; want the unit kept", res.Reaped, *f.destroyed)
	}
	if len(res.Kept) != 1 || res.Kept[0].Check.Name != removal.CheckRepository {
		t.Fatalf("kept %+v; want one unit kept by the repository check", res.Kept)
	}
	line := f.stderr.String()
	for _, want := range []string{"niwa: kept test-ws-pass:", "repository check", "branch never-pushed"} {
		if !strings.Contains(line, want) {
			t.Errorf("reap output %q does not contain %q", line, want)
		}
	}
	if _, err := workspace.ReadSessionMapping(f.root, passSessionID); err != nil {
		t.Errorf("the kept unit's mapping is gone: %v", err)
	}
}

func TestPass_ReapsTheSameUnitWithNothingUnpushed(t *testing.T) {
	f := newPassFixture(t)
	n, err := reapWorkspace(f.root, f.jobsDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(*f.destroyed) != 1 || (*f.destroyed)[0] != f.inst {
		t.Fatalf("reaped %d, destroyed %v; want %s removed\n%s", n, *f.destroyed, f.inst, f.stderr.String())
	}
	if _, err := workspace.ReadSessionMapping(f.root, passSessionID); err == nil {
		t.Error("mapping survived the reap")
	}
}

func TestPass_LeavesUnitsPastTheDeadlineInPlace(t *testing.T) {
	f := newPassFixture(t)
	prev := passDeadline
	passDeadline = 0
	t.Cleanup(func() { passDeadline = prev })

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(*f.destroyed) != 0 || res.Unreached != 1 {
		t.Fatalf("result %+v, destroyed %v; want the unit left in place, unreached", res, *f.destroyed)
	}
}

func TestPass_RunsNoOnReapCommandPastTheDeadline(t *testing.T) {
	f := newPassFixture(t)
	marker := filepath.Join(t.TempDir(), "ran")
	writeTestFile(t, filepath.Join(f.root, config.ConfigDir, config.ConfigFile),
		"[workspace]\nname = \"test-ws\"\n\n[session.on_reap]\ncommand = \"touch "+marker+"\"\n")
	// A mapping whose instance and session are both gone, which the pass
	// settles by running the on-reap command for it.
	const goneID = "55555555-5555-5555-5555-555555555555"
	mapEphemeral(t, f.root, goneID, filepath.Join(f.root, "test-ws-gone"), true)
	prev := passDeadline
	t.Cleanup(func() { passDeadline = prev })

	passDeadline = 0
	if _, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a pass with no time left ran the on-reap command")
	}
	if _, err := workspace.ReadSessionMapping(f.root, goneID); err != nil {
		t.Fatalf("a pass with no time left settled the stored mapping: %v", err)
	}
	if !strings.Contains(f.stderr.String(), "the pass's time ran out") {
		t.Errorf("reap output %q does not say the pass's time ran out", f.stderr.String())
	}

	passDeadline = prev
	if _, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("a pass with time left did not run the on-reap command")
	}
	if _, err := workspace.ReadSessionMapping(f.root, goneID); err == nil {
		t.Error("a pass with time left did not settle the stored mapping")
	}
}

func TestRemovalGate_GetsNoTimePastThePassDeadline(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, config.ConfigDir, config.ConfigFile),
		"[workspace]\nname = \"test-ws\"\n\n[session.before_remove]\ncommand = \"exit 0\"\n")
	var out bytes.Buffer
	gate := newRemovalGate(root, removeStepReap, &out)
	gate.deadline = time.Now().Add(-time.Second)
	allowed, reason := gate.check(filepath.Join(root, "test-ws-1"), nil)
	if allowed || !strings.Contains(reason, "the pass's time ran out") {
		t.Fatalf("check past the pass deadline = %v, %q; want kept because the pass's time ran out", allowed, reason)
	}
}

func TestOnReap_GetsNoTimePastThePassDeadline(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	writeTestFile(t, filepath.Join(root, config.ConfigDir, config.ConfigFile),
		"[workspace]\nname = \"test-ws\"\n\n[session.on_reap]\ncommand = \"touch "+marker+"\"\n")
	var out bytes.Buffer
	step := newOnReapStep(root, &out)
	step.deadline = time.Now().Add(-time.Second)
	if step.run(removedSession{SessionID: passSessionID, ReapKey: "k1"}) {
		t.Fatal("the on-reap step ran past the pass deadline")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the on-reap command ran past the pass deadline")
	}
	if !strings.Contains(out.String(), "the pass's time ran out") {
		t.Errorf("warning %q does not say the pass's time ran out", out.String())
	}
}

func TestPass_WatchStageReapKeepsTheUnitAndStillStages(t *testing.T) {
	f := newPassFixture(t)
	f.commitUnpushed(t, "never-pushed")
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	fakes := installWatchCharFakes(t)
	stubWatchTrust(t, home, false)
	client := newPullHeadServer(t)
	cmd, _, _ := newWatchCharCmd()
	stubDispatchRand(t, constByteReader(0xab))

	// The reap before staging reads the default jobs directory under HOME,
	// which holds no entry for the unit's session, so it reads as deleted.
	if err := stageReview(cmd, f.root, f.root, "", client, watchCharPR, reviewPlan{}); err != nil {
		t.Fatalf("stageReview after the reap: %v", err)
	}
	if len(fakes.launches) != 1 {
		t.Fatalf("launches = %d, want 1: the reap before watch must not stop the stage", len(fakes.launches))
	}
	for _, p := range *fakes.destroyed {
		if p == f.inst {
			t.Fatalf("the reap before watch removed %s, which holds an unpushed commit", f.inst)
		}
	}
	if _, err := os.Stat(f.inst); err != nil {
		t.Fatalf("the kept unit is gone: %v", err)
	}
	if !strings.Contains(f.stderr.String(), "branch never-pushed") {
		t.Errorf("the reap before watch did not name the kept unit: %q", f.stderr.String())
	}
}

func TestPass_OpportunisticReapKeepsTheUnitAndNeverFails(t *testing.T) {
	f := newPassFixture(t)
	f.commitUnpushed(t, "never-pushed")
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	// reapOpportunistically reads the default jobs directory, which holds no
	// entry for the session, so it reads as deleted.
	reapOpportunistically(f.root)
	if len(*f.destroyed) != 0 {
		t.Fatalf("the opportunistic reap removed %v", *f.destroyed)
	}
	if !strings.Contains(f.stderr.String(), "branch never-pushed") {
		t.Errorf("the opportunistic reap did not name the kept unit: %q", f.stderr.String())
	}
}

func TestPass_LeavesNoProcessRunning(t *testing.T) {
	f := newPassFixture(t)
	f.commitUnpushed(t, "never-pushed")
	if _, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(os.Getpid())).Output()
	// pgrep itself is the caller's child while it runs; anything else is
	// a process the pass left behind.
	for _, pid := range strings.Fields(string(out)) {
		if comm, err := exec.Command("ps", "-o", "comm=", "-p", pid).Output(); err == nil && !strings.Contains(string(comm), "pgrep") {
			t.Errorf("process %s (%s) is still running after the pass", pid, strings.TrimSpace(string(comm)))
		}
	}
}

func TestCloneURLsFromConfig(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Sources: []config.SourceConfig{{Org: "acme", Repos: []string{"api"}}, {Org: "beta"}},
		Repos:   map[string]config.RepoOverride{"web": {URL: "https://example.com/web.git"}},
	}
	urls := cloneURLsFromConfig(cfg)
	cases := map[string]string{
		"web":  "https://example.com/web.git",
		"api":  "git@github.com:acme/api.git",
		"tool": "git@github.com:beta/tool.git",
	}
	for name, want := range cases {
		if got, ok := urls(name); !ok || got != want {
			t.Errorf("clone URL of %s = %q, %v; want %q", name, got, ok, want)
		}
	}
	two := cloneURLsFromConfig(&config.WorkspaceConfig{Sources: []config.SourceConfig{{Org: "a"}, {Org: "b"}}})
	if got, ok := two("tool"); ok {
		t.Errorf("two discovering sources gave %q; want no URL", got)
	}
	if got, ok := urls("../x"); ok {
		t.Errorf("a name with a separator gave %q", got)
	}
}
