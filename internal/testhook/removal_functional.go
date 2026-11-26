//go:build niwa_functional

package testhook

import "os"

// removalFileRemotesEnv admits file remotes on the removal path's git calls.
const removalFileRemotesEnv = "NIWA_TEST_REMOVAL_FILE_REMOTES"

// RemovalFileRemotes reports whether the removal path may contact a file
// remote: only in the test build, and only when NIWA_TEST_REMOVAL_FILE_REMOTES
// is "1". The functional suite's repositories are local bare repositories
// reached by file URL, which a release build's git calls refuse, so a
// scenario that needs the rule to read a clone URL turns this on, and one
// that checks the refusal leaves it off.
func RemovalFileRemotes() bool { return os.Getenv(removalFileRemotesEnv) == "1" }
