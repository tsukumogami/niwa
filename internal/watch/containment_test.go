package watch

import (
	"strings"
	"testing"
)

// TestBuildContainedEnv_CanaryAbsentAndAllowlistSubset is the AC12 canary test:
// a planted secret and credential-bearing variables in the parent env must be
// absent from the contained session env, and the session env must be a subset
// of the allowlist (plus the synthetic HOME).
func TestBuildContainedEnv_CanaryAbsentAndAllowlistSubset(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/dev", // must be replaced by synthetic HOME
		"ANTHROPIC_API_KEY=sk-model",
		"NIWA_CANARY_SECRET=canary", // planted secret -> must NOT survive
		"GITHUB_TOKEN=ghp_secret",
		"GH_TOKEN=gh_secret",
		"GH_ENTERPRISE_TOKEN=ghe",
		"GITHUB_ACTIONS=true",
		"SSH_AUTH_SOCK=/tmp/agent.sock",
		"AWS_SECRET_ACCESS_KEY=aws",
		"LANG=en_US.UTF-8",
	}
	syntheticHome := "/instance/.watch-home"

	env := BuildContainedEnv(parent, syntheticHome)

	got := map[string]string{}
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		got[kv[:eq]] = kv[eq+1:]
	}

	// Allowed survivors.
	if got["PATH"] != "/usr/bin" {
		t.Errorf("PATH not carried: %q", got["PATH"])
	}
	if got["ANTHROPIC_API_KEY"] != "sk-model" {
		t.Errorf("model auth not carried: %q", got["ANTHROPIC_API_KEY"])
	}
	if got["LANG"] != "en_US.UTF-8" {
		t.Errorf("LANG not carried")
	}
	// Synthetic HOME, not the developer's.
	if got["HOME"] != syntheticHome {
		t.Errorf("HOME = %q, want synthetic %q", got["HOME"], syntheticHome)
	}

	// Every denied credential-bearing var and the canary must be absent.
	mustBeAbsent := []string{
		"NIWA_CANARY_SECRET", "GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN",
		"GITHUB_ACTIONS", "SSH_AUTH_SOCK", "AWS_SECRET_ACCESS_KEY",
	}
	for _, name := range mustBeAbsent {
		if _, present := got[name]; present {
			t.Errorf("contained env must not contain %q", name)
		}
	}
	for _, name := range deniedEnvNames {
		if _, present := got[name]; present {
			t.Errorf("denied env var %q leaked into contained session", name)
		}
	}

	// The env is a subset of the allowlist (+ HOME).
	for name := range got {
		if name == "HOME" {
			continue
		}
		if !envAllowlist[name] {
			t.Errorf("contained env contains non-allowlisted var %q", name)
		}
	}
}

func TestBuildContainedEnv_DropsHomeWhenNoSynthetic(t *testing.T) {
	env := BuildContainedEnv([]string{"HOME=/home/dev", "PATH=/bin"}, "")
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			t.Errorf("HOME must not be carried when no synthetic home is given: %q", kv)
		}
	}
}

func TestContainmentProfile_ShapeAndVerify(t *testing.T) {
	profile := ContainmentProfile()
	// A correct profile verifies.
	if err := VerifyContainmentApplied(profile); err != nil {
		t.Fatalf("freshly-built profile should verify: %v", err)
	}
}

func TestVerifyContainmentApplied_RejectsRelaxations(t *testing.T) {
	base := ContainmentProfile()

	// allowedDomains non-empty -> reject (egress would be permitted).
	relaxed := ContainmentProfile()
	relaxed["sandbox"].(map[string]any)["network"].(map[string]any)["allowedDomains"] = []any{"evil.example.com"}
	if err := VerifyContainmentApplied(relaxed); err == nil {
		t.Error("non-empty allowedDomains must be rejected")
	}

	// sandbox disabled -> reject.
	off := ContainmentProfile()
	off["sandbox"].(map[string]any)["enabled"] = false
	if err := VerifyContainmentApplied(off); err == nil {
		t.Error("sandbox.enabled=false must be rejected")
	}

	// bypassPermissions -> reject (not fail-closed).
	bypass := ContainmentProfile()
	bypass["permissions"].(map[string]any)["defaultMode"] = "bypassPermissions"
	if err := VerifyContainmentApplied(bypass); err == nil {
		t.Error("bypassPermissions must be rejected")
	}

	// A non-bypass mode that is still not fail-closed -> reject. This is the
	// allowlist case a denylist would wrongly accept: acceptEdits (and any other
	// mode ContainmentProfile does not produce) must be rejected.
	for _, badMode := range []string{"acceptEdits", "dontAsk", "auto", "plan", "typo"} {
		m := ContainmentProfile()
		m["permissions"].(map[string]any)["defaultMode"] = badMode
		if err := VerifyContainmentApplied(m); err == nil {
			t.Errorf("permission mode %q must be rejected (only the fail-closed allowlist passes)", badMode)
		}
	}

	// Missing sandbox stanza (a merge dropped it) -> reject.
	if err := VerifyContainmentApplied(map[string]any{"permissions": base["permissions"]}); err == nil {
		t.Error("missing sandbox stanza must be rejected")
	}
}
