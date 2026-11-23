package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
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
	// The real listing runs lsof, a process this test must see gone too.
	realProcessListing(t)
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

const verdictKeptSessionID = "66666666-6666-6666-6666-666666666666"

// addKeptUnit adds a second mapped unit beside the fixture's, whose clone
// holds a commit on branch that the remote doesn't, and returns its path.
func (f passFixture) addKeptUnit(t *testing.T, name, branch string) string {
	t.Helper()
	remote := filepath.Join(filepath.Dir(f.root), "remote", "app.git")
	inst := makeReapInstance(t, f.root, name)
	clone := filepath.Join(inst, "tools", "app")
	passGit(t, filepath.Dir(f.root), "clone", "-q", "file://"+remote, clone)
	passGit(t, clone, "checkout", "-q", "-b", branch)
	passGit(t, clone, "commit", "-q", "--allow-empty", "-m", "work")
	mapEphemeral(t, f.root, verdictKeptSessionID, inst, true)
	return inst
}

// appendConfig adds body to the fixture's workspace configuration.
func (f passFixture) appendConfig(t *testing.T, body string) {
	t.Helper()
	cfgPath := filepath.Join(f.root, config.ConfigDir, config.ConfigFile)
	prev, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, cfgPath, string(prev)+body)
}

func verdictByName(t *testing.T, vs []unitVerdict, name string) unitVerdict {
	t.Helper()
	for _, v := range vs {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("no verdict for %s in %+v", name, vs)
	return unitVerdict{}
}

func runVerdictPass(t *testing.T, f passFixture, instance string) passResult {
	t.Helper()
	res, err := runPass(context.Background(), f.root, passVerdictOnly, passScope{JobsDir: f.jobsDir, Now: time.Now(), Instance: instance})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPassVerdictOnlyRemovesNothing(t *testing.T) {
	f := newPassFixture(t)
	f.addKeptUnit(t, "test-ws-kept", "unpushed-work")
	markers := t.TempDir()
	f.appendConfig(t, "\n[session.on_reap]\ncommand = \"touch "+filepath.Join(markers, "on-reap")+"\"\n"+
		"\n[session.before_remove]\ncommand = \"touch "+filepath.Join(markers, "gate")+"\"\n")

	res := runVerdictPass(t, f, "")
	if res.Reaped != 0 || len(*f.destroyed) != 0 {
		t.Fatalf("reaped %d, destroyed %v; a verdict-only pass removes nothing", res.Reaped, *f.destroyed)
	}
	for _, id := range []string{passSessionID, verdictKeptSessionID} {
		if _, err := workspace.ReadSessionMapping(f.root, id); err != nil {
			t.Errorf("mapping %s is gone: %v", id, err)
		}
	}
	for _, m := range []string{"on-reap", "gate"} {
		if _, err := os.Stat(filepath.Join(markers, m)); err == nil {
			t.Errorf("the %s command ran in a verdict-only pass", m)
		}
	}
	if len(res.Verdicts) != 2 || len(res.Kept) != 0 {
		t.Fatalf("verdicts %+v, kept %+v; want two verdicts and no kept line", res.Verdicts, res.Kept)
	}
	pass := verdictByName(t, res.Verdicts, "test-ws-pass")
	if !pass.Reached || pass.Verdict.Kind != removal.Reap {
		t.Errorf("clean unit %+v; want reached, reap", pass)
	}
	kept := verdictByName(t, res.Verdicts, "test-ws-kept")
	first, _ := kept.Verdict.First()
	if !kept.Reached || kept.Verdict.Kind != removal.Keep || first.Name != removal.CheckRepository || !strings.Contains(first.Evidence, "branch unpushed-work") {
		t.Errorf("kept unit %+v; want kept by the repository check naming the branch", kept)
	}
	if strings.Contains(f.stderr.String(), "niwa: kept test-ws-kept") {
		t.Errorf("a verdict-only pass printed the reap's kept line: %q", f.stderr.String())
	}
}

func TestPassVerdictOnlyEvaluatesOnlyTheNamedUnit(t *testing.T) {
	f := newPassFixture(t)
	f.addKeptUnit(t, "test-ws-kept", "unpushed-work")
	res := runVerdictPass(t, f, "test-ws-kept")
	if len(res.Verdicts) != 1 || res.Verdicts[0].Name != "test-ws-kept" || !res.Verdicts[0].Reached {
		t.Fatalf("verdicts %+v; want only test-ws-kept", res.Verdicts)
	}
	if res := runVerdictPass(t, f, "test-ws-none"); len(res.Verdicts) != 0 {
		t.Fatalf("verdicts %+v for an instance that doesn't exist", res.Verdicts)
	}
}

func TestPassVerdictOnlyMarksUnitsPastTheDeadlineNotReached(t *testing.T) {
	f := newPassFixture(t)
	f.addKeptUnit(t, "test-ws-kept", "unpushed-work")
	prev := passDeadline
	passDeadline = 0
	t.Cleanup(func() { passDeadline = prev })

	res := runVerdictPass(t, f, "")
	if len(res.Verdicts) != 2 || res.Unreached != 2 {
		t.Fatalf("verdicts %+v, unreached %d; want both units listed, unreached", res.Verdicts, res.Unreached)
	}
	for _, v := range res.Verdicts {
		if v.Reached {
			t.Errorf("%s reached past the deadline", v.Name)
		}
	}
	var out bytes.Buffer
	printVerdictsText(&out, res.Verdicts)
	if strings.Count(out.String(), ": not reached\n") != 2 {
		t.Errorf("text %q; want both units printed as not reached", out.String())
	}
}

func TestPassVerdictOnlyMarksAUnitWhoseLockIsHeldNotReached(t *testing.T) {
	f := newPassFixture(t)
	prev := tryInstanceLockFunc
	tryInstanceLockFunc = func(string, string) (func(), error) {
		return nil, workspace.ErrLockHeld
	}
	t.Cleanup(func() { tryInstanceLockFunc = prev })

	res := runVerdictPass(t, f, "")
	if len(res.Verdicts) != 1 || res.Verdicts[0].Reached {
		t.Fatalf("verdicts %+v; want the unit listed, not reached", res.Verdicts)
	}
	if !strings.Contains(f.stderr.String(), "niwa: kept test-ws-pass: another niwa command") {
		t.Errorf("output %q; want the held lock named", f.stderr.String())
	}
}

func TestPassVerdictOnlyTakesTheLockAsAVerdictOnlyReap(t *testing.T) {
	f := newPassFixture(t)
	var holders []string
	prev := tryInstanceLockFunc
	tryInstanceLockFunc = func(_ string, holder string) (func(), error) {
		holders = append(holders, holder)
		return func() {}, nil
	}
	t.Cleanup(func() { tryInstanceLockFunc = prev })

	runVerdictPass(t, f, "")
	if len(holders) != 1 || !strings.HasPrefix(holders[0], "niwa reap --verdict-only (pid ") {
		t.Fatalf("lock holders %q; want one verdict-only reap", holders)
	}
}

func TestPassVerdictOnlyPrintsTheLivenessKeep(t *testing.T) {
	f := newPassFixture(t)
	f.commitUnpushed(t, "never-pushed")
	writeJobEntry(t, f.jobsDir, passSessionID)
	makeReapInstance(t, f.root, "test-ws-dev")
	other := makeReapInstance(t, f.root, "test-ws-other")
	mapEphemeral(t, f.root, verdictKeptSessionID, other, false)

	res := runVerdictPass(t, f, "")
	if len(res.Verdicts) != 3 {
		t.Fatalf("verdicts %+v; want every instance judged", res.Verdicts)
	}
	live := verdictByName(t, res.Verdicts, "test-ws-pass")
	if !live.Reached || live.Verdict.Kind != removal.Keep || len(live.Verdict.Checks) < 2 ||
		live.Verdict.Checks[0].Name != removal.CheckLiveness || live.Verdict.Checks[1].Name != removal.CheckRepository {
		t.Errorf("live unit %+v; want kept on liveness with the repository check still listed", live)
	}
	dev := verdictByName(t, res.Verdicts, "test-ws-dev")
	if first, _ := dev.Verdict.First(); !dev.Reached || first.Name != removal.CheckLiveness || !strings.Contains(first.Evidence, "no session mapping") {
		t.Errorf("unmapped instance %+v; want kept on liveness, naming the missing mapping", dev)
	}
	nonEphemeral := verdictByName(t, res.Verdicts, "test-ws-other")
	if first, _ := nonEphemeral.Verdict.First(); first.Name != removal.CheckLiveness || !strings.Contains(first.Evidence, "ephemeral") {
		t.Errorf("non-ephemeral instance %+v; want kept on liveness", nonEphemeral)
	}
	if len(*f.destroyed) != 0 {
		t.Errorf("destroyed %v", *f.destroyed)
	}
}

func TestPassVerdictOnlyPrintsNoVerdictsOnInvalidSettings(t *testing.T) {
	f := newPassFixture(t)
	f.appendConfig(t, "build_trees = [\"target/*\"]\n")
	res := runVerdictPass(t, f, "")
	if len(res.Verdicts) != 0 {
		t.Fatalf("verdicts %+v; want none on invalid settings", res.Verdicts)
	}
	if !strings.Contains(f.stderr.String(), "removed nothing") {
		t.Fatalf("output %q; want the removed-nothing line", f.stderr.String())
	}
}

func TestPassVerdictOnlySkipsStoredMappingsLaunchRecordsAndBackstop(t *testing.T) {
	f := newPassFixture(t)
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "on-reap")
	f.appendConfig(t, "\n[session.on_reap]\ncommand = \"touch "+marker+"\"\n")

	// A mapping whose instance and session are both gone, which an acting
	// pass settles by running the on-reap command.
	const goneID = "55555555-5555-5555-5555-555555555555"
	mapEphemeral(t, f.root, goneID, filepath.Join(f.root, "test-ws-gone"), true)
	// A launch record whose transcript is gone, which an acting pass sweeps.
	rec := workspace.LaunchRecord{
		DispatchID:   "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d",
		SessionID:    dispatchTestSessionID,
		Agent:        "claude",
		InstanceName: "test-ws+fix-0123abcd",
		WrittenAt:    time.Now().Add(-2 * launchRecordGrace),
	}
	if err := workspace.WriteLaunchRecord(f.root, rec); err != nil {
		t.Fatal(err)
	}
	// An unmapped dispatch instance past the TTL, which the backstop removes.
	orphan := makeReapInstance(t, f.root, dispInstOld)
	writeDispatchMarkerAt(t, orphan, time.Now().Add(-2*dispatchBackstopTTL))

	res := runVerdictPass(t, f, "")
	if len(*f.destroyed) != 0 || res.Reaped != 0 {
		t.Fatalf("destroyed %v; a verdict-only pass removes nothing", *f.destroyed)
	}
	if _, err := workspace.ReadSessionMapping(f.root, goneID); err != nil {
		t.Errorf("the stored mapping was settled: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the on-reap command ran")
	}
	if !launchRecordKept(t, f.root, rec) {
		t.Error("the launch record was swept")
	}
	if v := verdictByName(t, res.Verdicts, dispInstOld); v.Verdict.Kind != removal.Keep {
		t.Errorf("the orphan's verdict %+v; want kept on liveness", v)
	}

	// The same workspace under an acting pass does all three, so the
	// fixture is one they apply to.
	if _, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ReadSessionMapping(f.root, goneID); err == nil {
		t.Error("the acting pass did not settle the stored mapping")
	}
	if launchRecordKept(t, f.root, rec) {
		t.Error("the acting pass did not sweep the launch record")
	}
	found := false
	for _, p := range *f.destroyed {
		found = found || p == orphan
	}
	if !found {
		t.Errorf("the acting pass's backstop did not remove %s: destroyed %v", orphan, *f.destroyed)
	}
}

func TestReportKeptUnitScrubsACredentialHelperURL(t *testing.T) {
	var out bytes.Buffer
	reportKeptUnit(&out, keptUnit{Name: "test-ws-pass", Check: removal.Check{
		Name:     removal.CheckRepository,
		Evidence: "tools/app: repository configuration names a program: credential.https://user:tok@example.com.helper",
	}})
	line := out.String()
	if strings.Contains(line, "user:tok") || strings.Contains(line, "tok@") {
		t.Fatalf("kept line %q carries the userinfo", line)
	}
	if !strings.Contains(line, "credential.https://example.com.helper") {
		t.Fatalf("kept line %q lost the key around the userinfo", line)
	}
}

func TestPass_KeptLineCarriesNoCredentialHelperUserinfo(t *testing.T) {
	f := newPassFixture(t)
	passGit(t, f.clone, "config", "credential.https://user:tok@example.com.helper", "store")

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(res.Kept) != 1 {
		t.Fatalf("result %+v; want the unit kept", res)
	}
	out := f.stderr.String()
	if !strings.Contains(out, "repository configuration names a program: credential.https://example.com.helper") {
		t.Fatalf("output %q; want the kept line naming the key", out)
	}
	if strings.Contains(out, "user:tok") {
		t.Fatalf("output %q carries the userinfo", out)
	}
}

func TestPass_ReclaimedAndReapingWarningLinesAreScrubbed(t *testing.T) {
	f := newPassFixture(t)
	prev := removeTombstoneFunc
	removeTombstoneFunc = func(*workspace.DestroyPlan, string) error {
		return errors.New("rm failed for https://user:hunter2@example.com/x \x1b[31m")
	}
	t.Cleanup(func() { removeTombstoneFunc = prev })
	var err error
	stderr := captureOSStderr(t, func() {
		_, err = runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	// A removal that fails keeps the unit with a kept line; the instance is
	// renamed back under its own name.
	kept := f.stderr.String()
	if !strings.Contains(kept, "niwa: kept test-ws-pass: removing it failed: ") || strings.Contains(kept+stderr, "hunter2") || strings.Contains(kept+stderr, "\x1b") {
		t.Fatalf("kept line %q (stderr %q); want it printed with the userinfo and escape gone", kept, stderr)
	}
	if _, err := os.Stat(filepath.Join(f.inst, ".niwa", "instance.json")); err != nil {
		t.Fatalf("the instance isn't back under its own name: %v", err)
	}
}

func TestReportSparedInstancesScrubsNamesAndReasons(t *testing.T) {
	var out bytes.Buffer
	reportSparedInstances(&out, []sparedInstance{{
		Name:   "test-ws+-0000aa11",
		Reason: "it is marked to be kept: token=abc123 \x1b]0;title\x07 see https://u:p@example.com/x",
	}})
	for _, s := range []string{"abc123", "\x1b", "title", "u:p@"} {
		if strings.Contains(out.String(), s) {
			t.Errorf("spared report carries %q: %q", s, out.String())
		}
	}
	if !strings.Contains(out.String(), "test-ws+-0000aa11") {
		t.Errorf("spared report lost the name: %q", out.String())
	}
}

// runReapCommand runs `niwa reap args...` from dir and returns its output
// and error, with reap's flags reset before and after.
func runReapCommand(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(dir)
	reset := func() { reapVerdictOnly, reapJSON = false, false }
	reset()
	t.Cleanup(reset)
	var out, errOut bytes.Buffer
	rootCmd.SetArgs(append([]string{"reap"}, args...))
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	err := rootCmd.Execute()
	rootCmd.SetArgs(nil)
	rootCmd.SetOut(nil)
	rootCmd.SetErr(nil)
	reset()
	return out.String(), errOut.String(), err
}

func TestReapRefusesAnInstanceWithoutVerdictOnly(t *testing.T) {
	f := newPassFixture(t)
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	_, _, err := runReapCommand(t, f.root, "test-ws-pass")
	if err == nil || !strings.Contains(err.Error(), "only with --verdict-only") || !strings.Contains(err.Error(), "test-ws-pass") {
		t.Fatalf("error %v; want a usage error naming the argument", err)
	}
	if len(*f.destroyed) != 0 {
		t.Fatalf("destroyed %v; a usage error removes nothing", *f.destroyed)
	}
}

func TestReapRefusesJSONWithoutVerdictOnly(t *testing.T) {
	f := newPassFixture(t)
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	_, _, err := runReapCommand(t, f.root, "--json")
	if err == nil || !strings.Contains(err.Error(), "--json is accepted only with --verdict-only") {
		t.Fatalf("error %v; want a usage error", err)
	}
	if len(*f.destroyed) != 0 {
		t.Fatalf("destroyed %v; a usage error removes nothing", *f.destroyed)
	}
}

func TestReapRefusesAnUnknownInstance(t *testing.T) {
	f := newPassFixture(t)
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	out, _, err := runReapCommand(t, f.root, "--verdict-only", "test-ws-nope")
	if err == nil || !strings.Contains(err.Error(), `no instance named "test-ws-nope"`) {
		t.Fatalf("error %v; want the unknown instance named", err)
	}
	if out != "" || len(*f.destroyed) != 0 {
		t.Fatalf("output %q, destroyed %v; want nothing", out, *f.destroyed)
	}
}

func TestReapRefusesAnInstanceWithASeparator(t *testing.T) {
	f := newPassFixture(t)
	t.Setenv("HOME", filepath.Dir(f.jobsDir))
	for _, arg := range []string{"../test-ws-pass", `test-ws\pass`, "/abs"} {
		_, _, err := runReapCommand(t, f.root, "--verdict-only", arg)
		if err == nil || !strings.Contains(err.Error(), "path separator") || !strings.Contains(err.Error(), strconv.Quote(arg)) {
			t.Errorf("argument %q: error %v; want it refused, named", arg, err)
		}
	}
	if len(*f.destroyed) != 0 {
		t.Fatalf("destroyed %v", *f.destroyed)
	}
}

func TestReapVerdictOnlyPrintsTextAndJSON(t *testing.T) {
	f := newPassFixture(t)
	f.addKeptUnit(t, "test-ws-kept", "unpushed-work")
	t.Setenv("HOME", filepath.Dir(f.jobsDir))

	text, _, err := runReapCommand(t, f.root, "--verdict-only")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture's clones check out nothing (its remote's HEAD names no
	// branch), so a clean one is its pushed git directory alone.
	for _, want := range []string{"test-ws-kept: keep\n", "branch unpushed-work", "test-ws-pass: reap\n", "    tools/app/.git/ pushed\n", "    .niwa/ generated\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("text output does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Reaped") {
		t.Errorf("text output carries the reap's count line:\n%s", text)
	}

	named, _, err := runReapCommand(t, f.root, "--verdict-only", "test-ws-pass")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(named, "test-ws-pass: reap\n") || strings.Contains(named, "test-ws-kept") {
		t.Errorf("named output:\n%s\nwant only test-ws-pass", named)
	}

	raw, _, err := runReapCommand(t, f.root, "--verdict-only", "--json")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeVerdictsStrictly(t, strings.NewReader(raw))
	if doc.Schema != "niwa.verdicts/v1" || len(doc.Units) != 2 {
		t.Fatalf("document %+v", doc)
	}
	if len(*f.destroyed) != 0 {
		t.Fatalf("destroyed %v; verdict-only removes nothing", *f.destroyed)
	}
}

func TestReapVerdictOnlySkipsTheWorktreeSweep(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	scratchJobsDir(t)
	st := createHookWorktreeFor(t, f, "idle", otherSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, st, 48*time.Hour)

	if _, errOut, err := runReapCommand(t, f.root, "--verdict-only"); err != nil {
		t.Fatalf("verdict-only reap: %v\n%s", err, errOut)
	}
	assertKept(t, f, st)

	// A reap that acts removes it, so the worktree is one the sweep takes.
	if _, errOut, err := runReapCommand(t, f.root); err != nil {
		t.Fatalf("reap: %v\n%s", err, errOut)
	}
	assertRemoved(t, f, st)
}

// scrubLeaks names each of leaks that out still carries.
func scrubLeaks(out string, leaks ...string) []string {
	var found []string
	for _, s := range leaks {
		if strings.Contains(out, s) {
			found = append(found, s)
		}
	}
	return found
}

func TestReportInstanceLockKeptScrubsTheNameAndTheReason(t *testing.T) {
	// With no secret and no escape the line is the one it always was.
	for _, err := range []error{
		heldBy(lockTestHolder),
		errors.New("open /home/u/.niwa/locks/0a1b2c3d.lock: permission denied"),
	} {
		var out bytes.Buffer
		reportInstanceLockKept(&out, "/ws/test-ws-held", err)
		want := "niwa: kept " + sanitizeRemovalGateText("test-ws-held", 80) + ": " + instanceLockReason(err) + "\n"
		if out.String() != want {
			t.Errorf("kept line %q, want %q", out.String(), want)
		}
	}

	token := "ghp_" + strings.Repeat("A1b2", 9)
	var out bytes.Buffer
	reportInstanceLockKept(&out, "/ws/test-ws-token=abc123\x1b[31m",
		heldBy("niwa retask (pid 4242) password=hunter2 "+token+" https://u:pa55@example.com/x \x1b]0;title\x07"))
	line := out.String()
	if leaks := scrubLeaks(line, "abc123", "hunter2", token, "u:pa55", "\x1b", "title"); len(leaks) > 0 {
		t.Fatalf("kept line %q carries %q", line, leaks)
	}
	if !strings.HasPrefix(line, "niwa: kept test-ws-token=[redacted]: another niwa command is acting on it") ||
		!strings.Contains(line, "niwa retask (pid 4242)") || !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("kept line %q; want the name and the holder kept around what was scrubbed", line)
	}
}

func TestSettleStoredMappingScrubsTheChangedRecordLine(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stderr := captureReapStderr(t)
	installFakeInstanceLock(t, &fakeInstanceLock{})
	gone := filepath.Join(root, "test-ws-token=abc123\x1b[31m")
	mapEphemeral(t, root, reapDeadSessionID, gone, true)
	m := readMapping(t, root, reapDeadSessionID)
	// The record on disk is no longer the one selection read.
	m.DispatchID = "d2d2d2d2-0000-4000-8000-000000000002"

	settleStoredMapping(root, t.TempDir(), m, nil)
	out := stderr.String()
	if leaks := scrubLeaks(out, "abc123", "\x1b"); len(leaks) > 0 {
		t.Fatalf("output %q carries %q", out, leaks)
	}
	if !strings.Contains(out, "niwa: kept test-ws-token=[redacted]: its session record changed") {
		t.Fatalf("output %q; want the changed-record line with the name scrubbed", out)
	}
}

func TestPass_RemovedNothingLineIsScrubbed(t *testing.T) {
	f := newPassFixture(t)
	f.appendConfig(t, "build_trees = [\"token=hunter2\"]\n")
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.SettingsInvalid || len(*f.destroyed) != 0 {
		t.Fatalf("result %+v, destroyed %v; want the settings refused and nothing removed", res, *f.destroyed)
	}
	out := f.stderr.String()
	if !strings.Contains(out, "niwa: removed nothing: the workspace's removal settings are invalid: ") {
		t.Fatalf("output %q; want the removed-nothing line", out)
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("output %q carries the secret", out)
	}
	if !strings.Contains(out, "token=[redacted]") {
		t.Fatalf("output %q; want the entry named with its secret redacted", out)
	}
}

func TestReapVerdictOnlyExitsNonZeroOnInvalidSettings(t *testing.T) {
	for _, args := range [][]string{{"--verdict-only"}, {"--verdict-only", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newPassFixture(t)
			t.Setenv("HOME", filepath.Dir(f.jobsDir))
			f.appendConfig(t, "build_trees = [\"target/*\"]\n")

			out, _, err := runReapCommand(t, f.root, args...)
			var exit *sessionattach.ExitCodeError
			if !errors.As(err, &exit) || exit.Code == 0 {
				t.Fatalf("error %v; want a non-zero exit", err)
			}
			if exit.Msg != "" {
				t.Errorf("exit message %q; want none, the removed-nothing line says why", exit.Msg)
			}
			if out != "" {
				t.Errorf("stdout %q; want nothing when no verdict was reached", out)
			}
			if !strings.Contains(f.stderr.String(), "niwa: removed nothing: the workspace's removal settings are invalid") {
				t.Errorf("stderr %q; want the removed-nothing line", f.stderr.String())
			}
			if len(*f.destroyed) != 0 {
				t.Errorf("destroyed %v", *f.destroyed)
			}
		})
	}
}
