package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// Overlay [mcp.servers]: an overlay declares servers in the base table's shape,
// colliding names are the base config's, the survivors resolve against the
// overlay's own vault bundle, and after the merge they are ordinary entries of
// the table every generator reads.

// --- MergeWorkspaceOverlay ---

// TestMergeWorkspaceOverlay_MCPAddsOverlayOnlyServers checks the additive half
// of the merge: a server only the overlay declares joins the base's.
func TestMergeWorkspaceOverlay_MCPAddsOverlayOnlyServers(t *testing.T) {
	ws := baseWS()
	ws.MCP.Servers = map[string]config.MCPServerConfig{
		"docs": {URL: "https://docs.example.com"},
	}
	overlay := &config.WorkspaceOverlay{MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"example": {
			URL:     "https://mcp.example.com",
			Headers: map[string]config.MaybeSecret{"Authorization": {Plain: "Bearer literal"}},
		},
	}}}

	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	if got, want := merged.MCP.MCPServerNames(), []string{"docs", "example"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged servers = %v, want %v", got, want)
	}
	if got := merged.MCP.Servers["example"].Headers["Authorization"].Plain; got != "Bearer literal" {
		t.Errorf("overlay server header = %q, want the overlay's value", got)
	}
}

// TestMergeWorkspaceOverlay_MCPBaseWinsOnCollision is the merge-level rule the
// worktree path relies on: it calls the merge directly, with no pre-filter, so
// a colliding overlay server has to lose here too.
func TestMergeWorkspaceOverlay_MCPBaseWinsOnCollision(t *testing.T) {
	ws := baseWS()
	ws.MCP.Servers = map[string]config.MCPServerConfig{
		"example": {URL: "https://base.example.com"},
	}
	overlay := &config.WorkspaceOverlay{MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"example": {
			URL:     "https://overlay.example.com",
			Headers: map[string]config.MaybeSecret{"Authorization": {Plain: "vault://EXAMPLE_TOKEN"}},
		},
	}}}

	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	got := merged.MCP.Servers["example"]
	if got.URL != "https://base.example.com" {
		t.Errorf("url = %q, want the base config's", got.URL)
	}
	if len(got.Headers) != 0 {
		t.Errorf("the base server picked up the overlay's headers: %v", got.Headers)
	}
}

// TestMergeWorkspaceOverlay_MCPDoesNotAlias guards the copy: the resolver
// rewrites header and env values in place, so a merged map shared with either
// input would let a later pass write through into a config it does not own.
func TestMergeWorkspaceOverlay_MCPDoesNotAlias(t *testing.T) {
	ws := baseWS()
	ws.MCP.Servers = map[string]config.MCPServerConfig{
		"docs": {Command: "docs-mcp", Args: []string{"--stdio"}, Env: map[string]config.MaybeSecret{"K": {Plain: "base"}}},
	}
	overlay := &config.WorkspaceOverlay{MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"example": {URL: "https://mcp.example.com", Headers: map[string]config.MaybeSecret{"H": {Plain: "overlay"}}},
	}}}

	merged, err := MergeWorkspaceOverlay(ws, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	merged.MCP.Servers["docs"].Env["K"] = config.MaybeSecret{Plain: "changed"}
	merged.MCP.Servers["docs"].Args[0] = "changed"
	merged.MCP.Servers["example"].Headers["H"] = config.MaybeSecret{Plain: "changed"}
	merged.MCP.Servers["added"] = config.MCPServerConfig{URL: "https://added.example.com"}

	if got := ws.MCP.Servers["docs"].Env["K"].Plain; got != "base" {
		t.Errorf("base env value changed through the merged config: %q", got)
	}
	if got := ws.MCP.Servers["docs"].Args[0]; got != "--stdio" {
		t.Errorf("base args changed through the merged config: %q", got)
	}
	if got := overlay.MCP.Servers["example"].Headers["H"].Plain; got != "overlay" {
		t.Errorf("overlay header changed through the merged config: %q", got)
	}
	if _, leaked := ws.MCP.Servers["added"]; leaked {
		t.Error("a server added to the merged config appeared in the base config")
	}
}

