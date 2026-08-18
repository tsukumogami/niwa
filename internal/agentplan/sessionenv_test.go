package agentplan

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/agent"
)

// decodeCodexPayload decodes the one entry a Codex payload plan declares.
func decodeCodexPayload(t *testing.T, in PayloadInputs) map[string]any {
	t.Helper()
	if in.Scope == 0 {
		in.Scope = PayloadInRepo
	}
	if in.Dir == "" {
		in.Dir = "/repo"
	}
	plan, err := For(agent.AgentCodex).PayloadPlan(in)
	if err != nil {
		t.Fatalf("PayloadPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries, want 1", len(plan.Entries))
	}
	var doc map[string]any
	if _, err := toml.Decode(string(plan.Entries[0].Content), &doc); err != nil {
		t.Fatalf("the generated document is not valid TOML: %v", err)
	}
	return doc
}

// TestDeclaredEnvironmentReachesTheMeasuredRoute checks the one thing the whole
// delivery rests on: the values land under the key Codex actually reads them
// from, as literal strings, additive over whatever the session inherited.
func TestDeclaredEnvironmentReachesTheMeasuredRoute(t *testing.T) {
	doc := decodeCodexPayload(t, PayloadInputs{
		Env: map[string]string{"API_TOKEN": "abc", "REGION": "eu-west-1"},
	})

	policy, ok := doc["shell_environment_policy"].(map[string]any)
	if !ok {
		t.Fatalf("the generated document carries no shell_environment_policy table: %v", doc)
	}
	set, ok := policy["set"].(map[string]any)
	if !ok {
		t.Fatalf("shell_environment_policy carries no set table: %v", policy)
	}
	if set["API_TOKEN"] != "abc" || set["REGION"] != "eu-west-1" {
		t.Errorf("set = %v, want the declared values verbatim", set)
	}
}

// TestNiwaWritesNoIgnoreDefaultExcludes is the restraint the design asks for,
// mechanized rather than left to review.
//
// Codex's ignore_default_excludes defaults to true on the measured version, so
// its own *KEY* / *TOKEN* excludes are inactive and a session inherits those
// variables from the parent environment. That is Codex's behavior. Writing the
// key would change what a developer's own session inherits beyond anything they
// declared, and it would protect nothing niwa delivers, because set runs after
// exclude. So niwa writes set and nothing else -- asserted here as "no other
// key at all", which catches the next tempting addition as well as this one.
func TestNiwaWritesNoIgnoreDefaultExcludes(t *testing.T) {
	doc := decodeCodexPayload(t, PayloadInputs{Env: map[string]string{"API_TOKEN": "abc"}})

	policy, ok := doc["shell_environment_policy"].(map[string]any)
	if !ok {
		t.Fatalf("the generated document carries no shell_environment_policy table: %v", doc)
	}
	for key := range policy {
		if key != "set" {
			t.Errorf("shell_environment_policy carries %q; niwa writes only set, so the rest of a developer's environment policy stays theirs", key)
		}
	}
	if _, present := policy["ignore_default_excludes"]; present {
		t.Error("niwa wrote ignore_default_excludes, silently changing what a session inherits from the developer's own environment")
	}
}

// TestNoPolicyIsWrittenWhenNothingIsDeclared asserts the absent case directly.
// A policy table niwa wrote unasked would be niwa deciding what a session
// inherits, so "no declaration, no table" is checked rather than assumed.
func TestNoPolicyIsWrittenWhenNothingIsDeclared(t *testing.T) {
	doc := decodeCodexPayload(t, PayloadInputs{Servers: []MCPServer{stdioServer()}})
	if _, present := doc["shell_environment_policy"]; present {
		t.Errorf("a document was written with an environment policy although the workspace declared none: %v", doc)
	}
}

// TestServersAndEnvironmentShareOneDocument is why one producer method covers
// both declarations. Codex reads them out of a single project-layer file, so
// two plans writing the same path would have the second silently erase the
// first's table.
func TestServersAndEnvironmentShareOneDocument(t *testing.T) {
	in := PayloadInputs{
		Scope:   PayloadInRepo,
		Dir:     "/repo",
		Servers: []MCPServer{stdioServer()},
		Env:     map[string]string{"API_TOKEN": "abc"},
	}
	plan, err := For(agent.AgentCodex).PayloadPlan(in)
	if err != nil {
		t.Fatalf("PayloadPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries for one document, want 1", len(plan.Entries))
	}

	doc := decodeCodexPayload(t, in)
	if _, present := doc["mcp_servers"]; !present {
		t.Error("the document carrying an environment policy lost the declared servers")
	}
	if _, present := doc["shell_environment_policy"]; !present {
		t.Error("the document carrying declared servers lost the environment policy")
	}

	// The write is attributed to the capability that decides how it is
	// handled: resolved environment values are what make the file
	// secret-bearing.
	if plan.Entries[0].Capability != SessionEnvironment {
		t.Errorf("entry capability = %s, want %s", plan.Entries[0].Capability, SessionEnvironment)
	}
	if plan.Entries[0].Mode != payloadFileMode {
		t.Errorf("entry mode = %o, want %o", plan.Entries[0].Mode, payloadFileMode)
	}
}

