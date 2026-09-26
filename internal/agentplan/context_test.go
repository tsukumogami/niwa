package agentplan

import (
	"bytes"
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

// TestRootContextPlanMatchesItsDeclaration is the binding check for row 2, in
// the shape its route calls for.
//
// Rows delivered by a procedure are bound by name and checked in both
// directions by TestBindingsMatchTheirDeclarations; rows consulted by the
// launch path get the same treatment from TestLaunchSpecsMatchTheirDeclarations.
// Neither mechanism reaches a plan route, where the delivery is not a
// registered name but entries a producer declares. The route-appropriate
// question is whether the producer declares any: given a directory with a
// document to write, an agent the table says receives it must get an entry, and
// an agent the table says does not must get none.
//
// Both directions matter and neither is hypothetical. Row 2 stood declared
// unavailable for Codex on a false reason while the producer short-circuited on
// that declaration, so the row and the silence agreed with each other and
// neither was checked against the agent. Deleting the composed branch below
// fails this test rather than quietly restoring that state.
func TestRootContextPlanMatchesItsDeclaration(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := Lookup(RootSessionOrientation, ag)
		if err != nil {
			t.Fatalf("Lookup(root-session-orientation, %q): %v", ag, err)
		}

		plan, err := For(ag).RootContextPlan(RootContextInputs{
			Dir:     filepath.FromSlash("/ws"),
			Body:    []byte("# workspace\n"),
			HasBody: true,
		})
		if err != nil {
			t.Fatalf("RootContextPlan(%q): %v", ag, err)
		}

		switch {
		case d.State == StateImplemented && len(plan.Entries) == 0:
			t.Errorf("(%s, %s) is declared implemented, but its producer declares no entry for a directory with a document to write; the declaration stands behind nothing", RootSessionOrientation, ag)
		case d.State != StateImplemented && len(plan.Entries) > 0:
			t.Errorf("(%s, %s) is not declared implemented, yet its producer declares %d entr(ies); something delivers a capability nobody declared", RootSessionOrientation, ag, len(plan.Entries))
		}
	}
}

// TestRootContextPlanIsEmptyWithNothingToWrite pins the other reason a plan
// comes back empty, so the binding check above cannot be satisfied by a
// producer that writes a file whatever the configuration says. A workspace that
// declares no root content gets no document, for either agent.
func TestRootContextPlanIsEmptyWithNothingToWrite(t *testing.T) {
	for _, ag := range agent.All() {
		plan, err := For(ag).RootContextPlan(RootContextInputs{Dir: filepath.FromSlash("/ws")})
		if err != nil {
			t.Fatalf("RootContextPlan(%q): %v", ag, err)
		}
		if len(plan.Entries) != 0 {
			t.Errorf("root plan for %q declares %d entr(ies) with no body and no imported layer", ag, len(plan.Entries))
		}
	}
}

// TestRootContextPlanFoldsImportedLayersForAComposingAgent is the content half
// of row 2's delivery. Claude reaches the workspace context, the private
// overlay and the global layer through @import lines beside the document;
// Codex has no import mechanism, so a reference it cannot follow is content it
// does not have, and the layers are folded into the document itself.
//
// The Claude half is the one that keeps this honest: the same inputs must leave
// its document byte-identical to the body alone, or this producer has started
// writing one agent's content into the other's file.
func TestRootContextPlanFoldsImportedLayersForAComposingAgent(t *testing.T) {
	dir := filepath.FromSlash("/ws")
	in := RootContextInputs{
		Dir:      dir,
		Body:     []byte("# workspace\n"),
		HasBody:  true,
		Imported: [][]byte{[]byte("# repos\n"), []byte("# overlay\n")},
	}

	codex, err := For(agent.AgentCodex).RootContextPlan(in)
	if err != nil {
		t.Fatalf("RootContextPlan(codex): %v", err)
	}
	if len(codex.Entries) != 1 {
		t.Fatalf("codex root plan has %d entries, want 1", len(codex.Entries))
	}
	got := string(codex.Entries[0].Content)
	for _, want := range []string{"# workspace", "# repos", "# overlay"} {
		if !strings.Contains(got, want) {
			t.Errorf("codex root document does not carry %q:\n%s", want, got)
		}
	}
	if codex.Entries[0].Path != filepath.Join(dir, "AGENTS.md") {
		t.Errorf("codex root document path = %q, want %q", codex.Entries[0].Path, filepath.Join(dir, "AGENTS.md"))
	}

	claude, err := For(agent.AgentClaude).RootContextPlan(in)
	if err != nil {
		t.Fatalf("RootContextPlan(claude): %v", err)
	}
	if len(claude.Entries) != 1 {
		t.Fatalf("claude root plan has %d entries, want 1", len(claude.Entries))
	}
	if string(claude.Entries[0].Content) != string(in.Body) {
		t.Errorf("claude root document = %q, want the body alone %q; the imported layers reach a Claude session where they are written", claude.Entries[0].Content, in.Body)
	}
}

