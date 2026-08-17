package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// stubCodexTracked replaces the git-tracked probe for one test, so the shape
// half of the ownership test can be exercised without a repository.
func stubCodexTracked(t *testing.T, tracked bool) {
	t.Helper()
	prev := codexPathTracked
	codexPathTracked = func(string, string) bool { return tracked }
	t.Cleanup(func() { codexPathTracked = prev })
}

// markerDoc is a composed document as niwa writes it: the generation marker on
// the first line, then content.
func markerDoc(body string) string {
	return CodexGenerationMarker + "\n\n" + body
}

// conflictApplier returns an applier whose reporter writes to the returned
// buffer, so a test can read the apply's warnings.
func conflictApplier(t *testing.T) (*Applier, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	applier.Reporter = NewReporterWithTTY(out, false)
	return applier, out
}

// codexConflictRepo builds the standard Codex fixture with a real git
// repository at the repo checkout, so the tracked half of the ownership test is
// exercised against git rather than a stub.
func codexConflictRepo(t *testing.T, fx codexFixture) (niwaDir, root string, cfg *config.WorkspaceConfig, repoDir string) {
	t.Helper()
	dir, workspaceRoot, loaded := codexWorkspace(t, fx)
	repo := filepath.Join(workspaceRoot, "ws", "tools", "app")
	gitInit(t, repo)
	return dir, workspaceRoot, loaded, repo
}

// commitInRepo writes rel inside repoDir and commits it. The add is forced
// because niwa's own exclude patterns cover both names it writes, and a
// repository committing content at one of them is exactly the case under test.
func commitInRepo(t *testing.T, repoDir, rel, content string) {
	t.Helper()
	full := filepath.Join(repoDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, repoDir, "add", "-f", rel)
	runGitWT(t, repoDir, "commit", "-m", "add "+rel)
}

