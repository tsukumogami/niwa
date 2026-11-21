package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/firstread"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// firstReadGuardSuffix, firstReadGuardMatcher and firstReadGuardTimeout
// describe the PreToolUse hook a dispatch with --first-read puts in its launch
// settings document.
const (
	firstReadGuardSuffix  = "instance first-read-guard"
	firstReadGuardMatcher = "Bash|Read|Grep"
	firstReadGuardTimeout = 5
)

// instanceFirstReadGuardCmd is the PreToolUse hook that holds a dispatched
// worker to its brief's declared first read (see internal/firstread). It
// fails open like the worktree guard: anything it can't read, parse or
// resolve lets the call through, and an instance with no declaration, or one
// whose first read is done, is never refused.
var instanceFirstReadGuardCmd = &cobra.Command{
	Use:           "first-read-guard",
	Short:         "Internal: PreToolUse guard holding a dispatched worker to its brief's first read (reads JSON on stdin)",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	Run: func(cmd *cobra.Command, _ []string) {
		os.Exit(runFirstReadGuard(cmd.InOrStdin(), cmd.ErrOrStderr(), os.Getenv("CLAUDE_PROJECT_DIR")))
	},
}

func init() {
	instanceCmd.AddCommand(instanceFirstReadGuardCmd)
}

// runFirstReadGuard reads one PreToolUse payload and returns guardBlockExit
// with the refusal on stderr, or 0. projectDir is the session's project
// directory, which for a dispatched worker is its instance root; the
// payload's cwd is the fallback.
func runFirstReadGuard(stdin io.Reader, stderr io.Writer, projectDir string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "niwa first-read guard: internal error, allowing: %v\n", r)
			code = 0
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(stdin, guardStdinLimit))
	if err != nil {
		return 0
	}
	var payload firstread.Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0
	}
	instance := firstReadInstance(projectDir, payload.Cwd)
	if instance == "" || firstread.Done(instance) {
		return 0
	}
	decl, ok := firstread.Load(instance)
	if !ok {
		return 0
	}
	d := firstread.Decide(payload, decl, filepath.EvalSymlinks)
	if !d.Allow {
		fmt.Fprintln(stderr, d.Reason)
		return guardBlockExit
	}
	if d.Satisfies {
		if err := firstread.MarkDone(instance); err != nil {
			fmt.Fprintf(stderr, "niwa first-read guard: recording the first read: %v\n", err)
		}
	}
	return 0
}

// firstReadInstance finds the instance holding the declaration: the project
// directory when it has one, else the instance above the call's cwd.
func firstReadInstance(projectDir, cwd string) string {
	if projectDir != "" {
		if _, ok := firstread.Load(projectDir); ok {
			return projectDir
		}
	}
	if cwd == "" {
		return ""
	}
	dir, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return ""
	}
	return dir
}

// launchHooksKey is the launch settings document's hooks key. The guard runs
// beside the hooks in the instance's own settings files: Claude Code documents
// that hook entries merge across user, project and local settings, and a live
// dispatch carrying this key still ran the instance's worktree guard.
const launchHooksKey = "hooks"

// resolveFirstRead checks --first-read against the brief and the agent and
// returns the declaration to record, or nil when the flag wasn't given.
// briefPath is --brief as typed; content is what readBriefFile read from it.
func resolveFirstRead(n int, briefPath string, content []byte, ag agent.Agent, hasSettingsFlag bool) (*firstread.Declaration, error) {
	if n == 0 {
		return nil, nil
	}
	if n < 0 {
		return nil, fmt.Errorf("--first-read %d: the count of lines must be 1 or more", n)
	}
	if briefPath == "" {
		return nil, fmt.Errorf("--first-read needs --brief: it bounds the first read of the brief file")
	}
	if total := firstread.CountLines(content); n >= total {
		return nil, fmt.Errorf("--first-read %d: the brief has %d lines, so a first read of %d bounds nothing", n, total, n)
	}
	decl, err := agentplan.Lookup(agentplan.DispatchFirstReadGuard, ag)
	if err != nil {
		return nil, err
	}
	if decl.State != agentplan.StateImplemented || !hasSettingsFlag {
		return nil, fmt.Errorf("--first-read can't be enforced for the %q agent: %s", ag, decl.Reason)
	}
	resolved, err := filepath.EvalSymlinks(briefPath)
	if err != nil {
		return nil, fmt.Errorf("--brief %s: %w", briefPath, err)
	}
	if resolved, err = filepath.Abs(resolved); err != nil {
		return nil, fmt.Errorf("--brief %s: %w", briefPath, err)
	}
	return &firstread.Declaration{Brief: resolved, Lines: n}, nil
}

// firstReadGuardHooks is the launch settings document's "hooks" value for a
// dispatch with --first-read. niwaPath is the running binary's absolute
// path, and the hook runs that binary by it alone.
//
// It is deliberately not PATH-first like the hooks in materialized settings
// files (workspace.GuardedNiwaHookCommand). Those outlive the niwa that wrote
// them, so they follow upgrades through PATH. This one lives for one session,
// and the dispatching binary is the one that wrote the declaration it reads.
// A PATH-first command runs whichever niwa the worker's PATH names first; one
// that predates the subcommand prints help and exits 0, which Claude Code
// reads as allow, so the guard would be silently off. If the binary is gone
// by the time the hook runs, the shell exits 127, which is also not a
// refusal: the guard fails open, as it does everywhere else.
func firstReadGuardHooks(niwaPath string) map[string]any {
	return map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": firstReadGuardMatcher,
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "'" + strings.ReplaceAll(filepath.ToSlash(niwaPath), "'", `'\''`) + "' " + firstReadGuardSuffix,
						"timeout": firstReadGuardTimeout,
					},
				},
			},
		},
	}
}
