package infisical

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// forbiddenManagementCallPattern matches a direct call-site invocation
// of either of the two MUTATING management functions team-phase code
// must never drive with the operator's session JWT (Decision 4 /
// AC-10): MintClientSecret (mints a credential) and RevokeClientSecret
// (revokes one). It matches the bare identifier followed by "(" so a
// qualified call from another package (infisical.MintClientSecret(...))
// is caught the same as an unqualified one from within this package,
// while a mere mention in a comment or string literal (no trailing
// paren) is not.
//
// ReadIdentity is deliberately NOT in this pattern. It was originally
// banned outright alongside the two mutating calls, but Issue 5 (the
// team runner) settled a narrower boundary: ReadIdentity is a
// read-only probe, authenticated with the operator's own session
// bearer, and it is the landing check the design itself requires for
// the Universal-Auth-attach step ("does the identity now expose a
// client_id?") and the R21 re-run verification sweep. AC-10's actual
// custody guarantee is that team-phase code never CREATES an identity,
// never ATTACHES Universal Auth, and never GRANTS environment access
// through a management REST call with the operator's session JWT --
// reads that merely check whether those things already happened do
// not touch that boundary. The runtime request recorder (see below)
// is what actually enforces this at the call level: it asserts zero
// team-path calls to the mint/revoke (client-secrets) endpoints, while
// permitting the identity-read and project-membership-read calls the
// landing checks and R21 sweep require.
var forbiddenManagementCallPattern = regexp.MustCompile(`\b(MintClientSecret|RevokeClientSecret)\s*\(`)

// TestAC10_NoManagementCallsFromTeamPhaseCode is the static half of
// the AC-10 backstop Decision 4 accepts in place of a compiler-
// enforced package boundary: it greps every source file under
// internal/onboard whose name signals team-phase code (matching
// "team", case-insensitive) for a direct call-site invocation of
// MintClientSecret or RevokeClientSecret, and fails if it finds one.
//
// This is a DIRECT CALL-SITE check only. It does not catch
// indirection: assigning one of these functions to a variable or
// struct field and invoking it through that binding, passing it as a
// higher-order argument, or reaching it via a wrapper function defined
// outside a "team"-named file, all evade this regex. The load-bearing
// enforcement for AC-10 is the runtime request recorder on
// infisicalFakeServer (test/functional/infisical_fake_server.go),
// which observes actual HTTP traffic on the team path regardless of
// how the call was reached; this test is a cheap, fast-failing
// second line of defense for the straightforward case, not a
// substitute for that recorder-based assertion.
//
// internal/onboard does not exist yet as of this issue (it lands in
// PLAN-niwa-onboard.md issue 3 onward); when the team-phase directory
// is absent this test passes vacuously -- there is nothing to check
// yet, and the check activates automatically once team.go (issue 5)
// exists.
func TestAC10_NoManagementCallsFromTeamPhaseCode(t *testing.T) {
	onboardDir := onboardPackageDir(t)

	entries, err := os.ReadDir(onboardDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Skipf("internal/onboard does not exist yet at %s; AC-10 check activates once team-phase code lands", onboardDir)
		}
		t.Fatalf("reading %s: %v", onboardDir, err)
	}

	var violations []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if !strings.Contains(strings.ToLower(name), "team") {
			continue
		}
		path := filepath.Join(onboardDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for _, match := range forbiddenManagementCallPattern.FindAllString(string(src), -1) {
			violations = append(violations, name+": "+match)
		}
	}

	if len(violations) > 0 {
		t.Fatalf(
			"AC-10 violation: team-phase file(s) call a mutating management REST function directly (must run only against the operator's own non-privileged CLI delegations, never MintClientSecret/RevokeClientSecret):\n%s",
			strings.Join(violations, "\n"),
		)
	}
}

// onboardPackageDir locates internal/onboard relative to this test
// file's own path (via runtime.Caller) so the test works regardless
// of the working directory `go test` is invoked from.
func onboardPackageDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to resolve this test file's path")
	}
	// thisFile is .../internal/vault/infisical/lint_test.go
	infisicalDir := filepath.Dir(thisFile)
	vaultDir := filepath.Dir(infisicalDir)
	internalDir := filepath.Dir(vaultDir)
	return filepath.Join(internalDir, "onboard")
}
