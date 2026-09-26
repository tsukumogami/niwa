//go:build !unix

package workspace

// Platforms without an O_NOFOLLOW open get no weaker substitute: a
// stat-then-open leaves a window in which the path can be swapped between the
// check and the read, which is exactly the defense the inline read exists to
// mount. readRegularFileNoFollow refuses instead, so a tree's own context file
// is left out of the composition and reported rather than read unsafely.
const nofollowOpenFlags = 0

const nofollowSupported = false
