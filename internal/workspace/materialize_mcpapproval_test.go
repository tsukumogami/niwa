package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// These tests cover the approval half of a declared MCP server: the
// enabledMcpjsonServers list the instance root's settings.json carries for the
// servers niwa generated into that tree's .mcp.json.
//
// The value's shape is load-bearing. Claude Code validates settings.json
// against its schema, and a value that is not an array of strings makes it
// reject the whole file -- which silently drops the approval along with every
// other key. So the materialized assertions decode the written bytes the way a
// strict reader would rather than reading the in-memory map back.

// enableAllProjectMcpServersKey is the wholesale approval niwa must never
// write. It is spelled here only so the tests can assert its absence.
const enableAllProjectMcpServersKey = "enableAllProjectMcpServers"

// mcpServerDecl is a minimal stdio declaration for name.
func mcpServerDecl(name string) config.MCPServerConfig {
	return config.MCPServerConfig{Command: name + "-mcp"}
}

// approvalWorkspace builds a workspace config declaring the named servers.
func approvalWorkspace(names ...string) *config.WorkspaceConfig {
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws"}}
	if len(names) > 0 {
		cfg.MCP.Servers = map[string]config.MCPServerConfig{}
		for _, n := range names {
			cfg.MCP.Servers[n] = mcpServerDecl(n)
		}
	}
	return cfg
}

// materializeInstanceRootSettings runs the instance-root settings materializer
// over cfg and returns the written settings.json bytes.
func materializeInstanceRootSettings(t *testing.T, cfg *config.WorkspaceConfig, configDir, instanceRoot string) []byte {
	t.Helper()
	if _, err := (&RootSettingsMaterializer{}).Materialize(&MaterializeContext{
		Config:    cfg,
		ConfigDir: configDir,
		RepoDir:   instanceRoot,
		RepoIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("RootSettingsMaterializer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(instanceRoot, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	return data
}

// newApprovalDirs returns a config dir and an instance root under a scratch
// directory.
func newApprovalDirs(t *testing.T) (configDir, instanceRoot string) {
	t.Helper()
	tmp := t.TempDir()
	configDir = filepath.Join(tmp, ".niwa")
	instanceRoot = filepath.Join(tmp, "instance")
	for _, d := range []string{configDir, instanceRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return configDir, instanceRoot
}

// approvedServersIn validates a written settings document the way Claude Code
// would for this key and returns the approved names. The document must parse
// as a JSON object; the key, when present, must be an array whose every
// element is a string. present reports whether the key exists at all.
func approvedServersIn(t *testing.T, data []byte) (names []string, present bool) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("settings.json is not a JSON object: %v\n%s", err, data)
	}
	if _, ok := doc[enableAllProjectMcpServersKey]; ok {
		t.Errorf("settings.json carries %s, which niwa must never write:\n%s", enableAllProjectMcpServersKey, data)
	}
	raw, ok := doc[config.EnabledMcpjsonServersKey]
	if !ok {
		return nil, false
	}
	var elems []any
	if err := json.Unmarshal(raw, &elems); err != nil {
		t.Fatalf("%s is not a JSON array: %s", config.EnabledMcpjsonServersKey, raw)
	}
	names = make([]string, 0, len(elems))
	for i, e := range elems {
		s, isString := e.(string)
		if !isString {
			t.Fatalf("%s[%d] = %v (%T), want a string", config.EnabledMcpjsonServersKey, i, e, e)
		}
		names = append(names, s)
	}
	return names, true
}

func TestEnabledMcpjsonServersKeySpelling(t *testing.T) {
	if config.EnabledMcpjsonServersKey != "enabledMcpjsonServers" {
		t.Fatalf("EnabledMcpjsonServersKey = %q, want the spelling Claude Code reads", config.EnabledMcpjsonServersKey)
	}
}

func TestBuildSettingsDoc_ApprovedMCPServers(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		want  []string
		write bool
	}{
		{"sorted and de-duplicated", []string{"zeta", "example", "zeta", ""}, []string{"example", "zeta"}, true},
		{"none", nil, nil, false},
		{"only empty names", []string{""}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := buildSettingsDoc(BuildSettingsConfig{ApprovedMCPServers: tc.in})
			if err != nil {
				t.Fatalf("buildSettingsDoc: %v", err)
			}
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, present := approvedServersIn(t, data)
			if present != tc.write {
				t.Fatalf("%s present = %v, want %v:\n%s", config.EnabledMcpjsonServersKey, present, tc.write, data)
			}
			if tc.write && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s = %v, want %v", config.EnabledMcpjsonServersKey, got, tc.want)
			}
		})
	}
}

