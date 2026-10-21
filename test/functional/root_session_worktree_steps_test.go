package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// root_session_worktree_steps_test.go holds the steps for sessions launched
// at an instance root or the workspace root rather than inside a repo: the
// hooks apply writes into the root settings, `niwa worktree from-hook` and
// `niwa worktree guard` driven with synthetic hook payloads, and `niwa
// instance context`. Every hook invocation sets CLAUDE_PROJECT_DIR to the
// root the session is pretending to have been launched in and starts the
// process there, as Claude Code does, so the binary's launch-directory anchor
// is exercised end to end.

// rootPhrase matches the two places a root session can be launched, as the
// step text spells them. Used inside the step regexes below.
const rootPhrase = `(the workspace root|the root of instance "[^"]*")`

var rootOfInstanceRe = regexp.MustCompile(`^the root of instance "([^"]*)"$`)

// rootDir resolves a rootPhrase capture to a directory.
func rootDir(s *testState, where string) (string, error) {
	if where == "the workspace root" {
		return s.workspaceRoot, nil
	}
	if m := rootOfInstanceRe.FindStringSubmatch(where); m != nil {
		return filepath.Join(s.workspaceRoot, m[1]), nil
	}
	return "", fmt.Errorf("unrecognized root %q", where)
}

// settingsPhrase matches the settings documents a root-session scenario
// inspects: the workspace root's and an instance root's settings.json, and a
// repo's settings.local.json.
const settingsPhrase = `(the workspace root|the instance "[^"]*"|the repo "[^"]*" in instance "[^"]*")`

var (
	settingsInstanceRe = regexp.MustCompile(`^the instance "([^"]*)"$`)
	settingsRepoRe     = regexp.MustCompile(`^the repo "([^"]*)" in instance "([^"]*)"$`)
)

func settingsPath(s *testState, where string) (string, error) {
	if where == "the workspace root" {
		return filepath.Join(s.workspaceRoot, ".claude", "settings.json"), nil
	}
	if m := settingsInstanceRe.FindStringSubmatch(where); m != nil {
		return filepath.Join(s.workspaceRoot, m[1], ".claude", "settings.json"), nil
	}
	if m := settingsRepoRe.FindStringSubmatch(where); m != nil {
		return filepath.Join(s.workspaceRoot, m[2], filepath.FromSlash(m[1]), ".claude", "settings.local.json"), nil
	}
	return "", fmt.Errorf("unrecognized settings document %q", where)
}

// settingsHookEntry is one matcher group of a Claude Code settings hooks block.
type settingsHookEntry struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

// readSettingsHooks decodes the hooks block of a settings document. A missing
// document has no hooks.
func readSettingsHooks(path string) (map[string][]settingsHookEntry, []byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string][]settingsHookEntry{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Hooks map[string][]settingsHookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, data, fmt.Errorf("parsing %s: %w\n%s", path, err, data)
	}
	return doc.Hooks, data, nil
}

// theSettingsRegisterHook asserts the settings document carries a hook for
// event whose matcher equals matcher and whose command mentions command.
func theSettingsRegisterHook(ctx context.Context, where, event, matcher, command string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := settingsPath(s, where)
	if err != nil {
		return err
	}
	hooks, data, err := readSettingsHooks(path)
	if err != nil {
		return err
	}
	for _, entry := range hooks[event] {
		if entry.Matcher != matcher {
			continue
		}
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, command) {
				return nil
			}
		}
	}
	return fmt.Errorf("%s has no %s hook with matcher %q running %q:\n%s", path, event, matcher, command, data)
}

// theSettingsRegisterHookAnyMatcher is theSettingsRegisterHook without the
// matcher constraint, for events (WorktreeCreate, WorktreeRemove) that take
// none.
func theSettingsRegisterHookAnyMatcher(ctx context.Context, where, event, command string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := settingsPath(s, where)
	if err != nil {
		return err
	}
	hooks, data, err := readSettingsHooks(path)
	if err != nil {
		return err
	}
	for _, entry := range hooks[event] {
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, command) {
				return nil
			}
		}
	}
	return fmt.Errorf("%s has no %s hook running %q:\n%s", path, event, command, data)
}

