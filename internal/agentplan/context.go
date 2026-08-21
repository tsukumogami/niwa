package agentplan

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the producer side of the orientation documents: the context
// files niwa writes into an instance, a repository, and a worktree. It is the
// only place the name of such a file is decided, which is what lets
// internal/workspace render the content without knowing which agent will read
// it -- the caller resolves sources, expands template variables, and checks
// containment, then hands the rendered bytes here and gets back the writes.
//
// Every producer follows one shape: fill an input struct from the effective
// configuration, ask the declaration table whether this agent gets the
// capability, and return the entries for the executor. A capability the table
// does not declare implemented produces an empty plan -- not a partial one and
// not an error, because "this agent does not receive repository-level context"
// is an answer the table already gives.

// contextFileMode is the permission every context document is written with. It
// is ordinary content: no resolved secret material reaches these files.
const contextFileMode = 0o644

// Producer declares plans for one agent. It is the handle internal/workspace
// holds instead of the agent itself: a writer that has one can obtain the
// entries for the document it is installing, and can do nothing else with the
// agent -- it cannot branch on it, and it cannot name the file.
//
// Constructing one is the single point where an agent value crosses into plan
// production, which is why For is the only way to make one.
type Producer struct {
	ag agent.Agent

	// gateClosed is this agent's own enabled gate, turned off. It is a field
	// on the producer rather than a check at each call site because that is
	// what makes the gate structurally per-agent: a producer carries one
	// agent, so a gate it carries cannot reach any other agent's plan, and the
	// executor downstream sees only entries that already survived it.
	gateClosed bool
}

// For returns the producer that declares plans for ag. The zero Agent resolves
// to Claude, matching internal/agent's fail-safe contract. The producer it
// returns is ungated; a caller that has a scope's gate applies it with Gated.
func For(ag agent.Agent) Producer { return Producer{ag: ag} }

// Gated returns a copy of p whose plans are empty when enabled is false.
//
// This is where [claude] enabled and [codex] enabled land. Each one is read
// for its own agent and handed to that agent's producer, so a gate filters the
// plan it was set on and no other -- a repository with Claude disabled still
// receives its full Codex delivery, and the reverse. The previous shape read
// one agent's key in front of a loop over every agent, which is the same
// boolean deciding two agents' deliveries; no spelling of the key would have
// fixed that.
//
// The gate deliberately does not reach the reconciliation specs' removals:
// turning a gate off is a request to stop delivering, not a request to delete
// what an earlier apply delivered. What niwa tracks in the managed-file record
// is still cleaned up by the record, exactly as it is for any other path a
// current apply stops producing.
func (p Producer) Gated(enabled bool) Producer {
	p.gateClosed = !enabled
	return p
}

// delivers reports whether the declaration table says this agent receives c and
// this agent's gate is open. The lookup is fail-closed: a pair the table cannot
// answer for is an error rather than a silent "no".
func (p Producer) delivers(c Capability) (bool, error) {
	d, err := Lookup(c, p.ag)
	if err != nil {
		return false, err
	}
	if p.gateClosed {
		return false, nil
	}
	return d.State == StateImplemented, nil
}

// localContextPath is the repository- and worktree-level context document
// inside dir. It is the one call to the agent's filename accessor that the
// repository-level producers share, so the two cannot disagree about where a
// document belongs.
func (p Producer) localContextPath(dir string) string {
	return filepath.Join(dir, p.ag.LocalContextFileName())
}

// rootContextPath is the context document for a directory niwa owns outright:
// the instance root and each group directory. Those are not repositories, so
// the name is the agent's plain one rather than the one that outranks a
// repository's own committed file.
func (p Producer) rootContextPath(dir string) string {
	return filepath.Join(dir, p.ag.RootContextFileName())
}

// RootContextInputs is one context document for a directory niwa owns: where it
// goes and what it says. HasBody separates "the configuration declares no
// source" from "the source rendered to nothing", exactly as RepoContextInputs
// does.
type RootContextInputs struct {
	Dir     string
	Body    []byte
	HasBody bool

	// Imported are the further documents a session in Dir reads only because
	// the agent follows a reference out of the main one -- the generated
	// workspace context, the private overlay's addendum, the global layer -- in
	// the order the references establish. The caller renders them from the same
	// sources the files beside the document are written from.
	//
	// An agent with an import mechanism ignores them here and reads them where
	// they are written. An agent without one gets them folded into the document
	// itself, because a reference it cannot follow is content it does not have.
	// Which it is, is the composition table's answer.
	Imported [][]byte
}

