package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// The authority read-back scenarios check what a dispatched session, an
// operator and a coordinator read back about a dispatch's authority: `niwa
// authority show`, `niwa authority decide` and the authority object in `niwa
// list --json`.
//
// The fake agents here are the dispatch fakes with one addition: before doing
// anything else, an invocation that starts or resumes a session runs `niwa
// authority show` from its own working directory and keeps what it printed in
// HOME, under a label naming the invocation. That is the session's first
// command, made before niwa has written the session's mapping.

// authorityReadScenario is what these steps remember between steps.
type authorityReadScenario struct {
	kept map[string]string // dispatch instances by the name a step kept them under
}

// authorityReadPrologue is the shell the fake agents run first. The labels are
// the invocations that start a session: a launch (claude --bg, codex exec),
// and the three resume paths (claude attach, claude --bg --resume, codex
// resume).
const authorityReadPrologue = `if [ -n "$FAKE_AUTHORITY_NIWA" ]; then
  label=''
  case "$1" in
    --bg) if [ "$2" = "--resume" ]; then label=bg-resume; else label=launch; fi ;;
    attach) label=attach ;;
    exec) label=launch ;;
    resume) label=resume ;;
  esac
  if [ -n "$label" ]; then
    out="$HOME/authority-read-$label"
    ls "$(dirname "$PWD")/.niwa/sessions" > "$out.mappings" 2>/dev/null
    printf '%s' "$CLAUDE_CODE_SESSION_ID" > "$out.session"
    ( cd "${FAKE_AUTHORITY_CD:-.}" && "$FAKE_AUTHORITY_NIWA" authority show ${FAKE_AUTHORITY_SHOW_ARGS---json} ) > "$out.out" 2> "$out.err"
    echo $? > "$out.exit"
    if [ -n "$FAKE_AUTHORITY_DECIDE" ]; then
      answer=$("$FAKE_AUTHORITY_NIWA" authority decide merge-own-pr 2>/dev/null)
      if [ $? -eq 0 ] && printf '%s\n' "$answer" | grep -qx 'decider=session'; then
        echo settled > "$HOME/decide-marker"
      else
        echo waiting-for-person > "$HOME/decide-marker"
      fi
    fi
  fi
fi
`

func withAuthorityReadPrologue(script string) string {
	return strings.Replace(script, "#!/bin/sh\n", "#!/bin/sh\n"+authorityReadPrologue, 1)
}

func registerAuthorityReadSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a config repo "([^"]*)" exists with authority fixture "([^"]*)" and repo "([^"]*)"$`, aConfigRepoWithAuthorityFixtureAndRepo)
	ctx.Step(`^a fake claude for dispatch that reads its authority first, with session "([^"]*)"$`, aReadingFakeClaudeWithSession)
	ctx.Step(`^a fake claude for dispatch that reads its authority first and mints a new session per launch$`, aReadingFakeClaudeThatMints)
	ctx.Step(`^a fake codex for dispatch that reads its authority first, with session "([^"]*)"$`, aReadingFakeCodexWithSession)
	ctx.Step(`^the fake agent reads its authority from "([^"]*)" as plain text$`, theFakeAgentReadsFromAsPlainText)
	ctx.Step(`^the fake agent asks decide about merge-own-pr first$`, theFakeAgentAsksDecideFirst)
	ctx.Step(`^the dispatching session is the first dispatch's session$`, theDispatchingSessionIsTheFirstDispatchsSession)
	ctx.Step(`^the worker's "([^"]*)" read printed the dispatch instance's record$`, theWorkersReadPrintedTheRecord)
	ctx.Step(`^the worker's "([^"]*)" read printed the line "([^"]*)"$`, theWorkersReadPrintedTheLine)
	ctx.Step(`^the worker's first read ran before its session mapping existed$`, theWorkersFirstReadRanBeforeItsMapping)
	ctx.Step(`^"niwa authority show --json" from "([^"]*)" in the dispatch instance prints the worker's first read$`, showFromInsideTheInstancePrintsTheFirstRead)
	ctx.Step(`^"niwa authority show --json" from the worktree prints the worker's first read$`, showFromTheWorktreePrintsTheFirstRead)
	ctx.Step(`^the decide marker reads "([^"]*)"$`, theDecideMarkerReads)
	ctx.Step(`^I keep the dispatch instance as "([^"]*)"$`, iKeepTheDispatchInstanceAs)
	ctx.Step(`^one "niwa list --json" carries each kept dispatch's record as its authority object, with "([^"]*)" the parent of "([^"]*)" and "([^"]*)"$`, oneListCarriesEachKeptRecord)
	ctx.Step(`^"niwa list --json" shows the dispatch instance's authority granting "([^"]*)"$`, listShowsTheInstanceGranting)
	ctx.Step(`^(\d+) session mappings exist in the workspace$`, sessionMappingsExist)
	ctx.Step(`^the median of ten runs of "([^"]*)" from inside the dispatch instance is under (\d+) ms$`, theMedianOfTenRunsIsUnder)
}

