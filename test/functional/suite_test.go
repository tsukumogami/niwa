// Package functional contains end-to-end tests driven by godog (cucumber-style).
// Tests require a prebuilt niwa binary; invoke via `make test-functional`.
package functional

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type stateKeyType struct{}

var stateKey = stateKeyType{}

// testState holds per-scenario state. The Before hook resets it so each
// scenario starts from a clean sandbox (fresh $HOME, fresh workspace root).
type testState struct {
	binPath       string            // absolute path to the niwa test binary
	homeDir       string            // sandboxed $HOME for this scenario (holds .niwa/, .bashrc, etc.)
	tmpDir        string            // scenario-scoped $TMPDIR (writes landed here stay isolated)
	workspaceRoot string            // sandboxed directory where workspaces live
	stdout        string            // last command's stdout
	stderr        string            // last command's stderr
	exitCode      int               // last command's exit code
	shellPwd      string            // pwd reported by the last wrapped-shell run
	shellStartPwd string            // cwd the wrapped shell started in (for "did not change" assertions)
	envOverrides  map[string]string // per-scenario env var overrides (win over defaults)
	gitServer     *localGitServer   // local bare-repo server for offline clone tests
	repoURLs      map[string]string // name → file:// URL for repos created by localGitServer
}

func getState(ctx context.Context) *testState {
	if s, ok := ctx.Value(stateKey).(*testState); ok {
		return s
	}
	return nil
}

func setState(ctx context.Context, s *testState) context.Context {
	return context.WithValue(ctx, stateKey, s)
}

// TestFeatures is the godog entry point. It's skipped when NIWA_TEST_BINARY
// isn't set so plain `go test ./...` doesn't try to invoke a missing binary;
// the Makefile's test-functional target sets NIWA_TEST_BINARY after building.
func TestFeatures(t *testing.T) {
	binPath := os.Getenv("NIWA_TEST_BINARY")
	if binPath == "" {
		t.Skip("NIWA_TEST_BINARY not set; run via 'make test-functional'")
	}

	absBin, err := filepath.Abs(binPath)
	if err != nil {
		t.Fatalf("resolving binary path: %v", err)
	}
	binPath = absBin

	paths := []string{"features"}
	if p := os.Getenv("NIWA_TEST_PATHS"); p != "" {
		paths = strings.Split(p, string(os.PathListSeparator))
	}

	opts := &godog.Options{
		Format:   "pretty",
		Paths:    paths,
		TestingT: t,
	}
	if tags := os.Getenv("NIWA_TEST_TAGS"); tags != "" {
		opts.Tags = tags
	}

	suite := godog.TestSuite{
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			initializeScenario(ctx, binPath)
		},
		Options: opts,
	}
	if suite.Run() != 0 {
		t.Fatal("functional tests failed")
	}
}