// TestDetectCodexConflicts_OwnershipByShapeAndMarker pins the ownership test
// niwa applies to the two names it writes into a working tree, on the untracked
// side: the payload delivery is recognized by its shape (a symlink, or a
// directory carrying the payload config's marker) and a composed file by the
// generation marker on its first line.
func TestDetectCodexConflicts_OwnershipByShapeAndMarker(t *testing.T) {
	stubCodexTracked(t, false)

	cases := []struct {
		name             string
		setup            func(t *testing.T, repoDir string)
		payloadConflict  bool
		overrideConflict bool
	}{
		{
			name:  "both names free",
			setup: func(*testing.T, string) {},
		},
		{
			name: "niwa's own composed override",
			setup: func(t *testing.T, repoDir string) {
				writeFileT(t, filepath.Join(repoDir, CodexOverrideFileName), markerDoc("prior content\n"))
			},
		},
		{
			name: "an untracked file without the marker",
			setup: func(t *testing.T, repoDir string) {
				writeFileT(t, filepath.Join(repoDir, CodexOverrideFileName), "someone else's override\n")
			},
			overrideConflict: true,
		},
		{
			name: "a symlink at the override name",
			setup: func(t *testing.T, repoDir string) {
				target := filepath.Join(t.TempDir(), "elsewhere.md")
				writeFileT(t, target, markerDoc("marker at the far end\n"))
				if err := os.Symlink(target, filepath.Join(repoDir, CodexOverrideFileName)); err != nil {
					t.Fatal(err)
				}
			},
			overrideConflict: true,
		},
		{
			name: "niwa's own payload symlink",
			setup: func(t *testing.T, repoDir string) {
				payload := filepath.Join(t.TempDir(), CodexPayloadDirName)
				if err := os.MkdirAll(payload, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(payload, filepath.Join(repoDir, CodexPayloadDirName)); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "niwa's own payload copy",
			setup: func(t *testing.T, repoDir string) {
				dir := filepath.Join(repoDir, CodexPayloadDirName)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFileT(t, filepath.Join(dir, codexPayloadConfigName), "# "+CodexGenerationMarker+"\nx = 1\n")
			},
		},
		{
			name: "the repository's own .codex directory",
			setup: func(t *testing.T, repoDir string) {
				dir := filepath.Join(repoDir, CodexPayloadDirName)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFileT(t, filepath.Join(dir, codexPayloadConfigName), "model = \"o3\"\n")
			},
			payloadConflict: true,
		},
		{
			name: "a regular file at the .codex name",
			setup: func(t *testing.T, repoDir string) {
				writeFileT(t, filepath.Join(repoDir, CodexPayloadDirName), "not niwa's\n")
			},
			payloadConflict: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := t.TempDir()
			tc.setup(t, repoDir)

			verdict, err := DetectCodexConflicts("app", repoDir)
			if err != nil {
				t.Fatalf("DetectCodexConflicts: %v", err)
			}
			if got := verdict.SuppressesPayload(); got != tc.payloadConflict {
				t.Errorf("SuppressesPayload() = %v, want %v", got, tc.payloadConflict)
			}
			// The coupling runs one way: a payload conflict takes the override
			// with it, an override conflict never reaches the payload.
			wantOverride := tc.overrideConflict || tc.payloadConflict
			if got := verdict.SuppressesOverride(); got != wantOverride {
				t.Errorf("SuppressesOverride() = %v, want %v", got, wantOverride)
			}
		})
	}
}

// TestDetectCodexConflicts_TrackedContentIsAlwaysAConflict pins the half of the
// ownership test the content check cannot carry. A repository can commit a file
// carrying niwa's generation marker verbatim -- copied from another checkout, or
// from a branch where niwa's write was committed by accident. The marker alone
// would call that file niwa's and overwrite committed content; being untracked
// is the other half of the test, and it is what refuses here.
func TestDetectCodexConflicts_TrackedContentIsAlwaysAConflict(t *testing.T) {
	repoDir := t.TempDir()
	gitInit(t, repoDir)
	commitInRepo(t, repoDir, CodexOverrideFileName, markerDoc("committed, marker and all\n"))
	commitInRepo(t, repoDir, filepath.Join(CodexPayloadDirName, codexPayloadConfigName), "# "+CodexGenerationMarker+"\nx = 1\n")

	verdict, err := DetectCodexConflicts("app", repoDir)
	if err != nil {
		t.Fatalf("DetectCodexConflicts: %v", err)
	}
	if !verdict.SuppressesPayload() {
		t.Error("a tracked .codex carrying the marker was not treated as a conflict")
	}
	if !verdict.SuppressesOverride() {
		t.Error("a tracked override carrying the marker was not treated as a conflict")
	}
}

// TestCodexConflictSet_ExposesVerdicts pins what a run hands to the stages
// after materialization: the per-repository verdicts, and the per-root question
// the trust step asks -- was this repository's payload refused.
func TestCodexConflictSet_ExposesVerdicts(t *testing.T) {
	set := &CodexConflictSet{}
	set.Record(CodexRepoVerdict{Repo: "clean", RepoDir: "/ws/tools/clean"})
	set.Record(CodexRepoVerdict{
		Repo:    "occupied",
		RepoDir: "/ws/tools/occupied",
		Payload: &CodexConflict{Repo: "occupied", Path: "/ws/tools/occupied/" + CodexPayloadDirName, WholeRepo: true},
	})

	if got := len(set.Verdicts()); got != 2 {
		t.Errorf("Verdicts() carried %d repositories, want both", got)
	}
	if !set.PayloadConflicted("/ws/tools/occupied") {
		t.Error("PayloadConflicted did not report the conflicted repository root")
	}
	if set.PayloadConflicted("/ws/tools/clean") {
		t.Error("PayloadConflicted reported a clean repository root")
	}
	if got := set.Warnings(); len(got) != 1 || !strings.Contains(got[0], "\"occupied\"") {
		t.Errorf("Warnings() = %v, want one line naming the repository", got)
	}
	// A nil set answers every question the way a run without a detection pass
	// would, so no caller needs a nil check of its own.
	var absent *CodexConflictSet
	if absent.Conflicted("/anything") || absent.SuppressesPayload("occupied") || absent.PayloadConflicted("/ws/tools/occupied") {
		t.Error("a nil verdict set claimed a conflict")
	}
}

// TestApply_CommittedCodexDirectoryDegradesTheWholeRepository is the plan's
// first conflict criterion (PRD R12): a repository that commits its own .codex
// gets no payload delivery and no composed override, keeps every committed byte,
// and the apply says so by name.
func TestApply_CommittedCodexDirectoryDegradesTheWholeRepository(t *testing.T) {
	niwaDir, root, cfg, repoDir := codexConflictRepo(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})
	const ownConfig = "model = \"o3\"\napproval_policy = \"never\"\n"
	commitInRepo(t, repoDir, filepath.Join(CodexPayloadDirName, "config.toml"), ownConfig)

	applier, out := conflictApplier(t)
	if _, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Nothing niwa writes reached the repository.
	if got := readComposed(t, filepath.Join(repoDir, CodexPayloadDirName, "config.toml")); got != ownConfig {
		t.Errorf("the repository's committed config changed:\n%q", got)
	}
	if _, err := os.Lstat(filepath.Join(repoDir, CodexOverrideFileName)); !os.IsNotExist(err) {
		t.Errorf("a .codex conflict must suppress the override too (stat err: %v)", err)
	}
	info, err := os.Lstat(filepath.Join(repoDir, CodexPayloadDirName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the committed .codex directory was replaced by niwa's payload link")
	}

	// And the apply said so, naming the repository and the path.
	warnings := out.String()
	if !strings.Contains(warnings, "\"app\"") || !strings.Contains(warnings, filepath.Join(repoDir, CodexPayloadDirName)) {
		t.Errorf("apply output does not report the conflict by repository and path:\n%s", warnings)
	}
}

// TestApply_CommittedOverrideSuppressesOnlyTheOverride pins the other side of
// the coupling: the override is refused, and everything else this repository
// gets from niwa still arrives.
func TestApply_CommittedOverrideSuppressesOnlyTheOverride(t *testing.T) {
	niwaDir, root, cfg, repoDir := codexConflictRepo(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})
	const ownOverride = "the repository's own override, committed\n"
	commitInRepo(t, repoDir, CodexOverrideFileName, ownOverride)

	applier, out := conflictApplier(t)
	if _, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := readComposed(t, filepath.Join(repoDir, CodexOverrideFileName)); got != ownOverride {
		t.Errorf("the repository's committed override changed:\n%q", got)
	}

	// The payload delivery still landed.
	link := filepath.Join(repoDir, CodexPayloadDirName)
	if _, err := os.Stat(filepath.Join(link, codexPayloadConfigName)); err != nil {
		t.Errorf("the payload delivery was suppressed by an override-only conflict: %v", err)
	}

	// So did the exclude coverage, which is what keeps both names out of the
	// repository's git status.
	excludes := readComposed(t, filepath.Join(repoDir, ".git", "info", "exclude"))
	for _, pattern := range []string{CodexPayloadDirName, CodexOverrideFileName} {
		if !strings.Contains(excludes, "\n"+pattern+"\n") {
			t.Errorf("exclude file is missing the bare %q pattern:\n%s", pattern, excludes)
		}
	}

	warnings := out.String()
	if !strings.Contains(warnings, "\"app\"") || !strings.Contains(warnings, filepath.Join(repoDir, CodexOverrideFileName)) {
		t.Errorf("apply output does not report the conflict by repository and path:\n%s", warnings)
	}
}

