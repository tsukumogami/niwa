package cli

import (
	"testing"
)

func TestDestroyCmd_HasForceFlag(t *testing.T) {
	flag := destroyCmd.Flags().Lookup("force")
	if flag == nil {
		t.Fatal("expected --force flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default %q, got %q", "false", flag.DefValue)
	}
}

func TestDestroyCmd_AcceptsOptionalPositionalArg(t *testing.T) {
	if err := destroyCmd.Args(destroyCmd, []string{}); err != nil {
		t.Errorf("should accept zero args: %v", err)
	}
	if err := destroyCmd.Args(destroyCmd, []string{"my-instance"}); err != nil {
		t.Errorf("should accept one arg: %v", err)
	}
	if err := destroyCmd.Args(destroyCmd, []string{"a", "b"}); err == nil {
		t.Error("should reject two args")
	}
}
