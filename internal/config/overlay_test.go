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

// writeOverlayFile writes content to workspace-overlay.toml in a temp dir
// and returns the path.
func writeOverlayFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace-overlay.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing overlay file: %v", err)
	}
	return path
}

// TestParseOverlay_Valid verifies a well-formed overlay parses without error.
func TestParseOverlay_Valid(t *testing.T) {
	toml := `
[[sources]]
org = "myorg"
repos = ["repo-a", "repo-b"]

[groups.alpha]
visibility = "public"
repos = ["repo-a"]

[claude.content.repos.repo-a]
source = "repos/repo-a.md"

[claude.content.repos.repo-b]
overlay = "overlay/repo-b.md"

[files]
"src/extra.md" = "docs/extra.md"
`
	path := writeOverlayFile(t, toml)
	o, err := ParseOverlay(path)
	if err != nil {
		t.Fatalf("ParseOverlay returned unexpected error: %v", err)
	}
	if len(o.Sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(o.Sources))
	}
}

// TestParseOverlay_SourceWithoutRepos verifies that sources without explicit
// repos list are rejected.
func TestParseOverlay_SourceWithoutRepos(t *testing.T) {
	toml := `
[[sources]]
org = "myorg"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for source without repos, got nil")
	}
	if !strings.Contains(err.Error(), "repos list is required") {
		t.Errorf("error %q does not mention repos list requirement", err.Error())
	}
}

// TestParseOverlay_ContentBothSourceAndOverlay verifies that content entries
// with both source and overlay set are rejected.
func TestParseOverlay_ContentBothSourceAndOverlay(t *testing.T) {
	toml := `
[[sources]]
org = "myorg"
repos = ["repo-a"]

[claude.content.repos.repo-a]
source = "repos/repo-a.md"
overlay = "overlay/repo-a.md"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for content entry with both source and overlay, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one of source or overlay") {
		t.Errorf("error %q does not mention source/overlay exclusivity", err.Error())
	}
}

// TestParseOverlay_ContentNeitherSourceNorOverlay verifies that content entries
// with neither source nor overlay set are rejected.
func TestParseOverlay_ContentNeitherSourceNorOverlay(t *testing.T) {
	toml := `
[[sources]]
org = "myorg"
repos = ["repo-a"]

[claude.content.repos.repo-a]
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for content entry with neither source nor overlay, got nil")
	}
	if !strings.Contains(err.Error(), "exactly one of source or overlay") {
		t.Errorf("error %q does not mention source/overlay exclusivity", err.Error())
	}
}

// TestParseOverlay_AbsolutePathInFiles verifies that absolute destination paths
// in [files] are rejected.
func TestParseOverlay_AbsolutePathInFiles(t *testing.T) {
	toml := `
[files]
"src/file.md" = "/absolute/dest.md"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for absolute path in files, got nil")
	}
	if !strings.Contains(err.Error(), "absolute paths are not allowed") {
		t.Errorf("error %q does not mention absolute paths", err.Error())
	}
}

// TestParseOverlay_DotDotInFiles verifies that ".." components in [files]
// destination paths are rejected.
func TestParseOverlay_DotDotInFiles(t *testing.T) {
	toml := `
[files]
"src/file.md" = "../escape/dest.md"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .. in files destination, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error %q does not mention path traversal", err.Error())
	}
}

// TestParseOverlay_DotDotInFilesSource verifies that ".." components in [files]
// source keys (the TOML key side of the map) are rejected.
func TestParseOverlay_DotDotInFilesSource(t *testing.T) {
	tests := []struct {
		name    string
		tomlDoc string
	}{
		{
			name: "dotdot source key",
			tomlDoc: `
[files]
"../../../etc/passwd" = "docs/safe.md"
`,
		},
		{
			name: "absolute source key",
			tomlDoc: `
[files]
"/etc/passwd" = "docs/safe.md"
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeOverlayFile(t, tc.tomlDoc)
			_, err := ParseOverlay(path)
			if err == nil {
				t.Fatal("expected error for unsafe files source key, got nil")
			}
		})
	}
}

// TestParseOverlay_ProtectedDestinationClaude verifies that [files] destination
// paths beginning with .claude/ are rejected.
func TestParseOverlay_ProtectedDestinationClaude(t *testing.T) {
	toml := `
[files]
"src/file.md" = ".claude/settings.json"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .claude/ destination, got nil")
	}
	if !strings.Contains(err.Error(), "protected directory") {
		t.Errorf("error %q does not mention protected directory", err.Error())
	}
}

// TestParseOverlay_ProtectedDestinationNiwa verifies that [files] destination
// paths beginning with .niwa/ are rejected.
func TestParseOverlay_ProtectedDestinationNiwa(t *testing.T) {
	toml := `
[files]
"src/file.md" = ".niwa/workspace.toml"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .niwa/ destination, got nil")
	}
	if !strings.Contains(err.Error(), "protected directory") {
		t.Errorf("error %q does not mention protected directory", err.Error())
	}
}

// TestParseOverlay_AbsoluteHookScript verifies that absolute hook script paths
// are rejected.
func TestParseOverlay_AbsoluteHookScript(t *testing.T) {
	toml := `
[[claude.hooks.pre_tool_use]]
scripts = ["/etc/evil.sh"]
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for absolute hook script path, got nil")
	}
	if !strings.Contains(err.Error(), "must be relative") {
		t.Errorf("error %q does not mention relative path requirement", err.Error())
	}
}

// TestParseOverlay_DotDotInHookScript verifies that ".." components in hook
// script paths are rejected.
func TestParseOverlay_DotDotInHookScript(t *testing.T) {
	toml := `
[[claude.hooks.pre_tool_use]]
scripts = ["../escape/evil.sh"]
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .. in hook script path, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error %q does not mention path traversal", err.Error())
	}
}

// TestParseOverlay_DotDotInContentSource verifies that ".." in content source
// paths is rejected.
func TestParseOverlay_DotDotInContentSource(t *testing.T) {
	toml := `
[[sources]]
org = "myorg"
repos = ["repo-a"]

[claude.content.repos.repo-a]
source = "../outside/repo-a.md"
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .. in content source, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error %q does not mention path traversal", err.Error())
	}
}

// TestParseOverlay_DotDotInEnvFiles verifies that ".." in env file paths is rejected.
func TestParseOverlay_DotDotInEnvFiles(t *testing.T) {
	toml := `
[env]
files = ["../escape.env"]
`
	path := writeOverlayFile(t, toml)
	_, err := ParseOverlay(path)
	if err == nil {
		t.Fatal("expected error for .. in env files, got nil")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Errorf("error %q does not mention path traversal", err.Error())
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
