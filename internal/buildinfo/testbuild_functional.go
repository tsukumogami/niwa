//go:build niwa_functional

package buildinfo

// IsTestBuild reports true: this binary carries the functional suite's test
// hooks and borrows from the host mirror before a release build does, so
// `niwa version` says so and a stray test binary is easy to spot.
func IsTestBuild() bool { return true }
