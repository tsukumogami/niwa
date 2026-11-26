//go:build !niwa_functional

package testhook

// RemovalFileRemotes reports false: a release build's removal path never
// contacts a file remote.
func RemovalFileRemotes() bool { return false }
