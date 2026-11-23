package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// credentialReadTimeout bounds the credential-helper read.
const credentialReadTimeout = 10 * time.Second

// readCredentialHelpers reads the person's own credential helpers from their
// global git configuration, once per pass, before any unit is judged. It is
// the one git call built outside gitCommand, because it has to see the global
// file gitCommand sets aside with GIT_CONFIG_GLOBAL=/dev/null. It is bounded
// so it touches nothing an agent wrote: it runs from an empty directory under
// the pass's scratch directory, checked for owner and mode first, with the
// fixed environment except the global-file setting, GIT_CEILING_DIRECTORIES
// set so no repository is discovered, and a 10-second context.
//
// It reads values and runs no program, and only credential.helper: never a
// credential. --no-includes keeps it off any file the global one includes,
// and a helper scoped to a URL (credential.<url>.helper) isn't this key, so
// neither is found; a remote that needs one reads as unreachable and keeps.
//
// The values go only on network calls to proof remotes, against a scratch
// store, never on a call inside a unit's repository.
func (e *Env) readCredentialHelpers(ctx context.Context) ([]string, error) {
	if e.Git == "" {
		return nil, errors.New("no git on the filtered PATH")
	}
	dir, err := e.scratchDir(scratchHelperRead)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, credentialReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.Git, "--no-pager", "config", "--global", "--no-includes", "--get-all", "-z", "credential.helper")
	cmd.Env = e.credentialReadEnv()
	cmd.Dir = dir
	groupKill(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 1 && stdout.Len() == 0 && strings.TrimSpace(stderr.String()) == "":
		// The key isn't set: the person has no helper.
		return nil, nil
	default:
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("reading the credential helpers: %s", msg)
	}
	return parseHelpers(stdout.Bytes())
}

// credentialReadEnv is gitEnv without GIT_CONFIG_GLOBAL, so git reads the
// person's global file, and nothing else changed.
func (e *Env) credentialReadEnv() []string {
	var env []string
	for _, kv := range e.gitEnv() {
		if strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// parseHelpers reads git config --get-all -z output: each value
// NUL-terminated. An empty value resets the list, as git reads it.
func parseHelpers(out []byte) ([]string, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("reading the credential helpers: the listing is not NUL-terminated")
	}
	var helpers []string
	for _, v := range strings.Split(string(out[:len(out)-1]), "\x00") {
		if v == "" {
			helpers = nil
			continue
		}
		helpers = append(helpers, v)
	}
	return helpers, nil
}
