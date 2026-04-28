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

	// Mesh scenario state (Issue #10). These fields carry task + pause
	// state between steps in a single scenario. They are zeroed per the
	// scenario Before hook via the fresh testState allocation.
	lastTaskID      string // ID of the most-recently created task envelope
	pauseHookMarker string // base name of the active pause-hook marker file
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

	// After hook: kill any niwa daemons this scenario left behind. Daemons
	// are spawned with Setsid=true so they survive the test process exit
	// and would hold flocks on daemon.pid.lock across scenarios, starving
	// the next run. Walking the workspace root's children finds every
	// daemon.pid the scenario wrote; the PID is SIGKILLed directly so no
	// grace window applies.
	ctx.After(func(ctx context.Context, sc *godog.Scenario, scenarioErr error) (context.Context, error) {
		s := getState(ctx)
		if s == nil {
			return ctx, nil
		}
		killLeftoverDaemons(s.workspaceRoot)
		return ctx, nil
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
	ctx.Step(`^I run "niwa session register" from instance root$`, iRunNiwaSessionRegisterFromInstanceRoot)
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

	// workspace-config-sources scenarios (PRD #72 regression + R28 lazy convert)
	ctx.Step(`^the config repo "([^"]*)" is force-pushed to:$`, func(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
		return theConfigRepoIsForcePushedTo(ctx, name, body.Content)
	})
	ctx.Step(`^the provenance marker exists$`, theProvenanceMarkerExistsInWorkspaceRoot)
	ctx.Step(`^the config dir is a git working tree from config repo "([^"]*)"$`, theConfigDirIsAGitWorkingTree)

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

	// Headless coordination steps
	ctx.Step(`^I set up coordinator session for instance "([^"]*)"$`, iSetUpCoordinatorSessionForInstance)
	ctx.Step(`^I set up worker session for instance "([^"]*)"$`, iSetUpWorkerSessionForInstance)
	ctx.Step(`^I run claude -p from instance root "([^"]*)" with simulated worker reply and prompt:$`, iRunClaudePFromInstanceRootWithSimulatedWorkerReply)

	// --- Mesh feature: functional-test harness (Issue #10) ---
	ctx.Step(`^a channeled workspace "([^"]*)" exists$`, iSetUpChanneledWorkspace)
	ctx.Step(`^a channeled workspace "([^"]*)" with permissions "([^"]*)" exists$`, iSetUpChanneledWorkspaceWithPermissions)
	ctx.Step(`^the worker in instance "([^"]*)" was spawned with "([^"]*)"$`, theWorkerWasSpawnedWith)
	ctx.Step(`^the worker in instance "([^"]*)" was not spawned with "([^"]*)"$`, theWorkerWasNotSpawnedWith)
	ctx.Step(`^the daemon runs with fake worker scenario "([^"]*)"$`, iRunFakeWorkerWithScenario)
	ctx.Step(`^the daemon has small timing overrides$`, iSetDefaultTimingOverrides)
	ctx.Step(`^the daemon pauses before claiming envelopes$`, iPauseDaemonBeforeClaim)
	ctx.Step(`^the daemon pauses after claiming envelopes$`, iPauseDaemonAfterClaim)
	ctx.Step(`^the pause marker "([^"]*)" eventually appears$`, iPauseMarkerEventuallyAppears)
	ctx.Step(`^I release the daemon pause marker$`, iReleaseDaemonPauseMarker)
	ctx.Step(`^I delegate a task to role "([^"]*)" in instance "([^"]*)" with body '([^']*)'$`, func(ctx context.Context, role, instance, body string) (context.Context, error) {
		return iDelegateTaskToRole(ctx, instance, role, body)
	})
	ctx.Step(`^I cancel the task in instance "([^"]*)"$`, iCancelTheTask)
	ctx.Step(`^the task state in instance "([^"]*)" eventually becomes "([^"]*)"$`, theTaskStateEventuallyBecomes)
	ctx.Step(`^the task reason in instance "([^"]*)" contains "([^"]*)"$`, theTaskReasonContains)
	ctx.Step(`^the task restart_count in instance "([^"]*)" equals (\d+)$`, theTaskRestartCountEquals)
	ctx.Step(`^the task transitions log in instance "([^"]*)" contains "([^"]*)"$`, theTransitionsLogContains)
	ctx.Step(`^the daemon log for instance "([^"]*)" does not contain "([^"]*)"$`, theDaemonLogDoesNotContain)
	ctx.Step(`^the daemon log for instance "([^"]*)" does not contain any of "([^"]*)"$`, theDaemonLogDoesNotContainAnyOf)
	ctx.Step(`^I SIGKILL the daemon for instance "([^"]*)"$`, iSIGKILLTheDaemon)
	ctx.Step(`^I SIGKILL the worker for instance "([^"]*)"$`, iSIGKILLTheWorker)
	ctx.Step(`^I restart the daemon for instance "([^"]*)"$`, iRestartTheDaemon)
	ctx.Step(`^I run two concurrent applies for instance "([^"]*)"$`, iRunTwoConcurrentApplies)
	ctx.Step(`^exactly one daemon is running for instance "([^"]*)"$`, exactlyOneDaemonIsRunning)
	ctx.Step(`^I update the task body in instance "([^"]*)" to '([^']*)'$`, iUpdateTheTaskBody)
	ctx.Step(`^an unauthorized MCP call for instance "([^"]*)" receives NOT_TASK_PARTY$`, iVerifyAuthorizationDenied)
	ctx.Step(`^the output contains status "([^"]*)"$`, theOutputContainsStatus)

	// --- @channels-e2e (Issue #11): real `claude -p` scenarios ---
	ctx.Step(`^the daemon uses the real claude worker spawn path$`, iEnsureNoFakeWorker)
	ctx.Step(`^I run claude -p preserving case from instance root "([^"]*)" with prompt:$`, iRunClaudePFromInstanceRootPreservingCase)
	ctx.Step(`^I queue a niwa_finish_task instruction for role "([^"]*)" in instance "([^"]*)"$`, iDelegateTaskToRoleWithFinishInstruction)
	ctx.Step(`^the task state in instance "([^"]*)" eventually becomes "([^"]*)" within (\d+) seconds$`, theTaskStateEventuallyBecomesWithin)

	// --- @channels-e2e-graph: real coordinator -> real workers delegation graph ---
	ctx.Step(`^a multi-repo channeled workspace "([^"]*)" with web and backend exists$`, iSetUpMultiRepoChanneledWorkspace)
	ctx.Step(`^a single-repo channeled workspace "([^"]*)" exists$`, iSetUpSingleRepoChanneledWorkspace)
	ctx.Step(`^I plant a legacy session directory "([^"]*)" in instance "([^"]*)"$`, iPlantLegacySessionDir)
	ctx.Step(`^I delete file "([^"]*)" in instance "([^"]*)"$`, iDeleteFileInInstance)
	ctx.Step(`^I delete directory "([^"]*)" in instance "([^"]*)"$`, iDeleteDirectoryInInstance)
	ctx.Step(`^I run claude -p preserving case from instance root "([^"]*)" within (\d+) seconds with prompt:$`, iRunClaudePFromInstanceRootPreservingCaseWithin)
	ctx.Step(`^the file "([^"]*)" in repo "([^"]*)" of instance "([^"]*)" exactly matches "([^"]*)"$`, theFileInRepoOfInstanceExactlyMatches)
	ctx.Step(`^exactly (\d+) tasks in instance "([^"]*)" are in state "completed"$`, func(ctx context.Context, n int, instance string) error {
		return allTasksInInstanceAreCompleted(ctx, n, instance)
	})
	ctx.Step(`^the coordinator in instance "([^"]*)" emitted niwa_delegate calls to roles "([^"]*)"$`, theCoordinatorEmittedDelegateCallsForRoles)
	ctx.Step(`^role "([^"]*)" in instance "([^"]*)" emitted at least (\d+) successful niwa_finish_task calls?$`, func(ctx context.Context, role, instance string, n int) error {
		return roleEmittedFinishTaskCalls(ctx, role, n, instance)
	})
}
