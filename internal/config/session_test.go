package config

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

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

// sessionConfigWith wraps a [session.*] fragment in the smallest workspace
// config Parse accepts.
func sessionConfigWith(fragment string) string {
	return `
[workspace]
name = "test"

[[sources]]
org = "myorg"
` + fragment
}

// TestDispatchEnvAndOnReapParse pins the authored shape of the dispatch-time
// and reap-time tables: every documented key decodes, none surfaces as an
// unknown field, and the timeout reads back as a duration.
func TestDispatchEnvAndOnReapParse(t *testing.T) {
	result, err := Parse([]byte(sessionConfigWith(`
[session.dispatch_env]
passthrough = ["BUILD_LABEL", "HANDOFF_MARK"]
unset = ["STALE_MARK"]

[session.on_reap]
command = "true"
timeout = "2s"
env = ["HANDOFF_MARK", "STORE_DIR"]
`)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("parsing reported %v; every documented key must decode", result.Warnings)
	}
	s := result.Config.Session
	if got := s.DispatchEnv.Passthrough; len(got) != 2 || got[0] != "BUILD_LABEL" || got[1] != "HANDOFF_MARK" {
		t.Errorf("passthrough = %v", got)
	}
	if got := s.DispatchEnv.Unset; len(got) != 1 || got[0] != "STALE_MARK" {
		t.Errorf("unset = %v", got)
	}
	if s.OnReap.Command != "true" || s.OnReap.TimeoutDuration() != 2*time.Second {
		t.Errorf("on_reap = %+v (timeout %s)", s.OnReap, s.OnReap.TimeoutDuration())
	}
	if got := s.OnReap.Env; len(got) != 2 || got[1] != "STORE_DIR" {
		t.Errorf("on_reap env = %v", got)
	}
	// The dispatch-time lists count toward no apply-time declaration.
	if !s.Env.IsEmpty() {
		t.Error("the dispatch-time lists made [session.env] report itself declared")
	}
}

// TestOnReapTimeoutDefault covers the unset timeout.
func TestOnReapTimeoutDefault(t *testing.T) {
	if got := (SessionOnReapConfig{}).TimeoutDuration(); got != 10*time.Second {
		t.Errorf("default timeout = %s, want 10s", got)
	}
}

// TestNoDispatchDeclarationLoadsAsBefore: a config without either table
// loads with both empty and no warning.
func TestNoDispatchDeclarationLoadsAsBefore(t *testing.T) {
	result, err := Parse([]byte(sessionConfigWith("")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Config.Session.DispatchEnv.IsEmpty() || result.Config.Session.OnReap.Command != "" {
		t.Errorf("an undeclared table decoded to %+v", result.Config.Session)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings = %v", result.Warnings)
	}
}

// TestSessionEnvListRefusals is every single-name load-time refusal, in each
// list that gets it. Each case must fail to load with an error naming the
// entry.
func TestSessionEnvListRefusals(t *testing.T) {
	refused := []struct {
		name  string
		entry string
	}{
		{"niwa prefix", "NIWA_THING"},
		{"niwa prefix lowercase", "niwa_thing"},
		{"claude prefix", "CLAUDE_THING"},
		{"lineage variable", "OTEL_RESOURCE_ATTRIBUTES"},
		{"reap key", "NIWA_REAP_KEY"},
		{"suffix _KEY", "SERVICE_KEY"},
		{"suffix _APIKEY", "SERVICE_APIKEY"},
		{"suffix _SECRET", "CLIENT_SECRET"},
		{"suffix _TOKEN", "API_TOKEN"},
		{"suffix _PAT", "GH_PAT"},
		{"suffix _PAT lowercase", "gh_pat"},
		{"suffix _PASSWORD", "DB_PASSWORD"},
		{"suffix _PASSWD", "DB_PASSWD"},
		{"suffix _CREDENTIALS", "SERVICE_CREDENTIALS"},
		{"suffix mixed case", "Service_Token"},
		{"prefix AWS_", "AWS_REGION"},
		{"prefix AWS_ lowercase", "aws_profile"},
		{"not a name", "BAD-NAME"},
		{"leading digit", "1NAME"},
	}
	lists := map[string]string{
		"passthrough": "[session.dispatch_env]\npassthrough",
		"unset":       "[session.dispatch_env]\nunset",
		"on_reap env": "[session.on_reap]\nenv",
	}
	for listName, list := range lists {
		for _, tc := range refused {
			t.Run(listName+"/"+tc.name, func(t *testing.T) {
				_, err := Parse([]byte(sessionConfigWith(fmt.Sprintf("%s = [%q]\n", list, tc.entry))))
				if err == nil {
					t.Fatalf("%q loaded without complaint", tc.entry)
				}
				if !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.entry)) {
					t.Errorf("the error does not name the entry: %v", err)
				}
			})
		}
	}
}

// TestSessionEnvListStructuralRefusals covers the refusals that depend on
// more than one name: duplicates, a name in both dispatch lists, and a name
// [session.env] already declares.
func TestSessionEnvListStructuralRefusals(t *testing.T) {
	cases := map[string]string{
		"duplicate passthrough": `
[session.dispatch_env]
passthrough = ["SAME_NAME", "SAME_NAME"]
`,
		"duplicate unset": `
[session.dispatch_env]
unset = ["SAME_NAME", "SAME_NAME"]
`,
		"duplicate on_reap env": `
[session.on_reap]
env = ["SAME_NAME", "SAME_NAME"]
`,
		"passthrough and unset": `
[session.dispatch_env]
passthrough = ["SAME_NAME"]
unset = ["SAME_NAME"]
`,
		"also in session.env vars": `
[session.env.vars]
SAME_NAME = "x"

[session.dispatch_env]
passthrough = ["SAME_NAME"]
`,
		"also in session.env promote": `
[session.env]
promote = ["SAME_NAME"]

[session.dispatch_env]
unset = ["SAME_NAME"]
`,
		"on_reap env also in session.env": `
[session.env]
promote = ["SAME_NAME"]

[session.on_reap]
env = ["SAME_NAME"]
`,
	}
	for name, fragment := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sessionConfigWith(fragment)))
			if err == nil {
				t.Fatal("loaded without complaint")
			}
			if !strings.Contains(err.Error(), `"SAME_NAME"`) {
				t.Errorf("the error does not name the entry: %v", err)
			}
		})
	}

	// A name may sit in [session.dispatch_env] and [session.on_reap] env both.
	if _, err := Parse([]byte(sessionConfigWith(`
[session.dispatch_env]
passthrough = ["SHARED_NAME"]

[session.on_reap]
env = ["SHARED_NAME"]
`))); err != nil {
		t.Errorf("a name in both the pass-through and the on-reap list was refused: %v", err)
	}
}

// TestOnReapTimeoutRefusals: an unparsable or non-positive timeout is refused
// at load.
func TestOnReapTimeoutRefusals(t *testing.T) {
	for _, v := range []string{"soon", "10", "0s", "-1s"} {
		t.Run(v, func(t *testing.T) {
			_, err := Parse([]byte(sessionConfigWith(fmt.Sprintf("[session.on_reap]\ncommand = \"true\"\ntimeout = %q\n", v))))
			if err == nil {
				t.Fatalf("timeout %q loaded without complaint", v)
			}
			if !strings.Contains(err.Error(), "session.on_reap.timeout") {
				t.Errorf("the error does not name the key: %v", err)
			}
		})
	}
}
