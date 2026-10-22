package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Every dispatch test in this file gets its approved MCP servers from a
// workspace.toml declaration run through a real Applier.Create, so the list
// dispatch sends is the one the pipeline recorded in instance state. The
// tests that plant or damage state do it only after Create wrote it.

// mcpServersDecl declares zeta before example, so a test that sees
// ["example","zeta"] knows the list arrived sorted from the recorded state.
const mcpServersDecl = `
[mcp.servers.zeta]
command = "zeta-mcp"

[mcp.servers.example]
command = "example-mcp"
`

// mcpWarningNeedle is the start of the warning dispatch prints for a name it
// leaves out of the launch approval.
const mcpWarningNeedle = "niwa dispatch: warning: MCP server "

// launchedMCPApproval returns the enabledMcpjsonServers value of the single
// launch document, and whether it is there. It fails the test on more than
// one document, on a value that isn't an array of strings, and on any element
// carrying enableAllProjectMcpServers.
func launchedMCPApproval(t *testing.T, passthrough []string) ([]string, bool) {
	t.Helper()
	for _, el := range passthrough {
		if strings.Contains(el, "enableAllProjectMcpServers") {
			t.Fatalf("a launch element carries enableAllProjectMcpServers: %v", passthrough)
		}
	}
	docs := launchSettingsDocs(t, passthrough)
	if len(docs) > 1 {
		t.Fatalf("passthrough carries %d settings documents, want at most one: %v", len(docs), passthrough)
	}
	if len(docs) == 0 {
		return nil, false
	}
	raw, ok := docs[0][config.EnabledMcpjsonServersKey]
	if !ok {
		return nil, false
	}
	items, isList := raw.([]any)
	if !isList {
		t.Fatalf("%s = %#v, want a JSON array", config.EnabledMcpjsonServersKey, raw)
	}
	names := make([]string, 0, len(items))
	for _, it := range items {
		s, isString := it.(string)
		if !isString {
			t.Fatalf("%s holds %#v, want strings only", config.EnabledMcpjsonServersKey, it)
		}
		names = append(names, s)
	}
	return names, true
}

func requireLaunchedMCPApproval(t *testing.T, passthrough []string, want []string) {
	t.Helper()
	got, ok := launchedMCPApproval(t, passthrough)
	if !ok {
		t.Fatalf("launch carries no %s; want %v (passthrough %v)", config.EnabledMcpjsonServersKey, want, passthrough)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", config.EnabledMcpjsonServersKey, got, want)
	}
}

