package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Canonical dispatch-shaped instance names: "<config>+-<8hex>" (no-name
// dispatch, where "+" is the end-of-config marker and the suffix is the
// mandatory "-<8hex>"). The backstop keys eligibility on this NAME's purely
// structural signature (isDispatchInstanceName, "\+[a-z0-9_]*-[0-9a-f]{8}$" --
// no "disp" literal), so the fixtures must use the real shape dispatch produces.
const (
	dispInstOld    = "test-ws+-0000aa11" // marked/aged old -> reapable
	dispInstYoung  = "test-ws+-0000bb22" // young -> spared
	dispInstMapped = "test-ws+-0000cc33" // mapped -> not touched
	dispInstBad    = "test-ws+-0000dd44" // malformed marker -> mtime fallback
	dispInstNoMark = "test-ws+-0000ee55" // no marker (SIGKILL-before-marker) -> mtime fallback
	dispInstOrphan = "test-ws+-0000ff66" // marked/aged old -> reapable (combined test)
	devInstName    = "test-ws-2"         // developer instance -> never matched
	hookInstName   = "test-ws-aabbccdd"  // hook-created instance, no "+" -> never matched
)

// writeDispatchMarkerAt writes a dispatch pending-marker inside the instance at
// instancePath carrying the given RFC3339 timestamp, mirroring what the dispatch
// command drops at create time.
func writeDispatchMarkerAt(t *testing.T, instancePath string, ts time.Time) {
	t.Helper()
	marker := filepath.Join(instancePath, dispatchPendingMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(ts.UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeRawDispatchMarker writes arbitrary bytes as the pending-marker, for the
// malformed-timestamp case.
func writeRawDispatchMarker(t *testing.T, instancePath, contents string) {
	t.Helper()
	marker := filepath.Join(instancePath, dispatchPendingMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// touchInstanceMtime sets the instance directory's modification time, used to
// simulate an old instance whose age must be read from the directory mtime
// (the SIGKILL-before-marker and malformed-marker fallback cases).
func touchInstanceMtime(t *testing.T, instancePath string, ts time.Time) {
	t.Helper()
	if err := os.Chtimes(instancePath, ts, ts); err != nil {
		t.Fatal(err)
	}
}

// TestBackstop_MarkedUnmappedOld_Reclaimed: a dispatch-named, unmapped instance
// whose marker timestamp is older than the TTL is reclaimed by the backstop --
// the SIGKILL-orphan case the backstop exists to close.
func TestBackstop_MarkedUnmappedOld_Reclaimed(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstOld)
	// No mapping written (unmapped). Marker older than the TTL.
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestBackstop_DispNamedUnmappedOldNoMarker_ReclaimedViaMtime: a dispatch-named,
// unmapped instance with NO marker file at all (the SIGKILL-before-marker race:
// the instance dir was created but the process died before the marker write)
// whose directory mtime is older than the TTL is reclaimed via the mtime
// fallback. This is the orphan the name-keyed backstop exists to close -- it was
// previously unreclaimable because it was both unmapped AND unmarked.
func TestBackstop_DispNamedUnmappedOldNoMarker_ReclaimedViaMtime(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstNoMark)
	// No marker, no mapping. Age it by stamping the directory mtime past the TTL.
	touchInstanceMtime(t, inst, now.Add(-2*dispatchBackstopTTL))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1 (SIGKILL-before-marker orphan must be reaped via mtime)", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestBackstop_MarkedUnmappedYoung_Spared: a dispatch-named, unmapped instance
// whose marker is younger than the TTL is SPARED -- this is the R38 in-flight
// dispatch protection.
func TestBackstop_MarkedUnmappedYoung_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstYoung)
	// Marker just one minute old: comfortably within the TTL.
	writeDispatchMarkerAt(t, inst, now.Add(-1*time.Minute))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (young in-flight instance must be spared)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (young instance must not be destroyed)", *destroyed)
	}
}

// TestBackstop_MappedInstance_NotTouched: a dispatch-named instance that has a
// mapping is owned by the primary sweep and is NEVER touched by the backstop,
// even when it still carries a stale marker and the marker is past the TTL.
func TestBackstop_MappedInstance_NotTouched(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstMapped)
	mapEphemeral(t, root, reapLiveSessionID, inst, true)
	// A stale marker past the TTL that was never cleaned up: the backstop must
	// still ignore it because the instance is mapped.
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (mapped instance must not be touched by the backstop)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (mapped instance must not be destroyed by the backstop)", *destroyed)
	}
	if _, err := workspace.ReadSessionMapping(root, reapLiveSessionID); err != nil {
		t.Errorf("mapping was deleted by the backstop; want retained: %v", err)
	}
}

// TestBackstop_NonDispatchName_NeverTouched: an instance whose name is NOT a
// dispatch name -- a developer instance ("<config>-2") or a hook-created instance
// ("<config>-<sessionhex>", no "+" marker) -- is never touched, even when
// it is unmapped and arbitrarily old. The name predicate is the load-bearing
// guard that keeps the backstop off non-dispatch instances.
func TestBackstop_NonDispatchName_NeverTouched(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	dev := makeReapInstance(t, root, devInstName)   // no "+" marker
	hook := makeReapInstance(t, root, hookInstName) // <config>-<sessionhex>, no "+"
	// Age both past the TTL via mtime and even drop a marker on one: still must
	// not be touched, because the NAME does not match.
	touchInstanceMtime(t, dev, now.Add(-2*dispatchBackstopTTL))
	writeDispatchMarkerAt(t, hook, now.Add(-2*dispatchBackstopTTL))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (non-dispatch-named instances must never be touched)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestBackstop_MalformedMarker_FallsBackToMtime: a dispatch-named, unmapped
// instance whose marker timestamp is malformed/unparseable does NOT spare the
// instance forever -- it falls back to the directory mtime. With an old mtime it
// is reaped; with a young mtime it is spared.
func TestBackstop_MalformedMarker_FallsBackToMtime(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	// Malformed marker but an OLD directory mtime: reaped via the mtime fallback.
	oldInst := makeReapInstance(t, root, dispInstBad)
	writeRawDispatchMarker(t, oldInst, "not-a-timestamp\n")
	touchInstanceMtime(t, oldInst, now.Add(-2*dispatchBackstopTTL))

	// Malformed marker but a YOUNG directory mtime: spared via the mtime fallback.
	youngInst := makeReapInstance(t, root, "test-ws+-00009977")
	writeRawDispatchMarker(t, youngInst, "garbage")
	touchInstanceMtime(t, youngInst, now.Add(-1*time.Minute))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1 (malformed marker falls back to mtime: old reaped, young spared)", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != oldInst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, oldInst)
	}
}

// TestBackstop_RunsViaReapWorkspace: the backstop is wired into reapWorkspace,
// so a dead mapped instance (primary sweep) and a dispatch-named-unmapped-old
// instance (backstop) are both reclaimed in a single reapWorkspace call, while
// the primary path's behavior for the mapped instance is unchanged.
func TestBackstop_RunsViaReapWorkspace(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: the mapped session reads as dead
	now := time.Now()

	dead := makeReapInstance(t, root, "test-ws-dead")
	mapEphemeral(t, root, reapDeadSessionID, dead, true)

	orphan := makeReapInstance(t, root, dispInstOrphan)
	writeDispatchMarkerAt(t, orphan, now.Add(-2*dispatchBackstopTTL))

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 2 {
		t.Fatalf("reaped count = %d, want 2 (one primary + one backstop)", n)
	}

	gotDead, gotOrphan := false, false
	for _, p := range *destroyed {
		switch p {
		case dead:
			gotDead = true
		case orphan:
			gotOrphan = true
		default:
			t.Errorf("unexpected destroyed path: %s", p)
		}
	}
	if !gotDead || !gotOrphan {
		t.Fatalf("destroyed = %v, want both %s and %s", *destroyed, dead, orphan)
	}

	// The primary path still deletes the dead mapping.
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
		t.Errorf("dead mapping retained after reapWorkspace; want deleted")
	}
}

