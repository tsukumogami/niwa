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

// The authority scenarios check what a dispatch records about its authority
// level: the session mapping's two fields, the record inside the instance, and
// what `niwa list --json` reads back. The refusal half reuses the generic
// dispatch steps (no instance, no mapping) and the stderr line counter.
//
// The decision-point scenarios dispatch a fake agent that asks `niwa
// authority decide merge` from inside its instance and acts on the exit code,
// the way a real session reaching a merge is meant to.

// dispatchFakeClaudeDecides is the behaviour name for the fake claude whose
// launch also reaches a merge decision.
const dispatchFakeClaudeDecides = "decide"

// dispatchFakeClaudeDecideHook runs inside the fake's --bg branch, with the
// instance as its cwd, after the job state is written. It asks niwa who
// settles the merge and writes what the agent would do to
// $HOME/agent-merge-marker: settled on exit 0, waiting-for-person on exit 3,
// and decide-failed-<code> on anything else so a broken call can't pass as an
// answer. The answer's stdout, stderr and exit code are kept beside it.
//
// The launch runs before dispatch writes the session mapping, so this
// exercises the instance record: the answer an early-asking worker gets.
const dispatchFakeClaudeDecideHook = `  niwa authority decide merge > "$HOME/agent-decide-stdout" 2> "$HOME/agent-decide-stderr"
  rc=$?
  printf '%s\n' "$rc" > "$HOME/agent-decide-exit"
  case "$rc" in
    0) echo settled > "$HOME/agent-merge-marker" ;;
    3) echo waiting-for-person > "$HOME/agent-merge-marker" ;;
    *) echo "decide-failed-$rc" > "$HOME/agent-merge-marker" ;;
  esac
`

func registerDispatchAuthoritySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the dispatch mapping for session "([^"]*)" records authority level "([^"]*)" from "([^"]*)"$`, theDispatchMappingRecordsAuthority)
	ctx.Step(`^the dispatch instance holds an authority record with level "([^"]*)" from "([^"]*)"$`, theDispatchInstanceHoldsAuthorityRecord)
	ctx.Step(`^the list JSON reports the dispatch instance at authority level "([^"]*)" from "([^"]*)"$`, theListJSONReportsAuthority)
	ctx.Step(`^a fake claude for dispatch that decides a merge with session "([^"]*)"$`, aFakeClaudeForDispatchThatDecidesAMerge)
	ctx.Step(`^the fake agent's merge marker reads "([^"]*)"$`, theFakeAgentsMergeMarkerReads)
	ctx.Step(`^the fake agent's decide answer contains "([^"]*)"$`, theFakeAgentsDecideAnswerContains)
	ctx.Step(`^the fake agent's decide call printed nothing on stderr$`, theFakeAgentsDecidePrintedNothingOnStderr)
}

// aFakeClaudeForDispatchThatDecidesAMerge installs the fake claude whose
// launch runs `niwa authority decide merge`, and puts the niwa under test on
// the agent's PATH beside it as plain "niwa", the name a real session would
// type.
func aFakeClaudeForDispatchThatDecidesAMerge(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := installDispatchFakeClaude(s, dispatchFakeClaudeDecides); err != nil {
		return ctx, err
	}
	link := filepath.Join(s.pathPrefix, "niwa")
	if err := os.Symlink(s.binPath, link); err != nil {
		return ctx, fmt.Errorf("putting niwa on the fake agent's PATH: %w", err)
	}
	s.envOverrides["FAKE_CLAUDE_SESSION_ID"] = sessionID
	return ctx, nil
}

// readAgentFile reads one of the files the decide hook writes under $HOME.
func readAgentFile(s *testState, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.homeDir, name))
	if err != nil {
		return "", fmt.Errorf("the fake agent left no %s (did the launch run?): %w\ndispatch stderr:\n%s", name, err, s.stderr)
	}
	return string(data), nil
}

func theFakeAgentsMergeMarkerReads(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := readAgentFile(s, "agent-merge-marker")
	if err != nil {
		return err
	}
	if strings.TrimSpace(got) != want {
		out, _ := readAgentFile(s, "agent-decide-stdout")
		errOut, _ := readAgentFile(s, "agent-decide-stderr")
		return fmt.Errorf("merge marker reads %q; want %q\ndecide stdout:\n%s\ndecide stderr:\n%s", strings.TrimSpace(got), want, out, errOut)
	}
	return nil
}

func theFakeAgentsDecideAnswerContains(ctx context.Context, line string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	out, err := readAgentFile(s, "agent-decide-stdout")
	if err != nil {
		return err
	}
	for _, l := range strings.Split(out, "\n") {
		if l == line {
			return nil
		}
	}
	return fmt.Errorf("decide answer has no line %q:\n%s", line, out)
}

func theFakeAgentsDecidePrintedNothingOnStderr(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	errOut, err := readAgentFile(s, "agent-decide-stderr")
	if err != nil {
		return err
	}
	if errOut != "" {
		return fmt.Errorf("decide printed on stderr:\n%s", errOut)
	}
	return nil
}

// authorityPair reads authority_level and authority_source out of a JSON
// object, failing when either is absent or not a string.
func authorityPair(doc map[string]any, where string) (string, string, error) {
	level, ok1 := doc["authority_level"].(string)
	source, ok2 := doc["authority_source"].(string)
	if !ok1 || !ok2 {
		return "", "", fmt.Errorf("%s does not carry authority_level and authority_source as strings: %v", where, doc)
	}
	return level, source, nil
}

func theDispatchMappingRecordsAuthority(ctx context.Context, sessionID, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading mapping %s: %w\nstderr:\n%s", path, err, s.stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing mapping %s: %w", path, err)
	}
	gotLevel, gotSource, err := authorityPair(doc, "mapping "+path)
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("mapping records %s from %s; want %s from %s", gotLevel, gotSource, level, source)
	}
	return nil
}

func theDispatchInstanceHoldsAuthorityRecord(ctx context.Context, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst := findDispatchInstance(s.workspaceRoot)
	if inst == "" {
		return fmt.Errorf("no dispatch instance under %s", s.workspaceRoot)
	}
	path := filepath.Join(inst, ".niwa", "authority.json")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the instance has no authority record: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s has mode %v; want 0600", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	gotLevel, gotSource, err := authorityPair(doc, path)
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("%s records %s from %s; want %s from %s", path, gotLevel, gotSource, level, source)
	}
	return nil
}

func theListJSONReportsAuthority(ctx context.Context, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	rec, err := dispatchListRecord(s)
	if err != nil {
		return err
	}
	gotLevel, gotSource, err := authorityPair(rec, "the dispatch instance's list record")
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("niwa list --json reports %s from %s; want %s from %s", gotLevel, gotSource, level, source)
	}
	return nil
}
