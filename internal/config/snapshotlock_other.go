//go:build !unix

package config

// AcquireSnapshotLock is a no-op where flock isn't available. Refreshes on
// these platforms are unserialized, as they were before the lock existed.
func AcquireSnapshotLock(string) (func(), error) {
	return func() {}, nil
}
