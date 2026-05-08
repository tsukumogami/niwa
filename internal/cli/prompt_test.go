package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReadConfirmation_Match(t *testing.T) {
	in := strings.NewReader("myws\n")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Type the workspace name: ", "myws", in, &out)
	if err != nil {
		t.Fatalf("ReadConfirmation: %v", err)
	}
	if !ok {
		t.Errorf("expected match")
	}
	if !strings.Contains(out.String(), "Type the workspace name:") {
		t.Errorf("expected prompt in output; got: %q", out.String())
	}
}

func TestReadConfirmation_Mismatch(t *testing.T) {
	in := strings.NewReader("not-the-name\n")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Confirm: ", "myws", in, &out)
	if err != nil {
		t.Fatalf("ReadConfirmation: %v", err)
	}
	if ok {
		t.Errorf("expected mismatch")
	}
}

func TestReadConfirmation_TrimsSurroundingWhitespace(t *testing.T) {
	in := strings.NewReader("  myws  \n")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Confirm: ", "myws", in, &out)
	if err != nil {
		t.Fatalf("ReadConfirmation: %v", err)
	}
	if !ok {
		t.Errorf("expected match after whitespace trim")
	}
}

func TestReadConfirmation_EmptyInputIsMismatch(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Confirm: ", "myws", in, &out)
	if err != nil {
		t.Fatalf("ReadConfirmation: %v", err)
	}
	if ok {
		t.Errorf("empty input should be a mismatch, not a match")
	}
}

func TestReadConfirmation_EOFNoLineIsError(t *testing.T) {
	in := strings.NewReader("")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Confirm: ", "myws", in, &out)
	if err == nil {
		t.Fatalf("expected error on EOF with no line")
	}
	if ok {
		t.Errorf("expected mismatch")
	}
	if !errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "EOF") {
		t.Errorf("error should reference EOF; got: %v", err)
	}
}

func TestReadConfirmation_EOFWithLineCountsAsLine(t *testing.T) {
	// A reader that returns "myws" + EOF without a trailing newline.
	in := strings.NewReader("myws")
	var out bytes.Buffer
	ok, err := ReadConfirmation("Confirm: ", "myws", in, &out)
	if err != nil {
		t.Fatalf("ReadConfirmation: %v", err)
	}
	if !ok {
		t.Errorf("expected match for 'myws' followed by EOF")
	}
}