// TestBuildSettingsDoc_ApprovedMCPServersMergeWithOtherKeys checks the list
// lands beside everything else the settings builder writes instead of
// replacing or being replaced by any of it.
func TestBuildSettingsDoc_ApprovedMCPServersMergeWithOtherKeys(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings: config.SettingsConfig{
			"permissions":                    {Plain: "ask"},
			config.RemoteControlAtStartupKey: {Plain: "true"},
		},
		InstalledHooks: map[string][]InstalledHookEntry{
			"pre_tool_use": {{Matcher: "Bash", Paths: []string{"/instance/.claude/hooks/pre_tool_use/gate.sh"}}},
		},
		ResolvedEnvVars:    map[string]string{"EXAMPLE_VAR": "1"},
		Plugins:            []string{"example@example"},
		UseAbsolutePaths:   true,
		ApprovedMCPServers: []string{"example"},
	})
	if err != nil {
		t.Fatalf("buildSettingsDoc: %v", err)
	}
	for _, key := range []string{"permissions", config.RemoteControlAtStartupKey, "hooks", "env", "enabledPlugins", config.EnabledMcpjsonServersKey} {
		if _, ok := doc[key]; !ok {
			t.Errorf("settings document is missing %q: %v", key, doc)
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, _ := approvedServersIn(t, data); !reflect.DeepEqual(got, []string{"example"}) {
		t.Errorf("%s = %v, want [example]", config.EnabledMcpjsonServersKey, got)
	}
}

func TestRootSettingsMaterializer_ApprovesDeclaredServers(t *testing.T) {
	configDir, instanceRoot := newApprovalDirs(t)
	cfg := approvalWorkspace("zeta", "example")
	// A server scoped away from Claude is not in Claude's .mcp.json, so it
	// must not be approved there either.
	cfg.MCP.Servers["codex-only"] = config.MCPServerConfig{Command: "codex-mcp", Agents: []string{"codex"}}
	cfg.Claude.Settings = config.SettingsConfig{
		"permissions":                    {Plain: "ask"},
		config.RemoteControlAtStartupKey: {Plain: "true"},
	}

	data := materializeInstanceRootSettings(t, cfg, configDir, instanceRoot)
	got, present := approvedServersIn(t, data)
	if !present {
		t.Fatalf("settings.json has no %s for declared servers:\n%s", config.EnabledMcpjsonServersKey, data)
	}
	if want := []string{"example", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", config.EnabledMcpjsonServersKey, got, want)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	perms, _ := doc["permissions"].(map[string]any)
	if perms["defaultMode"] != "default" {
		t.Errorf("permissions.defaultMode = %v, want default (the declared posture must survive the merge)", perms["defaultMode"])
	}
	if doc[config.RemoteControlAtStartupKey] != true {
		t.Errorf("%s = %v, want true", config.RemoteControlAtStartupKey, doc[config.RemoteControlAtStartupKey])
	}
	if doc["includeGitInstructions"] != false {
		t.Errorf("includeGitInstructions = %v, want false", doc["includeGitInstructions"])
	}
}

func TestRootSettingsMaterializer_NoServersNoKey(t *testing.T) {
	configDir, instanceRoot := newApprovalDirs(t)
	data := materializeInstanceRootSettings(t, approvalWorkspace(), configDir, instanceRoot)
	if _, present := approvedServersIn(t, data); present {
		t.Errorf("settings.json carries %s with no servers declared:\n%s", config.EnabledMcpjsonServersKey, data)
	}
}

// TestRootSettingsMaterializer_VerbatimMCPFileIsNotApproved keeps the verbatim
// route's behaviour: niwa does not parse a .mcp.json it copies, so it has no
// names to approve and approves none.
func TestRootSettingsMaterializer_VerbatimMCPFileIsNotApproved(t *testing.T) {
	configDir, instanceRoot := newApprovalDirs(t)
	if err := os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"mcpServers":{"example":{"command":"example-mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := approvalWorkspace()
	cfg.Instance.Files = map[string]string{"mcp.json": ".mcp.json"}

	data := materializeInstanceRootSettings(t, cfg, configDir, instanceRoot)
	if _, err := os.Stat(filepath.Join(instanceRoot, ".mcp.json")); err != nil {
		t.Fatalf("the verbatim .mcp.json was not distributed: %v", err)
	}
	if _, present := approvedServersIn(t, data); present {
		t.Errorf("settings.json approves servers from a verbatim .mcp.json:\n%s", data)
	}
}

func TestRootSettingsMaterializer_ClaudeGatedOffApprovesNothing(t *testing.T) {
	configDir, instanceRoot := newApprovalDirs(t)
	cfg := approvalWorkspace("example")
	off := false
	cfg.Claude.Enabled = &off

	data := materializeInstanceRootSettings(t, cfg, configDir, instanceRoot)
	if _, present := approvedServersIn(t, data); present {
		t.Errorf("settings.json approves servers for a workspace with Claude disabled:\n%s", data)
	}
}

// TestRootSettingsMaterializer_RemovalShrinksThenDropsTheKey re-applies over
// the same instance root, the way a later `niwa apply` does after the
// declaration changes.
func TestRootSettingsMaterializer_RemovalShrinksThenDropsTheKey(t *testing.T) {
	configDir, instanceRoot := newApprovalDirs(t)

	steps := []struct {
		declared []string
		want     []string
	}{
		{[]string{"example", "zeta"}, []string{"example", "zeta"}},
		{[]string{"example"}, []string{"example"}},
		{nil, nil},
	}
	for i, step := range steps {
		data := materializeInstanceRootSettings(t, approvalWorkspace(step.declared...), configDir, instanceRoot)
		got, present := approvedServersIn(t, data)
		if step.want == nil {
			if present {
				t.Errorf("apply %d: %s = %v, want the key removed:\n%s", i+1, config.EnabledMcpjsonServersKey, got, data)
			}
			continue
		}
		if !reflect.DeepEqual(got, step.want) {
			t.Errorf("apply %d: %s = %v, want %v", i+1, config.EnabledMcpjsonServersKey, got, step.want)
		}
	}
}
