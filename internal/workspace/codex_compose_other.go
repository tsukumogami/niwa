//go:build !unix

package workspace

// Platforms without an O_NOFOLLOW open get no weaker substitute: a
// stat-then-open leaves a window in which the path can be swapped between the
// check and the read, which is exactly the defense this rule exists to close.
// readRegularFileNoFollow refuses instead, so the inline is skipped and reported
// rather than performed unsafely.
const nofollowOpenFlags = 0

const nofollowSupported = false
