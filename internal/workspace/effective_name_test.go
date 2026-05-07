package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestEffectiveConfigName_OverrideWins(t *testing.T) {
	t.Parallel()
	state := &InstanceState{ConfigNameOverride: "my-name"}
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "upstream"}}
	got, err := EffectiveConfigName(state, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-name" {
		t.Fatalf("got %q, want %q", got, "my-name")
	}
}

func TestEffectiveConfigName_FallbackToCfgName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label string
		state *InstanceState
	}{
		{"nil_state", nil},
		{"empty_override", &InstanceState{ConfigNameOverride: ""}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "upstream"}}
			got, err := EffectiveConfigName(tc.state, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "upstream" {
				t.Fatalf("got %q, want %q", got, "upstream")
			}
		})
	}
}

func TestEffectiveConfigName_TamperedOverride_Rejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		label    string
		override string
	}{
		{"path_traversal", ".."},
		{"slash", "foo/bar"},
		{"whitespace", "foo bar"},
		{"niwa_marker", ".niwa"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()
			state := &InstanceState{ConfigNameOverride: tc.override}
			cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "upstream"}}
			_, err := EffectiveConfigName(state, cfg)
			if err == nil {
				t.Fatalf("expected error for tampered override %q; got nil", tc.override)
			}
			if !strings.Contains(err.Error(), "ConfigNameOverride") {
				t.Errorf("error %q does not name the field for the next debugger", err.Error())
			}
		})
	}
}

func TestEffectiveConfigName_NilCfgNoOverride_Errors(t *testing.T) {
	t.Parallel()
	_, err := EffectiveConfigName(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil state and nil cfg; got nil")
	}
}

// TestEffectiveConfigName_PreservesIssue1Validation: regression that the
// helper reuses ValidateInitName from issue 1 rather than encoding a
// parallel rule.
func TestEffectiveConfigName_PreservesIssue1Validation(t *testing.T) {
	t.Parallel()
	// Empty override falls through to cfg, so an empty cfg name returns "".
	cfg := &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: ""}}
	got, err := EffectiveConfigName(&InstanceState{}, cfg)
	if err != nil {
		t.Fatalf("unexpected error for empty override + empty cfg: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}

	// Tampered override + empty cfg: helper still rejects because override
	// is non-empty and validation fires first.
	got, err = EffectiveConfigName(&InstanceState{ConfigNameOverride: ".."}, cfg)
	if err == nil {
		t.Fatalf("expected error for tampered override with empty cfg fallback")
	}
	if got != "" {
		t.Errorf("got %q on error, want empty", got)
	}
	// And the wrapped error chain still surfaces the tampered input.
	if !strings.Contains(err.Error(), `".."`) {
		t.Errorf("error %q does not surface tampered input %q", err.Error(), "..")
	}
	// Wrapped error chain stays inspectable: errors.Unwrap should return
	// the underlying ValidateInitName error rather than nil so callers
	// can errors.Is / errors.As against a known sentinel later.
	if errors.Unwrap(err) == nil {
		t.Error("wrapped error has no unwrap target; future errors.Is callers will miss the inner error")
	}
}
