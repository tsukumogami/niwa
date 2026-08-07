package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// commonPath is where writeDispatchBriefCommon lands under a workspace root.
func commonPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, config.ConfigDir, dispatchBriefsDirName, dispatchBriefCommonFile)
}

func readCommon(t *testing.T, workspaceRoot string) string {
	t.Helper()
	data, err := os.ReadFile(commonPath(workspaceRoot))
	if err != nil {
		t.Fatalf("reading agreement: %v", err)
	}
	return string(data)
}

// TestWriteDispatchBriefCommon_CreatesWhenAbsent covers the fresh-workspace
// case: no directory, no file, and the agreement has to appear anyway. This is
// the path that makes the shipped agreement exist at all.
func TestWriteDispatchBriefCommon_CreatesWhenAbsent(t *testing.T) {
	root := t.TempDir()

	path, err := writeDispatchBriefCommon(root)
	if err != nil {
		t.Fatalf("writeDispatchBriefCommon: %v", err)
	}
	if want := commonPath(root); path != want {
		t.Errorf("returned path = %q, want %q", path, want)
	}

	got := readCommon(t, root)
	if !strings.Contains(got, dispatchBriefCommonStartMarker) || !strings.Contains(got, dispatchBriefCommonEndMarker) {
		t.Fatalf("agreement is not sentinel-delimited:\n%s", got)
	}
	if !strings.Contains(got, "Common working agreement for dispatched workers") {
		t.Error("agreement does not carry the embedded content")
	}
}

