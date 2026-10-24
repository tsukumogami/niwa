package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func capturePendingInstance(t *testing.T) string {
	t.Helper()
	inst := t.TempDir()
	if err := os.MkdirAll(filepath.Join(inst, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	return inst
}

func TestCapturePendingRoundTrip(t *testing.T) {
	inst := capturePendingInstance(t)
	want := CapturePending{
		DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7",
		Agent:      "claude",
		PID:        4242,
		LaunchedAt: time.Date(2026, 10, 24, 5, 0, 0, 123456789, time.UTC),
	}
	if err := WriteCapturePending(inst, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(CapturePendingPath(inst))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	got, found, err := ReadCapturePending(inst)
	if err != nil || !found {
		t.Fatalf("read = %+v, %v, %v", got, found, err)
	}
	if got != want {
		t.Errorf("read %+v, want %+v", got, want)
	}
	if err := RemoveCapturePending(inst); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadCapturePending(inst); found || err != nil {
		t.Errorf("after removal: found=%v err=%v", found, err)
	}
	if err := RemoveCapturePending(inst); err != nil {
		t.Errorf("removing a missing file: %v", err)
	}
}

func TestCapturePendingRefusesAnIncompleteWrite(t *testing.T) {
	inst := capturePendingInstance(t)
	if err := WriteCapturePending(inst, CapturePending{DispatchID: "not-a-uuid", Agent: "claude", PID: 1, LaunchedAt: time.Now()}); err == nil {
		t.Error("wrote a capture-pending file with a bad dispatch id")
	}
	if _, err := os.Stat(CapturePendingPath(inst)); !os.IsNotExist(err) {
		t.Errorf("a refused write left a file: %v", err)
	}
}

func TestReadCapturePendingRefuses(t *testing.T) {
	const ok = `{"dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7","agent":"claude","pid":4242,"launched_at":"2026-10-24T05:00:00Z"`
	for _, tc := range []struct {
		name, body string
	}{
		{"not JSON", "{not json"},
		{"bad dispatch id", `{"dispatch_id":"x","agent":"claude","pid":4242,"launched_at":"2026-10-24T05:00:00Z"}`},
		{"bad agent", `{"dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7","agent":"Claude Code","pid":4242,"launched_at":"2026-10-24T05:00:00Z"}`},
		{"no pid", `{"dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7","agent":"claude","launched_at":"2026-10-24T05:00:00Z"}`},
		{"negative pid", `{"dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7","agent":"claude","pid":-1,"launched_at":"2026-10-24T05:00:00Z"}`},
		{"no launch time", `{"dispatch_id":"3c803591-4c01-486f-a927-f96ec399d6a7","agent":"claude","pid":4242}`},
		{"too large", ok + `,"pad":"` + string(make([]byte, 5000)) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := capturePendingInstance(t)
			if err := os.WriteFile(CapturePendingPath(inst), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if p, found, err := ReadCapturePending(inst); err == nil || !found {
				t.Errorf("read %+v, found=%v, err=%v; want a refusal", p, found, err)
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		inst := capturePendingInstance(t)
		target := filepath.Join(inst, "elsewhere.json")
		if err := os.WriteFile(target, []byte(ok+"}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, CapturePendingPath(inst)); err != nil {
			t.Fatal(err)
		}
		if _, found, err := ReadCapturePending(inst); err == nil || !found {
			t.Errorf("followed a symlink: found=%v err=%v", found, err)
		}
	})
	t.Run("unknown member is ignored", func(t *testing.T) {
		inst := capturePendingInstance(t)
		if err := os.WriteFile(CapturePendingPath(inst), []byte(ok+`,"later":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, found, err := ReadCapturePending(inst); err != nil || !found {
			t.Errorf("found=%v err=%v", found, err)
		}
	})
}