// setRecordedMCPServers replaces what Create recorded, for the tests that
// show dispatch reads the state and nothing else.
func setRecordedMCPServers(t *testing.T, instancePath string, names []string) {
	t.Helper()
	state, err := workspace.LoadState(instancePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.ApprovedMCPServers = names
	if err := workspace.SaveState(instancePath, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// requireRecordedMCPServers asserts what Create recorded, so a test's premise
// is checked before dispatch reads it.
func requireRecordedMCPServers(t *testing.T, instancePath string, want []string) {
	t.Helper()
	state, err := workspace.LoadState(instancePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !slices.Equal(state.ApprovedMCPServers, want) {
		t.Fatalf("recorded approved_mcp_servers = %v, want %v", state.ApprovedMCPServers, want)
	}
}

// dispatchDeclared runs one detached dispatch of a workspace declaring decl,
// with hostCfg as the machine config, and returns the launch passthrough with
// lineage's env entry stripped, and stderr.
func dispatchDeclared(t *testing.T, decl, hostCfg string, afterCreate func(t *testing.T, instancePath string)) ([]string, string) {
	t.Helper()
	root := setupDeclaredDispatchWorkspace(t, decl)
	chdir(t, root)
	setHostConfig(t, hostCfg)
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, afterCreate)
	var pass []string
	captureLaunchPassthrough(f, &pass)
	dispatchDetach = true

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	if f.launchCalled != 1 {
		t.Fatalf("launch called %d times, want 1", f.launchCalled)
	}
	return pass, stderr
}

// TestDispatch_MCPApproval_SendsRecordedList: a workspace declaring zeta and
// example launches with one settings document approving exactly
// ["example","zeta"], and prints no MCP warning.
func TestDispatch_MCPApproval_SendsRecordedList(t *testing.T) {
	pass, stderr := dispatchDeclared(t, mcpServersDecl, "", func(t *testing.T, instancePath string) {
		requireRecordedMCPServers(t, instancePath, []string{"example", "zeta"})
	})
	requireLaunchedMCPApproval(t, pass, []string{"example", "zeta"})
	if strings.Contains(stderr, mcpWarningNeedle) {
		t.Fatalf("no name was dropped, yet stderr warns:\n%s", stderr)
	}
}

// TestDispatch_MCPApproval_NoServers: no declared server sends no key, and
// with nothing else contributing no document at all.
func TestDispatch_MCPApproval_NoServers(t *testing.T) {
	pass, stderr := dispatchDeclared(t, "", "", nil)
	if slices.Contains(pass, "--settings") {
		t.Fatalf("no contributor added a key, yet the launch carries a settings document: %v", pass)
	}
	for _, el := range pass {
		if strings.Contains(el, config.EnabledMcpjsonServersKey) {
			t.Fatalf("launch element carries %s with no server declared: %v", config.EnabledMcpjsonServersKey, pass)
		}
	}
	if strings.Contains(stderr, mcpWarningNeedle) {
		t.Fatalf("stderr warns with no server declared:\n%s", stderr)
	}
}

// TestDispatch_MCPApproval_ReadsRecordedStateOnly: the launch list is what
// the instance state records. A name only the state carries -- the shape an
// overlay-declared server takes, since dispatch never sees the overlay's
// config -- is sent, and a hand-edited settings.json list changes nothing.
//
// The harness provisions from the base config alone, so the overlay case is
// pinned through the state it would produce; the pipeline recording overlay
// servers is pinned by the workspace package's own tests.
func TestDispatch_MCPApproval_ReadsRecordedStateOnly(t *testing.T) {
	t.Run("state-only name", func(t *testing.T) {
		pass, _ := dispatchDeclared(t, mcpServersDecl, "", func(t *testing.T, instancePath string) {
			requireRecordedMCPServers(t, instancePath, []string{"example", "zeta"})
			setRecordedMCPServers(t, instancePath, []string{"example", "overlay-only", "zeta"})
		})
		requireLaunchedMCPApproval(t, pass, []string{"example", "overlay-only", "zeta"})
	})
	t.Run("hand-edited settings.json", func(t *testing.T) {
		pass, _ := dispatchDeclared(t, mcpServersDecl, "", func(t *testing.T, instancePath string) {
			settingsPath := filepath.Join(instancePath, ".claude", "settings.json")
			raw, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatalf("reading generated settings: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parsing generated settings: %v", err)
			}
			if _, ok := doc[config.EnabledMcpjsonServersKey]; !ok {
				t.Fatalf("generated settings carry no %s; the tamper would prove nothing:\n%s", config.EnabledMcpjsonServersKey, raw)
			}
			doc[config.EnabledMcpjsonServersKey] = []string{"tampered"}
			edited, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(settingsPath, edited, 0o644); err != nil {
				t.Fatal(err)
			}
		})
		requireLaunchedMCPApproval(t, pass, []string{"example", "zeta"})
	})
}

// TestDispatch_MCPApproval_DropsBadNames: a name outside the pattern is left
// out with exactly one %q-escaped warning and the dispatch goes ahead; when
// it is the only name, no key goes in.
func TestDispatch_MCPApproval_DropsBadNames(t *testing.T) {
	wantWarning := `niwa dispatch: warning: MCP server "bad name" left out of the launch approval: name must match ^[A-Za-z0-9_-]{1,64}$` + "\n"

	t.Run("ok and bad name", func(t *testing.T) {
		pass, stderr := dispatchDeclared(t, "\n[mcp.servers.ok]\ncommand = \"ok-mcp\"\n\n[mcp.servers.\"bad name\"]\ncommand = \"bad-mcp\"\n", "", func(t *testing.T, instancePath string) {
			requireRecordedMCPServers(t, instancePath, []string{"bad name", "ok"})
		})
		requireLaunchedMCPApproval(t, pass, []string{"ok"})
		if got := strings.Count(stderr, mcpWarningNeedle); got != 1 {
			t.Fatalf("stderr has %d MCP warnings, want 1:\n%s", got, stderr)
		}
		if !strings.Contains(stderr, wantWarning) {
			t.Fatalf("stderr lacks %q:\n%s", wantWarning, stderr)
		}
	})
	t.Run("only bad name", func(t *testing.T) {
		pass, stderr := dispatchDeclared(t, "\n[mcp.servers.\"bad name\"]\ncommand = \"bad-mcp\"\n", "", nil)
		if _, ok := launchedMCPApproval(t, pass); ok {
			t.Fatalf("every name was dropped, yet the launch carries %s: %v", config.EnabledMcpjsonServersKey, pass)
		}
		if got := strings.Count(stderr, wantWarning); got != 1 {
			t.Fatalf("warning appears %d times, want once:\n%s", got, stderr)
		}
	})
	t.Run("control characters reach stderr escaped", func(t *testing.T) {
		_, stderr := dispatchDeclared(t, mcpServersDecl, "", func(t *testing.T, instancePath string) {
			setRecordedMCPServers(t, instancePath, []string{"example", "\x1b[31mred\x07"})
		})
		if strings.ContainsAny(stderr, "\x1b\x07") {
			t.Fatalf("a raw control byte reached stderr: %q", stderr)
		}
		if !strings.Contains(stderr, `MCP server "\x1b[31mred\a" left out`) {
			t.Fatalf("stderr lacks the escaped name: %q", stderr)
		}
	})
}

// TestDispatch_MCPApproval_EveryPermissionMode: the list doesn't depend on
// how the permission mode was reached, or whether there is one.
func TestDispatch_MCPApproval_EveryPermissionMode(t *testing.T) {
	want := []string{"example", "zeta"}
	type modeCase struct {
		name     string
		decl     string
		host     string
		explicit string
	}
	var cases []modeCase
	for _, m := range config.DispatchPermissionModes {
		cases = append(cases, modeCase{name: "host " + m, decl: mcpServersDecl, host: hostModeTOML(m)})
	}
	cases = append(cases,
		modeCase{name: "explicit flag", decl: mcpServersDecl, explicit: "acceptEdits"},
		modeCase{name: "bypass posture", decl: postureS1 + mcpServersDecl},
		modeCase{name: "ask posture", decl: postureS2 + mcpServersDecl},
		modeCase{name: "no mode", decl: mcpServersDecl},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDeclaredDispatchWorkspace(t, tc.decl)
			chdir(t, root)
			setHostConfig(t, tc.host)
			f := installDispatchFakes(t, root)
			provisionThroughCreate(t, f, nil)
			var pass []string
			captureLaunchPassthrough(f, &pass)
			dispatchDetach = true
			dispatchPermissionMode = tc.explicit

			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
			}
			requireLaunchedMCPApproval(t, pass, want)
		})
	}
}

