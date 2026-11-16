package mirror

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gitBaseConfig is set on every git call in a mirror, whatever the store's
// own config says: a store's config isn't trusted after a fault.
var gitBaseConfig = []string{
	"core.fsmonitor=false",
	"protocol.ext.allow=never",
}

// gitEnv is the environment every git call here runs with: the parent's, with
// every inherited GIT_* variable removed (GIT_DIR, GIT_EXEC_PATH and
// GIT_ALTERNATE_OBJECT_DIRECTORIES among them, any of which would point git
// somewhere else), and no terminal prompt, since nobody is there to answer.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

// runGit runs a git subcommand with the base config plus config (each
// "key=value", passed as -c), in the cleaned environment. The error carries
// git's stderr.
func runGit(ctx context.Context, config []string, args ...string) error {
	full := make([]string, 0, 2*(len(gitBaseConfig)+len(config))+len(args))
	for _, kv := range append(append([]string{}, gitBaseConfig...), config...) {
		full = append(full, "-c", kv)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

// cloneMirror clones url as a bare mirror into dir. The clone runs with no
// template, so the new store has no hooks, and with fsckObjects and unpack
// limits of 1 set for the process itself, since the new store has no config
// to hold them yet: a malformed object from the remote is refused before any
// borrower can read it, and everything lands as a pack.
func cloneMirror(ctx context.Context, url, dir string) error {
	return runGit(ctx,
		[]string{
			"transfer.fsckObjects=true",
			"fetch.fsckObjects=true",
			"transfer.unpackLimit=1",
			"fetch.unpackLimit=1",
		},
		"clone", "--mirror", "--template=", "--quiet", "--", url, dir)
}
