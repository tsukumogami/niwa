//go:build unix

package workspace

import "syscall"

// nofollowOpenFlags refuses a symlink at the final path component in the open
// itself (O_NOFOLLOW) and keeps a non-regular file from stalling the open
// (O_NONBLOCK, which matters for FIFOs -- a read-only open of one otherwise
// blocks until a writer appears).
const nofollowOpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// nofollowSupported reports that this platform can enforce the rule at the open.
const nofollowSupported = true
