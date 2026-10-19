package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// preToolUseEntries returns the PreToolUse entries of a settings document.
func preToolUseEntries(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	return parsed.Hooks["PreToolUse"]
}

func guardEntries(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range preToolUseEntries(t, doc) {
		if e["matcher"] == worktreeGuardMatcher {
			out = append(out, e)
		}
	}
	return out
}

func TestBuildSettingsDoc_WorktreeGuard(t *testing.T) {
	supported := &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"}
	cases := []struct {
		name  string
		cfg   BuildSettingsConfig
		count int
	}{
		{"root scope with delegation", BuildSettingsConfig{WorktreeDelegation: supported, WorktreeGuard: true}, 1},
		{"deny branch still guarded", BuildSettingsConfig{WorktreeDelegation: &WorktreeDelegation{Supported: false, NiwaPath: "/abs/niwa"}, WorktreeGuard: true}, 1},
		{"repo scope", BuildSettingsConfig{WorktreeDelegation: supported}, 0},
		{"opted out", BuildSettingsConfig{WorktreeGuard: true}, 0},
		{"no niwa path", BuildSettingsConfig{WorktreeDelegation: &WorktreeDelegation{Supported: false}, WorktreeGuard: true}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := buildSettingsDoc(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			got := guardEntries(t, doc)
			if len(got) != tc.count {
				t.Fatalf("guard entries = %d, want %d: %v", len(got), tc.count, got)
			}
			if tc.count == 0 {
				return
			}
			hooks := got[0]["hooks"].([]any)
			h := hooks[0].(map[string]any)
			if !strings.Contains(h["command"].(string), "niwa worktree guard") {
				t.Errorf("command %q should run niwa worktree guard", h["command"])
			}
			if h["timeout"] != float64(worktreeGuardTimeoutSeconds) {
				t.Errorf("timeout = %v, want %d", h["timeout"], worktreeGuardTimeoutSeconds)
			}
		})
	}
}

// TestBuildSettingsDoc_WorktreeGuardKeepsOtherBashEntries checks the guard is
// appended to PreToolUse beside an existing Bash entry rather than replacing
// it, and that its matcher is not the bare "Bash" niwa watch dedupes on.
func TestBuildSettingsDoc_WorktreeGuardKeepsOtherBashEntries(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		InstalledHooks: map[string][]InstalledHookEntry{
			"pre_tool_use": {{Matcher: "Bash", Paths: []string{"/abs/gate.sh"}}},
		},
		UseAbsolutePaths:   true,
		WorktreeDelegation: &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"},
		WorktreeGuard:      true,
		PrBodyHook:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var bash, guard int
	for _, e := range preToolUseEntries(t, doc) {
		switch e["matcher"] {
		case "Bash":
			bash++
		case worktreeGuardMatcher:
			guard++
		}
	}
	if bash != 2 || guard != 1 {
		t.Errorf("want 2 Bash entries (installed + pr-body) and 1 guard, got %d and %d", bash, guard)
	}
}

// TestRootDocuments_CarryTheGuard checks the instance root and workspace root
// get the guard and a repo's settings never do.
func TestRootDocuments_CarryTheGuard(t *testing.T) {
	rootText, repoText := createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, false)
	if !strings.Contains(rootText, "niwa worktree guard") {
		t.Errorf("instance root settings should carry the guard:\n%s", rootText)
	}
	if strings.Contains(repoText, "worktree guard") {
		t.Errorf("repo settings must not carry the guard:\n%s", repoText)
	}

	rootText, _ = createWithHarness(t, &HarnessSupport{WorktreeHooks: true}, true)
	if strings.Contains(rootText, "worktree guard") {
		t.Errorf("an opted-out instance root must not carry the guard:\n%s", rootText)
	}

	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{
		WorktreeDelegation: &WorktreeDelegation{Supported: true, NiwaPath: "/abs/niwa"},
	})
	if len(guardEntries(t, doc)) != 1 {
		t.Errorf("workspace root settings should carry one guard entry")
	}
	doc, _ = materializeRoot(t, cfg, RootMaterializeOptions{})
	if len(guardEntries(t, doc)) != 0 {
		t.Errorf("workspace root with worktree_delegation off should carry no guard")
	}
}

func TestResolveHookScope(t *testing.T) {
	f := newHookFixture(t)
	s, err := ResolveHookScope(f.alpha, f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	if s.InstanceRoot == "" || strings.Join(s.Repos, ",") != "koto,niwa" || !strings.HasSuffix(s.WorktreesDir, "/.niwa/worktrees") {
		t.Errorf("unexpected scope %+v", s)
	}

	s, err = ResolveHookScope(f.workspaceRoot, f.workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if s.InstanceRoot != "" || strings.Join(s.Instances, ",") != "alpha,beta" {
		t.Errorf("workspace-root scope should list instances, got %+v", s)
	}

	state, _ := LoadState(f.alpha)
	state.NoWorktreeDelegation = true
	if err := SaveState(f.alpha, state); err != nil {
		t.Fatal(err)
	}
	if s, err := ResolveHookScope(f.alpha, f.alpha); err != nil || !s.OptedOut {
		t.Errorf("opted-out instance should report OptedOut, got %+v, %v", s, err)
	}

	if _, err := ResolveHookScope(t.TempDir(), t.TempDir()); err == nil {
		t.Error("a directory outside any workspace should be an error")
	}
}
