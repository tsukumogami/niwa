//go:build niwa_functional

package workspace

// borrowReleased is true only in the functional suite's test build, so its
// scenarios can exercise borrowing before it ships. See borrow_release.go.
const borrowReleased = true
