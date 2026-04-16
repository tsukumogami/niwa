package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureInstanceGitignoreCreatesFile covers the case where no
// .gitignore exists yet: the helper writes one containing just
// "*.local*".
func TestEnsureInstanceGitignoreCreatesFile(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if string(data) != "*.local*\n" {
		t.Errorf(".gitignore = %q, want %q", string(data), "*.local*\n")
	}
}

// TestEnsureInstanceGitignoreAppendsPattern covers a pre-existing
// .gitignore that does not already carry the *.local* pattern: the
// helper appends it on a new line and preserves prior content.
func TestEnsureInstanceGitignoreAppendsPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	initial := "node_modules/\n*.bak\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "node_modules/\n") {
		t.Errorf(".gitignore lost original content:\n%s", content)
	}
	if !strings.Contains(content, "*.bak\n") {
		t.Errorf(".gitignore lost original content:\n%s", content)
	}
	if !strings.Contains(content, "*.local*\n") {
		t.Errorf(".gitignore missing *.local* after append:\n%s", content)
	}
}

// TestEnsureInstanceGitignoreAppendsPatternWhenNoTrailingNewline
// covers a pre-existing .gitignore that lacks a trailing newline:
// the helper inserts one before appending so the new pattern stays
// on its own line.
func TestEnsureInstanceGitignoreAppendsPatternWhenNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules/"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if string(data) != "node_modules/\n*.local*\n" {
		t.Errorf(".gitignore = %q, want %q", string(data), "node_modules/\n*.local*\n")
	}
}

// TestEnsureInstanceGitignoreIdempotent covers running the helper
// twice on the same directory: the second call is a strict no-op
// (the file is not rewritten with duplicate entries).
func TestEnsureInstanceGitignoreIdempotent(t *testing.T) {
	dir := t.TempDir()

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("first EnsureInstanceGitignore: %v", err)
	}

	path := filepath.Join(dir, ".gitignore")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("second EnsureInstanceGitignore: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore after second call: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("idempotency violated:\nbefore:\n%s\nafter:\n%s", string(before), string(after))
	}

	// Also verify the pattern does not appear twice.
	if strings.Count(string(after), "*.local*") != 1 {
		t.Errorf("*.local* appears %d times, want 1:\n%s",
			strings.Count(string(after), "*.local*"), string(after))
	}
}

// TestEnsureInstanceGitignoreAlreadyHasPattern covers a pre-existing
// .gitignore that already carries "*.local*": the helper leaves the
// file unchanged rather than appending a duplicate.
func TestEnsureInstanceGitignoreAlreadyHasPattern(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	initial := "node_modules/\n*.local*\nbuild/\n"
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureInstanceGitignore(dir); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if string(data) != initial {
		t.Errorf(".gitignore changed; want unchanged:\ngot:\n%s\nwant:\n%s", string(data), initial)
	}
}
