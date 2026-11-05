package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The per-instance lock tests.
//
// A "real lock" is the production seam under a temp HOME, with the test
// calling workspace.TryFileLock on workspace.InstanceLockPath itself to hold
// the lock or to try it from inside another fake seam. flock contends between
// two opens in one process, so a try made from inside a seam sees the
// command's own hold. A "fake lock" replaces tryInstanceLockFunc to report the
// lock held, unsupported or failing, or to run a hook when it is granted and
// when it is released.

const lockTestHolder = "niwa retask (pid 4242)"

// lockTestHome points HOME at a fresh temp directory, so the production lock
// file lands under the test's own ~/.niwa/locks.
func lockTestHome(t *testing.T) string {
	t.Helper()
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	return home
}

// holdRealInstanceLock takes instancePath's real lock as another holder would
// and keeps it until the test ends.
func holdRealInstanceLock(t *testing.T, home, instancePath, holder string) {
	t.Helper()
	release, err := workspace.TryFileLock(workspace.InstanceLockPath(home, instancePath), holder)
	if err != nil {
		t.Fatalf("holding the instance lock: %v", err)
	}
	t.Cleanup(release)
}

// probeInstanceLock tries instancePath's real lock once and releases it at
// once when it was granted, returning what the try saw.
func probeInstanceLock(home, instancePath string) error {
	release, err := workspace.TryFileLock(workspace.InstanceLockPath(home, instancePath), "probe")
	if err != nil {
		return err
	}
	release()
	return nil
}

// heldBy is the error workspace.TryFileLock returns for a lock holder holds.
func heldBy(holder string) error {
	return fmt.Errorf("%w by %s", workspace.ErrLockHeld, holder)
}

// fakeInstanceLock is the fake lock. errFor decides what an attempt returns
// (nil grants it); wrapReal also takes the production lock under a grant, so
// another taker contends with it; onAcquire runs just after a grant and
// onRelease just before the release lets the lock go.
type fakeInstanceLock struct {
	errFor    func(instancePath string) error
	wrapReal  bool
	onAcquire func(instancePath string)
	onRelease func(instancePath string)
	attempts  map[string]int
	releases  int
}

func installFakeInstanceLock(t *testing.T, f *fakeInstanceLock) {
	t.Helper()
	f.attempts = map[string]int{}
	prev := tryInstanceLockFunc
	tryInstanceLockFunc = func(instancePath, holder string) (func(), error) {
		f.attempts[filepath.Clean(instancePath)]++
		if f.errFor != nil {
			if err := f.errFor(instancePath); err != nil {
				return nil, err
			}
		}
		realRelease := func() {}
		if f.wrapReal {
			r, err := tryInstanceLock(instancePath, holder)
			if err != nil {
				return nil, err
			}
			realRelease = r
		}
		if f.onAcquire != nil {
			f.onAcquire(instancePath)
		}
		return func() {
			f.releases++
			if f.onRelease != nil {
				f.onRelease(instancePath)
			}
			realRelease()
		}, nil
	}
	t.Cleanup(func() { tryInstanceLockFunc = prev })
}

// lockErrorFor returns an errFor reporting err for every path in paths.
func lockErrorFor(err error, paths ...string) func(string) error {
	set := map[string]bool{}
	for _, p := range paths {
		set[filepath.Clean(p)] = true
	}
	return func(p string) error {
		if set[filepath.Clean(p)] {
			return err
		}
		return nil
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// ---- resume ----

// resumeSeams counts the resume seams a lock refusal must never reach, and
// lets a test run a hook inside each.
type resumeSeams struct {
	listed, provisioned, launched, captured int
	onProvision, onLaunch, onCapture        func()
	launchErr                               error
	realProvision                           bool
}

func installResumeSeams(t *testing.T, f *resumeFixture, s *resumeSeams) {
	t.Helper()
	prevList, prevProvision, prevLaunch, prevCapture := listClaudeSessionsFunc, provisionInstanceNamedFunc, resumeLaunchFunc, dispatchCapture
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) {
		s.listed++
		return f.sessions, f.listErr
	}
	provisionInstanceNamedFunc = func(ctx context.Context, root, name string) (provisionResult, error) {
		s.provisioned++
		if s.onProvision != nil {
			s.onProvision()
		}
		if s.realProvision {
			return prevProvision(ctx, root, name)
		}
		path := filepath.Join(root, name)
		if err := writeMinimalInstanceState(path); err != nil {
			return provisionResult{}, err
		}
		return provisionResult{Name: name, Path: path}, nil
	}
	resumeLaunchFunc = func(context.Context, string, []string, string, []string) error {
		s.launched++
		if s.onLaunch != nil {
			s.onLaunch()
		}
		return s.launchErr
	}
	dispatchCapture = func(agentplan.SessionRecords, string, string, time.Duration, func() time.Time, time.Duration) (string, string, error) {
		s.captured++
		if s.onCapture != nil {
			s.onCapture()
		}
		return resumeTestSessionID, "", nil
	}
	t.Cleanup(func() {
		listClaudeSessionsFunc, provisionInstanceNamedFunc, resumeLaunchFunc, dispatchCapture = prevList, prevProvision, prevLaunch, prevCapture
	})
}

func TestResume_InstanceLockHeld_Refuses(t *testing.T) {
	for _, present := range []bool{true, false} {
		t.Run(map[bool]string{true: "instance_present", false: "instance_destroyed"}[present], func(t *testing.T) {
			f := newResumeFixture(t, "")
			f.writeRecord(t, f.record())
			f.writeTranscript(t, resumeTestSessionID)
			if present {
				f.presentInstance(t)
			}
			var s resumeSeams
			installResumeSeams(t, f, &s)
			holdRealInstanceLock(t, f.home, f.instancePath(), lockTestHolder)

			start := time.Now()
			_, _, err := runResumeCmd(t, resumeTestInstance)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("resume succeeded while another command held the instance's lock")
			}
			if want := `niwa: error: cannot resume "` + resumeTestInstance + `":`; !strings.HasPrefix(err.Error(), want) {
				t.Errorf("error %q does not start with %q", err, want)
			}
			if !strings.Contains(err.Error(), lockTestHolder) {
				t.Errorf("error %q does not name the holder %q", err, lockTestHolder)
			}
			if elapsed >= time.Second {
				t.Errorf("resume took %s to refuse; a held lock must refuse without waiting", elapsed)
			}
			if s.listed != 0 || s.provisioned != 0 || s.launched != 0 {
				t.Errorf("seams reached under a held lock: list=%d provision=%d launch=%d", s.listed, s.provisioned, s.launched)
			}
			if _, err := workspace.ReadSessionMapping(f.root, resumeTestSessionID); err == nil {
				t.Error("a session mapping was written although resume refused")
			}
			if got := exists(f.instancePath()); got != present {
				t.Errorf("instance present = %v after the refusal, want %v", got, present)
			}
		})
	}
}

