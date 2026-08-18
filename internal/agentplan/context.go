package agentplan

import (
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
}

// For returns the producer that declares plans for ag. The zero Agent resolves
// to Claude, matching internal/agent's fail-safe contract.
func For(ag agent.Agent) Producer { return Producer{ag: ag} }

// delivers reports whether the declaration table says this agent receives c.
// The lookup is fail-closed: a pair the table cannot answer for is an error
// rather than a silent "no".
func (p Producer) delivers(c Capability) (bool, error) {
	d, err := Lookup(c, p.ag)
	if err != nil {
		return false, err
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

// SubdirContext is one context document that belongs in a directory nested
// inside the repository, declared by [content.repos.<name>.subdirs]. Dir is
// absolute and has already been containment-checked by the caller against the
// repository it belongs to; Body is the rendered document.
type SubdirContext struct {
	Dir  string
	Body []byte
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
}

// RepoContextPlan declares the repository-level context documents: the
// repository's own, with the overlay addendum folded in, plus one per declared
// subdirectory.
//
// The overlay is folded into the same entry rather than declared as a second
// write of the same path. The writer this replaces wrote the base file and then
// rewrote it with base + "\n" + overlay, so the bytes are identical and the
// path is still produced once, which is what the caller records.
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

	plan := &Plan{}

	switch {
	case in.HasBody:
		body := in.Body
		if in.HasOverlay {
			body = []byte(string(in.Body) + "\n" + string(in.Overlay))
		}
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, in.Dir, body))
	case in.HasOverlay:
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, in.Dir, in.Overlay))
	}

	for _, sub := range in.Subdirs {
		plan.Entries = append(plan.Entries, p.contextEntry(RepoOrientationDoc, sub.Dir, sub.Body))
	}

	return plan, nil
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
}

// WorktreeContextPlan declares the worktree's purpose/branch section as a
// section replace, so re-applying a worktree rewrites the section in place
// instead of appending a second copy. Anything the developer wrote above the
// heading is left alone by the executor.
func (p Producer) WorktreeContextPlan(in WorktreeContextInputs) (*Plan, error) {
	ok, err := p.delivers(WorktreeOrientationDoc)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &Plan{}, nil
	}

	e := p.contextEntry(WorktreeOrientationDoc, in.Dir, []byte(in.Heading+"\n\n"+string(in.Body)))
	e.Op = OpReplaceSection
	e.Marker = in.Heading

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
