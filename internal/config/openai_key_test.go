package config

import "testing"

// TestOpenAIAndAnthropicKeysCoexist proves the secret table is agent-neutral:
// OPENAI_API_KEY binds as an ordinary secret row alongside ANTHROPIC_API_KEY,
// through the same [claude.env.secrets] mechanism, with no code change. Binding
// one does not disturb the other. Mirrors the ANTHROPIC_API_KEY round-trip test.
func TestOpenAIAndAnthropicKeysCoexist(t *testing.T) {
	input := `
[workspace]
name = "ws"

[claude.env.secrets]
ANTHROPIC_API_KEY = "vault://team/ANTHROPIC_API_KEY"
OPENAI_API_KEY = "vault://team/OPENAI_API_KEY"

[claude.env.secrets.required]
OPENAI_API_KEY = "needed for codex"

[vault.providers.team]
kind = "fake"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ce := result.Config.Claude.Env

	// Both keys decode as independent secret rows.
	if got := ce.Secrets.Values["ANTHROPIC_API_KEY"].Plain; got != "vault://team/ANTHROPIC_API_KEY" {
		t.Errorf("ANTHROPIC_API_KEY.Plain = %q, want vault://team/ANTHROPIC_API_KEY", got)
	}
	if got := ce.Secrets.Values["OPENAI_API_KEY"].Plain; got != "vault://team/OPENAI_API_KEY" {
		t.Errorf("OPENAI_API_KEY.Plain = %q, want vault://team/OPENAI_API_KEY", got)
	}
	// The OpenAI required note is carried without disturbing the Anthropic row.
	if got := ce.Secrets.Required["OPENAI_API_KEY"]; got != "needed for codex" {
		t.Errorf("OPENAI_API_KEY required = %q, want %q", got, "needed for codex")
	}
	if _, present := ce.Secrets.Required["ANTHROPIC_API_KEY"]; present {
		t.Errorf("ANTHROPIC_API_KEY unexpectedly present in required; binding OPENAI_API_KEY must not add it")
	}
	if len(ce.Secrets.Values) != 2 {
		t.Errorf("secret values = %d, want 2 (both keys coexist)", len(ce.Secrets.Values))
	}
}
