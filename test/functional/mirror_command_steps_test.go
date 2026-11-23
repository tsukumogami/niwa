package functional

// Steps for the outline in features/mirror-repair.feature that deletes a
// mirror and restores it with each ordinary command that can need it: niwa
// create, niwa apply, niwa dispatch, the SessionStart hook on its path that
// provisions and on its path that doesn't, and niwa worktree create. Each row
// runs one command; what that command must have reported depends on where its
// output reaches a reader, stderr or the hook's injected context.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// The commands the outline runs, as its rows name them.
const (
	mirrorCmdCreate      = "niwa create of a third instance"
	mirrorCmdApply       = "niwa apply at the root"
	mirrorCmdDispatch    = "niwa dispatch"
	mirrorCmdHookProv    = "a SessionStart hook that provisions"
	mirrorCmdHookNoProv  = "a SessionStart hook that doesn't provision"
	mirrorCmdWorktree    = "niwa worktree create in myws"
	mirrorHookSessionID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	mirrorPlainSessionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

const mirrorCommandPattern = `(niwa create of a third instance|niwa apply at the root|niwa dispatch|a SessionStart hook that provisions|a SessionStart hook that doesn't provision|niwa worktree create in myws)`

func registerMirrorCommandSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I restore the mirror by running `+mirrorCommandPattern+`$`, iRestoreTheMirrorByRunning)
	ctx.Step(`^`+mirrorCommandPattern+` reported the restore of "([^"]*)"$`, theCommandReportedTheRestore)
	ctx.Step(`^the borrowers of "([^"]*)" match their snapshot apart from what `+mirrorCommandPattern+` adds$`, theBorrowersMatchApartFromTheCommand)
	ctx.Step(`^the mirror log gained a check line and a restore line for "([^"]*)" with fault "([^"]*)" and result "([^"]*)" from command "([^"]*)"$`, theMirrorLogGainedCheckAndRestoreFrom)
	ctx.Step(`^I run "([^"]*)" in instance "([^"]*)"$`, iRunInInstance)
	ctx.Step(`^I pipe a SessionStart hook for session "([^"]*)" that is not a background worker$`, iPipeASessionStartHookForSession)
	ctx.Step(`^the hook printed nothing$`, theHookPrintedNothing)
	ctx.Step(`^no workspace key directory exists under the sandbox home$`, noWorkspaceKeyDirectoryExists)
}

// iRestoreTheMirrorByRunning runs the row's command and nothing else.
func iRestoreTheMirrorByRunning(ctx context.Context, command string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	switch command {
	case mirrorCmdCreate:
		return ctx, runNiwa(s, s.homeDir, "niwa create myws")
	case mirrorCmdApply:
		return ctx, runNiwa(s, s.workspaceRoot, "niwa apply")
	case mirrorCmdDispatch:
		if ctx, err = aFakeClaudeForDispatchWithSession(ctx, "cccccccc-cccc-4ccc-8ccc-cccccccccccc"); err != nil {
			return ctx, err
		}
		return ctx, runNiwa(s, s.workspaceRoot, "niwa dispatch mirror-task --detach")
	case mirrorCmdHookProv:
		if ctx, err = aBackgroundJobStateForSession(ctx, mirrorHookSessionID); err != nil {
			return ctx, err
		}
		return iPipeASessionStartHookForSession(ctx, mirrorHookSessionID)
	case mirrorCmdHookNoProv:
		return iPipeASessionStartHookForSession(ctx, mirrorPlainSessionID)
	case mirrorCmdWorktree:
		return ctx, runNiwa(s, filepath.Join(s.workspaceRoot, "myws"), "niwa worktree create app")
	}
	return ctx, fmt.Errorf("unknown command %q", command)
}

func iRunInInstance(ctx context.Context, command, instance string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, filepath.Join(s.workspaceRoot, instance), command)
}