func TestResume_HoldsInstanceLockThroughLaunchAndCapture(t *testing.T) {
	f := newResumeFixture(t, "")
	f.writeRecord(t, f.record())
	f.writeTranscript(t, resumeTestSessionID)
	// The instance is absent, so resume provisions it.
	seen := map[string]error{}
	probe := func(step string) func() {
		return func() { seen[step] = probeInstanceLock(f.home, f.instancePath()) }
	}
	s := resumeSeams{onProvision: probe("provision"), onLaunch: probe("launch"), onCapture: probe("capture")}
	installResumeSeams(t, f, &s)

	if _, stderr, err := runResumeCmd(t, resumeTestInstance); err != nil {
		t.Fatalf("resume: %v\nstderr:\n%s", err, stderr)
	}
	for _, step := range []string{"provision", "launch", "capture"} {
		err, ok := seen[step]
		if !ok {
			t.Errorf("%s never ran", step)
			continue
		}
		if !errors.Is(err, workspace.ErrLockHeld) {
			t.Errorf("a try of the instance's lock during %s saw %v, want ErrLockHeld", step, err)
		}
	}
	if err := probeInstanceLock(f.home, f.instancePath()); err != nil {
		t.Errorf("the instance's lock is still held after resume returned: %v", err)
	}
}

func TestResume_RollbackDestroyRunsUnderOwnLock(t *testing.T) {
	f := newResumeFixture(t, "")
	f.writeRecord(t, f.record())
	f.writeTranscript(t, resumeTestSessionID)
	launchErr := errors.New("launch failed for the test")
	s := resumeSeams{launchErr: launchErr, realProvision: true}
	installResumeSeams(t, f, &s)

	_, stderr, err := runResumeCmd(t, resumeTestInstance)
	if err == nil {
		t.Fatal("resume succeeded although its launch failed")
	}
	if !errors.Is(err, launchErr) {
		t.Errorf("resume returned %v, want the launch error", err)
	}
	if strings.Contains(err.Error(), "lock") {
		t.Errorf("resume returned a lock error: %v", err)
	}
	if s.provisioned != 1 {
		t.Fatalf("provisioned %d times, want 1 (the instance must be restored first)", s.provisioned)
	}
	if exists(f.instancePath()) {
		t.Errorf("the restored instance %s survived the rollback\nstderr:\n%s", f.instancePath(), stderr)
	}
	if err := probeInstanceLock(f.home, f.instancePath()); err != nil {
		t.Errorf("the instance's lock is still held after resume returned: %v", err)
	}
}

// ---- destroy ----

func TestDestroy_InstanceLockHeld_KeepsInstance(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, inside := range []bool{false, true} {
			name := fmt.Sprintf("force=%v/%s", force, map[bool]string{false: "by_name", true: "from_inside"}[inside])
			t.Run(name, func(t *testing.T) {
				home := lockTestHome(t)
				root := destroyTestSetup(t, []string{"alpha", "beta"})
				instance := filepath.Join(root, "alpha")
				mapNamedSession(t, root, ownerSID, instance, "fix-thing")
				fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}}
				installFakeClaudeSessions(t, &fake)
				tmp := t.TempDir()
				t.Setenv("TMPDIR", tmp)
				respFile := filepath.Join(tmp, "niwa-response")
				withResponseFile(t, respFile)
				quietDestroyCmd()
				setDestroyFlags(t, force, false, "")
				holdRealInstanceLock(t, home, instance, lockTestHolder)

				var args []string
				if inside {
					chdirTo(t, instance)
				} else {
					chdirTo(t, root)
					args = []string{"alpha"}
				}
				start := time.Now()
				err := runDestroy(destroyCmd, args)
				elapsed := time.Since(start)
				if err == nil {
					t.Fatal("destroy succeeded while another command held the instance's lock")
				}
				for _, want := range []string{"alpha", lockTestHolder} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %q", err, want)
					}
				}
				if elapsed >= time.Second {
					t.Errorf("destroy took %s to refuse; a held lock must refuse without waiting", elapsed)
				}
				assertInstancesExist(t, root, "alpha", "beta")
				if !mappingExists(t, root, ownerSID) {
					t.Error("the instance's session mapping was removed")
				}
				if fake.listCalls != 0 || len(fake.removed) != 0 {
					t.Errorf("claude reached under a held lock: list=%d rm=%v", fake.listCalls, fake.removed)
				}
				if exists(respFile) {
					t.Error("a landing path was written although destroy refused")
				}
			})
		}
	}
}

