//go:build !niwa_functional

package testhook

import "time"

// Fault returns nil: no fault fires outside the test build.
func Fault(label string) error { return nil }

// MirrorLockWait returns def unchanged: only the test build shortens the
// mirror's lock wait.
func MirrorLockWait(def time.Duration) time.Duration { return def }

// ProbeGitEnv records nothing: only the test build writes the probe.
func ProbeGitEnv(kind, call string, env []string) {}
