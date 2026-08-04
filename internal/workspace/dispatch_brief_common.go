package workspace

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// dispatchBriefsFS embeds the canonical standing agreement niwa ships into
// every workspace's dispatch-briefs directory.
//
// The agreement has a different reader from every other file the root
// materializer writes: the skills under .claude/skills/ are read by the
// coordinator that dispatches work, while this file is read by the dispatched
// WORKER, as the first tool call of its task, because every brief points at it.
// Collapsing the two would lose a load point — the coordinator never reads the
// agreement and the worker never reads the dispatch skill.
//
// The all: prefix is load-bearing: the agreement's filename starts with an
// underscore (it sorts first in the briefs directory, which is why it was named
// that way), and a bare embed pattern skips files beginning with _ or . entirely.
//
//go:embed all:dispatchbriefs
var dispatchBriefsFS embed.FS

// dispatchBriefsEmbedPath is the embedded agreement source.
const dispatchBriefsEmbedPath = "dispatchbriefs/_common.md"

// dispatchBriefsTargetDir is the directory under the workspace config dir where
// the /dispatch skill writes task briefs and where the shared agreement lives.
// It is preserved across config-snapshot swaps (see preserveDispatchBriefs), so
// a file written here survives every later refresh.
const dispatchBriefsTargetDir = "dispatch-briefs"

// dispatchBriefCommonFile is the shared agreement every brief references.
const dispatchBriefCommonFile = "_common.md"

// The sentinel bounds the block niwa owns. Everything outside it belongs to the
// workspace and is never touched.
//
// This differs deliberately from the worktree-context layer
// (stripWorktreeContextSection), which truncates at its heading and therefore
// owns the file's tail. A workspace will reasonably want its own sections AFTER
// niwa's — repo-specific testing constraints, house conventions — so this block
// needs an explicit end marker rather than an implicit "to end of file".
const (
	dispatchBriefCommonStartMarker = "<!-- niwa:dispatch-brief-common:start -->"
	dispatchBriefCommonEndMarker   = "<!-- niwa:dispatch-brief-common:end -->"
)

// writeDispatchBriefCommon merges niwa's canonical standing agreement into
// <workspaceRoot>/.niwa/dispatch-briefs/_common.md, preserving any content the
// workspace wrote outside niwa's sentinel block.
//
// Merge rather than write-if-absent: write-if-absent would freeze every
// workspace at whatever version of the agreement it first saw, so niwa could
// never ship a correction. That matters here more than it looks — the recipe
// this agreement's session-control guidance replaces was itself published and
// wrong.
//
// Merge rather than plain overwrite: the agreement accumulates workspace-
// specific rules over time (that is how it earns its keep), and workspace-root
// writes are untracked, so a plain overwrite would destroy those silently with
// no drift warning to catch it.
//
// Returns the written path.
func writeDispatchBriefCommon(workspaceRoot string) (string, error) {
	block, err := dispatchBriefsFS.ReadFile(dispatchBriefsEmbedPath)
	if err != nil {
		return "", fmt.Errorf("reading embedded dispatch-brief agreement: %w", err)
	}

	dir := filepath.Join(workspaceRoot, config.ConfigDir, dispatchBriefsTargetDir)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return "", fmt.Errorf("creating dispatch-briefs directory %q: %w", dir, mkErr)
	}
	path := filepath.Join(dir, dispatchBriefCommonFile)

	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("reading %q: %w", path, readErr)
	}

	merged := mergeDispatchBriefCommon(string(existing), string(block))
	if wErr := os.WriteFile(path, []byte(merged), 0o644); wErr != nil {
		return "", fmt.Errorf("writing %q: %w", path, wErr)
	}
	return path, nil
}

// mergeDispatchBriefCommon replaces niwa's sentinel-delimited block in existing
// with block, or appends it when no well-formed block is present.
//
// A start marker with no matching end marker is treated as "no niwa block":
// appending is recoverable and guessing at the block's bounds is not, and the
// failure mode being guarded against is destroying prose someone wrote.
func mergeDispatchBriefCommon(existing, block string) string {
	wrapped := dispatchBriefCommonStartMarker + "\n" + strings.TrimRight(block, "\n") + "\n" + dispatchBriefCommonEndMarker + "\n"

	if existing == "" {
		return wrapped
	}

	start := strings.Index(existing, dispatchBriefCommonStartMarker)
	if start >= 0 {
		rest := existing[start+len(dispatchBriefCommonStartMarker):]
		if endRel := strings.Index(rest, dispatchBriefCommonEndMarker); endRel >= 0 {
			end := start + len(dispatchBriefCommonStartMarker) + endRel + len(dispatchBriefCommonEndMarker)
			// Absorb the newline that terminated the end marker's line so a
			// re-run reproduces byte-identical output.
			if end < len(existing) && existing[end] == '\n' {
				end++
			}
			return existing[:start] + wrapped + existing[end:]
		}
	}

	prefix := existing
	if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + "\n" + wrapped
}
