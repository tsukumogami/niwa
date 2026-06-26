package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

	n, err := reapBackstop(root, now)
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

	n, err := reapBackstop(root, now)
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

	n, err := reapBackstop(root, now)
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

	n, err := reapBackstop(root, now)
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

	n, err := reapBackstop(root, now)
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

	n, err := reapBackstop(root, now)
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

	targets, err := selectBackstopTargets(root, now)
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
