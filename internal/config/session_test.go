package config

import "testing"

// TestSessionDeclarationParses pins the authored shape of the agent-neutral
// session declaration: every key it documents decodes into a field, and none of
// them surfaces as an unknown one -- which is what a user would see if a key
// were named differently here than in the guide.
func TestSessionDeclarationParses(t *testing.T) {
	input := `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[vault.providers.team]
kind = "fake"

[session.env]
promote = ["SHARED_TOKEN"]

[session.env.vars]
REGION = "eu-west-1"
API_TOKEN = "vault://team/api-token"

[session.posture]
approvals = "on-request"
sandbox = "workspace-write"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("parsing the declaration reported %v; every documented key must decode", result.Warnings)
	}

	env := result.Config.Session.Env
	if len(env.Promote) != 1 || env.Promote[0] != "SHARED_TOKEN" {
		t.Errorf("promote = %v, want [SHARED_TOKEN]", env.Promote)
	}
	if got := env.Vars.Values["REGION"].Plain; got != "eu-west-1" {
		t.Errorf("REGION = %q, want eu-west-1", got)
	}
	// The reference is carried, not resolved: resolution is the vault
	// pipeline's job and happens before anything is generated.
	if got := env.Vars.Values["API_TOKEN"].Plain; got != "vault://team/api-token" {
		t.Errorf("API_TOKEN = %q, want the reference carried through", got)
	}

	posture := result.Config.Session.Posture
	if posture.Approvals != "on-request" {
		t.Errorf("approvals = %q, want on-request", posture.Approvals)
	}
	if posture.Sandbox != "workspace-write" {
		t.Errorf("sandbox = %q, want workspace-write", posture.Sandbox)
	}
}

// TestPostureDeclarationsAreIndependentFields keeps the two posture keys apart
// at the declaration surface, which is where the separation starts. A workspace
// that declares one leaves the other empty, and empty is what every generator
// reads as "write nothing" -- so relaxing approvals cannot reach the sandbox
// even before a producer sees it.
func TestPostureDeclarationsAreIndependentFields(t *testing.T) {
	input := `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[session.posture]
approvals = "never"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	posture := result.Config.Session.Posture
	if posture.Approvals != "never" {
		t.Errorf("approvals = %q, want never", posture.Approvals)
	}
	if posture.Sandbox != "" {
		t.Errorf("declaring approvals set sandbox = %q; the two are separate decisions", posture.Sandbox)
	}
	if posture.IsEmpty() {
		t.Error("a declaration with one posture key reports itself empty")
	}
	if !(SessionPostureConfig{}).IsEmpty() {
		t.Error("a zero posture declaration does not report itself empty")
	}
}

// TestEmptySessionDeclarationIsEmpty keeps the "declared nothing" case cheap to
// ask about: every generator branches on it before deciding to write anything.
func TestEmptySessionDeclarationIsEmpty(t *testing.T) {
	if !(SessionEnvConfig{}).IsEmpty() {
		t.Error("a zero session env declaration does not report itself empty")
	}
	declared := SessionEnvConfig{Vars: EnvVarsTable{Values: map[string]MaybeSecret{"A": {Plain: "b"}}}}
	if declared.IsEmpty() {
		t.Error("a declaration with one variable reports itself empty")
	}
}

// TestSessionEnvVaultRefsAreValidated puts the neutral table under the same
// same-file provider rule every other value slot obeys. Without it a reference
// to a provider this file never declares would fail at resolution time with no
// hint about where it came from.
func TestSessionEnvVaultRefsAreValidated(t *testing.T) {
	input := `
[workspace]
name = "test"

[[sources]]
org = "myorg"

[vault.providers.team]
kind = "fake"

[session.env.vars]
API_TOKEN = "vault://elsewhere/api-token"
`
	if _, err := Parse([]byte(input)); err == nil {
		t.Fatal("a reference to an undeclared provider parsed without complaint")
	}
}
