package agentplan

import (
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// These cover the producers as data: what a plan says, not what lands on disk.
// The rules they pin are the ones the writers accumulated before the conversion
// -- the overlay append, the nested subdirectory documents, and the legacy
// import removal that must not create the file it tidies -- so a later edit that
// drops one fails here with a name rather than as a hash in a manifest.

func TestRepoContextPlanFoldsTheOverlayIntoTheBase(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	plan, err := For(agent.AgentClaude).RepoContextPlan(RepoContextInputs{
		Dir:        dir,
		Body:       []byte("# app\n"),
		HasBody:    true,
		Overlay:    []byte("## addendum\n"),
		HasOverlay: true,
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1: the overlay belongs in the base document's entry", len(plan.Entries))
	}
	e := plan.Entries[0]
	if want := filepath.Join(dir, "CLAUDE.local.md"); e.Path != want {
		t.Errorf("entry path = %q, want %q", e.Path, want)
	}
	if want := "# app\n\n## addendum\n"; string(e.Content) != want {
		t.Errorf("entry content = %q, want %q (base, blank line, overlay)", e.Content, want)
	}
	if e.Op != OpWriteFile || e.Mode != contextFileMode || !e.Managed {
		t.Errorf("entry = {op %d, mode %v, managed %v}, want a managed 0o644 whole-file write", e.Op, e.Mode, e.Managed)
	}
	if e.Capability != RepoOrientationDoc {
		t.Errorf("entry capability = %s, want %s", e.Capability, RepoOrientationDoc)
	}
}

func TestRepoContextPlanWritesAnOverlayWithNoBase(t *testing.T) {
	plan, err := For(agent.AgentClaude).RepoContextPlan(RepoContextInputs{
		Dir:        filepath.FromSlash("/ws/public/app"),
		Overlay:    []byte("## addendum\n"),
		HasOverlay: true,
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	if got := string(plan.Entries[0].Content); got != "## addendum\n" {
		t.Errorf("entry content = %q, want the overlay verbatim", got)
	}
}

func TestRepoContextPlanDeclaresEachSubdirDocument(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	docs := filepath.Join(dir, "docs")
	deep := filepath.Join(docs, "deep")

	plan, err := For(agent.AgentClaude).RepoContextPlan(RepoContextInputs{
		Dir:     dir,
		Body:    []byte("# app\n"),
		HasBody: true,
		Subdirs: []SubdirContext{
			{Dir: docs, Body: []byte("# docs\n")},
			{Dir: deep, Body: []byte("# deep\n")},
		},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	want := []string{
		filepath.Join(dir, "CLAUDE.local.md"),
		filepath.Join(docs, "CLAUDE.local.md"),
		filepath.Join(deep, "CLAUDE.local.md"),
	}
	if len(plan.Entries) != len(want) {
		t.Fatalf("plan has %d entries, want %d", len(plan.Entries), len(want))
	}
	for i, path := range want {
		if plan.Entries[i].Path != path {
			t.Errorf("entry %d path = %q, want %q", i, plan.Entries[i].Path, path)
		}
	}
}

// TestRepoAndWorktreeContextPlansAreEmptyForAnUndeclaredAgent pins the gate the
// conversion moved out of the writers: an agent whose declaration for the
// document is unavailable gets no entries, and no error either -- the table has
// already answered.
func TestRepoAndWorktreeContextPlansAreEmptyForAnUndeclaredAgent(t *testing.T) {
	p := For(agent.AgentCodex)

	repo, err := p.RepoContextPlan(RepoContextInputs{
		Dir:     filepath.FromSlash("/ws/public/app"),
		Body:    []byte("# app\n"),
		HasBody: true,
		Subdirs: []SubdirContext{{Dir: filepath.FromSlash("/ws/public/app/docs"), Body: []byte("# docs\n")}},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(repo.Entries) != 0 {
		t.Errorf("repo plan declares %d entries for an agent the table says does not receive them", len(repo.Entries))
	}

	worktree, err := p.WorktreeContextPlan(WorktreeContextInputs{
		Dir:     filepath.FromSlash("/ws/.niwa/worktrees/app"),
		Heading: "## Worktree",
		Body:    []byte("purpose\n"),
	})
	if err != nil {
		t.Fatalf("WorktreeContextPlan: %v", err)
	}
	if len(worktree.Entries) != 0 {
		t.Errorf("worktree plan declares %d entries for an agent the table says does not receive them", len(worktree.Entries))
	}
}

func TestWorktreeContextPlanReplacesTheSectionItOwns(t *testing.T) {
	dir := filepath.FromSlash("/ws/.niwa/worktrees/app")
	const heading = "## Worktree Context"

	plan, err := For(agent.AgentClaude).WorktreeContextPlan(WorktreeContextInputs{
		Dir:     dir,
		Heading: heading,
		Body:    []byte("- Purpose: ship\n"),
	})
	if err != nil {
		t.Fatalf("WorktreeContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	e := plan.Entries[0]
	if e.Op != OpReplaceSection {
		t.Errorf("entry op = %d, want a section replace so a re-apply does not append a second copy", e.Op)
	}
	if e.Marker != heading {
		t.Errorf("entry marker = %q, want the heading %q", e.Marker, heading)
	}
	if want := heading + "\n\n- Purpose: ship\n"; string(e.Content) != want {
		t.Errorf("entry content = %q, want %q", e.Content, want)
	}
	if want := filepath.Join(dir, "CLAUDE.local.md"); e.Path != want {
		t.Errorf("entry path = %q, want %q", e.Path, want)
	}
}

func TestLegacyImportPlanRemovesTheImportAndItsBlankLine(t *testing.T) {
	plan := LegacyImportPlan(LegacyImportInputs{
		Dir:      filepath.FromSlash("/ws"),
		Existing: []byte("# Workspace\n\n@workspace-context.md\n\nRest\n"),
		Exists:   true,
		Import:   "@workspace-context.md",
	})
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	if want := "# Workspace\n\nRest\n"; string(plan.Entries[0].Content) != want {
		t.Errorf("content = %q, want %q", plan.Entries[0].Content, want)
	}
	if want := filepath.Join(filepath.FromSlash("/ws"), "CLAUDE.md"); plan.Entries[0].Path != want {
		t.Errorf("path = %q, want the legacy root context document %q", plan.Entries[0].Path, want)
	}
}

// TestLegacyImportPlanDeclaresNothingWithoutAnImportToRemove is the one that
// matters for the managed-file record: a cleanup that wrote unconditionally
// would create a document nobody asked for, on every apply, for every agent.
func TestLegacyImportPlanDeclaresNothingWithoutAnImportToRemove(t *testing.T) {
	absent := LegacyImportPlan(LegacyImportInputs{
		Dir:    filepath.FromSlash("/ws"),
		Import: "@workspace-context.md",
	})
	if len(absent.Entries) != 0 {
		t.Errorf("a missing document produced %d entries; the cleanup must not create the file it tidies", len(absent.Entries))
	}

	clean := LegacyImportPlan(LegacyImportInputs{
		Dir:      filepath.FromSlash("/ws"),
		Existing: []byte("# Workspace\n"),
		Exists:   true,
		Import:   "@workspace-context.md",
	})
	if len(clean.Entries) != 0 {
		t.Errorf("a document with no legacy import produced %d entries; nothing would change", len(clean.Entries))
	}
}
