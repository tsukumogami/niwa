package testhook

// The points on the removal path RemovalBeforeStatus is called at: between
// a repository's configuration read and the git status after it.
const (
	// StageRule is the rule's own status in a repository, after the pass's
	// first configuration read there.
	StageRule = "rule"
	// StageReread is a snapshot's status (the re-read before removal and
	// the check on the tombstone), after the snapshot's own configuration
	// read.
	StageReread = "reread"
)