func TestDestroy_HoldsInstanceLockThroughRemoval(t *testing.T) {
	t.Run("no_taker_between_check_and_rm", func(t *testing.T) {
		home := lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha"})
		instance := filepath.Join(root, "alpha")
		writeTestMapping(t, root, ownerSID, instance, "")
		seen := map[string]error{}
		prevList, prevRemove := listClaudeSessionsFunc, removeClaudeSessionFunc
		listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) {
			seen["list"] = probeInstanceLock(home, instance)
			return []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}, nil
		}
		removeClaudeSessionFunc = func(context.Context, string) (string, error) {
			seen["rm"] = probeInstanceLock(home, instance)
			return "", nil
		}
		t.Cleanup(func() { listClaudeSessionsFunc, removeClaudeSessionFunc = prevList, prevRemove })
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		for _, step := range []string{"list", "rm"} {
			if err, ok := seen[step]; !ok || !errors.Is(err, workspace.ErrLockHeld) {
				t.Errorf("a try of the instance's lock during %s saw %v (ran=%v), want ErrLockHeld", step, err, ok)
			}
		}
		if exists(instance) {
			t.Error("the instance was not destroyed")
		}
	})

	t.Run("released_after_removal", func(t *testing.T) {
		lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha"})
		instance := filepath.Join(root, "alpha")
		installFakeClaudeSessions(t, &fakeClaudeSessions{})
		var atRelease error
		releaseRan := false
		installFakeInstanceLock(t, &fakeInstanceLock{onRelease: func(string) {
			releaseRan = true
			_, atRelease = os.Lstat(instance)
		}})
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		if !releaseRan {
			t.Fatal("the lock was never released")
		}
		if !errors.Is(atRelease, fs.ErrNotExist) {
			t.Errorf("the instance directory was still there when the lock was released (Lstat: %v)", atRelease)
		}
	})
}

func TestDestroyWorkspace_AnyInstanceLockHeld_RefusesWhole(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		home := lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha", "beta", "gamma"})
		for _, n := range []string{"alpha", "beta", "gamma"} {
			writeTestMapping(t, root, map[string]string{"alpha": ownerSID, "beta": otherSID, "gamma": "33333333-3333-4333-8333-333333333333"}[n], filepath.Join(root, n), "")
		}
		fake := fakeClaudeSessions{}
		installFakeClaudeSessions(t, &fake)
		holdRealInstanceLock(t, home, filepath.Join(root, "beta"), lockTestHolder)
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, true, "testws")

		err := runDestroy(destroyCmd, nil)
		if err == nil {
			t.Fatal("destroy --workspace succeeded while an instance's lock was held")
		}
		for _, want := range []string{"beta", lockTestHolder} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %q", err, want)
			}
		}
		assertInstancesExist(t, root, "alpha", "beta", "gamma")
		if !exists(root) {
			t.Error("the workspace root was removed")
		}
		if len(fake.removed) != 0 {
			t.Errorf("sessions removed under a held lock: %v", fake.removed)
		}
		// The locks taken before the refusal were let go.
		for _, n := range []string{"alpha", "gamma"} {
			if err := probeInstanceLock(home, filepath.Join(root, n)); err != nil {
				t.Errorf("instance %s's lock is still held after the refusal: %v", n, err)
			}
		}
	})

	t.Run("free", func(t *testing.T) {
		lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha", "beta", "gamma"})
		installFakeClaudeSessions(t, &fakeClaudeSessions{})
		chdirTo(t, root)
		t.Cleanup(func() { _ = os.Chdir(filepath.Dir(root)) })
		quietDestroyCmd()
		setDestroyFlags(t, false, true, "testws")

		if err := runDestroy(destroyCmd, nil); err != nil {
			t.Fatalf("destroy --workspace: %v", err)
		}
		if exists(root) {
			t.Error("the workspace root survived destroy --workspace")
		}
	})
}

func TestDestroy_HelpSaysForceDoesNotOverrideLock(t *testing.T) {
	long := strings.Join(strings.Fields(destroyCmd.Long), " ")
	if !strings.Contains(long, "--force does not override another niwa command holding the instance's lock") {
		t.Errorf("destroy's help does not say --force leaves a held lock alone:\n%s", destroyCmd.Long)
	}
	if !strings.Contains(long, "--force <name> prints the reasons as overridden and destroys the instance anyway") {
		t.Errorf("destroy's help no longer says what --force does override:\n%s", destroyCmd.Long)
	}
}

// ---- reap ----

func readMapping(t *testing.T, root, sid string) workspace.SessionMapping {
	t.Helper()
	m, err := workspace.ReadSessionMapping(root, sid)
	if err != nil {
		t.Fatalf("reading mapping %s: %v", sid, err)
	}
	return m
}

