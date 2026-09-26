package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// This file pins the output of the preparation path so a later refactor can
// prove it changed no behavior. It characterizes what main does today, not what
// anyone wishes it did: if an expectation here disagrees with the code, the
// expectation is what is wrong.
//
// The oracle is the pipeline's own record. Every write lands in writtenFiles,
// and Step 7 of runPipeline hashes each one into InstanceState.ManagedFiles. So
// the path set comes from the apply path itself rather than a hand-picked list,
// and a refactor that adds a file, drops one, or changes one's bytes moves the
// manifest.
//
// Regenerate after a deliberate behavior change with:
//
//	NIWA_UPDATE_CHARACTERIZATION=1 go test ./internal/workspace/ -run Characterization
//
// and read the diff before committing it.

// characterizationGoldenEnv is the opt-in for rewriting the checked-in
// manifests. It is an environment variable rather than a test flag so it cannot
// collide with flags registered elsewhere in the package's test binary.
const characterizationGoldenEnv = "NIWA_UPDATE_CHARACTERIZATION"

// pathPlaceholder is one absolute-path substitution applied to a managed file's
// bytes before hashing. Longest prefix first: an overlay or worktree directory
// nested under the instance root must be replaced by its own token, not by the
// instance root's.
type pathPlaceholder struct {
	abs   string
	token string
}

// characterizationFixture is one built workspace plus everything needed to
// normalize the absolute paths that leak into generated content.
type characterizationFixture struct {
	tmpDir       string
	niwaDir      string
	instanceRoot string
	overlaySrc   string
	globalDir    string
	niwaExe      string
	placeholders []pathPlaceholder
}

