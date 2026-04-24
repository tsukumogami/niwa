package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwapSnapshotAtomic_FreshTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")

	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(staging, "marker"), "v1")

	if err := SwapSnapshotAtomic(target, staging); err != nil {
		t.Fatalf("swap: %v", err)
	}

	snapshotReadFile(t, filepath.Join(target, "marker"), "v1")
	snapshotMustNotExist(t, staging)
	snapshotMustNotExist(t, target+".prev")
}

func TestSwapSnapshotAtomic_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(target, "marker"), "old")

	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(staging, "marker"), "new")

	if err := SwapSnapshotAtomic(target, staging); err != nil {
		t.Fatalf("swap: %v", err)
	}

	snapshotReadFile(t, filepath.Join(target, "marker"), "new")
	snapshotMustNotExist(t, staging)
	snapshotMustNotExist(t, target+".prev")
}

func TestSwapSnapshotAtomic_PreflightCleansStalePrev(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")
	stalePrev := target + ".prev"

	// Simulate an interrupted swap by leaving a .prev directory behind.
	if err := os.Mkdir(stalePrev, 0o755); err != nil {
		t.Fatalf("mkdir stale prev: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(stalePrev, "stale-marker"), "stale")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(target, "marker"), "old")

	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(staging, "marker"), "new")

	if err := SwapSnapshotAtomic(target, staging); err != nil {
		t.Fatalf("swap: %v", err)
	}

	snapshotReadFile(t, filepath.Join(target, "marker"), "new")
	snapshotMustNotExist(t, target+".prev")
}

func TestSwapSnapshotAtomic_FaultInjectionLeavesPrevious(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(target, "marker"), "old")

	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	snapshotWriteFile(t, filepath.Join(staging, "marker"), "new")

	t.Setenv("NIWA_TEST_FAULT", "error:simulated@snapshot-swap")
	err := SwapSnapshotAtomic(target, staging)
	if err == nil {
		t.Fatal("expected error from fault injection")
	}
	if !strings.Contains(err.Error(), "simulated") {
		t.Errorf("error %q does not propagate fault message", err.Error())
	}

	// Previous snapshot intact at canonical path.
	snapshotReadFile(t, filepath.Join(target, "marker"), "old")
	// Staging is untouched (fault fired before any rename).
	snapshotReadFile(t, filepath.Join(staging, "marker"), "new")
}

func TestSwapSnapshotAtomic_RejectsBadInput(t *testing.T) {
	if err := SwapSnapshotAtomic("", "/some/path"); err == nil {
		t.Error("expected error for empty target")
	}
	if err := SwapSnapshotAtomic("/some/path", ""); err == nil {
		t.Error("expected error for empty staging")
	}
	if err := SwapSnapshotAtomic("/same", "/same"); err == nil {
		t.Error("expected error for identical paths")
	}
}

func TestSwapSnapshotAtomic_TargetIsFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")

	snapshotWriteFile(t, target, "not a directory")
	if err := os.Mkdir(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}

	if err := SwapSnapshotAtomic(target, staging); err == nil {
		t.Error("expected error for non-directory target")
	}
}

// helpers

func snapshotWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func snapshotReadFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Errorf("file %s: got %q, want %q", path, string(data), want)
	}
}

func snapshotMustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("expected %s to not exist", path)
	}
}
