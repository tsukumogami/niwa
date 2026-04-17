package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// instanceGitignorePattern is the glob pattern that the instance-root
// .gitignore must cover. It matches every file carrying the ".local"
// infix that niwa's materializers enforce for secret-bearing output.
const instanceGitignorePattern = "*.local*"

// EnsureInstanceGitignore makes sure the instance root's .gitignore
// contains *.local*. It is idempotent: running twice on the same
// directory is a no-op after the first run. The behavior depends on
// the current state of .gitignore:
//
//   - No file: create .gitignore with a single "*.local*" line.
//   - File missing the pattern: append "*.local*" on a new line,
//     preserving existing content. A trailing newline is added
//     before the appended line when the existing file did not end
//     with one.
//   - File already containing the pattern (exact line match,
//     whitespace-trimmed): do nothing.
//
// The instance root is a non-git directory itself, but users
// frequently place it inside a larger tracked working tree; the
// .gitignore at the instance root lets those outer repositories
// inherit the *.local* exclusion.
func EnsureInstanceGitignore(instanceRoot string) error {
	path := filepath.Join(instanceRoot, ".gitignore")

	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading .gitignore: %w", err)
		}
		// Create a fresh file with just the pattern.
		data := []byte(instanceGitignorePattern + "\n")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
		return nil
	}

	if hasInstanceGitignorePattern(existing) {
		return nil
	}

	// Append the pattern, ensuring a trailing newline on the prior
	// content so the new line is on its own row.
	var buf strings.Builder
	buf.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buf.WriteByte('\n')
	}
	buf.WriteString(instanceGitignorePattern)
	buf.WriteByte('\n')

	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("updating .gitignore: %w", err)
	}
	return nil
}

// hasInstanceGitignorePattern reports whether the .gitignore content
// already contains an exact "*.local*" line (comments and
// surrounding whitespace are ignored). A more permissive match would
// risk treating a narrower pattern like "*.local.env" as equivalent,
// which is not the invariant we want.
func hasInstanceGitignorePattern(data []byte) bool {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == instanceGitignorePattern {
			return true
		}
	}
	return false
}