// theMirrorLogGainedCheckAndRestoreFrom is the log step with the command the
// lines must name.
func theMirrorLogGainedCheckAndRestoreFrom(ctx context.Context, repoID, fault, result, command string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	if len(lines) != 2 {
		return fmt.Errorf("the mirror log gained %d lines, want a check and a restore line: %v", len(lines), lines)
	}
	if err := requireLogLine(lines[0], command, repoID, "check", fault, result); err != nil {
		return err
	}
	return requireLogLine(lines[1], command, repoID, "restore", fault, result)
}

// theCommandReportedTheRestore checks that the row's command told its reader
// about the restore: a warning on stderr for a command run in a terminal, the
// notice in the injected context for the hook, whose stderr nobody sees.
func theCommandReportedTheRestore(ctx context.Context, command, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	switch command {
	case mirrorCmdHookProv, mirrorCmdHookNoProv:
		return theHookContextCarriesTheNotice(s, command, repoID)
	case mirrorCmdWorktree:
		if err := theErrorOutputCarriesExactlyTheMirrorWarning(ctx, "mirror "+repoID+": fault missing, result restored"); err != nil {
			return err
		}
		return theWorktreeHoldsTheSource(ctx, s, repoID)
	}
	return theErrorOutputCarriesExactlyTheMirrorWarning(ctx, "mirror "+repoID+": fault missing, result restored")
}

// theHookContextCarriesTheNotice decodes the hook's stdout as one SessionStart
// payload and finds the restore notice in its context exactly once.
func theHookContextCarriesTheNotice(s *testState, command, repoID string) error {
	var inj struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(s.stdout))
	if err := dec.Decode(&inj); err != nil {
		return fmt.Errorf("the hook's stdout is not a JSON object: %w\n%s", err, s.stdout)
	}
	if dec.More() {
		return fmt.Errorf("the hook's stdout holds more than one JSON object:\n%s", s.stdout)
	}
	if inj.HookSpecificOutput.HookEventName != "SessionStart" {
		return fmt.Errorf("hookEventName = %q, want SessionStart", inj.HookSpecificOutput.HookEventName)
	}
	text := inj.HookSpecificOutput.AdditionalContext
	head := "niwa mirror " + repoID + ": fault missing, result restored."
	if n := strings.Count(text, head); n != 1 {
		return fmt.Errorf("the context carries the notice for %s %d times, want once:\n%s", repoID, n, text)
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	notice := text[strings.Index(text, head):]
	if i := strings.IndexByte(notice, '\n'); i >= 0 {
		notice = notice[:i]
	}
	if !strings.HasSuffix(notice, "Mirror log: "+filepath.Join(dir, ".meta", "mirror.log")) {
		return fmt.Errorf("the notice %q doesn't name the sandbox's mirror log", notice)
	}

	if command == mirrorCmdHookNoProv {
		if text != notice {
			return fmt.Errorf("the context of a hook that provisions nothing is %q, want only the notice", text)
		}
		if _, err := os.Stat(filepath.Join(s.workspaceRoot, ".niwa", "sessions", mirrorPlainSessionID+".json")); err == nil {
			return fmt.Errorf("a hook that provisions nothing wrote a session mapping")
		}
		instances, err := filepath.Glob(filepath.Join(s.workspaceRoot, "*", ".niwa", "instance.json"))
		if err != nil {
			return err
		}
		if len(instances) != 2 {
			return fmt.Errorf("found %d instances, want only myws and myws-2: %v", len(instances), instances)
		}
		return nil
	}
	instance := "myws-" + mirrorHookSessionID[:12]
	if !strings.Contains(text, instance) || !strings.Contains(text, "\n  cd ") {
		return fmt.Errorf("the provisioning hook's context lost the instance path or its cd line:\n%s", text)
	}
	return nil
}

