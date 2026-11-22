//go:build niwa_functional

package testhook

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/testfault"
)

// This file is built only into the functional suite's test binary (`make
// build-test` sets the niwa_functional tag), never into a release build.

// lockWaitEnv shortens the mirror's lock waits, so a scenario can show a
// command giving up on a held lock without waiting minutes.
const lockWaitEnv = "NIWA_TEST_MIRROR_LOCK_WAIT"

// probeEnv names the file ProbeGitEnv appends to.
const probeEnv = "NIWA_TEST_GIT_ENV_PROBE"

// Fault returns testfault.Maybe(label): an error when NIWA_TEST_FAULT holds
// a spec for label.
func Fault(label string) error { return testfault.Maybe(label) }

// MirrorLockWait returns the wait NIWA_TEST_MIRROR_LOCK_WAIT gives when it
// parses as a Go duration above zero and below def, and def otherwise. It
// shortens a wait and never lengthens one, as NIWA_TEST_VAULT_TIMEOUT does.
func MirrorLockWait(def time.Duration) time.Duration {
	raw := os.Getenv(lockWaitEnv)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 || d >= def {
		return def
	}
	return d
}

// probeLine is one line of the probe file: one mirror git process.
type probeLine struct {
	Kind string            `json:"kind"`
	Call string            `json:"call"`
	Env  map[string]string `json:"env"`
}

// ProbeGitEnv appends one JSON line to the file NIWA_TEST_GIT_ENV_PROBE
// names, when it's set: the process's kind (KindNetwork or KindLocal), the
// call it makes, and every GIT_* entry of env, the environment the process
// is given, each with its value. A later entry for a name wins, as it does
// for the process. Each line is one write to a file opened O_APPEND, so lines
// from concurrent commands land whole. A failure to write is ignored: the
// probe never changes what the command does.
func ProbeGitEnv(kind, call string, env []string) {
	path := os.Getenv(probeEnv)
	if path == "" {
		return
	}
	line := probeLine{Kind: kind, Call: call, Env: map[string]string{}}
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if ok && strings.HasPrefix(name, "GIT_") {
			line.Env[name] = value
		}
	}
	data, err := json.Marshal(line)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
}
