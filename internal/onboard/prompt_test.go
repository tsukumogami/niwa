package onboard

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestConfirm_DefaultOnEnter(t *testing.T) {
	in := strings.NewReader("\n")
	var out strings.Builder

	got, err := Confirm("Proceed?", true, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Errorf("got %v, want true (default) on bare Enter", got)
	}
	if !strings.Contains(out.String(), "[Y/n]") {
		t.Errorf("prompt %q does not show the stated default", out.String())
	}
}

func TestConfirm_DefaultNoOnEnter(t *testing.T) {
	in := strings.NewReader("\n")
	var out strings.Builder

	got, err := Confirm("Proceed?", false, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Errorf("got %v, want false (default) on bare Enter", got)
	}
	if !strings.Contains(out.String(), "[y/N]") {
		t.Errorf("prompt %q does not show the stated default", out.String())
	}
}

func TestConfirm_ExplicitYesNo(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{"n\n", false},
		{"N\n", false},
		{"no\n", false},
	}
	for _, c := range cases {
		got, err := Confirm("Proceed?", true, strings.NewReader(c.input), io.Discard)
		if err != nil {
			t.Fatalf("input %q: unexpected error: %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("input %q: got %v, want %v", c.input, got, c.want)
		}
	}
}

func TestConfirm_RepromptsOnInvalidInput(t *testing.T) {
	in := strings.NewReader("banana\nY\n")
	var out strings.Builder

	got, err := Confirm("Proceed?", false, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Errorf("got %v, want true after re-prompt accepted Y", got)
	}
	if strings.Count(out.String(), "Proceed?") != 2 {
		t.Errorf("prompt shown %d times, want 2 (initial + reprompt): %q", strings.Count(out.String(), "Proceed?"), out.String())
	}
}

func TestConfirm_EOFOnEmptyInputIsError(t *testing.T) {
	_, err := Confirm("Proceed?", true, strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("expected an error on immediate EOF")
	}
}

func TestConfirm_EOFAfterContentIsValidAnswer(t *testing.T) {
	// No trailing newline: ReadString hits EOF but the buffered line
	// ("y") is non-empty, so it's treated as a valid final answer --
	// the terminal closed right after Enter.
	got, err := Confirm("Proceed?", false, strings.NewReader("y"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Errorf("got %v, want true", got)
	}
}

func TestSelect_ValidChoice(t *testing.T) {
	opts := []Option{{Label: "Team", Value: "team"}, {Label: "Individual", Value: "individual"}}
	got, err := Select("Pick one", opts, strings.NewReader("2\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "individual" {
		t.Errorf("got %q, want individual", got)
	}
}

func TestSelect_RepromptsOnOutOfRangeOrUnparseable(t *testing.T) {
	opts := []Option{{Label: "Team", Value: "team"}, {Label: "Individual", Value: "individual"}}
	in := strings.NewReader("bogus\n9\n1\n")
	var out strings.Builder

	got, err := Select("Pick one", opts, in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "team" {
		t.Errorf("got %q, want team", got)
	}
}

func TestSelect_RequiresAtLeastOneOption(t *testing.T) {
	_, err := Select("Pick one", nil, strings.NewReader("1\n"), io.Discard)
	if err == nil {
		t.Fatal("expected an error for zero options")
	}
}

func TestSelect_EOFOnEmptyInputIsError(t *testing.T) {
	opts := []Option{{Label: "Team", Value: "team"}}
	_, err := Select("Pick one", opts, strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("expected an error on immediate EOF")
	}
}

func TestPause_ReadsAndDiscardsOneLine(t *testing.T) {
	err := Pause("Press Enter once ready", strings.NewReader("anything at all\n"), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPause_EOFOnEmptyInputIsError(t *testing.T) {
	err := Pause("Press Enter once ready", strings.NewReader(""), io.Discard)
	if err == nil {
		t.Fatal("expected an error on immediate EOF")
	}
}

func TestGate_TTYPasses(t *testing.T) {
	orig := IsStdinTTY
	defer func() { IsStdinTTY = orig }()
	IsStdinTTY = func() bool { return true }

	if err := Gate(false); err != nil {
		t.Fatalf("unexpected error with a TTY: %v", err)
	}
}

func TestGate_NonTTYWithoutOverrideFails(t *testing.T) {
	orig := IsStdinTTY
	defer func() { IsStdinTTY = orig }()
	IsStdinTTY = func() bool { return false }

	err := Gate(false)
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("err = %v, want ErrNonInteractive", err)
	}
}

func TestGate_NonTTYWithOverridePasses(t *testing.T) {
	orig := IsStdinTTY
	defer func() { IsStdinTTY = orig }()
	IsStdinTTY = func() bool { return false }

	if err := Gate(true); err != nil {
		t.Fatalf("unexpected error with override: %v", err)
	}
}

func TestConfirm_PromptIsSanitized(t *testing.T) {
	var out strings.Builder
	_, err := Confirm("Identity: \x1b[31mred\x1b[0m", true, strings.NewReader("\n"), &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("raw ESC byte reached output: %q", out.String())
	}
}