// theSettingsRegisterNoHookRunning asserts no hook of any event in the
// settings document mentions command. A missing document passes.
func theSettingsRegisterNoHookRunning(ctx context.Context, where, command string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := settingsPath(s, where)
	if err != nil {
		return err
	}
	hooks, data, err := readSettingsHooks(path)
	if err != nil {
		return err
	}
	for event, entries := range hooks {
		for _, entry := range entries {
			for _, h := range entry.Hooks {
				if strings.Contains(h.Command, command) {
					return fmt.Errorf("%s registers a %s hook running %q:\n%s", path, event, command, data)
				}
			}
		}
	}
	return nil
}

// runHookFrom runs a niwa hook command the way Claude Code would for a
// session launched in dir: the process starts in dir, CLAUDE_PROJECT_DIR
// names dir, and payload arrives on stdin.
func runHookFrom(s *testState, dir, command, payload string) error {
	prev, had := s.envOverrides["CLAUDE_PROJECT_DIR"]
	s.envOverrides["CLAUDE_PROJECT_DIR"] = dir
	defer func() {
		if had {
			s.envOverrides["CLAUDE_PROJECT_DIR"] = prev
		} else {
			delete(s.envOverrides, "CLAUDE_PROJECT_DIR")
		}
	}()
	return runNiwaWithStdin(s, dir, command, payload)
}

// iPipeRootWorktreeCreateHook fires a WorktreeCreate hook from a root
// session. cwd is the payload's cwd relative to that root ("." for the root
// itself); name is the worktree name the agent passed to EnterWorktree.
func iPipeRootWorktreeCreateHook(ctx context.Context, cwd, name, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	payload, err := json.Marshal(map[string]string{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "x",
		"cwd":             filepath.Join(dir, filepath.FromSlash(cwd)),
		"name":            name,
	})
	if err != nil {
		return ctx, err
	}
	if err := runHookFrom(s, dir, "niwa worktree from-hook", string(payload)); err != nil {
		return ctx, fmt.Errorf("niwa worktree from-hook (create): %w", err)
	}
	s.printedWorktreePath = strings.TrimSpace(s.stdout)
	return ctx, nil
}

// iPipeRootWorktreeRemoveHook fires a WorktreeRemove hook for the worktree
// the last create printed, from a root session whose cwd is that root.
func iPipeRootWorktreeRemoveHook(ctx context.Context, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return ctx, fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	payload, err := json.Marshal(map[string]string{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "x",
		"cwd":             dir,
		"worktree_path":   s.printedWorktreePath,
	})
	if err != nil {
		return ctx, err
	}
	if err := runHookFrom(s, dir, "niwa worktree from-hook", string(payload)); err != nil {
		return ctx, fmt.Errorf("niwa worktree from-hook (remove): %w", err)
	}
	return ctx, nil
}

// iPipeRootSubagentStopHook fires a SubagentStop hook for an Agent-tool
// subagent whose cwd is the worktree the last create printed, as Claude Code
// sends it when that subagent finishes.
func iPipeRootSubagentStopHook(ctx context.Context, agentID, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return ctx, fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name":  "SubagentStop",
		"session_id":       "x",
		"cwd":              s.printedWorktreePath,
		"agent_id":         agentID,
		"agent_type":       "general-purpose",
		"stop_hook_active": false,
	})
	if err != nil {
		return ctx, err
	}
	if err := runHookFrom(s, dir, "niwa worktree from-hook", string(payload)); err != nil {
		return ctx, fmt.Errorf("niwa worktree from-hook (subagent stop): %w", err)
	}
	return ctx, nil
}

