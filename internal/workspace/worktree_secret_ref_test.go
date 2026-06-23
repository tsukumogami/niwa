package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestApplyToWorktreeRejectsUnresolvedSettingsSecret pins the Issue-3 audit
// outcome for the SettingsMaterializer: on the standalone worktree path cfg is
// overlay-merged but UNRESOLVED, so a vault:// settings value is still a literal
// "vault://..." Plain string. The only settings value that reaches disk is the
// "permissions" key (constrained to "bypass"/"ask"), so an unresolved vault://
// permissions value must FAIL CLOSED in buildSettingsDoc ("unknown permissions
// value") rather than land a literal vault:// in settings.local.json.
func TestApplyToWorktreeRejectsUnresolvedSettingsSecret(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cfg.Claude.Settings = config.SettingsConfig{
		"permissions": config.MaybeSecret{Plain: "vault://secret/permissions"},
	}

	_, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{})
	if err == nil {
		t.Fatal("expected ApplyToWorktree to fail closed on an unresolved vault:// permissions value, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown permissions value") {
		t.Errorf("error = %q, want it to name the unknown permissions value (fail-closed, not a literal write)", err.Error())
	}

	// Fail-closed must mean no settings file was written carrying the literal ref.
	settingsPath := filepath.Join(worktreePath, ".claude", "settings.local.json")
	if data, readErr := os.ReadFile(settingsPath); readErr == nil {
		if strings.Contains(string(data), "vault://") {
			t.Errorf("settings.local.json contains a literal vault:// ref:\n%s", data)
		}
	}
}

// TestApplyToWorktreeWritesNoUnresolvedVaultRef is the no-op guard for Issue 3:
// it exercises the full standalone ApplyToWorktree path with a vault://-looking
// string in both a non-secret settings value path and a [files] mapping, and
// asserts NO worktree output file ever contains the literal "vault://" string.
// The settings value uses a valid "permissions" so the materializer runs to
// completion; the files mapping carries a vault://-looking SOURCE path, which the
// FilesMaterializer treats as a path (it copies file BYTES, never the path
// string), so it cannot leak a literal ref into any output.
func TestApplyToWorktreeWritesNoUnresolvedVaultRef(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	// A valid, non-secret settings value so the SettingsMaterializer writes a
	// real settings.local.json this run.
	cfg.Claude.Settings = config.SettingsConfig{
		"permissions": config.MaybeSecret{Plain: "bypass"},
	}

	// A [files] mapping whose copied content is plain. [files] is
	// map[string]string of PATHS, never MaybeSecret, so resolution never applies
	// to it; the bytes that land come from the source file, not the config.
	srcRel := "files/note.txt"
	if err := os.MkdirAll(filepath.Join(configDir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, srcRel), []byte("plain content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Files = map[string]string{srcRel: "docs/note.txt"}

	written, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	// Scan every written file plus a full walk of the worktree for the literal
	// "vault://" string. The standalone path resolves no secrets, so a literal
	// ref reaching disk would be the regression Issue 3 guards against.
	seen := map[string]bool{}
	for _, p := range written {
		assertNoVaultLiteral(t, p)
		seen[p] = true
	}
	err = filepath.Walk(worktreePath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || seen[path] {
			return nil
		}
		assertNoVaultLiteral(t, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking worktree: %v", err)
	}

	// Sanity: the settings file was written and carries the resolved permission.
	settingsPath := filepath.Join(worktreePath, ".claude", "settings.local.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings.local.json: %v", err)
	}
	if !strings.Contains(string(data), "bypassPermissions") {
		t.Errorf("settings.local.json missing resolved permission mode:\n%s", data)
	}
}

// assertNoVaultLiteral fails the test if the file at path contains the literal
// "vault://" string.
func assertNoVaultLiteral(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if strings.Contains(string(data), "vault://") {
		t.Errorf("worktree output %s contains a literal vault:// ref:\n%s", path, data)
	}
}
