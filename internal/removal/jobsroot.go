package removal

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ConfigDirVar is the variable Claude Code reads its config directory from.
const ConfigDirVar = "CLAUDE_CONFIG_DIR"

// ResolveJobsRoot gives the jobs root as Claude Code resolves it: the jobs
// directory under $CLAUDE_CONFIG_DIR when the variable is set, and
// ~/.claude/jobs when it isn't. lookup reads the environment and home is the
// person's home directory.
//
// The variable's value is used only when it is an absolute path that
// already equals its cleaned form, so the root is exactly the path Claude
// Code uses. A relative value, or one holding a "..", a "." or a repeated or
// trailing separator, is an error naming the variable, and so is a value
// starting with "~", which is never expanded. A pass that gets an error
// keeps every unit: it can't tell which sessions are live.
func ResolveJobsRoot(lookup func(string) (string, bool), home string) (string, error) {
	if v, ok := lookup(ConfigDirVar); ok && v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%s is not an absolute path: %q", ConfigDirVar, v)
		}
		if filepath.Clean(v) != v {
			return "", fmt.Errorf("%s is not a clean path: %q", ConfigDirVar, v)
		}
		return filepath.Join(v, "jobs"), nil
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", errors.New("the home directory can't be resolved, so the jobs root is unknown")
	}
	return filepath.Join(home, ".claude", "jobs"), nil
}
