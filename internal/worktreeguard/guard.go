// Package worktreeguard decides whether a tool call from a session launched
// at a niwa instance root or workspace root should be refused because it
// tries to build a worktree a way that can't work there: EnterWorktree with a
// model-supplied path, or a hand-run `git worktree add`. The refusal tells
// the agent the form that works instead.
//
// The guard steers agents; it is not a security boundary. It reads simple
// commands only (see splitSimpleCommands), and the WorktreeCreate hook stays
// the enforcement for the EnterWorktree route.
//
// Decide is pure apart from the path resolver its caller injects, so the
// hook command that wraps it stays cheap on every Bash call: this package
// starts no process and opens no connection.
package worktreeguard

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Tool names the guard inspects.
const (
	toolBash          = "Bash"
	toolEnterWorktree = "EnterWorktree"
)

// Payload is the part of a PreToolUse hook payload the guard reads.
type Payload struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

// Scope is what the caller resolved about where the session is.
type Scope struct {
	// InstanceRoot is the instance the session acts in, or "" when none
	// resolved (a workspace-root session that hasn't moved into one).
	InstanceRoot string
	// WorktreesDir is the instance's niwa worktree directory, symlink-resolved.
	WorktreesDir string
	// Repos names the instance's repos, for the refusal text.
	Repos []string
	// Instances names the workspace's instances, for the refusal text when no
	// instance resolved.
	Instances []string
	// OptedOut is set when the instance was created with
	// --no-worktree-delegation; the guard then allows every call.
	OptedOut bool
}

// Decision is the guard's answer. Reason is set when Allow is false.
type Decision struct {
	Allow  bool
	Reason string
}

// PathResolver resolves symlinks in an absolute, cleaned path. It errors when
// the path doesn't exist.
type PathResolver func(string) (string, error)

// Decide judges one tool call.
func Decide(p Payload, s Scope, resolve PathResolver) Decision {
	if s.OptedOut {
		return Decision{Allow: true}
	}
	switch p.ToolName {
	case toolBash:
		var in struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(p.ToolInput, &in); err != nil {
			return Decision{Allow: true}
		}
		if commandAddsWorktree(in.Command) {
			return Decision{Reason: refusal(s, "a worktree built with git worktree add isn't one niwa manages")}
		}
	case toolEnterWorktree:
		var in struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(p.ToolInput, &in); err != nil || in.Path == "" {
			return Decision{Allow: true}
		}
		if !underWorktrees(p.Cwd, in.Path, s.WorktreesDir, resolve) {
			return Decision{Reason: refusal(s, "EnterWorktree with a path can't enter a worktree from here")}
		}
	}
	return Decision{Allow: true}
}

// underWorktrees reports whether path, joined with cwd when relative,
// cleaned and symlink-resolved, lies under worktreesDir. A path that doesn't
// resolve is not under it: every allowed target exists.
func underWorktrees(cwd, path, worktreesDir string, resolve PathResolver) bool {
	if worktreesDir == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	resolved, err := resolve(filepath.Clean(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(worktreesDir, resolved)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// refusal builds the reason text: what went wrong, the form that works, and
// the repos (or instances) available.
func refusal(s Scope, why string) string {
	if s.InstanceRoot == "" {
		where := "this workspace has no instances"
		if len(s.Instances) > 0 {
			where = "cd into one of: " + strings.Join(s.Instances, ", ")
		}
		return fmt.Sprintf("niwa: %s. This session is at a workspace root; %s, then use EnterWorktree with name <repo>/<slug>.", why, where)
	}
	example := "<repo>"
	if len(s.Repos) > 0 {
		example = s.Repos[0]
	}
	return fmt.Sprintf("niwa: %s. Instead, use EnterWorktree with name %s/<slug> (or <group>/<repo>/<slug>); niwa creates and tracks the worktree. Repos in this instance: %s.",
		why, example, strings.Join(s.Repos, ", "))
}