func TestReap_InstanceLockHeld_SparesThisSweep(t *testing.T) {
	lockTestHome(t)
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	held := makeReapInstance(t, root, "test-ws-held")
	free := makeReapInstance(t, root, "test-ws-free")
	mapEphemeral(t, root, reapDeadSessionID, held, true)
	mapEphemeral(t, root, reapLiveSessionID, free, true)
	before := readMapping(t, root, reapDeadSessionID)
	destroyed := stubDestroyAll(t)
	stderr := captureReapStderr(t)
	lock := fakeInstanceLock{errFor: lockErrorFor(heldBy(lockTestHolder), held)}
	installFakeInstanceLock(t, &lock)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if n != 1 {
		t.Errorf("reaped %d, want 1", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != free {
		t.Errorf("destroyed %v, want only %s", *destroyed, free)
	}
	if !exists(held) {
		t.Error("the held instance's directory is gone")
	}
	after := readMapping(t, root, reapDeadSessionID)
	if after.OnReapFailures != before.OnReapFailures {
		t.Errorf("OnReapFailures went from %d to %d", before.OnReapFailures, after.OnReapFailures)
	}
	var kept []string
	for _, l := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(l, "niwa: kept ") {
			kept = append(kept, l)
		}
	}
	if len(kept) != 1 || !strings.HasPrefix(kept[0], "niwa: kept test-ws-held: ") || !strings.Contains(kept[0], lockTestHolder) {
		t.Errorf("kept lines = %q, want one for test-ws-held naming %q", kept, lockTestHolder)
	}
	if got := lock.attempts[held]; got != 1 {
		t.Errorf("the held instance's lock was tried %d times, want exactly 1", got)
	}
}

func TestReap_RereadsLivenessUnderLockForEveryAgent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, jobsDir, inst string)
	}{
		{"session_came_back", func(t *testing.T, jobsDir, _ string) { writeJobEntry(t, jobsDir, reapDeadSessionID) }},
		{"other_job_rooted", func(t *testing.T, jobsDir, inst string) { writeJobStateCwd(t, jobsDir, "0000bb22", inst) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lockTestHome(t)
			root := setupHookWorkspace(t, true)
			jobsDir := t.TempDir()
			inst := makeReapInstance(t, root, "test-ws-dead")
			mapEphemeral(t, root, reapDeadSessionID, inst, true)
			destroyed := stubDestroyAll(t)
			installFakeInstanceLock(t, &fakeInstanceLock{onAcquire: func(string) { tc.change(t, jobsDir, inst) }})

			if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
				t.Fatalf("reapWorkspace: %v", err)
			}
			if len(*destroyed) != 0 {
				t.Errorf("destroyed %v after the world changed under the lock", *destroyed)
			}
			if !exists(inst) {
				t.Error("the instance is gone")
			}
			if !mappingExists(t, root, reapDeadSessionID) {
				t.Error("the mapping is gone")
			}
		})
	}
}

func TestReap_LockOrder(t *testing.T) {
	t.Run("holder_first", func(t *testing.T) {
		lockTestHome(t)
		root := setupHookWorkspace(t, true)
		inst := makeReapInstance(t, root, "test-ws-dead")
		mapEphemeral(t, root, reapDeadSessionID, inst, true)
		destroyed := stubDestroyAll(t)
		captureReapStderr(t)
		installFakeInstanceLock(t, &fakeInstanceLock{errFor: lockErrorFor(heldBy("niwa retask (pid 1)"), inst)})

		if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
			t.Fatalf("reapWorkspace: %v", err)
		}
		if len(*destroyed) != 0 {
			t.Errorf("destroyed %v while the holder had the lock", *destroyed)
		}
		if !exists(inst) || !mappingExists(t, root, reapDeadSessionID) {
			t.Errorf("instance present=%v mapping present=%v, want both", exists(inst), mappingExists(t, root, reapDeadSessionID))
		}
	})

	t.Run("reaper_first", func(t *testing.T) {
		home := lockTestHome(t)
		root := setupHookWorkspace(t, true)
		inst := makeReapInstance(t, root, "test-ws-dead")
		mapEphemeral(t, root, reapDeadSessionID, inst, true)
		stubDestroyRemoving(t)
		var dirAtRelease, mappingAtRelease, released bool
		installFakeInstanceLock(t, &fakeInstanceLock{wrapReal: true, onRelease: func(string) {
			released = true
			dirAtRelease = exists(inst)
			_, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
			mappingAtRelease = err == nil
		}})

		if n, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil || n != 1 {
			t.Fatalf("reapWorkspace = %d, %v; want 1, nil", n, err)
		}
		if !released {
			t.Fatal("the lock was never released")
		}
		if dirAtRelease || mappingAtRelease {
			t.Errorf("at release: directory present=%v mapping present=%v, want both gone", dirAtRelease, mappingAtRelease)
		}
		// A second taker gets the lock and finds nothing to act on.
		release, err := workspace.TryFileLock(workspace.InstanceLockPath(home, inst), "second taker")
		if err != nil {
			t.Fatalf("a second taker could not take the lock after the reap: %v", err)
		}
		defer release()
		if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
			t.Error("the second taker still finds the mapping")
		}
		if present, err := instancePresent(inst, filepath.Base(inst)); err != nil || present {
			t.Errorf("instancePresent = %v, %v; want false, nil", present, err)
		}
	})
}

func TestReapBackstop_InstanceLockHeld_Spares(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		lockTestHome(t)
		root := setupHookWorkspace(t, true)
		now := time.Now()
		inst := makeReapInstance(t, root, dispInstOld)
		writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
		stubDestroyRemoving(t)
		stderr := captureReapStderr(t)
		installFakeInstanceLock(t, &fakeInstanceLock{errFor: lockErrorFor(heldBy(lockTestHolder), inst)})

		n, err := reapBackstop(root, t.TempDir(), now)
		if err != nil || n != 0 {
			t.Fatalf("reapBackstop = %d, %v; want 0, nil", n, err)
		}
		if !exists(inst) {
			t.Error("the held instance was removed")
		}
		if !strings.Contains(stderr.String(), "niwa: kept "+dispInstOld+": ") || !strings.Contains(stderr.String(), lockTestHolder) {
			t.Errorf("no kept line naming the holder:\n%s", stderr.String())
		}
	})

	t.Run("job_rooted_under_lock", func(t *testing.T) {
		lockTestHome(t)
		root := setupHookWorkspace(t, true)
		jobsDir := t.TempDir()
		now := time.Now()
		inst := makeReapInstance(t, root, dispInstOld)
		writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
		stubDestroyRemoving(t)
		installFakeInstanceLock(t, &fakeInstanceLock{onAcquire: func(string) { writeJobStateCwd(t, jobsDir, "0000aa11", inst) }})

		n, err := reapBackstop(root, jobsDir, now)
		if err != nil || n != 0 {
			t.Fatalf("reapBackstop = %d, %v; want 0, nil", n, err)
		}
		if !exists(inst) {
			t.Error("an instance a job became rooted in under the lock was removed")
		}
	})
}

