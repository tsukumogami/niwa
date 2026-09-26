package workspace

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// settingsHooksDoc is the minimal shape of the parts of settings.local.json the
// work-summary tests inspect: the hooks block, keyed by Pascal event name, each
// entry carrying an optional matcher and one or more commands.
type settingsHooksDoc struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// readSettingsHooks materializes the repo settings and returns the parsed hooks
// block. It fails the test if the file is missing or malformed.
func readSettingsHooks(t *testing.T, repoDir string) settingsHooksDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("reading settings.local.json: %v", err)
	}
	var doc settingsHooksDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings.local.json: %v\n%s", err, string(data))
	}
	return doc
}

// workSummaryCommandsFor returns every hook command registered for the given
// Pascal event that is an inline (niwa-default) work-summary pass-through — one
// carrying the `command -v shirabe` guard.
func workSummaryCommandsFor(doc settingsHooksDoc, event string) []string {
	var cmds []string
	for _, entry := range doc.Hooks[event] {
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, "command -v shirabe") {
				cmds = append(cmds, h.Command)
			}
		}
	}
	return cmds
}

// runWorkSummaryRepo materializes a single repo from cfg and returns its repo dir
// so the test can read the resulting settings.local.json. declaredScripts, when
// non-empty, are written under configDir/hooks/<event>/ and referenced from
// cfg.Claude.Hooks so the dedup path has real on-disk scripts to inspect.
func runWorkSummaryRepo(t *testing.T, cfg *config.WorkspaceConfig) string {
	t.Helper()
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	materializers := defaultRepoMaterializers(io.Discard)
	if _, _, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:       cfg,
		ConfigDir: configDir,
		RepoName:  "repo1",
		RepoDir:   repoDir,
	}); err != nil {
		t.Fatalf("runRepoMaterializers: %v", err)
	}
	return repoDir
}

func shirabePluginList() *[]string {
	p := []string{"shirabe@shirabe"}
	return &p
}

// TestWorkSummaryHooks_InjectedForShirabeAdopter is the core default-on case: a
// repo that installs the shirabe plugin and declares no work-summary hooks of
// its own materializes all three niwa-default hooks — PostToolUse capture on
// Bash, UserPromptSubmit absence, SessionStart compact — each a pure pass-through
// behind the `command -v shirabe` guard.
func TestWorkSummaryHooks_InjectedForShirabeAdopter(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude:    config.ClaudeConfig{Plugins: shirabePluginList()},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))

	cases := []struct {
		event   string
		matcher string
		mode    string
	}{
		{"PostToolUse", "Bash", "capture"},
		{"UserPromptSubmit", "", "absence"},
		{"SessionStart", "compact", "compact"},
	}
	for _, c := range cases {
		entries := doc.Hooks[c.event]
		if len(entries) != 1 {
			t.Fatalf("%s: want exactly 1 entry, got %d", c.event, len(entries))
		}
		if entries[0].Matcher != c.matcher {
			t.Errorf("%s: matcher = %q, want %q", c.event, entries[0].Matcher, c.matcher)
		}
		if len(entries[0].Hooks) != 1 {
			t.Fatalf("%s: want 1 command, got %d", c.event, len(entries[0].Hooks))
		}
		cmd := entries[0].Hooks[0].Command
		wantSuffix := "exec shirabe work-summary " + c.mode
		if !strings.HasSuffix(cmd, wantSuffix) {
			t.Errorf("%s: command = %q, want suffix %q", c.event, cmd, wantSuffix)
		}
		if !strings.HasPrefix(cmd, "command -v shirabe >/dev/null 2>&1 || exit 0;") {
			t.Errorf("%s: command = %q, missing fail-safe guard prefix", c.event, cmd)
		}
	}
}

// TestWorkSummaryHooks_AbsentWithoutShirabe asserts the gate: an instance that
// does not install the shirabe plugin receives none of the work-summary hooks.
func TestWorkSummaryHooks_AbsentWithoutShirabe(t *testing.T) {
	other := []string{"somethingelse@mkt"}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude:    config.ClaudeConfig{Plugins: &other},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart"} {
		if cmds := workSummaryCommandsFor(doc, event); len(cmds) != 0 {
			t.Errorf("%s: expected no work-summary hooks without shirabe, got %v", event, cmds)
		}
	}
}

