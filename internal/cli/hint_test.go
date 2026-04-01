package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHintShellInit_Suppressed(t *testing.T) {
	t.Setenv("_NIWA_SHELL_INIT", "1")

	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	hintShellInit(cmd)

	if stderr.Len() != 0 {
		t.Errorf("expected no hint when _NIWA_SHELL_INIT is set, got %q", stderr.String())
	}
}

func TestHintShellInit_Shown(t *testing.T) {
	t.Setenv("_NIWA_SHELL_INIT", "")

	cmd := &cobra.Command{}
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)

	hintShellInit(cmd)

	output := stderr.String()
	if !strings.Contains(output, "shell integration not detected") {
		t.Errorf("expected hint message, got %q", output)
	}
	if !strings.Contains(output, "niwa shell-init install") {
		t.Errorf("expected install instruction in hint, got %q", output)
	}
}

func TestValidateStdoutPath_AbsolutePass(t *testing.T) {
	if err := validateStdoutPath("/home/user/workspace"); err != nil {
		t.Errorf("expected no error for absolute path, got %v", err)
	}
}

func TestValidateStdoutPath_RelativeFails(t *testing.T) {
	if err := validateStdoutPath("relative/path"); err == nil {
		t.Error("expected error for relative path")
	}
}

func TestValidateStdoutPath_NewlineFails(t *testing.T) {
	if err := validateStdoutPath("/home/user/work\nspace"); err == nil {
		t.Error("expected error for path with newline")
	}
}