// TestApplyCharacterization pins the preparation path's output. Two manifests:
// the instance manifest, built by running Create over a fixture that exercises
// every content writer the pipeline reaches; and the worktree manifest, built by
// calling ApplyToWorktree, whose writers the instance path never reaches (a live
// worktree requires real git worktree registration, which would make the
// manifest depend on the host's git).
//
// The fixture deliberately exercises three accumulated boundary rules, because
// those are what a mechanical conversion of the context writers is most likely
// to drop silently:
//
//   - overlay append: repo "app" carries both a base content source and an
//     overlay= entry, so its CLAUDE.local.md is base + "\n" + overlay bytes.
//   - subdir content: repo "app" declares two subdirs, one nested inside the
//     other, so the subdir loop's target computation and containment check both
//     run.
//   - @import migration: the workspace content source ships the three legacy
//     relative imports (@workspace-context.md, @CLAUDE.overlay.md,
//     @CLAUDE.global.md), which the context installers strip out of CLAUDE.md
//     after writing it. All three removals are visible in the pinned hash.
func TestApplyCharacterization(t *testing.T) {
	t.Run("instance", func(t *testing.T) {
		fx := buildCharacterizationFixture(t)

		loaded, err := config.Load(filepath.Join(fx.niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}

		applier := newCharacterizationApplier(t, fx)

		gotRoot, err := applier.Create(context.Background(), loaded.Config,
			fx.niwaDir, fx.tmpDir, loaded.Config.Workspace.Name)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if gotRoot != fx.instanceRoot {
			t.Fatalf("Create returned %q, want %q", gotRoot, fx.instanceRoot)
		}

		state, err := LoadState(fx.instanceRoot)
		if err != nil {
			t.Fatalf("loading state: %v", err)
		}
		if len(state.ManagedFiles) == 0 {
			t.Fatal("state recorded no managed files; the fixture wrote nothing")
		}

		// Tie the manifest to the pipeline's own record: every file it claims
		// to manage must still hash to what it recorded. Without this the
		// manifest would only pin the bytes on disk, not the bookkeeping.
		for _, mf := range state.ManagedFiles {
			if mf.Generated.IsZero() {
				t.Errorf("managed file %s has a zero Generated timestamp", mf.Path)
			}
			live, hashErr := HashFile(mf.Path)
			if hashErr != nil {
				t.Fatalf("re-hashing managed file %s: %v", mf.Path, hashErr)
			}
			if live != mf.ContentHash {
				t.Errorf("managed file %s: recorded hash %s, on-disk hash %s",
					mf.Path, mf.ContentHash, live)
			}
		}

		paths := make([]string, 0, len(state.ManagedFiles))
		seen := map[string]bool{}
		for _, mf := range state.ManagedFiles {
			if seen[mf.Path] {
				t.Errorf("managed file %s recorded twice", mf.Path)
			}
			seen[mf.Path] = true
			paths = append(paths, mf.Path)
		}

		fx.assertInstanceBoundaryRules(t)

		manifest := fx.manifest(t, paths)
		fx.compare(t, "instance_managed_files.txt", manifest)
	})

	t.Run("worktree", func(t *testing.T) {
		fx := buildCharacterizationFixture(t)

		loaded, err := config.Load(filepath.Join(fx.niwaDir, "workspace.toml"))
		if err != nil {
			t.Fatalf("loading config: %v", err)
		}

		applier := newCharacterizationApplier(t, fx)
		if _, err := applier.Create(context.Background(), loaded.Config,
			fx.niwaDir, fx.tmpDir, loaded.Config.Workspace.Name); err != nil {
			t.Fatalf("Create: %v", err)
		}

		// The worktree path takes the overlay-merged config, the same value the
		// pipeline hands its own worktree writers, so the overlay-append rule is
		// exercised here too.
		overlayDir, merged := characterizationMergedConfig(t, fx, loaded.Config)

		worktreePath := filepath.Join(fx.instanceRoot, ".niwa", "worktrees", "app-ship")
		if err := os.MkdirAll(worktreePath, 0o755); err != nil {
			t.Fatal(err)
		}
		fx.placeholders = append([]pathPlaceholder{
			{abs: worktreePath, token: "{WORKTREE}"},
		}, fx.placeholders...)

		written, err := ApplyToWorktree(merged, fx.niwaDir, fx.instanceRoot, worktreePath,
			"public", "app", "ship the contract", "session/ship", WorktreeApplyOptions{
				OverlayDir: overlayDir,
				Stderr:     io.Discard,
				WorktreeDelegation: &WorktreeDelegation{
					Supported: true,
					NiwaPath:  fx.niwaExe,
				},
			})
		if err != nil {
			t.Fatalf("ApplyToWorktree: %v", err)
		}
		if len(written) == 0 {
			t.Fatal("ApplyToWorktree wrote nothing; the fixture is wrong")
		}

		// Characterized, not endorsed: the returned list names the worktree's
		// CLAUDE.local.md twice, once from InstallRepoContentTo and once from
		// installWorktreeContextLayer, which writes the context section into the
		// same file. Callers hash the list into ManagedFiles, so today the
		// duplicate is harmless — but the conversion must not change the count
		// without someone noticing.
		if got := countOccurrences(written, filepath.Join(worktreePath, "CLAUDE.local.md")); got != 2 {
			t.Errorf("worktree CLAUDE.local.md appears %d time(s) in the written list, want 2 (current behavior)", got)
		}

		fx.assertWorktreeBoundaryRules(t, worktreePath)

		manifest := fx.manifest(t, written)
		fx.compare(t, "worktree_files.txt", manifest)
	})
}

// characterizationMergedConfig re-runs the overlay merge the pipeline performs
// in Step 0.6, so the worktree subtest sees the same effective config (repo
// "app" carrying an OverlaySource) the instance path did.
func characterizationMergedConfig(t *testing.T, fx *characterizationFixture, base *config.WorkspaceConfig) (string, *config.WorkspaceConfig) {
	t.Helper()
	overlayDir, err := config.OverlayDir(characterizationOverlayURL)
	if err != nil {
		t.Fatalf("resolving overlay dir: %v", err)
	}
	overlay, err := config.ParseOverlay(filepath.Join(overlayDir, "workspace-overlay.toml"))
	if err != nil {
		t.Fatalf("parsing overlay: %v", err)
	}
	merged, err := MergeWorkspaceOverlay(base, overlay, overlayDir)
	if err != nil {
		t.Fatalf("merging overlay: %v", err)
	}
	return overlayDir, merged
}

// characterizationOverlayURL is the overlay the fixture registers in the
// workspace-root init state. Create reads that state, so the overlay branch of
// the pipeline runs without the test having to call Apply.
const characterizationOverlayURL = "testorg/char-ws-overlay"

// newCharacterizationApplier builds the Applier the fixture runs under, with
// every network- and host-touching seam replaced by a fixed local value.
func newCharacterizationApplier(t *testing.T, fx *characterizationFixture) *Applier {
	t.Helper()

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {
				{Name: "app", Visibility: "public", SSHURL: "git@github.com:testorg/app.git"},
				{Name: "secrets", Visibility: "private", SSHURL: "git@github.com:testorg/secrets.git"},
			},
		},
	})
	applier.Cloner = &Cloner{}
	applier.GlobalConfigDir = fx.globalDir
	applier.Reporter = NewReporterWithTTY(io.Discard, false)

	// Materialize the overlay from a local directory instead of cloning it.
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		return false, 0, copyTreeForCharacterization(fx.overlaySrc, dir)
	}
	applier.headSHA = func(string) (string, error) { return "0123456789abcdef", nil }

	// The plugin registry heal and the marketplace reconcile both target the
	// real ~/.claude. Neither writes a managed file, so stub them rather than
	// letting a test touch the developer's home directory.
	applier.prunePluginRecords = nil
	applier.reconcileMarketplaceAutoUpdate = nil

	return applier
}

