//go:build !unix

package workspace

// Platforms without flock get no substitute lock. A lock file created and
// removed by hand would serialize nothing under contention -- the window
// between the check and the create is the whole race -- and pretending
// otherwise is worse than the honest gap, which is bounded by the same
// exposure Codex's own concurrent sessions already carry. The atomic
// replacement below still holds: a concurrent apply can lose its entries to
// the other's write, but never leaves a truncated or invalid document.
func acquireCodexTrustLock(string) (func(), error) {
	return func() {}, nil
}