// TestAgentWithNoPolicyRouteWritesNoEnvironmentHere keeps the neutral
// declaration from leaking into a document that does not carry it. Claude reads
// its environment from the settings document; its payload here is servers only.
func TestAgentWithNoPolicyRouteWritesNoEnvironmentHere(t *testing.T) {
	plan, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{
		Scope: PayloadAtInstanceRoot,
		Dir:   "/instance",
		Env:   map[string]string{"API_TOKEN": "abc"},
	})
	if err != nil {
		t.Fatalf("PayloadPlan(claude): %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("claude's payload declared %d entries for an environment it reads elsewhere", len(plan.Entries))
	}
}

// TestUnusableEnvironmentDeclarationsAreRefused covers the validation half:
// every failure produces an error and no entry, so a plan never half-lands.
func TestUnusableEnvironmentDeclarationsAreRefused(t *testing.T) {
	cases := map[string]map[string]string{
		"a name no shell can export":   {"not a name": "value"},
		"a name starting with a digit": {"9LIVES": "value"},
		"a value that never resolved":  {"API_TOKEN": "${VAULT_TOKEN}"},
	}
	for name, env := range cases {
		plan, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{
			Scope: PayloadInRepo,
			Dir:   "/repo",
			Env:   env,
		})
		if err == nil {
			t.Errorf("%s: produced a plan instead of an error", name)
			continue
		}
		if plan != nil && len(plan.Entries) > 0 {
			t.Errorf("%s: a failure still declared %d entries", name, len(plan.Entries))
		}
	}
}

// TestARefusedEnvironmentTakesTheWholeDocumentWithIt is the same-document
// consequence stated as a test. Codex fails a config load whole, so there is no
// "the servers still work" outcome to preserve: an environment niwa cannot
// express means no file at all, which leaves the session loading the developer's
// own layers rather than a broken one.
func TestARefusedEnvironmentTakesTheWholeDocumentWithIt(t *testing.T) {
	_, err := For(agent.AgentCodex).PayloadPlan(PayloadInputs{
		Scope:   PayloadInRepo,
		Dir:     "/repo",
		Servers: []MCPServer{stdioServer()},
		Env:     map[string]string{"API_TOKEN": "${VAULT_TOKEN}"},
	})
	if err == nil {
		t.Fatal("a document with a valid server and an unusable environment was declared anyway")
	}
	if !strings.Contains(err.Error(), "API_TOKEN") {
		t.Errorf("the error does not name the variable that caused it: %v", err)
	}
}

// TestGeneratedPolicyCheckCatchesADamagedOne exercises the decode-back gate
// directly, the way the server half is: the check runs over the bytes, so a
// document that does not say what the producer meant is caught even when every
// input passed validation.
func TestGeneratedPolicyCheckCatchesADamagedOne(t *testing.T) {
	env := map[string]string{"API_TOKEN": "abc"}

	good := map[string]any{
		"shell_environment_policy": map[string]any{"set": map[string]any{"API_TOKEN": "abc"}},
	}
	if err := checkCodexEnvPolicy(good, env); err != nil {
		t.Fatalf("a well-formed policy was rejected: %v", err)
	}

	damaged := map[string]map[string]any{
		"no table": {},
		"an extra key": {
			"shell_environment_policy": map[string]any{
				"set":                     map[string]any{"API_TOKEN": "abc"},
				"ignore_default_excludes": false,
			},
		},
		"a value of the wrong type": {
			"shell_environment_policy": map[string]any{"set": map[string]any{"API_TOKEN": int64(3)}},
		},
		"a value that did not survive": {
			"shell_environment_policy": map[string]any{"set": map[string]any{"API_TOKEN": "other"}},
		},
		"a variable that vanished": {
			"shell_environment_policy": map[string]any{"set": map[string]any{}},
		},
	}
	for name, doc := range damaged {
		if err := checkCodexEnvPolicy(doc, env); err == nil {
			t.Errorf("%s: the check accepted a document it should not have", name)
		}
	}

	// And the mirror case: a policy present when nothing was declared.
	if err := checkCodexEnvPolicy(good, nil); err == nil {
		t.Error("the check accepted a policy table for a workspace that declared no environment")
	}
}

// TestInstanceExcludePatternsCoverTheGeneratedNames asserts the patterns come
// from the same layout table the writes do, so a name that moves cannot leave
// its coverage behind.
func TestInstanceExcludePatternsCoverTheGeneratedNames(t *testing.T) {
	patterns := InstanceExcludePatterns()

	for _, ag := range agent.All() {
		layout, ok := For(ag).payloadLayout()
		if !ok {
			continue
		}
		want := layout.excludePattern()
		found := false
		for _, pattern := range patterns {
			if pattern == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s generates a configuration at %q with no instance-root pattern covering it: %v", ag, want, patterns)
		}
	}

	// Deduplicated: two agents naming the same file must not produce the
	// pattern twice, or the .gitignore grows a duplicate line on every apply.
	seen := map[string]bool{}
	for _, pattern := range patterns {
		if seen[pattern] {
			t.Errorf("pattern %q appears more than once in %v", pattern, patterns)
		}
		seen[pattern] = true
	}
}
