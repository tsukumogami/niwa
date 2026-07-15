package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// readRootSettings materializes the workspace-root config into a temp dir and
// returns the parsed settings.json document plus the workspace root path.
func materializeRoot(t *testing.T, cfg *config.WorkspaceConfig, opts RootMaterializeOptions) (map[string]any, string) {
	t.Helper()
	root := t.TempDir()
	if opts.NiwaPath == "" {
		opts.NiwaPath = "/abs/niwa"
	}
	written, err := MaterializeWorkspaceRoot(cfg, root, opts)
	if err != nil {
		t.Fatalf("MaterializeWorkspaceRoot: %v", err)
	}
	// settings.json + CLAUDE.md + at least one project skill (dispatch).
	if len(written) < 3 {
		t.Fatalf("expected >= 3 written files (settings.json + CLAUDE.md + skills), got %d: %v", len(written), written)
	}

	settingsPath := filepath.Join(root, rootClaudeDir, rootSettingsFile)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading root settings: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing root settings: %v", err)
	}
	return doc, root
}

func TestMaterializeWorkspaceRoot_SessionHooks(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Claude: config.ClaudeConfig{
			Settings: config.SettingsConfig{
				"permissions": {Plain: "bypass"},
			},
		},
	}

	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{
		NiwaPath:             "/abs/niwa",
		EphemeralSessionMode: true,
	})

	// Permission posture: sourced exactly as the instance materializer sources
	// it -> permissions.defaultMode.
	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions block missing or wrong type: %#v", doc["permissions"])
	}
	if perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("permissions.defaultMode = %v, want bypassPermissions", perms["defaultMode"])
	}

	// Ephemeral-session-mode flag.
	if doc["ephemeralSessionMode"] != true {
		t.Errorf("ephemeralSessionMode = %v, want true", doc["ephemeralSessionMode"])
	}

	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks block missing: %#v", doc["hooks"])
	}

	// Only the SessionStart hook is installed -- teardown is reaper-driven, so
	// no SessionEnd entry is materialized (DESIGN Decision 6, revised).
	if _, present := hooks[sessionEndEvent]; present {
		t.Errorf("%s hook entry is present; want absent (teardown is reaper-driven)", sessionEndEvent)
	}

	for _, event := range []string{sessionStartEvent} {
		entries, ok := hooks[event].([]any)
		if !ok || len(entries) != 1 {
			t.Fatalf("%s entries missing or wrong shape: %#v", event, hooks[event])
		}
		entry := entries[0].(map[string]any)
		cmds := entry["hooks"].([]any)
		if len(cmds) != 1 {
			t.Fatalf("%s: want 1 command, got %d", event, len(cmds))
		}
		cmd := cmds[0].(map[string]any)
		if cmd["type"] != "command" {
			t.Errorf("%s: command type = %v, want command", event, cmd["type"])
		}
		gotCmd, _ := cmd["command"].(string)
		if !strings.HasSuffix(gotCmd, "instance from-hook") {
			t.Errorf("%s: command = %q, want suffix %q", event, gotCmd, "instance from-hook")
		}
		if !strings.Contains(gotCmd, "/abs/niwa") {
			t.Errorf("%s: command = %q, want absolute niwa path", event, gotCmd)
		}
		// Timeout must be generous (>= 120s) to absorb niwa create's clone +
		// vault cost. JSON numbers decode to float64.
		timeout, ok := cmd["timeout"].(float64)
		if !ok {
			t.Fatalf("%s: timeout missing or wrong type: %#v", event, cmd["timeout"])
		}
		if timeout < 120 {
			t.Errorf("%s: timeout = %v, want >= 120", event, timeout)
		}
	}
}

func TestMaterializeWorkspaceRoot_EphemeralFalse(t *testing.T) {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: false})
	if doc["ephemeralSessionMode"] != false {
		t.Errorf("ephemeralSessionMode = %v, want false", doc["ephemeralSessionMode"])
	}
	// Hooks are still written: the integration is the same regardless of the
	// recorded flag; the flag records posture, the hooks are inert until the
	// state opts in.
	if _, ok := doc["hooks"].(map[string]any); !ok {
		t.Errorf("hooks block missing even with ephemeral flag false")
	}
}

