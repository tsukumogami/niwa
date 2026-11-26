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
	// StageTombstoneLock is the pass taking the shared tombstone lock, after
	// the re-read and right before the rename to the tombstone. The command
	// gets the instance's own path as NIWA_PLANT_INSTANCE_PATH.
	StageTombstoneLock = "tombstone-lock"
)
