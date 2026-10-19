package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// imperativeStart matches a line that starts with one of the imperative verbs
// the PRD rules out of the injected context.
var imperativeStart = regexp.MustCompile(`(?m)^(?:- )?(Run|Use|Always|Never|Do|Don't|Make|Create|Navigate|Go)\b`)

func TestBuildRootContext(t *testing.T) {
	f := newHookFixture(t)
	text, err := BuildRootContext(f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{rootContextIntro, "public/niwa", "public/koto", rootContextWorktree} {
		if !strings.Contains(text, want) {
			t.Errorf("context lacks %q:\n%s", want, text)
		}
	}
	if imperativeStart.MatchString(text) {
		t.Errorf("context has a line starting with an imperative verb:\n%s", text)
	}

	single, err := BuildRootContext(f.beta)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(single, "private/solo") {
		t.Errorf("one-repo context should list its repo:\n%s", single)
	}

	empty := t.TempDir()
	if err := SaveState(empty, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	none, err := BuildRootContext(empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(none, rootContextIntro) || !strings.Contains(none, rootContextNoRepos) {
		t.Errorf("zero-repo context should say the instance has no repos:\n%s", none)
	}

	state, _ := LoadState(f.alpha)
	state.NoWorktreeDelegation = true
	if err := SaveState(f.alpha, state); err != nil {
		t.Fatal(err)
	}
	optedOut, err := BuildRootContext(f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(optedOut, "EnterWorktree") {
		t.Errorf("an opted-out instance's context must leave out the worktree sentence:\n%s", optedOut)
	}
}

// TestBuildRootContext_SizeBound checks 20 repos with 30-character
// <group>/<repo> names stay within 2,000 characters.
func TestBuildRootContext_SizeBound(t *testing.T) {
	root := t.TempDir()
	group := "groupname-0123"
	for i := 0; i < 20; i++ {
		repo := fmt.Sprintf("repository-name-%02d", i) // group/repo is 33 chars
		if err := os.MkdirAll(filepath.Join(root, group, repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveState(root, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	text, err := BuildRootContext(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) > 2000 {
		t.Errorf("context is %d characters, want at most 2000", len(text))
	}
}

func TestBuildSettingsDoc_RootContextHook(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{RootContextNiwaPath: "/abs/niwa"})
	if err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	entries := hooks["SessionStart"].([]map[string]any)
	if len(entries) != 1 || entries[0]["matcher"] != rootContextMatcher {
		t.Fatalf("want one SessionStart entry with matcher %q, got %v", rootContextMatcher, entries)
	}
	for _, source := range []string{"startup", "resume", "clear", "compact"} {
		if !regexp.MustCompile(`(^|\|)` + source + `($|\|)`).MatchString(rootContextMatcher) {
			t.Errorf("matcher %q lacks source %q", rootContextMatcher, source)
		}
	}

	doc, err = buildSettingsDoc(BuildSettingsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if hooks, ok := doc["hooks"].(map[string]any); ok && hooks["SessionStart"] != nil {
		t.Errorf("no context hook should be written without a niwa path")
	}
}

// TestRootContextOnlyAtInstanceRoot checks the hook reaches the instance root,
// opted out or not, and never a repo.
func TestRootContextOnlyAtInstanceRoot(t *testing.T) {
	rootText, repoText := createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, false)
	if !strings.Contains(rootText, "instance context") {
		t.Errorf("instance root settings should carry the context hook:\n%s", rootText)
	}
	if strings.Contains(repoText, "instance context") {
		t.Errorf("repo settings must not carry the context hook:\n%s", repoText)
	}
	rootText, _ = createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, true)
	if !strings.Contains(rootText, "instance context") {
		t.Errorf("an opted-out instance root still gets the layout context:\n%s", rootText)
	}

	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{})
	if hooks, ok := doc["hooks"].(map[string]any); ok {
		for _, e := range toEntries(hooks["SessionStart"]) {
			if e["matcher"] == rootContextMatcher {
				t.Errorf("workspace root settings must not carry the instance context hook")
			}
		}
	}
}

func toEntries(v any) []map[string]any {
	list, _ := v.([]any)
	var out []map[string]any
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// TestGuardedNiwaContextCommand runs the command form against stub binaries:
// help text from an old niwa is dropped, the JSON a current niwa prints passes.
func TestGuardedNiwaContextCommand(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	for _, tc := range []struct {
		name, script, want string
	}{
		{"old niwa prints help", "#!/bin/sh\necho 'Usage: niwa instance [command]'\n", ""},
		{"current niwa prints JSON", "#!/bin/sh\necho '{\"hookSpecificOutput\":{}}'\n", "{\"hookSpecificOutput\":{}}\n"},
		{"failing niwa", "#!/bin/sh\necho oops >&2\nexit 1\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stub := filepath.Join(dir, "niwa")
			if err := os.WriteFile(stub, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", guardedNiwaContextCommand(stub))
			cmd.Env = []string{"PATH=" + dir}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("command failed: %v", err)
			}
			if string(out) != tc.want {
				t.Errorf("stdout = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestWorkspaceContextHasNoMoveIntoRepoBullets pins the removal of the two
// generated bullets.
func TestWorkspaceContextHasNoMoveIntoRepoBullets(t *testing.T) {
	text := generateWorkspaceContext(&config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}, nil)
	for _, gone := range []string{"before running git commands", "navigate into the specific repo"} {
		if strings.Contains(text, gone) {
			t.Errorf("workspace-context.md still carries %q:\n%s", gone, text)
		}
	}
}
