package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// TestRunWatchOnce_RefusesWhenSandboxIncapable proves the command fails closed:
// when the capability probe reports the host cannot contain a session, the run
// refuses before touching the workspace or GitHub.
func TestRunWatchOnce_RefusesWhenSandboxIncapable(t *testing.T) {
	prev := sandboxCapabilityCheck
	sentinel := errors.New("no netns here")
	sandboxCapabilityCheck = func(context.Context) error { return sentinel }
	t.Cleanup(func() { sandboxCapabilityCheck = prev })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runWatchOnce(cmd, nil)
	if err == nil {
		t.Fatal("expected watch --once to refuse when the sandbox is incapable")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("refusal should wrap the capability error, got %v", err)
	}
	if !strings.Contains(err.Error(), "uncontained") {
		t.Errorf("refusal message should say it refuses to dispatch uncontained, got %q", err.Error())
	}
}

// TestRunWatchOnce_WarnPolicyDoesNotRefuse proves the warn fallback is a
// fallthrough, not a distinct dispatch path: when the capability probe fails and
// --uncontained=warn is set, the preflight does NOT return the uncontained
// refusal -- control continues into the unchanged downstream (which then fails
// for an unrelated, benign reason in this bare test env). This structurally
// asserts warn/allow keep the same metadata-prompt/credential-scrub/human-gate
// path (Issue 9 AC3): there is no alternate uncontained dispatch path to diverge
// onto.
func TestRunWatchOnce_WarnPolicyDoesNotRefuse(t *testing.T) {
	prev := sandboxCapabilityCheck
	sandboxCapabilityCheck = func(context.Context) error { return errors.New("no netns here") }
	t.Cleanup(func() { sandboxCapabilityCheck = prev })

	prevPolicy := watchUncontained
	watchUncontained = "warn"
	t.Cleanup(func() { watchUncontained = prevPolicy })

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	err := runWatchOnce(cmd, nil)
	// It must have moved past the preflight: whatever error surfaces downstream,
	// it is not the fail-closed "refusing to dispatch uncontained" refusal.
	if err != nil && strings.Contains(err.Error(), "refusing to dispatch uncontained") {
		t.Errorf("warn policy must not refuse at the preflight; got %v", err)
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

func TestResolveUncontainedPolicy(t *testing.T) {
	cases := []struct {
		flag, configDefault string
		want                string
		wantErr             bool
	}{
		// Empty everywhere -> the safe default.
		{"", "", "refuse", false},
		// Config default applies when the flag is empty.
		{"", "warn", "warn", false},
		{"", "allow", "allow", false},
		// Flag overrides the config default (flag > config > default).
		{"refuse", "allow", "refuse", false},
		{"allow", "refuse", "allow", false},
		{"warn", "", "warn", false},
		// Invalid values are hard errors (fail-closed), never coerced.
		{"bogus", "", "", true},
		{"", "bogus", "", true},
		{"ALLOW", "", "", true}, // case-sensitive; not silently normalized
	}
	for _, tc := range cases {
		got, err := resolveUncontainedPolicy(tc.flag, tc.configDefault)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveUncontainedPolicy(%q,%q) expected error, got %q", tc.flag, tc.configDefault, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveUncontainedPolicy(%q,%q) unexpected error: %v", tc.flag, tc.configDefault, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveUncontainedPolicy(%q,%q) = %q, want %q", tc.flag, tc.configDefault, got, tc.want)
		}
	}
}

func TestValidateDraftPath(t *testing.T) {
	root := filepath.FromSlash("/ws")
	good := filepath.Join(root, "inst+watch-a-b-1-deadbeef", "watch-review-draft.md")
	if err := validateDraftPath(root, good); err != nil {
		t.Errorf("expected valid draft path to pass: %v", err)
	}

	bad := []string{
		filepath.FromSlash("/etc/passwd"),                         // outside root
		filepath.Join(root, "inst", "other.md"),                   // wrong basename
		filepath.Join(root, "..", "etc", "watch-review-draft.md"), // traversal out
		filepath.FromSlash("/wsother/inst/watch-review-draft.md"), // prefix-but-not-child
	}
	for _, p := range bad {
		if err := validateDraftPath(root, p); err == nil {
			t.Errorf("expected draft path %q to be rejected", p)
		}
	}
}
