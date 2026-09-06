package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// writeWorktreeSetupScript drops an executable script into the repo's
// scripts/setup/ inside a worktree fixture's clone-shaped directory.
func writeWorktreeSetupScript(t *testing.T, repoDir, name, body string) {
	t.Helper()
	dir := filepath.Join(repoDir, "scripts", "setup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// optIn returns a config with the repo opted into worktree setup.
func optIn(cfg *config.WorkspaceConfig, repo string) *config.WorkspaceConfig {
	on := true
	if cfg.Repos == nil {
		cfg.Repos = map[string]config.RepoOverride{}
	}
	ov := cfg.Repos[repo]
	ov.WorktreeSetup = &on
	cfg.Repos[repo] = ov
	return cfg
}

// TestApplyToWorktree_RunsRepoSetupWhenOptedIn is the feature.
//
// The script's effect has to be present IN THE WORKTREE and the script has to
// have run with the worktree as its working directory -- both, because a script
// that ran against the clone and wrote there would satisfy a weaker assertion
// while leaving the worktree exactly as unprovisioned as before.
func TestApplyToWorktree_RunsRepoSetupWhenOptedIn(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")

	// The script records its own working directory, so the assertion is about
	// where it ran rather than merely that it ran.
	writeWorktreeSetupScript(t, worktreePath, "01-provision.sh",
		"#!/bin/sh\npwd > ./setup-ran-in.txt\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(worktreePath, "setup-ran-in.txt"))
	if err != nil {
		t.Fatalf("the repo's setup script did not run against the worktree: %v", err)
	}

	// Compare resolved paths. `pwd` in the script reports the symlink-resolved
	// directory, and on macOS t.TempDir() sits under /var/folders, which is a
	// symlink to /private/var/folders. Comparing the raw strings fails there
	// while the script ran in exactly the right place.
	wantResolved, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		t.Fatalf("resolving the worktree path: %v", err)
	}
	if ranIn := strings.TrimSpace(string(got)); ranIn != wantResolved {
		t.Errorf("setup ran with cwd %q, want the worktree %q", ranIn, wantResolved)
	}
}

// TestApplyToWorktree_NoSetupWithoutOptIn pins the default. A repo that has not
// opted in must spawn no setup process at all -- not run one that exits early,
// which is a weaker property and would still execute repo-provided code in a
// place its author never intended.
func TestApplyToWorktree_NoSetupWithoutOptIn(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)

	writeWorktreeSetupScript(t, worktreePath, "01-provision.sh",
		"#!/bin/sh\ntouch ./should-not-exist.txt\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "should-not-exist.txt")); err == nil {
		t.Error("setup ran in a worktree of a repo that never opted in")
	}
}

// TestApplyToWorktree_OptInIsPerRepo covers the isolation half at the level a
// user sees it: opting repo A in must not run repo B's scripts.
func TestApplyToWorktree_OptInIsPerRepo(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "some-other-repo")

	writeWorktreeSetupScript(t, worktreePath, "01-provision.sh",
		"#!/bin/sh\ntouch ./should-not-exist.txt\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "should-not-exist.txt")); err == nil {
		t.Error("another repo's opt-in turned setup on for this one")
	}
}

// TestApplyToWorktree_SetupEnvironment pins the contract a script reads.
//
// The instance-root anchor is the substantive entry: it exists so a script can
// stop deriving the instance root by walking up from its working directory,
// which reaches <instanceRoot> from a clone and <instanceRoot>/.niwa from a
// worktree -- a real, writable, wrong directory, reached with no error.
func TestApplyToWorktree_SetupEnvironment(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")

	writeWorktreeSetupScript(t, worktreePath, "01-env.sh",
		"#!/bin/sh\n{ echo \"P=$NIWA_WORKTREE_PATH\"; echo \"R=$NIWA_WORKTREE_REPO\"; "+
			"echo \"U=$NIWA_WORKTREE_PURPOSE\"; echo \"B=$NIWA_WORKTREE_BRANCH\"; "+
			"echo \"I=$NIWA_INSTANCE_ROOT\"; } > ./env.txt\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"ship-it", "branch-xyz", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(worktreePath, "env.txt"))
	if err != nil {
		t.Fatalf("setup script did not run: %v", err)
	}
	got := string(raw)

	for _, want := range []string{
		"P=" + worktreePath,
		"R=app",
		"U=ship-it",
		"B=branch-xyz",
		"I=" + instanceRoot,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("setup environment missing %q; got:\n%s", want, got)
		}
	}
}

