// Package testhook holds the hooks the functional suite's test build uses to
// reach into niwa: fault labels, a shorter mirror lock wait, and a record of
// the environment each mirror git process is given.
//
// Every hook has two files. The one built with the niwa_functional tag (the
// tag `make build-test` sets) does the work; its untagged twin is a no-op, so
// a release build has no code that reads the hooks' variables or writes the
// probe. This file holds what both builds share.
package testhook

// The fault labels Fault answers. They're reached only through Fault, never
// by calling testfault.Maybe with them directly, since testfault is active in
// every build and these labels must fire only in the test build.
const (
	// LabelBorrowClone fails a borrowing clone after git started.
	LabelBorrowClone = "borrow-clone"
	// LabelDissociateCopy fails the copy of a clone's borrowed objects.
	LabelDissociateCopy = "dissociate-copy"
)

// The kinds of mirror git process ProbeGitEnv records.
const (
	// KindNetwork is a call that reaches the remote: a clone, a fetch or a
	// fetch by id.
	KindNetwork = "network"
	// KindLocal is a call that reads or writes local files only.
	KindLocal = "local"
)