// TestApply_ConflictArrivingAfterARecordDeletesNothing is the test the cleanup
// exemption exists for, and the one an implementation that only stops writing
// must fail.
//
// The pipeline's managed-file reconciliation deletes, by record, every path the
// current apply did not produce. A conflicted path is exactly a path the apply
// did not produce, so a repository that got its override on one apply and
// committed its own file at that name before the next would have that committed
// file deleted -- by the very apply that refused to touch it. The exemption is
// the apply handing its conflict verdicts to the cleanup.
func TestApply_ConflictArrivingAfterARecordDeletesNothing(t *testing.T) {
	niwaDir, root, cfg, repoDir := codexConflictRepo(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})

	applier, _ := conflictApplier(t)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	overridePath := filepath.Join(repoDir, CodexOverrideFileName)
	if !managedFileRecorded(t, instanceRoot, overridePath) {
		t.Fatalf("the first apply did not record %s; the test cannot see the deletion it exists to catch", overridePath)
	}

	// The repository now ships its own file at that name.
	const committed = "the repository's own override, committed after niwa's\n"
	if err := os.Remove(overridePath); err != nil {
		t.Fatal(err)
	}
	commitInRepo(t, repoDir, CodexOverrideFileName, committed)

	// Two more applies: the first is the one that would delete, the second
	// proves the exemption is not a one-run accident.
	for i := 0; i < 2; i++ {
		applier, out := conflictApplier(t)
		if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
		got, err := os.ReadFile(overridePath)
		if err != nil {
			t.Fatalf("apply %d deleted the repository's committed file: %v", i+1, err)
		}
		if string(got) != committed {
			t.Errorf("apply %d changed the committed file:\n%q", i+1, got)
		}
		if !strings.Contains(out.String(), overridePath) {
			t.Errorf("apply %d did not report the conflict:\n%s", i+1, out.String())
		}
		// The record drops the entry: the state must stop claiming niwa owns a
		// path it just declared foreign. Forward-carrying the entry would also
		// keep the file, but under a hash that no longer describes it.
		if managedFileRecorded(t, instanceRoot, overridePath) {
			t.Errorf("apply %d left %s in the managed-file record", i+1, overridePath)
		}
	}

	// Nothing was staged or deleted in the repository either.
	if status := gitStatusPorcelainWT(t, repoDir); status != "" {
		t.Errorf("git status is not clean after the conflicted applies:\n%s", status)
	}
}

