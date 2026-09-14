package workspace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
)

// configTarball builds a gzipped tarball shaped like the one GitHub
// serves for a config repo: a single wrapper directory, and under it a
// `config` subpath holding a worktree hook committed executable.
func configTarball(t *testing.T, hookMode int64, script string) []byte {
	t.Helper()

	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)

	for _, dir := range []string{
		"cfgrepo-abc1234/",
		"cfgrepo-abc1234/config/",
		"cfgrepo-abc1234/config/worktree-hooks/",
		"cfgrepo-abc1234/config/worktree-hooks/apply/",
	} {
		if err := tw.WriteHeader(&tar.Header{Name: dir, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatal(err)
		}
	}

	body := []byte(script)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "cfgrepo-abc1234/config/worktree-hooks/apply/01-bootstrap.sh",
		Mode:     hookMode,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

// TestApplyToWorktreeRunsHookExtractedFromTarball closes the seam issue
// #306 fell through. The extractor lives in internal/github and the hook
// runner lives here, and for as long as the defect existed each package's
// own tests passed: extraction wrote the file it was asked to write, and
// the runner correctly skipped a file with no exec bit. What nothing
// asserted was the one thing a user cares about -- that a hook a config
// repo commits as 100755 still runs after niwa fetches the config.
//
// So this test does not stat anything. It extracts through the real
// ExtractSubpath, hands the result to the real ApplyToWorktree, and asks
// whether the hook ran. A future change that drops the mode on the
// extractor side fails here as "the hook did not run", which is the
// symptom that was reported, rather than as a number mismatch someone
// has to translate.
//
// The counterpart, TestApplyToWorktreeNonExecutableHookSkipped, pins that
// a hook committed WITHOUT the exec bit is still skipped -- so this pair
// says the mode is carried through, not that everything is made runnable.
func TestApplyToWorktreeRunsHookExtractedFromTarball(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX exec bits, and /bin/sh hooks, are not a thing on Windows")
	}

	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	// The hook records that it ran, and records the worktree context it
	// was handed, so a hook that runs with a broken environment is not
	// mistaken for a pass.
	script := "#!/bin/sh\nprintf '%s\\n' \"$NIWA_WORKTREE_PURPOSE\" > \"$NIWA_WORKTREE_PATH/hook-ran.txt\"\n"
	raw := configTarball(t, 0o755, script)

	if err := github.ExtractSubpath(bytes.NewReader(raw), "config", configDir); err != nil {
		t.Fatalf("extracting the config tarball: %v", err)
	}

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath,
		"apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	marker, err := os.ReadFile(filepath.Join(worktreePath, "hook-ran.txt"))
	if err != nil {
		// Name the cause, because this is the failure issue #306
		// reported and the mode is the first thing to look at.
		hook := filepath.Join(configDir, "worktree-hooks", "apply", "01-bootstrap.sh")
		mode := "could not stat it"
		if info, statErr := os.Stat(hook); statErr == nil {
			mode = info.Mode().Perm().String()
		}
		t.Fatalf("a hook the tarball carried as 0755 did not run after extraction "+
			"(extracted mode: %s): %v", mode, err)
	}
	if got := string(bytes.TrimSpace(marker)); got != "ship-the-thing" {
		t.Errorf("hook ran but saw NIWA_WORKTREE_PURPOSE = %q, want %q", got, "ship-the-thing")
	}
}
