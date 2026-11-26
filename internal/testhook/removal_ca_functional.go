//go:build niwa_functional

package testhook

import "os"

// removalCAFileEnv names a CA bundle the removal path's git calls trust.
const removalCAFileEnv = "NIWA_TEST_REMOVAL_CA_FILE"

// RemovalCAFile returns the CA bundle the removal path's git calls are given
// as GIT_SSL_CAINFO: only in the test build, and only while
// NIWA_TEST_REMOVAL_CA_FILE names one. The functional suite's https git
// server has a certificate no system store trusts, so a scenario that has
// the rule contact it sets this to the server's CA.
func RemovalCAFile() string { return os.Getenv(removalCAFileEnv) }