// RootContextPlan declares the context document for a directory niwa owns
// outright: an instance root, or the workspace root above it. It is the document
// a session started there reads.
//
// Every agent niwa prepares for reads it. An agent that walks up from its
// working directory finds it above; an agent that fixes a project root first and
// reads downward finds it because the working directory is always the last
// directory of that walk, whether or not a marker was found above it -- measured
// against codex-cli 0.147.0 both ways, with a negative control that puts the
// document one directory up and sees it not arrive.
//
// For an agent that composes its outer layers, the imported documents are folded
// in and the composition is measured: a chain past the default budget is cut
// with nothing in the text and nothing on stderr, and there is no project-layer
// configuration at a directory like this one to declare a larger budget in, so
// the overflow is reported here or nowhere.
func (p Producer) RootContextPlan(in RootContextInputs) (*Plan, error) {
	ok, err := p.delivers(RootSessionOrientation)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &Plan{}, nil
	}

	comp := p.composition()
	if !comp.composesOuterLayers {
		if !in.HasBody {
			return &Plan{}, nil
		}
		return &Plan{Entries: []Entry{p.rootContextEntry(RootSessionOrientation, in.Dir, in.Body)}}, nil
	}

	var layers [][]byte
	if in.HasBody {
		layers = append(layers, in.Body)
	}
	layers = append(layers, in.Imported...)

	doc := joinLayers(layers...)
	if doc == nil {
		return &Plan{}, nil
	}

	plan := &Plan{Entries: []Entry{p.rootContextEntry(RootSessionOrientation, in.Dir, doc)}}
	// The measure is this document alone, and where it is inexact it under-
	// reports rather than over-reports. niwa writes a root document at the
	// workspace root as well as at each instance root under it, and the two are
	// in the same chain whenever a marker-bearing ancestor sits above the
	// workspace root -- the walk starts there, descends through both, and spends
	// one budget across them. Neither check sees the sum.
	//
	// It is left inexact deliberately. The two documents are produced by
	// separate materializers reached from separate commands, so making either
	// check see both means threading one's size into the other's inputs, which
	// couples them for a bound that the workspace-root document -- generated,
	// fixed, and around a kilobyte -- moves by a rounding error. The condition
	// to revisit this is the workspace-root document becoming configurable.
	if comp.measuresChain && len(doc) > codexDocBudget {
		plan.Warnings = append(plan.Warnings, rootBudgetWarning(p.rootContextPath(in.Dir), len(doc)))
	}
	return plan, nil
}

// rootBudgetWarning is the line reported for a root document composed past the
// default context budget.
//
// It names the size rather than advising a fix, because the fix is not niwa's to
// apply here: the budget is a project-layer key, the project layer is a
// directory niwa writes into repositories, and a directory niwa owns outright
// has none. Saying nothing would leave a developer with a document whose tail
// silently stopped arriving and no way to learn it from the tool that wrote it.
func rootBudgetWarning(path string, size int) string {
	return fmt.Sprintf(
		"%s composes to %d bytes, past the %d-byte default a Codex session spends on its whole context chain; the overflow is cut with nothing in the text and nothing on stderr, and niwa declares no larger budget at a directory it owns outright. Shorten the workspace-level context, or raise project_doc_max_bytes for this directory in your own Codex configuration.",
		path, size, codexDocBudget,
	)
}

// GroupContextPlan declares a group directory's context document.
//
// It is empty for an agent that composes the outer layers into each
// repository's own document, and that is the delivery rather than the absence of
// one: the group layer still reaches the session, at the only placement the
// session's discovery visits. Writing a group document for such an agent would
// put bytes on disk that nothing ever reads.
func (p Producer) GroupContextPlan(in RootContextInputs) (*Plan, error) {
	ok, err := p.delivers(WorkspaceOrientation)
	if err != nil {
		return nil, err
	}
	if !ok || !in.HasBody || p.composition().composesOuterLayers {
		return &Plan{}, nil
	}
	return &Plan{Entries: []Entry{p.rootContextEntry(WorkspaceOrientation, in.Dir, in.Body)}}, nil
}

// rootContextEntry is one context document written whole into a directory niwa
// owns. Like contextEntry it is Managed: the document is niwa's to keep current
// and to remove when it stops being declared.
func (p Producer) rootContextEntry(c Capability, dir string, body []byte) Entry {
	return Entry{
		Capability: c,
		Op:         OpWriteFile,
		Path:       p.rootContextPath(dir),
		Content:    body,
		Mode:       contextFileMode,
		Managed:    true,
	}
}

