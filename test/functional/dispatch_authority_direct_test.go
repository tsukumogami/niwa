package functional

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDispatchAuthorityRefusalExitsNonZeroDirect runs a dispatch that authority
// resolution refuses straight through exec, with no pty and no `script`
// wrapper, and stdin not a terminal. It guards niwa's own exit status on that
// refusal, so a pty-driven scenario that sees the wrong code points at the
// harness rather than at niwa.
//
// It reuses the scenarios' sandbox, fixtures and fake claude so the workspace
// is the one the narrow-ceiling scenarios dispatch under: a grant ceiling of
// merge-pr only, which leaves cut-release outside it.
func TestDispatchAuthorityRefusalExitsNonZeroDirect(t *testing.T) {
	binPath := os.Getenv("NIWA_TEST_BINARY")
	if binPath == "" {
		t.Skip("NIWA_TEST_BINARY not set; run via 'make test-functional'")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		t.Fatalf("resolving binary path: %v", err)
	}

	sandbox, err := os.MkdirTemp(processSandboxRoot, "direct-*")
	if err != nil {
		t.Fatalf("allocating sandbox: %v", err)
	}
	homeDir := filepath.Join(sandbox, "home")
	tmpDir := filepath.Join(sandbox, "tmp")
	workspaceRoot := filepath.Join(sandbox, "workspace-root")
	for _, d := range []string{homeDir, tmpDir, workspaceRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sharedBinDir := filepath.Join(sandbox, "shared-bin")
	if err := writeFakeInfisical(sharedBinDir); err != nil {
		t.Fatal(err)
	}
	gs, err := newLocalGitServer(filepath.Join(sandbox, "gitserver"))
	if err != nil {
		t.Fatal(err)
	}
	s := &testState{
		binPath:        absBin,
		sandbox:        sandbox,
		homeDir:        homeDir,
		tmpDir:         tmpDir,
		workspaceRoot:  workspaceRoot,
		sharedBinDir:   sharedBinDir,
		envOverrides:   make(map[string]string),
		gitServer:      gs,
		repoURLs:       make(map[string]string),
		stagedFiles:    make(map[string]string),
		stagedSymlinks: make(map[string]string),
	}
	t.Cleanup(func() {
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			s.cleanups[i]()
		}
	})

	ctx := setState(context.Background(), s)
	if _, err := aConfigRepoWithAuthorityFixture(ctx, "ws", "narrow-ceiling"); err != nil {
		t.Fatal(err)
	}
	if _, err := iRunNiwaInitFromConfigRepo(ctx, "ws"); err != nil {
		t.Fatal(err)
	}
	if s.exitCode != 0 {
		t.Fatalf("niwa init exited %d\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	// A fake claude, so a dispatch that wrongly got past the refusal could
	// never reach a real one.
	if _, err := aFakeClaudeForDispatchWithSession(ctx, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	before := rootEntries(s)

	cmd := exec.Command(absBin, "dispatch", "do a thing", "--detach", "--grant", "cut-release")
	cmd.Dir = workspaceRoot
	cmd.Env = s.buildEnv()
	cmd.Stdin = nil // /dev/null: not a terminal
	out, runErr := cmd.CombinedOutput()

	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			t.Fatalf("running niwa dispatch: %v\n%s", runErr, out)
		}
		code = exitErr.ExitCode()
	}
	if code != 1 {
		t.Fatalf("niwa dispatch exited %d on an authority refusal, want 1\n%s", code, out)
	}
	const want = "authority cut-release (requested by --grant) is outside the grant ceiling"
	if !strings.Contains(string(out), want) {
		t.Fatalf("output lacks %q:\n%s", want, out)
	}
	if after := rootEntries(s); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Fatalf("the refused dispatch changed the workspace root:\nbefore %v\nafter  %v", before, after)
	}
}