// TestSelectBackstopTargets_Matrix exercises the pure selection logic across the
// full spare/reap matrix in one workspace and asserts the exact target set,
// independent of the destroy path.
func TestSelectBackstopTargets_Matrix(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	old := makeReapInstance(t, root, dispInstOld)       // dispatch-named, marked, unmapped, old -> target
	young := makeReapInstance(t, root, dispInstYoung)   // dispatch-named, marked, unmapped, young -> spared
	mapped := makeReapInstance(t, root, dispInstMapped) // dispatch-named, marked, mapped, old -> spared
	noMark := makeReapInstance(t, root, dispInstNoMark) // dispatch-named, NO marker, mtime old -> target
	bad := makeReapInstance(t, root, dispInstBad)       // dispatch-named, malformed marker, mtime old -> target
	dev := makeReapInstance(t, root, devInstName)       // non-disp name, old -> spared
	hook := makeReapInstance(t, root, hookInstName)     // hook name, marked old -> spared

	writeDispatchMarkerAt(t, old, now.Add(-2*dispatchBackstopTTL))
	writeDispatchMarkerAt(t, young, now.Add(-1*time.Minute))
	writeDispatchMarkerAt(t, mapped, now.Add(-2*dispatchBackstopTTL))
	mapEphemeral(t, root, reapLiveSessionID, mapped, true)
	touchInstanceMtime(t, noMark, now.Add(-2*dispatchBackstopTTL))
	writeRawDispatchMarker(t, bad, "garbage")
	touchInstanceMtime(t, bad, now.Add(-2*dispatchBackstopTTL))
	touchInstanceMtime(t, dev, now.Add(-2*dispatchBackstopTTL))
	writeDispatchMarkerAt(t, hook, now.Add(-2*dispatchBackstopTTL))

	targets, _, err := selectBackstopTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectBackstopTargets error: %v", err)
	}

	want := map[string]bool{old: true, noMark: true, bad: true}
	got := make(map[string]bool, len(targets))
	for _, tg := range targets {
		got[tg.InstancePath] = true
	}
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Fatalf("missing expected target %s; got %v", p, got)
		}
	}
}

// writeJobStateCwd writes a present job-state entry under jobsDir/<dirName>
// whose recorded cwd is cwd. It models a live Claude Code worker rooted in an
// instance directory (a dispatched worker launches with cmd.Dir == its
// instance, so its job-state cwd is that instance path). The reaper's
// mapping-independent liveness guard (instanceHasLiveJob) keys on this cwd.
func writeJobStateCwd(t *testing.T, jobsDir, dirName, cwd string) {
	t.Helper()
	dir := filepath.Join(jobsDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"template":"` + bgJobTemplate + `","cwd":"` + cwd + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestBackstop_LiveWorkerRooted_Spared is the regression guard for the
// data-loss bug: a dispatch-named, UNMAPPED, past-TTL instance that a live
// Claude Code worker is currently rooted in (its job-state cwd is the instance
// directory) must NOT be reaped by the backstop. Before the fix the backstop
// keyed on name + age alone and reaped exactly this shape -- including the
// caller's own instance mid-dispatch. It also encodes "dispatch cannot reap its
// own caller": the caller is precisely a live worker rooted in its instance.
func TestBackstop_LiveWorkerRooted_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstOld)
	// Unmapped and well past the TTL -- the exact shape the backstop would
	// otherwise reclaim.
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	// But a live worker is rooted in it: its job-state cwd is the instance dir.
	writeJobStateCwd(t, jobsDir, "0000aa11", inst)

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (a live worker's instance must never be reaped)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (live instance must not be destroyed)", *destroyed)
	}
}