// theWorktreeHoldsTheSource checks the worktree niwa worktree create made:
// under myws/.niwa/worktrees, holding the source repo's tracked files, its
// history reaching the root commit.
func theWorktreeHoldsTheSource(ctx context.Context, s *testState, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	match := sessionCreateRE.FindStringSubmatch(s.stdout)
	if match == nil {
		return fmt.Errorf("could not find the worktree in the output: %q", s.stdout)
	}
	path := strings.TrimSpace(match[2])
	if dir := filepath.Join(s.workspaceRoot, "myws", ".niwa", "worktrees"); filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), repoID+"-") {
		return fmt.Errorf("the worktree is at %s, want %s/%s-<id>", path, dir, repoID)
	}
	for i := 1; i <= 3; i++ {
		if _, err := os.Stat(filepath.Join(path, fmt.Sprintf("file%d.txt", i))); err != nil {
			return fmt.Errorf("the worktree lacks the source's file%d.txt: %w", i, err)
		}
	}
	root, err := fixtureGitWorkTree(path, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-list in the worktree: %w\n%s", err, root)
	}
	if got := strings.TrimSpace(string(root)); got != m.rootCommits[repoID] {
		return fmt.Errorf("the worktree's history reaches %q, want the root %q", got, m.rootCommits[repoID])
	}
	return nil
}

// theBorrowersMatchApartFromTheCommand is the snapshot check, except that
// niwa worktree create adds a session branch and a worktree entry to the
// borrower in myws, and nothing else.
func theBorrowersMatchApartFromTheCommand(ctx context.Context, repoID, command string) error {
	if command != mirrorCmdWorktree {
		return theBorrowersMatchTheirSnapshot(ctx, repoID)
	}
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		before := m.snapshots[clone]
		after, err := takeSnapshot(clone)
		if err != nil {
			return err
		}
		inMyws := strings.HasPrefix(clone, filepath.Join(s.workspaceRoot, "myws")+string(filepath.Separator))
		if inMyws {
			var added []string
			kept := []string{}
			for _, line := range strings.Split(strings.TrimSpace(after.refs), "\n") {
				if strings.HasPrefix(line, "refs/heads/session/") && !strings.Contains(before.refs, line) {
					added = append(added, line)
					continue
				}
				kept = append(kept, line)
			}
			if len(added) != 1 {
				return fmt.Errorf("worktree create added %d session branches in %s, want 1: %v", len(added), clone, added)
			}
			after.refs = strings.Join(kept, "\n") + "\n"
			list, err := fixtureGitWorkTree(clone, "worktree", "list", "--porcelain")
			if err != nil {
				return fmt.Errorf("git worktree list in %s: %w\n%s", clone, err, list)
			}
			if n := strings.Count(string(list), "worktree "); n != 2 {
				return fmt.Errorf("%s lists %d worktrees, want itself and the new one:\n%s", clone, n, list)
			}
		}
		if string(after.alternates) != string(before.alternates) {
			return fmt.Errorf("alternates in %s changed from %q to %q", clone, before.alternates, after.alternates)
		}
		for _, c := range []struct{ what, before, after string }{
			{"refs", before.refs, after.refs},
			{"HEAD", before.head, after.head},
			{"index", before.index, after.index},
			{"status", before.status, after.status},
		} {
			if c.before != c.after {
				return fmt.Errorf("%s in %s changed:\nbefore: %s\nafter:  %s", c.what, clone, c.before, c.after)
			}
		}
		if len(after.files) != len(before.files) {
			return fmt.Errorf("tracked files in %s changed from %d to %d", clone, len(before.files), len(after.files))
		}
		for rel, data := range before.files {
			if after.files[rel] != data {
				return fmt.Errorf("tracked file %s in %s changed", rel, clone)
			}
		}
	}
	return nil
}

func theHookPrintedNothing(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if s.stdout != "" {
		return fmt.Errorf("the hook printed %q, want nothing", s.stdout)
	}
	return nil
}

func noWorkspaceKeyDirectoryExists(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	found, err := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "w-*"))
	if err != nil {
		return err
	}
	if len(found) != 0 {
		return fmt.Errorf("a workspace key directory exists with no borrowers: %v", found)
	}
	return nil
}
