package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// These tests never read or write a real ~/.codex: every generated file lands
// in a scratch directory.

// generatedCodexPayload runs one install into a fresh tree and returns the
// bytes that landed on disk together with the decoded document. Reading the
// file back rather than inspecting the plan is the point of these tests: what a
// session loads is the file.
func generatedCodexPayload(t *testing.T, req PayloadRequest) (string, map[string]any) {
	t.Helper()
	repo := t.TempDir()
	req.Scope = agentplan.PayloadInRepo
	req.Dir = repo

	install, err := InstallPayloadConfig(req, agentplan.For(agent.AgentCodex))
	if err != nil {
		t.Fatalf("InstallPayloadConfig: %v", err)
	}
	path := filepath.Join(repo, ".codex", "config.toml")
	if len(install.Written) != 1 || install.Written[0] != path {
		t.Fatalf("wrote %v, want [%s]", install.Written, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the generated configuration: %v", err)
	}
	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		t.Fatalf("the generated configuration is not valid TOML: %v", err)
	}
	return string(data), doc
}

// TestTheGeneratedFileCarriesNoPostureWhenNoneIsDeclared asserts the
// absent-by-default property against the file itself.
//
// It is written this way on purpose. Every other test here would still pass if
// posture delivery quietly acquired a default, because they all declare one;
// this one declares servers and an environment, the way a workspace that never
// asked for a posture would, and then looks in the bytes that landed for the
// two keys. The developer's own Codex defaults are what a session runs under,
// and niwa is never the reason that changes.
func TestTheGeneratedFileCarriesNoPostureWhenNoneIsDeclared(t *testing.T) {
	data, doc := generatedCodexPayload(t, PayloadRequest{
		Servers: []agentplan.MCPServer{{Name: "files", Command: "npx"}},
		Env:     map[string]string{"REGION": "eu-west-1"},
	})

	if _, present := doc["approval_policy"]; present {
		t.Errorf("the generated file carries approval_policy although the workspace declared none:\n%s", data)
	}
	if _, present := doc["sandbox_mode"]; present {
		t.Errorf("the generated file carries sandbox_mode although the workspace declared none:\n%s", data)
	}
	// The names must not appear at all -- not commented, not as a key niwa
	// wrote empty. A key present in any form is a key Codex reads.
	if strings.Contains(data, "approval_policy") || strings.Contains(data, "sandbox_mode") {
		t.Errorf("the generated file mentions a posture key for a workspace that declared none:\n%s", data)
	}

	// And the apply reports no posture write.
	if report := agentplan.For(agent.AgentCodex).PostureReport(agentplan.SessionPosture{}); report != "" {
		t.Errorf("an apply with no declared posture reported one: %q", report)
	}
}

// TestTheGeneratedFileCarriesTheDeclaredPosture is the positive half: what the
// workspace declared reaches the file, in Codex's own spelling, beside
// everything else the document carries.
func TestTheGeneratedFileCarriesTheDeclaredPosture(t *testing.T) {
	data, doc := generatedCodexPayload(t, PayloadRequest{
		Servers: []agentplan.MCPServer{{Name: "files", Command: "npx"}},
		Posture: agentplan.SessionPosture{Approvals: "on-failure", Sandbox: "workspace-write"},
	})

	if got := doc["approval_policy"]; got != "on-failure" {
		t.Errorf("approval_policy = %v, want on-failure:\n%s", got, data)
	}
	if got := doc["sandbox_mode"]; got != "workspace-write" {
		t.Errorf("sandbox_mode = %v, want workspace-write:\n%s", got, data)
	}
	if _, present := doc["mcp_servers"]; !present {
		t.Errorf("the document carrying a posture lost the declared servers:\n%s", data)
	}
}

// TestTheGeneratedFileNeverDerivesASandboxFromApprovals is the third safety
// property at the file level. The declaration relaxes approvals as far as niwa
// accepts and says nothing about the sandbox; Codex's danger-full-access must
// appear nowhere in the bytes.
func TestTheGeneratedFileNeverDerivesASandboxFromApprovals(t *testing.T) {
	data, doc := generatedCodexPayload(t, PayloadRequest{
		Posture: agentplan.SessionPosture{Approvals: "never"},
	})

	if got := doc["approval_policy"]; got != "never" {
		t.Errorf("approval_policy = %v, want never:\n%s", got, data)
	}
	if _, present := doc["sandbox_mode"]; present {
		t.Errorf("relaxing approvals wrote a sandbox_mode into the file:\n%s", data)
	}
	if strings.Contains(data, "danger-full-access") {
		t.Errorf("the file names Codex's sandbox-disabling value for a declaration that never mentioned the sandbox:\n%s", data)
	}
}

// TestARefusedPostureLeavesNoFileBehind is validation-before-write for the
// posture keys, checked where it matters: on disk. One key Codex cannot
// type-check fails its whole config load, so a refused declaration has to leave
// nothing partial for a session to choke on.
func TestARefusedPostureLeavesNoFileBehind(t *testing.T) {
	repo := t.TempDir()
	install, err := InstallPayloadConfig(PayloadRequest{
		Scope:   agentplan.PayloadInRepo,
		Dir:     repo,
		Servers: []agentplan.MCPServer{{Name: "files", Command: "npx"}},
		Env:     map[string]string{"REGION": "eu-west-1"},
		Posture: agentplan.SessionPosture{Sandbox: "wide-open"},
	}, agentplan.For(agent.AgentCodex))
	if err == nil {
		t.Fatalf("a posture value from no vocabulary was accepted, writing %v", install.Written)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".codex", "config.toml")); !os.IsNotExist(statErr) {
		t.Errorf("a refused posture left a file behind (stat: %v)", statErr)
	}
}

// TestSessionPostureFromConfigSuppliesNoDefault pins the resolution step to
// carrying the declaration and nothing more. A default invented here would
// defeat the absent-by-default property no matter how carefully the producers
// behaved.
func TestSessionPostureFromConfigSuppliesNoDefault(t *testing.T) {
	if got := SessionPostureFromConfig(nil); !got.IsZero() {
		t.Errorf("a nil configuration produced posture %+v, want none", got)
	}
	if got := SessionPostureFromConfig(&config.WorkspaceConfig{}); !got.IsZero() {
		t.Errorf("a configuration declaring nothing produced posture %+v, want none", got)
	}

	declared := SessionPostureFromConfig(&config.WorkspaceConfig{
		Session: config.SessionConfig{
			Posture: config.SessionPostureConfig{Approvals: "on-request"},
		},
	})
	if declared.Approvals != "on-request" {
		t.Errorf("approvals = %q, want on-request", declared.Approvals)
	}
	if declared.Sandbox != "" {
		t.Errorf("sandbox = %q, want the empty value the workspace left it at", declared.Sandbox)
	}
}
