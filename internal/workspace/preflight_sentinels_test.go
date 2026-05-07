package workspace

import (
	"errors"
	"testing"
)

// TestNewSentinels_DistinctFromExisting verifies the new sentinels are not
// confused with each other or with the existing init-conflict sentinels.
func TestNewSentinels_DistinctFromExisting(t *testing.T) {
	t.Parallel()
	all := []error{
		ErrWorkspaceExists,
		ErrInsideInstance,
		ErrNiwaDirectoryExists,
		ErrTargetDirExists,
		ErrRegistryNameInUse,
	}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("errors.Is(%v, %v) = true, want false (distinct sentinels)", a, b)
			}
		}
	}
}

// TestNewSentinels_WrapThroughInitConflictError verifies each new sentinel
// can be wrapped in InitConflictError such that errors.Is recovers it.
func TestNewSentinels_WrapThroughInitConflictError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
	}{
		{"ErrTargetDirExists", ErrTargetDirExists},
		{"ErrRegistryNameInUse", ErrRegistryNameInUse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wrapped := &InitConflictError{
				Err:        tc.err,
				Detail:     "detail",
				Suggestion: "suggestion",
			}
			if !errors.Is(wrapped, tc.err) {
				t.Fatalf("errors.Is(%T, %v) = false, want true", wrapped, tc.err)
			}
		})
	}
}

// TestErrTargetDirExists_DoesNotMatchWorkspaceExists ensures issue 2's
// sub-case routing logic can rely on the sentinels being distinct: a
// generic target-exists error must not satisfy errors.Is for the more
// specific niwa-aware sentinels.
func TestErrTargetDirExists_DoesNotMatchWorkspaceExists(t *testing.T) {
	t.Parallel()
	wrapped := &InitConflictError{Err: ErrTargetDirExists}
	if errors.Is(wrapped, ErrWorkspaceExists) {
		t.Error("ErrTargetDirExists wraps satisfy errors.Is(ErrWorkspaceExists); want false")
	}
	if errors.Is(wrapped, ErrNiwaDirectoryExists) {
		t.Error("ErrTargetDirExists wraps satisfy errors.Is(ErrNiwaDirectoryExists); want false")
	}
}