// ---- watch's stale-review discard ----

// stagePruneRecords stages two reviews under root: one whose session is gone
// and one live but stale (its PR is no longer requested). It returns their
// records.
func stagePruneRecords(t *testing.T, root string) []watch.StagedRecord {
	t.Helper()
	recs := []watch.StagedRecord{
		{Handle: "gone0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: makeReapInstance(t, root, "test-ws-gone"), DispatchedSHA: "aaaa111"},
		{Handle: "stale002", Owner: "acme", Repo: "api", Number: 2, InstancePath: makeReapInstance(t, root, "test-ws-stale"), DispatchedSHA: "bbbb222"},
	}
	for _, r := range recs {
		if err := watch.SaveStagedRecord(root, r); err != nil {
			t.Fatal(err)
		}
	}
	return recs
}

func TestPruneStagedRecords_InstanceLockHeld_KeepsRecord(t *testing.T) {
	lockTestHome(t)
	root := setupDispatchWorkspace(t)
	recs := stagePruneRecords(t, root)
	gateLog := declareLoggingGate(t, root, "/never-kept")
	liveCalls := 0
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, p string) bool { liveCalls++; return p == recs[1].InstancePath }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
	destroyed := stubDestroyAll(t)
	lock := fakeInstanceLock{errFor: lockErrorFor(heldBy(lockTestHolder), recs[0].InstancePath, recs[1].InstancePath)}
	installFakeInstanceLock(t, &lock)

	var out bytes.Buffer
	pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})

	if len(*destroyed) != 0 {
		t.Errorf("destroyed %v while the locks were held", *destroyed)
	}
	if liveCalls != 0 {
		t.Errorf("liveness was read %d times under a held lock", liveCalls)
	}
	if runs := gateRuns(t, gateLog); len(runs) != 0 {
		t.Errorf("the removal gate was asked under a held lock: %q", runs)
	}
	for _, r := range recs {
		if _, err := watch.LoadStagedRecord(root, r.Handle); err != nil {
			t.Errorf("record %s is gone: %v", r.Handle, err)
		}
		if !exists(r.InstancePath) {
			t.Errorf("instance %s is gone", r.InstancePath)
		}
	}
	var kept []string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "kept staged review") {
			kept = append(kept, l)
			if !strings.Contains(l, lockTestHolder) {
				t.Errorf("kept line %q does not name the holder", l)
			}
		}
	}
	if len(kept) != 2 {
		t.Errorf("kept lines = %q, want one per record", kept)
	}

	// With the lock free, the next pass discards both.
	lock.errFor = nil
	pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})
	if handles, _ := watch.ListStagedHandles(root); len(handles) != 0 {
		t.Errorf("records left after a free pass: %v", handles)
	}
	if len(*destroyed) != 2 {
		t.Errorf("destroyed %v after a free pass, want both instances", *destroyed)
	}
}

func TestPruneStagedRecords_HoldsInstanceLockThroughDiscard(t *testing.T) {
	t.Run("real_lock", func(t *testing.T) {
		home := lockTestHome(t)
		root := setupDispatchWorkspace(t)
		recs := stagePruneRecords(t, root)
		seen := map[string][]error{}
		origLive := stagedInstanceLiveFunc
		stagedInstanceLiveFunc = func(_, p string) bool {
			seen["live"] = append(seen["live"], probeInstanceLock(home, p))
			return false
		}
		t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
		origDestroy := destroyInstanceFunc
		destroyInstanceFunc = func(p string) error {
			seen["destroy"] = append(seen["destroy"], probeInstanceLock(home, p))
			return nil
		}
		t.Cleanup(func() { destroyInstanceFunc = origDestroy })

		pruneStagedRecords(context.Background(), &bytes.Buffer{}, root, &fakeFreshnessClient{}, map[string]bool{})

		for _, step := range []string{"live", "destroy"} {
			if len(seen[step]) != len(recs) {
				t.Errorf("%s ran %d times, want %d", step, len(seen[step]), len(recs))
			}
			for _, err := range seen[step] {
				if !errors.Is(err, workspace.ErrLockHeld) {
					t.Errorf("a try of the instance's lock during %s saw %v, want ErrLockHeld", step, err)
				}
			}
		}
	})

	t.Run("record_deleted_before_release", func(t *testing.T) {
		lockTestHome(t)
		root := setupDispatchWorkspace(t)
		recs := stagePruneRecords(t, root)
		byPath := map[string]string{}
		for _, r := range recs {
			byPath[filepath.Clean(r.InstancePath)] = r.Handle
		}
		origLive := stagedInstanceLiveFunc
		stagedInstanceLiveFunc = func(_, _ string) bool { return false }
		t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
		stubDestroyAll(t)
		var stillThere []string
		lock := fakeInstanceLock{onRelease: func(p string) {
			if _, err := watch.LoadStagedRecord(root, byPath[filepath.Clean(p)]); err == nil {
				stillThere = append(stillThere, byPath[filepath.Clean(p)])
			}
		}}
		installFakeInstanceLock(t, &lock)

		pruneStagedRecords(context.Background(), &bytes.Buffer{}, root, &fakeFreshnessClient{}, map[string]bool{})

		if lock.releases != len(recs) {
			t.Errorf("released %d times, want %d", lock.releases, len(recs))
		}
		if len(stillThere) != 0 {
			t.Errorf("records still staged when their lock was released: %v", stillThere)
		}
	})
}

// ---- shared behavior ----

// lockPathCase runs one path with the fake lock installed and reports what it
// did. Each builds its own fixture.
type lockPathCase struct {
	name string
	run  func(t *testing.T, lockErr error) (err error, out string, acted bool)
}

