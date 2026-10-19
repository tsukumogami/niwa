package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktreeguard"
)

// guardStdinLimit bounds how much of the hook payload the guard reads. A
// PreToolUse payload is a few kilobytes; the cap keeps a pathological one
// from stalling every Bash call in a root session.
const guardStdinLimit = 1 << 20

// guardBlockExit is the exit code that makes Claude Code block a PreToolUse
// call and show stderr to the model. It is honored in every permission mode
// and whether or not the workspace is trusted, which JSON decisions are not.
// Every other exit lets the call proceed.
const guardBlockExit = 2

// worktreeGuardCmd is the PreToolUse hook niwa writes into the instance
// root's and workspace root's settings, matched on EnterWorktree and Bash. It
// refuses the two ways of building a worktree that can't work from a root --
// EnterWorktree with a model-supplied path, and a hand-run
// `git worktree add` -- and its stderr tells the agent to use EnterWorktree
// with a `<repo>/<slug>` name instead. See internal/worktreeguard.
//
// It fails open: anything it can't read, parse or resolve lets the call
// through, because it runs on every Bash call in a root session and a broken
// guard must never block the shell. A niwa too old to have this subcommand
// fails it as an unknown command (exit 1) or prints help (exit 0); neither is
// exit 2, so Claude Code lets the call proceed either way.
var worktreeGuardCmd = &cobra.Command{
	Use:           "guard",
	Short:         "Internal: PreToolUse guard against building worktrees by hand at a root (reads JSON on stdin)",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(cmd *cobra.Command, _ []string) {
		os.Exit(runWorktreeGuard(cmd.InOrStdin(), cmd.ErrOrStderr()))
	},
}

func init() {
	sessionCmd.AddCommand(worktreeGuardCmd)
}

// runWorktreeGuard reads one PreToolUse payload and returns the exit code:
// guardBlockExit with the reason on stderr to block, 0 otherwise.
func runWorktreeGuard(stdin io.Reader, stderr io.Writer) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "niwa worktree guard: internal error, allowing: %v\n", r)
			code = 0
		}
	}()

	raw, err := io.ReadAll(io.LimitReader(stdin, guardStdinLimit))
	if err != nil {
		fmt.Fprintf(stderr, "niwa worktree guard: reading payload, allowing: %v\n", err)
		return 0
	}
	var payload worktreeguard.Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		fmt.Fprintf(stderr, "niwa worktree guard: parsing payload, allowing: %v\n", err)
		return 0
	}
	if !guardMayRefuse(payload) {
		return 0
	}
	anchor := workspace.HookAnchor()
	cwd := payload.Cwd
	if cwd == "" {
		// A payload without cwd is judged from the launch directory, so the
		// guard doesn't switch itself off for it.
		cwd = anchor
		payload.Cwd = anchor
	}

	hs, err := workspace.ResolveHookScope(anchor, cwd)
	if err != nil {
		// Not in a niwa instance or workspace this guard can reason about.
		return 0
	}
	decision := worktreeguard.Decide(payload, worktreeguard.Scope{
		InstanceRoot: hs.InstanceRoot,
		WorktreesDir: hs.WorktreesDir,
		Repos:        hs.Repos,
		Instances:    hs.Instances,
		OptedOut:     hs.OptedOut,
	}, filepath.EvalSymlinks)
	if decision.Allow {
		return 0
	}
	fmt.Fprintln(stderr, decision.Reason)
	return guardBlockExit
}

// guardMayRefuse is a cheap prefilter run before any filesystem work: only an
// EnterWorktree call, or a Bash command that mentions "worktree", can be
// refused, so every other call returns at once. A command that builds the word
// at run time (a variable, command substitution) passes here, as it would pass
// the tokenizer: the guard steers, it doesn't enforce.
func guardMayRefuse(p worktreeguard.Payload) bool {
	switch p.ToolName {
	case "EnterWorktree":
		return true
	case "Bash":
		return strings.Contains(string(p.ToolInput), "worktree")
	}
	return false
}
