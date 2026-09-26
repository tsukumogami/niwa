package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The activity rule is the reclamation path for an agent that never deletes a
// session's record. Two facts answer it: when the record was last appended to,
// and whether a writer holds its lock right now. The staleness half is what
// makes an instance reclaimable at all; the lock half is the only thing
// standing between a live worker and a destroyed working directory when its
// turn runs longer than the grace period, which is exactly the shape of the
// data-loss bug the background-dispatch work had to fix for the other agent.
//
// Every test here drives a real record store and a real flock, under a fixture
// home. Nothing stubs the probe: a fake would assert that this file's idea of a
// lock matches itself, and the entire claim is about what the kernel does.

// activityAgent returns the agent whose declaration says its liveness is read
// from record activity, or skips. Nothing here names an agent: the tests follow
// whichever one declares the kind, so a second agent adopting it arrives
// covered and a first one abandoning it does not leave these silently passing
// against nothing.
func activityAgent(t *testing.T) (agent.Agent, agentplan.SessionRecords) {
	t.Helper()
	for _, ag := range agentplan.LaunchableAgents() {
		spec, ok := agentplan.For(ag).LaunchSpec()
		if !ok {
			continue
		}
		if spec.Records.Liveness == agentplan.LivenessRecordActivity {
			return ag, spec.Records
		}
	}
	t.Skip("no launchable agent declares LivenessRecordActivity")
	return "", agentplan.SessionRecords{}
}

// activitySessionID is the id the fixture record carries, and the name the lock
// file is built from. It is a valid session id because the reader rejects one
// that is not.
const activitySessionID = "01a00000-0000-7000-8000-0000000000bb"

// ensureLockDir creates the lock directory the way a real store has one.
//
// This is fixture realism rather than decoration. The agent creates that
// directory at the first session bootstrap and never removes it -- it holds a
// coordination lock that outlives every session -- so a store with any rollout
// in it has one. A fixture that wrote records without it would be modelling a
// state that cannot occur, and would then exercise the probe's
// missing-directory branch, which exists to say "this build is looking in the
// wrong place" and correctly declines to answer.
func ensureLockDir(t *testing.T, r agentplan.SessionRecords, home string) string {
	t.Helper()
	dir := writerLockDir(r, home, func(string) string { return "" })
	if dir == "" {
		t.Fatal("the session records resolve to no writer-lock directory under the fixture home")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	return dir
}

// writeActivityRecord writes one session record for cwd and backdates it, so a
// test can say "last worked in this long ago" rather than sleeping.
func writeActivityRecord(t *testing.T, r agentplan.SessionRecords, home, cwd string, touched time.Time) string {
	t.Helper()
	ensureLockDir(t, r, home)
	root := recordStoreRoot(r, home, func(string) string { return "" })
	if root == "" {
		t.Fatal("the session records resolve to no root under the fixture home")
	}
	dir := root
	for i := 0; i < r.Depth; i++ {
		dir = filepath.Join(dir, fmt.Sprintf("d%d", i))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir record dir: %v", err)
	}

	doc := map[string]any{}
	setAtPath(doc, r.IDPath, activitySessionID)
	setAtPath(doc, r.CwdPath, cwd)
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}
	body := string(encoded)
	if r.FirstLineOnly {
		body += "\n{\"ordinal\":1}"
	}
	name := r.FileName
	if name == "" {
		name = strings.ReplaceAll(r.FileGlob, "*", activitySessionID)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatalf("backdating the record: %v", err)
	}
	return path
}