// TestDispatch_MCPApproval_CoexistsInOneDocument: remote control, inbound
// acceptance, lineage and the MCP approval share the single settings document.
func TestDispatch_MCPApproval_CoexistsInOneDocument(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDeclaredDispatchWorkspace(t, mcpServersDecl)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch+"accept_session_messages_on_dispatch = true\n")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	var raw []string
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		raw = req.Passthrough
		return nil
	}
	dispatchDetach = true

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	requireLaunchedMCPApproval(t, raw, []string{"example", "zeta"})
	doc := launchSettingsDocs(t, raw)[0]
	if v, ok := doc[config.RemoteControlAtStartupKey]; !ok || v != true {
		t.Fatalf("document lacks %s true: %v", config.RemoteControlAtStartupKey, doc)
	}
	if v, ok := doc[config.CrossSessionInboundKey]; !ok || v != crossSessionInboundAccept {
		t.Fatalf("document lacks %s %q: %v", config.CrossSessionInboundKey, crossSessionInboundAccept, doc)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || env["OTEL_RESOURCE_ATTRIBUTES"] == nil {
		t.Fatalf("document lacks the lineage env entry: %v", doc)
	}
}

// TestDispatch_MCPApproval_CodexCarriesNoKey: Codex has no settings flag, so
// its launch carries no approval and the dispatch says nothing about it.
func TestDispatch_MCPApproval_CodexCarriesNoKey(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, mcpServersDecl)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	var raw []string
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		raw = req.Passthrough
		return nil
	}
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	if f.launchCalled != 1 {
		t.Fatalf("launch called %d times, want 1", f.launchCalled)
	}
	for _, el := range raw {
		if strings.Contains(el, config.EnabledMcpjsonServersKey) {
			t.Fatalf("a Codex launch carries %s: %v", config.EnabledMcpjsonServersKey, raw)
		}
	}
	if strings.Contains(stderr, mcpWarningNeedle) {
		t.Fatalf("a Codex dispatch warns about MCP servers:\n%s", stderr)
	}
}

