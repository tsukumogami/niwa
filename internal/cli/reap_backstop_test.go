package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// writeDispatchMarkerAt writes a dispatch pending-marker inside the instance at
// instancePath carrying the given RFC3339 timestamp, mirroring what the dispatch
// command drops at create time. The instance's .niwa directory already exists
// (makeReapInstance writes instance.json under it), but MkdirAll keeps this
// robust regardless of call order.
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

// TestBackstop_MarkedUnmappedOld_Reclaimed: a marked, unmapped instance whose
// marker timestamp is older than the TTL is reclaimed by the backstop -- the
// SIGKILL-orphan case the backstop exists to close.
func TestBackstop_MarkedUnmappedOld_Reclaimed(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-disp-old")
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

// TestBackstop_MarkedUnmappedYoung_Spared: a marked, unmapped instance whose
// marker is younger than the TTL is SPARED -- this is the R38 in-flight
// dispatch protection.
func TestBackstop_MarkedUnmappedYoung_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-disp-young")
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

// TestBackstop_MappedInstance_NotTouched: an instance that has a mapping is
// owned by the primary sweep and is NEVER touched by the backstop, even when it
// still carries a stale marker and the marker is past the TTL.
func TestBackstop_MappedInstance_NotTouched(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-disp-mapped")
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

// TestBackstop_UnmarkedInstance_NeverTouched: an instance with no marker (a
// developer instance) is never touched, regardless of mapping or age.
func TestBackstop_UnmarkedInstance_NeverTouched(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	makeReapInstance(t, root, "test-ws-dev") // no marker, no mapping

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (unmarked instance must never be touched)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestBackstop_MalformedMarker_Spared: a marked, unmapped instance whose marker
// timestamp is malformed/unparseable is SPARED (fail safe -- never reap on a
// parse failure).
func TestBackstop_MalformedMarker_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-disp-bad")
	writeRawDispatchMarker(t, inst, "not-a-timestamp\n")

	destroyed := stubDestroyAll(t)

	n, err := reapBackstop(root, now)
	if err != nil {
		t.Fatalf("reapBackstop error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (malformed marker must be spared)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (malformed marker must not trigger a destroy)", *destroyed)
	}
}

// TestBackstop_RunsViaReapWorkspace: the backstop is wired into reapWorkspace,
// so a dead mapped instance (primary sweep) and a marked-unmapped-old instance
// (backstop) are both reclaimed in a single reapWorkspace call, while the
// primary path's behavior for the mapped instance is unchanged.
func TestBackstop_RunsViaReapWorkspace(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: the mapped session reads as dead
	now := time.Now()

	dead := makeReapInstance(t, root, "test-ws-dead")
	mapEphemeral(t, root, reapDeadSessionID, dead, true)

	orphan := makeReapInstance(t, root, "test-ws-disp-orphan")
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

	old := makeReapInstance(t, root, "test-ws-old")     // marked, unmapped, old -> target
	young := makeReapInstance(t, root, "test-ws-young") // marked, unmapped, young -> spared
	mapped := makeReapInstance(t, root, "test-ws-map")  // marked, mapped, old -> spared
	makeReapInstance(t, root, "test-ws-dev2")           // unmarked -> spared
	bad := makeReapInstance(t, root, "test-ws-bad")     // malformed marker -> spared

	writeDispatchMarkerAt(t, old, now.Add(-2*dispatchBackstopTTL))
	writeDispatchMarkerAt(t, young, now.Add(-1*time.Minute))
	writeDispatchMarkerAt(t, mapped, now.Add(-2*dispatchBackstopTTL))
	mapEphemeral(t, root, reapLiveSessionID, mapped, true)
	writeRawDispatchMarker(t, bad, "garbage")

	targets, err := selectBackstopTargets(root, now)
	if err != nil {
		t.Fatalf("selectBackstopTargets error: %v", err)
	}

	if len(targets) != 1 || targets[0].InstancePath != old {
		got := make([]string, 0, len(targets))
		for _, tg := range targets {
			got = append(got, tg.InstancePath)
		}
		t.Fatalf("targets = %v, want [%s]", got, old)
	}
}
