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
	binPath       string             // absolute path to the niwa test binary
	homeDir       string             // sandboxed $HOME for this scenario (holds .niwa/, .bashrc, etc.)
	tmpDir        string             // scenario-scoped $TMPDIR (writes landed here stay isolated)
	workspaceRoot string             // sandboxed directory where workspaces live
	stdout        string             // last command's stdout
	stderr        string             // last command's stderr
	exitCode      int                // last command's exit code
	shellPwd      string             // pwd reported by the last wrapped-shell run
	shellStartPwd string             // cwd the wrapped shell started in (for "did not change" assertions)
	envOverrides  map[string]string  // per-scenario env var overrides (win over defaults)
	gitServer     *localGitServer    // local bare-repo server for offline clone tests
	repoURLs      map[string]string  // name → file:// URL for repos created by localGitServer
	githubFake    *tarballFakeServer // GitHub API fake (per-scenario; spawned lazily)
	pathPrefix    string             // dir prepended to $PATH for niwa subprocesses (e.g. a fake claude)

	// printedWorktreePath records the stdout of the last `niwa worktree
	// from-hook` create dispatch (the bare absolute worktree path the hook
	// prints back to Claude), so a later step can assert it exists / is gone.
	printedWorktreePath string

	// Session scenario state. Carry session lifecycle state between steps
	// in a single scenario; zeroed per the Before hook's fresh allocation.
	lastSessionID           string // ID parsed from `niwa session create`
	lastSessionWorktreePath string // worktree path parsed from `niwa session create`

	// worktreeFileSnapshots records the bytes of named files inside the last
	// worktree at snapshot time, so a later step can assert an idempotent
	// re-apply produced no spurious change. Keyed by worktree-relative path.
	worktreeFileSnapshots map[string]string

	// lastDispatchInstancePath records the disp-<hex> instance directory
	// discovered after a `niwa dispatch` run, so later steps can assert its
	// presence/absence without hardcoding the random name suffix.
	lastDispatchInstancePath string
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

	// After hook: tear down per-scenario fakes.
	ctx.After(func(ctx context.Context, sc *godog.Scenario, scenarioErr error) (context.Context, error) {
		s := getState(ctx)
		if s == nil {
			return ctx, nil
		}
		if s.githubFake != nil {
			s.githubFake.Close()
			s.githubFake = nil
		}
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
	ctx.Step(`^I run "([^"]*)" from workspace root$`, iRunFromWorkspaceRoot)
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
	ctx.Step(`^a config repo "([^"]*)" exists with a "([^"]*)" source file and body:$`, aConfigRepoWithSourceFileAndBody)
	ctx.Step(`^an overlay repo "([^"]*)" exists with body:$`, anOverlayRepoExistsWithBody)
	ctx.Step(`^a source repo "([^"]*)" exists$`, aSourceRepoExists)
	ctx.Step(`^I run niwa init from config repo "([^"]*)"$`, iRunNiwaInitFromConfigRepo)
	ctx.Step(`^I run niwa init from config repo "([^"]*)" with overlay "([^"]*)"$`, iRunNiwaInitFromConfigRepoWithOverlay)
	ctx.Step(`^I run niwa init "([^"]*)" from config repo "([^"]*)"$`, iRunNiwaInitNamedFromConfigRepo)
	ctx.Step(`^I run niwa init "([^"]*)" from config repo "([^"]*)" with --rebind$`, iRunNiwaInitNamedFromConfigRepoWithRebind)
	ctx.Step(`^I source the bash wrapper and run niwa init "([^"]*)" from config repo "([^"]*)"$`, iSourceWrapperAndRunNiwaInitNamed)
	ctx.Step(`^I pre-create directory "([^"]*)"$`, iPreCreateDirectory)
	ctx.Step(`^the registry already has workspace "([^"]*)" rooted at "([^"]*)"$`, iRegisterWorkspaceAt)
	ctx.Step(`^the workspace root "([^"]*)" has a workspace\.toml$`, theWorkspaceRootHasWorkspaceTOML)
	ctx.Step(`^the file "([^"]*)" exists under workspace root "([^"]*)"$`, theFileExistsUnderWorkspaceRoot)
	ctx.Step(`^the file "([^"]*)" does not exist under workspace root "([^"]*)"$`, theFileDoesNotExistUnderWorkspaceRoot)
	ctx.Step(`^the file "([^"]*)" under workspace root "([^"]*)" contains "([^"]*)"$`, theFileUnderWorkspaceRootContains)
	ctx.Step(`^the registry has workspace "([^"]*)" rooted at "([^"]*)"$`, theRegistryHasWorkspaceRootedAt)
	ctx.Step(`^the registry entry "([^"]*)" still points at "([^"]*)"$`, theRegistryHasWorkspaceRootedAt)
	ctx.Step(`^niwa go "([^"]*)" from outside lands in "([^"]*)"$`, niwaGoFromOutsideLandsIn)
	ctx.Step(`^I run "([^"]*)" from instance "([^"]*)" of workspace "([^"]*)"$`, iRunFromInstance)
	ctx.Step(`^the instance "([^"]*)" exists$`, theInstanceExists)
	ctx.Step(`^the instance "([^"]*)" does not exist$`, theInstanceDoesNotExist)
	ctx.Step(`^the repo "([^"]*)" exists in instance "([^"]*)"$`, theRepoExistsInInstance)

	// workspace-config-sources scenarios (PRD #72 regression + R28 lazy convert)
	ctx.Step(`^the config repo "([^"]*)" is force-pushed to:$`, func(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
		return theConfigRepoIsForcePushedTo(ctx, name, body.Content)
	})
	ctx.Step(`^the provenance marker exists$`, theProvenanceMarkerExistsInWorkspaceRoot)
	ctx.Step(`^the config dir is a git working tree from config repo "([^"]*)"$`, theConfigDirIsAGitWorkingTree)
	ctx.Step(`^a dispatch brief "([^"]*)" exists in the workspace root$`, aDispatchBriefExistsInWorkspaceRoot)
	ctx.Step(`^the dispatch brief "([^"]*)" still exists in the workspace root$`, theDispatchBriefStillExistsInWorkspaceRoot)

	// Assertions
	ctx.Step(`^the exit code is (\d+)$`, theExitCodeIs)
	ctx.Step(`^the exit code is not (\d+)$`, theExitCodeIsNot)
	ctx.Step(`^I append "([^"]*)" to file "([^"]*)" in instance "([^"]*)"$`, iAppendToFileInInstance)
	ctx.Step(`^the file "([^"]*)" in HOME contains "([^"]*)"$`, theFileInHomeContains)
	ctx.Step(`^the output contains "([^"]*)"$`, theOutputContains)
	ctx.Step(`^the output does not contain "([^"]*)"$`, theOutputDoesNotContain)
	ctx.Step(`^the output equals "([^"]*)"$`, theOutputEquals)
	ctx.Step(`^the output is empty$`, theOutputIsEmpty)
	ctx.Step(`^the error output contains "([^"]*)"$`, theErrorOutputContains)
	ctx.Step(`^the error output does not contain "([^"]*)"$`, theErrorOutputDoesNotContain)
	ctx.Step(`^the error output does not contain an ANSI escape sequence$`, theErrorOutputDoesNotContainAnsiEscapeSequence)
	ctx.Step(`^the response file contains the path to workspace "([^"]*)"$`, theResponseFileContainsWorkspace)
	ctx.Step(`^the response file contains the path to the parent of workspace "([^"]*)"$`, theResponseFileContainsWorkspaceParent)
	ctx.Step(`^the response file does not exist$`, theResponseFileDoesNotExist)
	ctx.Step(`^the response file is empty$`, theResponseFileIsEmpty)
	ctx.Step(`^the workspace "([^"]*)" does not exist$`, theWorkspaceDirDoesNotExist)
	ctx.Step(`^the instance "([^"]*)" of workspace "([^"]*)" exists$`, theInstanceOfWorkspaceExists)
	ctx.Step(`^the instance "([^"]*)" of workspace "([^"]*)" does not exist$`, theInstanceOfWorkspaceDoesNotExist)
	ctx.Step(`^the wrapped shell ended in workspace "([^"]*)"$`, theWrappedShellEndedInWorkspace)
	ctx.Step(`^the wrapped shell did not change directory$`, theWrappedShellDidNotChangeDirectory)
	ctx.Step(`^no niwa temp files remain in the system temp directory$`, noNiwaTempFilesRemain)
	ctx.Step(`^a foreign directory "([^"]*)" exists in the workspace root$`, aForeignDirectoryExistsAtInstancePath)
	ctx.Step(`^I write "([^"]*)" to file "([^"]*)" in repo "([^"]*)" of instance "([^"]*)"$`, func(ctx context.Context, content, relPath, groupRepo, instanceName string) (context.Context, error) {
		return iWriteFileToRepoInInstance(ctx, content, relPath, groupRepo, instanceName)
	})
	ctx.Step(`^I write to file "([^"]*)" in repo "([^"]*)" of instance "([^"]*)" with body:$`, iWriteFileBodyToRepoInInstance)
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

	// --- Session lifecycle steps ---
	ctx.Step(`^I call niwa_create_session for repo "([^"]*)" with purpose "([^"]*)" in instance "([^"]*)"$`, iCallCreateSession)
	ctx.Step(`^I call niwa worktree create for repo "([^"]*)" with purpose "([^"]*)" in instance "([^"]*)"$`, iCallCreateWorktree)
	ctx.Step(`^I call niwa worktree apply for the last session in instance "([^"]*)"$`, iCallApplyWorktree)
	ctx.Step(`^I call niwa session apply for the last session in instance "([^"]*)"$`, iCallApplySessionAlias)
	ctx.Step(`^I snapshot the file "([^"]*)" in the last worktree$`, iSnapshotLastWorktreeFile)
	ctx.Step(`^the file "([^"]*)" in the last worktree is unchanged$`, theLastWorktreeFileIsUnchanged)
	ctx.Step(`^the last command stderr contains the session deprecation notice$`, theLastCommandStderrContainsDeprecationNotice)
	ctx.Step(`^I call niwa_destroy_session in instance "([^"]*)"$`, iCallDestroySession)
	ctx.Step(`^I run niwa session detach for the last session in instance "([^"]*)"$`, iRunSessionDetachForLastSessionInInstance)
	ctx.Step(`^I run "([^"]*)" from channeled instance "([^"]*)"$`, iRunFromChanneledInstance)
	ctx.Step(`^I seed a live attach sentinel for the last session in instance "([^"]*)"$`, iSeedLiveAttachSentinelForLastSession)
	ctx.Step(`^I call niwa_destroy_session without force in instance "([^"]*)"$`, iCallDestroySessionWithoutForce)
	ctx.Step(`^I write an uncommitted change "([^"]*)" in the last worktree$`, iWriteUncommittedChangeInLastWorktree)
	ctx.Step(`^I call niwa worktree destroy for the last session in instance "([^"]*)"$`, iCallDestroyWorktree)
	ctx.Step(`^I call niwa worktree destroy --force for the last session in instance "([^"]*)"$`, iCallDestroyWorktreeForce)
	ctx.Step(`^the session is active in instance "([^"]*)"$`, theSessionIsActiveInInstance)
	ctx.Step(`^the last session is active in instance "([^"]*)"$`, theSessionIsActiveInInstance)
	ctx.Step(`^the session is ended in instance "([^"]*)"$`, theSessionIsEndedInInstance)
	ctx.Step(`^the session worktree exists in instance "([^"]*)"$`, theSessionWorktreeExistsInInstance)
	ctx.Step(`^the session scaffold directory "([^"]*)" exists in the worktree$`, theSessionScaffoldDirExistsInWorktree)
	// --- Session CLI steps (Issue #5) ---
	ctx.Step(`^the response file contains the last session worktree path in instance "([^"]*)"$`, theResponseFileContainsLastSessionWorktreePath)
	ctx.Step(`^the response file contains a path under instance "([^"]*)" worktrees directory$`, theResponseFileContainsPathUnderWorktreesDir)
	ctx.Step(`^a session lifecycle state file exists for repo "([^"]*)" with status "([^"]*)" in instance "([^"]*)"$`, aSessionLifecycleStateExistsForRepo)
	ctx.Step(`^I run "niwa go ([^"]*)" with last session id$`, iRunNiwaGoWithLastSessionID)

	// --- Session destroy gate + branch/worktree assertions ---
	ctx.Step(`^the last MCP response contains code "([^"]*)"$`, theLastMCPResponseContainsCode)
	ctx.Step(`^the main clone of "([^"]*)" in instance "([^"]*)" is on branch "([^"]*)"$`, theMainCloneIsOnBranch)
	ctx.Step(`^the session branch exists in repo "([^"]*)" of instance "([^"]*)"$`, theSessionBranchExistsInRepo)
	ctx.Step(`^the session branch does not exist in repo "([^"]*)" of instance "([^"]*)"$`, theSessionBranchDoesNotExistInRepo)
	ctx.Step(`^the session worktree directory does not exist$`, theSessionWorktreeDirectoryDoesNotExist)

	ctx.Step(`^a single-repo channeled workspace "([^"]*)" exists$`, iSetUpSingleRepoChanneledWorkspace)
	ctx.Step(`^a single-repo channeled workspace "([^"]*)" exists with repo content$`, iSetUpSingleRepoChanneledWorkspaceWithContent)
	ctx.Step(`^the file "([^"]*)" exists in the last worktree$`, theFileExistsInLastWorktree)
	ctx.Step(`^the file "([^"]*)" in the last worktree contains "([^"]*)"$`, theFileInLastWorktreeContains)

	// --- @critical: rank-2 deprecation + plugin auto-install ---
	registerRank2Steps(ctx)

	// --- git invisibility: niwa stays out of managed repos' git status ---
	registerGitInvisibilitySteps(ctx)

	// --- plugin-record lifecycle: destroy prune, create/update heal,
	// release-tracking (Issue 8) ---
	registerPluginRecordSteps(ctx)

	// --- worktree-delegation integration: from-hook create/remove + the
	// supported/deny/opt-out install branches (deterministic via a fake claude) ---
	registerWorktreeDelegationSteps(ctx)

	// --- ephemeral-session integration: instance from-hook provision/teardown
	// and the orphan reaper, driven against the offline localGitServer ---
	registerEphemeralSessionSteps(ctx)

	// --- dispatch lifecycle: niwa dispatch provision/rollback and reaper
	// reclamation, driven offline against the localGitServer with a fake claude ---
	registerDispatchSteps(ctx)

	// --- plugin pre-warm settings drift (#179): the pre-warm must not dirty
	// niwa's managed settings.json while still resolving plugins to disk ---
	registerPrewarmDriftSteps(ctx)

	// --- Init-bootstrap harness extensions (Issue 5) ---
	// GitHub tarball fake — spun up lazily per scenario; backed by the
	// existing tarballFakeServer used by unit tests. Wire it into the
	// niwa binary via NIWA_GITHUB_API_URL.
	ctx.Step(`^a GitHub fake is configured$`, aGitHubFakeIsConfigured)
	ctx.Step(`^the GitHub fake serves "([^"]*)" at ref "([^"]*)" with a workspace marker$`, theGitHubFakeServesAtRefWithWorkspaceMarker)
	ctx.Step(`^the GitHub fake serves "([^"]*)" at ref "([^"]*)" empty$`, theGitHubFakeServesAtRefEmpty)
	ctx.Step(`^the GitHub fake returns HTTP (\d+) for "([^"]*)" at ref "([^"]*)"$`, theGitHubFakeReturnsStatusForAtRef)
	ctx.Step(`^the GitHub fake returns HTTP (\d+) for "([^"]*)" repo metadata$`, theGitHubFakeReturnsStatusForRepoMetadata)
	ctx.Step(`^the GitHub fake serves "([^"]*)" repo metadata with body:$`, theGitHubFakeServesRepoMetadataWithBody)

	// TTY simulation: drive niwa init under util-linux `script -q` so
	// stdin is a real pty. The supplied input is fed line-by-line.
	ctx.Step(`^I run "([^"]*)" under a pty with input "([^"]*)"$`, iRunUnderPTYWithInput)
}