// buildCharacterizationFixture writes the workspace config, content sources,
// overlay, global config, hooks, and env files, and pre-creates the repo
// checkouts so the cloner skips them.
func buildCharacterizationFixture(t *testing.T) *characterizationFixture {
	t.Helper()

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	instanceRoot := filepath.Join(tmpDir, "char-ws")

	// The overlay clone lands under XDG_CONFIG_HOME, and the plugin heal reads
	// HOME. Redirect both into the fixture so nothing escapes the tempdir.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))

	// The worktree-delegation probe shells out to `claude --version`, so the
	// hook-vs-deny branch would otherwise depend on what is installed on the
	// machine running the test. A fake ahead of the real one on PATH fixes the
	// branch to "supported".
	fakeBin := filepath.Join(tmpDir, "fakebin")
	charMkdirAll(t, fakeBin)
	charWriteFile(t, filepath.Join(fakeBin, "claude"), "#!/bin/sh\necho '9.9.9 (Claude Code)'\n", 0o755)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The generated worktree hook command embeds os.Executable(), which under
	// `go test` is a per-run binary path. The test binary is the same executable
	// the pipeline resolves, so reading it here yields exactly the string that
	// will appear in the output, and normalization can substitute it.
	niwaExe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolving test executable: %v", err)
	}

	charMkdirAll(t, filepath.Join(contentDir, "repos"))
	charMkdirAll(t, filepath.Join(niwaDir, "hooks", "pre_tool_use"))
	charMkdirAll(t, filepath.Join(niwaDir, "env"))
	charMkdirAll(t, filepath.Join(niwaDir, "dist"))

	// The workspace content source carries {workspace} (the absolute instance
	// root, normalized below) and the three legacy relative imports the context
	// installers migrate away.
	charWriteFile(t, filepath.Join(contentDir, "workspace.md"), strings.Join([]string{
		"# {workspace_name} Workspace",
		"",
		"@workspace-context.md",
		"",
		"@CLAUDE.overlay.md",
		"",
		"@CLAUDE.global.md",
		"",
		"Root: {workspace}",
		"",
	}, "\n"), 0o644)

	charWriteFile(t, filepath.Join(contentDir, "public.md"),
		"# {group_name} Group\n\nWorkspace: {workspace_name}\nRoot: {workspace}\n", 0o644)
	charWriteFile(t, filepath.Join(contentDir, "repos", "app.md"),
		"# {repo_name}\n\nGroup: {group_name}\nRoot: {workspace}\n", 0o644)
	charWriteFile(t, filepath.Join(contentDir, "repos", "app-docs.md"),
		"# {repo_name} docs\n", 0o644)
	charWriteFile(t, filepath.Join(contentDir, "repos", "app-docs-deep.md"),
		"# {repo_name} docs/deep\n", 0o644)
	// No [content.repos.secrets] entry: this one is picked up by the
	// repos/<name>.md auto-discovery branch.
	charWriteFile(t, filepath.Join(contentDir, "repos", "secrets.md"),
		"# Auto-discovered {repo_name}\n", 0o644)
	// The worktree layer's body template.
	charWriteFile(t, filepath.Join(contentDir, "worktree.md"),
		"Purpose: {purpose}\nBranch: {branch}\nRepo: {repo_name}\nRoot: {workspace}\n", 0o644)

	charWriteFile(t, filepath.Join(niwaDir, "hooks", "pre_tool_use", "lint.sh"),
		"#!/bin/sh\necho lint\n", 0o755)
	charWriteFile(t, filepath.Join(niwaDir, "env", "workspace.env"), "WORKSPACE_VAR=hello\n", 0o644)
	charWriteFile(t, filepath.Join(niwaDir, "dist", "editorconfig"), "root = true\n", 0o644)

	configTOML := `
[workspace]
name = "char-ws"
content_dir = "claude"

[[sources]]
org = "testorg"

[groups.public]
visibility = "public"

[groups.private]
visibility = "private"

[claude.settings]
permissions = "bypass"

[env.vars]
EXTRA_VAR = "world"

[files]
"dist/editorconfig" = ".editorconfig"

[instance.files]
"dist/editorconfig" = ".editorconfig"

[content.workspace]
source = "workspace.md"

[content.worktree]
source = "worktree.md"

[content.groups.public]
source = "public.md"

[content.repos.app]
source = "repos/app.md"

  [content.repos.app.subdirs]
  docs = "repos/app-docs.md"
  "docs/deep" = "repos/app-docs-deep.md"
`
	charWriteFile(t, filepath.Join(niwaDir, "workspace.toml"), configTOML, 0o644)

	// The overlay: an overlay= entry on a base-config repo is what makes
	// MergeWorkspaceOverlay set OverlaySource, which is the only way to reach
	// the overlay-append branch of InstallRepoContentTo.
	overlaySrc := filepath.Join(tmpDir, "overlay-src")
	charMkdirAll(t, overlaySrc)
	charWriteFile(t, filepath.Join(overlaySrc, "workspace-overlay.toml"), `
[claude.content.repos.app]
overlay = "app-extra.md"
`, 0o644)
	charWriteFile(t, filepath.Join(overlaySrc, "app-extra.md"),
		"## Overlay addendum for app\n", 0o644)
	charWriteFile(t, filepath.Join(overlaySrc, "CLAUDE.overlay.md"),
		"# Overlay workspace context\n", 0o644)

	// Global (personal) config layer.
	globalDir := filepath.Join(tmpDir, "global")
	charMkdirAll(t, globalDir)
	charWriteFile(t, filepath.Join(globalDir, "CLAUDE.global.md"),
		"# Global personal context\n", 0o644)

	// Pre-create the repo checkouts (and the declared subdirs) so the cloner
	// skips them and the subdir installers have somewhere to write.
	for _, r := range []struct{ group, name string }{
		{"public", "app"},
		{"private", "secrets"},
	} {
		repoDir := filepath.Join(instanceRoot, r.group, r.name)
		charMkdirAll(t, filepath.Join(repoDir, ".git"))
		charWriteFile(t, filepath.Join(repoDir, ".gitignore"), "*.local*\n", 0o644)
	}
	charMkdirAll(t, filepath.Join(instanceRoot, "public", "app", "docs", "deep"))

	// Register the overlay in the workspace-root init state. Create reads this
	// (LoadState(workspaceRoot)) and threads OverlayURL into the pipeline, so
	// the overlay branch runs on a plain Create.
	if err := SaveState(tmpDir, &InstanceState{
		SchemaVersion: SchemaVersion,
		OverlayURL:    characterizationOverlayURL,
	}); err != nil {
		t.Fatalf("seeding workspace-root init state: %v", err)
	}

	fx := &characterizationFixture{
		tmpDir:       tmpDir,
		niwaDir:      niwaDir,
		instanceRoot: instanceRoot,
		overlaySrc:   overlaySrc,
		globalDir:    globalDir,
		niwaExe:      niwaExe,
	}

	overlayDir, err := config.OverlayDir(characterizationOverlayURL)
	if err != nil {
		t.Fatalf("resolving overlay dir: %v", err)
	}

	// Longest-prefix-first: the overlay clone and the config dir both sit under
	// paths that would otherwise be swallowed by a shorter replacement.
	fx.placeholders = []pathPlaceholder{
		{abs: overlayDir, token: "{OVERLAY}"},
		{abs: globalDir, token: "{GLOBAL}"},
		{abs: niwaDir, token: "{CONFIG}"},
		{abs: instanceRoot, token: "{INSTANCE}"},
		{abs: niwaExe, token: "{NIWA}"},
		{abs: tmpDir, token: "{TMP}"},
	}
	sort.SliceStable(fx.placeholders, func(i, j int) bool {
		return len(fx.placeholders[i].abs) > len(fx.placeholders[j].abs)
	})

	return fx
}