func TestMaterializeWorkspaceRoot_ClaudeMD(t *testing.T) {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "my-workspace"}}
	_, root := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true})

	claudePath := filepath.Join(root, rootClaudeFile)
	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("reading root CLAUDE.md: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "my-workspace") {
		t.Errorf("root CLAUDE.md missing workspace name; got:\n%s", content)
	}
	if !strings.Contains(content, "niwa instance from-hook") {
		t.Errorf("root CLAUDE.md missing ephemeral-session orientation; got:\n%s", content)
	}
	if !strings.Contains(content, "multi-repo workspace managed by niwa") {
		t.Errorf("root CLAUDE.md missing workspace-context orientation; got:\n%s", content)
	}
}

// TestMaterializeWorkspaceRoot_AgentFilename asserts the true-workspace-root
// context file is named by the selected agent: AGENTS.md under Codex, CLAUDE.md
// under Claude and the zero-value agent, with the same body. PRD R5, R6, R7.
func TestMaterializeWorkspaceRoot_AgentFilename(t *testing.T) {
	cases := []struct {
		name       string
		ag         agent.Agent
		wantFile   string
		absentFile string
	}{
		{"codex", agent.AgentCodex, "AGENTS.md", "CLAUDE.md"},
		{"claude", agent.AgentClaude, "CLAUDE.md", "AGENTS.md"},
		{"zero-value defaults to claude", agent.Agent(""), "CLAUDE.md", "AGENTS.md"},
	}
	var claudeBody string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "my-workspace"}}
			_, root := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true, Agent: tc.ag})

			data, err := os.ReadFile(filepath.Join(root, tc.wantFile))
			if err != nil {
				t.Fatalf("reading root %s: %v", tc.wantFile, err)
			}
			body := string(data)
			if !strings.Contains(body, "my-workspace") {
				t.Errorf("root %s missing workspace name; got:\n%s", tc.wantFile, body)
			}
			if _, err := os.Stat(filepath.Join(root, tc.absentFile)); !os.IsNotExist(err) {
				t.Errorf("expected %s to be absent, stat err = %v", tc.absentFile, err)
			}
			if tc.ag == agent.AgentClaude {
				claudeBody = body
			}
			if claudeBody != "" && body != claudeBody {
				t.Errorf("root body differs across agents:\n got: %q\nwant: %q", body, claudeBody)
			}
		})
	}
}

func TestMaterializeWorkspaceRoot_DispatchSkill(t *testing.T) {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	root := t.TempDir()
	written, err := MaterializeWorkspaceRoot(cfg, root, RootMaterializeOptions{
		NiwaPath:             "/abs/niwa",
		EphemeralSessionMode: true,
	})
	if err != nil {
		t.Fatalf("MaterializeWorkspaceRoot: %v", err)
	}

	skillPath := filepath.Join(root, rootClaudeDir, "skills", "dispatch", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("reading dispatch SKILL.md: %v", err)
	}
	content := string(data)

	if !strings.HasPrefix(content, "---") {
		t.Errorf("dispatch SKILL.md should start with YAML frontmatter; got:\n%s", content[:min(80, len(content))])
	}
	if !strings.Contains(content, "name: dispatch") {
		t.Errorf("dispatch SKILL.md missing 'name: dispatch' frontmatter")
	}
	if !strings.Contains(content, "# /dispatch") {
		t.Errorf("dispatch SKILL.md missing '# /dispatch' heading")
	}

	// The written paths slice must include the installed skill path.
	found := false
	for _, p := range written {
		if p == skillPath {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("returned paths %v do not include dispatch skill path %q", written, skillPath)
	}
}

func TestMaterializeWorkspaceRoot_NoPermissionsConfigured(t *testing.T) {
	// No [claude.settings] permissions key: the doc carries no permissions
	// block but still installs the session hooks.
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true})
	if _, ok := doc["permissions"]; ok {
		t.Errorf("permissions block present despite no configured posture: %#v", doc["permissions"])
	}
	if _, ok := doc["hooks"].(map[string]any); !ok {
		t.Errorf("hooks block missing")
	}
}