func initializeScenario(ctx *godog.ScenarioContext, binPath string) {
	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		// Each scenario gets its own sandbox under the binary's directory.
		// Using t.TempDir() would work but placing it alongside the binary
		// makes test artifacts easier to inspect on failure.
		repoRoot := filepath.Dir(binPath)
		sandbox := filepath.Join(repoRoot, ".niwa-test")
		_ = os.RemoveAll(sandbox)
		if err := os.MkdirAll(sandbox, 0o755); err != nil {
			return ctx, err
		}
		homeDir := filepath.Join(sandbox, "home")
		tmpDir := filepath.Join(sandbox, "tmp")
		for _, d := range []string{homeDir, tmpDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return ctx, err
			}
		}

		// workspaceRoot must live outside any existing niwa instance tree so that
		// niwa init's CheckInitConflicts check does not fire when the developer's
		// machine has a niwa workspace ancestor covering the repo root. Using the
		// system temp dir guarantees a clean parent regardless of repo location.
		wsParent := filepath.Join(os.TempDir(), "niwa-test-workspaces")
		_ = os.RemoveAll(wsParent)
		if err := os.MkdirAll(wsParent, 0o755); err != nil {
			return ctx, err
		}
		workspaceRoot := wsParent

		gitServerDir := filepath.Join(sandbox, "gitserver")
		gs, err := newLocalGitServer(gitServerDir)
		if err != nil {
			return ctx, err
		}

		state := &testState{
			binPath:       binPath,
			homeDir:       homeDir,
			tmpDir:        tmpDir,
			workspaceRoot: workspaceRoot,
			envOverrides:  make(map[string]string),
			gitServer:     gs,
			repoURLs:      make(map[string]string),
		}
		return setState(ctx, state), nil
	})

	// Environment
	ctx.Step(`^a clean niwa environment$`, aCleanNiwaEnvironment)
	ctx.Step(`^a workspace "([^"]*)" exists$`, aWorkspaceExists)
	ctx.Step(`^a registered workspace "([^"]*)" exists$`, aRegisteredWorkspaceExists)
	ctx.Step(`^a workspace "([^"]*)" exists with body:$`, aWorkspaceExistsWithBody)
	ctx.Step(`^an instance "([^"]*)" of workspace "([^"]*)" exists with repos "([^"]*)"$`, func(ctx context.Context, instanceName, workspaceName, repos string) (context.Context, error) {
		return aWorkspaceInstanceExistsWithRepos(ctx, workspaceName, instanceName, repos)
	})
	ctx.Step(`^I set env "([^"]*)" to "([^"]*)"$`, iSetEnv)
	ctx.Step(`^I set env "([^"]*)" to a temp path$`, iSetEnvToTempPath)

	// Commands
	ctx.Step(`^I run "([^"]*)"$`, iRun)
	ctx.Step(`^I run "([^"]*)" from workspace "([^"]*)"$`, iRunFromWorkspace)
	ctx.Step(`^I source the bash wrapper and run "([^"]*)" from workspace "([^"]*)"$`, iSourceWrapperAndRunFromWorkspace)
	ctx.Step(`^I source the bash wrapper and run "([^"]*)"$`, iSourceWrapperAndRun)
	ctx.Step(`^I source the noisy bash wrapper and run "([^"]*)" from workspace "([^"]*)"$`, iSourceNoisyWrapperAndRunFromWorkspace)
	ctx.Step(`^I run completion for "([^"]*)" with prefix "([^"]*)"$`, iRunCompletion)
	ctx.Step(`^I run completion for "([^"]*)" with prefix "([^"]*)" from instance "([^"]*)" of workspace "([^"]*)"$`, iRunCompletionFromInstance)
	ctx.Step(`^I source the installer env file and run completion for "([^"]*)" with prefix "([^"]*)"$`, iSourceShellInitAndRunCompletion)
	ctx.Step(`^the "([^"]*)" shell-init output contains "([^"]*)"$`, shellInitContains)

	// Personal overlay
	ctx.Step(`^a personal overlay exists with body:$`, aPersonalOverlayExistsWithBody)

	// Local git server
	ctx.Step(`^a local git server is set up$`, aLocalGitServerIsSetUp)
	ctx.Step(`^a config repo "([^"]*)" exists with body:$`, aConfigRepoExistsWithBody)
	ctx.Step(`^an overlay repo "([^"]*)" exists with body:$`, anOverlayRepoExistsWithBody)
	ctx.Step(`^a source repo "([^"]*)" exists$`, aSourceRepoExists)
	ctx.Step(`^I run niwa init from config repo "([^"]*)"$`, iRunNiwaInitFromConfigRepo)
	ctx.Step(`^I run niwa init from config repo "([^"]*)" with overlay "([^"]*)"$`, iRunNiwaInitFromConfigRepoWithOverlay)
	ctx.Step(`^the instance "([^"]*)" exists$`, theInstanceExists)
	ctx.Step(`^the instance "([^"]*)" does not exist$`, theInstanceDoesNotExist)
	ctx.Step(`^the repo "([^"]*)" exists in instance "([^"]*)"$`, theRepoExistsInInstance)

	// Mesh / session steps
	ctx.Step(`^NIWA_INSTANCE_ROOT is set to a temp directory$`, niwaInstanceRootIsSetToATempDirectory)
	ctx.Step(`^I run "niwa session register" as role "([^"]*)"$`, iRunNiwaSessionRegisterAsRole)
	ctx.Step(`^I run "niwa session register" from repo directory "([^"]*)"$`, iRunNiwaSessionRegisterFromRepoDir)
	ctx.Step(`^a sessions\.json entry exists for role "([^"]*)"$`, aSessionsJSONEntryExistsForRole)
	ctx.Step(`^the coordinator inbox directory exists$`, func(ctx context.Context) (context.Context, error) {
		return ctx, theInboxDirectoryExistsForRole(ctx, "coordinator")
	})
	ctx.Step(`^the worker session sends a "([^"]*)" message to "([^"]*)" with body "([^"]*)"$`, theWorkerSessionSendsAMessageToWithBody)
	ctx.Step(`^the coordinator inbox contains (\d+) message$`, theCoordinatorInboxContainsNMessages)
	ctx.Step(`^the coordinator session checks messages$`, theCoordinatorSessionChecksMessages)
	ctx.Step(`^a Claude session file exists for the parent process with session ID "([^"]*)" and matching cwd$`, aClaudeSessionFileExistsForParentProcessWithMatchingCwd)
	ctx.Step(`^a Claude session file exists for the parent process with session ID "([^"]*)" and mismatched cwd$`, aClaudeSessionFileExistsForParentProcessWithMismatchedCwd)
	ctx.Step(`^the sessions\.json entry for role "([^"]*)" has claude_session_id "([^"]*)"$`, theSessionsJSONEntryForRoleHasClaudeSessionID)
	ctx.Step(`^the sessions\.json entry for role "([^"]*)" has no claude_session_id$`, theSessionsJSONEntryForRoleHasNoClaudeSessionID)

	// Mesh daemon steps
	ctx.Step(`^I remember the daemon PID for instance "([^"]*)"$`, iRememberDaemonPIDForInstance)
	ctx.Step(`^the daemon PID for instance "([^"]*)" has not changed$`, theDaemonPIDForInstanceHasNotChanged)
	ctx.Step(`^I remove the sessions directory from instance "([^"]*)"$`, iRemoveSessionsDirFromInstance)
	ctx.Step(`^the daemon for instance "([^"]*)" eventually stops$`, theDaemonForInstanceEventuallyStops)
	ctx.Step(`^I set NIWA_INSTANCE_ROOT to instance "([^"]*)"$`, iSetNiwaInstanceRootToInstance)
	ctx.Step(`^the daemon log for instance "([^"]*)" eventually contains "([^"]*)"$`, theDaemonLogForInstanceEventuallyContains)

	// niwa_ask / niwa_wait steps
	ctx.Step(`^the coordinator asks the worker a question and the worker replies$`, theCoordinatorAsksWorkerAndReplies)
	ctx.Step(`^the ask response contains the answer$`, theAskResponseContainsAnswer)
	ctx.Step(`^the coordinator calls niwa_ask with timeout (\d+) seconds and no reply$`, theCoordinatorCallsAskWithTimeout)
	ctx.Step(`^(\d+) "([^"]*)" messages are placed in the coordinator inbox$`, nMessagesPlacedInCoordinatorInbox)
	ctx.Step(`^the coordinator calls niwa_wait for "([^"]*)" messages with count (\d+)$`, theCoordinatorCallsWait)
	ctx.Step(`^the coordinator sends a message with invalid type "([^"]*)"$`, coordinatorSendsWithInvalidType)

	// Assertions
	ctx.Step(`^the exit code is (\d+)$`, theExitCodeIs)
	ctx.Step(`^the exit code is not (\d+)$`, theExitCodeIsNot)
	ctx.Step(`^the output contains "([^"]*)"$`, theOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, theOutputDoesNotContain)
	ctx.Step(`^the output equals "([^"]*)"$`, theOutputEquals)
	ctx.Step(`^the output is empty$`, theOutputIsEmpty)
	ctx.Step(`^the error output contains "([^"]*)"$`, theErrorOutputContains)
	ctx.Step(`^the error output does not contain "([^"]*)"$`, theErrorOutputDoesNotContain)
	ctx.Step(`^the error output does not contain an ANSI escape sequence$`, theErrorOutputDoesNotContainAnsiEscapeSequence)
	ctx.Step(`^the response file contains the path to workspace "([^"]*)"$`, theResponseFileContainsWorkspace)
	ctx.Step(`^the response file does not exist$`, theResponseFileDoesNotExist)
	ctx.Step(`^the wrapped shell ended in workspace "([^"]*)"$`, theWrappedShellEndedInWorkspace)
	ctx.Step(`^the wrapped shell did not change directory$`, theWrappedShellDidNotChangeDirectory)
	ctx.Step(`^no niwa temp files remain in the system temp directory$`, noNiwaTempFilesRemain)
	ctx.Step(`^a foreign directory "([^"]*)" exists in the workspace root$`, aForeignDirectoryExistsAtInstancePath)
	ctx.Step(`^I write "([^"]*)" to file "([^"]*)" in repo "([^"]*)" of instance "([^"]*)"$`, func(ctx context.Context, content, relPath, groupRepo, instanceName string) (context.Context, error) {
		return iWriteFileToRepoInInstance(ctx, content, relPath, groupRepo, instanceName)
	})
	ctx.Step(`^the completion output contains "([^"]*)"$`, theCompletionOutputContains)
	ctx.Step(`^the completion output does not contain "([^"]*)"$`, theCompletionOutputDoesNotContain)
	ctx.Step(`^the completion description for "([^"]*)" is "([^"]*)"$`, theCompletionDescriptionMatches)

	// File assertions
	ctx.Step(`^the file "([^"]*)" exists in instance "([^"]*)"$`, theFileExistsInInstance)
	ctx.Step(`^the file "([^"]*)" does not exist in instance "([^"]*)"$`, theFileDoesNotExistInInstance)
	ctx.Step(`^the file "([^"]*)" in instance "([^"]*)" contains "([^"]*)"$`, theFileInInstanceContains)
	ctx.Step(`^the file "([^"]*)" in instance "([^"]*)" does not contain "([^"]*)"$`, theFileInInstanceDoesNotContain)

	// Claude integration
	ctx.Step(`^claude is available$`, claudeIsAvailable)
	ctx.Step(`^I run claude -p from instance root "([^"]*)" with prompt:$`, iRunClaudePFromInstanceRoot)
	ctx.Step(`^I run claude -p from repo "([^"]*)" in instance "([^"]*)" with prompt:$`, iRunClaudePFromRepoInInstance)
}
