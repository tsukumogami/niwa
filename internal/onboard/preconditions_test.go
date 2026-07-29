package onboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// --- EnsureAuthenticatedSession (AC-36) ---

func TestEnsureAuthenticatedSession_AlreadyAuthenticatedNeverPauses(t *testing.T) {
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: true, Organization: "acme"}, nil
	}
	pauseCalls := 0
	pause := func(prompt string) error {
		pauseCalls++
		return nil
	}

	if err := EnsureAuthenticatedSession(context.Background(), checker, pause); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseCalls != 0 {
		t.Errorf("pause called %d times, want 0 -- an already-authenticated session must never pause", pauseCalls)
	}
}

// TestEnsureAuthenticatedSession_MissingSessionPausesThenResumes covers
// AC-36 directly: no authenticated session at first, the wizard pauses
// (never fails fast), and resumes once the operator logs in.
func TestEnsureAuthenticatedSession_MissingSessionPausesThenResumes(t *testing.T) {
	callCount := 0
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		callCount++
		if callCount == 1 {
			return infisical.SessionStatus{Authenticated: false}, nil
		}
		return infisical.SessionStatus{Authenticated: true}, nil
	}

	var gotPrompt string
	pause := func(prompt string) error {
		gotPrompt = prompt
		return nil
	}

	if err := EnsureAuthenticatedSession(context.Background(), checker, pause); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("checker called %d times, want 2 (check, pause, re-check)", callCount)
	}
	if !strings.Contains(gotPrompt, "infisical login") {
		t.Errorf("pause prompt = %q, want it to mention `infisical login`", gotPrompt)
	}
}

func TestEnsureAuthenticatedSession_PausesRepeatedlyUntilAuthenticated(t *testing.T) {
	callCount := 0
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		callCount++
		return infisical.SessionStatus{Authenticated: callCount >= 3}, nil
	}
	pauseCalls := 0
	pause := func(prompt string) error {
		pauseCalls++
		return nil
	}

	if err := EnsureAuthenticatedSession(context.Background(), checker, pause); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseCalls != 2 {
		t.Errorf("pause called %d times, want 2 (unauthenticated on checks 1 and 2)", pauseCalls)
	}
}

func TestEnsureAuthenticatedSession_CheckerErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{}, wantErr
	}
	err := EnsureAuthenticatedSession(context.Background(), checker, func(string) error { return nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, wantErr)
	}
}

func TestEnsureAuthenticatedSession_NilPauseWithNoSessionIsError(t *testing.T) {
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: false}, nil
	}
	err := EnsureAuthenticatedSession(context.Background(), checker, nil)
	if err == nil {
		t.Fatal("want an error when pause is nil and no session is authenticated, got nil")
	}
}

func TestEnsureAuthenticatedSession_PauseErrorPropagates(t *testing.T) {
	checker := func(ctx context.Context) (infisical.SessionStatus, error) {
		return infisical.SessionStatus{Authenticated: false}, nil
	}
	wantErr := errors.New("stdin closed")
	pause := func(string) error { return wantErr }

	err := EnsureAuthenticatedSession(context.Background(), checker, pause)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want it to wrap %v", err, wantErr)
	}
}

// --- EnsurePersonalOverlay (AC-37) ---