// TestBackstop_RetainedInstance_Spared closes the gap between "kept" meaning
// kept for the length of one command and kept at all.
//
// A foreground turn that finishes and produces work but yields no discoverable
// session record leaves an instance with no mapping. Dispatch disarms its own
// rollback so the work survives the command -- but the backstop runs
// out-of-process at the top of the next create, dispatch or watch, and its
// eligibility signal is the directory NAME. Unmapped, dispatch-named and past
// the TTL is exactly the abandoned-dispatch shape it exists to reclaim, so the
// directory the developer was told was being kept is deleted half an hour later
// by an unrelated command.
//
// Removing the pending marker does not help: the age check falls back to the
// directory mtime, which TestBackstop_DispNamedUnmappedOldNoMarker_ReclaimedViaMtime
// pins. Keeping the work needs a signal the sweep honors rather than the
// absence of one, and this is that signal.
func TestBackstop_RetainedInstance_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstOld)
	// Old enough to be reaped on every other count, and unmapped, which is the
	// state the keep path leaves behind.
	touchInstanceMtime(t, inst, now.Add(-2*dispatchBackstopTTL))
	const reason = "a codex turn finished here but no session record was found, so niwa could not identify the session"
	if err := writeDispatchRetainMarker(inst, reason); err != nil {
		t.Fatalf("writing the retain marker: %v", err)
	}

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 || len(*destroyed) != 0 {
		t.Fatalf("reaped %d (%v); an instance marked to be kept must survive the sweep that was told to keep it", n, *destroyed)
	}

	// And the developer is told, because nothing will ever reclaim it on its
	// own and an instance that accumulates silently is the other half of this
	// failure.
	_, spared, err := selectBackstopTargets(root, t.TempDir(), now)
	if err != nil {
		t.Fatalf("selectBackstopTargets error: %v", err)
	}
	if len(spared) != 1 {
		t.Fatalf("spared %d instance(s), want 1", len(spared))
	}
	if spared[0].Reason != reason {
		t.Errorf("spared reason = %q, want the marker's own words %q", spared[0].Reason, reason)
	}
}

// TestBackstop_LiveWorkerOfEveryAgent_Spared is the data-loss case the backstop
// had no coverage for, and it is the one this feature made reachable.
//
// The backstop exists for the orphan the deferred rollback cannot clean up: a
// worker started detached survives a niwa killed before the mapping is written,
// leaving a live worker in an unmapped instance. Thirty minutes later the next
// opportunistic sweep -- which runs at the top of every create and every
// dispatch -- finds it unmapped and past the TTL. The only thing between that
// and destroying the directory a worker is working in is the live-session
// guard, and while niwa launched one agent that guard could read one store.
//
// So this runs the same scenario once per launchable agent, planting a session
// record in *that agent's* declared store rather than in one agent's harness
// state, and asserts the instance survives. It fails for any agent whose store
// the guard cannot read.
//
// Not every subtest carries the same weight, and it is worth saying which does
// what. For the agent whose declared store is the jobs directory, the two
// guards read one tree and ask one question, so its subtest is a control: it
// passes with the new guard removed, and what it proves is that generalizing
// the read did not break the case that already worked. The subtest that fails
// without the new guard is the one for an agent whose sessions live somewhere
// the jobs-directory read has never looked -- and that is the case detaching a
// second agent's worker created. The loop is written over LaunchableAgents()
// rather than around either of them because a third agent should arrive
// already covered, and land in whichever of those two roles its declaration
// puts it in.
func TestBackstop_LiveWorkerOfEveryAgent_Spared(t *testing.T) {
	for _, ag := range agentplan.LaunchableAgents() {
		t.Run(string(ag), func(t *testing.T) {
			spec, ok := agentplan.For(ag).LaunchSpec()
			if !ok {
				t.Fatalf("no launch spec for %s", ag)
			}

			root := setupHookWorkspace(t, true)
			now := time.Now()

			// Every record store resolves under a fixture home, so this reads
			// nothing the developer running the suite happens to have on disk.
			home := t.TempDir()
			prevHome := userHomeDir
			userHomeDir = func() string { return home }
			t.Cleanup(func() { userHomeDir = prevHome })

			inst := makeReapInstance(t, root, dispInstOld)
			writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
			writeSessionRecordFor(t, spec.Records, home, inst)

			destroyed := stubDestroyAll(t)

			// jobsDir is the one production derives (defaultJobsDir: the same
			// path under home), rather than an empty fixture directory. It
			// matters, and getting it wrong is what would make this test read
			// as more coverage than it is. For one agent the declared record
			// store IS the jobs directory, so both guards read the same tree
			// and ask the same question of it; handing that subtest an empty
			// jobsDir would separate two roots that are one directory in
			// practice, and the subtest would then "fail without the new
			// guard" for a reason that cannot happen. Wired this way, the
			// subtest asserts the property rather than the mechanism -- a live
			// worker's instance survives -- and which guard supplies it is the
			// agent's own business.
			//
			// It comes from defaultJobsDir rather than from a path written
			// here, so the claim that the two roots coincide is demonstrated by
			// the production accessor instead of asserted by a fixture.
			t.Setenv("HOME", home)
			n, err := reapBackstop(root, defaultJobsDir(), now)
			if err != nil {
				t.Fatalf("reapBackstop error: %v", err)
			}
			if n != 0 {
				t.Fatalf("reaped count = %d, want 0 (a live %s worker's instance must never be reaped)", n, ag)
			}
			if len(*destroyed) != 0 {
				t.Fatalf("destroyed = %v, want []", *destroyed)
			}

			// Only a permanent spare is reported, and which of the three kinds
			// is permanent is the whole distinction. An agent whose records
			// disappear with the session needs no notice: the instance goes
			// when the session does. An agent answered by activity needs none
			// either, for a different reason -- the record here was just
			// written, so the sparing is real but temporary, and it ends on its
			// own once nobody has touched the session for the grace period.
			// Only an agent that offers no readable signal at all is spared
			// forever, and that developer has to be told in words that their
			// disk is not going to get emptier on its own.
			_, spared, err := selectBackstopTargets(root, defaultJobsDir(), now)
			if err != nil {
				t.Fatalf("selectBackstopTargets error: %v", err)
			}
			// Worth knowing what this arm is currently worth: no launchable
			// agent declares LivenessNone any more, so this resolves to false
			// for every subtest and the branch below asserts only absence. It
			// is kept as the statement of the rule rather than deleted,
			// because the kind is still declarable and an agent adopting it
			// should arrive covered -- but nobody should read a green run here
			// as evidence that the permanent-spare report works.
			wantSpared := spec.Records.Liveness == agentplan.LivenessNone
			if got := len(spared) > 0; got != wantSpared {
				t.Fatalf("reported spared = %v (%v), want %v for a %s record store", got, spared, wantSpared, ag)
			}
			if wantSpared {
				if spared[0].Name != filepath.Base(inst) {
					t.Errorf("spared name = %q, want the instance name %q; the report has to name what niwa destroy takes", spared[0].Name, filepath.Base(inst))
				}
				if !strings.Contains(spared[0].Reason, string(ag)) {
					t.Errorf("spared reason = %q, want it to name %s", spared[0].Reason, ag)
				}
			}
		})
	}
}

