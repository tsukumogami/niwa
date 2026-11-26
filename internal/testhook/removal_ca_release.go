//go:build !niwa_functional

package testhook

// RemovalCAFile returns "": a release build's removal path trusts only the
// system's certificates.
func RemovalCAFile() string { return "" }
