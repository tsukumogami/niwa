package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

// stubAskPostureSeams overrides the trust seams and HOME for resolveAskPosture and
// records whether the seed/remove were invoked, restoring the originals on cleanup.
type askPostureStub struct {
	seeded  bool
	removed bool
}

func stubAskPostureSeams(t *testing.T, home string, seedErr error) *askPostureStub {
	t.Helper()
	s := &askPostureStub{}
	origEnsure, origRemove, origHome := ensureInstanceTrustedFunc, removeInstanceTrustFunc, reviewHomeDir
	ensureInstanceTrustedFunc = func(string) error { s.seeded = true; return seedErr }
	removeInstanceTrustFunc = func(string) error { s.removed = true; return nil }
	reviewHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() {
		ensureInstanceTrustedFunc, removeInstanceTrustFunc, reviewHomeDir = origEnsure, origRemove, origHome
	})
	return s
}

// TestResolveAskPosture_PrerequisitesMet: a sandboxed instance outside ~/.claude with a
// successful trust seed yields the ask posture.
func TestResolveAskPosture_PrerequisitesMet(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir() // a normal repo path, not under ~/.claude
	s := stubAskPostureSeams(t, home, nil)

	if got := resolveAskPosture(inst, true); !got {
		t.Errorf("prerequisites met must yield the ask posture (true)")
	}
	if !s.seeded {
		t.Errorf("the ask posture must seed workspace trust")
	}
}

// TestResolveAskPosture_NoSandbox: a non-sandboxed review never uses the ask posture
// and never seeds trust.
func TestResolveAskPosture_NoSandbox(t *testing.T) {
	s := stubAskPostureSeams(t, t.TempDir(), nil)
	if got := resolveAskPosture(t.TempDir(), false); got {
		t.Errorf("a non-sandboxed review must not use the ask posture")
	}
	if s.seeded {
		t.Errorf("a non-sandboxed review must not seed trust")
	}
}

// TestResolveAskPosture_TrustSeedFailureFallsBack: a failed trust seed falls back to the
// hard-deny posture.
func TestResolveAskPosture_TrustSeedFailureFallsBack(t *testing.T) {
	home := t.TempDir()
	inst := t.TempDir()
	stubAskPostureSeams(t, home, errors.New("cannot write ~/.claude.json"))

	if got := resolveAskPosture(inst, true); got {
		t.Errorf("a trust-seed failure must fall back to the hard-deny posture (false)")
	}
}

// TestResolveAskPosture_UnderClaudeHomeFallsBack: an instance under ~/.claude falls back
// to the hard-deny posture and never seeds trust (writes there are blocked regardless).
func TestResolveAskPosture_UnderClaudeHomeFallsBack(t *testing.T) {
	home := t.TempDir()
	inst := filepath.Join(home, ".claude", "instances", "watch-x")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	s := stubAskPostureSeams(t, home, nil)

	if got := resolveAskPosture(inst, true); got {
		t.Errorf("an instance under ~/.claude must fall back to the hard-deny posture")
	}
	if s.seeded {
		t.Errorf("an instance under ~/.claude must not seed trust")
	}
}
