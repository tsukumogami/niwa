package cli

import (
	"bytes"
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

// TestResolveContainmentPlan walks the containment matrix (design Decision 7)
// with a stubbed capability probe.
func TestResolveContainmentPlan(t *testing.T) {
	capable := func(context.Context) error { return nil }
	incapable := func(context.Context) error { return errors.New("no netns") }

	cases := []struct {
		name                 string
		containment, sandbox string
		probe                func(context.Context) error
		wantContained        bool
		wantSandbox          bool
		wantErr              bool
	}{
		{"off never probes", "off", "required", nil, false, false, false},
		{"on+disabled", "on", "disabled", nil, true, false, false},
		{"on+required capable", "on", "required", capable, true, true, false},
		{"on+required incapable refuses", "on", "required", incapable, false, false, true},
		{"on+optional capable uses sandbox", "on", "optional", capable, true, true, false},
		{"on+optional incapable proceeds without", "on", "optional", incapable, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.probe != nil {
				prev := sandboxCapabilityCheck
				sandboxCapabilityCheck = tc.probe
				t.Cleanup(func() { sandboxCapabilityCheck = prev })
			}
			cmd := &cobra.Command{}
			var errbuf bytes.Buffer
			cmd.SetErr(&errbuf)
			cmd.SetContext(context.Background())

			plan, err := resolveContainmentPlan(context.Background(), cmd, tc.containment, tc.sandbox)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected refusal for %s", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.contained != tc.wantContained || plan.applySandbox != tc.wantSandbox {
				t.Errorf("plan = {contained:%v sandbox:%v}, want {contained:%v sandbox:%v}",
					plan.contained, plan.applySandbox, tc.wantContained, tc.wantSandbox)
			}
			// The optional-unavailable cell prints a notice and proceeds.
			if tc.containment == "on" && tc.sandbox == "optional" && !tc.wantSandbox {
				if !strings.Contains(errbuf.String(), "proceeding contained without it") {
					t.Errorf("optional-unavailable should print a notice, got %q", errbuf.String())
				}
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

func TestResolveContainmentSwitches(t *testing.T) {
	mk := func(c, s string) *config.GlobalConfig {
		gc := &config.GlobalConfig{}
		gc.Global.WatchContainment = c
		gc.Global.WatchSandbox = s
		return gc
	}
	cases := []struct {
		name         string
		gc           *config.GlobalConfig
		wantC, wantS string
		wantErr      bool
	}{
		{"nil -> defaults", nil, "on", "required", false},
		{"empty -> defaults", mk("", ""), "on", "required", false},
		{"containment off", mk("off", ""), "off", "required", false},
		{"sandbox optional", mk("", "optional"), "on", "optional", false},
		{"sandbox disabled", mk("on", "disabled"), "on", "disabled", false},
		{"invalid containment", mk("maybe", ""), "", "", true},
		{"invalid sandbox", mk("on", "sometimes"), "", "", true},
	}
	for _, tc := range cases {
		c, s, err := resolveContainmentSwitches(tc.gc)
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
		if c != tc.wantC || s != tc.wantS {
			t.Errorf("%s: got (%q,%q), want (%q,%q)", tc.name, c, s, tc.wantC, tc.wantS)
		}
	}
}