// TestDispatch_MCPApproval_UnreadableStateWarns: when the state Create wrote
// can't be read, a settings-capable launch carries no approval and stderr
// names the state file, even with an explicit permission mode that silences
// the posture warning.
func TestDispatch_MCPApproval_UnreadableStateWarns(t *testing.T) {
	var statePath string
	root := setupDeclaredDispatchWorkspace(t, mcpServersDecl)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, func(t *testing.T, instancePath string) {
		requireRecordedMCPServers(t, instancePath, []string{"example", "zeta"})
		statePath = statePathOf(instancePath)
		if err := os.WriteFile(statePath, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	var pass []string
	captureLaunchPassthrough(f, &pass)
	dispatchDetach = true
	dispatchPermissionMode = "acceptEdits"

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	if _, ok := launchedMCPApproval(t, pass); ok {
		t.Fatalf("an unreadable state still sent %s: %v", config.EnabledMcpjsonServersKey, pass)
	}
	want := "niwa dispatch: warning: could not read " + statePath
	if !strings.Contains(stderr, want) || !strings.Contains(stderr, "MCP servers") {
		t.Fatalf("stderr lacks the MCP warning naming %s:\n%s", statePath, stderr)
	}
}

// TestWatchLaunches_CarryNoMCPApproval: niwa watch review launches build their
// own passthrough and never reach step (9c), so neither carries the key.
func TestWatchLaunches_CarryNoMCPApproval(t *testing.T) {
	for _, sandbox := range []bool{false, true} {
		for name, req := range map[string]launchRequest{
			"review": watchReviewLaunch("/tmp/inst", "slug", "review this", sandbox, launchEnvShape{}),
			"resume": watchResumeLaunch("/tmp/inst", "handle", "11111111-1111-4111-8111-111111111111", "continue", sandbox, launchEnvShape{}),
		} {
			for _, el := range req.Passthrough {
				if strings.Contains(el, config.EnabledMcpjsonServersKey) || strings.Contains(el, "enableAllProjectMcpServers") {
					t.Fatalf("%s launch (sandbox %v) carries an MCP approval: %v", name, sandbox, req.Passthrough)
				}
			}
		}
	}
}

func TestFilterMCPServerNames(t *testing.T) {
	long := strings.Repeat("a", 64)
	cases := []struct {
		name        string
		in          []string
		kept, dropd []string
	}{
		{"nil", nil, nil, nil},
		{"all valid", []string{"example", "my_server-2", long}, []string{"example", "my_server-2", long}, nil},
		{"space", []string{"ok", "bad name"}, []string{"ok"}, []string{"bad name"}},
		{"empty", []string{""}, nil, []string{""}},
		{"too long", []string{long + "a"}, nil, []string{long + "a"}},
		{"quote and dot", []string{`a"b`, "a.b", "a/b"}, nil, []string{`a"b`, "a.b", "a/b"}},
		{"control and unicode", []string{"a\nb", "caf\u00e9"}, nil, []string{"a\nb", "caf\u00e9"}},
		{"order kept", []string{"z", "!", "a"}, []string{"z", "a"}, []string{"!"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := filterMCPServerNames(tc.in)
			if !slices.Equal(kept, tc.kept) || !slices.Equal(dropped, tc.dropd) {
				t.Fatalf("filterMCPServerNames(%q) = (%q, %q), want (%q, %q)", tc.in, kept, dropped, tc.kept, tc.dropd)
			}
		})
	}

	t.Run("cap", func(t *testing.T) {
		var in []string
		for i := 0; i < maxLaunchMCPServers+3; i++ {
			in = append(in, "s"+strings.Repeat("x", i%10)+string(rune('a'+i%26)))
		}
		kept, dropped := filterMCPServerNames(in)
		if len(kept) != maxLaunchMCPServers || len(dropped) != 3 {
			t.Fatalf("kept %d, dropped %d; want %d and 3", len(kept), len(dropped), maxLaunchMCPServers)
		}
		if !slices.Equal(kept, in[:maxLaunchMCPServers]) || !slices.Equal(dropped, in[maxLaunchMCPServers:]) {
			t.Fatal("the cap must keep the first names in order and drop the rest")
		}
	})
}

func TestMCPApprovalListMarshal(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"nil", nil, `[]`},
		{"names", []string{"example", "zeta"}, `["example","zeta"]`},
		// The type holds whatever it is given; filtering is the caller's.
		// Even so, a name with JSON metacharacters stays one string.
		{"metacharacters", []string{`a","enableAllProjectMcpServers":true,"b`}, `["a\",\"enableAllProjectMcpServers\":true,\"b"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(mcpApprovalList{names: tc.in})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("marshal = %s, want %s", got, tc.want)
			}
			var back []string
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatalf("output is not a JSON array of strings: %v", err)
			}
		})
	}

	// Inside a rendered document it is one key holding an array.
	doc, ok := renderLaunchSettings(map[string]any{config.EnabledMcpjsonServersKey: mcpApprovalList{names: []string{"example"}}})
	if !ok || doc != `{"enabledMcpjsonServers":["example"]}` {
		t.Fatalf("renderLaunchSettings = (%s, %v)", doc, ok)
	}
}

func TestQuoteDroppedName(t *testing.T) {
	if got := quoteDroppedName("bad name"); got != `"bad name"` {
		t.Fatalf("quoteDroppedName = %s", got)
	}
	for _, name := range []string{strings.Repeat("x", 500), strings.Repeat("\x1b", 500), strings.Repeat("\u00e9", 200)} {
		got := quoteDroppedName(name)
		if len(got) > maxWarnedNameBytes {
			t.Fatalf("quoted %d bytes, want at most %d: %s", len(got), maxWarnedNameBytes, got)
		}
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"...`) {
			t.Fatalf("a cut name must read as a quoted prefix and an ellipsis: %s", got)
		}
		if strings.ContainsAny(got, "\x1b") {
			t.Fatalf("raw control byte in %q", got)
		}
	}
}
