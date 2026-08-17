package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// installWorktreeCodex gives a niwa-managed worktree the same two per-tree
// writes every clone gets: the `.codex` payload delivery, and a composed
// `AGENTS.override.md`.
//
// Nothing here is worktree-specific machinery, and that is the finding it rests
// on rather than an assumption: a real `git worktree add` tree's `.git` is a
// regular file, and Codex's project-root marker check is a bare metadata stat
// that a file satisfies, so the walk stops at the worktree root exactly as it
// stops at a clone's. The payload therefore reaches a worktree session through
// the same link, and the override claims the same context slot. What differs is
// only the content: the framing names this worktree's repository, purpose, and
// branch, and a single shared file could not carry that for N concurrent
// worktrees.
//
// Three rules the clone path already establishes apply here unchanged:
//
//   - The conflict rule (issue 7's DetectCodexConflicts). The ownership test is
//     the generation marker plus untrackedness, not a managed-file record --
//     which is what lets the standalone `niwa worktree apply` path, which
//     persists no records, recognize the override it wrote on its own previous
//     run instead of refusing to refresh it.
//   - The coupled suppression: a `.codex` conflict suppresses the composed
//     override too, because the override's byte budget is declared in the
//     payload config only the refused delivery would have put in reach.
//   - Loud reporting. Every refusal names its path on stderr; a quiet skip is
//     the silent failure the whole rule exists to prevent.
//
// No trust entry is written for a worktree. Trust resolves through the `.git`
// file's pointer to the main repository root, so the repository's single entry
// already covers every worktree of it -- measured, not assumed. Adding one here
// would write a redundant entry into the developer's Codex config for every
// worktree that ever existed.
//
// It returns the composed override, or nothing when it was suppressed or the
// never-empty rule produced no document. The `.codex` delivery is deliberately
// not in the returned set: it is a symlink (or, under the fallback, a
// directory), neither of which the managed-file pipeline can hash, and the
// writer reconciles it against the payload on every apply instead.
func installWorktreeCodex(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, worktreePath, group, repo, purpose, branch string, stderr io.Writer) ([]string, error) {
	if stderr == nil {
		stderr = os.Stderr
	}

	verdict, err := DetectCodexConflicts(repo, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("checking Codex paths in worktree %s: %w", worktreePath, err)
	}
	for _, c := range verdict.Conflicts() {
		fmt.Fprintf(stderr, "worktree %s: %s\n", worktreePath, c)
	}

	var written []string

	if !verdict.SuppressesOverride() {
		layer, err := worktreeContextSection(cfg, configDir, instanceRoot, worktreePath, repo, purpose, branch)
		if err != nil {
			return nil, err
		}
		result, err := InstallWorktreeCodexOverride(cfg, configDir, overlayDir, instanceRoot, worktreePath, group, repo, layer)
		if err != nil {
			return nil, fmt.Errorf("composing Codex context for worktree %s: %w", worktreePath, err)
		}
		if result.Refusal != nil {
			// The workspace layers and the framing are in the override; only the
			// checkout's own committed content is missing, and this is the sole
			// signal that anything is.
			fmt.Fprintf(stderr, "worktree %s: %s\n", worktreePath, result.Refusal)
		}
		written = append(written, result.WrittenFiles...)
	}

	if verdict.SuppressesPayload() {
		return written, nil
	}

	// The payload belongs to the instance and is written by `niwa apply`. A
	// worktree apply never creates it, so with no payload there is nothing to
	// deliver and a link planted anyway would dangle at a path that was never
	// this instance's.
	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	if _, statErr := os.Stat(payloadDir); statErr != nil {
		if os.IsNotExist(statErr) {
			return written, nil
		}
		return nil, fmt.Errorf("inspecting Codex payload %s: %w", payloadDir, statErr)
	}

	link, err := InstallRepoCodexLink(instanceRoot, worktreePath)
	if err != nil {
		return nil, fmt.Errorf("linking the Codex payload into worktree %s: %w", worktreePath, err)
	}
	if link.Foreign {
		// Unreachable through the gate above unless something occupied the name
		// during this apply; reported the same way regardless.
		fmt.Fprintf(stderr, "worktree %s: %s is occupied by something niwa did not write; no Codex payload delivered there, and nothing at that path was modified or removed\n",
			worktreePath, filepath.Join(worktreePath, CodexPayloadDirName))
	}

	return written, nil
}