// SubdirContext is one context document that belongs in a directory nested
// inside the repository, declared by [content.repos.<name>.subdirs]. Dir is
// absolute and has already been containment-checked by the caller against the
// repository it belongs to; Body is the rendered document.
//
// Probe is what the caller found at ContextProbeSpec(Dir), zero for an agent
// whose documents are not under an ownership rule.
type SubdirContext struct {
	Dir   string
	Body  []byte
	Probe ContextProbe
}

// RepoContextInputs is what a repository-level context install needs from the
// configuration, with everything agent-shaped removed: the directory the
// documents belong in, the rendered bodies, and nothing about filenames.
//
// The two Has flags separate "the configuration declares no source" from "the
// source rendered to nothing", which the writer this replaces distinguished by
// testing the source path for emptiness. A source that renders empty still
// produces a file.
type RepoContextInputs struct {
	// Dir is the absolute directory the repository-level document belongs in:
	// the repo checkout on the instance apply path, the worktree root when the
	// same install is targeted at a worktree.
	Dir string

	// Body is the rendered base document, valid only when HasBody is set.
	Body    []byte
	HasBody bool

	// Overlay is the private overlay's addendum, valid only when HasOverlay is
	// set. When a base document is present too, the overlay is appended to it
	// behind a blank line; when it is not, the overlay is the whole document.
	Overlay    []byte
	HasOverlay bool

	// Subdirs are the nested documents, in the order the caller resolved them.
	Subdirs []SubdirContext

	// Workspace and Group are the outer orientation layers, rendered by the
	// caller from the same sources the instance-root and group documents are
	// built from. An agent whose discovery reaches those documents where they
	// are written ignores them here; an agent whose walk stops at the repository
	// root gets them folded into the repository's own document, because that is
	// the only placement its sessions see.
	Workspace []byte
	Group     []byte

	// Probe is what the caller found at ContextProbeSpec(Dir), zero for an agent
	// whose documents are not under an ownership rule.
	Probe ContextProbe
}

// RepoContextPlan declares the repository-level context documents: the
// repository's own, with the overlay addendum folded in, plus one per declared
// subdirectory.
//
// The overlay is folded into the same entry rather than declared as a second
// write of the same path. The writer this replaces wrote the base file and then
// rewrote it with base + "\n" + overlay, so the bytes are identical and the
// path is still produced once, which is what the caller records.
//
// For an agent whose documents are composed and owned, the repository's document
// also carries the workspace and group layers and the repository's own committed
// context file, in that order -- and the whole thing is subject to the
// never-empty rule and the ownership rule. That variation is the composition
// table's, not this function's shape: the entries it produces are the same kind
// of entry either way.
func (p Producer) RepoContextPlan(in RepoContextInputs) (*Plan, error) {
	ok, err := p.delivers(RepoOrientationDoc)
	if err != nil {
		return nil, err
	}
	if !ok {
		// Nothing to write, and nothing to warn about: an agent that does not
		// receive repository-level context is a declared state, not a failure.
		return &Plan{}, nil
	}

	comp := p.composition()
	if comp.owned {
		return p.composedRepoContextPlan(in, comp), nil
	}

	plan := &Plan{}

	switch {
	case in.HasBody:
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, in.Dir, repoBody(in)))
	case in.HasOverlay:
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, in.Dir, in.Overlay))
	}

	for _, sub := range in.Subdirs {
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, sub.Dir, sub.Body))
	}

	return plan, nil
}

// repoBody folds the overlay addendum into the base document behind a blank
// line, matching what the writer this producer replaces did.
func repoBody(in RepoContextInputs) []byte {
	switch {
	case in.HasBody && in.HasOverlay:
		return []byte(string(in.Body) + "\n" + string(in.Overlay))
	case in.HasBody:
		return in.Body
	case in.HasOverlay:
		return in.Overlay
	default:
		return nil
	}
}

