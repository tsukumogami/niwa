package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// syntheticLaunchSpec is a launch description shaped unlike the one niwa ships
// today: a subcommand and a fixed policy flag ahead of the pass-through, a
// prompt separator, and different flag spellings. It exists so the argv builder
// is exercised against two shapes rather than against the only shape that
// happens to be declared, which is what makes it a builder rather than one
// agent's argv written in a loop.
func syntheticLaunchSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:          "othertool",
		LeadingArgs:     []string{"exec", "--headless"},
		PromptSeparator: true,
		Flags: agentplan.LaunchFlags{
			Model:        "-m",
			DisplayName:  "--label",
			SubagentType: "",
		},
	}
}

// TestBuildLaunchArgs_Order verifies the leading arguments come first,
// pass-through values sit in the middle as separate elements, and the prompt is
// the last single element.
func TestBuildLaunchArgs_Order(t *testing.T) {
	got := buildLaunchArgs(claudeLaunchSpec(), "do the thing", []string{"--model", "opus", "--permission-mode", "acceptEdits"})
	want := []string{"--bg", "--model", "opus", "--permission-mode", "acceptEdits", "do the thing"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_NoPassthrough verifies the minimal argv.
func TestBuildLaunchArgs_NoPassthrough(t *testing.T) {
	got := buildLaunchArgs(claudeLaunchSpec(), "hi", nil)
	want := []string{"--bg", "hi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_SeparatorShape verifies a spec that declares a prompt
// separator gets one, immediately before the prompt and after everything else.
// Without it, an agent whose parser reads a leading dash as a flag would take a
// prompt beginning with one as an unknown argument.
func TestBuildLaunchArgs_SeparatorShape(t *testing.T) {
	got := buildLaunchArgs(syntheticLaunchSpec(), "--looks-like-a-flag", []string{"-m", "fast"})
	want := []string{"exec", "--headless", "-m", "fast", "--", "--looks-like-a-flag"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildLaunchArgs = %#v, want %#v", got, want)
	}
}

// TestBuildLaunchArgs_PromptRemainsSingleElement verifies a prompt full of
// shell metacharacters, quotes, spaces, and a newline stays one argv element
// and never bleeds into a flag position -- the flag-injection guard (D8). It
// runs against both shapes, because the guard has to hold for whichever agent
// is launched and not just for the one whose argv was written first.
func TestBuildLaunchArgs_PromptRemainsSingleElement(t *testing.T) {
	prompt := "fix the bug; rm -rf / --no-preserve-root\n--malicious 'quoted \"value\"' && echo pwned"

	for _, tc := range []struct {
		name        string
		spec        agentplan.LaunchSpec
		passthrough []string
		wantLen     int
		wantLead    []string
	}{
		{
			name:        "claude",
			spec:        claudeLaunchSpec(),
			passthrough: []string{"--agent", "reviewer"},
			wantLen:     4,
			wantLead:    []string{"--bg", "--agent", "reviewer"},
		},
		{
			name:        "synthetic",
			spec:        syntheticLaunchSpec(),
			passthrough: []string{"-m", "fast"},
			wantLen:     6,
			wantLead:    []string{"exec", "--headless", "-m", "fast", "--"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildLaunchArgs(tc.spec, prompt, tc.passthrough)
			if len(got) != tc.wantLen {
				t.Fatalf("got %d args, want %d: %#v", len(got), tc.wantLen, got)
			}
			if !reflect.DeepEqual(got[:len(tc.wantLead)], tc.wantLead) {
				t.Errorf("leading argv = %#v, want %#v", got[:len(tc.wantLead)], tc.wantLead)
			}
			if got[len(got)-1] != prompt {
				t.Errorf("prompt mangled: last element = %q, want %q", got[len(got)-1], prompt)
			}
		})
	}
}

// TestRealDispatchLaunch_EmptyPromptRejected verifies an empty prompt is
// rejected before any exec (R43). It does not depend on the binary being
// present.
func TestRealDispatchLaunch_EmptyPromptRejected(t *testing.T) {
	err := realDispatchLaunch(context.Background(), claudeLaunchSpec(), t.TempDir(), "", "", nil, nil)
	if err == nil {
		t.Fatal("expected an error for an empty prompt, got nil")
	}
}
