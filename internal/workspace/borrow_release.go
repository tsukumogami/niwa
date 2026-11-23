//go:build !niwa_functional

package workspace

// borrowReleased holds borrowing off in every release build: a clone never
// borrows from the host mirror, and no mirror is seeded for borrowing,
// whatever share_git_history says. Only the functional suite's test build
// (`make build-test`, the niwa_functional tag) sets it true, in
// borrow_functional.go. The change that turns borrowing on deletes both files
// and the AND in borrowEnabled.
const borrowReleased = false
