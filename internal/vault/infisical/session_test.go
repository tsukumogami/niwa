package infisical

import (
	"context"
	"testing"
)

// session_test.go reuses fakeCommander (defined in infisical_test.go,
// same package) rather than introducing a second commander stub --
// its stdout/stderr/exitCode/runErr fields cover everything
// DetectSessionStatus's tests need.

func TestDetectSessionStatus_AuthenticatedWithOrganization(t *testing.T) {
	c := &fakeCommander{
		stdout: []byte(`{"sessions":[{"principalType":"user","status":"authenticated","domain":"app.infisical.com","authMethod":"oauth","tokenSource":"keyring","organization":"org-abc-123","verification":{"state":"verified"}}]}`),
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Authenticated {
		t.Error("Authenticated = false, want true")
	}
	if status.Organization != "org-abc-123" {
		t.Errorf("Organization = %q, want org-abc-123", status.Organization)
	}
}

func TestDetectSessionStatus_AuthenticatedNoOrganization(t *testing.T) {
	// A machine-identity token session per Assumption C's documented
	// fallback: authenticated is true, but no organization field --
	// callers must fall back to classifying the management call's own
	// error rather than trusting this proactive signal.
	c := &fakeCommander{
		stdout: []byte(`{"sessions":[{"principalType":"machine","status":"authenticated"}]}`),
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Authenticated {
		t.Error("Authenticated = false, want true")
	}
	if status.Organization != "" {
		t.Errorf("Organization = %q, want empty", status.Organization)
	}
}

func TestDetectSessionStatus_NoSession(t *testing.T) {
	c := &fakeCommander{
		stdout: []byte(`{"sessions":[]}`),
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Authenticated {
		t.Error("Authenticated = true, want false")
	}
}

func TestDetectSessionStatus_NonZeroExit(t *testing.T) {
	c := &fakeCommander{
		exitCode: 1,
		stderr:   []byte("not logged in"),
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Authenticated {
		t.Error("Authenticated = true, want false")
	}
}

func TestDetectSessionStatus_MalformedJSON(t *testing.T) {
	c := &fakeCommander{
		stdout: []byte("not json at all"),
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Authenticated {
		t.Error("Authenticated = true, want false")
	}
}

func TestDetectSessionStatus_StartFailure(t *testing.T) {
	c := &fakeCommander{
		runErr: errStartFailureForTest,
	}
	status, err := DetectSessionStatus(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Authenticated {
		t.Error("Authenticated = true, want false")
	}
}

func TestDetectSessionStatus_NilCommanderDoesNotPanic(t *testing.T) {
	// Passing a nil commander falls back to defaultCommander, which
	// would attempt a real subprocess. We only verify this doesn't
	// panic when the binary is absent from PATH in the test sandbox;
	// a real "infisical" on PATH is exercised by the functional suite,
	// not here.
	_, _ = DetectSessionStatus(context.Background(), nil)
}

// errStartFailureForTest is a stand-in for a process start failure
// (e.g. exec.ErrNotFound wrapped by the real commander).
var errStartFailureForTest = &testStartError{}

type testStartError struct{}

func (*testStartError) Error() string {
	return "exec: \"infisical\": executable file not found in $PATH"
}
