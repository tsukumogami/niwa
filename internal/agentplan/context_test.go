package agentplan

import (
	"path/filepath"
	"strings"
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

// TestRootContextPlanIsEmptyForAnUndeclaredAgent pins the gate the conversion
// moved out of the writers: an agent whose declaration for the document is
// unavailable gets no entries, and no error either -- the table has already
// answered. The instance-root document is the case that survives, because an
// instance root is not a project root and Codex's walk never starts above one.
func TestRootContextPlanIsEmptyForAnUndeclaredAgent(t *testing.T) {
	plan, err := For(agent.AgentCodex).RootContextPlan(RootContextInputs{
		Dir:     filepath.FromSlash("/ws"),
		Body:    []byte("# workspace\n"),
		HasBody: true,
	})
	if err != nil {
		t.Fatalf("RootContextPlan: %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("root plan declares %d entries for an agent the table says does not receive them", len(plan.Entries))
	}
}

// TestGroupContextPlanIsEmptyForAComposingAgent is the other half, and it is a
// different shape of answer: the capability IS implemented for Codex, and the
// document still is not written, because the group layer reaches the session
// composed into each repository's own document instead. A file in the group
// directory would be bytes nothing ever reads.
func TestGroupContextPlanIsEmptyForAComposingAgent(t *testing.T) {
	in := RootContextInputs{
		Dir:     filepath.FromSlash("/ws/public"),
		Body:    []byte("# public\n"),
		HasBody: true,
	}

	codex, err := For(agent.AgentCodex).GroupContextPlan(in)
	if err != nil {
		t.Fatalf("GroupContextPlan: %v", err)
	}
	if len(codex.Entries) != 0 {
		t.Errorf("group plan declares %d entries for an agent that receives the group layer inside each repository's document", len(codex.Entries))
	}

	claude, err := For(agent.AgentClaude).GroupContextPlan(in)
	if err != nil {
		t.Fatalf("GroupContextPlan: %v", err)
	}
	if len(claude.Entries) != 1 {
		t.Fatalf("group plan declares %d entries for claude, want 1", len(claude.Entries))
	}
	if want := filepath.Join(in.Dir, "CLAUDE.md"); claude.Entries[0].Path != want {
		t.Errorf("group entry path = %q, want %q", claude.Entries[0].Path, want)
	}
}

// TestComposedRepoPlanCarriesTheWholeChain is the composition rule as data. A
// Codex session inside a repository never sees the documents above it, so the
// document at the repository root has to carry the workspace layer, the group
// layer, the repository's own, and the repository's committed file -- in that
// order, outermost first, behind the generation marker.
func TestComposedRepoPlanCarriesTheWholeChain(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	plan, err := For(agent.AgentCodex).RepoContextPlan(RepoContextInputs{
		Dir:        dir,
		Workspace:  []byte("# workspace\n"),
		Group:      []byte("# public\n"),
		Body:       []byte("# app\n"),
		HasBody:    true,
		Overlay:    []byte("## addendum\n"),
		HasOverlay: true,
		Probe: ContextProbe{
			Dir:        dir,
			OwnedPath:  filepath.Join(dir, "AGENTS.override.md"),
			Inlined:    []byte("# committed\n"),
			HasInlined: true,
		},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}

	e := plan.Entries[0]
	if want := filepath.Join(dir, "AGENTS.override.md"); e.Path != want {
		t.Errorf("entry path = %q, want %q", e.Path, want)
	}
	want := generationMarker + "\n\n# workspace\n\n# public\n\n# app\n\n## addendum\n\n# committed\n"
	if string(e.Content) != want {
		t.Errorf("composed document =\n%q\nwant\n%q", e.Content, want)
	}
	if e.Pre != IfNotForeign || e.Owner != generationMarker {
		t.Errorf("entry = {pre %d, owner %q}, want the ownership gate keyed on the generation marker", e.Pre, e.Owner)
	}
	if e.ExcludeAs != "AGENTS.override.md" {
		t.Errorf("entry ExcludeAs = %q, want the repo-relative path; an uncovered name leaves the tree dirty", e.ExcludeAs)
	}
}

// TestComposedRepoPlanWritesNothingWhenNoLayerHasContent is the never-empty
// rule. A file at this name claims the directory's one context slot whatever it
// holds, so an empty one would suppress the repository's own committed file
// entirely and without a word -- and the committed file is not composed alone
// either, since a document that only repeats it says nothing discovery would
// not have delivered anyway.
func TestComposedRepoPlanWritesNothingWhenNoLayerHasContent(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	plan, err := For(agent.AgentCodex).RepoContextPlan(RepoContextInputs{
		Dir: dir,
		Probe: ContextProbe{
			Dir:        dir,
			OwnedPath:  filepath.Join(dir, "AGENTS.override.md"),
			Inlined:    []byte("# committed\n"),
			HasInlined: true,
		},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("plan declares %d entries with no configured layer; a file here would suppress the repository's own", len(plan.Entries))
	}
}

// TestComposedRepoPlanRefusesAForeignPath is the conflict rule in both of its
// halves: nothing is declared at the occupied path, and the path is carried in
// Exempt so the cleanup that runs later does not delete the very file the
// refusal promised to leave alone.
func TestComposedRepoPlanRefusesAForeignPath(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	owned := filepath.Join(dir, "AGENTS.override.md")

	plan, err := For(agent.AgentCodex).RepoContextPlan(RepoContextInputs{
		Dir:       dir,
		Workspace: []byte("# workspace\n"),
		Body:      []byte("# app\n"),
		HasBody:   true,
		Probe:     ContextProbe{Dir: dir, OwnedPath: owned, Foreign: true},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("plan declares %d entries at a path the repository commits its own file at", len(plan.Entries))
	}
	if len(plan.Exempt) != 1 || plan.Exempt[0] != owned {
		t.Errorf("plan Exempt = %v, want [%s]", plan.Exempt, owned)
	}
	if len(plan.Warnings) != 1 {
		t.Errorf("plan declares %d warnings for a refusal; a quiet skip is the silent failure the rule exists to prevent", len(plan.Warnings))
	}
}

// TestComposedRepoPlanReportsAnOverBudgetChain covers the truncation the agent
// performs without a word. The chain is still written whole -- cutting it here
// would reproduce the same silent loss earlier -- and the warning names the
// directory whose session pays for it.
func TestComposedRepoPlanReportsAnOverBudgetChain(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	docs := filepath.Join(dir, "docs")

	half := make([]byte, codexDocBudget/2+1)
	for i := range half {
		half[i] = 'x'
	}

	plan, err := For(agent.AgentCodex).RepoContextPlan(RepoContextInputs{
		Dir:     dir,
		Body:    half,
		HasBody: true,
		Subdirs: []SubdirContext{{Dir: docs, Body: half}},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("plan has %d entries, want 2: an over-budget chain is reported, not trimmed", len(plan.Entries))
	}
	if len(plan.Warnings) != 1 {
		t.Fatalf("plan declares %d warnings, want 1 naming the over-budget chain", len(plan.Warnings))
	}
	if !strings.Contains(plan.Warnings[0], docs) {
		t.Errorf("budget warning names %q, want the deepest directory %q", plan.Warnings[0], docs)
	}
}

// TestComposedWorktreePlanStandsAloneWithoutAPriorDocument is the rule that
// keeps a worktree recognizable on its next apply. When the repository-level
// plan wrote nothing, this section is the whole document and has to carry the
// marker; appending a markerless section to a file nobody wrote would leave niwa
// unable to claim it later and refusing to refresh it forever.
func TestComposedWorktreePlanStandsAloneWithoutAPriorDocument(t *testing.T) {
	dir := filepath.FromSlash("/ws/.niwa/worktrees/app")
	const heading = "## Worktree Context"

	plan, err := For(agent.AgentCodex).WorktreeContextPlan(WorktreeContextInputs{
		Dir:     dir,
		Heading: heading,
		Body:    []byte("- Purpose: ship\n"),
		Probe:   ContextProbe{Dir: dir, OwnedPath: filepath.Join(dir, "AGENTS.override.md")},
	})
	if err != nil {
		t.Fatalf("WorktreeContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	e := plan.Entries[0]
	if e.Op != OpWriteFile {
		t.Errorf("entry op = %d, want a whole-file write: there is no document for a section replace to join", e.Op)
	}
	want := generationMarker + "\n\n" + heading + "\n\n- Purpose: ship\n"
	if string(e.Content) != want {
		t.Errorf("entry content = %q, want %q", e.Content, want)
	}
}

// TestComposedWorktreePlanReplacesItsSectionInAnOwnedDocument is the other
// branch: the repository-level plan already wrote the composed chain, so the
// section joins it and a re-apply rewrites the section in place rather than
// appending a second copy.
func TestComposedWorktreePlanReplacesItsSectionInAnOwnedDocument(t *testing.T) {
	dir := filepath.FromSlash("/ws/.niwa/worktrees/app")
	const heading = "## Worktree Context"

	plan, err := For(agent.AgentCodex).WorktreeContextPlan(WorktreeContextInputs{
		Dir:     dir,
		Heading: heading,
		Body:    []byte("- Purpose: ship\n"),
		Probe:   ContextProbe{Dir: dir, OwnedPath: filepath.Join(dir, "AGENTS.override.md"), Owned: true},
	})
	if err != nil {
		t.Fatalf("WorktreeContextPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	e := plan.Entries[0]
	if e.Op != OpReplaceSection || e.Marker != heading {
		t.Errorf("entry = {op %d, marker %q}, want a section replace delimited by the heading", e.Op, e.Marker)
	}
	if e.Owner != generationMarker {
		t.Errorf("entry owner = %q, want the generation marker: the section's file is still ownership-gated", e.Owner)
	}
}

// TestContextProbeSpecIsEmptyForAnAgentWithoutAnOwnershipRule keeps the probe
// seam from costing anything where it buys nothing. Claude's documents are not
// composed into a tree niwa does not own, so no path is inspected and no
// subprocess runs.
func TestContextProbeSpecIsEmptyForAnAgentWithoutAnOwnershipRule(t *testing.T) {
	if got := For(agent.AgentClaude).ContextProbeSpec(filepath.FromSlash("/ws/public/app")); got != (ContextProbeSpec{}) {
		t.Errorf("claude probe spec = %+v, want the zero spec", got)
	}

	dir := filepath.FromSlash("/ws/public/app")
	got := For(agent.AgentCodex).ContextProbeSpec(dir)
	if got.OwnedPath != filepath.Join(dir, "AGENTS.override.md") {
		t.Errorf("codex probe spec owned path = %q", got.OwnedPath)
	}
	if got.InlinePath != filepath.Join(dir, "AGENTS.md") {
		t.Errorf("codex probe spec inline path = %q, want the committed file the composed document displaces", got.InlinePath)
	}
	if got.OwnerMarker != generationMarker {
		t.Errorf("codex probe spec owner marker = %q", got.OwnerMarker)
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
