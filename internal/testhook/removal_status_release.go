//go:build !niwa_functional

package testhook

// RemovalBeforeStatus does nothing: a release build never runs a command
// between a repository's configuration read and its status.
func RemovalBeforeStatus(stage, gitDir, workTree string) {}

// RemovalAtTombstoneLock does nothing: a release build never runs a command
// at the tombstone lock.
func RemovalAtTombstoneLock(instancePath string) {}
