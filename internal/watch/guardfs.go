package watch

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// guardFSInput is the subset of the Claude Code PreToolUse hook payload the
// filesystem guard needs. The hook receives the full tool call as JSON on stdin;
// Write/Edit/MultiEdit carry file_path, NotebookEdit carries notebook_path. cwd is
// the session working directory (the review instance root) and is a fallback
// containment root only when the hook did not pass an explicit --root and
// CLAUDE_PROJECT_DIR is unset.
type guardFSInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

// GuardFSDecision is the filesystem-escape guard for a contained review session.
// It reads a PreToolUse hook payload (a Write/Edit/NotebookEdit call) from stdin and
// returns the process exit code the hook contract uses: 0 for a resolved decision, 2
// to block. It is fail-closed by construction -- unreadable input, unparseable JSON,
// a missing target path, or an undeterminable instance root all return 2 (deny), so a
// malformed or adversarial payload can never coax a write through, in either mode.
//
// The OS sandbox cages only Bash subprocesses; the built-in
// Write/Edit/MultiEdit/NotebookEdit tools run through the permission system, which a
// dispatched session's bypassPermissions skips. So without this hook an injected
// review agent could write outside the instance (~/.ssh/authorized_keys, ~/.bashrc,
// ~/.gitconfig hooksPath) and persist / gain code execution from merely reading an
// untrusted PR. The guard closes that: a write whose resolved target is inside the
// instance is permitted (the agent must write its draft and clone-local files there);
// a write that resolves outside the instance is not.
//
// askOutside selects the guard's two postures for the out-of-instance case:
//
//   - askOutside == false (hard-deny posture, the shipped floor): an out-of-instance
//     write is a hard deny (exit 2) and an in-instance write is a plain allow (exit 0);
//     no decision object is printed. This is inert-under-bypassPermissions safe -- the
//     exit code is the whole contract.
//   - askOutside == true (operator-approval posture): the guard prints a PreToolUse
//     permission-decision object to stdout -- "allow" for an in-instance write, "ask"
//     for an out-of-instance one -- and exits 0. Under a non-bypass permission mode the
//     harness honors the hook decision, so the out-of-instance write surfaces an
//     operator approval that fails closed if unanswered, while the in-instance write is
//     auto-approved so the review runs without hanging on a prompt.
//
// In both postures every fail-closed path exits 2 with no decision object, so the
// out-of-instance escape can only ever become an ask for a cleanly-resolved target.
//
// rootOverride is the instance path the review-session hook bakes in (via --root). It
// is the authoritative containment root: niwa knows the instance path at settings-write
// time, so the guard does not have to trust an ambient value such as CLAUDE_PROJECT_DIR,
// which could in principle be inherited wider than the instance.
func GuardFSDecision(stdin io.Reader, stdout, stderr io.Writer, rootOverride string, askOutside bool) int {
	data, err := io.ReadAll(io.LimitReader(stdin, 32<<20))
	if err != nil {
		fmt.Fprintf(stderr, "niwa watch guard-fs: reading hook input: %v\n", err)
		return 2
	}
	var in guardFSInput
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(stderr, "niwa watch guard-fs: parsing hook input: %v\n", err)
		return 2
	}
	target := in.ToolInput.FilePath
	if target == "" {
		target = in.ToolInput.NotebookPath
	}
	if target == "" {
		// A Write/Edit/MultiEdit/NotebookEdit with no target path is unexpected;
		// deny rather than guess.
		fmt.Fprintln(stderr, "niwa watch guard-fs: no target path in tool input; denying")
		return 2
	}
	root := guardInstanceRoot(rootOverride, in.Cwd)
	if root == "" {
		fmt.Fprintln(stderr, "niwa watch guard-fs: cannot determine the review instance root; denying")
		return 2
	}
	if writeWithinInstance(target, root) {
		if askOutside {
			emitDecision(stdout, "allow", "in-instance write permitted by niwa watch review guard")
		}
		return 0
	}
	if askOutside {
		// Operator-approval posture: surface the anomalous out-of-instance write as an
		// approval the operator can accept or deny in the agents view. An unanswered ask
		// fails closed (the write does not land).
		emitDecision(stdout, "ask", fmt.Sprintf("out-of-instance write to %q; operator approval required", target))
		return 0
	}
	fmt.Fprintf(stderr, "niwa watch: writing outside the review instance is denied by the no-egress sandbox (target %q)\n", target)
	return 2
}

// preToolUseDecision is the PreToolUse hookSpecificOutput the guard prints on stdout
// in the operator-approval posture. permissionDecision is "allow" or "ask"; the
// harness honors it under a non-bypass permission mode.
type preToolUseDecision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// emitDecision writes a PreToolUse permission-decision object to stdout. A marshal
// failure is silent: the caller still returns exit 0 for an allow (harmless) and, for
// an ask, an absent decision under a non-bypass mode falls through to the normal
// permission flow -- which prompts, so the out-of-instance write still does not
// silently land.
func emitDecision(stdout io.Writer, decision, reason string) {
	var d preToolUseDecision
	d.HookSpecificOutput.HookEventName = "PreToolUse"
	d.HookSpecificOutput.PermissionDecision = decision
	d.HookSpecificOutput.PermissionDecisionReason = reason
	if out, err := json.Marshal(d); err == nil {
		fmt.Fprintln(stdout, string(out))
	}
}

// guardInstanceRoot resolves the review instance root the write is confined to.
// The explicit rootOverride (the --root the review-session hook bakes in) wins --
// it is niwa's own record of the instance path and is not subject to any ambient
// widening. Only when it is empty (a bare `niwa watch guard-fs` invocation) does it
// fall back to CLAUDE_PROJECT_DIR, then the hook payload's cwd, then the process
// working directory. Returns "" only when none is available (caller denies).
func guardInstanceRoot(rootOverride, inputCwd string) string {
	if rootOverride != "" {
		return rootOverride
	}
	if v := os.Getenv("CLAUDE_PROJECT_DIR"); v != "" {
		return v
	}
	if inputCwd != "" {
		return inputCwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// writeWithinInstance reports whether target (as the Write/Edit tool would open
// it) lands inside the instance root. A relative target is resolved against root;
// ".." components are collapsed by filepath.Clean; and symlinks on the deepest
// existing ancestor of BOTH target and root are resolved, so an in-instance
// symlink pointing outside cannot be used to escape (and a symlinked root such as
// macOS /var -> /private/var still compares equal to itself).
//
// Residual (future hardening, not a blocker): the resolution is time-of-check, so a
// symlink created AFTER this check but before the tool's open() could in principle
// redirect the write (a TOCTOU race). Exploiting it needs a concurrent writer to
// plant the symlink in that window; the only concurrent write surface a review
// session has is Bash, which the OS sandbox cages to the instance -- so this is a
// theoretical residual, tracked separately, not an open hole here.
func writeWithinInstance(target, root string) bool {
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	root = filepath.Clean(root)

	rootReal := resolveDeepestExisting(root)
	targetReal := resolveDeepestExisting(target)
	return withinDir(targetReal, rootReal)
}

// withinDir reports whether p is root itself or a path beneath it.
func withinDir(p, root string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}

// resolveDeepestExisting walks up p to its deepest existing ancestor, resolves
// that ancestor's symlinks, and re-appends the non-existent remainder. This lets
// the containment check see through a symlinked directory component even when the
// final target file does not exist yet (the common Write case).
func resolveDeepestExisting(p string) string {
	cur := p
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return cur
			}
			rest := strings.TrimPrefix(p, cur)
			return filepath.Clean(resolved + rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		cur = parent
	}
}
