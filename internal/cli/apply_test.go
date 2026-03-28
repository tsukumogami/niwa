package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestCombineInstanceErrors_SingleError(t *testing.T) {
	errs := []instanceError{
		{instance: "/workspace/ws-1", err: errForTest("failed to load state")},
	}

	combined := combineInstanceErrors(errs)
	if combined == nil {
		t.Fatal("expected non-nil error")
	}

	msg := combined.Error()
	if got, want := contains(msg, "ws-1"), true; got != want {
		t.Errorf("error should mention instance: %s", msg)
	}
	if got, want := contains(msg, "failed to load state"), true; got != want {
		t.Errorf("error should mention cause: %s", msg)
	}
}

func TestCombineInstanceErrors_MultipleErrors(t *testing.T) {
	errs := []instanceError{
		{instance: "/workspace/ws-1", err: errForTest("state error")},
		{instance: "/workspace/ws-2", err: errForTest("clone error")},
	}

	combined := combineInstanceErrors(errs)
	if combined == nil {
		t.Fatal("expected non-nil error")
	}

	msg := combined.Error()
	if !contains(msg, "2 instances") {
		t.Errorf("error should mention count: %s", msg)
	}
	if !contains(msg, "ws-1") || !contains(msg, "ws-2") {
		t.Errorf("error should mention both instances: %s", msg)
	}
}

func TestResolveRegistryScope_NotFound(t *testing.T) {
	// Use a temp dir for XDG_CONFIG_HOME so there's no real registry.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := resolveRegistryScope("nonexistent-workspace")
	if err == nil {
		t.Fatal("expected error for non-existent workspace")
	}
	if !contains(err.Error(), "not found in registry") {
		t.Errorf("error should mention registry: %v", err)
	}
}

func TestApplyCmd_HasInstanceFlag(t *testing.T) {
	// Verify the --instance flag is registered on the command.
	flag := applyCmd.Flags().Lookup("instance")
	if flag == nil {
		t.Fatal("expected --instance flag to be registered")
	}
	if flag.DefValue != "" {
		t.Errorf("expected empty default, got %q", flag.DefValue)
	}
}

func TestApplyCmd_AcceptsPositionalArg(t *testing.T) {
	// cobra.MaximumNArgs(1) should accept 0 or 1 args.
	if err := applyCmd.Args(applyCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := applyCmd.Args(applyCmd, []string{"my-workspace"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := applyCmd.Args(applyCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}

func TestApplyModes_Values(t *testing.T) {
	// Verify the mode constants exist and are distinct, to confirm we're
	// using the right types from the workspace package.
	modes := []workspace.ApplyMode{
		workspace.ApplySingle,
		workspace.ApplyAll,
		workspace.ApplyNamed,
	}
	seen := map[workspace.ApplyMode]bool{}
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate ApplyMode value: %d", m)
		}
		seen[m] = true
	}
}

// errForTest is a simple error type for test assertions.
type testErr string

func errForTest(msg string) error { return testErr(msg) }
func (e testErr) Error() string   { return string(e) }

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