// thePrintedWorktreeHasAnUntrackedFile writes a file git doesn't track into
// the worktree the last create printed.
func thePrintedWorktreeHasAnUntrackedFile(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return ctx, fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	return ctx, os.WriteFile(filepath.Join(s.printedWorktreePath, name), []byte("work\n"), 0o644)
}

// pipeGuard sends one PreToolUse payload to `niwa worktree guard` from a root
// session whose cwd is that root.
func pipeGuard(ctx context.Context, where, tool string, input map[string]string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"session_id":      "x",
		"cwd":             dir,
		"tool_name":       tool,
		"tool_input":      input,
	})
	if err != nil {
		return ctx, err
	}
	if err := runHookFrom(s, dir, "niwa worktree guard", string(payload)); err != nil {
		return ctx, fmt.Errorf("niwa worktree guard: %w", err)
	}
	return ctx, nil
}

func iPipeGuardBash(ctx context.Context, command, where string) (context.Context, error) {
	return pipeGuard(ctx, where, "Bash", map[string]string{"command": command})
}

// iPipeGuardEnterWorktreePath sends EnterWorktree with a path. A relative
// path is resolved against the root first, so the scenario names a location
// inside the instance without spelling out the sandbox.
func iPipeGuardEnterWorktreePath(ctx context.Context, path, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, filepath.FromSlash(path))
	}
	return pipeGuard(ctx, where, "EnterWorktree", map[string]string{"path": path})
}

func iPipeGuardEnterWorktreePrintedPath(ctx context.Context, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return ctx, fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	return pipeGuard(ctx, where, "EnterWorktree", map[string]string{"path": s.printedWorktreePath})
}

func iPipeGuardEnterWorktreeName(ctx context.Context, name, where string) (context.Context, error) {
	return pipeGuard(ctx, where, "EnterWorktree", map[string]string{"name": name})
}

// iPipeRawToGuard sends stdin verbatim, for the malformed-input cases.
func iPipeRawToGuard(ctx context.Context, raw, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	if err := runHookFrom(s, dir, "niwa worktree guard", raw); err != nil {
		return ctx, fmt.Errorf("niwa worktree guard: %w", err)
	}
	return ctx, nil
}

// iRunInstanceContextHook runs the SessionStart context command from a root
// session, with the startup payload Claude Code sends on stdin.
func iRunInstanceContextHook(ctx context.Context, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := rootDir(s, where)
	if err != nil {
		return ctx, err
	}
	payload := fmt.Sprintf(`{"hook_event_name":"SessionStart","source":"startup","session_id":"x","cwd":%q}`, dir)
	if err := runHookFrom(s, dir, "niwa instance context", payload); err != nil {
		return ctx, fmt.Errorf("niwa instance context: %w", err)
	}
	return ctx, nil
}

// sessionStartContext decodes the last command's stdout as a SessionStart
// hook response and returns its additionalContext.
func sessionStartContext(s *testState) (string, error) {
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &out); err != nil {
		return "", fmt.Errorf("stdout is not a SessionStart hook response: %w\nstdout:\n%s\nstderr:\n%s", err, s.stdout, s.stderr)
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" {
		return "", fmt.Errorf("hookEventName = %q, want SessionStart\nstdout:\n%s", out.HookSpecificOutput.HookEventName, s.stdout)
	}
	return out.HookSpecificOutput.AdditionalContext, nil
}

func theSessionStartContextContains(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	text, err := sessionStartContext(s)
	if err != nil {
		return err
	}
	if !strings.Contains(text, want) {
		return fmt.Errorf("session-start context does not contain %q:\n%s", want, text)
	}
	return nil
}

func theSessionStartContextDoesNotContain(ctx context.Context, unwanted string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	text, err := sessionStartContext(s)
	if err != nil {
		return err
	}
	if strings.Contains(text, unwanted) {
		return fmt.Errorf("session-start context contains %q:\n%s", unwanted, text)
	}
	return nil
}