// TestApplyToWorktree_MixedSetupDirectoryGatesPerScript is the case the
// two-mechanism opt-in exists for, and the only test that proves the
// script-facing signal is usable rather than merely present.
//
// One scripts/setup/ can hold a dependency install that MUST run per tree and a
// git-hooks installer that must NOT -- git hooks live in the shared
// git-common-dir, so a per-tree run is duplicate work at best. The per-repo
// switch is all-or-nothing for the directory, so the script gates itself on
// NIWA_WORKTREE_PATH being present.
//
// An implementation that exported the signal identically on both surfaces would
// fail this: the gating script would exit early in the clone too.
func TestApplyToWorktree_MixedSetupDirectoryGatesPerScript(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")

	// Shared-state script: opts itself out of worktree runs.
	writeWorktreeSetupScript(t, worktreePath, "01-shared.sh",
		"#!/bin/sh\n[ -n \"$NIWA_WORKTREE_PATH\" ] && exit 0\ntouch ./shared-ran.txt\n")
	// Per-tree script: no gate, runs everywhere.
	writeWorktreeSetupScript(t, worktreePath, "02-pertree.sh",
		"#!/bin/sh\ntouch ./pertree-ran.txt\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}

	if _, err := os.Stat(filepath.Join(worktreePath, "pertree-ran.txt")); err != nil {
		t.Errorf("the ungated per-tree script did not run in the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreePath, "shared-ran.txt")); err == nil {
		t.Error("the shared-state script did not manage to gate itself out of the " +
			"worktree run, so NIWA_WORKTREE_PATH is not usable as a signal")
	}
}

// TestCloneSetupEnv_DoesNotCarryTheWorktreeSignal is the other half of the
// mixed-directory case, and without it the pair does not discriminate.
//
// The gating script above opts out when NIWA_WORKTREE_PATH is present. If the
// clone path also exported that variable, the same script would exit early on
// the clone too -- so the shared-state step would run NOWHERE, silently, and
// the worktree-only test above would still pass. That is the failure this test
// exists to catch, and it was caught by mutating cloneSetupEnv to leak the
// variable and finding the worktree test unmoved.
//
// Asserted against RunSetupScripts directly because that is where the clone
// path runs, and the property is about which environment reaches a script.
func TestCloneSetupEnv_DoesNotCarryTheWorktreeSignal(t *testing.T) {
	for _, entry := range cloneSetupEnv("/instance/root") {
		if strings.HasPrefix(entry, "NIWA_WORKTREE_") {
			t.Fatalf("the clone environment carries %q: a script that gates itself "+
				"on the worktree signal would then opt out of the clone run too, and "+
				"its shared-state work would run nowhere", entry)
		}
	}

	// And the anchor IS present, because retiring `cd ../..` on the clone path
	// is the reason this environment exists at all.
	var sawAnchor bool
	for _, entry := range cloneSetupEnv("/instance/root") {
		if entry == "NIWA_INSTANCE_ROOT=/instance/root" {
			sawAnchor = true
		}
	}
	if !sawAnchor {
		t.Error("the clone environment does not carry NIWA_INSTANCE_ROOT, so a clone " +
			"script still has to derive the instance root by walking up")
	}
}

// TestRunSetupScripts_MixedDirectoryRunsBothOnTheClone completes the pair by
// exercising the behaviour rather than the environment builder: against a
// clone, BOTH the gating script and the ungated one run.
func TestRunSetupScripts_MixedDirectoryRunsBothOnTheClone(t *testing.T) {
	repoDir := t.TempDir()
	writeWorktreeSetupScript(t, repoDir, "01-shared.sh",
		"#!/bin/sh\n[ -n \"$NIWA_WORKTREE_PATH\" ] && exit 0\ntouch \"$PWD/shared-ran.txt\"\n")
	writeWorktreeSetupScript(t, repoDir, "02-pertree.sh",
		"#!/bin/sh\ntouch \"$PWD/pertree-ran.txt\"\n")

	var buf strings.Builder
	result := RunSetupScripts(repoDir, "scripts/setup", NewReporter(&buf), nil,
		cloneSetupEnv("/instance/root")...)

	for _, s := range result.Scripts {
		if s.Error != nil {
			t.Fatalf("setup script %s failed: %v", s.Name, s.Error)
		}
	}
	if _, err := os.Stat(filepath.Join(repoDir, "shared-ran.txt")); err != nil {
		t.Errorf("the shared-state script did not run against the clone, so its work "+
			"happens nowhere: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "pertree-ran.txt")); err != nil {
		t.Errorf("the ungated script did not run against the clone: %v", err)
	}
}

// TestApplyToWorktree_SetupFailureIsDataNotError is the property that keeps this
// feature clear of niwa#285.
//
// A failing script must leave ApplyToWorktree returning nil, because the
// delegated WorktreeCreate path turns any error here into a guarded teardown
// that deletes the worktree -- and the teardown's dirty guard cannot tell a
// worktree that lost work from one that did not, since everything niwa writes
// is git-excluded. The script here writes NOTHING before failing, which is the
// worst case: the tree reads perfectly clean.
func TestApplyToWorktree_SetupFailureIsDataNotError(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")

	writeWorktreeSetupScript(t, worktreePath, "01-fails.sh",
		"#!/bin/sh\necho boom >&2\nexit 1\n")

	var sink SetupResult
	written, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{Setup: &sink})
	if err != nil {
		t.Fatalf("a failing setup script must not fail ApplyToWorktree -- on the "+
			"delegated create path that deletes the worktree: %v", err)
	}
	if written == nil {
		t.Error("expected the written-files list to survive a setup failure")
	}

	// And the failure must be visible as data, or the caller cannot report it.
	var sawFailure bool
	for _, s := range sink.Scripts {
		if s.Error != nil {
			sawFailure = true
		}
	}
	if !sawFailure {
		t.Errorf("the setup failure did not reach the sink, so no caller can report "+
			"it; sink was %+v", sink)
	}
}

// TestApplyToWorktree_SetupOutcomeSinkIsOptional pins that a caller which does
// not want the outcome is unaffected -- the nil-sink half of the idiom, at the
// level a caller uses it.
func TestApplyToWorktree_SetupOutcomeSinkIsOptional(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	optIn(cfg, "app")

	writeWorktreeSetupScript(t, worktreePath, "01-fails.sh", "#!/bin/sh\nexit 1\n")

	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
		"purpose", "branch", WorktreeApplyOptions{}); err != nil {
		t.Fatalf("a nil Setup sink must be a silent no-op, not an error: %v", err)
	}
}
