package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateRecordFields_WorktreePath covers the containment rule: only a
// path at or under <instanceRoot>/.niwa/worktrees/ is accepted, and neither a
// `..` component nor an unrelated absolute path gets through.
func TestValidateRecordFields_WorktreePath(t *testing.T) {
	root := t.TempDir()
	worktrees := filepath.Join(root, ".niwa", "worktrees")

	cases := []struct {
		name         string
		worktreePath string
		wantErr      bool
	}{
		{"valid", filepath.Join(worktrees, "niwa-abcd1234"), false},
		{"valid_nested", filepath.Join(worktrees, "niwa-abcd1234", "sub"), false},
		{"worktrees_root_itself", worktrees, false},
		{"empty", "", true},
		{"escapes_via_dotdot", filepath.Join(worktrees, "..", "..", "..", "etc"), true},
		{"escapes_via_dotdot_sibling", filepath.Join(worktrees, "niwa-abcd1234", "..", "..", "sessions"), true},
		{"absolute_elsewhere", "/etc", true},
		{"instance_root_itself", root, true},
		{"prefix_lookalike", worktrees + "-evil/niwa-abcd1234", true},
		{"relative_path", "niwa-abcd1234", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecordFields(root, tc.worktreePath, "session/abcd1234")
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateRecordFields(%q) = nil, want an error", tc.worktreePath)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateRecordFields(%q) = %v, want nil", tc.worktreePath, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "worktree_path") {
				t.Errorf("error does not name the field: %v", err)
			}
		})
	}
}

// TestValidateRecordFields_WorktreePathSymlink pins the two symlink outcomes: a
// symlinked instance root must not cause a false refusal, and a symlink planted
// inside the worktrees directory must not redirect teardown out of the
// instance.
func TestValidateRecordFields_WorktreePathSymlink(t *testing.T) {
	t.Run("symlinked_root_is_accepted", func(t *testing.T) {
		real := t.TempDir()
		worktree := filepath.Join(real, ".niwa", "worktrees", "niwa-abcd1234")
		if err := os.MkdirAll(worktree, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "root-link")
		if err := os.Symlink(real, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		// Root spelled through the link, path spelled through the real dir:
		// resolution on both sides has to agree for this to pass.
		if err := ValidateRecordFields(link, worktree, "session/abcd1234"); err != nil {
			t.Errorf("symlinked instance root refused: %v", err)
		}
		if err := ValidateRecordFields(link, filepath.Join(link, ".niwa", "worktrees", "niwa-abcd1234"), "session/abcd1234"); err != nil {
			t.Errorf("path spelled through the symlinked root refused: %v", err)
		}
	})

	t.Run("symlink_out_of_worktrees_is_refused", func(t *testing.T) {
		root := t.TempDir()
		worktrees := filepath.Join(root, ".niwa", "worktrees")
		if err := os.MkdirAll(worktrees, 0o700); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		planted := filepath.Join(worktrees, "niwa-abcd1234")
		if err := os.Symlink(outside, planted); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := ValidateRecordFields(root, planted, "session/abcd1234"); err == nil {
			t.Error("a symlink pointing out of the worktrees directory was accepted")
		}
	})
}

// TestValidateRecordFields_BranchName covers the ref-name rules. The refused
// names are the shapes a crafted record would use: an option git would read
// from the argv position, a revision-syntax name `git branch -d` resolves to
// another branch, and names carrying characters that are invalid in a ref or
// invisible in the warning line destroy prints.
func TestValidateRecordFields_BranchName(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, ".niwa", "worktrees", "niwa-abcd1234")

	cases := []struct {
		name       string
		branchName string
		wantErr    bool
	}{
		{"session_default", "session/abcd1234", false},
		{"bootstrap_prefix", "niwa-bootstrap/abcd1234", false},
		{"plain", "main", false},
		{"dash_inside", "feature/add-thing", false},
		{"dot_inside", "v1.2.3", false},
		{"empty", "", true},
		{"option_injection", "--upload-pack=/tmp/x", true},
		{"previous_branch_revsyntax", "@{-1}", true},
		{"leading_dash", "-delete-me", true},
		{"escape_byte", "session/abc\x1b[31m1234", true},
		{"bidi_override", "session/abc‮1234", true},
		{"del_byte", "session/abc\x7f1234", true},
		{"space", "session/abc 1234", true},
		{"double_dot", "foo..bar", true},
		{"lock_suffix", "foo.lock", true},
		{"leading_slash", "/foo", true},
		{"trailing_slash", "foo/", true},
		{"double_slash", "a//b", true},
		{"bare_at", "@", true},
		{"tilde", "foo~1", true},
		{"caret", "foo^", true},
		{"colon", "foo:bar", true},
		{"question", "foo?", true},
		{"star", "foo*", true},
		{"bracket", "foo[1]", true},
		{"backslash", "foo\\bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRecordFields(root, valid, tc.branchName)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateRecordFields(branch=%q) = nil, want an error", tc.branchName)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateRecordFields(branch=%q) = %v, want nil", tc.branchName, err)
			}
			if tc.wantErr && err != nil && !strings.Contains(err.Error(), "branch_name") {
				t.Errorf("error does not name the field: %v", err)
			}
		})
	}
}