func TestMaterializeWorkspaceRoot_HoistsWorkspacePlugins(t *testing.T) {
	// A github-sourced marketplace and its plugin hoist to the root settings so
	// a root-launched session loads the workspace's plugins/skills. Track is
	// "main" so the build does no network release resolution.
	plugins := []string{"shirabe@shirabe"}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Claude: config.ClaudeConfig{
			Plugins: &plugins,
			Marketplaces: config.MarketplaceConfigs{
				{Source: "tsukumogami/shirabe", Track: "main"},
			},
		},
	}

	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true})

	enabled, ok := doc["enabledPlugins"].(map[string]any)
	if !ok {
		t.Fatalf("enabledPlugins block missing or wrong type: %#v", doc["enabledPlugins"])
	}
	if enabled["shirabe@shirabe"] != true {
		t.Errorf("enabledPlugins[shirabe@shirabe] = %v, want true", enabled["shirabe@shirabe"])
	}

	mkts, ok := doc["extraKnownMarketplaces"].(map[string]any)
	if !ok {
		t.Fatalf("extraKnownMarketplaces block missing or wrong type: %#v", doc["extraKnownMarketplaces"])
	}
	if _, ok := mkts["shirabe"]; !ok {
		t.Errorf("extraKnownMarketplaces missing github marketplace 'shirabe': %#v", mkts)
	}
}

func TestMaterializeWorkspaceRoot_ExcludesInstanceLocalMarketplace(t *testing.T) {
	// A repo:-sourced marketplace points into an instance checkout that does not
	// exist at the workspace root, so it (and the plugin bound to it) is omitted
	// from the root settings while a github-sourced sibling still hoists.
	plugins := []string{"shirabe@shirabe", "tsukumogami@tsukumogami"}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Claude: config.ClaudeConfig{
			Plugins: &plugins,
			Marketplaces: config.MarketplaceConfigs{
				{Source: "tsukumogami/shirabe", Track: "main"},
				{Source: "repo:tools/.claude-plugin/marketplace.json"},
			},
		},
	}

	doc, _ := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true})

	enabled, ok := doc["enabledPlugins"].(map[string]any)
	if !ok {
		t.Fatalf("enabledPlugins block missing: %#v", doc["enabledPlugins"])
	}
	if enabled["shirabe@shirabe"] != true {
		t.Errorf("github-sourced plugin should hoist; enabledPlugins = %#v", enabled)
	}
	if _, present := enabled["tsukumogami@tsukumogami"]; present {
		t.Errorf("instance-local plugin must be excluded from root, got enabledPlugins = %#v", enabled)
	}

	mkts, ok := doc["extraKnownMarketplaces"].(map[string]any)
	if !ok {
		t.Fatalf("extraKnownMarketplaces block missing: %#v", doc["extraKnownMarketplaces"])
	}
	if _, ok := mkts["shirabe"]; !ok {
		t.Errorf("github marketplace should hoist; got %#v", mkts)
	}
	if _, ok := mkts["tsukumogami"]; ok {
		t.Errorf("instance-local marketplace must be excluded from root; got %#v", mkts)
	}
}

func TestRootHoistableConfig(t *testing.T) {
	plugins := []string{"shirabe@shirabe", "tsukumogami@tsukumogami", "bare"}
	marketplaces := config.MarketplaceConfigs{
		{Source: "tsukumogami/shirabe"},
		{Source: "repo:tools/.claude-plugin/marketplace.json"},
	}

	keptPlugins, keptMarketplaces, reports := rootHoistableConfig(plugins, marketplaces)

	// github marketplace survives; repo: source is dropped.
	if len(keptMarketplaces) != 1 || keptMarketplaces[0].Source != "tsukumogami/shirabe" {
		t.Errorf("keptMarketplaces = %#v, want only tsukumogami/shirabe", keptMarketplaces)
	}

	// shirabe@shirabe (github marketplace) and the unqualified "bare" plugin
	// survive; tsukumogami@tsukumogami (repo: marketplace) is dropped.
	wantPlugins := map[string]bool{"shirabe@shirabe": true, "bare": true}
	if len(keptPlugins) != len(wantPlugins) {
		t.Fatalf("keptPlugins = %#v, want %v", keptPlugins, wantPlugins)
	}
	for _, p := range keptPlugins {
		if !wantPlugins[p] {
			t.Errorf("unexpected kept plugin %q", p)
		}
	}

	// Both exclusions are reported, no silent truncation.
	if len(reports) != 2 {
		t.Fatalf("want 2 exclusion reports (marketplace + plugin), got %d: %#v", len(reports), reports)
	}
	joined := strings.Join(reports, "\n")
	if !strings.Contains(joined, "repo:tools/.claude-plugin/marketplace.json") {
		t.Errorf("marketplace exclusion report missing the source; got:\n%s", joined)
	}
	if !strings.Contains(joined, "tsukumogami@tsukumogami") {
		t.Errorf("plugin exclusion report missing the plugin; got:\n%s", joined)
	}
}