// holdWriterLock creates the session's lock file and takes the same advisory
// lock the agent takes, held until the test ends.
//
// It is a real flock rather than a stub because that is the entire mechanism
// under test. The lock is taken on a separate open file description from any
// the probe will use, so the probe contends with it exactly as it would with
// another process: flock associates the lock with the description, not with the
// process.
func holdWriterLock(t *testing.T, r agentplan.SessionRecords, home string) string {
	t.Helper()
	dir := ensureLockDir(t, r, home)
	path := filepath.Join(dir, activitySessionID+r.WriterLockSuffix)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the writer lock: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatalf("taking the writer lock: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return path
}

// TestActivity_StaleRecordButLockHeld_Spared is the case that would have caught
// the data-loss bug for this rule, and the reason the lock probe exists at all.
//
// A single turn can run far longer than the grace period without appending
// anything to the record -- a long build, a long sleep, a long wait on a tool --
// so staleness on its own would reclaim the directory a worker is writing in.
// The record here is a day past the grace period and a writer holds its lock,
// which is what a live worker mid-turn looks like from outside. Deleting the
// lock probe makes this test destroy an instance.
func TestActivity_StaleRecordButLockHeld_Spared(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-held")
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-24*time.Hour))
	holdWriterLock(t, records, home)

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	targets, _, err := selectReapTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("selected %d target(s), want 0: a writer holds the session's lock, so a worker is in that directory now", len(targets))
	}
	if _, err := reapWorkspace(root, t.TempDir(), now); err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
	if _, err := workspace.ReadSessionMapping(root, activitySessionID); err != nil {
		t.Fatalf("the mapping must survive: %v", err)
	}
}

// TestActivity_StaleRecordNoLock_Reclaimed is the other half, and without it
// the rule above would be indistinguishable from sparing everything.
//
// Nobody has worked in the session for longer than the grace period and no
// writer is attached. There is no lock file at all, which is what a cleanly
// finished session leaves behind -- the agent unlinks it on exit.
func TestActivity_StaleRecordNoLock_Reclaimed(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-stale")
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Minute))

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	n, err := reapWorkspace(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1: the session was last worked in past the grace period with no writer attached", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
	if _, err := workspace.ReadSessionMapping(root, activitySessionID); err == nil {
		t.Fatal("the mapping outlived the instance it points at")
	}
}

