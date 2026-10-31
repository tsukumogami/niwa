package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
// Three in-instance writes are a hard deny too, in both postures and never an
// operator approval (see classifyWrite): the instance's own .claude/ tree and its
// .mcp.json, which Claude Code loads as the session's settings, hooks, skills,
// agents, commands and MCP servers, so a write there could loosen the review's own
// containment at its next start; a path through a symlink that can't be resolved;
// and a file with more than one hard link. Oversized input is denied after it is
// read to the end (see readHookInput).
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
	data, err := readHookInput(stdin)
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
	switch classifyWrite(target, root) {
	case writeInside:
		if askOutside {
			emitDecision(stdout, "allow", "in-instance write permitted by niwa watch review guard")
		}
		return 0
	case writeInstanceSettings:
		// A hard deny in both postures: the session's own settings are never an
		// operator-approval question.
		fmt.Fprintf(stderr, "niwa watch: writing the review instance's Claude Code settings (.claude/ or .mcp.json) is denied (target %q)\n", target)
		return 2
	case writeUnresolvable:
		fmt.Fprintf(stderr, "niwa watch guard-fs: cannot resolve the write target through its symlinks; denying (target %q)\n", target)
		return 2
	case writeHardLinked:
		fmt.Fprintf(stderr, "niwa watch guard-fs: the write target has more than one hard link; denying (target %q)\n", target)
		return 2
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

// hookInputLimit caps how much of a hook payload a guard decodes.
const hookInputLimit = 32 << 20

// readHookInput reads a PreToolUse hook payload from stdin. It always reads stdin to
// the end, even when the payload is over hookInputLimit or the read fails part way:
// a hook that exits without draining leaves Claude Code writing into a closed pipe,
// which its hook runner reports as a non-blocking result, so the tool call would go
// through. An oversized payload is an error, so the caller denies.
func readHookInput(stdin io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(stdin, hookInputLimit+1))
	if err != nil {
		_, _ = io.Copy(io.Discard, stdin)
		return nil, err
	}
	if len(data) > hookInputLimit {
		_, _ = io.Copy(io.Discard, stdin)
		return nil, fmt.Errorf("hook input is larger than %d bytes", hookInputLimit)
	}
	return data, nil
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

// writeClass is the guard's classification of a write target.
type writeClass int

const (
	// writeInside is a write that resolves inside the instance, to a path the
	// review may write.
	writeInside writeClass = iota
	// writeOutside is a write that resolves outside the instance.
	writeOutside
	// writeInstanceSettings is a write to the instance's own .claude/ directory or
	// its .mcp.json, which Claude Code loads as the review session's project
	// settings, hooks, skills, agents, commands and MCP servers.
	writeInstanceSettings
	// writeUnresolvable is a write whose path passes through a symlink that can't
	// be resolved (dangling, looping or unreadable), so where it lands is unknown.
	writeUnresolvable
	// writeHardLinked is a write to an existing file with more than one hard link,
	// whose other names may be anywhere, the instance's settings included.
	writeHardLinked
)

// classifyWrite decides where target (as the Write/Edit tool would open it) lands
// relative to the instance root. A relative target is resolved against root; ".."
// components are collapsed by filepath.Clean; and symlinks on the deepest existing
// ancestor of BOTH target and root are resolved, so an in-instance symlink pointing
// outside cannot be used to escape (and a symlinked root, such as macOS's /var,
// which links into the system's private tree, still compares equal to itself).
//
// Inside the instance, three cases are refused whatever the posture:
//
//   - The instance's own .claude/ tree and its .mcp.json, compared without regard
//     to case because a case-insensitive volume (the macOS default) maps .CLAUDE
//     and .MCP.JSON onto the real files. Both the path as written and the resolved
//     path are checked, so a symlink into .claude/ (or a .claude that is itself a
//     symlink) doesn't get around it. A .claude/ or .mcp.json deeper in the
//     instance, such as the PR clone's own, isn't the session's project settings
//     and stays writable.
//   - A path through a symlink that can't be resolved. The tool would create the
//     symlink's target, which may be one of the files above or outside the
//     instance.
//   - An existing file with more than one hard link: its path is in the instance,
//     but writing it rewrites every other name it has.
//
// Residual (future hardening, not a blocker): the resolution is time-of-check, so a
// symlink created AFTER this check but before the tool's open() could in principle
// redirect the write (a TOCTOU race). Exploiting it needs a concurrent writer to
// plant the symlink in that window; the only concurrent write surface a review
// session has is Bash, which the OS sandbox cages to the instance -- so this is a
// theoretical residual, tracked separately, not an open hole here.
func classifyWrite(target, root string) writeClass {
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	root = filepath.Clean(root)

	rootReal, ok := resolveDeepestExisting(root)
	if !ok {
		return writeUnresolvable
	}
	targetReal, ok := resolveDeepestExisting(target)
	if !ok {
		return writeUnresolvable
	}
	// The path as written, against both spellings of the root.
	for _, r := range []string{root, rootReal} {
		if withinDir(target, r) && isInstanceSettingsPath(target, r) {
			return writeInstanceSettings
		}
	}
	if !withinDir(targetReal, rootReal) {
		return writeOutside
	}
	if isInstanceSettingsPath(targetReal, rootReal) {
		return writeInstanceSettings
	}
	if hasOtherHardLinks(targetReal) {
		return writeHardLinked
	}
	return writeInside
}

// isInstanceSettingsPath reports whether p, a path within root, is root's own
// .claude directory or anything under it, or root's .mcp.json. The comparison
// ignores case.
func isInstanceSettingsPath(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return false
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	if strings.EqualFold(parts[0], ".claude") {
		return true
	}
	return len(parts) == 1 && strings.EqualFold(parts[0], ".mcp.json")
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
// final target file does not exist yet (the common Write case). It reports false
// when the existing ancestor can't be resolved (a dangling or looping symlink), or
// when a component can't be inspected for a reason other than not existing: the
// caller can't know where such a write lands, so it denies.
func resolveDeepestExisting(p string) (string, bool) {
	cur := p
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", false
			}
			rest := strings.TrimPrefix(p, cur)
			return filepath.Clean(resolved + rest), true
		}
		if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			return "", false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, true
		}
		cur = parent
	}
}

// hasOtherHardLinks reports whether p is an existing non-directory with more than
// one hard link. A file whose link count can't be read counts as linked, so the
// caller denies.
func hasOtherHardLinks(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return uint64(st.Nlink) > 1
}
