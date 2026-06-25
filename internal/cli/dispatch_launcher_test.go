package cli

import (
	"context"
	"reflect"
	"testing"
)

// TestBuildClaudeBgArgs_Order verifies --bg is first, pass-through values sit
// in the middle as separate elements, and the prompt is the last single
// element.
func TestBuildClaudeBgArgs_Order(t *testing.T) {
	got := buildClaudeBgArgs("do the thing", []string{"--model", "opus", "--permission-mode", "acceptEdits"})
	want := []string{"--bg", "--model", "opus", "--permission-mode", "acceptEdits", "do the thing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildClaudeBgArgs = %#v, want %#v", got, want)
	}
}

// TestBuildClaudeBgArgs_NoPassthrough verifies the minimal argv.
func TestBuildClaudeBgArgs_NoPassthrough(t *testing.T) {
	got := buildClaudeBgArgs("hi", nil)
	want := []string{"--bg", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildClaudeBgArgs = %#v, want %#v", got, want)
	}
}

// TestBuildClaudeBgArgs_PromptRemainsSingleElement verifies a prompt full of
// shell metacharacters, quotes, spaces, and a newline stays one argv element
// and never bleeds into a flag position -- the flag-injection guard (D8).
func TestBuildClaudeBgArgs_PromptRemainsSingleElement(t *testing.T) {
	prompt := "fix the bug; rm -rf / --no-preserve-root\n--malicious 'quoted \"value\"' && echo pwned"
	got := buildClaudeBgArgs(prompt, []string{"--agent", "reviewer"})

	if len(got) != 4 {
		t.Fatalf("got %d args, want 4: %#v", len(got), got)
	}
	if got[0] != "--bg" {
		t.Errorf("args[0] = %q, want --bg", got[0])
	}
	if got[1] != "--agent" || got[2] != "reviewer" {
		t.Errorf("pass-through not preserved: %#v", got[1:3])
	}
	if got[len(got)-1] != prompt {
		t.Errorf("prompt mangled: last element = %q, want %q", got[len(got)-1], prompt)
	}
}

// TestRealDispatchLaunch_EmptyPromptRejected verifies an empty prompt is
// rejected before any exec (R43). It does not depend on claude being present.
func TestRealDispatchLaunch_EmptyPromptRejected(t *testing.T) {
	err := realDispatchLaunch(context.Background(), t.TempDir(), "", nil)
	if err == nil {
		t.Fatal("expected an error for an empty prompt, got nil")
	}
}
