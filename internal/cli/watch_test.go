package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/watch"
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

// TestResolveMaxStaged mirrors the resolveSandboxMode contract for the cross-run
// staged-agent cap: unset (0) and nil resolve to the default, a positive value is
// honored, and a negative value is a hard error.
func TestResolveMaxStaged(t *testing.T) {
	mk := func(n int) *config.GlobalConfig {
		gc := &config.GlobalConfig{}
		gc.Global.WatchMaxStaged = n
		return gc
	}
	cases := []struct {
		name    string
		gc      *config.GlobalConfig
		want    int
		wantErr bool
	}{
		{"nil -> default", nil, watch.DefaultMaxStaged, false},
		{"zero -> default", mk(0), watch.DefaultMaxStaged, false},
		{"explicit positive honored", mk(8), 8, false},
		{"explicit one honored", mk(1), 1, false},
		{"negative rejected", mk(-1), 0, true},
	}
	for _, tc := range cases {
		got, err := resolveMaxStaged(tc.gc)
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
		if got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestCountLiveStaged: the cap's occupancy input counts only entries whose value is
// true. A Defer'd session is recorded live (true) and so counts toward the cap; a
// PR with no live agent never appears as true. Continuation (Issue 5) reuses one of
// these already-counted agents, so it is cap-neutral.
func TestCountLiveStaged(t *testing.T) {
	if got := countLiveStaged(nil); got != 0 {
		t.Errorf("nil map: got %d, want 0", got)
	}
	if got := countLiveStaged(map[string]bool{}); got != 0 {
		t.Errorf("empty map: got %d, want 0", got)
	}
	live := map[string]bool{
		"acme/api#1": true, // fresh live
		"acme/api#2": true, // deferred but live (still holds an agent)
		"acme/web#3": true, // continuing live (already counted; cap-neutral)
	}
	if got := countLiveStaged(live); got != 3 {
		t.Errorf("three live: got %d, want 3", got)
	}
	// Defensive: a false entry (not produced by liveStagedSessions today) is not
	// counted as an occupied agent.
	mixed := map[string]bool{"acme/api#1": true, "acme/api#2": false}
	if got := countLiveStaged(mixed); got != 1 {
		t.Errorf("one live + one false: got %d, want 1", got)
	}
}