// TestActivity_AbandonedLockFile_Reclaimed covers what a killed worker leaves
// behind: the lock file still on disk with nothing holding it.
//
// The kernel drops an advisory lock when its holder dies, so the file is
// litter rather than a claim. A check that tested for the file's existence
// instead of probing it would spare this instance forever, which is the leak
// this whole rule exists to end.
func TestActivity_AbandonedLockFile_Reclaimed(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-litter")
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Minute))

	// The file, with no lock on it: exactly what is left when a worker is
	// killed before it can unlink.
	dir := writerLockDir(records, home, func(string) string { return "" })
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, activitySessionID+records.WriterLockSuffix)
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	n, err := reapWorkspace(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1: an unheld lock file is litter from a killed worker, not a live session", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestActivity_RecentlyWorkedIn_Spared pins the cheap half of the rule: a
// session touched within the grace period is spared without the lock ever being
// probed.
//
// The probe is deliberately not free of consequence -- taking the lock, however
// briefly, can collide with the agent's own attempt and fail a concurrent
// resume -- so "recent" has to short-circuit. This asserts the outcome; the
// ordering that produces it is asserted directly below.
func TestActivity_RecentlyWorkedIn_Spared(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-fresh")
	writeActivityRecord(t, records, home, inst, now.Add(-time.Minute))

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	n, err := reapWorkspace(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if n != 0 || len(*destroyed) != 0 {
		t.Fatalf("reaped %d (%v), want 0: the session was worked in a minute ago", n, *destroyed)
	}
}

// TestActivity_FreshRecordIsNotProbed asserts the ordering rather than its
// outcome, because the ordering is a safety property and an outcome test cannot
// see it.
//
// A record inside the grace period must be answered by its timestamp alone. If
// the lock were probed anyway, every sweep would take and drop the lock of
// every live session on the host, and each of those is a chance to make a
// concurrent resume fail with a store conflict. The probe is detected by giving
// it a lock path it cannot open silently: a directory where the lock file
// should be. Opening that fails with a kind of error the probe reports as
// unreadable, so a probe that ran would turn this into an unreadable verdict
// rather than a live one.
func TestActivity_FreshRecordIsNotProbed(t *testing.T) {
	_, records := activityAgent(t)

	home := t.TempDir()
	now := time.Now()

	dir := writerLockDir(records, home, func(string) string { return "" })
	if err := os.MkdirAll(filepath.Join(dir, activitySessionID+records.WriterLockSuffix), 0o700); err != nil {
		t.Fatal(err)
	}

	rec := sessionRecord{ID: activitySessionID, Cwd: t.TempDir(), Touched: now.Add(-time.Minute)}
	live, known := recordActivityLive(records, rec, home, func(string) string { return "" }, now, defaultReapIdleGrace)
	if !known || !live {
		t.Fatalf("live=%v known=%v, want live and known: a record inside the grace period is answered without probing the lock", live, known)
	}

	// The control: the same store, the same unopenable lock path, a record just
	// outside the grace period. Now the probe does run, and cannot answer.
	stale := sessionRecord{ID: activitySessionID, Cwd: rec.Cwd, Touched: now.Add(-defaultReapIdleGrace - time.Minute)}
	if _, known := recordActivityLive(records, stale, home, func(string) string { return "" }, now, defaultReapIdleGrace); known {
		t.Fatal("a stale record was answered without the lock being probed, so the check above proves nothing")
	}
}

// TestActivity_UnreadableLock_SparesAndReports covers the verdict that is
// neither live nor gone.
//
// A probe that cannot answer must not be read as "no writer". The instance is
// spared, and because a developer cannot otherwise tell a sweep that declined
// from a sweep that found nothing, it is spared out loud.
func TestActivity_UnreadableLock_SparesAndReports(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-unreadable")
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Minute))

	// A directory where the lock file belongs: the probe's open fails with
	// something that is not "no such file", which is the unreadable case.
	dir := writerLockDir(records, home, func(string) string { return "" })
	if err := os.MkdirAll(filepath.Join(dir, activitySessionID+records.WriterLockSuffix), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	targets, spared, err := selectReapTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("selected %d target(s), want 0: an unanswerable probe is not evidence a session is gone", len(targets))
	}
	if len(spared) != 1 {
		t.Fatalf("spared %d, want 1: a sweep that declines silently looks exactly like a sweep that found nothing", len(spared))
	}
	if spared[0].Name != filepath.Base(inst) {
		t.Errorf("spared name = %q, want %q", spared[0].Name, filepath.Base(inst))
	}
	if !strings.Contains(spared[0].Reason, string(ag)) {
		t.Errorf("spared reason = %q, want it to name %s", spared[0].Reason, ag)
	}
	if _, err := reapWorkspace(root, t.TempDir(), now); err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestActivity_BackstopSparesLiveQuietWorker carries the same property to the
// other half of the reaper.
//
// An unmapped instance past the backstop's own age limit, whose session has
// been quiet longer than the grace period but whose lock a writer holds, is the
// detached-worker case: niwa died before writing the mapping, the worker
// outlived it, and the backstop is the only thing that will ever look at that
// directory again.
func TestActivity_BackstopSparesLiveQuietWorker(t *testing.T) {
	_, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })
	t.Setenv("HOME", home)

	inst := makeReapInstance(t, root, dispInstOld)
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Hour))
	holdWriterLock(t, records, home)

	destroyed := stubDestroyAll(t)
	n, err := reapBackstop(root, defaultJobsDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop: %v", err)
	}
	if n != 0 || len(*destroyed) != 0 {
		t.Fatalf("reaped %d (%v), want 0: a writer is attached to the session rooted in that directory", n, *destroyed)
	}
}

