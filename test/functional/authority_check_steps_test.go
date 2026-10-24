package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// The authority check scenarios run `niwa authority check` the way a gate
// would: with a session id and an absolute directory in the instance, never
// relying on the working directory. Commands name directories through
// placeholders, so a scenario reads without the random instance suffix:
//
//	{instance}        the last dispatch instance
//	{first-instance}  the instance kept as the first dispatch
//	{worktree}        the last niwa worktree created
//	{workspace-root}  the workspace root
//	{outside}         a directory in no workspace

func registerAuthorityCheckSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I run the check "([^"]*)"$`, iRunTheCheck)
	ctx.Step(`^the check document is:$`, theCheckDocumentIs)
	ctx.Step(`^the check prints the same for session "([^"]*)" from "([^"]*)" in the dispatch instance, from the worktree and from the instance root$`, theCheckPrintsTheSameFromEveryAnchor)
	ctx.Step(`^the worker's session mapping is rewritten without its origin$`, theWorkersMappingLosesItsOrigin)
	ctx.Step(`^the worker's session mapping is rewritten to name the first dispatch instance$`, theWorkersMappingNamesTheFirstInstance)
	ctx.Step(`^the worker's session mapping is rewritten to name dispatch "([^"]*)"$`, theWorkersMappingNamesDispatch)
	ctx.Step(`^the dispatch instance's authority record is deleted$`, theDispatchInstancesRecordIsDeleted)
}

// checkPlaceholders expands the placeholders in command. One whose value an
// earlier step never set is an error, not an empty string: an empty -C would
// still exit 1 and pass a scenario that expects a refusal for another reason.
func checkPlaceholders(s *testState, command string) (string, error) {
	values := map[string]string{
		"{instance}":       s.lastDispatchInstancePath,
		"{first-instance}": s.authority.firstInstance,
		"{worktree}":       s.lastSessionWorktreePath,
		"{workspace-root}": s.workspaceRoot,
	}
	if strings.Contains(command, "{outside}") {
		outside := filepath.Join(s.sandbox, "outside-every-workspace")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			return "", err
		}
		values["{outside}"] = outside
	}
	for name, value := range values {
		if !strings.Contains(command, name) {
			continue
		}
		if value == "" {
			return "", fmt.Errorf("%s is used before any step set it", name)
		}
		command = strings.ReplaceAll(command, name, value)
	}
	return command, nil
}

// iRunTheCheck runs the command from the workspace root's parent, a
// directory in no instance, so the answer can only come from -C.
func iRunTheCheck(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	command, err := checkPlaceholders(s, command)
	if err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, filepath.Dir(s.workspaceRoot), command)
}

// theCheckDocumentIs compares the last check's stdout with want, every
// dispatch id written as <dispatch-id>, after checking it exited 0.
func theCheckDocumentIs(ctx context.Context, want *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.exitCode != 0 {
		return fmt.Errorf("the check exited %d\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	got := recordDispatchIDs.ReplaceAllString(s.stdout, `"${1}dispatch_id": "<dispatch-id>"`)
	if strings.TrimSpace(got) != strings.TrimSpace(want.Content) {
		return fmt.Errorf("check printed\n%s\nwant\n%s", got, want.Content)
	}
	return nil
}

func theCheckPrintsTheSameFromEveryAnchor(ctx context.Context, sessionID, rel string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree created")
	}
	check := func(dir string) (string, error) {
		if err := runNiwa(s, filepath.Dir(s.workspaceRoot), "niwa authority check --json --session "+sessionID+" -C "+dir+" --point merge-pr"); err != nil {
			return "", err
		}
		if s.exitCode != 0 {
			return "", fmt.Errorf("the check from %s exited %d\n%s%s", dir, s.exitCode, s.stdout, s.stderr)
		}
		return s.stdout, nil
	}
	want, err := check(s.lastDispatchInstancePath)
	if err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(s.lastDispatchInstancePath, rel), s.lastSessionWorktreePath} {
		got, err := check(dir)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("from %s the check printed\n%s\nfrom the instance root\n%s", dir, got, want)
		}
	}
	return nil
}

// rewriteWorkerMapping edits the last dispatch's session mapping, member by
// member, keeping every other member as dispatch wrote it.
func rewriteWorkerMapping(s *testState, edit func(m map[string]any)) error {
	sid, err := mappingFor(s, s.lastDispatchInstancePath)
	if err != nil {
		return err
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sid+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	edit(m)
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func theWorkersMappingLosesItsOrigin(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, rewriteWorkerMapping(s, func(m map[string]any) { delete(m, "origin") })
}

func theWorkersMappingNamesTheFirstInstance(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.authority.firstInstance == "" {
		return ctx, fmt.Errorf("no first dispatch kept")
	}
	return ctx, rewriteWorkerMapping(s, func(m map[string]any) {
		m["instance_path"] = s.authority.firstInstance
		m["instance_name"] = filepath.Base(s.authority.firstInstance)
	})
}

func theWorkersMappingNamesDispatch(ctx context.Context, dispatchID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, rewriteWorkerMapping(s, func(m map[string]any) { m["dispatch_id"] = dispatchID })
}

func theDispatchInstancesRecordIsDeleted(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	return ctx, os.Remove(filepath.Join(s.lastDispatchInstancePath, ".niwa", "authority.json"))
}