func lockPathCases() []lockPathCase {
	return []lockPathCase{
		{"resume", func(t *testing.T, lockErr error) (error, string, bool) {
			f := newResumeFixture(t, "")
			f.writeRecord(t, f.record())
			f.writeTranscript(t, resumeTestSessionID)
			f.presentInstance(t)
			var s resumeSeams
			installResumeSeams(t, f, &s)
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			stdout, stderr, err := runResumeCmd(t, resumeTestInstance)
			_, mapErr := workspace.ReadSessionMapping(f.root, resumeTestSessionID)
			return err, stdout + stderr, s.launched == 1 && mapErr == nil
		}},
		{"destroy", func(t *testing.T, lockErr error) (error, string, bool) {
			lockTestHome(t)
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			instance := filepath.Join(root, "alpha")
			writeTestMapping(t, root, ownerSID, instance, "")
			fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}}
			installFakeClaudeSessions(t, &fake)
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			chdirTo(t, root)
			stderr := quietDestroyCmd()
			setDestroyFlags(t, false, false, "")
			err := runDestroy(destroyCmd, []string{"alpha"})
			return err, stderr.String(), len(fake.removed) == 1 && !exists(instance)
		}},
		{"destroy_workspace", func(t *testing.T, lockErr error) (error, string, bool) {
			lockTestHome(t)
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			installFakeClaudeSessions(t, &fakeClaudeSessions{})
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			chdirTo(t, root)
			t.Cleanup(func() { _ = os.Chdir(filepath.Dir(root)) })
			stderr := quietDestroyCmd()
			setDestroyFlags(t, false, true, "testws")
			err := runDestroy(destroyCmd, nil)
			return err, stderr.String(), !exists(root)
		}},
		{"reap", func(t *testing.T, lockErr error) (error, string, bool) {
			lockTestHome(t)
			root := setupHookWorkspace(t, true)
			inst := makeReapInstance(t, root, "test-ws-dead")
			mapEphemeral(t, root, reapDeadSessionID, inst, true)
			destroyed := stubDestroyAll(t)
			stderr := captureReapStderr(t)
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			n, err := reapWorkspace(root, t.TempDir(), time.Now())
			return err, stderr.String(), n == 1 && len(*destroyed) == 1 && !mappingExists(t, root, reapDeadSessionID)
		}},
		{"backstop", func(t *testing.T, lockErr error) (error, string, bool) {
			lockTestHome(t)
			root := setupHookWorkspace(t, true)
			now := time.Now()
			inst := makeReapInstance(t, root, dispInstOld)
			writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
			stubDestroyRemoving(t)
			stderr := captureReapStderr(t)
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			n, err := reapBackstop(root, t.TempDir(), now)
			return err, stderr.String(), n == 1 && !exists(inst)
		}},
		{"discard", func(t *testing.T, lockErr error) (error, string, bool) {
			lockTestHome(t)
			root := setupDispatchWorkspace(t)
			recs := stagePruneRecords(t, root)
			origLive := stagedInstanceLiveFunc
			stagedInstanceLiveFunc = func(_, _ string) bool { return false }
			t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
			destroyed := stubDestroyAll(t)
			installFakeInstanceLock(t, &fakeInstanceLock{errFor: func(string) error { return lockErr }})
			var out bytes.Buffer
			pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})
			handles, _ := watch.ListStagedHandles(root)
			return nil, out.String(), len(*destroyed) == len(recs) && len(handles) == 0
		}},
	}
}

func TestInstanceLock_Unsupported_KeepsTodaysBehavior(t *testing.T) {
	for _, tc := range lockPathCases() {
		t.Run(tc.name, func(t *testing.T) {
			err, out, acted := tc.run(t, workspace.ErrLockUnsupported)
			if err != nil {
				t.Fatalf("%s failed without flock: %v\n%s", tc.name, err, out)
			}
			if !acted {
				t.Errorf("%s did not do what it does today without flock\n%s", tc.name, out)
			}
			if strings.Contains(out, "lock") {
				t.Errorf("%s printed a lock message without flock:\n%s", tc.name, out)
			}
		})
	}
}

func TestInstanceLock_OtherError_RefusesOrSpares(t *testing.T) {
	lockErr := errors.New("the lock directory is not owned by the current user")
	refuses := map[string]bool{"resume": true, "destroy": true, "destroy_workspace": true}
	for _, tc := range lockPathCases() {
		t.Run(tc.name, func(t *testing.T) {
			err, out, acted := tc.run(t, lockErr)
			if acted {
				t.Errorf("%s acted although its lock could not be taken\n%s", tc.name, out)
			}
			if refuses[tc.name] {
				if err == nil || !strings.Contains(err.Error(), lockErr.Error()) {
					t.Errorf("%s returned %v, want a refusal carrying %q", tc.name, err, lockErr)
				}
				return
			}
			if err != nil {
				t.Errorf("%s returned %v, want it to spare and go on", tc.name, err)
			}
			if !strings.Contains(out, "kept") || !strings.Contains(out, lockErr.Error()) {
				t.Errorf("%s printed no kept line naming the error:\n%s", tc.name, out)
			}
		})
	}
}

// niwaDirPaths lists every path under root's .niwa/.
func niwaDirPaths(t *testing.T, root string) map[string]bool {
	t.Helper()
	paths := map[string]bool{}
	_ = filepath.WalkDir(filepath.Join(root, ".niwa"), func(p string, _ fs.DirEntry, err error) error {
		if err == nil {
			paths[p] = true
		}
		return nil
	})
	return paths
}

