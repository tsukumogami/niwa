//go:build !niwa_functional

package buildinfo

// IsTestBuild reports whether this binary was built with the niwa_functional
// tag, as `make build-test` builds the functional suite's binary. It is false
// in every release build.
func IsTestBuild() bool { return false }