// writeSessionRecordFor plants one session record in an agent's own declared
// store, rooted at cwd. It writes whatever shape the declaration describes --
// a named file inside a per-session directory, or a glob-matched transcript
// whose first line is its metadata -- so the fixture follows the declaration
// rather than restating one agent's layout.
func writeSessionRecordFor(t *testing.T, r agentplan.SessionRecords, home, cwd string) {
	t.Helper()
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

	const sid = "01a00000-0000-7000-8000-0000000000aa"
	doc := map[string]any{}
	setAtPath(doc, r.IDPath, sid)
	setAtPath(doc, r.CwdPath, cwd)
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the record: %v", err)
	}

	name := r.FileName
	if name == "" {
		name = strings.ReplaceAll(r.FileGlob, "*", sid)
	}
	body := string(encoded)
	if r.FirstLineOnly {
		// A second line, because a store whose records are transcripts would
		// have one, and a reader that swallowed the whole file must fail rather
		// than quietly succeed.
		body += "\n{\"ordinal\":1}"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body+"\n"), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

// setAtPath writes value at a dotted field path, creating the objects along the
// way. It is the fixture side of the reader's own path walk, so a declaration
// that nests its fields is exercised rather than flattened.
func setAtPath(doc map[string]any, path []string, value string) {
	cur := doc
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[key] = next
		}
		cur = next
	}
	cur[path[len(path)-1]] = value
}

// TestBackstop_LiveWorkerInWorktree_Spared: a worker whose cwd is a
// subdirectory of the instance (e.g. a per-repo worktree) still counts as live,
// so the instance is spared. The guard matches at-or-below the instance path,
// not just an exact cwd.
func TestBackstop_LiveWorkerInWorktree_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstOld)
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	writeJobStateCwd(t, jobsDir, "0000aa11", filepath.Join(inst, "public", "tsuku"))

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (a worker rooted in a subdir is still live)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (live instance must not be destroyed)", *destroyed)
	}
}

// TestBackstop_DeadWorkerSiblingCwd_StillReaped: a genuinely orphaned
// dispatch instance (unmapped, past TTL, NO live job rooted in it) is still
// reclaimed. A live job whose cwd sits beside the instance -- a sibling path
// sharing a name prefix -- must not be mistaken for one inside it. This pins
// that the liveness guard narrows reaping to live instances only and does not
// disable the backstop.
func TestBackstop_DeadWorkerSiblingCwd_StillReaped(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, dispInstOld)
	writeDispatchMarkerAt(t, inst, now.Add(-2*dispatchBackstopTTL))
	// A live job, but rooted in a SIBLING directory that shares the instance's
	// path as a string prefix without the separator boundary. It must not spare
	// the instance.
	writeJobStateCwd(t, jobsDir, "0000bb22", inst+"-other")

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1 (a genuine orphan must still be reaped)", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
}

// TestInstanceHasLiveJob exercises the mapping-independent liveness helper
// directly across its spare/reclaim cases.
func TestInstanceHasLiveJob(t *testing.T) {
	jobsDir := t.TempDir()
	inst := "/ws/test-ws+-0000aa11"

	if instanceHasLiveJob(jobsDir, inst) {
		t.Fatal("empty jobs dir must report no live job")
	}
	if instanceHasLiveJob("", inst) {
		t.Fatal("empty jobsDir must report no live job (fail safe)")
	}

	writeJobStateCwd(t, jobsDir, "0000aa11", inst)
	if !instanceHasLiveJob(jobsDir, inst) {
		t.Fatal("a job whose cwd is the instance must count as live")
	}
	if !instanceHasLiveJob(jobsDir, inst+"/") {
		t.Fatal("a trailing separator on instancePath must still match (cleaned)")
	}
	if instanceHasLiveJob(jobsDir, inst+"-other") {
		t.Fatal("a sibling path sharing a prefix must not match")
	}
	if instanceHasLiveJob(jobsDir, "/ws/test-ws+-0000bb22") {
		t.Fatal("an unrelated instance must not match")
	}
}

// ---- The on-reap command ----
//
// These tests run real commands under /bin/sh. Every command is a neutral
// shell one-liner; the environment they record holds only fake values the
// test sets.

const (
	onReapDispatchID = "d1d1d1d1-0000-4000-8000-000000000001"
	onReapSID2       = "44444444-4444-4444-8444-444444444444"
	onReapSID3       = "55555555-5555-4555-8555-555555555555"
	onReapSID4       = "66666666-6666-4666-8666-666666666666"
	onReapSID5       = "77777777-7777-4777-8777-777777777777"
)

// appendOnReapConfig adds a [session.on_reap] table to the workspace config
// under root. body is the table's lines.
func appendOnReapConfig(t *testing.T, root, body string) {
	t.Helper()
	path := workspaceConfigPath(root)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[session.on_reap]\n%s\n", body); err != nil {
		t.Fatal(err)
	}
}

// onReapLogCommand returns a command that appends one line per run --
// "<session id> <reap key> <instance path>" -- to a log file, and the file.
func onReapLogCommand(t *testing.T) (command, logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "runs.log")
	return fmt.Sprintf(`printf '%%s %%s %%s\n' "$NIWA_SESSION_ID" "$NIWA_REAP_KEY" "$NIWA_INSTANCE_PATH" >> '%s'`, logPath), logPath
}

// onReapRuns reads the run log, one entry per line.
func onReapRuns(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// captureReapStderr points the reap's on-reap output at a buffer.
func captureReapStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := reapStderr
	reapStderr = &buf
	t.Cleanup(func() { reapStderr = prev })
	return &buf
}