// assertInstanceBoundaryRules checks that the fixture actually reached the
// three rules the manifest exists to protect. Without these the golden could
// pin a workspace where none of them ran, and the later conversion could drop
// all three while the hashes stayed put.
func (fx *characterizationFixture) assertInstanceBoundaryRules(t *testing.T) {
	t.Helper()

	// @import migration: the workspace content source shipped all three legacy
	// relative imports; the context installers must have removed them from the
	// CLAUDE.md they were written into.
	claudeMD := fx.readInstanceFile(t, "CLAUDE.md")
	for _, legacy := range []string{workspaceContextImport, overlayClaudeImport, globalClaudeImport} {
		if strings.Contains(claudeMD, legacy+"\n") {
			t.Errorf("CLAUDE.md still carries the legacy import %q; the migration removal did not run", legacy)
		}
	}
	// ...and replaced them with absolute imports in the rules file.
	rules := fx.readInstanceFile(t, workspaceRulesFile)
	for _, want := range []string{workspaceContextFile, overlayClaudeFile, globalClaudeFile} {
		if !strings.Contains(rules, "@"+filepath.Join(fx.instanceRoot, want)) {
			t.Errorf("%s is missing the absolute import for %s", workspaceRulesFile, want)
		}
	}

	// {workspace} expansion: the source used the variable, so the output must
	// carry the expanded root and not the literal.
	if strings.Contains(claudeMD, "{workspace}") {
		t.Error("CLAUDE.md still contains the literal {workspace}; template expansion did not run")
	}
	if !strings.Contains(claudeMD, fx.instanceRoot) {
		t.Error("CLAUDE.md does not contain the expanded instance root; the fixture does not exercise {workspace}")
	}

	// Overlay append: base content first, overlay bytes after.
	appLocal := fx.readInstanceFile(t, filepath.Join("public", "app", "CLAUDE.local.md"))
	base := strings.Index(appLocal, "# app")
	over := strings.Index(appLocal, "## Overlay addendum for app")
	if base < 0 || over < 0 {
		t.Errorf("public/app/CLAUDE.local.md is missing base or overlay content:\n%s", appLocal)
	} else if over < base {
		t.Errorf("overlay content was prepended rather than appended:\n%s", appLocal)
	}

	// Subdir content, including a subdir nested inside another subdir.
	fx.readInstanceFile(t, filepath.Join("public", "app", "docs", "CLAUDE.local.md"))
	fx.readInstanceFile(t, filepath.Join("public", "app", "docs", "deep", "CLAUDE.local.md"))

	// Auto-discovery: "secrets" has no [content.repos] entry and is picked up
	// from repos/secrets.md.
	if got := fx.readInstanceFile(t, filepath.Join("private", "secrets", "CLAUDE.local.md")); !strings.Contains(got, "Auto-discovered secrets") {
		t.Errorf("private/secrets/CLAUDE.local.md is not the auto-discovered source:\n%s", got)
	}

	// The worktree-delegation hook is the one place os.Executable() reaches
	// generated content, so confirm normalization has something to normalize.
	settings := fx.readInstanceFile(t, filepath.Join("public", "app", ".claude", "settings.local.json"))
	if !strings.Contains(settings, filepath.ToSlash(fx.niwaExe)) {
		t.Error("settings.local.json carries no niwa binary path; the executable normalization is untested")
	}
}

