package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveOverlayURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantURL string
		wantOK  bool
	}{
		// HTTPS URLs
		{
			name:    "https without .git suffix",
			input:   "https://github.com/acme/myrepo",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		{
			name:    "https with .git suffix",
			input:   "https://github.com/acme/myrepo.git",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		// SSH URLs
		{
			name:    "ssh with .git suffix",
			input:   "git@github.com:acme/myrepo.git",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		{
			name:    "ssh without .git suffix",
			input:   "git@github.com:acme/myrepo",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		// Shorthand
		{
			name:    "shorthand org/repo",
			input:   "acme/myrepo",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		{
			name:    "shorthand with .git suffix",
			input:   "acme/myrepo.git",
			wantURL: "acme/myrepo-overlay",
			wantOK:  true,
		},
		// Unparseable inputs
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
		{
			name:   "no slash",
			input:  "justrepo",
			wantOK: false,
		},
		{
			name:   "absolute path",
			input:  "/org/repo",
			wantOK: false,
		},
		{
			name:   "https missing repo",
			input:  "https://github.com/org/",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := DeriveOverlayURL(tc.input)
			if ok != tc.wantOK {
				t.Errorf("DeriveOverlayURL(%q) ok=%v, want %v", tc.input, ok, tc.wantOK)
			}
			if ok && got != tc.wantURL {
				t.Errorf("DeriveOverlayURL(%q) = %q, want %q", tc.input, got, tc.wantURL)
			}
		})
	}
}

func TestOverlayDir_WithXDGConfigHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := OverlayDir("acme/myrepo")
	if err != nil {
		t.Fatalf("OverlayDir returned error: %v", err)
	}
	want := filepath.Join(tmp, "niwa", "overlays", "acme-myrepo")
	if got != want {
		t.Errorf("OverlayDir = %q, want %q", got, want)
	}
}

func TestOverlayDir_WithoutXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	got, err := OverlayDir("acme/myrepo")
	if err != nil {
		t.Fatalf("OverlayDir returned error: %v", err)
	}
	want := filepath.Join(home, ".config", "niwa", "overlays", "acme-myrepo")
	if got != want {
		t.Errorf("OverlayDir = %q, want %q", got, want)
	}
}

func TestOverlayDir_HTTPSInput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := OverlayDir("https://github.com/acme/myrepo.git")
	if err != nil {
		t.Fatalf("OverlayDir returned error: %v", err)
	}
	want := filepath.Join(tmp, "niwa", "overlays", "acme-myrepo")
	if got != want {
		t.Errorf("OverlayDir = %q, want %q", got, want)
	}
}

func TestOverlayDir_SSHInput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := OverlayDir("git@github.com:acme/myrepo.git")
	if err != nil {
		t.Fatalf("OverlayDir returned error: %v", err)
	}
	want := filepath.Join(tmp, "niwa", "overlays", "acme-myrepo")
	if got != want {
		t.Errorf("OverlayDir = %q, want %q", got, want)
	}
}

func TestOverlayDir_InvalidURL(t *testing.T) {
	_, err := OverlayDir("notavalidurl")
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
}

func TestCloneOrSyncOverlay_MissingDirReturnsFirstTime(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "nonexistent")

	// Use a local path that doesn't exist as a repo URL — the clone will fail,
	// but it should fail with firstTime=true.
	firstTime, err := CloneOrSyncOverlay("/does/not/exist/as/a/repo", dir)
	if !firstTime {
		t.Errorf("expected firstTime=true for missing dir, got false")
	}
	if err == nil {
		t.Error("expected error when cloning from invalid URL, got nil")
	}
}

func TestCloneOrSyncOverlay_ExistingValidRepoReturnsNotFirstTime(t *testing.T) {
	tmp := t.TempDir()

	// Create a minimal git repo to serve as the "remote".
	remoteDir := filepath.Join(tmp, "remote")
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRunIn(t, remoteDir, "init", "--initial-branch=main")
	gitRunIn(t, remoteDir, "config", "user.email", "test@test.com")
	gitRunIn(t, remoteDir, "config", "user.name", "Test")
	gitRunIn(t, remoteDir, "commit", "--allow-empty", "-m", "init")

	// Clone it.
	cloneDir := filepath.Join(tmp, "clone")
	firstTime, err := CloneOrSyncOverlay(remoteDir, cloneDir)
	if err != nil {
		t.Fatalf("initial clone failed: %v", err)
	}
	if !firstTime {
		t.Error("expected firstTime=true on initial clone")
	}

	// Sync (pull) — should return firstTime=false.
	firstTime2, err := CloneOrSyncOverlay(remoteDir, cloneDir)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if firstTime2 {
		t.Error("expected firstTime=false on subsequent sync")
	}
}

func TestOverlayDir_DeriveAndDir_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	sourceURL := "https://github.com/myorg/myconfig.git"
	conventionURL, ok := DeriveOverlayURL(sourceURL)
	if !ok {
		t.Fatal("DeriveOverlayURL returned ok=false")
	}
	if !strings.HasSuffix(conventionURL, "-overlay") {
		t.Errorf("convention URL %q does not end with -overlay", conventionURL)
	}

	dir, err := OverlayDir(conventionURL)
	if err != nil {
		t.Fatalf("OverlayDir error: %v", err)
	}
	want := filepath.Join(tmp, "niwa", "overlays", "myorg-myconfig-overlay")
	if dir != want {
		t.Errorf("OverlayDir = %q, want %q", dir, want)
	}
}

// gitRunIn runs a git command inside dir, failing the test on error.
func gitRunIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
