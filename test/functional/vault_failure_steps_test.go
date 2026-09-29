package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// vault_failure_steps_test.go holds the steps the vault failure scenarios use
// to drive the shared `infisical` stub (writeFakeInfisical): seeding the
// values its export serves, and counting the invocations it logs.

// infisicalInvocationLogVar is the stub's opt-in invocation log variable.
const infisicalInvocationLogVar = "INFISICAL_STUB_INVOCATION_LOG"

// infisicalInvocationLogPath is where a scenario's stub log lives: inside the
// scenario's TMPDIR, so it is discarded with the sandbox.
func infisicalInvocationLogPath(s *testState) string {
	return filepath.Join(s.tmpDir, "infisical-invocations.log")
}

// theInfisicalStubLogsItsInvocations turns the stub's invocation log on for
// every niwa command the scenario runs from here on.
func theInfisicalStubLogsItsInvocations(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides[infisicalInvocationLogVar] = infisicalInvocationLogPath(s)
	return ctx, nil
}

// countInfisicalInvocations counts the logged invocations whose argv starts
// with the given words ("export", "login status").
func countInfisicalInvocations(s *testState, prefix string) (int, string, error) {
	data, err := os.ReadFile(infisicalInvocationLogPath(s))
	if os.IsNotExist(err) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("reading infisical invocation log: %w", err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == prefix || strings.HasPrefix(line, prefix+" ") {
			n++
		}
	}
	return n, string(data), nil
}

// theInfisicalStubLoggedInvocations asserts an exact count of logged
// invocations beginning with the given words.
func theInfisicalStubLoggedInvocations(ctx context.Context, wantStr, prefix string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	want, err := strconv.Atoi(wantStr)
	if err != nil {
		return err
	}
	got, log, err := countInfisicalInvocations(s, prefix)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("infisical stub logged %d %q invocations, want %d; log:\n%s", got, prefix, want, log)
	}
	return nil
}

// theInfisicalStubHoldsSecret seeds a value the stub's export serves for the
// given project and folder in the default "dev" environment, in the same
// layout its `secrets set` writes.
func theInfisicalStubHoldsSecret(ctx context.Context, key, value, project, folder string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.tmpDir, "infisical-stub-store", "secrets", project, "dev"+folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, key), []byte(value), 0o600)
}

func registerVaultFailureSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the infisical stub logs its invocations$`, theInfisicalStubLogsItsInvocations)
	ctx.Step(`^the infisical stub logged (\d+) "([^"]*)" invocations?$`, theInfisicalStubLoggedInvocations)
	ctx.Step(`^the infisical stub holds "([^"]*)" = "([^"]*)" in project "([^"]*)" at path "([^"]*)"$`, theInfisicalStubHoldsSecret)
}