// composedRepoContextPlan is the owned-document form of RepoContextPlan: one
// composed document at the repository root carrying the whole chain a session
// there reads, plus one per declared subdirectory carrying that subdirectory's
// own body.
//
// The subdirectory documents carry no chain of their own, and that is a property
// of the agent rather than an omission: a session below the repository root
// reads the root document and every document between it and the working
// directory, so repeating the outer layers in each one would spend the shared
// byte budget several times over to say the same thing twice.
func (p Producer) composedRepoContextPlan(in RepoContextInputs, comp composition) *Plan {
	plan := &Plan{}

	var layers [][]byte
	if comp.composesOuterLayers {
		layers = append(layers, in.Workspace, in.Group)
	}
	layers = append(layers, repoBody(in))

	// The committed file joins the chain only when niwa has something of its own
	// to deliver. A document composed from the committed content alone would
	// claim the directory's context slot to say exactly what native discovery
	// already says, so the configured layers are composed first and the inline
	// is added only if they came to anything.
	root := composeDocument(layers...)
	if root != nil && in.Probe.HasInlined {
		root = composeDocument(append(layers, in.Probe.Inlined)...)
	}

	if root != nil && in.Probe.InlineRefusal != "" {
		plan.Warnings = append(plan.Warnings, in.Probe.InlineRefusal)
	}

	// Chain sizes are accumulated as the entries are declared, so the budget
	// check measures the documents that will actually be written and not the
	// ones a refusal or the never-empty rule kept off disk.
	sizes := map[string]int{in.Dir: p.appendOwnedEntry(plan, RepoOrientationDoc, in.Dir, in.Dir, root, in.Probe)}

	for _, sub := range in.Subdirs {
		doc := composeDocument(sub.Body)
		sizes[sub.Dir] = p.appendOwnedEntry(plan, RepoOrientationDoc, in.Dir, sub.Dir, doc, sub.Probe)
	}

	// The worst chain travels out on the plan, where the generated project-layer
	// configuration for the same tree turns it into a declared budget that
	// covers it. Reporting an overflow here instead would move Codex's silent
	// cut to a line niwa prints and still leave the developer to raise the
	// budget by hand in a file niwa already writes.
	if comp.measuresChain {
		plan.ChainBytes = deepestChain(in.Dir, sizes)
	}

	return plan
}

// appendOwnedEntry declares one owned document and returns the bytes it will
// occupy in a session's chain, or records why it was not declared and returns
// zero. An absent document (the never-empty rule) is silent; a refused one is
// reported and exempted from cleanup.
func (p Producer) appendOwnedEntry(plan *Plan, c Capability, root, dir string, doc []byte, probe ContextProbe) int {
	if probe.Foreign {
		plan.Warnings = append(plan.Warnings, conflictWarning(probe.OwnedPath))
		plan.Exempt = append(plan.Exempt, probe.OwnedPath)
		return 0
	}
	if doc == nil {
		return 0
	}
	e := p.contextEntry(c, dir, doc)
	e.Pre = IfNotForeign
	e.Owner = generationMarker
	e.ExcludeAs = excludePattern(root, e.Path)
	plan.Entries = append(plan.Entries, e)
	return len(doc)
}

