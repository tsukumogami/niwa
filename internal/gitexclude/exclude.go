// Package gitexclude records niwa's ignore coverage in a managed repository's
// .git/info/exclude so niwa-authored files stay invisible to the repository's
// git status. It is a leaf package (stdlib only) so both internal/workspace
// (the apply path) and internal/mcp (the worktree-create path) can use it
// without an import cycle.
package gitexclude

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// errNotGitRepo signals that the target working tree is not a git repository,
// so there is no git status to keep clean and EnsureRepoExclude is a no-op.
var errNotGitRepo = errors.New("not a git repository")

// niwa records its ignore coverage between these sentinel lines in a managed
// repository's .git/info/exclude. The delimited block lets niwa rewrite its own
// patterns in place on every apply while preserving any user-authored exclude
// content outside the markers.
const (
	niwaExcludeBegin = "# >>> niwa managed >>>"
	niwaExcludeEnd   = "# <<< niwa managed <<<"
)

// niwaExcludePatterns are the ignore patterns niwa writes so its output stays
// invisible to a managed repository's git status. "*.local*" covers every
// materialized managed-repo file (all carry the .local infix); ".niwa/" covers
// the per-worktree scaffolding niwa writes into a worktree.
var niwaExcludePatterns = []string{"*.local*", ".niwa/"}

// EnsureRepoExclude records niwa's ignore coverage in the git exclude file for
// the working tree at tree. The exclude file (.git/info/exclude) is
// repository-local and never committed, so recording coverage changes no
// tracked file; it is resolved from the shared common git directory, so one
// write covers the primary checkout and every linked worktree of the
// repository.
//
// The write is idempotent (the niwa block is rewritten in place) and preserves
// any content the file already holds outside the niwa markers. An unwritable
// exclude file in a real repository returns an error so callers can fail closed
// rather than leave niwa-authored files visible. A tree that is not a git
// repository at all is a silent no-op: there is no git status to pollute.
func EnsureRepoExclude(tree string) error {
	commonDir, err := gitCommonDir(tree)
	if err != nil {
		if errors.Is(err, errNotGitRepo) {
			return nil
		}
		return fmt.Errorf("resolving git common dir for %s: %w", tree, err)
	}

	infoDir := filepath.Join(commonDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", infoDir, err)
	}

	excludePath := filepath.Join(infoDir, "exclude")
	existing, err := os.ReadFile(excludePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", excludePath, err)
	}

	updated := renderNiwaBlock(existing)
	if bytes.Equal(existing, updated) {
		return nil
	}

	if err := os.WriteFile(excludePath, updated, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", excludePath, err)
	}
	return nil
}

// gitCommonDir resolves the shared git directory for the working tree at tree.
// For a primary checkout this is <tree>/.git; for a linked worktree it is the
// primary repository's .git. git's --git-common-dir output may be relative to
// tree, in which case it is resolved against tree.
func gitCommonDir(tree string) (string, error) {
	cmd := exec.Command("git", "-C", tree, "rev-parse", "--git-common-dir")
	// Force the C locale so git's error text is stable for the not-a-repo check.
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", errNotGitRepo
		}
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("git rev-parse --git-common-dir returned empty output")
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(tree, dir)
	}
	return dir, nil
}

// renderNiwaBlock returns existing with niwa's managed exclude block inserted or
// replaced in place. Content outside the niwa markers is preserved verbatim.
// The function is pure and idempotent: renderNiwaBlock(renderNiwaBlock(x)) is
// equal to renderNiwaBlock(x). The result always ends with a trailing newline.
func renderNiwaBlock(existing []byte) []byte {
	blockLines := []string{niwaExcludeBegin}
	blockLines = append(blockLines, niwaExcludePatterns...)
	blockLines = append(blockLines, niwaExcludeEnd)

	var lines []string
	if len(existing) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n")
	}

	begin, end := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case niwaExcludeBegin:
			if begin == -1 {
				begin = i
			}
		case niwaExcludeEnd:
			end = i
		}
	}

	var result []string
	if begin != -1 && end != -1 && end >= begin {
		// Replace the existing niwa block in place, keeping surrounding content.
		result = append(result, lines[:begin]...)
		result = append(result, blockLines...)
		result = append(result, lines[end+1:]...)
	} else {
		// No existing block: append after current content, separated by a blank
		// line when the file already has content.
		result = append(result, lines...)
		if len(result) > 0 && result[len(result)-1] != "" {
			result = append(result, "")
		}
		result = append(result, blockLines...)
	}

	return []byte(strings.Join(result, "\n") + "\n")
}
