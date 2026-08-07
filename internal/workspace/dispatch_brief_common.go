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

// The target directory is dispatchBriefsDirName, declared in snapshotwriter.go
// beside the preservation that carries this file across a config swap. Sharing
// the constant is deliberate: if the directory ever moves and only one side
// follows, the agreement stops surviving refreshes and nothing says so.

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

	dir := filepath.Join(workspaceRoot, config.ConfigDir, dispatchBriefsDirName)
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
// A start marker with no matching end marker is an ORPHAN, not a block: it is
// left exactly where it is and the fresh block is appended. Guessing at the
// bounds of something we cannot parse is how prose someone wrote gets deleted,
// and appending is recoverable.
//
// The orphan case is why the search is for a well-formed PAIR rather than for
// the first start marker. An orphan followed by a real block is a state this
// function itself produces — append leaves the orphan in place above the block
// it just wrote — so it recurs on the very next materialization, and every
// `niwa apply` and `niwa create` runs one. Pairing the orphan's start with the
// real block's end would open a replacement span across everything between
// them, which is precisely the workspace content the merge exists to protect.
func mergeDispatchBriefCommon(existing, block string) string {
	wrapped := dispatchBriefCommonStartMarker + "\n" + strings.TrimRight(block, "\n") + "\n" + dispatchBriefCommonEndMarker + "\n"

	if existing == "" {
		return wrapped
	}

	if start, end, ok := findDispatchBriefCommonBlock(existing); ok {
		return existing[:start] + wrapped + existing[end:]
	}

	prefix := existing
	if !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	return prefix + "\n" + wrapped
}

// findDispatchBriefCommonBlock locates the first well-formed niwa block in s and
// returns its half-open byte range.
//
// Well-formed means the end marker follows the start marker with no second
// start marker in between. That bound is what makes an orphaned start marker
// inert: the orphan's own end-marker search runs past the next start marker, so
// the orphan is skipped and the real block below it is found instead. Without
// the bound, an orphan silently extends the span over everything down to the
// real block's end marker.
func findDispatchBriefCommonBlock(s string) (start, end int, ok bool) {
	offset := 0
	for {
		rel := strings.Index(s[offset:], dispatchBriefCommonStartMarker)
		if rel < 0 {
			return 0, 0, false
		}
		start = offset + rel
		bodyAt := start + len(dispatchBriefCommonStartMarker)

		endRel := strings.Index(s[bodyAt:], dispatchBriefCommonEndMarker)
		if endRel < 0 {
			// No end marker anywhere after this start: nothing below is
			// well-formed either, so stop rather than scanning further.
			return 0, 0, false
		}

		nextStartRel := strings.Index(s[bodyAt:], dispatchBriefCommonStartMarker)
		if nextStartRel >= 0 && nextStartRel < endRel {
			// This start is an orphan — another block opens before this one
			// closes. Skip it and try the next start marker.
			offset = bodyAt + nextStartRel
			continue
		}

		end = bodyAt + endRel + len(dispatchBriefCommonEndMarker)
		// Absorb the newline that terminated the end marker's line so a re-run
		// reproduces byte-identical output.
		if end < len(s) && s[end] == '\n' {
			end++
		}
		return start, end, true
	}
}
