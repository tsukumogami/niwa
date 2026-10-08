package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

const defaultSetupDir = "scripts/setup"

// SetupTerminalEnv is the variable that tells a setup script a terminal is
// available: niwa sets it to "1" when the command running the script was
// started with both stdin and stderr on a terminal, and only for the commands
// a person runs directly (`niwa apply`, `niwa create`). Provisioning paths --
// the SessionStart hook, dispatch, watch, reap -- never set it. A script that
// wants to prompt reads and writes /dev/tty; its stdout and stderr stay piped
// through niwa's output handling either way.
const SetupTerminalEnv = "NIWA_SETUP_TERMINAL"

// setupTerminalEnv returns the entries that signal a terminal to setup
// scripts: the variable when terminal is true, nothing otherwise.
func setupTerminalEnv(terminal bool) []string {
	if !terminal {
		return nil
	}
	return []string{SetupTerminalEnv + "=1"}
}

// setupScriptEnv builds a setup script's environment: base (the inherited
// environment) with every SetupTerminalEnv entry removed, followed by extra.
// The terminal signal therefore reaches a script only when the caller put it in
// extra; an inherited value never survives.
func setupScriptEnv(base, extra []string) []string {
	env := make([]string, 0, len(base)+len(extra))
	prefix := SetupTerminalEnv + "="
	for _, kv := range base {
		if strings.HasPrefix(kv, prefix) || kv == SetupTerminalEnv {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// ScriptResult records the outcome of running a single setup script.
type ScriptResult struct {
	Name  string
	Error error // nil = success
}

// SetupResult records the outcome of running setup scripts for one repo.
type SetupResult struct {
	RepoName string
	Scripts  []ScriptResult
	Skipped  bool // directory not found
	Disabled bool // explicitly disabled via empty string
}

// ResolveSetupDir returns the effective setup directory for a repo. The
// resolution order is: repo override -> workspace default -> "scripts/setup".
// Returns empty string when explicitly disabled (repo override set to "").
func ResolveSetupDir(ws *config.WorkspaceConfig, repoName string) string {
	if override, ok := ws.Repos[repoName]; ok && override.SetupDir != nil {
		return *override.SetupDir
	}
	if ws.Workspace.SetupDir != "" {
		return ws.Workspace.SetupDir
	}
	return defaultSetupDir
}

// RunSetupScripts scans setupDir within repoDir for executable scripts and
// runs them in lexical order. Stops on the first script that exits non-zero.
// Returns nil if the directory doesn't exist or is empty.
//
// Each script is announced before it runs and its stdout and stderr are
// streamed durably, one line at a time, prefixed with `[<repo>/<script>] `.
// r receives all of it; pass a non-nil *Reporter. red is nil-tolerant; when
// non-nil, every emitted line is scrubbed of the secrets it has registered.
// Passing nil means no scrubbing, which is appropriate only where no secret
// has been resolved into the repo's working tree.
// extraEnv is appended to the inherited process environment for each script.
// It is variadic so a caller with nothing to add passes nothing.
//
// The environment is always built explicitly rather than left to Go's
// nil-cmd.Env inheritance, because one variable must never be inherited:
// NIWA_SETUP_TERMINAL. It is niwa's statement that this run has a terminal, so
// a value carried in from the parent -- an outer niwa, a shell that exported it
// once -- would make a script on a provisioning path believe it can prompt. It
// is filtered from os.Environ() on every call and reaches a script only when
// the caller put it in extraEnv. See setupScriptEnv.
//
// Nothing here carries a resolved secret: these are paths, names and a flag.
// Secrets reach setup scripts by FILE only.
//
// The terminal signal does not change where output goes. A script's stdout and
// stderr are still piped through the line scanner and the redactor, and the
// spinner is stopped by the r.Log announcement before each script starts, so a
// script that prompts through /dev/tty draws on a quiet terminal.
func RunSetupScripts(repoDir, setupDir string, r *Reporter, red *secret.Redactor, extraEnv ...string) *SetupResult {
	// The repo name reaches output, and while it comes from workspace config
	// rather than from the repo, it costs nothing to hold it to the same
	// standard as the script names below.
	repoName := stripEscapes(filepath.Base(repoDir))
	result := &SetupResult{RepoName: filepath.Base(repoDir)}

	if setupDir == "" {
		result.Disabled = true
		return result
	}

	dir := filepath.Join(repoDir, setupDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Skipped = true
			return result
		}
		result.Scripts = append(result.Scripts, ScriptResult{
			Name:  setupDir,
			Error: fmt.Errorf("reading setup directory: %w", err),
		})
		return result
	}

	// Collect executable files in lexical order.
	var scripts []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		scripts = append(scripts, entry.Name())
	}
	sort.Strings(scripts)

	if len(scripts) == 0 {
		result.Skipped = true
		return result
	}

	for _, name := range scripts {
		scriptPath := filepath.Join(dir, name)

		info, err := os.Stat(scriptPath)
		if err != nil {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: fmt.Errorf("stat: %w", err),
			})
			break
		}

		// Check executable bit.
		if info.Mode()&0o111 == 0 {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: fmt.Errorf("not executable (chmod +x to enable)"),
			})
			continue // warn and skip, don't stop
		}

		// Script filenames are repo-controlled and reach the terminal in
		// both the announcement and the per-line prefix, so they go
		// through the same sanitizer as the script's own output.
		display := stripEscapes(name)
		r.Log("running setup script %s/%s", repoName, display)
		prefix := fmt.Sprintf("[%s/%s] ", repoName, display)

		cmd := exec.Command(scriptPath)
		cmd.Dir = repoDir
		cmd.Env = setupScriptEnv(os.Environ(), extraEnv)

		if err := runCmdWithReporter(r, cmd, prefix, red); err != nil {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: err,
			})
			break // stop remaining scripts for this repo
		}

		result.Scripts = append(result.Scripts, ScriptResult{Name: name})
	}

	return result
}