// onReapWarnings returns the on-reap warning lines in out.
func onReapWarnings(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "niwa: warning: on-reap for session ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// stubDestroyRemoving installs a destroyInstanceFunc that really removes the
// directory, so the next reap finds the instance gone.
func stubDestroyRemoving(t *testing.T) {
	t.Helper()
	prev := destroyInstanceFunc
	destroyInstanceFunc = func(path string) error { return os.RemoveAll(path) }
	t.Cleanup(func() { destroyInstanceFunc = prev })
}

// TestOnReap_FixedEnvironment: the command sees PATH, HOME, the three values
// naming the session and the names its own env list declares that are set,
// and nothing else from niwa -- a name declared only for pass-through is
// absent. It runs from the workspace root.
func TestOnReap_FixedEnvironment(t *testing.T) {
	root := setupHookWorkspace(t, true)
	out := filepath.Join(t.TempDir(), "env.txt")
	cwdOut := filepath.Join(t.TempDir(), "cwd.txt")
	appendOnReapConfig(t, root, fmt.Sprintf(`command = "env > '%s'; pwd -P > '%s'"
env = ["LISTED_MARK", "UNSET_LISTED_MARK"]`, out, cwdOut))
	if f, err := os.OpenFile(workspaceConfigPath(root), os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "\n[session.dispatch_env]\npassthrough = [\"PASS_ONLY_MARK\"]\n")
		f.Close()
	}
	t.Setenv("LISTED_MARK", "listed-fake")
	t.Setenv("UNSET_LISTED_MARK", "")
	os.Unsetenv("UNSET_LISTED_MARK")
	t.Setenv("PASS_ONLY_MARK", "pass-fake")
	t.Setenv("UNRELATED_MARK", "unrelated-fake")

	var buf bytes.Buffer
	step := newOnReapStep(root, &buf)
	inst := filepath.Join(root, "gone-instance")
	if !step.run(removedSession{SessionID: reapDeadSessionID, ReapKey: onReapDispatchID, InstancePath: inst}) {
		t.Fatalf("run failed: %s", buf.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "PWD", "SHLVL", "_":
			// set by sh itself
			continue
		}
		got[k] = v
	}
	want := map[string]string{
		"PATH":               os.Getenv("PATH"),
		"HOME":               os.Getenv("HOME"),
		"NIWA_REAP_KEY":      onReapDispatchID,
		"NIWA_SESSION_ID":    reapDeadSessionID,
		"NIWA_INSTANCE_PATH": inst,
		"LISTED_MARK":        "listed-fake",
	}
	if len(got) != len(want) {
		t.Errorf("environment names = %v, want exactly %v", keysOf(got), keysOf(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	cwd, _ := os.ReadFile(cwdOut)
	wantCwd, _ := filepath.EvalSymlinks(root)
	if strings.TrimSpace(string(cwd)) != wantCwd {
		t.Errorf("command ran in %q, want the workspace root %q", strings.TrimSpace(string(cwd)), wantCwd)
	}
	if buf.Len() != 0 {
		t.Errorf("a successful run printed %q", buf.String())
	}
}

func keysOf(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestOnReap_FailuresPrintOneLine: a command that exits 1, one that sleeps
// past its timeout, and one missing from the PATH each fail with exactly one
// warning line, within the timeout plus one second. A command that exits 0
// on finding its tool absent prints nothing.
func TestOnReap_FailuresPrintOneLine(t *testing.T) {
	cases := []struct {
		name    string
		command string
		ok      bool
		reason  string
	}{
		{"exits 1", "echo failing-detail >&2; exit 1", false, "exited with status 1: failing-detail"},
		{"sleeps past its timeout", "sleep 10", false, "timed out after 1s"},
		{"missing from the PATH", "no-such-close-tool-x --close", false, "exited with status 127"},
		{"exits 0 when its tool is absent", "command -v no-such-close-tool-x >/dev/null 2>&1 || exit 0; exec no-such-close-tool-x", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupHookWorkspace(t, true)
			appendOnReapConfig(t, root, fmt.Sprintf("command = %q\ntimeout = \"1s\"", tc.command))
			var buf bytes.Buffer
			step := newOnReapStep(root, &buf)
			start := time.Now()
			ok := step.run(removedSession{SessionID: reapDeadSessionID, ReapKey: reapDeadSessionID})
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("run took %s, want within the timeout plus one second", elapsed)
			}
			if ok != tc.ok {
				t.Fatalf("run = %v, want %v (%s)", ok, tc.ok, buf.String())
			}
			warnings := onReapWarnings(buf.String())
			if tc.ok {
				if buf.Len() != 0 {
					t.Errorf("printed %q, want nothing", buf.String())
				}
				return
			}
			if len(warnings) != 1 || strings.Count(buf.String(), "\n") != 1 {
				t.Fatalf("output = %q, want exactly one warning line", buf.String())
			}
			want := "niwa: warning: on-reap for session " + reapDeadSessionID + ": " + tc.reason
			if !strings.HasPrefix(warnings[0], want) {
				t.Errorf("warning = %q, want prefix %q", warnings[0], want)
			}
		})
	}
}

// TestOnReap_TimeoutHonoredAndGrandchildren: a configured 2s timeout is
// honoured; a grandchild that keeps stdout open, or one that left the process
// group with setsid, does not hold the run past its timeout plus one second;
// and a shell that exits while its grandchild still holds stdout succeeds
// without waiting for it.
func TestOnReap_TimeoutHonoredAndGrandchildren(t *testing.T) {
	setsidCmd := `perl -MPOSIX -e 'POSIX::setsid(); sleep 6' &`
	if _, err := exec.LookPath("perl"); err != nil {
		setsidCmd = ""
	}
	cases := []struct {
		name    string
		command string
		timeout string
		limit   time.Duration
		ok      bool
	}{
		{"2s timeout honored", "sleep 10", "2s", 3 * time.Second, false},
		{"grandchild keeps stdout open past exit", "sleep 6 & exit 0", "2s", 1 * time.Second, true},
		{"grandchild keeps stdout open past timeout", "sleep 6 & sleep 10", "1s", 2 * time.Second, false},
		{"setsid grandchild past timeout", setsidCmd + " sleep 10", "1s", 2 * time.Second, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.HasPrefix(tc.name, "setsid") && setsidCmd == "" {
				t.Skip("perl is not available to call setsid")
			}
			root := setupHookWorkspace(t, true)
			appendOnReapConfig(t, root, fmt.Sprintf("command = %q\ntimeout = %q", tc.command, tc.timeout))
			// A buffer, not a file, so the command's stdout is a pipe the
			// grandchild can hold.
			var buf bytes.Buffer
			step := newOnReapStep(root, &buf)
			start := time.Now()
			ok := step.run(removedSession{SessionID: reapDeadSessionID, ReapKey: reapDeadSessionID})
			elapsed := time.Since(start)
			if elapsed > tc.limit {
				t.Errorf("run took %s, want at most %s", elapsed, tc.limit)
			}
			if ok != tc.ok {
				t.Errorf("run = %v, want %v (%s)", ok, tc.ok, buf.String())
			}
			if !tc.ok && !strings.Contains(buf.String(), "timed out after "+tc.timeout) {
				t.Errorf("warning = %q, want a timeout", buf.String())
			}
		})
	}
}

// TestOnReap_ProcessGroupKilledOnTimeout: on timeout the whole process group
// goes, so a grandchild in the group doesn't live on.
func TestOnReap_ProcessGroupKilledOnTimeout(t *testing.T) {
	root := setupHookWorkspace(t, true)
	marker := filepath.Join(t.TempDir(), "survived")
	appendOnReapConfig(t, root, fmt.Sprintf("command = \"(sleep 2; touch '%s') & sleep 10\"\ntimeout = \"1s\"", marker))
	step := newOnReapStep(root, io.Discard)
	if step.run(removedSession{SessionID: reapDeadSessionID, ReapKey: reapDeadSessionID}) {
		t.Fatal("a timed-out run reported success")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a grandchild in the command's process group outlived the timeout")
	}
}

// TestOnReap_WarningReasonIsSanitized: the reason is stripped of control
// characters and truncated.
func TestOnReap_WarningReasonIsSanitized(t *testing.T) {
	root := setupHookWorkspace(t, true)
	appendOnReapConfig(t, root, `command = "printf 'bad\\033[31mred\\tx%0500d\\n' 0 >&2; exit 1"`)
	var buf bytes.Buffer
	newOnReapStep(root, &buf).run(removedSession{SessionID: reapDeadSessionID, ReapKey: reapDeadSessionID})
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("output = %q, want one line", out)
	}
	if strings.ContainsAny(out[:len(out)-1], "\x1b\t\r") {
		t.Errorf("control characters reached the warning: %q", out)
	}
	if !strings.HasSuffix(out, "...\n") {
		t.Errorf("a long reason was not truncated: %q", out)
	}
	prefix := "niwa: warning: on-reap for session " + reapDeadSessionID + ": "
	if len(out)-len(prefix)-1 > maxOnReapReasonBytes {
		t.Errorf("reason is %d bytes, want at most %d", len(out)-len(prefix)-1, maxOnReapReasonBytes)
	}
}