func TestEnsurePersonalOverlay_RegistersUnregisteredPointer(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	overlayDir := filepath.Join(t.TempDir(), "overlay")
	// Pre-create as an existing git repo so this test isolates the
	// pointer-registration half only.
	mustGitInit(t, overlayDir)

	result, err := EnsurePersonalOverlay(context.Background(), EnsurePersonalOverlayParams{
		OverlayDir: overlayDir,
		Repo:       "acme/dot-niwa-overlay",
		GitInvoker: workspace.StdGitInvoker(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.PointerWrite.Changed {
		t.Error("expected the pointer write to report Changed=true")
	}
	if result.ScaffoldedNew {
		t.Error("overlay repo already existed; ScaffoldedNew must be false")
	}

	cfgPath := filepath.Join(xdg, "niwa", "config.toml")
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("reading config.toml: %v", readErr)
	}
	if !strings.Contains(string(data), "acme/dot-niwa-overlay") {
		t.Errorf("config.toml missing registered repo:\n%s", data)
	}
}

func TestEnsurePersonalOverlay_AlreadyRegisteredIsNoOp(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if _, err := WriteLocalPointer("acme/dot-niwa-overlay"); err != nil {
		t.Fatalf("seeding pointer: %v", err)
	}

	overlayDir := filepath.Join(t.TempDir(), "overlay")
	mustGitInit(t, overlayDir)

	result, err := EnsurePersonalOverlay(context.Background(), EnsurePersonalOverlayParams{
		OverlayDir: overlayDir,
		Repo:       "should-be-ignored/because-already-registered",
		GitInvoker: workspace.StdGitInvoker(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PointerWrite.Changed {
		t.Error("pointer already registered; PointerWrite.Changed must be false (zero value)")
	}
}

func TestEnsurePersonalOverlay_UnregisteredPointerNoRepoSuppliedIsError(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	overlayDir := filepath.Join(t.TempDir(), "overlay")
	mustGitInit(t, overlayDir)

	_, err := EnsurePersonalOverlay(context.Background(), EnsurePersonalOverlayParams{
		OverlayDir: overlayDir,
		GitInvoker: workspace.StdGitInvoker(),
	})
	if err == nil {
		t.Fatal("want an error when the pointer is unregistered and no repo was supplied, got nil")
	}
}

// TestEnsurePersonalOverlay_ScaffoldsNewRepoAndGuidesNoRemoteCreated is
// the core AC-37 test: when the overlay repo doesn't exist yet, the
// function scaffolds it locally (git init + niwa.toml + commit),
// pauses with guidance, and never issues any remote-creating command.
func TestEnsurePersonalOverlay_ScaffoldsNewRepoAndGuidesNoRemoteCreated(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if _, err := WriteLocalPointer("acme/dot-niwa-overlay"); err != nil {
		t.Fatalf("seeding pointer: %v", err)
	}

	overlayDir := filepath.Join(t.TempDir(), "overlay-not-yet-created")

	var pausePrompt string
	pauseCalls := 0
	pause := func(prompt string) error {
		pauseCalls++
		pausePrompt = prompt
		return nil
	}

	result, err := EnsurePersonalOverlay(context.Background(), EnsurePersonalOverlayParams{
		OverlayDir: overlayDir,
		GitInvoker: workspace.StdGitInvoker(),
		Pause:      pause,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.ScaffoldedNew {
		t.Error("want ScaffoldedNew=true for a not-yet-existing overlay repo")
	}
	if pauseCalls != 1 {
		t.Fatalf("pause called %d times, want exactly 1", pauseCalls)
	}
	if !strings.Contains(pausePrompt, "push") {
		t.Errorf("pause prompt = %q, want it to guide the operator to push", pausePrompt)
	}

	if _, statErr := os.Stat(filepath.Join(overlayDir, ".git")); statErr != nil {
		t.Fatalf("expected a local git repo at %s: %v", overlayDir, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(overlayDir, workspace.GlobalConfigOverrideFile)); statErr != nil {
		t.Fatalf("expected a scaffolded %s: %v", workspace.GlobalConfigOverrideFile, statErr)
	}

	// AC-37: no remote was ever configured -- `git remote` lists nothing.
	out, remoteErr := workspace.StdGitInvoker().CommandContext(context.Background(), "-C", overlayDir, "remote").CombinedOutput()
	if remoteErr != nil {
		t.Fatalf("git remote: %v", remoteErr)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected no git remotes to be configured, got: %q", out)
	}

	// A commit exists (the scaffold was committed locally).
	logOut, logErr := workspace.StdGitInvoker().CommandContext(context.Background(), "-C", overlayDir, "log", "--oneline").CombinedOutput()
	if logErr != nil {
		t.Fatalf("git log: %v\n%s", logErr, logOut)
	}
	if strings.TrimSpace(string(logOut)) == "" {
		t.Error("expected at least one local commit")
	}
}

func TestEnsurePersonalOverlay_ExistingRepoIsNoOp(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if _, err := WriteLocalPointer("acme/dot-niwa-overlay"); err != nil {
		t.Fatalf("seeding pointer: %v", err)
	}

	overlayDir := filepath.Join(t.TempDir(), "overlay")
	mustGitInit(t, overlayDir)

	pauseCalls := 0
	pause := func(string) error {
		pauseCalls++
		return nil
	}

	result, err := EnsurePersonalOverlay(context.Background(), EnsurePersonalOverlayParams{
		OverlayDir: overlayDir,
		GitInvoker: workspace.StdGitInvoker(),
		Pause:      pause,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ScaffoldedNew {
		t.Error("repo already existed; ScaffoldedNew must be false")
	}
	if pauseCalls != 0 {
		t.Errorf("pause called %d times, want 0 -- an existing overlay repo must never pause", pauseCalls)
	}
}

// mustGitInit creates dir and runs `git init` in it via the real git
// binary (matching config_authoring_test.go's own precedent of testing
// against real git rather than a fake for these repo-shape assertions).
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	out, err := workspace.StdGitInvoker().CommandContext(context.Background(), "-C", dir, "init").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}
