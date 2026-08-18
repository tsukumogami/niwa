package agentplan

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/agent"
)

// The MCP suite is where the four measured Codex constraints become
// assertions. Every one of them is about something that must NOT reach disk, so
// the tests read the returned plan rather than a directory: a producer that
// declares no entry has written nothing, which is the strongest form of "no
// partial file" available.

// stdioServer is the ordinary declaration the happy-path cases use.
func stdioServer() MCPServer {
	return MCPServer{
		Name:    "files",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Env:     map[string]string{"ROOT": "/srv"},
	}
}

func httpServer() MCPServer {
	return MCPServer{
		Name:    "search",
		URL:     "https://mcp.example.test/v1",
		Headers: map[string]string{"Authorization": "Bearer abc123"},
	}
}

// codexPlan produces the Codex plan for one repository, failing the test on an
// unexpected error.
func codexPlan(t *testing.T, in PayloadInputs) *Plan {
	t.Helper()
	in.Scope = PayloadInRepo
	if in.Dir == "" {
		in.Dir = "/repo"
	}
	plan, err := For(agent.AgentCodex).PayloadPlan(in)
	if err != nil {
		t.Fatalf("PayloadPlan: %v", err)
	}
	return plan
}

// TestClaudeGenerationMatchesTheVerbatimRoute is the equivalence the
// compatibility path turns on: what the structured declaration generates for
// Claude is the document a workspace would have distributed verbatim for the
// same servers. The fixture is written the way a hand-authored .mcp.json is
// written, and the comparison is over the decoded documents, so an ordering or
// whitespace difference does not read as a behavior difference -- and a missing
// key, an extra key, or a changed value does.
func TestClaudeGenerationMatchesTheVerbatimRoute(t *testing.T) {
	const verbatim = `{
  "mcpServers": {
    "files": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem"],
      "env": {"ROOT": "/srv"}
    },
    "search": {
      "type": "http",
      "url": "https://mcp.example.test/v1",
      "headers": {"Authorization": "Bearer abc123"}
    }
  }
}`

	plan, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{
		Scope:   PayloadAtInstanceRoot,
		Dir:     "/instance",
		Servers: []MCPServer{httpServer(), stdioServer()},
	})
	if err != nil {
		t.Fatalf("PayloadPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}

	var got, want map[string]any
	if err := json.Unmarshal(plan.Entries[0].Content, &got); err != nil {
		t.Fatalf("decoding the generated document: %v", err)
	}
	if err := json.Unmarshal([]byte(verbatim), &want); err != nil {
		t.Fatalf("decoding the verbatim document: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the generated .mcp.json is not what the verbatim route would have distributed:\ngot  %#v\nwant %#v", got, want)
	}

	if suffix := "/instance/" + claudeMCPFileName; !strings.HasSuffix(plan.Entries[0].Path, suffix) {
		t.Errorf("generated document path is %q, want it to end in %q", plan.Entries[0].Path, suffix)
	}
}

// TestEachAgentTakesOneScope pins where each generated configuration lands. The
// placements are not symmetric and the asymmetry is the point: Claude reads a
// project document at the instance root, and Codex reads a project layer only
// from a project root downward, which an instance root is not.
func TestEachAgentTakesOneScope(t *testing.T) {
	servers := []MCPServer{stdioServer()}
	cases := []struct {
		agent     agent.Agent
		scope     PayloadScope
		wantEntry bool
	}{
		{agent.AgentClaude, PayloadAtInstanceRoot, true},
		{agent.AgentClaude, PayloadInRepo, false},
		{agent.AgentCodex, PayloadInRepo, true},
		{agent.AgentCodex, PayloadAtInstanceRoot, false},
	}
	for _, tc := range cases {
		plan, err := For(tc.agent).PayloadPlan(PayloadInputs{Scope: tc.scope, Dir: "/tree", Servers: servers})
		if err != nil {
			t.Fatalf("PayloadPlan(%s, scope %d): %v", tc.agent, tc.scope, err)
		}
		if got := len(plan.Entries) > 0; got != tc.wantEntry {
			t.Errorf("PayloadPlan(%s, scope %d) produced entries = %v, want %v", tc.agent, tc.scope, got, tc.wantEntry)
		}
	}
}

// TestCodexDocumentCarriesTheMeasuredSchema reads the generated document back
// the way Codex would: the marker line, then one [mcp_servers.<name>] table per
// server, with the transport implied by which of command and url is present.
func TestCodexDocumentCarriesTheMeasuredSchema(t *testing.T) {
	plan := codexPlan(t, PayloadInputs{Servers: []MCPServer{stdioServer(), httpServer()}})
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}
	content := string(plan.Entries[0].Content)

	if !strings.HasPrefix(content, generatedConfigMarker+"\n") {
		t.Errorf("the generated document does not open with the ownership marker:\n%s", content)
	}

	var doc struct {
		Servers map[string]map[string]any `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(content, &doc); err != nil {
		t.Fatalf("the generated document does not decode: %v\n%s", err, content)
	}

	files, ok := doc.Servers["files"]
	if !ok {
		t.Fatalf("the generated document defines no server %q", "files")
	}
	if files["command"] != "npx" {
		t.Errorf("files.command = %v, want npx", files["command"])
	}
	if _, wrong := files["transport"]; wrong {
		t.Error("the generated stdio entry carries a transport key; Codex infers the transport and has no such key")
	}
	env, ok := files["env"].(map[string]any)
	if !ok || env["ROOT"] != "/srv" {
		t.Errorf("files.env = %v, want ROOT=/srv", files["env"])
	}

	search, ok := doc.Servers["search"]
	if !ok {
		t.Fatalf("the generated document defines no server %q", "search")
	}
	if search["url"] != "https://mcp.example.test/v1" {
		t.Errorf("search.url = %v", search["url"])
	}
	headers, ok := search["http_headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer abc123" {
		t.Errorf("search.http_headers = %v, want the declared Authorization header", search["http_headers"])
	}
	if _, wrong := search["headers"]; wrong {
		t.Error("the generated http entry carries a headers key; Codex reads http_headers")
	}
}

// TestSSEIsAHardErrorForCodex is measured constraint 3. A declared SSE server is
// served as streamable HTTP without a word, which is a live protocol swap rather
// than a missing server -- so it fails, and the message carries the remedy.
func TestSSEIsAHardErrorForCodex(t *testing.T) {
	server := httpServer()
	server.Transport = MCPTransportSSE

	plan, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{Scope: PayloadInRepo, Dir: "/repo", Servers: []MCPServer{server}})
	if err == nil {
		t.Fatalf("a declared sse server produced a plan with %d entries; it must be an error", len(plan.Entries))
	}
	if plan != nil && len(plan.Entries) > 0 {
		t.Error("the failing plan carries entries; a validation failure must declare no write at all")
	}
	for _, want := range []string{server.Name, "sse", `agents = ["claude"]`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}

	// The same declaration is fine for the agent that implements it.
	claude, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{Scope: PayloadAtInstanceRoot, Dir: "/instance", Servers: []MCPServer{server}})
	if err != nil {
		t.Fatalf("the sse server is unavailable to Claude too: %v", err)
	}
	if !strings.Contains(string(claude.Entries[0].Content), `"type": "sse"`) {
		t.Errorf("the Claude document does not carry the sse type:\n%s", claude.Entries[0].Content)
	}
}

// TestScopingToOneAgentIsTheEscapeHatch checks the remedy the SSE error names
// actually works: a server scoped to Claude is generated for Claude and is not
// generated -- nor validated against Codex's schema -- for Codex.
func TestScopingToOneAgentIsTheEscapeHatch(t *testing.T) {
	server := httpServer()
	server.Transport = MCPTransportSSE
	server.Agents = []string{string(agent.AgentClaude)}

	plan, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{Scope: PayloadInRepo, Dir: "/repo", Servers: []MCPServer{server}})
	if err != nil {
		t.Fatalf("a Claude-scoped sse server still failed the Codex plan: %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("the Codex plan carries %d entries for a Claude-scoped server", len(plan.Entries))
	}

	claude, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{Scope: PayloadAtInstanceRoot, Dir: "/instance", Servers: []MCPServer{server}})
	if err != nil {
		t.Fatalf("PayloadPlan(claude): %v", err)
	}
	if len(claude.Entries) != 1 {
		t.Fatalf("the Claude plan carries %d entries, want 1", len(claude.Entries))
	}
}

// TestSurvivingInterpolationIsAnError is measured constraint 4, checked in
// every field a value can hide in and for both agents: one of them would expand
// the reference and the other would pass the characters through, so the same
// declaration would mean two different things.
func TestSurvivingInterpolationIsAnError(t *testing.T) {
	const named = "interpolating-server"
	cases := map[string]MCPServer{
		"command": {Name: named, Command: "${HOME}/bin/server"},
		"args":    {Name: named, Command: "server", Args: []string{"--root", "${HOME}"}},
		"env":     {Name: named, Command: "server", Env: map[string]string{"TOKEN": "${API_TOKEN}"}},
		"url":     {Name: named, URL: "https://${HOST}/mcp"},
		"headers": {Name: named, URL: "https://h/mcp", Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
	}
	for field, server := range cases {
		for _, ag := range agent.All() {
			scope := PayloadAtInstanceRoot
			if ag == agent.AgentCodex {
				scope = PayloadInRepo
			}
			plan, err := For(ag).PayloadPlan(PayloadInputs{Scope: scope, Dir: "/tree", Servers: []MCPServer{server}})
			if err == nil {
				t.Errorf("%s: an unresolved ${} in %s produced a plan for %s", field, field, ag)
				continue
			}
			if plan != nil && len(plan.Entries) > 0 {
				t.Errorf("%s: the failing plan for %s carries entries", field, ag)
			}
			if !strings.Contains(err.Error(), field) || !strings.Contains(err.Error(), named) {
				t.Errorf("%s: the error names neither the server nor the field: %v", field, err)
			}
		}
	}
}

// TestNameCollisionIsRefusedNotWritten is measured constraint 2. The layers
// merge field by field, so writing through a collision produces a server
// neither definition describes; the error names both sides because the fix is
// renaming one of them.
func TestNameCollisionIsRefusedNotWritten(t *testing.T) {
	_, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{
		Scope:    PayloadInRepo,
		Dir:      "/repo",
		Servers:  []MCPServer{stdioServer()},
		Existing: []string{"other", "files"},
	})
	if err == nil {
		t.Fatal("a colliding server name produced a plan; the merge would run a server neither definition describes")
	}
	for _, want := range []string{"files", codexMCPTable, "config.toml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the collision error does not mention %q: %v", want, err)
		}
	}

	// A name nobody else defines is written as usual.
	plan := codexPlan(t, PayloadInputs{Servers: []MCPServer{stdioServer()}, Existing: []string{"other"}})
	if len(plan.Entries) != 1 {
		t.Errorf("a non-colliding declaration produced %d entries, want 1", len(plan.Entries))
	}
}

// TestClaudeNamesDoNotCollide records the other half of the collision rule:
// Claude's document is a whole file niwa owns at a path of its own, so there is
// no developer-side layer for a generated name to merge with and nothing to
// read.
func TestClaudeNamesDoNotCollide(t *testing.T) {
	if spec := For(agent.AgentClaude).MCPCollisionSpec(); !spec.IsZero() {
		t.Errorf("the Claude producer asks for a collision read: %+v", spec)
	}
	spec := For(agent.AgentCodex).MCPCollisionSpec()
	if spec.IsZero() || spec.Table != codexMCPTable {
		t.Errorf("the Codex collision spec is %+v, want the %s table", spec, codexMCPTable)
	}
}

// TestCrossTransportFieldsAreRejected covers the misuse that a Codex config
// load fails whole on. Passing a field through "because the agent ignores what
// it does not understand" is exactly the assumption the measurement falsified.
func TestCrossTransportFieldsAreRejected(t *testing.T) {
	cases := map[string]MCPServer{
		"both command and url": {Name: "s", Command: "server", URL: "https://h/mcp"},
		"neither":              {Name: "s"},
		"headers on stdio":     {Name: "s", Command: "server", Headers: map[string]string{"X": "1"}},
		"args on http":         {Name: "s", URL: "https://h/mcp", Args: []string{"--flag"}},
		"env on http":          {Name: "s", URL: "https://h/mcp", Env: map[string]string{"K": "v"}},
		"stdio with a url":     {Name: "s", Transport: MCPTransportStdio, URL: "https://h/mcp"},
		"http with a command":  {Name: "s", Transport: MCPTransportHTTP, Command: "server"},
		"unknown transport":    {Name: "s", Transport: MCPTransport("grpc"), Command: "server"},
	}
	for name, server := range cases {
		plan, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{Scope: PayloadInRepo, Dir: "/repo", Servers: []MCPServer{server}})
		if err == nil {
			t.Errorf("%s: produced a plan with %d entries, want an error", name, len(plan.Entries))
			continue
		}
		if plan != nil && len(plan.Entries) > 0 {
			t.Errorf("%s: the failing plan carries entries", name)
		}
	}
}

// TestDuplicateAndEmptyNamesAreRejected keeps two declarations of one name, and
// a nameless one, from reaching a document where they would silently become one
// entry or an unaddressable one.
func TestDuplicateAndEmptyNamesAreRejected(t *testing.T) {
	dup := []MCPServer{stdioServer(), stdioServer()}
	if _, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{Scope: PayloadInRepo, Dir: "/repo", Servers: dup}); err == nil {
		t.Error("two declarations of one server name produced a plan")
	}
	empty := []MCPServer{{Command: "server"}}
	if _, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{Scope: PayloadInRepo, Dir: "/repo", Servers: empty}); err == nil {
		t.Error("a server with no name produced a plan")
	}
}

// TestGeneratedEntryCarriesItsBookkeeping asserts the properties that travel
// with the write rather than arriving later: the restrictive mode the resolved
// values require, the managed-file membership, the ownership gate, and the
// git-exclude pattern that keeps the file from making a working tree read
// dirty.
func TestGeneratedEntryCarriesItsBookkeeping(t *testing.T) {
	plan := codexPlan(t, PayloadInputs{Servers: []MCPServer{stdioServer()}})
	e := plan.Entries[0]

	if e.Capability != MCPServers {
		t.Errorf("entry capability = %s, want %s", e.Capability, MCPServers)
	}
	if e.Mode != payloadFileMode {
		t.Errorf("entry mode = %o, want %o: the declaration's env and headers values resolve through the vault pipeline", e.Mode, payloadFileMode)
	}
	if !e.Managed {
		t.Error("the generated configuration is not managed, so cleanup would never remove it")
	}
	if e.Pre != IfNotForeign || e.Owner != generatedConfigMarker {
		t.Errorf("entry gate = %d owner = %q, want the ownership rule", e.Pre, e.Owner)
	}
	// The directory rather than the file: everything niwa puts under that name
	// is generated, so a second generated file cannot arrive uncovered.
	if e.ExcludeAs != ".codex/" {
		t.Errorf("entry ExcludeAs = %q, want .codex/", e.ExcludeAs)
	}

	// The instance-root document is niwa's own directory, so it carries no
	// ownership gate and nothing to exclude.
	root, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{Scope: PayloadAtInstanceRoot, Dir: "/instance", Servers: []MCPServer{stdioServer()}})
	if err != nil {
		t.Fatalf("PayloadPlan(claude): %v", err)
	}
	if root.Entries[0].Pre != Always || root.Entries[0].ExcludeAs != "" {
		t.Errorf("the instance-root entry carries a gate (%d) or an exclude (%q) it does not need", root.Entries[0].Pre, root.Entries[0].ExcludeAs)
	}
}

// TestAForeignFileIsRefusedAndExempted keeps a repository that commits its own
// configuration at niwa's name: nothing is written, the refusal is reported,
// and the path is exempted so the cleanup pass does not delete the file the
// refusal just promised to leave alone.
func TestAForeignFileIsRefusedAndExempted(t *testing.T) {
	const owned = "/repo/.codex/config.toml"
	plan := codexPlan(t, PayloadInputs{
		Servers: []MCPServer{stdioServer()},
		Probe:   ContextProbe{Dir: "/repo", OwnedPath: owned, Foreign: true},
	})
	if len(plan.Entries) != 0 {
		t.Errorf("the plan carries %d entries for an occupied path", len(plan.Entries))
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], owned) {
		t.Errorf("the refusal is not reported with its path: %v", plan.Warnings)
	}
	if len(plan.Exempt) != 1 || plan.Exempt[0] != owned {
		t.Errorf("the refused path is not exempted from cleanup: %v", plan.Exempt)
	}
}

// TestProbeSpecAsksOnlyWhereTheTreeIsNotNiwas pins the probe seam: a producer
// asks about the path it would take in a tree it does not own, and asks nothing
// about the instance root.
func TestProbeSpecAsksOnlyWhereTheTreeIsNotNiwas(t *testing.T) {
	spec := For(agent.AgentCodex).PayloadProbeSpec(PayloadInRepo, "/repo")
	if spec.OwnedPath == "" || spec.OwnerMarker != generatedConfigMarker {
		t.Errorf("the Codex repo probe spec is %+v, want the generated-config marker", spec)
	}
	if spec.InlinePath != "" {
		t.Errorf("the probe spec asks to inline %q; a configuration document inlines nothing", spec.InlinePath)
	}
	if got := For(agent.AgentCodex).PayloadProbeSpec(PayloadAtInstanceRoot, "/instance"); got.OwnedPath != "" {
		t.Errorf("the Codex producer probes the instance root, where it writes nothing: %+v", got)
	}
	if got := For(agent.AgentClaude).PayloadProbeSpec(PayloadAtInstanceRoot, "/instance"); got.OwnedPath != "" {
		t.Errorf("the Claude producer probes its own instance root: %+v", got)
	}
}

// TestVerbatimReportNamesTheOneRouteItDescribes checks the compatibility
// report: it fires for a workspace distributing the file with nothing declared,
// and stays quiet once the declaration exists or when the destination is
// something else entirely.
func TestVerbatimReportNamesTheOneRouteItDescribes(t *testing.T) {
	claude := For(agent.AgentClaude)
	dests := []string{"workspace-context.md", ".mcp.json"}

	report := claude.MCPVerbatimReport(dests, false)
	if !strings.Contains(report, ".mcp.json") || !strings.Contains(report, "[mcp.servers") {
		t.Errorf("the report does not name the file and the declaration: %q", report)
	}
	if got := claude.MCPVerbatimReport(dests, true); got != "" {
		t.Errorf("the report fires for a workspace that declares its servers: %q", got)
	}
	if got := claude.MCPVerbatimReport([]string{"README.local.md"}, false); got != "" {
		t.Errorf("the report fires for a workspace distributing something else: %q", got)
	}
	if got := For(agent.AgentCodex).MCPVerbatimReport(dests, false); got != "" {
		t.Errorf("the Codex producer reports a route it has never had: %q", got)
	}
}

// TestGenerationIsDeterministic keeps the generated bytes from depending on map
// iteration order, which is what would make an apply rewrite an unchanged file
// and a managed-file hash move for no reason.
func TestGenerationIsDeterministic(t *testing.T) {
	servers := []MCPServer{
		{Name: "b", Command: "b", Env: map[string]string{"A": "1", "B": "2", "C": "3"}},
		{Name: "a", URL: "https://a/mcp", Headers: map[string]string{"X": "1", "Y": "2"}},
	}
	first := codexPlan(t, PayloadInputs{Servers: servers}).Entries[0].Content
	for i := 0; i < 8; i++ {
		next := codexPlan(t, PayloadInputs{Servers: servers}).Entries[0].Content
		if string(next) != string(first) {
			t.Fatalf("the generated document differs between runs:\n%s\n---\n%s", first, next)
		}
	}
}

// TestNoServersProducesNoDocument keeps an empty declaration from writing an
// empty configuration: a file with no servers in it is not the same thing as no
// file, and only one of the two is what a workspace that declares nothing asked
// for.
func TestNoServersProducesNoDocument(t *testing.T) {
	for _, ag := range agent.All() {
		for _, scope := range []PayloadScope{PayloadAtInstanceRoot, PayloadInRepo} {
			plan, err := For(ag).PayloadPlan(PayloadInputs{Scope: scope, Dir: "/tree"})
			if err != nil {
				t.Fatalf("PayloadPlan(%s, scope %d): %v", ag, scope, err)
			}
			if len(plan.Entries) != 0 {
				t.Errorf("PayloadPlan(%s, scope %d) wrote a document for a workspace declaring no servers", ag, scope)
			}
		}
	}
}

// TestGeneratedDocumentCheckCatchesADamagedOne exercises the last gate directly:
// the schema check runs over the decoded document, so a document that does not
// say what the producer meant is caught even if every input passed validation.
func TestGeneratedDocumentCheckCatchesADamagedOne(t *testing.T) {
	good := []byte(generatedConfigMarker + "\n\n[mcp_servers.files]\ncommand = \"npx\"\n")
	if err := checkCodexPayloadDocument(good, []MCPServer{{Name: "files", Command: "npx"}}, nil, SessionPosture{}); err != nil {
		t.Fatalf("a well-formed document was rejected: %v", err)
	}

	damaged := map[string]string{
		"no table":         generatedConfigMarker + "\n",
		"missing server":   "[mcp_servers.other]\ncommand = \"npx\"\n",
		"both transports":  "[mcp_servers.files]\ncommand = \"npx\"\nurl = \"https://h\"\n",
		"unknown key":      "[mcp_servers.files]\ncommand = \"npx\"\nstartup = 3\n",
		"wrong value type": "[mcp_servers.files]\ncommand = 3\n",
		"not valid toml":   "[mcp_servers.files\ncommand = \"npx\"\n",
	}
	for name, doc := range damaged {
		if err := checkCodexPayloadDocument([]byte(doc), []MCPServer{{Name: "files", Command: "npx"}}, nil, SessionPosture{}); err == nil {
			t.Errorf("%s: the check accepted a document it should not have", name)
		}
	}
}
