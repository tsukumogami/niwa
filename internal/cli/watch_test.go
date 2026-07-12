package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestCheckSandboxCapability_FailClosed validates the preflight is fail-closed
// in the actual environment: it returns nil only when the sandbox is genuinely
// enforceable here, and otherwise a descriptive refusal -- never a silent pass.
func TestCheckSandboxCapability_FailClosed(t *testing.T) {
	err := checkSandboxCapability(context.Background())
	if err == nil {
		t.Log("host reports sandbox-capable; preflight would permit dispatch")
		return
	}
	// On an incapable host the refusal must name the missing capability, so an
	// operator understands why dispatch was refused.
	msg := err.Error()
	if !strings.Contains(msg, "sandbox") {
		t.Errorf("refusal message should explain the sandbox incapability, got %q", msg)
	}
}

// TestRunWatchOnce_RefusesWhenSandboxRequiredButIncapable proves the default
// posture (containment on, sandbox required) fails closed: when the probe reports
// the host cannot enforce the sandbox, the run refuses before touching the
// workspace or GitHub. XDG_CONFIG_HOME points at an empty dir so the defaults --
// not the host's config -- are exercised.
func TestRunWatchOnce_RefusesWhenSandboxRequiredButIncapable(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	prev := sandboxCapabilityCheck
	sentinel := errors.New("no netns here")
	sandboxCapabilityCheck = func(context.Context) error { return sentinel }
	t.Cleanup(func() { sandboxCapabilityCheck = prev })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runWatchOnce(cmd, nil)
	if err == nil {
		t.Fatal("expected watch --once to refuse when sandbox=required cannot be enforced")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("refusal should wrap the capability error, got %v", err)
	}
	if !strings.Contains(err.Error(), "refusing to dispatch") {
		t.Errorf("refusal message should say it refuses to dispatch, got %q", err.Error())
	}
}

// TestResolveReviewPlan walks the single-switch matrix with a stubbed capability
// probe: off never probes and yields no sandbox; required probes and either
// sandboxes (capable) or refuses (incapable).
func TestResolveReviewPlan(t *testing.T) {
	capable := func(context.Context) error { return nil }
	incapable := func(context.Context) error { return errors.New("no netns") }

	cases := []struct {
		name        string
		mode        string
		probe       func(context.Context) error
		wantSandbox bool
		wantErr     bool
	}{
		{"off never probes", "off", nil, false, false},
		{"required capable", "required", capable, true, false},
		{"required incapable refuses", "required", incapable, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.probe != nil {
				prev := sandboxCapabilityCheck
				sandboxCapabilityCheck = tc.probe
				t.Cleanup(func() { sandboxCapabilityCheck = prev })
			}
			plan, err := resolveReviewPlan(context.Background(), tc.mode)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected refusal for %s", tc.name)
				}
				if !strings.Contains(err.Error(), "refusing to dispatch") {
					t.Errorf("refusal should say it refuses to dispatch, got %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.sandbox != tc.wantSandbox {
				t.Errorf("plan.sandbox = %v, want %v", plan.sandbox, tc.wantSandbox)
			}
		})
	}
}

func TestOwnerRepoFromGitURL(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"git@github.com:acme/api.git", "acme", "api", true},
		{"https://github.com/acme/api.git", "acme", "api", true},
		{"https://github.com/acme/api", "acme", "api", true},
		{"git@ghe.example.com:org/sub-repo.git", "org", "sub-repo", true},
		{"", "", "", false},
		{"not-a-url", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, ok := ownerRepoFromGitURL(tc.in)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("ownerRepoFromGitURL(%q) = (%q,%q,%v) want (%q,%q,%v)",
				tc.in, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}

func TestResolveSandboxMode(t *testing.T) {
	mk := func(s string) *config.GlobalConfig {
		gc := &config.GlobalConfig{}
		gc.Global.WatchSandbox = s
		return gc
	}
	cases := []struct {
		name    string
		gc      *config.GlobalConfig
		want    string
		wantErr bool
	}{
		{"nil -> default required", nil, "required", false},
		{"empty -> default required", mk(""), "required", false},
		{"off", mk("off"), "off", false},
		{"required", mk("required"), "required", false},
		{"optional is no longer valid", mk("optional"), "", true},
		{"invalid", mk("sometimes"), "", true},
	}
	for _, tc := range cases {
		mode, err := resolveSandboxMode(tc.gc)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}
		if mode != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, mode, tc.want)
		}
	}
}