// TestRootContextPlanReportsAnOverBudgetComposition pins the one thing a
// composed root document can silently lose. Codex spends a single byte budget
// across its whole chain and cuts the overflow with nothing in the text and
// nothing on stderr, and a directory niwa owns outright carries no project
// layer to declare a larger budget in -- so if this warning is not produced,
// nothing anywhere tells the developer their orientation stopped arriving.
func TestRootContextPlanReportsAnOverBudgetComposition(t *testing.T) {
	dir := filepath.FromSlash("/ws")
	big := bytes.Repeat([]byte("x"), codexDocBudget+1)

	over, err := For(agent.AgentCodex).RootContextPlan(RootContextInputs{
		Dir: dir, Body: big, HasBody: true,
	})
	if err != nil {
		t.Fatalf("RootContextPlan: %v", err)
	}
	if len(over.Warnings) != 1 {
		t.Fatalf("an over-budget root document produced %d warnings, want 1", len(over.Warnings))
	}
	if !strings.Contains(over.Warnings[0], filepath.Join(dir, "AGENTS.md")) {
		t.Errorf("the warning does not name the document it is about: %q", over.Warnings[0])
	}
	if len(over.Entries) != 1 {
		t.Errorf("the document was not declared alongside its warning; a warning is not a refusal")
	}

	under, err := For(agent.AgentCodex).RootContextPlan(RootContextInputs{
		Dir: dir, Body: []byte("# workspace\n"), HasBody: true,
	})
	if err != nil {
		t.Fatalf("RootContextPlan: %v", err)
	}
	if len(under.Warnings) != 0 {
		t.Errorf("a document inside the budget warned anyway: %q", under.Warnings)
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

// TestComposedRepoPlanMeasuresTheDeepestChain covers the measurement the
// declared budget is sized from. The chain is written whole -- cutting it here
// would reproduce the silent loss the budget exists against -- and what travels
// out is the worst chain a session reads, which is the deepest path through the
// declared documents rather than the sum of all of them.
func TestComposedRepoPlanMeasuresTheDeepestChain(t *testing.T) {
	dir := filepath.FromSlash("/ws/public/app")
	docs := filepath.Join(dir, "docs")
	notes := filepath.Join(dir, "notes")

	half := make([]byte, codexDocBudget/2+1)
	for i := range half {
		half[i] = 'x'
	}

	plan, err := For(agent.AgentCodex).RepoContextPlan(RepoContextInputs{
		Dir:     dir,
		Body:    half,
		HasBody: true,
		Subdirs: []SubdirContext{{Dir: docs, Body: half}, {Dir: notes, Body: half}},
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if len(plan.Entries) != 3 {
		t.Fatalf("plan has %d entries, want 3: an over-budget chain is covered, not trimmed", len(plan.Entries))
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("plan declares %v; niwa raises the budget in the project layer rather than reporting the overflow", plan.Warnings)
	}

	// The two subdirectories are siblings, so no session reads both. The worst
	// chain is the repository document plus one of them.
	root, sub := len(plan.Entries[0].Content), len(plan.Entries[1].Content)
	if plan.ChainBytes != root+sub {
		t.Errorf("plan reports a %d-byte chain, want the %d-byte deepest path rather than every document added up", plan.ChainBytes, root+sub)
	}
	if plan.ChainBytes <= codexDocBudget {
		t.Errorf("the fixture composes %d bytes, which the %d-byte default already covers; it is not exercising the budget", plan.ChainBytes, codexDocBudget)
	}
}

// TestClaudeRepoPlanMeasuresNoChain is the other half: an agent that reads its
// context documents whole spends no shared counter, so its plan reports no
// chain and nothing downstream declares a bound for it.
func TestClaudeRepoPlanMeasuresNoChain(t *testing.T) {
	plan, err := For(agent.AgentClaude).RepoContextPlan(RepoContextInputs{
		Dir:     filepath.FromSlash("/ws/public/app"),
		Body:    []byte("body\n"),
		HasBody: true,
	})
	if err != nil {
		t.Fatalf("RepoContextPlan: %v", err)
	}
	if plan.ChainBytes != 0 {
		t.Errorf("plan reports a %d-byte chain for an agent that spends no shared budget", plan.ChainBytes)
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