func aConfigRepoWithAuthorityFixtureAndRepo(ctx context.Context, name, fixture, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	body, ok := authorityFixtures[fixture]
	if !ok {
		return ctx, fmt.Errorf("no authority fixture %q", fixture)
	}
	repoURL, ok := s.repoURLs[repo]
	if !ok {
		return ctx, fmt.Errorf("no source repo %q", repo)
	}
	toml := fmt.Sprintf("[workspace]\nname = %q\n\n[groups.tools]\n\n[repos.%s]\nurl = %q\ngroup = \"tools\"\n\n%s", name, repo, repoURL, body)
	url, err := s.gitServer.ConfigRepo(name, toml)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

func installReadingFakeClaude(s *testState, behaviour string) error {
	if err := installDispatchFakeClaude(s, behaviour); err != nil {
		return err
	}
	path := filepath.Join(s.pathPrefix, "claude")
	if err := os.WriteFile(path, []byte(withAuthorityReadPrologue(dispatchFakeClaudeScript(behaviour))), 0o755); err != nil {
		return err
	}
	s.envOverrides["FAKE_AUTHORITY_NIWA"] = s.binPath
	return nil
}

func aReadingFakeClaudeWithSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := installReadingFakeClaude(s, "ok"); err != nil {
		return ctx, err
	}
	s.envOverrides["FAKE_CLAUDE_SESSION_ID"] = sessionID
	return ctx, nil
}

func aReadingFakeClaudeThatMints(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, installReadingFakeClaude(s, dispatchFakeClaudeMintedSession)
}

func aReadingFakeCodexWithSession(ctx context.Context, sessionID string) (context.Context, error) {
	ctx, err := aFakeCodexForDispatchWithSession(ctx, sessionID)
	if err != nil {
		return ctx, err
	}
	s := getState(ctx)
	path := filepath.Join(s.pathPrefix, "codex")
	if err := os.WriteFile(path, []byte(withAuthorityReadPrologue(dispatchFakeCodexScript)), 0o755); err != nil {
		return ctx, err
	}
	s.envOverrides["FAKE_AUTHORITY_NIWA"] = s.binPath
	return ctx, nil
}

func theFakeAgentReadsFromAsPlainText(ctx context.Context, dir string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides["FAKE_AUTHORITY_CD"] = strings.ReplaceAll(dir, "{workspace-root}", s.workspaceRoot)
	s.envOverrides["FAKE_AUTHORITY_SHOW_ARGS"] = ""
	return ctx, nil
}

func theFakeAgentAsksDecideFirst(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides["FAKE_AUTHORITY_DECIDE"] = "1"
	return ctx, nil
}

