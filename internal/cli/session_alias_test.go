package cli

import (
	"os"
	"testing"
)

// TestWorktreeCmd_CanonicalNameAndAlias verifies the parent command was
// renamed to "worktree" while keeping "session" as a backward-compatible
// alias. Both must resolve to the same command tree.
func TestWorktreeCmd_CanonicalNameAndAlias(t *testing.T) {
	if sessionCmd.Use != "worktree" {
		t.Errorf("parent command Use = %q, want %q", sessionCmd.Use, "worktree")
	}
	hasSessionAlias := false
	for _, a := range sessionCmd.Aliases {
		if a == "session" {
			hasSessionAlias = true
		}
	}
	if !hasSessionAlias {
		t.Errorf("parent command missing %q alias; got %v", "session", sessionCmd.Aliases)
	}
	// The renamed parent must still own every subcommand by canonical name.
	want := map[string]bool{
		"create":  false,
		"destroy": false,
		"list":    false,
		"attach":  false,
		"detach":  false,
	}
	for _, sub := range sessionCmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("worktree parent is missing subcommand %q", name)
		}
	}
}

// TestInvokedViaSessionAlias drives the deprecation-notice predicate that
// PersistentPreRun consults. It checks os.Args for the legacy "session"
// token; "worktree" (or no token at all) must not trigger the notice.
func TestInvokedViaSessionAlias(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"session token present", []string{"niwa", "session", "list"}, true},
		{"worktree token present", []string{"niwa", "worktree", "list"}, false},
		{"no subcommand", []string{"niwa"}, false},
		{"session after double dash is ignored", []string{"niwa", "worktree", "create", "--", "session"}, false},
		{"worktree wins over later session arg", []string{"niwa", "worktree", "create", "app", "session"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := os.Args
			os.Args = tc.args
			defer func() { os.Args = orig }()
			if got := invokedViaSessionAlias(); got != tc.want {
				t.Errorf("invokedViaSessionAlias(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