func TestRootHoistableConfig_NoExclusions(t *testing.T) {
	// All github sources: nothing is excluded, so no reports are produced.
	plugins := []string{"shirabe@shirabe"}
	marketplaces := config.MarketplaceConfigs{{Source: "tsukumogami/shirabe"}}

	keptPlugins, keptMarketplaces, reports := rootHoistableConfig(plugins, marketplaces)
	if len(keptPlugins) != 1 || len(keptMarketplaces) != 1 {
		t.Errorf("expected all entries kept; plugins=%#v marketplaces=%#v", keptPlugins, keptMarketplaces)
	}
	if len(reports) != 0 {
		t.Errorf("expected no exclusion reports, got %#v", reports)
	}
}

func TestPluginMarketplace(t *testing.T) {
	cases := map[string]string{
		"shirabe@shirabe":         "shirabe",
		"tsukumogami@tsukumogami": "tsukumogami",
		"bare":                    "",
		"trailing@":               "",
		"a@b@c":                   "c",
	}
	for in, want := range cases {
		if got := pluginMarketplace(in); got != want {
			t.Errorf("pluginMarketplace(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaterializeWorkspaceRoot_RootFilesVerbatim(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Root:      config.RootConfig{Files: map[string]string{"mcp.json": ".mcp.json"}},
	}

	written, err := MaterializeWorkspaceRoot(cfg, root, RootMaterializeOptions{
		NiwaPath:  "/abs/niwa",
		ConfigDir: configDir,
	})
	if err != nil {
		t.Fatalf("MaterializeWorkspaceRoot: %v", err)
	}

	verbatim := filepath.Join(root, ".mcp.json")
	if _, statErr := os.Stat(verbatim); statErr != nil {
		t.Errorf("expected verbatim .mcp.json at workspace root: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".mcp.local.json")); statErr == nil {
		t.Error("workspace-root distribution must not insert a .local infix")
	}
	if !contains(written, verbatim) {
		t.Errorf("written set %v should include %s", written, verbatim)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestInstallWorkspaceRootSettings_InstanceFilesVerbatimTracked(t *testing.T) {
	tmp := t.TempDir()
	configDir := filepath.Join(tmp, ".niwa")
	instanceRoot := filepath.Join(tmp, "instance")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Instance:  config.InstanceConfig{Files: map[string]string{"mcp.json": ".mcp.json"}},
	}

	written, err := InstallWorkspaceRootSettings(cfg, configDir, instanceRoot, map[string]string{})
	if err != nil {
		t.Fatalf("InstallWorkspaceRootSettings: %v", err)
	}

	verbatim := filepath.Join(instanceRoot, ".mcp.json")
	if _, statErr := os.Stat(verbatim); statErr != nil {
		t.Errorf("expected verbatim .mcp.json at instance root: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(instanceRoot, ".mcp.local.json")); statErr == nil {
		t.Error("instance-root distribution must not insert a .local infix")
	}
	// The returned set feeds ManagedFiles, which drives drift + cleanRemovedFiles.
	// Tracking the path here is what makes removal-cleanup work on the next apply.
	if !contains(written, verbatim) {
		t.Errorf("written set %v should include %s (so it is tracked in ManagedFiles)", written, verbatim)
	}
}
