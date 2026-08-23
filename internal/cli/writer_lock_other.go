//go:build !unix

package cli

// writerLockHeld cannot answer on a platform without flock, so it says so
// rather than guessing.
//
// Returning "unknown" rather than "not held" is the whole point of the second
// return value. The caller treats an unanswered probe as a reason to spare an
// instance, so a platform that cannot run this check reclaims nothing on the
// activity rule instead of reclaiming everything.
func writerLockHeld(string) (held bool, known bool) {
	return false, false
}