// thePrintedWorktreeIsASession asserts the hook's stdout was exactly one
// absolute path plus a newline, that the path lies under the instance's
// .niwa/worktrees, and that a session record there names it with the given
// repo and purpose.
func thePrintedWorktreeIsASession(ctx context.Context, repo, purpose, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" || s.stdout != s.printedWorktreePath+"\n" {
		return fmt.Errorf("stdout is not exactly one worktree path and a newline: %q\nstderr:\n%s", s.stdout, s.stderr)
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	worktrees, err := filepath.EvalSymlinks(filepath.Join(instRoot, ".niwa", "worktrees"))
	if err != nil {
		return fmt.Errorf("resolving the worktrees directory of %s: %w", instance, err)
	}
	printed, err := filepath.EvalSymlinks(s.printedWorktreePath)
	if err != nil {
		return fmt.Errorf("resolving printed worktree path %q: %w", s.printedWorktreePath, err)
	}
	if !strings.HasPrefix(printed, worktrees+string(filepath.Separator)) {
		return fmt.Errorf("printed worktree %s is not under %s", printed, worktrees)
	}

	sessionsDir := filepath.Join(instRoot, ".niwa", "sessions")
	var seen []string
	_ = filepath.WalkDir(sessionsDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		var rec struct {
			Repo         string `json:"repo"`
			Purpose      string `json:"purpose"`
			WorktreePath string `json:"worktree_path"`
		}
		if json.Unmarshal(data, &rec) != nil || rec.WorktreePath == "" {
			return nil
		}
		if resolved, err := filepath.EvalSymlinks(rec.WorktreePath); err == nil && resolved == printed {
			seen = append(seen, fmt.Sprintf("repo=%q purpose=%q", rec.Repo, rec.Purpose))
		}
		return nil
	})
	want := fmt.Sprintf("repo=%q purpose=%q", repo, purpose)
	if len(seen) == 1 && seen[0] == want {
		return nil
	}
	return fmt.Errorf("want exactly one session record in %s for %s with %s; found: %v", sessionsDir, printed, want, seen)
}

// noWorktreeWasCreatedInInstance asserts the instance's worktrees directory
// is empty or absent, so a refused create left nothing behind.
func noWorktreeWasCreatedInInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, instance, ".niwa", "worktrees")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return fmt.Errorf("%s holds %s; a refused create must leave nothing", dir, strings.Join(names, ", "))
	}
	return nil
}

// noPathNamedExistsUnderWorkspace asserts the workspace tree holds no
// directory entry with the given name, so a model-chosen repo prefix was
// never used as a path.
func noPathNamedExistsUnderWorkspace(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	var found []string
	_ = filepath.WalkDir(s.workspaceRoot, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Name() == name {
			found = append(found, p)
		}
		return nil
	})
	if len(found) > 0 {
		return fmt.Errorf("found paths named %q:\n  %s", name, strings.Join(found, "\n  "))
	}
	return nil
}

// theClaudeConfigTrustsTheWorkspaceRoot seeds the sandboxed Claude config
// with a trusted entry keyed by the symlink-resolved workspace root, the key
// Claude Code computes for a session launched there.
func theClaudeConfigTrustsTheWorkspaceRoot(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	key, err := filepath.EvalSymlinks(s.workspaceRoot)
	if err != nil {
		return ctx, fmt.Errorf("resolving the workspace root: %w", err)
	}
	return theClaudeConfigHasTrustedEntryFor(ctx, key)
}