// TestOnReap_InvalidStoredKeyRunsNothing: a mapping whose stored dispatch id
// holds shell metacharacters gets no command run, one warning line, and the
// reap still succeeds.
func TestOnReap_InvalidStoredKeyRunsNothing(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stubDestroyAll(t)
	stderr := captureReapStderr(t)
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
	inst := makeReapInstance(t, root, "test-ws-dead")
	writeMapping(t, root, workspace.SessionMapping{
		SessionID: reapDeadSessionID, InstancePath: inst, Ephemeral: true,
		DispatchID: "x'; touch /tmp/should-not-exist; '",
	})

	n, err := reapWorkspace(root, t.TempDir(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("reap = %d, %v; want 1, nil", n, err)
	}
	if runs := onReapRuns(t, logPath); len(runs) != 0 {
		t.Errorf("the command ran for an invalid key: %v", runs)
	}
	if w := onReapWarnings(stderr.String()); len(w) != 1 || !strings.Contains(w[0], "reap key is not a plain identifier") {
		t.Errorf("warnings = %q", w)
	}
}

// TestReap_OnReapRunsOncePerRemovedSession: a reap that removes a session runs
// the command once with the recomputed key -- the dispatch id when the
// mapping has one, the session id otherwise -- and drops the mapping on
// success.
func TestReap_OnReapRunsOncePerRemovedSession(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stubDestroyAll(t)
	captureReapStderr(t)
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
	dispatched := makeReapInstance(t, root, "test-ws-dispatched")
	hooked := makeReapInstance(t, root, "test-ws-hooked")
	writeMapping(t, root, workspace.SessionMapping{SessionID: reapDeadSessionID, InstancePath: dispatched, Ephemeral: true, DispatchID: onReapDispatchID})
	writeMapping(t, root, workspace.SessionMapping{SessionID: onReapSID2, InstancePath: hooked, Ephemeral: true})

	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	runs := onReapRuns(t, logPath)
	sort.Strings(runs)
	want := []string{
		reapDeadSessionID + " " + onReapDispatchID + " " + dispatched,
		onReapSID2 + " " + onReapSID2 + " " + hooked,
	}
	sort.Strings(want)
	if strings.Join(runs, "|") != strings.Join(want, "|") {
		t.Errorf("runs = %q, want %q", runs, want)
	}
	for _, sid := range []string{reapDeadSessionID, onReapSID2} {
		if mappingExists(t, root, sid) {
			t.Errorf("mapping %s kept after a successful run", sid)
		}
	}
}

// TestReap_FailedRunKeepsMappingAndRetries: a failed run keeps the mapping
// with its count raised and the reap's outcome unchanged; the next reap that
// finds the session gone runs the command again, and a run that then
// succeeds drops the mapping.
func TestReap_FailedRunKeepsMappingAndRetries(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stubDestroyRemoving(t)
	stderr := captureReapStderr(t)
	flag := filepath.Join(t.TempDir(), "succeed")
	logPath := filepath.Join(t.TempDir(), "runs.log")
	appendOnReapConfig(t, root, fmt.Sprintf(`command = "echo run >> '%s'; [ -e '%s' ]"`, logPath, flag))
	inst := makeReapInstance(t, root, "test-ws-dead")
	mapEphemeral(t, root, reapDeadSessionID, inst, true)
	jobsDir := t.TempDir()

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("first reap = %d, %v; want 1, nil", n, err)
	}
	if w := onReapWarnings(stderr.String()); len(w) != 1 {
		t.Fatalf("warnings = %q, want one", w)
	}
	m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
	if err != nil {
		t.Fatalf("the mapping was not kept after a failed run: %v", err)
	}
	if m.OnReapFailures != 1 {
		t.Errorf("failure count = %d, want 1", m.OnReapFailures)
	}

	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runs := onReapRuns(t, logPath); len(runs) != 2 {
		t.Errorf("runs = %d, want 2 (the failed one and the retry)", len(runs))
	}
	if mappingExists(t, root, reapDeadSessionID) {
		t.Error("the mapping was kept after the retry succeeded")
	}
}