// TestWriteDispatchBriefCommon_AppendsToExistingWithoutBlock covers a workspace
// that hand-wrote its own agreement before niwa shipped one. Every pre-existing
// byte must survive: this file accumulates workspace-specific rules over time,
// and workspace-root writes are untracked, so losing them would be silent.
func TestWriteDispatchBriefCommon_AppendsToExistingWithoutBlock(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Dir(commonPath(root))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "# Our own agreement\n\nDo not delete this line.\n"
	if err := os.WriteFile(commonPath(root), []byte(existing), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if _, err := writeDispatchBriefCommon(root); err != nil {
		t.Fatalf("writeDispatchBriefCommon: %v", err)
	}

	got := readCommon(t, root)
	if !strings.HasPrefix(got, existing) {
		t.Errorf("pre-existing content was not preserved verbatim at the head:\n%s", got)
	}
	if !strings.Contains(got, dispatchBriefCommonStartMarker) {
		t.Error("niwa block was not appended")
	}
}

// TestWriteDispatchBriefCommon_ReplacesBlockPreservingBothSides is the
// load-bearing case. A workspace may well want its own sections AFTER niwa's,
// which is why the block carries an explicit end marker instead of truncating
// to end-of-file the way the worktree-context layer does. Both surrounding
// regions must come out byte-identical.
func TestWriteDispatchBriefCommon_ReplacesBlockPreservingBothSides(t *testing.T) {
	root := t.TempDir()

	if _, err := writeDispatchBriefCommon(root); err != nil {
		t.Fatalf("seeding write: %v", err)
	}

	const (
		before = "# House rules\n\nAlways rebase.\n\n"
		after  = "\n## Language-specific\n\nRun the formatter.\n"
	)
	seeded := readCommon(t, root)
	// Plant a stale block so the replacement is observable, with workspace
	// content on both sides of it.
	stale := strings.Replace(seeded, "Read your brief first.", "STALE MARKER TEXT", 1)
	if stale == seeded {
		t.Fatal("failed to construct a stale block")
	}
	if err := os.WriteFile(commonPath(root), []byte(before+stale+after), 0o644); err != nil {
		t.Fatalf("seeding surrounded file: %v", err)
	}

	if _, err := writeDispatchBriefCommon(root); err != nil {
		t.Fatalf("writeDispatchBriefCommon: %v", err)
	}

	got := readCommon(t, root)
	if !strings.HasPrefix(got, before) {
		t.Errorf("content before the block was not preserved:\n%q", got)
	}
	if !strings.HasSuffix(got, after) {
		t.Errorf("content after the block was not preserved:\n%q", got)
	}
	if strings.Contains(got, "STALE MARKER TEXT") {
		t.Error("stale niwa block survived; the block was not replaced")
	}
	if !strings.Contains(got, "Read your brief first.") {
		t.Error("fresh niwa block content is missing")
	}
	if n := strings.Count(got, dispatchBriefCommonStartMarker); n != 1 {
		t.Errorf("start marker count = %d, want 1", n)
	}
}

// TestWriteDispatchBriefCommon_IsIdempotent guards the failure a plain append
// would cause: every apply stacking another copy of the agreement onto the
// file until it is unreadable.
func TestWriteDispatchBriefCommon_IsIdempotent(t *testing.T) {
	root := t.TempDir()

	if _, err := writeDispatchBriefCommon(root); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := readCommon(t, root)

	if _, err := writeDispatchBriefCommon(root); err != nil {
		t.Fatalf("second write: %v", err)
	}
	second := readCommon(t, root)

	if first != second {
		t.Errorf("re-run was not byte-identical:\nfirst:\n%q\n\nsecond:\n%q", first, second)
	}
	if n := strings.Count(second, dispatchBriefCommonStartMarker); n != 1 {
		t.Errorf("start marker count after re-run = %d, want 1", n)
	}
}

// TestMergeDispatchBriefCommon_MalformedSentinelAppends covers a truncated
// block — start marker present, end marker gone. Guessing at the bounds could
// eat arbitrary workspace prose; appending is recoverable, so that is the
// deliberate choice.
//
// The second call is the load-bearing half. Append leaves an orphaned start
// marker sitting above the block it just wrote, so the NEXT materialization
// sees orphan-then-block — and every `niwa apply` and `niwa create` is a next
// materialization. A merge that pairs the orphan's start with the real block's
// end deletes everything between them, which is exactly the workspace prose
// this function exists to protect.
func TestMergeDispatchBriefCommon_MalformedSentinelAppends(t *testing.T) {
	const ownSection = "## Our own testing constraints\nNever run the suite against prod.\n"
	existing := "# House rules\n\n" +
		dispatchBriefCommonStartMarker + "\nhalf a block, no end marker\n\n" +
		ownSection

	first := mergeDispatchBriefCommon(existing, "AGREEMENT V1")

	if !strings.HasPrefix(first, existing) {
		t.Errorf("run 1 modified pre-existing content:\n%q", first)
	}
	if !strings.Contains(first, "half a block, no end marker") {
		t.Error("run 1 destroyed the truncated block's content")
	}
	if !strings.Contains(first, "AGREEMENT V1") {
		t.Error("run 1 did not append the fresh block")
	}

	second := mergeDispatchBriefCommon(first, "AGREEMENT V2")

	if !strings.Contains(second, ownSection) {
		t.Errorf("run 2 destroyed the workspace's own section:\n%q", second)
	}
	if !strings.Contains(second, "half a block, no end marker") {
		t.Errorf("run 2 destroyed the orphaned block's content:\n%q", second)
	}
	if !strings.HasPrefix(second, "# House rules") {
		t.Errorf("run 2 destroyed the file's head:\n%q", second)
	}
	if !strings.Contains(second, "AGREEMENT V2") {
		t.Error("run 2 did not write the fresh block")
	}
	if strings.Contains(second, "AGREEMENT V1") {
		t.Errorf("run 2 left the stale block behind instead of replacing it:\n%q", second)
	}

	third := mergeDispatchBriefCommon(second, "AGREEMENT V2")
	if third != second {
		t.Errorf("run 3 was not byte-identical to run 2:\nrun2:\n%q\n\nrun3:\n%q", second, third)
	}
}

// TestMergeDispatchBriefCommon_OrphanBelowTheBlock is the mirror of the case
// above: a stray start marker AFTER a well-formed block. The block must still be
// the one replaced, and the stray marker must survive untouched.
func TestMergeDispatchBriefCommon_OrphanBelowTheBlock(t *testing.T) {
	existing := mergeDispatchBriefCommon("", "AGREEMENT V1") +
		"\n## Later section\n" + dispatchBriefCommonStartMarker + "\npasted by hand\n"

	got := mergeDispatchBriefCommon(existing, "AGREEMENT V2")

	if !strings.Contains(got, "## Later section") {
		t.Errorf("workspace section below the block was destroyed:\n%q", got)
	}
	if !strings.Contains(got, "pasted by hand") {
		t.Errorf("stray marker's content was destroyed:\n%q", got)
	}
	if !strings.Contains(got, "AGREEMENT V2") {
		t.Error("fresh block was not written")
	}
	if strings.Contains(got, "AGREEMENT V1") {
		t.Errorf("stale block survived:\n%q", got)
	}
}

// TestWriteRootSkills_InstallsNestedSkillFiles pins the whole-directory
// behavior: a root skill can ship references alongside its manifest. Without
// it, a skill that needs more than one file has to push its detail into this
// repo's docs/, which a workspace that merely uses niwa does not have on disk.
func TestWriteRootSkills_InstallsNestedSkillFiles(t *testing.T) {
	root := t.TempDir()

	written, err := writeRootSkills(root)
	if err != nil {
		t.Fatalf("writeRootSkills: %v", err)
	}

	skillsRoot := filepath.Join(root, rootClaudeDir, rootSkillsTargetDir)

	// The dispatch skill is the pre-existing single-file case and must be
	// unchanged.
	if _, err := os.Stat(filepath.Join(skillsRoot, "dispatch", rootSkillFileName)); err != nil {
		t.Errorf("dispatch SKILL.md not installed: %v", err)
	}

	// The fleet skill ships references; those must land with their relative
	// path preserved.
	fleetRef := filepath.Join(skillsRoot, "fleet", "references", "review-standard.md")
	if _, err := os.Stat(fleetRef); err != nil {
		t.Errorf("nested skill reference not installed at %s: %v", fleetRef, err)
	}

	var sawNested bool
	for _, p := range written {
		if p == fleetRef {
			sawNested = true
		}
	}
	if !sawNested {
		t.Error("nested skill file is missing from the returned written paths")
	}
}
