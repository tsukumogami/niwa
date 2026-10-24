//go:build unix

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// The umask is process-global. Package workspace already has t.Parallel
// tests, so withUmask cannot run in this binary the way
// internal/github/tar_mode_unix_test.go does. The helper subprocess
// below is the same pin — set the umask, create a file, assert the
// mode — without racing those parallel tests.

func TestApplyPlanWriteFileHonorsModeUnderUmask077(t *testing.T) {
	if os.Getenv("NIWA_APPLYPLAN_UMASK_HELPER") == "1" {
		syscall.Umask(0o077)
		dir := os.Getenv("NIWA_APPLYPLAN_UMASK_DIR")
		nested := filepath.Join(dir, "a", "b", "settings.json")
		secret := filepath.Join(dir, "env.local")
		script := filepath.Join(dir, "hook.sh")
		secretEntry := newPlanEntry(agentplan.OpWriteFile, secret, "TOKEN=x\n")
		secretEntry.Mode = 0o600
		scriptEntry := newPlanEntry(agentplan.OpWriteFile, script, "#!/bin/sh\n")
		scriptEntry.Mode = 0o755
		_, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{
			newPlanEntry(agentplan.OpWriteFile, nested, "{}\n"),
			secretEntry,
			scriptEntry,
		}})
		if err != nil {
			os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplyPlanWriteFileHonorsModeUnderUmask077$")
	cmd.Env = append(os.Environ(),
		"NIWA_APPLYPLAN_UMASK_HELPER=1",
		"NIWA_APPLYPLAN_UMASK_DIR="+dir,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("umask helper: %v\n%s", err, out)
	}

	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(dir, "a", "b", "settings.json"), 0o644},
		{filepath.Join(dir, "env.local"), 0o600},
		{filepath.Join(dir, "hook.sh"), 0o755},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if got := info.Mode().Perm(); got != tc.mode {
			t.Errorf("%s mode = %04o, want %04o (umask 077 was in force; a missing create-path chmod would leave this umask-narrowed)",
				tc.path, got, tc.mode)
		}
	}
}
