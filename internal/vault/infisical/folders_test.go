package infisical

import (
	"context"
	"errors"
	"testing"
)

// folders_test.go reuses fakeCommander (defined in infisical_test.go,
// same package), the same double session_test.go and
// TestDetectSessionStatus_* rely on.

func TestCreateSecretsFolder_Success(t *testing.T) {
	c := &fakeCommander{exitCode: 0}
	err := CreateSecretsFolder(context.Background(), c, "proj-1", "dev", "/team")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.capturedName != "infisical" {
		t.Errorf("capturedName = %q, want infisical", c.capturedName)
	}
	wantArgs := []string{"secrets", "folders", "create", "--projectId", "proj-1", "--env", "dev", "--path", "/team"}
	if len(c.capturedArgs) != len(wantArgs) {
		t.Fatalf("capturedArgs = %v, want %v", c.capturedArgs, wantArgs)
	}
	for i, want := range wantArgs {
		if c.capturedArgs[i] != want {
			t.Errorf("capturedArgs[%d] = %q, want %q", i, c.capturedArgs[i], want)
		}
	}
}

func TestCreateSecretsFolder_DefaultsPathToRoot(t *testing.T) {
	c := &fakeCommander{exitCode: 0}
	if err := CreateSecretsFolder(context.Background(), c, "proj-1", "dev", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for i, a := range c.capturedArgs {
		if a == "--path" && i+1 < len(c.capturedArgs) {
			found = true
			if c.capturedArgs[i+1] != "/" {
				t.Errorf("path arg = %q, want /", c.capturedArgs[i+1])
			}
		}
	}
	if !found {
		t.Error("--path flag not found in argv")
	}
}

func TestCreateSecretsFolder_PlanGated(t *testing.T) {
	c := &fakeCommander{
		exitCode: 1,
		stderr:   []byte("error: plan does not allow additional folders"),
	}
	err := CreateSecretsFolder(context.Background(), c, "proj-1", "dev", "/team")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrPlanGated) {
		t.Errorf("err = %v, want wrapping ErrPlanGated", err)
	}
}

func TestCreateSecretsFolder_GenericFailureNotPlanGated(t *testing.T) {
	c := &fakeCommander{
		exitCode: 1,
		stderr:   []byte("error: folder create failed"),
	}
	err := CreateSecretsFolder(context.Background(), c, "proj-1", "dev", "/team")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrPlanGated) {
		t.Error("a generic failure must not be classified as ErrPlanGated")
	}
}

func TestCreateSecretsFolder_StartFailure(t *testing.T) {
	c := &fakeCommander{runErr: errStartFailureForTest}
	err := CreateSecretsFolder(context.Background(), c, "proj-1", "dev", "/team")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSecretsFolder_RequiresProjectIDAndEnv(t *testing.T) {
	c := &fakeCommander{}
	if err := CreateSecretsFolder(context.Background(), c, "", "dev", "/team"); err == nil {
		t.Error("expected error for empty projectID")
	}
	if err := CreateSecretsFolder(context.Background(), c, "proj-1", "", "/team"); err == nil {
		t.Error("expected error for empty env")
	}
	if c.callCount != 0 {
		t.Errorf("callCount = %d, want 0 (validation must fail before any subprocess call)", c.callCount)
	}
}

func TestCreateSecretsFolder_NilCommanderDoesNotPanic(t *testing.T) {
	_ = CreateSecretsFolder(context.Background(), nil, "proj-1", "dev", "/team")
}