// TestReap_TenFailedRunsDropTheMapping: at the tenth failed run niwa drops the
// mapping with a final warning line.
func TestReap_TenFailedRunsDropTheMapping(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stderr := captureReapStderr(t)
	appendOnReapConfig(t, root, `command = "exit 1"`)
	writeMapping(t, root, workspace.SessionMapping{SessionID: reapDeadSessionID, InstancePath: filepath.Join(root, "gone"), Ephemeral: true, OnReapFailures: onReapFailureLimit - 2})
	jobsDir := t.TempDir()

	if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
	if err != nil || m.OnReapFailures != onReapFailureLimit-1 {
		t.Fatalf("after nine failures: %+v, %v", m, err)
	}
	stderr.Reset()
	if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if mappingExists(t, root, reapDeadSessionID) {
		t.Error("the mapping survived its tenth failed run")
	}
	w := onReapWarnings(stderr.String())
	if len(w) != 2 || !strings.Contains(w[1], "gave up after 10 failed runs") {
		t.Errorf("warnings = %q, want the failure and a final line", w)
	}
}

// TestReap_MappingWrittenBeforeTheCountLoadsAtZero: a mapping file with no
// failure count decodes with a count of zero.
func TestReap_MappingWrittenBeforeTheCountLoadsAtZero(t *testing.T) {
	root := setupHookWorkspace(t, true)
	dir := filepath.Join(root, workspace.StateDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"session_id":"` + reapDeadSessionID + `","instance_name":"x","instance_path":"/x","transcript_path":"","created":"2026-01-01T00:00:00Z","ephemeral":true}`
	if err := os.WriteFile(filepath.Join(dir, reapDeadSessionID+".json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
	if err != nil || m.OnReapFailures != 0 {
		t.Errorf("legacy mapping = %+v, %v; want a count of zero", m, err)
	}
}

// TestReap_KeptMappingsAreReapedOnceTheirSessionIsGone covers the mappings
// reap finds by walking the store rather than the instances: one kept by
// destroy because its session was still running, one left by
// destroy --keep-session, one whose instance was deleted by hand, and one with
// no instance at all. Each gets the command once its session is gone, and a
// live session's mapping gets nothing.
func TestReap_KeptMappingsAreReapedOnceTheirSessionIsGone(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta", "gamma"})
	captureReapStderr(t)
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
	jobsDir := t.TempDir()

	// Kept because its session was still running when destroy ran.
	alpha := filepath.Join(root, "alpha")
	writeTestMapping(t, root, ownerSID, alpha, "")
	installFakeClaudeSessions(t, &fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID, PID: 42}}})
	var destroyOut bytes.Buffer
	removeOwningSessions(workspace.NewReporterWithTTY(&destroyOut, false), root, []string{alpha})
	if !mappingExists(t, root, ownerSID) {
		t.Fatal("precondition: destroy must keep a running session's mapping")
	}
	if err := os.RemoveAll(alpha); err != nil {
		t.Fatal(err)
	}

	// Left by --keep-session: the instance is gone and the mapping stays.
	beta := filepath.Join(root, "beta")
	writeTestMapping(t, root, otherSID, beta, "")
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, true, false, "")
	prevKeep := destroyKeepSession
	destroyKeepSession = true
	t.Cleanup(func() { destroyKeepSession = prevKeep })
	if err := runDestroy(destroyCmd, []string{"beta"}); err != nil {
		t.Fatalf("destroy --keep-session: %v", err)
	}
	if runs := onReapRuns(t, logPath); len(runs) != 0 {
		t.Fatalf("destroy --keep-session ran the command: %v", runs)
	}

	// Deleted by hand.
	gamma := filepath.Join(root, "gamma")
	writeTestMapping(t, root, onReapSID3, gamma, "")
	if err := os.RemoveAll(gamma); err != nil {
		t.Fatal(err)
	}

	// No instance path at all, and a still-live session with no instance.
	writeMapping(t, root, workspace.SessionMapping{SessionID: onReapSID4, Ephemeral: true})
	writeMapping(t, root, workspace.SessionMapping{SessionID: onReapSID5, InstancePath: filepath.Join(root, "delta"), Ephemeral: true})
	writeJobEntry(t, jobsDir, onReapSID5)

	if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	runs := onReapRuns(t, logPath)
	got := map[string]int{}
	for _, r := range runs {
		got[strings.Fields(r)[0]]++
	}
	for _, sid := range []string{ownerSID, otherSID, onReapSID3, onReapSID4} {
		if got[sid] != 1 {
			t.Errorf("session %s: %d runs, want 1 (runs %q)", sid, got[sid], runs)
		}
		if mappingExists(t, root, sid) {
			t.Errorf("session %s: mapping kept after a successful run", sid)
		}
	}
	if got[onReapSID5] != 0 || !mappingExists(t, root, onReapSID5) {
		t.Errorf("a live session's mapping was reaped: runs %d", got[onReapSID5])
	}

	// A second reap finds nothing more to do.
	if _, err := reapWorkspace(root, jobsDir, time.Now()); err != nil {
		t.Fatal(err)
	}
	if again := onReapRuns(t, logPath); len(again) != len(runs) {
		t.Errorf("a second reap ran the command again: %q", again)
	}
}

// TestReap_StepBudget: five sessions removed in one reap with a command that
// sleeps 10 seconds end within 31 seconds of wall time, and each session whose
// run timed out or was skipped for lack of budget gets one warning line.
func TestReap_StepBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 30 seconds")
	}
	root := setupHookWorkspace(t, true)
	stderr := captureReapStderr(t)
	appendOnReapConfig(t, root, `command = "sleep 10"`)
	sids := []string{reapDeadSessionID, onReapSID2, onReapSID3, onReapSID4, onReapSID5}
	for _, sid := range sids {
		writeMapping(t, root, workspace.SessionMapping{SessionID: sid, InstancePath: filepath.Join(root, "gone-"+sid[:4]), Ephemeral: true})
	}

	start := time.Now()
	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 31*time.Second {
		t.Errorf("the reap's on-reap runs took %s, want at most 31s", elapsed)
	}
	w := onReapWarnings(stderr.String())
	if len(w) != len(sids) {
		t.Fatalf("warnings = %d, want one per session:\n%s", len(w), stderr.String())
	}
	skipped := 0
	for _, line := range w {
		if strings.Contains(line, "skipped") {
			skipped++
		} else if !strings.Contains(line, "timed out") {
			t.Errorf("unexpected warning %q", line)
		}
	}
	if skipped == 0 {
		t.Error("no run was skipped for lack of budget")
	}
	for _, sid := range sids {
		if !mappingExists(t, root, sid) {
			t.Errorf("session %s lost its mapping after a failed run", sid)
		}
	}
}

// TestReap_StepBudgetScaled is the budget rule at a scale fast enough for
// every run: with a 3s budget and a 1s timeout, three runs time out and the
// rest are skipped.
func TestReap_StepBudgetScaled(t *testing.T) {
	prev := onReapStepBudget
	onReapStepBudget = 3 * time.Second
	t.Cleanup(func() { onReapStepBudget = prev })
	root := setupHookWorkspace(t, true)
	stderr := captureReapStderr(t)
	appendOnReapConfig(t, root, "command = \"sleep 10\"\ntimeout = \"1s\"")
	sids := []string{reapDeadSessionID, onReapSID2, onReapSID3, onReapSID4, onReapSID5}
	for _, sid := range sids {
		writeMapping(t, root, workspace.SessionMapping{SessionID: sid, InstancePath: filepath.Join(root, "gone-"+sid[:4]), Ephemeral: true})
	}
	start := time.Now()
	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("took %s, want within the 3s budget plus one second", elapsed)
	}
	w := onReapWarnings(stderr.String())
	timedOut, skipped := 0, 0
	for _, line := range w {
		switch {
		case strings.Contains(line, "timed out"):
			timedOut++
		case strings.Contains(line, "skipped"):
			skipped++
		}
	}
	if len(w) != 5 || timedOut != 3 || skipped != 2 {
		t.Errorf("warnings: %d (timed out %d, skipped %d), want 5 (3, 2):\n%s", len(w), timedOut, skipped, stderr.String())
	}
}

// TestReap_ConfigurationReadability: a workspace with no configuration file
// declares no command, so the mapping goes as it always did; a file that
// exists but can't be parsed counts as a failed run, warns and keeps it.
func TestReap_ConfigurationReadability(t *testing.T) {
	t.Run("no configuration file", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		stderr := captureReapStderr(t)
		if err := os.Remove(workspaceConfigPath(root)); err != nil {
			t.Fatal(err)
		}
		writeMapping(t, root, workspace.SessionMapping{SessionID: reapDeadSessionID, InstancePath: filepath.Join(root, "gone"), Ephemeral: true})
		if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
			t.Fatal(err)
		}
		if mappingExists(t, root, reapDeadSessionID) {
			t.Error("with no configuration the mapping must be dropped as before")
		}
		if stderr.Len() != 0 {
			t.Errorf("printed %q", stderr.String())
		}
	})
	t.Run("unparsable configuration file", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		stderr := captureReapStderr(t)
		if err := os.WriteFile(workspaceConfigPath(root), []byte("[workspace\nname = "), 0o644); err != nil {
			t.Fatal(err)
		}
		writeMapping(t, root, workspace.SessionMapping{SessionID: reapDeadSessionID, InstancePath: filepath.Join(root, "gone"), Ephemeral: true})
		if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
			t.Fatalf("an unreadable on-reap configuration failed the reap: %v", err)
		}
		m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
		if err != nil || m.OnReapFailures != 1 {
			t.Errorf("mapping = %+v, %v; want it kept with one failed run", m, err)
		}
		if w := onReapWarnings(stderr.String()); len(w) != 1 || !strings.Contains(w[0], "could not read [session.on_reap]") {
			t.Errorf("warnings = %q", w)
		}
	})
}

// TestReap_NoCommandForUnmappedOrRolledBack: the backstop's reap of an
// unmapped dispatch instance runs no command.
func TestReap_NoCommandForUnmappedInstance(t *testing.T) {
	root := setupHookWorkspace(t, true)
	stubDestroyAll(t)
	captureReapStderr(t)
	command, logPath := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", command))
	orphan := makeReapInstance(t, root, dispInstOrphan)
	writeDispatchMarkerAt(t, orphan, time.Now().Add(-2*dispatchBackstopTTL))

	n, err := reapWorkspace(root, t.TempDir(), time.Now())
	if err != nil || n != 1 {
		t.Fatalf("reap = %d, %v; want the backstop to reclaim one", n, err)
	}
	if runs := onReapRuns(t, logPath); len(runs) != 0 {
		t.Errorf("the backstop ran the command: %v", runs)
	}
}

// TestOnReap_CallSites pins where the command can run. niwa reset and a
// dispatch's rollback remove no session niwa knows by id, so neither file may
// reach the runner; the behavioural tests cover the call sites that do.
func TestOnReap_CallSites(t *testing.T) {
	want := map[string]bool{"on_reap.go": true, "reap.go": true, "destroy_session.go": true, "watch.go": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		uses := strings.Contains(string(data), "newOnReapStep(")
		if uses != want[name] {
			t.Errorf("%s: reaches the on-reap runner = %v, want %v", name, uses, want[name])
		}
	}
}
