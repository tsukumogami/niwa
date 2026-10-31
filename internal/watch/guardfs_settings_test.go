package watch

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardPayloads returns one hook payload per file tool for target: Write, Edit and
// MultiEdit carry file_path, NotebookEdit carries notebook_path.
func guardPayloads(t *testing.T, target string) map[string]string {
	t.Helper()
	enc := func(tool, key string) string {
		b, err := json.Marshal(map[string]any{
			"tool_name":  tool,
			"tool_input": map[string]string{key: target},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	return map[string]string{
		"Write":        enc("Write", "file_path"),
		"Edit":         enc("Edit", "file_path"),
		"MultiEdit":    enc("MultiEdit", "file_path"),
		"NotebookEdit": enc("NotebookEdit", "notebook_path"),
	}
}

// guardRun runs GuardFSDecision with an explicit --root, as the installed hook does,
// and returns the exit code, stdout and stderr.
func guardRun(root, payload string, ask bool) (int, string, string) {
	var stdout, stderr strings.Builder
	code := GuardFSDecision(strings.NewReader(payload), &stdout, &stderr, root, ask)
	return code, stdout.String(), stderr.String()
}

// mkdirAll and writeAt are small fixture helpers.
func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeAt(t *testing.T, p, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeDirFixture builds an instance root holding the files a review instance
// has: .claude/settings.json and a PR clone, plus <root>/lnk -> <root>/.claude.
func claudeDirFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeAt(t, filepath.Join(root, ".claude", "settings.json"), "{}")
	mkdirAll(t, filepath.Join(root, "pr-clone"))
	if err := os.Symlink(filepath.Join(root, ".claude"), filepath.Join(root, "lnk")); err != nil {
		t.Fatal(err)
	}
	return root
}

// instanceSettingsTargets lists the write targets that reach the instance's own
// Claude Code settings.
func instanceSettingsTargets(root string) []string {
	return []string{
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".claude", "hooks", "h.sh"),
		filepath.Join(root, ".claude", "skills", "s", "SKILL.md"),
		filepath.Join(root, ".claude", "agents", "a.md"),
		filepath.Join(root, ".claude", "commands", "c.md"),
		filepath.Join(root, ".claude", "new", "deep", "x"),
		filepath.Join(root, ".mcp.json"),
		".claude/settings.local.json",
		".mcp.json",
		"pr-clone/../.claude/settings.local.json",
		filepath.Join(root, ".CLAUDE", "settings.local.json"),
		filepath.Join(root, ".Claude", "x"),
		filepath.Join(root, ".MCP.JSON"),
		filepath.Join(root, "lnk", "settings.local.json"),
	}
}

// TestGuardFSDecision_DeniesInstanceClaudeDir: in the hard-deny posture every write
// into the instance's .claude/ tree or to its .mcp.json is refused, through every
// file tool, whether or not the target exists, in any letter case, and through a
// symlink to .claude.
func TestGuardFSDecision_DeniesInstanceClaudeDir(t *testing.T) {
	root := claudeDirFixture(t)
	for _, target := range instanceSettingsTargets(root) {
		for tool, payload := range guardPayloads(t, target) {
			code, _, stderr := guardRun(root, payload, false)
			if code != 2 {
				t.Errorf("%s %s: exit %d, want 2", tool, target, code)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Errorf("%s %s: no refusal on stderr", tool, target)
			}
		}
	}
}

// TestGuardFSDecision_AskOutside_DeniesInstanceClaudeDir: the same writes are a hard
// deny in the operator-approval posture too, never an allow or an ask.
func TestGuardFSDecision_AskOutside_DeniesInstanceClaudeDir(t *testing.T) {
	root := claudeDirFixture(t)
	for _, target := range instanceSettingsTargets(root) {
		for tool, payload := range guardPayloads(t, target) {
			code, stdout, stderr := guardRun(root, payload, true)
			if code != 2 {
				t.Errorf("%s %s: exit %d, want 2", tool, target, code)
			}
			if stdout != "" {
				t.Errorf("%s %s: printed a decision %q, want none", tool, target, stdout)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Errorf("%s %s: no refusal on stderr", tool, target)
			}
		}
	}
}

// TestGuardFSDecision_AllowsInstanceWritesBesideClaudeDir: writes next to the
// protected paths, including the PR clone's own .claude/ and names that merely start
// with .claude, stay allowed in both postures.
func TestGuardFSDecision_AllowsInstanceWritesBesideClaudeDir(t *testing.T) {
	root := claudeDirFixture(t)
	targets := []string{
		filepath.Join(root, "watch-review-draft.md"),
		filepath.Join(root, "pr-clone", "main.go"),
		filepath.Join(root, "pr-clone", ".claude", "settings.json"),
		filepath.Join(root, ".claude-notes.md"),
		filepath.Join(root, "notes", ".mcp.json.bak"),
	}
	for _, target := range targets {
		for tool, payload := range guardPayloads(t, target) {
			if code, _, stderr := guardRun(root, payload, false); code != 0 {
				t.Errorf("hard-deny %s %s: exit %d, want 0 (stderr %q)", tool, target, code, stderr)
			}
			code, stdout, stderr := guardRun(root, payload, true)
			if code != 0 {
				t.Errorf("ask %s %s: exit %d, want 0 (stderr %q)", tool, target, code, stderr)
			}
			if got := decisionOf(t, stdout); got != "allow" {
				t.Errorf("ask %s %s: decision %q, want allow", tool, target, got)
			}
		}
	}
}

// TestGuardFSDecision_DeniesDanglingSymlink: a target whose path passes through a
// symlink that can't be resolved is refused in both postures, wherever the symlink
// points, because the tool would create the symlink's target.
func TestGuardFSDecision_DeniesDanglingSymlink(t *testing.T) {
	root := claudeDirFixture(t)
	outside := t.TempDir()
	links := map[string]string{
		"m.json": filepath.Join(root, ".mcp.json"),
		"out":    filepath.Join(outside, "new-file"),
		"d":      filepath.Join(root, ".claude", "newdir"),
	}
	for name, dest := range links {
		if err := os.Symlink(dest, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	targets := []string{
		filepath.Join(root, "m.json"),
		filepath.Join(root, "out"),
		filepath.Join(root, "d", "x"),
	}
	for _, target := range targets {
		for tool, payload := range guardPayloads(t, target) {
			for _, ask := range []bool{false, true} {
				code, stdout, _ := guardRun(root, payload, ask)
				if code != 2 {
					t.Errorf("ask=%v %s %s: exit %d, want 2", ask, tool, target, code)
				}
				if stdout != "" {
					t.Errorf("ask=%v %s %s: printed a decision %q, want none", ask, tool, target, stdout)
				}
			}
		}
	}
	for name, dest := range links {
		if _, err := os.Lstat(dest); err == nil {
			t.Errorf("the guard created %s's target %s", name, dest)
		}
	}
}

// TestGuardFSDecision_DeniesHardLinkedTarget: a file in the instance that is a hard
// link to the instance's settings is refused; an ordinary single-link file is not.
func TestGuardFSDecision_DeniesHardLinkedTarget(t *testing.T) {
	root := claudeDirFixture(t)
	hl := filepath.Join(root, "hl")
	if err := os.Link(filepath.Join(root, ".claude", "settings.json"), hl); err != nil {
		t.Fatal(err)
	}
	single := filepath.Join(root, "single.txt")
	writeAt(t, single, "x")
	for tool, payload := range guardPayloads(t, hl) {
		for _, ask := range []bool{false, true} {
			if code, _, _ := guardRun(root, payload, ask); code != 2 {
				t.Errorf("ask=%v %s hard link: exit %d, want 2", ask, tool, code)
			}
		}
	}
	for tool, payload := range guardPayloads(t, single) {
		if code, _, stderr := guardRun(root, payload, false); code != 0 {
			t.Errorf("%s single-link file: exit %d, want 0 (stderr %q)", tool, code, stderr)
		}
	}
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// TestGuardFSDecision_OversizedInputIsDrainedAndDenied: a payload over the input cap
// is read to the end, so Claude Code's write never fails into a closed pipe, and it
// is denied in both postures.
func TestGuardFSDecision_OversizedInputIsDrainedAndDenied(t *testing.T) {
	root := t.TempDir()
	size := int64(hookInputLimit + 1024)
	for _, ask := range []bool{false, true} {
		cr := &countingReader{r: io.LimitReader(zeroReader{}, size)}
		var stdout, stderr strings.Builder
		if code := GuardFSDecision(cr, &stdout, &stderr, root, ask); code != 2 {
			t.Errorf("ask=%v: exit %d, want 2", ask, code)
		}
		if cr.n != size {
			t.Errorf("ask=%v: read %d of %d bytes; the payload must be drained", ask, cr.n, size)
		}
		if stdout.Len() != 0 {
			t.Errorf("ask=%v: printed a decision %q, want none", ask, stdout.String())
		}
	}
}

// zeroReader yields an endless stream of zero bytes.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