func assertNoNewLockPath(t *testing.T, before, after map[string]bool) {
	t.Helper()
	for p := range after {
		if !before[p] && strings.Contains(strings.ToLower(filepath.Base(p)), "lock") {
			t.Errorf("a lock file appeared under the workspace's .niwa/: %s", p)
		}
	}
}

func assertLockFileUnderHome(t *testing.T, home, instancePath string) {
	t.Helper()
	lockPath := workspace.InstanceLockPath(home, instancePath)
	if filepath.Dir(lockPath) != filepath.Join(home, ".niwa", "locks") {
		t.Errorf("the lock path %s is not in %s", lockPath, filepath.Join(home, ".niwa", "locks"))
	}
	if !exists(lockPath) {
		t.Errorf("no lock file at %s", lockPath)
	}
	if strings.HasPrefix(lockPath, instancePath+string(filepath.Separator)) {
		t.Errorf("the lock file %s is inside the instance", lockPath)
	}
}

func TestInstanceLock_NoLockFileInsideInstanceOrWorkspace(t *testing.T) {
	t.Run("resume", func(t *testing.T) {
		f := newResumeFixture(t, "")
		f.writeRecord(t, f.record())
		f.writeTranscript(t, resumeTestSessionID)
		f.presentInstance(t)
		var s resumeSeams
		installResumeSeams(t, f, &s)
		before := niwaDirPaths(t, f.root)
		if _, stderr, err := runResumeCmd(t, resumeTestInstance); err != nil {
			t.Fatalf("resume: %v\n%s", err, stderr)
		}
		assertLockFileUnderHome(t, f.home, f.instancePath())
		assertNoNewLockPath(t, before, niwaDirPaths(t, f.root))
	})

	t.Run("destroy", func(t *testing.T) {
		home := lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha", "beta"})
		instance := filepath.Join(root, "alpha")
		installFakeClaudeSessions(t, &fakeClaudeSessions{})
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "")
		before := niwaDirPaths(t, root)
		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		assertLockFileUnderHome(t, home, instance)
		assertNoNewLockPath(t, before, niwaDirPaths(t, root))
	})
}

// ---- races the per-instance lock alone can't see ----

// TestDestroyWorkspace_InstanceRestoredAfterEnumeration_Kept: destroy
// --workspace locks the instances it enumerated. An instance a resume
// restores after that (its lock held by the resume, never by destroy) must
// not be removed with the workspace, whether its state file is written yet
// or not, and nothing else is removed either. A free directory that is not
// an instance still goes with the root, as before.
func TestDestroyWorkspace_InstanceRestoredAfterEnumeration_Kept(t *testing.T) {
	const resumeHolder = "niwa resume (pid 4343)"
	for _, tc := range []struct {
		name      string
		withState bool
	}{
		{"restored", true},
		{"restoring", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lockTestHome(t)
			root := destroyTestSetup(t, []string{"alpha", "beta", "gamma"})
			delta := filepath.Join(root, "delta")
			fake := fakeClaudeSessions{}
			installFakeClaudeSessions(t, &fake)
			restored := false
			installFakeInstanceLock(t, &fakeInstanceLock{
				errFor: lockErrorFor(heldBy(resumeHolder), delta),
				// The resume restores delta once destroy holds its last
				// lock, so delta is on disk for the scan and the prompt
				// and for everything after them.
				onAcquire: func(p string) {
					if restored || filepath.Base(p) != "gamma" {
						return
					}
					restored = true
					if tc.withState {
						makeReapInstance(t, root, "delta")
					} else if err := os.MkdirAll(filepath.Join(delta, "repo"), 0o755); err != nil {
						t.Fatal(err)
					}
				},
			})
			chdirTo(t, root)
			quietDestroyCmd()
			setDestroyFlags(t, false, true, "testws")

			err := runDestroy(destroyCmd, nil)
			if !restored {
				t.Fatal("the resume step never ran")
			}
			if err == nil {
				t.Fatal("destroy --workspace succeeded with an instance it never checked in the workspace")
			}
			if !strings.Contains(err.Error(), "delta") {
				t.Errorf("error %q does not name delta", err)
			}
			if !exists(delta) {
				t.Error("the restored instance was removed")
			}
			if !exists(root) {
				t.Error("the workspace root was removed")
			}
			assertInstancesExist(t, root, "alpha", "beta", "gamma")
			if len(fake.removed) != 0 {
				t.Errorf("sessions removed: %v", fake.removed)
			}
		})
	}

	t.Run("free_stray_directory", func(t *testing.T) {
		lockTestHome(t)
		root := destroyTestSetup(t, []string{"alpha"})
		if err := os.MkdirAll(filepath.Join(root, "stray"), 0o755); err != nil {
			t.Fatal(err)
		}
		installFakeClaudeSessions(t, &fakeClaudeSessions{})
		chdirTo(t, root)
		t.Cleanup(func() { _ = os.Chdir(filepath.Dir(root)) })
		quietDestroyCmd()
		setDestroyFlags(t, false, true, "testws")

		if err := runDestroy(destroyCmd, nil); err != nil {
			t.Fatalf("destroy --workspace: %v", err)
		}
		if exists(root) {
			t.Error("the workspace root survived")
		}
	})
}

// storedMappingFixture is a mapping whose instance is gone and whose session
// is gone, so the reaper's stored-mapping walk selects it, plus one regular
// target whose destroy runs first and gives a test a step between selection
// and settlement.
type storedMappingFixture struct {
	root, jobsDir, gone, other, logPath string
}

func newStoredMappingFixture(t *testing.T) storedMappingFixture {
	t.Helper()
	lockTestHome(t)
	root := setupHookWorkspace(t, true)
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
	gone := filepath.Join(root, "test-ws-gone")
	mapEphemeral(t, root, reapDeadSessionID, gone, true)
	other := makeReapInstance(t, root, "test-ws-other")
	mapEphemeral(t, root, onReapSID2, other, true)
	return storedMappingFixture{root: root, jobsDir: t.TempDir(), gone: gone, other: other, logPath: logPath}
}

