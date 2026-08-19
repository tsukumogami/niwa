package agentplan

import "io/fs"

// Op is the closed set of primitive operations a plan entry may declare.
// Adding a member is a design change rather than an implementation detail: each
// one is a new way niwa touches a developer's tree, and the executor in
// internal/workspace is where that gets reviewed once instead of per writer.
//
// The set is four because the write sites it replaces are: almost all of them
// are a plain write, and the exceptions are each one named helper with a
// documented rule -- the @import accumulation that appends a line unless it is
// already there, the worktree context layer's delimited-section replace, and
// the payload tree's symlink-or-copy delivery.
type Op uint8

const (
	// OpWriteFile writes Content at Path with Mode, creating parent
	// directories.
	OpWriteFile Op = iota

	// OpAppendLine appends Content to Path unless the line is already
	// present, which makes re-applying an instance idempotent.
	OpAppendLine

	// OpReplaceSection replaces the region of Path delimited by Marker,
	// leaving whatever else the file holds alone.
	OpReplaceSection

	// OpDeliverTree links Source at Path, copying when the link cannot be
	// made. Owner is required: it is the line the executor writes into the
	// delivered tree's sentinel file, and reads back to tell its own copy from
	// a directory somebody else put at the name.
	OpDeliverTree
)

// Precondition is the closed set of conditions gating an entry. Preconditions
// are evaluated by the executor at write time, not by the producer, so a
// condition about the target tree does not drag filesystem access into this
// package.
type Precondition uint8

const (
	// Always writes the entry unconditionally. It is the zero value, so an
	// entry that says nothing about gating is written.
	Always Precondition = iota

	// IfSourceExists writes the entry only when Source is present. An absent
	// Source is a no-op, not an error.
	IfSourceExists

	// IfNotForeign writes the entry only when Path is free for niwa to take:
	// absent, or a regular file whose first line is Owner. It is the write-time
	// half of the ownership rule, re-checked here because the producer's verdict
	// was computed in a pre-pass and a repository's own file can appear in
	// between. Anything else -- a symlink, a directory, a file niwa did not
	// write -- is left exactly as it is.
	IfNotForeign
)

// Entry is one declared write: what to put where, under which capability, and
// what bookkeeping follows from it.
//
// Capability is not decoration. It is what binds the entry to the declaration
// table -- an entry tagged with a capability that is not implemented for its
// agent is a delivery nobody declared, and an implemented declaration with no
// entry behind it is a declaration nobody delivered. Both directions are
// checked, which is what keeps the table from drifting into fiction.
type Entry struct {
	// Capability is the declared capability this write delivers.
	Capability Capability

	// Op is the operation the executor performs.
	Op Op

	// Path is the absolute target.
	Path string

	// Content is the bytes for OpWriteFile, the line for OpAppendLine, and
	// the section body for OpReplaceSection.
	Content []byte

	// Source is the OpDeliverTree source and the IfSourceExists probe path.
	Source string

	// Mode is the permission the file is created with: 0o600 for anything
	// carrying resolved secret material, 0o644 for ordinary content, 0o755
	// for an executable.
	Mode fs.FileMode

	// Marker delimits the region OpReplaceSection rewrites.
	Marker string

	// Owner is the line that identifies an existing path as one niwa wrote:
	// the first line of the file for IfNotForeign, and the content of the
	// sentinel inside a delivered tree for OpDeliverTree. It is a separate
	// field from Marker because an entry can need both: a worktree's Codex
	// document is a section replace delimited by the section heading, in a file
	// whose ownership is decided by the generation marker on its first line.
	Owner string

	// Pre gates the entry.
	Pre Precondition

	// Managed reports whether the written path joins the instance's managed
	// file record and its cleanup. Files niwa writes into a developer's own
	// tree deliberately do not.
	Managed bool

	// ExcludeAs is an extra git-exclude pattern this write implies, empty
	// when it implies none. Declaring it beside the write is what keeps a
	// niwa-written file from being staged by the repository that receives it.
	ExcludeAs string

	// Sources records what fed the content, for the managed file record's
	// source fingerprint. It never carries secret material; see SourceEntry.
	Sources []SourceEntry
}

// Plan is one agent's whole declared output for one materialization level:
// what will be written, and what the user should be told about what will not.
//
// A plan is data. It is produced from an agent and a set of inputs and consumed
// by the executor, and there is no path from configuration to bytes on disk
// that goes around it -- which is what makes the agent load-bearing by
// construction rather than by review discipline.
type Plan struct {
	// Entries are the declared writes, in the order they should be applied.
	Entries []Entry

	// Warnings are the things a user needs to hear about this plan: a
	// declaration that could not be honored, a refusal, an omission.
	Warnings []string

	// Exempt lists paths the managed-file cleanup must not delete even though
	// this plan produced nothing at them. There is exactly one reason a path
	// lands here: the plan refused to write it because the repository commits
	// its own file at one of niwa's names. Without the exemption, cleanup would
	// see a recorded path the current apply did not produce and delete the
	// repository's own file -- the opposite of what the refusal promised. The
	// record entry still goes; only the deletion is exempted.
	Exempt []string

	// ChainBytes is how many bytes the documents this plan writes occupy in the
	// worst chain a session reads under the tree it wrote them in. It is zero
	// for a plan whose agent spends no shared budget across its context
	// documents, and zero for every plan that is not a context plan.
	//
	// It travels on the plan because the size is a by-product of composing the
	// chain and the caller cannot recompute it: the executor sees the entries,
	// not the layers they were folded from. The generated project-layer
	// configuration declares a budget covering it -- see codexBudgetFor.
	ChainBytes int
}