// TestMergeWorkspaceOverlay_MCPOverlayOntoEmptyBase covers a base with no
// [mcp] table at all, where the merged map has to be created.
func TestMergeWorkspaceOverlay_MCPOverlayOntoEmptyBase(t *testing.T) {
	overlay := &config.WorkspaceOverlay{MCP: config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"example": {URL: "https://mcp.example.com"},
	}}}
	merged, err := MergeWorkspaceOverlay(baseWS(), overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	if got := merged.MCP.Servers["example"].URL; got != "https://mcp.example.com" {
		t.Errorf("overlay server not merged onto an empty base: %+v", merged.MCP)
	}
}

// TestDropShadowedOverlayMCPServers checks the pipeline's pre-resolution
// filter: colliding names leave the overlay, in name order, and the rest stay.
func TestDropShadowedOverlayMCPServers(t *testing.T) {
	base := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"zulu":  {URL: "https://z.example.com"},
		"alpha": {URL: "https://a.example.com"},
	}}
	overlay := config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"zulu":    {URL: "https://overlay-z.example.com"},
		"alpha":   {URL: "https://overlay-a.example.com"},
		"example": {URL: "https://mcp.example.com"},
	}}

	dropped := dropShadowedOverlayMCPServers(base, &overlay)
	if want := []string{"alpha", "zulu"}; !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
	if got, want := overlay.MCPServerNames(), []string{"example"}; !reflect.DeepEqual(got, want) {
		t.Errorf("overlay keeps %v, want %v", got, want)
	}

	none := config.MCPConfig{}
	if dropped := dropShadowedOverlayMCPServers(base, &none); dropped != nil {
		t.Errorf("an overlay with no servers dropped %v", dropped)
	}
}

// --- The apply pipeline ---

// overlayMCPRun is what one apply with an overlay produced.
type overlayMCPRun struct {
	instanceRoot string
	repoDir      string
	output       string
	err          error
}

// applyWithOverlay runs one Apply over a base config and an overlay, both
// given as TOML, against a vault registry carrying the fake backend. The
// base config is given without its [workspace] / [[sources]] preamble.
func applyWithOverlay(t *testing.T, baseBody, overlayTOML string) overlayMCPRun {
	t.Helper()
	t.Setenv("CODEX_HOME", "")

	configTOML := `
[workspace]
name = "overlay-mcp-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
` + baseBody
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})
	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	overlaySrc := setupOverlayDir(t, overlayTOML)

	if err := SaveState(instanceRoot, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "overlay-mcp-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/overlay-mcp-ws-overlay",
		OverlayCommit:  "abc123",
	}); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}
	applier.DeveloperHome = t.TempDir()
	applier.vaultRegistry = newFakeVaultRegistry(t)
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		data, err := os.ReadFile(filepath.Join(overlaySrc, "workspace-overlay.toml"))
		if err != nil {
			return false, 0, err
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	applier.headSHA = func(string) (string, error) { return "abc123", nil }
	var out syncBuffer
	applier.Reporter = NewReporterWithTTY(&out, false)

	applyErr := applier.Apply(context.Background(), loaded.Config, niwaDir, instanceRoot)
	return overlayMCPRun{
		instanceRoot: instanceRoot,
		repoDir:      filepath.Join(instanceRoot, "all", "repo1"),
		output:       permanentOutput(out.String()),
		err:          applyErr,
	}
}

// generatedMCPJSON reads the instance root's .mcp.json, returning the raw
// bytes (for order checks) and its servers.
func generatedMCPJSON(t *testing.T, instanceRoot string) (string, map[string]map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(instanceRoot, ".mcp.json"))
	if err != nil {
		t.Fatalf("reading .mcp.json: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing .mcp.json: %v\n%s", err, data)
	}
	return string(data), doc.MCPServers
}