// ranFor reports whether the on-reap log has a run for sid.
func ranFor(t *testing.T, logPath, sid string) bool {
	t.Helper()
	for _, r := range onReapRuns(t, logPath) {
		if strings.HasPrefix(r, sid+" ") {
			return true
		}
	}
	return false
}

// TestReap_StoredMapping_ResumeBetweenSelectionAndSettle_Spared: the reaper
// selects a stored mapping (instance gone, session gone) before its target
// loop. A resume that restores the instance and writes a new mapping for the
// session while that loop runs must not have the on-reap command run for the
// now-live session, nor its new mapping deleted.
func TestReap_StoredMapping_ResumeBetweenSelectionAndSettle_Spared(t *testing.T) {
	f := newStoredMappingFixture(t)
	captureReapStderr(t)
	var resumed workspace.SessionMapping
	prev := destroyInstanceFunc
	destroyInstanceFunc = func(path string) error {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if resumed.SessionID != "" {
			return nil
		}
		// The resume: restore the instance and write the session's new
		// mapping, as runResume does under the instance's lock.
		makeReapInstance(t, f.root, filepath.Base(f.gone))
		resumed = workspace.SessionMapping{
			SessionID:    reapDeadSessionID,
			InstanceName: filepath.Base(f.gone),
			InstancePath: f.gone,
			Ephemeral:    true,
			Origin:       workspace.MappingOriginDispatch,
			Created:      time.Now().UTC().Add(time.Second).Truncate(time.Second),
		}
		return workspace.WriteSessionMapping(f.root, resumed)
	}
	t.Cleanup(func() { destroyInstanceFunc = prev })

	if _, err := reapWorkspace(f.root, f.jobsDir, time.Now()); err != nil {
		t.Fatalf("reapWorkspace: %v", err)
	}
	if resumed.SessionID == "" {
		t.Fatal("the resume step never ran")
	}
	if ranFor(t, f.logPath, reapDeadSessionID) {
		t.Error("the on-reap command ran for the resumed session")
	}
	got, err := workspace.ReadSessionMapping(f.root, reapDeadSessionID)
	if err != nil {
		t.Fatalf("the resumed session's mapping is gone: %v", err)
	}
	if !sameSessionMapping(got, resumed) {
		t.Errorf("the resumed session's mapping changed: got %+v, want %+v", got, resumed)
	}
	if !exists(f.gone) {
		t.Error("the restored instance is gone")
	}
}

// TestReap_StoredMapping_LockHeld_Spared: a stored mapping whose instance's
// lock another command holds (a resume part-way through restoring it) is kept
// for this sweep, with one "niwa: kept" line naming the holder, and the
// on-reap command doesn't run for it. A mapping that changed under the lock is
// kept the same way.
func TestReap_StoredMapping_LockHeld_Spared(t *testing.T) {
	t.Run("held", func(t *testing.T) {
		f := newStoredMappingFixture(t)
		stubDestroyRemoving(t)
		stderr := captureReapStderr(t)
		before := readMapping(t, f.root, reapDeadSessionID)
		installFakeInstanceLock(t, &fakeInstanceLock{errFor: lockErrorFor(heldBy(lockTestHolder), f.gone)})

		if _, err := reapWorkspace(f.root, f.jobsDir, time.Now()); err != nil {
			t.Fatalf("reapWorkspace: %v", err)
		}
		if ranFor(t, f.logPath, reapDeadSessionID) {
			t.Error("the on-reap command ran while the instance's lock was held")
		}
		if after := readMapping(t, f.root, reapDeadSessionID); !sameSessionMapping(after, before) {
			t.Errorf("the mapping changed: %+v -> %+v", before, after)
		}
		if !strings.Contains(stderr.String(), "niwa: kept test-ws-gone: ") || !strings.Contains(stderr.String(), lockTestHolder) {
			t.Errorf("no kept line naming the holder:\n%s", stderr.String())
		}
		// The free target in the same sweep was still reclaimed.
		if !ranFor(t, f.logPath, onReapSID2) {
			t.Error("the other target's on-reap command did not run")
		}
	})

	t.Run("mapping_changed_under_lock", func(t *testing.T) {
		f := newStoredMappingFixture(t)
		stubDestroyRemoving(t)
		stderr := captureReapStderr(t)
		var rewritten workspace.SessionMapping
		installFakeInstanceLock(t, &fakeInstanceLock{onAcquire: func(p string) {
			if filepath.Clean(p) != filepath.Clean(f.gone) || rewritten.SessionID != "" {
				return
			}
			rewritten = readMapping(t, f.root, reapDeadSessionID)
			rewritten.DispatchID = "d2d2d2d2-0000-4000-8000-000000000002"
			if err := workspace.WriteSessionMapping(f.root, rewritten); err != nil {
				t.Fatal(err)
			}
		}})

		if _, err := reapWorkspace(f.root, f.jobsDir, time.Now()); err != nil {
			t.Fatalf("reapWorkspace: %v", err)
		}
		if rewritten.SessionID == "" {
			t.Fatal("the stored mapping's lock was never taken")
		}
		if ranFor(t, f.logPath, reapDeadSessionID) {
			t.Error("the on-reap command ran for a mapping that changed under the lock")
		}
		if after := readMapping(t, f.root, reapDeadSessionID); !sameSessionMapping(after, rewritten) {
			t.Errorf("the rewritten mapping changed: got %+v, want %+v", after, rewritten)
		}
		if !strings.Contains(stderr.String(), "niwa: kept test-ws-gone: its session record changed") {
			t.Errorf("no kept line for the changed mapping:\n%s", stderr.String())
		}
	})
}