// TestWorkSummaryHooks_OffSwitchSuppresses asserts the documented off switch:
// [claude] work_summary_hooks = false suppresses all three even for a shirabe
// adopter.
func TestWorkSummaryHooks_OffSwitchSuppresses(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins:          shirabePluginList(),
			WorkSummaryHooks: boolPtr(false),
		},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart"} {
		if cmds := workSummaryCommandsFor(doc, event); len(cmds) != 0 {
			t.Errorf("%s: off switch should suppress injection, got %v", event, cmds)
		}
	}
}

// TestWorkSummaryHooks_DefaultOnKeyIsOn asserts an explicit
// work_summary_hooks = true is equivalent to the absent-key default: injection
// stays on.
func TestWorkSummaryHooks_DefaultOnKeyIsOn(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins:          shirabePluginList(),
			WorkSummaryHooks: boolPtr(true),
		},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))
	if cmds := workSummaryCommandsFor(doc, "PostToolUse"); len(cmds) != 1 {
		t.Errorf("work_summary_hooks = true should inject, got %v", cmds)
	}
}

// TestWorkSummaryHooks_NoDoubleRegistration is the idempotence guard: a workspace
// that installs shirabe AND still declares the three work-summary hooks itself
// (the dot-niwa state until its companion cleanup lands) must not double-register.
// Each event keeps exactly the one declared script hook; niwa injects no inline
// duplicate.
func TestWorkSummaryHooks_NoDoubleRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")

	writeScript := func(event, name, mode string) string {
		dir := filepath.Join(configDir, "hooks", event)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rel := filepath.Join("hooks", event, name)
		body := "#!/usr/bin/env bash\ncommand -v shirabe >/dev/null 2>&1 || exit 0\nexec shirabe work-summary " + mode + "\n"
		if err := os.WriteFile(filepath.Join(configDir, rel), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return rel
	}
	captureScript := writeScript("post_tool_use", "work-summary-capture.sh", "capture")
	absenceScript := writeScript("user_prompt_submit", "work-summary-return.sh", "absence")
	compactScript := writeScript("session_start", "work-summary-compact.sh", "compact")

	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins: shirabePluginList(),
			Hooks: config.HooksConfig{
				"post_tool_use":      {{Matcher: "Bash", Scripts: []string{captureScript}}},
				"user_prompt_submit": {{Scripts: []string{absenceScript}}},
				"session_start":      {{Matcher: "compact", Scripts: []string{compactScript}}},
			},
		},
	}

	materializers := defaultRepoMaterializers(io.Discard)
	if _, _, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:       cfg,
		ConfigDir: configDir,
		RepoName:  "repo1",
		RepoDir:   repoDir,
	}); err != nil {
		t.Fatalf("runRepoMaterializers: %v", err)
	}
	doc := readSettingsHooks(t, repoDir)

	for _, event := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart"} {
		entries := doc.Hooks[event]
		total := 0
		for _, e := range entries {
			total += len(e.Hooks)
		}
		if total != 1 {
			t.Errorf("%s: want exactly 1 registered command (declared only, no inline duplicate), got %d: %+v", event, total, entries)
		}
		if cmds := workSummaryCommandsFor(doc, event); len(cmds) != 0 {
			t.Errorf("%s: niwa injected an inline duplicate alongside the declared script: %v", event, cmds)
		}
	}
}

// TestBuildSettingsDoc_WorkSummaryCompactSurvivesEphemeralMerge guards the
// interaction with the ephemeral SessionStart merge: an injected SessionStart
// `compact` work-summary hook and the ephemeral "niwa instance from-hook" entry
// must BOTH appear in the SessionStart block, with the work-summary entry first.
func TestBuildSettingsDoc_WorkSummaryCompactSurvivesEphemeralMerge(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		WorkSummaryHooks: []WorkSummaryHookMode{
			{Event: "session_start", Matcher: "compact", Mode: "compact"},
		},
		SessionHooks: &SessionHooks{
			Command:        "/usr/bin/niwa instance from-hook",
			TimeoutSeconds: 180,
		},
	})
	if err != nil {
		t.Fatalf("buildSettingsDoc: %v", err)
	}
	entries := sessionStartEntries(t, doc)
	if len(entries) != 2 {
		t.Fatalf("SessionStart should carry 2 entries (work-summary + ephemeral), got %d", len(entries))
	}
	if got := sessionStartCommandAt(t, entries, 0); !strings.Contains(got, "work-summary compact") {
		t.Errorf("first SessionStart entry = %q, want the work-summary compact hook", got)
	}
	if got := sessionStartCommandAt(t, entries, 1); !strings.Contains(got, "instance from-hook") {
		t.Errorf("second SessionStart entry = %q, want the ephemeral instance-from-hook", got)
	}
}