// resolvedPath spells a path with symlinks resolved, so two spellings of one
// directory (/tmp and /private/tmp on macOS) compare equal.
func resolvedPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// mappingFor returns the session id of the mapping that names inst.
func mappingFor(s *testState, inst string) (string, error) {
	inst = resolvedPath(inst)
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	for _, name := range sessionMappings(s) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var m struct {
			SessionID    string `json:"session_id"`
			InstancePath string `json:"instance_path"`
		}
		if json.Unmarshal(data, &m) == nil && resolvedPath(m.InstancePath) == inst {
			return m.SessionID, nil
		}
	}
	return "", fmt.Errorf("no session mapping names %s", inst)
}

// theDispatchingSessionIsTheFirstDispatchsSession sets the session id
// variable to the first dispatch's own session, the way a worker dispatched
// from that session inherits it.
func theDispatchingSessionIsTheFirstDispatchsSession(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	sid, err := mappingFor(s, s.authority.firstInstance)
	if err != nil {
		return ctx, err
	}
	s.envOverrides["CLAUDE_CODE_SESSION_ID"] = sid
	return ctx, nil
}

// workerRead returns what the fake agent's read under label printed, and
// fails unless it exited 0.
func workerRead(s *testState, label string) (string, error) {
	base := filepath.Join(s.homeDir, "authority-read-"+label)
	code, err := os.ReadFile(base + ".exit")
	if err != nil {
		return "", fmt.Errorf("the fake agent made no %q read: %w", label, err)
	}
	out, _ := os.ReadFile(base + ".out")
	if strings.TrimSpace(string(code)) != "0" {
		errOut, _ := os.ReadFile(base + ".err")
		return "", fmt.Errorf("the %q read exited %s\nstdout:\n%s\nstderr:\n%s", label, strings.TrimSpace(string(code)), out, errOut)
	}
	return string(out), nil
}

// theWorkersReadPrintedTheRecord compares the read byte for byte with the
// record dispatch stored: `show --json` prints the same document, so the
// dispatch id, level and grant all match.
func theWorkersReadPrintedTheRecord(ctx context.Context, label string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := workerRead(s, label)
	if err != nil {
		return err
	}
	want, err := lastRecord(s)
	if err != nil {
		return err
	}
	if got != string(want) {
		return fmt.Errorf("the %q read printed\n%s\nthe stored record is\n%s", label, got, want)
	}
	return nil
}

func theWorkersReadPrintedTheLine(ctx context.Context, label, line string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := workerRead(s, label)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(got, "\n") {
		if l == line {
			return nil
		}
	}
	return fmt.Errorf("the %q read has no line %q:\n%s", label, line, got)
}

func theWorkersFirstReadRanBeforeItsMapping(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	sid, err := mappingFor(s, s.lastDispatchInstancePath)
	if err != nil {
		return err
	}
	listing, err := os.ReadFile(filepath.Join(s.homeDir, "authority-read-launch.mappings"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(listing), sid) {
		return fmt.Errorf("the session's own mapping %s already existed at its first read:\n%s", sid, listing)
	}
	return nil
}

func showFrom(s *testState, dir string) error {
	if err := runNiwa(s, dir, "niwa authority show --json"); err != nil {
		return err
	}
	if s.exitCode != 0 {
		return fmt.Errorf("niwa authority show --json from %s exited %d\n%s%s", dir, s.exitCode, s.stdout, s.stderr)
	}
	first, err := workerRead(s, "launch")
	if err != nil {
		return err
	}
	if s.stdout != first {
		return fmt.Errorf("from %s show printed\n%s\nthe worker's first read was\n%s", dir, s.stdout, first)
	}
	return nil
}

func showFromInsideTheInstancePrintsTheFirstRead(ctx context.Context, rel string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return showFrom(s, filepath.Join(s.lastDispatchInstancePath, rel))
}

func showFromTheWorktreePrintsTheFirstRead(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree created")
	}
	return showFrom(s, s.lastSessionWorktreePath)
}

func theDecideMarkerReads(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, "decide-marker"))
	if err != nil {
		return fmt.Errorf("the fake agent wrote no decide marker: %w", err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		return fmt.Errorf("decide marker %q, want %q", got, want)
	}
	return os.Remove(filepath.Join(s.homeDir, "decide-marker"))
}

