package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// instanceGitignorePattern is the glob pattern that the instance-root
// .gitignore must cover. It matches every file carrying the ".local"
// infix that niwa's materializers enforce for secret-bearing output.
const instanceGitignorePattern = "*.local*"

// EnsureInstanceGitignore makes sure the instance root's .gitignore
// contains *.local* and every extra pattern the caller names. It is
// idempotent: running twice with the same patterns is a no-op after
// the first run. The behavior depends on the current state of
// .gitignore:
//
//   - No file: create .gitignore holding the patterns, one per line.
//   - File missing some of them: append the missing ones on new
//     lines, preserving existing content. A trailing newline is added
//     before the appended lines when the existing file did not end
//     with one.
//   - File already containing all of them (exact line match,
//     whitespace-trimmed): do nothing.
//
// The instance root is a non-git directory itself, but users
// frequently place it inside a larger tracked working tree; the
// .gitignore at the instance root lets those outer repositories
// inherit the exclusions.
//
// extra carries the names niwa writes that *.local* does not reach.
// The infix is a convention niwa's own materializers enforce, and a
// generated configuration cannot follow it: it has to sit at the name
// its agent reads. Those files carry resolved secret material, so
// without their patterns here a workspace prepared inside an outer
// tracked tree could stage one.
func EnsureInstanceGitignore(instanceRoot string, extra ...string) error {
	path := filepath.Join(instanceRoot, ".gitignore")

	wanted := []string{instanceGitignorePattern}
	for _, pattern := range extra {
		if pattern != "" && !slices.Contains(wanted, pattern) {
			wanted = append(wanted, pattern)
		}
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading .gitignore: %w", err)
		}
		// Create a fresh file with just the patterns.
		data := []byte(strings.Join(wanted, "\n") + "\n")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing .gitignore: %w", err)
		}
		return nil
	}

	present := instanceGitignorePatterns(existing)
	missing := make([]string, 0, len(wanted))
	for _, pattern := range wanted {
		if !present[pattern] {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// Append the missing patterns, ensuring a trailing newline on the
	// prior content so each new line is on its own row.
	var buf strings.Builder
	buf.Write(existing)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buf.WriteByte('\n')
	}
	buf.WriteString(strings.Join(missing, "\n"))
	buf.WriteByte('\n')

	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("updating .gitignore: %w", err)
	}
	return nil
}

// instanceGitignorePatterns returns the set of exact pattern lines the
// .gitignore content already carries (comments and surrounding
// whitespace are ignored). The match is exact rather than permissive
// on purpose: treating a narrower pattern like "*.local.env" as
// equivalent to "*.local*" is not the invariant we want.
func instanceGitignorePatterns(data []byte) map[string]bool {
	present := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			present[line] = true
		}
	}
	return present
}