// TestApply_ClearedConflictWritesFreshFilesAndRerecords closes the loop: the
// refusal is a verdict about the current state of the path, not a latch.
func TestApply_ClearedConflictWritesFreshFilesAndRerecords(t *testing.T) {
	niwaDir, root, cfg, repoDir := codexConflictRepo(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})
	commitInRepo(t, repoDir, CodexOverrideFileName, "committed for now\n")

	applier, _ := conflictApplier(t)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	overridePath := filepath.Join(repoDir, CodexOverrideFileName)
	if managedFileRecorded(t, instanceRoot, overridePath) {
		t.Fatalf("a conflicted path was recorded as a managed file")
	}

	// The repository drops the file.
	runGitWT(t, repoDir, "rm", "-q", CodexOverrideFileName)
	runGitWT(t, repoDir, "commit", "-m", "drop the override")

	applier, out := conflictApplier(t)
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}

	fresh := readComposed(t, overridePath)
	if !HasCodexGenerationMarker([]byte(fresh)) {
		t.Errorf("the override written after the conflict cleared carries no generation marker:\n%s", fresh)
	}
	if !strings.Contains(fresh, "sentinel-instance") || !strings.Contains(fresh, "sentinel-repo") {
		t.Errorf("the composed override is missing its layers:\n%s", fresh)
	}
	if !managedFileRecorded(t, instanceRoot, overridePath) {
		t.Error("the freshly written override was not re-recorded")
	}
	if strings.Contains(out.String(), "occupied by something niwa did not write") {
		t.Errorf("a cleared conflict still reported a conflict:\n%s", out.String())
	}
}

// TestApply_UntrackedMarkerFileIsRefreshedNormally pins the other side of the
// content test: a file at niwa's own name that is untracked and carries the
// marker is niwa's, whoever wrote it, and the apply overwrites it. Nothing
// committed is at stake, so this is not the case R12 protects.
func TestApply_UntrackedMarkerFileIsRefreshedNormally(t *testing.T) {
	niwaDir, root, cfg, repoDir := codexConflictRepo(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})
	overridePath := filepath.Join(repoDir, CodexOverrideFileName)
	writeFileT(t, overridePath, markerDoc("stale content from an earlier run\n"))

	applier, out := conflictApplier(t)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := readComposed(t, overridePath)
	if strings.Contains(got, "stale content from an earlier run") {
		t.Errorf("niwa's own prior override was not overwritten:\n%s", got)
	}
	if !strings.Contains(got, "sentinel-instance") {
		t.Errorf("the refreshed override is missing its layers:\n%s", got)
	}
	if !managedFileRecorded(t, instanceRoot, overridePath) {
		t.Error("the refreshed override was not recorded")
	}
	if strings.Contains(out.String(), "occupied by something niwa did not write") {
		t.Errorf("niwa's own file was reported as a conflict:\n%s", out.String())
	}
}

// TestCleanRemovedFiles_SkipsConflictedPaths exercises the exemption directly,
// against a hand-built result: a recorded path the run did not produce is
// deleted, and the same path is kept once the run's verdicts name it.
func TestCleanRemovedFiles_SkipsConflictedPaths(t *testing.T) {
	dir := t.TempDir()
	conflicted := filepath.Join(dir, CodexOverrideFileName)
	plain := filepath.Join(dir, "CLAUDE.local.md")

	for _, p := range []string{conflicted, plain} {
		writeFileT(t, p, "content\n")
	}

	conflicts := &CodexConflictSet{}
	conflicts.Record(CodexRepoVerdict{
		Repo:     "app",
		RepoDir:  dir,
		Override: &CodexConflict{Repo: "app", Path: conflicted},
	})

	a := &Applier{Reporter: NewReporterWithTTY(&bytes.Buffer{}, false)}
	existing := &InstanceState{ManagedFiles: []ManagedFile{{Path: conflicted}, {Path: plain}}}
	a.cleanRemovedFiles(existing, &pipelineResult{codexConflicts: conflicts})

	if _, err := os.Stat(conflicted); err != nil {
		t.Errorf("the cleanup deleted a conflicted path: %v", err)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Errorf("the cleanup did not delete an unproduced, unconflicted path (stat err: %v)", err)
	}

	// A result with no verdicts at all behaves exactly as it did before
	// conflicts existed.
	writeFileT(t, conflicted, "content\n")
	a.cleanRemovedFiles(existing, &pipelineResult{})
	if _, err := os.Stat(conflicted); !os.IsNotExist(err) {
		t.Errorf("a nil verdict set exempted a path (stat err: %v)", err)
	}
}

// managedFileRecorded reports whether the instance state records path as a
// managed file.
func managedFileRecorded(t *testing.T, instanceRoot, path string) bool {
	t.Helper()
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, mf := range state.ManagedFiles {
		if mf.Path == path {
			return true
		}
	}
	return false
}

// writeFileT writes content to path, creating parent directories.
func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