// writeActivityRecordFor is writeActivityRecord for a session other than the
// mapped one, so a test can put a second session's record in the same
// directory.
func writeActivityRecordFor(t *testing.T, r agentplan.SessionRecords, home, cwd, sessionID string, touched time.Time) {
	t.Helper()
	ensureLockDir(t, r, home)
	root := recordStoreRoot(r, home, func(string) string { return "" })
	dir := root
	for i := 0; i < r.Depth; i++ {
		dir = filepath.Join(dir, fmt.Sprintf("d%d", i))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir record dir: %v", err)
	}
	doc := map[string]any{}
	setAtPath(doc, r.IDPath, sessionID)
	setAtPath(doc, r.CwdPath, cwd)
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if r.FirstLineOnly {
		body += "\n{\"ordinal\":1}"
	}
	name := r.FileName
	if name == "" {
		name = strings.ReplaceAll(r.FileGlob, "*", sessionID)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
	if err := os.Chtimes(path, touched, touched); err != nil {
		t.Fatalf("backdating: %v", err)
	}
}

// TestActivity_AnotherSessionsRecordDoesNotAnswerForThisOne pins that the
// verdict is about the session the mapping names.
//
// A directory can hold more than one session's record: somebody opening an
// instance to see what is in it starts a second one, and the agent records it
// with the same working directory. Correlating on the directory alone -- which
// is how the dispatch capture identifies a session, correctly, because it has
// just made a directory nothing else was using -- would answer about whichever
// record turned up and report it as the mapped session's fate.
//
// Here the mapped session has no readable record and a stranger's is stale.
// Answering from the stranger reclaims an instance whose own session was never
// examined.
func TestActivity_AnotherSessionsRecordDoesNotAnswerForThisOne(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-stranger")
	const otherSessionID = "01a00000-0000-7000-8000-0000000000cc"
	writeActivityRecordFor(t, records, home, inst, otherSessionID, now.Add(-defaultReapIdleGrace-time.Hour))

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	targets, spared, err := selectReapTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("selected %d target(s), want 0: the only stale record belongs to a different session, so the mapped one's liveness was never read", len(targets))
	}
	if len(spared) != 1 {
		t.Fatalf("spared %d, want 1: a mapping its agent's records do not describe is a question the sweep cannot answer, and it has to say so", len(spared))
	}
	if _, err := reapWorkspace(root, t.TempDir(), now); err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestActivity_SecondSessionInTheInstanceStaysReclaimable is the same fix seen
// from the other side.
//
// Two records rooted in one instance used to make it permanently unreclaimable:
// the correlation declined on ambiguity and reported it as a mapping its
// agent's records no longer described, which was the opposite of true -- two
// records described it. Matching the mapped id picks the right one, so the leak
// this rule exists to close does not come back for any instance somebody looked
// inside.
func TestActivity_SecondSessionInTheInstanceStaysReclaimable(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-two")
	stale := now.Add(-defaultReapIdleGrace - time.Hour)
	writeActivityRecord(t, records, home, inst, stale)
	writeActivityRecordFor(t, records, home, inst, "01a00000-0000-7000-8000-0000000000cc", stale)

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	n, err := reapWorkspace(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1: both sessions in that directory are past the grace period with no writer attached", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestActivity_BackstopUnreadableProbe_SparesAndReports covers the backstop's
// third answer, which the mapped path has a test for and this one did not.
//
// An unmapped instance past the backstop's age limit, whose session is stale
// and whose lock cannot be probed, must be spared -- an unanswerable question
// is not evidence a worker has gone -- and it must be reported, because this is
// the half of the sweep where nothing else will speak for the directory.
func TestActivity_BackstopUnreadableProbe_SparesAndReports(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })
	t.Setenv("HOME", home)

	inst := makeReapInstance(t, root, dispInstOld)
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Hour))

	// A directory where the lock file belongs: the open fails with something
	// that is not "no such file", so the probe cannot answer.
	dir := writerLockDir(records, home, func(string) string { return "" })
	if err := os.MkdirAll(filepath.Join(dir, activitySessionID+records.WriterLockSuffix), 0o700); err != nil {
		t.Fatal(err)
	}

	destroyed := stubDestroyAll(t)
	targets, spared, err := selectBackstopTargets(root, defaultJobsDir(), now)
	if err != nil {
		t.Fatalf("selectBackstopTargets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("selected %d target(s), want 0: an unanswerable probe is not evidence the worker is gone", len(targets))
	}
	if len(spared) != 1 {
		t.Fatalf("spared %d, want 1: on the backstop there is no mapping and no other line that would mention this directory", len(spared))
	}
	if !strings.Contains(spared[0].Reason, string(ag)) {
		t.Errorf("spared reason %q does not name the agent it is about", spared[0].Reason)
	}
	if n, err := reapBackstop(root, defaultJobsDir(), now); err != nil || n != 0 {
		t.Fatalf("reapBackstop = %d, %v; want 0 reaped and no error", n, err)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestActivity_BackstopReclaimsAbandonedWorker is the backstop's converse, and
// it is the whole point of the feature: before this rule, an instance any
// session of this agent had ever run in was spared by every future sweep for
// good.
func TestActivity_BackstopReclaimsAbandonedWorker(t *testing.T) {
	_, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })
	t.Setenv("HOME", home)

	inst := makeReapInstance(t, root, dispInstOld)
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Hour))

	destroyed := stubDestroyAll(t)
	n, err := reapBackstop(root, defaultJobsDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped %d, want 1: nobody has worked in that session past the grace period and no writer is attached", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestReapIdleGrace_RejectsAValueThatWouldShortenIt pins the direction a
// misreading is allowed to go.
//
// The override exists so the rule can be watched acting without waiting a day.
// A typo in it must not silently shorten the window before instances are
// destroyed, so anything that will not parse, or is not positive, falls back to
// the default rather than to zero.
func TestReapIdleGrace_RejectsAValueThatWouldShortenIt(t *testing.T) {
	for _, bad := range []string{"", "   ", "soon", "24", "-1h", "0s", "0"} {
		t.Setenv("NIWA_REAP_IDLE_GRACE", bad)
		if got := reapIdleGrace(); got != defaultReapIdleGrace {
			t.Errorf("NIWA_REAP_IDLE_GRACE=%q gave %s, want the %s default", bad, got, defaultReapIdleGrace)
		}
	}
	t.Setenv("NIWA_REAP_IDLE_GRACE", "90m")
	if got := reapIdleGrace(); got != 90*time.Minute {
		t.Errorf("NIWA_REAP_IDLE_GRACE=90m gave %s, want 90m", got)
	}
}

// TestWriterLockHeld_ReportsWhatTheKernelDoes is direct coverage of the probe,
// against a real lock rather than a description of one.
func TestWriterLockHeld_ReportsWhatTheKernelDoes(t *testing.T) {
	dir := t.TempDir()

	// Absent: the agent unlinks the file when the writer detaches, so this is
	// an answer and not a failure to look.
	if held, known := writerLockHeld(filepath.Join(dir, "gone.lock")); held || !known {
		t.Errorf("absent lock: held=%v known=%v, want not held and known", held, known)
	}

	// Present and unheld: what a killed worker leaves behind.
	litter := filepath.Join(dir, "litter.lock")
	if err := os.WriteFile(litter, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if held, known := writerLockHeld(litter); held || !known {
		t.Errorf("unheld lock file: held=%v known=%v, want not held and known", held, known)
	}

	// Held by another open file description, which is how flock scopes a lock.
	f, err := os.OpenFile(litter, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("taking the lock: %v", err)
	}
	if held, known := writerLockHeld(litter); !held || !known {
		t.Errorf("held lock: held=%v known=%v, want held and known", held, known)
	}

	// Released by the holder: the probe reports the change rather than a cached
	// answer.
	//
	// This does not prove the probe releases what it takes, and it is worth
	// being exact about that rather than letting the name imply otherwise.
	// Deleting the explicit LOCK_UN from writerLockHeld fails nothing here,
	// because the deferred close drops the lock anyway -- the property holds
	// for a reason no assertion at this level can distinguish. The explicit
	// release stays because it says what is meant at the point it happens, not
	// because a test caught its absence.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("releasing: %v", err)
	}
	if held, known := writerLockHeld(litter); held || !known {
		t.Errorf("after release: held=%v known=%v, want not held and known", held, known)
	}

	// A missing lock DIRECTORY is not a missing lock file, and the open cannot
	// tell them apart -- both are ENOENT. This is the one place where reading
	// the wrong one resolves toward destroying something: if the agent renamed
	// that directory, or a permission change hid it, every session would read
	// as having no writer and the guard would retire itself without a symptom.
	if held, known := writerLockHeld(filepath.Join(dir, "nosuchdir", "any.lock")); held || known {
		t.Errorf("missing lock directory: held=%v known=%v, want not held and NOT known -- a store this build cannot find is not a store with nobody writing in it", held, known)
	}

	// Unanswerable: a directory where a lock file belongs.
	weird := filepath.Join(dir, "weird.lock")
	if err := os.MkdirAll(weird, 0o700); err != nil {
		t.Fatal(err)
	}
	if held, known := writerLockHeld(weird); held || known {
		t.Errorf("unopenable lock path: held=%v known=%v, want not held and not known", held, known)
	}

	// An empty path is not a path to try.
	if held, known := writerLockHeld(""); held || known {
		t.Errorf("empty lock path: held=%v known=%v, want not held and not known", held, known)
	}
}

// TestWriterLockDir_FollowsTheAgentsOwnHomeOverride pins that the lock
// directory tracks the agent's relocatable state directory rather than a path
// built from the developer's home alone. A probe that missed the override would
// look in the wrong place and report every session as having no writer, which
// fails in the reclaiming direction.
func TestWriterLockDir_FollowsTheAgentsOwnHomeOverride(t *testing.T) {
	_, records := activityAgent(t)
	if records.HomeEnv == "" {
		t.Skip("this agent's state directory cannot be relocated")
	}

	const override = "/somewhere/else"
	got := writerLockDir(records, "/home/someone", func(name string) string {
		if name == records.HomeEnv {
			return override
		}
		return ""
	})
	want := filepath.Join(append([]string{override}, records.WriterLockPath...)...)
	if got != want {
		t.Errorf("writerLockDir with %s set = %q, want %q", records.HomeEnv, got, want)
	}

	// With no override the lock directory is a sibling of the record store,
	// under the agent's own directory in home -- not a child of the records.
	got = writerLockDir(records, "/home/someone", func(string) string { return "" })
	want = filepath.Join(append([]string{"/home/someone", records.HomePath[0]}, records.WriterLockPath...)...)
	if got != want {
		t.Errorf("writerLockDir with no override = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, recordStoreRoot(records, "/home/someone", func(string) string { return "" })) {
		t.Errorf("the lock directory %q sits inside the record store; it is a sibling of it", got)
	}
}

// TestActivity_RecordWithATraversingIDIsNotProbed covers the one input in this
// path that niwa did not write.
//
// The lock file is named for the session id, and the id is read out of a record
// in the agent's own store -- a directory anything running as the developer can
// drop a file into. An id carrying path separators would build a path outside
// the lock directory, and the probe would then open and briefly lock whatever
// is there. The id is validated before it is joined, so such a record is
// declined instead: not live, and not proven gone either.
func TestActivity_RecordWithATraversingIDIsNotProbed(t *testing.T) {
	_, records := activityAgent(t)

	home := t.TempDir()
	now := time.Now()
	stale := now.Add(-defaultReapIdleGrace - time.Hour)

	// A file outside the lock directory that a traversing id would reach. If
	// the probe opened it, the test below would read it as an answer.
	outside := filepath.Join(home, "outside.lock")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// The lock directory exists, as it does in any real store, so a declined id
	// is declined for being an id rather than for the store looking wrong.
	dir := ensureLockDir(t, records, home)
	rel, err := filepath.Rel(dir, outside)
	if err != nil {
		t.Fatal(err)
	}
	traversing := strings.TrimSuffix(rel, records.WriterLockSuffix)

	for _, id := range []string{traversing, "../escape", "not-a-uuid", ""} {
		rec := sessionRecord{ID: id, Cwd: t.TempDir(), Touched: stale}
		live, known := recordActivityLive(records, rec, home, func(string) string { return "" }, now, defaultReapIdleGrace)
		if live || known {
			t.Errorf("record id %q: live=%v known=%v, want neither -- an id that is not a session id must not become a lock path", id, live, known)
		}
	}

	// The control: the same store and the same staleness with a well-formed id
	// does reach the probe, so the check above is rejecting the id rather than
	// failing for some unrelated reason.
	rec := sessionRecord{ID: activitySessionID, Cwd: t.TempDir(), Touched: stale}
	if _, known := recordActivityLive(records, rec, home, func(string) string { return "" }, now, defaultReapIdleGrace); !known {
		t.Fatal("a well-formed id did not reach the probe, so the rejections above prove nothing")
	}
}

// TestActivity_ReclamationOnIdlenessIsAnnounced binds the one reclamation that
// follows nothing the developer did.
//
// Deleting a session and watching its instance go is the outcome somebody asked
// for, and a count covers it. Walking away and finding the directory gone is
// not, and learning it from a resume into a missing directory is the failure
// this line exists to prevent. So this reclamation names the instance and the
// rule that took it.
func TestActivity_ReclamationOnIdlenessIsAnnounced(t *testing.T) {
	ag, records := activityAgent(t)

	root := setupHookWorkspace(t, true)
	now := time.Now()
	home := t.TempDir()
	prevHome := userHomeDir
	userHomeDir = func() string { return home }
	t.Cleanup(func() { userHomeDir = prevHome })

	inst := makeReapInstance(t, root, "test-ws-activity-announced")
	writeActivityRecord(t, records, home, inst, now.Add(-defaultReapIdleGrace-time.Hour))

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    activitySessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(ag),
	}); err != nil {
		t.Fatal(err)
	}

	targets, _, err := selectReapTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("selected %d target(s), want 1", len(targets))
	}
	notice := targets[0].Notice
	if notice.Reason == "" {
		t.Fatal("an instance reclaimed for idleness carried no explanation; the developer's only other way to find out is a resume into a directory that is gone")
	}
	// The name the destroy command would take, not the path, for the same
	// reason the sparing report uses it.
	if notice.Name != filepath.Base(inst) {
		t.Errorf("notice names %q, want the instance name %q", notice.Name, filepath.Base(inst))
	}
	if !strings.Contains(notice.Reason, string(ag)) {
		t.Errorf("notice reason %q does not name the agent whose rule reclaimed it", notice.Reason)
	}
	// How long it sat, so the reader knows which knob turns it. A reason that
	// said only "idle" would not.
	if !strings.Contains(notice.Reason, defaultReapIdleGrace.String()) {
		t.Errorf("notice reason %q does not say how long the session went untouched", notice.Reason)
	}
}

// TestPresenceReclamationIsNotAnnounced is the control. A session the developer
// deleted needs no per-instance line: they asked for it, and narrating every
// ordinary reclamation would be one more thing to learn to skim.
func TestPresenceReclamationIsNotAnnounced(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-presence-quiet")
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    reapDeadSessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
	}); err != nil {
		t.Fatal(err)
	}

	targets, _, err := selectReapTargets(root, jobsDir, now)
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("selected %d target(s), want 1", len(targets))
	}
	if targets[0].Notice.Reason != "" {
		t.Errorf("a deleted session's reclamation announced itself (%q); only the reclamation nobody asked for needs to", targets[0].Notice.Reason)
	}
}