// TestValidateRecordFields_AcceptsEffectiveBranchName asserts the validator
// never refuses a name niwa itself writes. The shapes come from
// EffectiveBranchName: the recorded BranchName when set (CreateSession builds
// it as BranchPrefix + session ID, with "niwa-bootstrap/" the one non-default
// prefix in the tree) and the `session/<sid>` fallback for pre-v1.1 records
// that have no BranchName at all.
func TestValidateRecordFields_AcceptsEffectiveBranchName(t *testing.T) {
	root := t.TempDir()
	worktreePath := filepath.Join(root, ".niwa", "worktrees", "niwa-abcd1234")

	sids := []string{"abcd1234", "00000000", "ffffffff"}
	prefixes := []string{"", "session/", "niwa-bootstrap/"}

	for _, sid := range sids {
		for _, prefix := range prefixes {
			branch := ""
			if prefix != "" {
				branch = prefix + sid
			}
			state := NewSessionLifecycleState(sid, "niwa", "test", "", worktreePath, branch)
			effective := state.EffectiveBranchName()
			if err := ValidateRecordFields(root, worktreePath, effective); err != nil {
				t.Errorf("EffectiveBranchName() = %q refused: %v", effective, err)
			}
		}
	}
}

// TestDestroySession_RefusesCraftedRecord covers the three ways a record that
// niwa did not write is turned away before git runs: a worktree path outside
// the instance, a branch name git would read as an option, and a worktree
// directory that is simply not there (which the dirty guard used to report as
// clean).
func TestDestroySession_RefusesCraftedRecord(t *testing.T) {
	seed := func(t *testing.T, worktreePath, branchName string) (root, sid string) {
		t.Helper()
		root = t.TempDir()
		sessionsDir := filepath.Join(root, ".niwa", "sessions")
		if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		sid = "abcd1234"
		state := NewSessionLifecycleState(sid, "myrepo", "crafted record", "", worktreePath, branchName)
		if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
			t.Fatalf("WriteSessionLifecycleState: %v", err)
		}
		return root, sid
	}

	t.Run("worktree_path_outside_instance", func(t *testing.T) {
		outside := t.TempDir()
		root, sid := seed(t, outside, "session/abcd1234")
		inv := &recordingInvoker{}
		state, err := DestroySession(context.Background(), root, sid, true /* force */, inv)
		if err == nil {
			t.Fatal("crafted worktree_path was accepted")
		}
		if len(inv.calls) != 0 {
			t.Errorf("git ran despite the refusal: %v", inv.calls)
		}
		if state.Status == SessionStatusEnded {
			t.Error("status must not advance to ended when destroy is refused")
		}
	})

	t.Run("branch_name_reads_as_option", func(t *testing.T) {
		root := t.TempDir()
		worktreePath := filepath.Join(root, ".niwa", "worktrees", "myrepo-abcd1234")
		if err := os.MkdirAll(worktreePath, 0o700); err != nil {
			t.Fatal(err)
		}
		sessionsDir := filepath.Join(root, ".niwa", "sessions")
		if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		state := NewSessionLifecycleState("abcd1234", "myrepo", "crafted record", "", worktreePath, "--upload-pack=/tmp/x")
		if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
			t.Fatalf("WriteSessionLifecycleState: %v", err)
		}
		inv := &recordingInvoker{}
		if _, err := DestroySession(context.Background(), root, "abcd1234", true /* force */, inv); err == nil {
			t.Fatal("crafted branch_name was accepted")
		}
		if len(inv.calls) != 0 {
			t.Errorf("git ran despite the refusal: %v", inv.calls)
		}
	})

	t.Run("missing_worktree_directory", func(t *testing.T) {
		root := t.TempDir()
		// Inside the instance, so containment passes -- but never created, so
		// the dirty guard cannot verify it is clean.
		worktreePath := filepath.Join(root, ".niwa", "worktrees", "myrepo-abcd1234")
		sessionsDir := filepath.Join(root, ".niwa", "sessions")
		if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		state := NewSessionLifecycleState("abcd1234", "myrepo", "missing worktree", "", worktreePath, "session/abcd1234")
		if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
			t.Fatalf("WriteSessionLifecycleState: %v", err)
		}
		inv := &recordingInvoker{}
		gotState, err := DestroySession(context.Background(), root, "abcd1234", false /* force */, inv)
		if err == nil {
			t.Fatal("a missing worktree directory was reported clean and destroyed")
		}
		if gotState.Status == SessionStatusEnded {
			t.Error("status must not advance to ended when destroy is refused")
		}
	})
}

// TestDestroySession_BranchArgvUsesEndOfOptions asserts the branch name reaches
// git after `--`, so the argv position cannot be read as an option even if a
// name slips past validation.
func TestDestroySession_BranchArgvUsesEndOfOptions(t *testing.T) {
	root := t.TempDir()
	repoPath := filepath.Join(root, "group", "myrepo")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	worktreePath := filepath.Join(root, ".niwa", "worktrees", "myrepo-abcd1234")
	sid := seedActiveSession(t, root, "myrepo", worktreePath)

	inv := &recordingInvoker{}
	if _, err := DestroySession(context.Background(), root, sid, true /* force */, inv); err != nil {
		t.Fatalf("DestroySession: %v", err)
	}

	var sawWorktreeRemove, sawBranchDelete bool
	for _, call := range inv.calls {
		switch {
		case contains(call, "worktree") && contains(call, "remove"):
			sawWorktreeRemove = true
			if call[len(call)-1] != worktreePath {
				t.Errorf("worktree remove argv does not end with the worktree path: %v", call)
			}
		case contains(call, "branch"):
			sawBranchDelete = true
			if len(call) < 2 || call[len(call)-2] != "--" {
				t.Errorf("branch argv does not pass the name after --: %v", call)
			}
			if call[len(call)-1] != "session/"+sid {
				t.Errorf("branch argv name = %q, want %q", call[len(call)-1], "session/"+sid)
			}
		}
	}
	if !sawWorktreeRemove {
		t.Errorf("no worktree remove call recorded: %v", inv.calls)
	}
	if !sawBranchDelete {
		t.Errorf("no branch delete call recorded: %v", inv.calls)
	}
}

// contains reports whether argv holds the exact argument arg.
func contains(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}