func iKeepTheDispatchInstanceAs(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	if s.authorityRead.kept == nil {
		s.authorityRead.kept = map[string]string{}
	}
	s.authorityRead.kept[name] = s.lastDispatchInstancePath
	return ctx, nil
}

// listAuthorities runs `niwa list --json` once from the workspace root and
// returns each instance's authority object by its resolved path.
func listAuthorities(s *testState) (map[string]map[string]any, error) {
	if err := runNiwa(s, s.workspaceRoot, "niwa list --json"); err != nil {
		return nil, err
	}
	if s.exitCode != 0 {
		return nil, fmt.Errorf("niwa list --json exited %d\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	var rows []struct {
		Path      string         `json:"path"`
		Authority map[string]any `json:"authority"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &rows); err != nil {
		return nil, fmt.Errorf("niwa list --json: %w\n%s", err, s.stdout)
	}
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[resolvedPath(r.Path)] = r.Authority
	}
	return out, nil
}

func recordDoc(path string) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Join(path, ".niwa", "authority.json"))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(data, &m)
}

// oneListCarriesEachKeptRecord reads every kept dispatch's posture from a
// single `niwa list --json` call, the way a coordinator reads the sessions
// it dispatched, with no --session lookup.
func oneListCarriesEachKeptRecord(ctx context.Context, parent, childA, childB string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	listed, err := listAuthorities(s)
	if err != nil {
		return err
	}
	ids := map[string]any{}
	for name, inst := range s.authorityRead.kept {
		want, err := recordDoc(inst)
		if err != nil {
			return fmt.Errorf("reading %s's record: %w", name, err)
		}
		if !reflect.DeepEqual(listed[resolvedPath(inst)], want) {
			return fmt.Errorf("%s's authority object is %v, its record %v", name, listed[resolvedPath(inst)], want)
		}
		ids[name] = want["dispatch_id"]
	}
	for _, child := range []string{childA, childB} {
		if got := listed[resolvedPath(s.authorityRead.kept[child])]["parent_dispatch_id"]; got != ids[parent] {
			return fmt.Errorf("%s's parent_dispatch_id is %v, want %s's %v", child, got, parent, ids[parent])
		}
	}
	return nil
}

func listShowsTheInstanceGranting(ctx context.Context, authority string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	listed, err := listAuthorities(s)
	if err != nil {
		return err
	}
	obj := listed[resolvedPath(s.lastDispatchInstancePath)]
	grant, _ := obj["grant"].([]any)
	for _, g := range grant {
		if e, ok := g.(map[string]any); ok && e["authority"] == authority {
			return nil
		}
	}
	return fmt.Errorf("the dispatch instance's authority object grants no %s: %v", authority, obj)
}

func sessionMappingsExist(ctx context.Context, n int) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ctx, err
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		body := fmt.Sprintf(`{"session_id": %q, "instance_name": "x", "instance_path": %q, "created": "2026-01-01T00:00:00Z", "ephemeral": false}`,
			id, s.lastDispatchInstancePath)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// theMedianOfTenRunsIsUnder times ten runs of the built binary, each a whole
// process start, and checks the median.
func theMedianOfTenRunsIsUnder(ctx context.Context, command string, ms int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	var runs []time.Duration
	for i := 0; i < 10; i++ {
		if err := runNiwa(s, s.lastDispatchInstancePath, command); err != nil {
			return err
		}
		if s.exitCode != 0 {
			return fmt.Errorf("%s exited %d\n%s%s", command, s.exitCode, s.stdout, s.stderr)
		}
		runs = append(runs, s.lastRunDuration)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	median := (runs[4] + runs[5]) / 2
	if median >= time.Duration(ms)*time.Millisecond {
		return fmt.Errorf("the median of ten runs of %s is %s, want under %d ms (runs %v)", command, median, ms, runs)
	}
	return nil
}