// generatedCodexConfig reads a repo's generated Codex config, returning the raw
// text and its [mcp_servers] table.
func generatedCodexConfig(t *testing.T, repoDir string) (string, map[string]map[string]any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("reading the generated Codex config: %v", err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		t.Fatalf("parsing the generated Codex config: %v\n%s", err, data)
	}
	return string(data), doc.MCPServers
}

// assertInOrder fails unless every needle appears in haystack, each after the
// one before it.
func assertInOrder(t *testing.T, what, haystack string, needles ...string) {
	t.Helper()
	last := -1
	for _, n := range needles {
		i := strings.Index(haystack, n)
		if i < 0 {
			t.Errorf("%s does not contain %q:\n%s", what, n, haystack)
			return
		}
		if i < last {
			t.Errorf("%s has %q out of name order:\n%s", what, n, haystack)
			return
		}
		last = i
	}
}

// TestApplyOverlayMCPServerResolvesAgainstOverlayVault is the main path: an
// overlay-only http server whose header is a vault:// reference reaches the
// instance root's .mcp.json and the repo's Codex config with the value the
// overlay's provider holds, sorted by name among the base config's servers.
func TestApplyOverlayMCPServerResolvesAgainstOverlayVault(t *testing.T) {
	const secretValue = "overlay-mcp-resolved-token"

	run := applyWithOverlay(t, `
[mcp.servers.docs]
url = "https://docs.example.com"
`, `
[vault.provider]
kind = "fake"

[vault.provider.values]
EXAMPLE_TOKEN = "`+secretValue+`"

[mcp.servers.example]
url = "https://mcp.example.com"

[mcp.servers.example.headers]
Authorization = "vault://EXAMPLE_TOKEN"

[mcp.servers.analytics]
url = "https://analytics.example.com"
`)
	if run.err != nil {
		t.Fatalf("Apply: %v", run.err)
	}

	raw, servers := generatedMCPJSON(t, run.instanceRoot)
	example, ok := servers["example"]
	if !ok {
		t.Fatalf(".mcp.json has no overlay server:\n%s", raw)
	}
	if example["url"] != "https://mcp.example.com" {
		t.Errorf("overlay server url = %v", example["url"])
	}
	headers, _ := example["headers"].(map[string]any)
	if headers["Authorization"] != secretValue {
		t.Errorf("overlay header = %v, want the value the overlay's provider holds:\n%s", headers["Authorization"], raw)
	}
	assertInOrder(t, ".mcp.json", raw, `"analytics"`, `"docs"`, `"example"`)

	codexRaw, codexServers := generatedCodexConfig(t, run.repoDir)
	codexExample, ok := codexServers["example"]
	if !ok {
		t.Fatalf("the Codex config has no [mcp_servers.example]:\n%s", codexRaw)
	}
	codexHeaders, _ := codexExample["http_headers"].(map[string]any)
	if codexHeaders == nil {
		codexHeaders, _ = codexExample["headers"].(map[string]any)
	}
	if codexHeaders["Authorization"] != secretValue {
		t.Errorf("Codex overlay header = %v, want the resolved value:\n%s", codexExample, codexRaw)
	}
	assertInOrder(t, "the Codex config", codexRaw,
		"[mcp_servers.analytics]", "[mcp_servers.docs]", "[mcp_servers.example]")

	settings, err := os.ReadFile(filepath.Join(run.instanceRoot, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading the instance root's settings.json: %v", err)
	}
	approved, present := approvedServersIn(t, settings)
	if want := []string{"analytics", "docs", "example"}; !present || !reflect.DeepEqual(approved, want) {
		t.Errorf("approved servers = %v (present %v), want %v, overlay servers sorted among the base config's:\n%s",
			approved, present, want, settings)
	}
}

// TestApplyOverlayMCPCollisionDropsBeforeResolving pins the pre-resolution
// filter. The overlay's provider fails outright on any key it does not hold,
// and the colliding entry's header names such a key -- so resolving the
// dropped entry would fail the apply. It must not be resolved, the base url
// must be what is generated, and a warning must name the server.
func TestApplyOverlayMCPCollisionDropsBeforeResolving(t *testing.T) {
	run := applyWithOverlay(t, `
[mcp.servers.example]
url = "https://base.example.com"
`, `
[vault.provider]
kind = "fake"
fail_plain = true

[mcp.servers.example]
url = "https://overlay.example.com"

[mcp.servers.example.headers]
Authorization = "vault://KEY_THE_PROVIDER_LACKS"
`)
	if run.err != nil {
		t.Fatalf("Apply failed, so the dropped server's vault reference was resolved: %v", run.err)
	}

	want := `workspace overlay declares MCP server "example", which the workspace config already declares; the workspace config's declaration is used`
	if !strings.Contains(run.output, want) {
		t.Errorf("no warning named the dropped server; want %q in:\n%s", want, run.output)
	}

	raw, servers := generatedMCPJSON(t, run.instanceRoot)
	if got := servers["example"]["url"]; got != "https://base.example.com" {
		t.Errorf(".mcp.json url = %v, want the base config's:\n%s", got, raw)
	}
	if strings.Contains(raw, "overlay.example.com") {
		t.Errorf(".mcp.json carries the dropped overlay entry:\n%s", raw)
	}
	codexRaw, codexServers := generatedCodexConfig(t, run.repoDir)
	if got := codexServers["example"]["url"]; got != "https://base.example.com" {
		t.Errorf("Codex url = %v, want the base config's:\n%s", got, codexRaw)
	}
}

// TestApplyOverlayMCPServerWithoutTransportFailsLikeABaseOne checks that an
// overlay server is validated by the same code a base server is: missing both
// url and command fails with identical text either way.
func TestApplyOverlayMCPServerWithoutTransportFailsLikeABaseOne(t *testing.T) {
	broken := `
[mcp.servers.example]
args = ["--verbose"]
`
	baseRun := applyWithOverlay(t, broken, "")
	if baseRun.err == nil {
		t.Fatal("fixture is wrong: a base server with neither url nor command was accepted")
	}
	overlayRun := applyWithOverlay(t, "", broken)
	if overlayRun.err == nil {
		t.Fatal("an overlay server with neither url nor command was accepted")
	}
	if overlayRun.err.Error() != baseRun.err.Error() {
		t.Errorf("overlay error differs from the base one:\noverlay: %v\nbase:    %v", overlayRun.err, baseRun.err)
	}
	if !strings.Contains(overlayRun.err.Error(), `mcp server "example" declares neither command nor url`) {
		t.Errorf("unexpected error text: %v", overlayRun.err)
	}
}

// TestApplyOverlayMCPRefToBaseOnlyProviderFails covers per-layer isolation: an
// overlay server cannot borrow a provider the base config declares. The
// reference fails with the provider-not-declared error the base config's own
// parse gives, instead of resolving against a bundle the overlay did not name.
func TestApplyOverlayMCPRefToBaseOnlyProviderFails(t *testing.T) {
	run := applyWithOverlay(t, `
[vault.providers.team]
kind = "fake"

[vault.providers.team.values]
EXAMPLE_TOKEN = "base-only-value"
`, `
[mcp.servers.example]
url = "https://mcp.example.com"

[mcp.servers.example.headers]
Authorization = "vault://team/EXAMPLE_TOKEN"
`)
	if run.err == nil {
		t.Fatal("an overlay reference to a provider only the base declares was accepted")
	}
	want := `mcp.servers.example.headers.Authorization references "vault://team/EXAMPLE_TOKEN" but the config declares no [vault] providers`
	if !strings.Contains(run.err.Error(), want) {
		t.Errorf("error = %v\nwant it to contain %q", run.err, want)
	}
	if strings.Contains(run.err.Error(), "base-only-value") {
		t.Errorf("the error carries the base provider's value: %v", run.err)
	}
}
