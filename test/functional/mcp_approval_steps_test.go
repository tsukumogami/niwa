package functional

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cucumber/godog"
)

// mcp_approval_steps_test.go holds the steps that read the approval list an
// instance root's settings.json carries for the MCP servers niwa generated
// into that tree's .mcp.json. They read the files the real binary wrote,
// never a hand-written fixture.

// enabledMcpjsonServersKey is the Claude Code settings key the steps read. It
// is spelled out rather than imported because the functional suite exercises
// the compiled binary and should notice if the spelling it emits drifts.
const enabledMcpjsonServersKey = "enabledMcpjsonServers"

// instanceSettingsPath is the instance root's settings document.
func instanceSettingsPath(ctx context.Context, instance string) (string, error) {
	s := getState(ctx)
	if s == nil {
		return "", fmt.Errorf("no test state")
	}
	return filepath.Join(s.workspaceRoot, instance, ".claude", "settings.json"), nil
}

// theInstanceSettingsApproveMCPServers asserts the instance root's settings
// document approves exactly the comma-separated names, in that order. The
// value must be a JSON array whose every element is a string: anything else
// makes Claude Code reject the whole settings file.
func theInstanceSettingsApproveMCPServers(ctx context.Context, instance, csv string) error {
	path, err := instanceSettingsPath(ctx, instance)
	if err != nil {
		return err
	}
	value, found, err := lookupJSONKey(path, enabledMcpjsonServersKey)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s has no %q", path, enabledMcpjsonServersKey)
	}
	list, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s %q = %v (%T), want a JSON array", path, enabledMcpjsonServersKey, value, value)
	}
	got := make([]string, 0, len(list))
	for i, elem := range list {
		name, ok := elem.(string)
		if !ok {
			return fmt.Errorf("%s %q[%d] = %v (%T), want a string", path, enabledMcpjsonServersKey, i, elem, elem)
		}
		got = append(got, name)
	}
	want := strings.Split(csv, ",")
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("%s %q = %v, want %v", path, enabledMcpjsonServersKey, got, want)
	}
	return nil
}

// theInstanceSettingsApproveNoMCPServers asserts the instance root's settings
// document carries neither the named approval list nor the wholesale
// approval, which niwa never writes.
func theInstanceSettingsApproveNoMCPServers(ctx context.Context, instance string) error {
	path, err := instanceSettingsPath(ctx, instance)
	if err != nil {
		return err
	}
	for _, key := range []string{enabledMcpjsonServersKey, "enableAllProjectMcpServers"} {
		value, found, err := lookupJSONKey(path, key)
		if err != nil {
			return err
		}
		if found {
			return fmt.Errorf("%s carries %q = %v, want no MCP approval", path, key, value)
		}
	}
	return nil
}

func registerMCPApprovalSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the instance "([^"]*)" settings document approves MCP servers "([^"]*)"$`, theInstanceSettingsApproveMCPServers)
	ctx.Step(`^the instance "([^"]*)" settings document approves no MCP servers$`, theInstanceSettingsApproveNoMCPServers)
}
