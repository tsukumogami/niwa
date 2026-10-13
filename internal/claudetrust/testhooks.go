package claudetrust

import "time"

// SetLockWaitForTest shortens how long the lock is waited for, forgets every
// lock path whose wait timed out, and returns the function that restores the
// wait and forgets the timeouts again.
//
// It exists for tests in other packages that drive a held lock through a
// caller (the apply pipeline) and must neither wait the full five seconds nor
// leave a timeout recorded for the tests that run after them. Production code
// never calls it. Tests that use it must not run in parallel with other trust
// writes in the same process.
func SetLockWaitForTest(wait time.Duration) (restore func()) {
	prevWait, prevPoll := lockWait, lockPoll
	lockWait = wait
	lockPoll = 10 * time.Millisecond
	clearLockTimeouts()
	return func() {
		lockWait, lockPoll = prevWait, prevPoll
		clearLockTimeouts()
	}
}