// excludePattern renders a written path as the git-exclude pattern the
// repository at root has to ignore it under: the path relative to the working
// tree, in slash form.
//
// The coverage travels with the write rather than arriving in a later change,
// and that is not tidiness. A niwa-written file in a working tree that git does
// not ignore makes the tree read dirty, and a dirty tree is what stops the
// worktree teardown from reclaiming a session's worktree -- so an uncovered
// name accumulates orphans rather than merely looking untidy.
func excludePattern(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// deepestChain returns the size of the largest chain from root down. A
// session's budget is spent on the documents between the project root and its
// working directory, so the worst case is the deepest path through the declared
// set rather than the sum of all of them.
func deepestChain(root string, sizes map[string]int) int {
	worst := sizes[root]
	for dir := range sizes {
		total := 0
		for ancestor, size := range sizes {
			if ancestor == dir || isAncestorDir(ancestor, dir) {
				total += size
			}
		}
		if total > worst {
			worst = total
		}
	}
	return worst
}

// isAncestorDir reports whether dir contains other. Both are absolute and
// already cleaned by the caller that built them with filepath.Join.
func isAncestorDir(dir, other string) bool {
	return strings.HasPrefix(other, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// WorktreeContextInputs is what the worktree layer needs: where the worktree
// is, the heading that opens the section niwa owns, and the rendered body that
// follows it. The heading is an input rather than a constant here because it
// delimits a section of a document the developer may also write in -- it is a
// property of the layer, not of the agent.
type WorktreeContextInputs struct {
	// Dir is the absolute worktree root.
	Dir string

	// Heading opens the niwa-owned section and delimits the region a re-apply
	// replaces. It must appear at the start of Body's section.
	Heading string

	// Body is the rendered section body, written after the heading.
	Body []byte

	// Probe is what the caller found at ContextProbeSpec(Dir) after the
	// repository-level plan for the same worktree was applied. It is taken at
	// that point, not before, because whether this section joins an existing
	// document or has to stand as one depends on whether that plan wrote.
	Probe ContextProbe
}

// WorktreeContextPlan declares the worktree's purpose/branch section as a
// section replace, so re-applying a worktree rewrites the section in place
// instead of appending a second copy. Anything the developer wrote above the
// heading is left alone by the executor.
//
// For an agent whose documents are owned, the section joins the composed
// document the repository-level plan wrote into the same worktree -- which is
// why the ownership marker has to be on the first line however the file came to
// exist. When that plan wrote nothing, because no configured layer had content,
// this section is the whole document and carries the marker itself. Appending a
// markerless section to a file nobody had written would produce a document niwa
// could not recognize as its own on the next apply, and would then refuse to
// refresh forever.
func (p Producer) WorktreeContextPlan(in WorktreeContextInputs) (*Plan, error) {
	ok, err := p.delivers(WorktreeOrientationDoc)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &Plan{}, nil
	}

	section := []byte(in.Heading + "\n\n" + string(in.Body))

	if !p.composition().owned {
		e := p.contextEntry(WorktreeOrientationDoc, in.Dir, section)
		e.Op = OpReplaceSection
		e.Marker = in.Heading
		return &Plan{Entries: []Entry{e}}, nil
	}

	if in.Probe.Foreign {
		return &Plan{
			Warnings: []string{conflictWarning(in.Probe.OwnedPath)},
			Exempt:   []string{in.Probe.OwnedPath},
		}, nil
	}

	e := p.contextEntry(WorktreeOrientationDoc, in.Dir, section)
	e.Pre = IfNotForeign
	e.Owner = generationMarker
	e.ExcludeAs = excludePattern(in.Dir, e.Path)
	if in.Probe.Owned {
		e.Op = OpReplaceSection
		e.Marker = in.Heading
	} else {
		e.Content = composeDocument(section)
	}

	return &Plan{Entries: []Entry{e}}, nil
}

// contextEntry is one context document written whole into dir. The entries it
// builds are Managed: these documents are niwa's to keep current and to remove
// when they stop being declared. Today the paths reach the managed-file record
// through the list the installing function returns to the pipeline, so the flag
// records what the file is rather than driving the bookkeeping.
func (p Producer) contextEntry(c Capability, dir string, body []byte) Entry {
	return Entry{
		Capability: c,
		Op:         OpWriteFile,
		Path:       p.localContextPath(dir),
		Content:    body,
		Mode:       contextFileMode,
		Managed:    true,
	}
}

// legacyRootContextFile is the name niwa wrote its instance-root context
// document under before an agent was something a workspace could choose. It is
// named here, in the package that owns filenames, because the cleanup below
// maintains a file earlier versions left behind: the file to clean is a fact
// about niwa's history, not about the agent this session prepares for.
const legacyRootContextFile = "CLAUDE.md"

// LegacyRootContextPath is the instance-root document the legacy-import
// cleanup reads and rewrites. Callers need it because the decision to rewrite
// depends on what the file holds, and reading the target tree is the executor's
// side of the boundary rather than this package's.
func LegacyRootContextPath(dir string) string {
	return filepath.Join(dir, legacyRootContextFile)
}

// LegacyImportInputs describes one legacy-import cleanup: the instance root,
// what the legacy document holds right now, and the relative @import line that
// no longer belongs in it.
type LegacyImportInputs struct {
	// Dir is the absolute instance root.
	Dir string

	// Existing is the legacy document's current bytes, meaningful only when
	// Exists is set.
	Existing []byte
	Exists   bool

	// Import is the relative @import line to remove.
	Import string
}

// LegacyImportPlan declares the removal of one relative @import that older
// niwa versions wrote into the instance-root context document, before those
// imports moved to absolute paths in the workspace rules file.
//
// It takes no agent, unlike the producers above, and that is deliberate: the
// document it cleans up was written when there was only one agent, so the file
// to clean is the same one whichever agent this session prepares for. Doing it
// per-agent would leave the historical file untouched for everyone else.
//
// The plan is empty unless the removal would change bytes -- an absent document
// and one that never carried the import both produce no entry, so the cleanup
// cannot create the file it exists to tidy.
func LegacyImportPlan(in LegacyImportInputs) *Plan {
	if !in.Exists {
		return &Plan{}
	}
	content := string(in.Existing)
	if !strings.Contains(content, in.Import) {
		return &Plan{}
	}

	// The writer that added these lines always emitted "line\n\n"; try that
	// form first so the blank line that followed goes with it.
	content = strings.Replace(content, in.Import+"\n\n", "", 1)
	content = strings.Replace(content, in.Import+"\n", "", 1)

	return &Plan{Entries: []Entry{{
		Capability: RootSessionOrientation,
		Op:         OpWriteFile,
		Path:       LegacyRootContextPath(in.Dir),
		Content:    []byte(content),
		Mode:       contextFileMode,
	}}}
}