// aRootSessionWorkspaceExists builds the PRD's fixture: one source repo with
// a commit per name in repos (comma-separated), all in group, a config repo
// named name declaring them, and `niwa init --from` that config repo from the
// workspace root. No instance is created; the scenario does that itself.
func aRootSessionWorkspaceExists(ctx context.Context, name, repos, group string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	var body strings.Builder
	fmt.Fprintf(&body, "[workspace]\nname = %q\n\n[groups.%s]\n", name, group)
	for _, repo := range strings.Split(repos, ",") {
		repo = strings.TrimSpace(repo)
		url, err := s.gitServer.SourceRepo(repo)
		if err != nil {
			return ctx, fmt.Errorf("creating source repo %q: %w", repo, err)
		}
		s.repoURLs[repo] = url
		fmt.Fprintf(&body, "\n[repos.%s]\nurl = %q\ngroup = %q\n", repo, url, group)
	}
	cfgURL, err := s.gitServer.ConfigRepo(name, body.String())
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = cfgURL
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+cfgURL); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init exit=%d\nstdout:\n%s\nstderr:\n%s", s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// registerRootSessionWorktreeSteps wires the root-session worktree steps into
// the scenario context. Called from initializeScenario.
func registerRootSessionWorktreeSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a root-session workspace "([^"]*)" exists with repos "([^"]*)" in group "([^"]*)"$`, aRootSessionWorkspaceExists)
	ctx.Step(`^`+settingsPhrase+` settings register a "([^"]*)" hook with matcher "([^"]*)" running "([^"]*)"$`, theSettingsRegisterHook)
	ctx.Step(`^`+settingsPhrase+` settings register a "([^"]*)" hook running "([^"]*)"$`, theSettingsRegisterHookAnyMatcher)
	ctx.Step(`^`+settingsPhrase+` settings register no hook running "([^"]*)"$`, theSettingsRegisterNoHookRunning)
	ctx.Step(`^I pipe a WorktreeCreate hook with cwd "([^"]*)" and name "([^"]*)" from `+rootPhrase+`$`, iPipeRootWorktreeCreateHook)
	ctx.Step(`^I pipe a WorktreeRemove hook for the printed worktree path from `+rootPhrase+`$`, iPipeRootWorktreeRemoveHook)
	ctx.Step(`^I pipe a SubagentStop hook for agent "([^"]*)" in the printed worktree from `+rootPhrase+`$`, iPipeRootSubagentStopHook)
	ctx.Step(`^the printed worktree has an untracked file "([^"]*)"$`, thePrintedWorktreeHasAnUntrackedFile)
	ctx.Step(`^I pipe a Bash call running "([^"]*)" to the worktree guard from `+rootPhrase+`$`, iPipeGuardBash)
	ctx.Step(`^I pipe an EnterWorktree call with path "([^"]*)" to the worktree guard from `+rootPhrase+`$`, iPipeGuardEnterWorktreePath)
	ctx.Step(`^I pipe an EnterWorktree call with the printed worktree path to the worktree guard from `+rootPhrase+`$`, iPipeGuardEnterWorktreePrintedPath)
	ctx.Step(`^I pipe an EnterWorktree call with name "([^"]*)" to the worktree guard from `+rootPhrase+`$`, iPipeGuardEnterWorktreeName)
	ctx.Step(`^I pipe "([^"]*)" to the worktree guard from `+rootPhrase+`$`, iPipeRawToGuard)
	ctx.Step(`^I run the instance context hook from `+rootPhrase+`$`, iRunInstanceContextHook)
	ctx.Step(`^the session-start context contains "([^"]*)"$`, theSessionStartContextContains)
	ctx.Step(`^the session-start context does not contain "([^"]*)"$`, theSessionStartContextDoesNotContain)
	ctx.Step(`^the printed worktree is a session of repo "([^"]*)" with purpose "([^"]*)" in instance "([^"]*)"$`, thePrintedWorktreeIsASession)
	ctx.Step(`^no worktree was created in instance "([^"]*)"$`, noWorktreeWasCreatedInInstance)
	ctx.Step(`^no path named "([^"]*)" exists under the workspace root$`, noPathNamedExistsUnderWorkspace)
	ctx.Step(`^the Claude config in HOME trusts the workspace root$`, theClaudeConfigTrustsTheWorkspaceRoot)
}