// assertWorktreeBoundaryRules is the worktree half: the same overlay-append and
// subdir rules, plus the delimited context section the worktree layer adds.
func (fx *characterizationFixture) assertWorktreeBoundaryRules(t *testing.T, worktreePath string) {
	t.Helper()

	local := charReadFile(t, filepath.Join(worktreePath, "CLAUDE.local.md"))
	for _, want := range []string{"# app", "## Overlay addendum for app", worktreeContextHeading, "ship the contract"} {
		if !strings.Contains(local, want) {
			t.Errorf("worktree CLAUDE.local.md is missing %q:\n%s", want, local)
		}
	}
	charReadFile(t, filepath.Join(worktreePath, "docs", "CLAUDE.local.md"))
	charReadFile(t, filepath.Join(worktreePath, "docs", "deep", "CLAUDE.local.md"))
}

// readInstanceFile reads one path under the instance root, failing the test if
// it is absent — an absent file here means the fixture stopped exercising a rule.
func (fx *characterizationFixture) readInstanceFile(t *testing.T, rel string) string {
	t.Helper()
	return charReadFile(t, filepath.Join(fx.instanceRoot, rel))
}

func charReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// countOccurrences reports how many times want appears in list.
func countOccurrences(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// manifest renders one sorted "path<TAB>hash" line per file. Paths are made
// relative to the instance root and hashes are taken over normalized bytes, so
// nothing in the manifest depends on where the fixture happened to land.
func (fx *characterizationFixture) manifest(t *testing.T, paths []string) string {
	t.Helper()

	seen := make(map[string]bool, len(paths))
	lines := make([]string, 0, len(paths))
	for _, p := range paths {
		rel := fx.normalizePath(t, p)
		// A repeated path is a property of the caller's list, asserted where
		// that list is produced; the manifest itself is a set.
		if seen[rel] {
			continue
		}
		seen[rel] = true

		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading written file %s: %v", p, err)
		}
		sum := sha256.Sum256([]byte(fx.normalizeContent(string(data))))
		lines = append(lines, rel+"\t"+hex.EncodeToString(sum[:]))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// normalizePath expresses an absolute written path relative to the instance
// root, falling back to a placeholder-substituted absolute path for anything
// written outside it.
func (fx *characterizationFixture) normalizePath(t *testing.T, p string) string {
	t.Helper()
	if rel, err := filepath.Rel(fx.instanceRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(fx.normalizeContent(p))
}

// normalizeContent replaces every fixture-specific absolute path with a stable
// token. Both the raw and the slash-normalized form are substituted, because
// the hook-command builder writes the executable path through filepath.ToSlash.
func (fx *characterizationFixture) normalizeContent(s string) string {
	for _, ph := range fx.placeholders {
		if ph.abs == "" {
			continue
		}
		s = strings.ReplaceAll(s, ph.abs, ph.token)
		if slashed := filepath.ToSlash(ph.abs); slashed != ph.abs {
			s = strings.ReplaceAll(s, slashed, ph.token)
		}
	}
	return s
}

// compare checks the rendered manifest against the checked-in expectation, or
// rewrites it when the update environment variable is set.
func (fx *characterizationFixture) compare(t *testing.T, name, got string) {
	t.Helper()

	goldenPath := filepath.Join("testdata", "characterization", name)

	if os.Getenv(characterizationGoldenEnv) != "" {
		charMkdirAll(t, filepath.Dir(goldenPath))
		charWriteFile(t, goldenPath, got, 0o644)
		t.Logf("rewrote %s (%d entries)", goldenPath, strings.Count(got, "\n"))
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading %s: %v\n\nset %s=1 to create it", goldenPath, err, characterizationGoldenEnv)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	t.Errorf("managed-file manifest changed\n%s", characterizationDiff(want, got))

	// A hash mismatch is unreadable on its own, so print the live bytes of each
	// file whose hash moved. Storing content in the golden would buy the same
	// diagnosis at the cost of a much larger fixture.
	wantHashes := parseCharacterizationManifest(want)
	gotHashes := parseCharacterizationManifest(got)
	for rel, gotHash := range gotHashes {
		wantHash, ok := wantHashes[rel]
		if !ok || wantHash == gotHash {
			continue
		}
		abs := filepath.Join(fx.instanceRoot, filepath.FromSlash(rel))
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		t.Logf("--- current contents of %s (normalized) ---\n%s", rel, fx.normalizeContent(string(data)))
	}
}

// parseCharacterizationManifest turns a manifest back into a path->hash map.
func parseCharacterizationManifest(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		rel, hash, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		out[rel] = hash
	}
	return out
}

// characterizationDiff renders a line-level added/removed/changed summary.
func characterizationDiff(want, got string) string {
	wantHashes := parseCharacterizationManifest(want)
	gotHashes := parseCharacterizationManifest(got)

	keys := map[string]bool{}
	for k := range wantHashes {
		keys[k] = true
	}
	for k := range gotHashes {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	var b strings.Builder
	for _, k := range sorted {
		w, inWant := wantHashes[k]
		g, inGot := gotHashes[k]
		switch {
		case !inWant:
			fmt.Fprintf(&b, "+ %s (new file)\n", k)
		case !inGot:
			fmt.Fprintf(&b, "- %s (no longer written)\n", k)
		case w != g:
			fmt.Fprintf(&b, "~ %s (contents changed)\n", k)
		}
	}
	if b.Len() == 0 {
		return "(no path-level difference; check for trailing whitespace)\n"
	}
	return b.String()
}

// copyTreeForCharacterization copies src into dst, creating dst. It stands in
// for the overlay clone so the fixture never reaches the network.
func copyTreeForCharacterization(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func charMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func charWriteFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
