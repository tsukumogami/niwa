package functional

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// This file carries the steps behind the Codex acceptance scenarios in
// features/codex-agent.feature, which take the capability contract's
// requirements end to end through the compiled binary.
//
// Three conventions run through all of them:
//
//   - What a Codex session "sees" is decided offline, against the single
//     context file Codex's first-match rule selects for a directory. The
//     selection is reimplemented here (codexContextChain) rather than measured
//     from a live session, so every criterion stays decidable with no network
//     and no model. The rules it mirrors are the standing spike's measured
//     findings: a bounded walk from the nearest project-root marker down to the
//     working directory, one file per directory by strict precedence.
//   - Everything niwa writes for Codex lands inside a repository, never at the
//     instance root: Codex reads a project layer from the nearest project root
//     downward, and an instance root is not one. So the payload steps below are
//     keyed by a working-tree location rather than by an instance.
//   - Checks that genuinely need a live session gate on `codex` being on PATH
//     and on a login the session can use, and skip rather than fail when either
//     is absent, following the repo's existing pattern for the `claude`-gated
//     scenarios. See codexIsAvailable for why the login has to be seeded.

const (
	// codexPayloadDir is the payload directory niwa owns inside every
	// repository working tree it prepares.
	codexPayloadDir = ".codex"
	// codexOverrideName is the composed per-repository context file. It is
	// first in Codex's per-directory precedence.
	codexOverrideName = "AGENTS.override.md"
	// codexNativeName is the context file a repository commits itself.
	codexNativeName = "AGENTS.md"
	// codexDefaultBudget is Codex's documented default project_doc_max_bytes.
	// A chain larger than this is exactly the case the generated project layer
	// raises the bound for, so the oversized-context scenario asserts both
	// halves against it: that the fixture really overflows the default, and that
	// what niwa declared covers the chain it composed.
	codexDefaultBudget = 32768
	// codexCredentialName is the developer's Codex login state. niwa must never
	// read or write it.
	codexCredentialName = "auth.json"
	// codexGuidePath is the committed user guide whose gap-list section is
	// generated from the declaration table.
	codexGuidePath = "../../docs/guides/codex-agent.md"
	// codexLiveTimeout bounds a gated live Codex invocation.
	codexLiveTimeout = 3 * time.Minute
	// codexInteractiveDwell is how long the gated interactive scenario lets the
	// TUI run before killing it. A trust or approval prompt appears immediately
	// on startup, so reaching the end of the dwell with none is the signal.
	codexInteractiveDwell = 15 * time.Second
)

// codexContextCandidates is Codex's hardcoded per-directory precedence. The
// first name present in a directory claims that directory's single context
// slot and suppresses the rest -- including an empty file, which is why niwa
// writing a whitespace-only override would silently displace a repository's
// own committed context.
var codexContextCandidates = []string{codexOverrideName, codexNativeName}

func registerCodexAgentSteps(ctx *godog.ScenarioContext) {
	// Fixture staging.
	ctx.Step(`^a staged file "([^"]*)" with body:$`, aStagedFileWithBody)
	ctx.Step(`^a staged file "([^"]*)" of (\d+) bytes ending with "([^"]*)"$`, aStagedFileOfSizeEndingWith)
	ctx.Step(`^a staged symlink "([^"]*)" pointing at "([^"]*)"$`, aStagedSymlinkPointingAt)
	ctx.Step(`^a staged directory "([^"]*)"$`, aStagedDirectory)
	ctx.Step(`^a source repo "([^"]*)" exists with the staged files$`, aSourceRepoExistsWithStagedFiles)
	ctx.Step(`^a config repo "([^"]*)" exists with the staged files and body:$`, aConfigRepoExistsWithStagedFilesAndBody)
	ctx.Step(`^the config repo "([^"]*)" is re-pushed with the staged files and body:$`, theConfigRepoIsRePushedWithStagedFiles)
	ctx.Step(`^the registry entry "([^"]*)" is re-pointed through a symlink$`, theRegistryEntryIsRepointedThroughASymlink)

	// Codex context discovery (the offline proxy for what a session sees).
	ctx.Step(`^the Codex context at "([^"]*)" selects "([^"]*)"$`, theCodexContextAtSelects)
	ctx.Step(`^the Codex context at "([^"]*)" contains "([^"]*)"$`, theCodexContextAtContains)
	ctx.Step(`^the Codex context at "([^"]*)" does not contain "([^"]*)"$`, theCodexContextAtDoesNotContain)
	ctx.Step(`^the Codex context at "([^"]*)" exceeds the default Codex budget$`, theCodexContextAtExceedsDefaultBudget)

	// The payload, the skills, and what neither may carry.
	ctx.Step(`^the Codex payload at "([^"]*)" is niwa's own$`, theCodexPayloadAtIsNiwasOwn)
	ctx.Step(`^the Codex payload at "([^"]*)" declares MCP server "([^"]*)"$`, theCodexPayloadAtDeclaresMCPServer)
	ctx.Step(`^the Codex payload at "([^"]*)" declares no credentials$`, theCodexPayloadAtDeclaresNoCredentials)
	ctx.Step(`^the Codex payload at "([^"]*)" declares a budget covering the composed chain$`, theCodexPayloadAtDeclaresACoveringBudget)
	ctx.Step(`^"([^"]*)" holds exactly (\d+) Codex config files?$`, theLocationHoldsNCodexConfigFiles)
	ctx.Step(`^"([^"]*)" holds exactly (\d+) Codex skills trees?$`, theLocationHoldsNCodexSkillsTrees)
	ctx.Step(`^the Codex skills tree "([^"]*)" at "([^"]*)" mirrors "([^"]*)"$`, theCodexSkillsTreeMirrors)
	ctx.Step(`^instance "([^"]*)" declares no Codex hooks$`, theInstanceDeclaresNoCodexHooks)

	// The developer's own Codex setup.
	ctx.Step(`^the developer Codex config exists with body:$`, theDeveloperCodexConfigExistsWithBody)
	ctx.Step(`^the developer Codex home has a credential file$`, theDeveloperCodexHomeHasACredentialFile)
	ctx.Step(`^the developer Codex credential file is unreadable$`, theDeveloperCodexCredentialFileIsUnreadable)
	ctx.Step(`^the developer Codex credential file is unchanged$`, theDeveloperCodexCredentialFileIsUnchanged)
	ctx.Step(`^the developer Codex config has exactly (\d+) project entr(?:y|ies)$`, theDeveloperCodexConfigHasNProjectEntries)
	ctx.Step(`^the developer Codex config trusts repo "([^"]*)" in instance "([^"]*)"$`, theDeveloperCodexConfigTrustsRepo)
	ctx.Step(`^the developer Codex config does not trust repo "([^"]*)" in instance "([^"]*)"$`, theDeveloperCodexConfigDoesNotTrustRepo)
	ctx.Step(`^the developer Codex config does not trust the last worktree$`, theDeveloperCodexConfigDoesNotTrustLastWorktree)
	ctx.Step(`^the developer Codex config grew only by project entries inside the workspace root$`, theDeveloperCodexConfigGrewOnlyByProjectEntries)

	// Working-tree assertions.
	ctx.Step(`^the file "([^"]*)" exists at "([^"]*)"$`, theFileExistsAtLocation)
	ctx.Step(`^the file "([^"]*)" does not exist at "([^"]*)"$`, theFileDoesNotExistAtLocation)
	ctx.Step(`^the file "([^"]*)" at "([^"]*)" matches git HEAD$`, theFileAtLocationMatchesGitHEAD)
	ctx.Step(`^the git status of every repo in instance "([^"]*)" is clean$`, theGitStatusOfEveryRepoIsClean)
	ctx.Step(`^the git status of the last worktree is clean$`, theGitStatusOfLastWorktreeIsClean)
	ctx.Step(`^the git exclude of repo "([^"]*)" in instance "([^"]*)" carries the Codex patterns$`, theGitExcludeCarriesCodexPatterns)
	ctx.Step(`^the Codex payload directory of repo "([^"]*)" in instance "([^"]*)" cannot be written$`, theCodexPayloadDirCannotBeWritten)

	// The declaration table, asserted directly.
	ctx.Step(`^the capability "([^"]*)" is declared unavailable for Codex$`, theCapabilityIsDeclaredUnavailableForCodex)
	ctx.Step(`^the committed Codex gap list carries the declared reason for "([^"]*)"$`, theGapListCarriesTheDeclaredReason)
	ctx.Step(`^the refusal carries the declared reason for "([^"]*)"$`, theRefusalCarriesTheDeclaredReason)

	// Live, codex-gated.
	ctx.Step(`^codex is available$`, codexIsAvailable)
	ctx.Step(`^the Codex sandbox can run here$`, theCodexSandboxCanRunHere)
	ctx.Step(`^I run codex exec from "([^"]*)" with prompt:$`, iRunCodexExecFrom)
	ctx.Step(`^I start an interactive codex session at "([^"]*)" under a pty$`, iStartInteractiveCodexAt)
	ctx.Step(`^the codex session output shows no trust or approval prompt$`, theCodexSessionShowsNoTrustPrompt)
	ctx.Step(`^the codex session reached its ready state$`, theCodexSessionReachedReadyState)
}

// --- fixture staging ---------------------------------------------------

// aStagedFileWithBody stages one committed file for the next fixture-repo
// step. Staging exists because a fixture repository here ships several files
// whose exact bytes matter (sentinels, oversized content, a context file in an
// intermediate directory), and a Gherkin step carries at most one docstring.
func aStagedFileWithBody(ctx context.Context, path string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.stagedFiles[path] = body.Content
	return ctx, nil
}

// aStagedFileOfSizeEndingWith stages a file of at least size bytes whose final
// line is the given sentinel. It is how the byte-budget scenario builds context
// larger than Codex's 32768-byte default without pasting 32KB into a feature
// file, and the trailing sentinel is what makes truncation observable: a chain
// cut short loses the end, not the beginning.
func aStagedFileOfSizeEndingWith(ctx context.Context, path string, size int, sentinel string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	var b strings.Builder
	const filler = "Filler line that pads this layer past the default budget.\n"
	for b.Len()+len(filler) < size {
		b.WriteString(filler)
	}
	b.WriteString(sentinel + "\n")
	s.stagedFiles[path] = b.String()
	return ctx, nil
}

// aStagedSymlinkPointingAt stages a committed symlink. {home} in the target
// expands to the sandbox home, so a fixture can point a repository's own
// context file at the developer's Codex credentials -- the shape the composer's
// O_NOFOLLOW read exists to refuse.
func aStagedSymlinkPointingAt(ctx context.Context, path, target string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.stagedSymlinks[path] = strings.ReplaceAll(target, "{home}", s.homeDir)
	return ctx, nil
}

// aStagedDirectory stages a directory by staging a placeholder file inside it.
// Git tracks no empty directories, so a fixture that needs a deep working
// directory has to commit something in it.
func aStagedDirectory(ctx context.Context, path string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.stagedFiles[strings.TrimSuffix(path, "/")+"/.gitkeep"] = ""
	return ctx, nil
}

// drainStaged returns the staged files and symlinks and clears them, so one
// scenario can build several fixture repositories in sequence.
func (s *testState) drainStaged() (map[string]string, map[string]string) {
	files, symlinks := s.stagedFiles, s.stagedSymlinks
	s.stagedFiles = make(map[string]string)
	s.stagedSymlinks = make(map[string]string)
	return files, symlinks
}

func aSourceRepoExistsWithStagedFiles(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	files, symlinks := s.drainStaged()
	if len(files) == 0 && len(symlinks) == 0 {
		return ctx, fmt.Errorf("source repo %q was asked for staged files but none were staged", name)
	}
	url, err := s.gitServer.SourceRepoSpec(name, files, symlinks)
	if err != nil {
		return ctx, fmt.Errorf("creating source repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

func aConfigRepoExistsWithStagedFilesAndBody(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	files, _ := s.drainStaged()
	files[".niwa/workspace.toml"] = s.interpolateRepoURLs(body.Content)
	url, err := s.gitServer.SourceRepoSpec(name, files, nil)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

// theConfigRepoIsRePushedWithStagedFiles replaces the config repo's committed
// tree with the staged files plus a new workspace.toml and pushes it. It is how
// the refresh scenario changes the workspace's configured content between two
// applies.
func theConfigRepoIsRePushedWithStagedFiles(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	files, _ := s.drainStaged()
	files[".niwa/workspace.toml"] = s.interpolateRepoURLs(body.Content)

	work, err := os.MkdirTemp(s.tmpDir, "repush-*")
	if err != nil {
		return ctx, fmt.Errorf("creating re-push work dir: %w", err)
	}
	defer os.RemoveAll(work)

	if out, err := runGitInDir(s.tmpDir, "clone", url, work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", url, err, out)
	}
	entries, err := os.ReadDir(work)
	if err != nil {
		return ctx, fmt.Errorf("reading %s: %w", work, err)
	}
	for _, e := range entries {
		if e.Name() == ".git" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(work, e.Name())); err != nil {
			return ctx, fmt.Errorf("clearing %s: %w", e.Name(), err)
		}
	}
	for rel, content := range files {
		dst := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return ctx, fmt.Errorf("creating parent dir for %s: %w", rel, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			return ctx, fmt.Errorf("writing %s: %w", rel, err)
		}
	}
	for _, args := range [][]string{
		{"-c", "user.email=t@example.com", "-c", "user.name=niwa-test", "add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=niwa-test", "commit", "-m", "update content"},
		{"push", "origin", "HEAD"},
	} {
		if out, err := runGitInDir(work, args...); err != nil {
			return ctx, fmt.Errorf("git %v: %w\n%s", args, err, out)
		}
	}
	return ctx, nil
}

func (s *testState) interpolateRepoURLs(body string) string {
	for repoName, repoURL := range s.repoURLs {
		body = strings.ReplaceAll(body, "{repo:"+repoName+"}", repoURL)
	}
	return body
}

// theRegistryEntryIsRepointedThroughASymlink rewrites the registry so the
// workspace root niwa resolves from it reaches the same directory through a
// symlink, and points the scenario's own paths at that alias too. It runs after
// `niwa init`, because init records the path its own working directory resolves
// to and a symlinked cwd would be canonicalized away before niwa ever saw it.
//
// Every path niwa derives from here on -- the instance root, each repository
// root, each trust key -- carries the symlinked component. The keys it writes
// must still name the resolved path: Codex compares a session's directory
// against the key after resolving it, so an unresolved key is a
// present-but-well-formed entry that trusts nothing.
func theRegistryEntryIsRepointedThroughASymlink(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	alias := s.workspaceRoot + "-via-symlink"
	if err := os.RemoveAll(alias); err != nil {
		return ctx, fmt.Errorf("clearing %s: %w", alias, err)
	}
	if err := os.Symlink(s.workspaceRoot, alias); err != nil {
		return ctx, fmt.Errorf("linking %s -> %s: %w", alias, s.workspaceRoot, err)
	}

	registry := filepath.Join(s.homeDir, ".config", "niwa", "config.toml")
	data, err := os.ReadFile(registry)
	if err != nil {
		return ctx, fmt.Errorf("reading the registry: %w", err)
	}
	if !strings.Contains(string(data), "registry."+name) {
		return ctx, fmt.Errorf("the registry carries no entry for workspace %q:\n%s", name, data)
	}
	if !strings.Contains(string(data), s.workspaceRoot) {
		return ctx, fmt.Errorf("the registry names no path under %s:\n%s", s.workspaceRoot, data)
	}
	repointed := strings.ReplaceAll(string(data), s.workspaceRoot, alias)
	if err := os.WriteFile(registry, []byte(repointed), 0o644); err != nil {
		return ctx, fmt.Errorf("writing the registry: %w", err)
	}

	s.workspaceRootAlias = alias
	s.workspaceRoot = alias
	return ctx, nil
}

// --- Codex context discovery ------------------------------------------

// resolveLocation turns a feature-file location into an absolute directory.
// A location is workspace-root-relative ("ws/tools/app/docs"), or begins with
// {worktree} to address the worktree the last create step produced.
func (s *testState) resolveLocation(loc string) (string, error) {
	if loc == "{worktree}" || strings.HasPrefix(loc, "{worktree}/") {
		if s.lastSessionWorktreePath == "" {
			return "", fmt.Errorf("location %q needs a worktree; create one first", loc)
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(loc, "{worktree}"), "/")
		return filepath.Join(s.lastSessionWorktreePath, filepath.FromSlash(rest)), nil
	}
	return filepath.Join(s.workspaceRoot, filepath.FromSlash(loc)), nil
}

// codexProjectRoot walks up from dir to the nearest ancestor holding a `.git`
// entry, which is Codex's default project-root marker, and returns dir itself
// when there is none. The marker check is a bare metadata stat, so a linked
// worktree's `.git` file satisfies it exactly as a clone's directory does.
func codexProjectRoot(dir string) string {
	for cur := dir; ; {
		if _, err := os.Lstat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
		}
		cur = parent
	}
}

// codexContextChain returns the context files a Codex session started in dir
// reads, project root first, one per directory by strict first-match.
//
// This is the offline proxy the acceptance criteria are decided against. Its
// discriminating power is the selection itself: a test that only asserted
// "some file contains the sentinel" would pass while a lower-precedence
// candidate shadowed the composed one, which is the silent failure the whole
// override design exists to prevent.
func codexContextChain(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("context location %s: %w", abs, err)
	}
	root := codexProjectRoot(abs)

	var dirs []string
	for cur := abs; ; cur = filepath.Dir(cur) {
		dirs = append(dirs, cur)
		if cur == root {
			break
		}
		if parent := filepath.Dir(cur); parent == cur {
			break
		}
	}
	// Collected innermost-first; Codex reads root-first.
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var chain []string
	for _, d := range dirs {
		for _, name := range codexContextCandidates {
			candidate := filepath.Join(d, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				chain = append(chain, candidate)
				break
			}
		}
	}
	return chain, nil
}

// codexContextChainAt resolves a feature-file location and returns its chain
// alongside the project root the chain is reported relative to.
func codexContextChainAt(s *testState, loc string) ([]string, string, error) {
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return nil, "", err
	}
	chain, err := codexContextChain(dir)
	if err != nil {
		return nil, "", err
	}
	return chain, codexProjectRoot(dir), nil
}

// codexContextBytes concatenates a chain in the order Codex reads it.
func codexContextBytes(chain []string) ([]byte, error) {
	var buf bytes.Buffer
	for _, path := range chain {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func theCodexContextAtSelects(ctx context.Context, loc, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	chain, root, err := codexContextChainAt(s, loc)
	if err != nil {
		return err
	}
	got := make([]string, 0, len(chain))
	for _, path := range chain {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
	}
	expected := strings.Split(want, ",")
	for i := range expected {
		expected[i] = strings.TrimSpace(expected[i])
	}
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		return fmt.Errorf("Codex context at %s selects %v, want %v (project root %s)", loc, got, expected, root)
	}
	return nil
}

func theCodexContextAtContains(ctx context.Context, loc, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	chain, _, err := codexContextChainAt(s, loc)
	if err != nil {
		return err
	}
	data, err := codexContextBytes(chain)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("Codex context at %s does not carry %q; chain %v:\n%s", loc, want, chain, data)
	}
	return nil
}

func theCodexContextAtDoesNotContain(ctx context.Context, loc, unwanted string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	chain, _, err := codexContextChainAt(s, loc)
	if err != nil {
		return err
	}
	data, err := codexContextBytes(chain)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), unwanted) {
		return fmt.Errorf("Codex context at %s still carries %q; chain %v:\n%s", loc, unwanted, chain, data)
	}
	return nil
}

// theCodexContextAtExceedsDefaultBudget proves the oversized fixture really is
// oversized: a session standing here reads a chain larger than Codex's
// 32768-byte default, which is the case the apply-time report exists for.
func theCodexContextAtExceedsDefaultBudget(ctx context.Context, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	total, err := codexChainBytesAt(s, loc)
	if err != nil {
		return err
	}
	if total <= codexDefaultBudget {
		return fmt.Errorf("Codex context at %s is %d bytes, which the %d-byte default already covers; the fixture is not exercising the budget", loc, total, codexDefaultBudget)
	}
	return nil
}

// codexChainBytesAt is what a session standing at loc spends of its byte
// budget: the size of every file in the chain its discovery selects, summed in
// the order it reads them.
func codexChainBytesAt(s *testState, loc string) (int, error) {
	chain, _, err := codexContextChainAt(s, loc)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, path := range chain {
		info, err := os.Stat(path)
		if err != nil {
			return 0, fmt.Errorf("stating %s: %w", path, err)
		}
		total += int(info.Size())
	}
	return total, nil
}

// --- payload, skills ---------------------------------------------------

// codexPayloadPath is the generated configuration a session standing in dir
// loads. It is a real file in the repository rather than a link to an
// instance-level one: Codex reads a project layer from the nearest project root
// downward, so the configuration has to live inside the repository itself.
func codexPayloadPath(dir string) string {
	return filepath.Join(dir, codexPayloadDir, "config.toml")
}

// theCodexPayloadAtIsNiwasOwn asserts a session standing at loc loads a payload
// niwa generated: present, a regular file, parseable, and carrying the marker
// that distinguishes it from a configuration the repository committed itself.
// The marker is the whole point -- a scenario that only checked for a file
// would pass against a repository's own committed `.codex/config.toml`.
func theCodexPayloadAtIsNiwasOwn(ctx context.Context, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	path := codexPayloadPath(dir)
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("no Codex payload a session at %s would load: %w", loc, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.Contains(string(data), "niwa-generated-config") {
		return fmt.Errorf("%s carries no niwa generation marker, so it is not niwa's own:\n%s", path, data)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s does not parse as TOML, so a session would fail its whole config load: %w\n%s", path, err, data)
	}
	return nil
}

// theCodexPayloadAtDeclaresMCPServer reads the generated payload the way Codex
// does -- by parsing it and looking in the table Codex reads servers from --
// rather than grepping, so a well-formed file that puts the server somewhere
// Codex never looks fails here.
func theCodexPayloadAtDeclaresMCPServer(ctx context.Context, loc, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	path := codexPayloadPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w\n%s", path, err, data)
	}
	servers, ok := doc["mcp_servers"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s declares no [mcp_servers] table:\n%s", path, data)
	}
	if _, ok := servers[name]; !ok {
		return fmt.Errorf("%s declares no MCP server %q:\n%s", path, name, data)
	}
	return nil
}

// theCodexPayloadAtDeclaresNoCredentials asserts the payload carries no API key
// and no auth-related key. niwa binds no key for Codex deliberately: with a
// broken login an exported key becomes a silent metered fallback.
func theCodexPayloadAtDeclaresNoCredentials(ctx context.Context, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	payload := filepath.Join(dir, codexPayloadDir)
	entries, err := os.ReadDir(payload)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", payload, err)
	}
	forbidden := []string{"OPENAI_API_KEY", "forced_login_method", "api_key", "auth.json"}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		path := filepath.Join(payload, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		for _, key := range forbidden {
			if strings.Contains(string(data), key) {
				return fmt.Errorf("%s mentions %q; niwa binds no Codex credentials", path, key)
			}
		}
	}
	return nil
}

// theCodexPayloadAtDeclaresACoveringBudget asserts that the bound a session at
// loc loads is large enough for the chain that session reads.
//
// Both halves are measured off disk rather than restated from niwa's own
// arithmetic: the chain is what Codex's discovery selects in that tree, and the
// budget is what the generated configuration actually declares. An exact fit
// passes an "it all fits" check today and truncates the first time any file in
// the chain grows, so the bar is double the chain -- and truncation is a raw cut
// with nothing on stderr, so nothing downstream would notice.
func theCodexPayloadAtDeclaresACoveringBudget(ctx context.Context, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	chain, err := codexChainBytesAt(s, loc)
	if err != nil {
		return err
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	path := codexPayloadPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no Codex payload a session at %s would load: %w", loc, err)
	}
	var doc struct {
		Budget int64 `toml:"project_doc_max_bytes"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("%s does not parse as TOML: %w\n%s", path, err, data)
	}
	if doc.Budget == 0 {
		return fmt.Errorf("%s declares no project_doc_max_bytes, so the %d-byte chain a session at %s reads is cut at the %d-byte default with nothing said:\n%s", path, chain, loc, codexDefaultBudget, data)
	}
	if doc.Budget < int64(chain) {
		return fmt.Errorf("%s declares project_doc_max_bytes = %d for a %d-byte chain; the innermost layer is cut without a word", path, doc.Budget, chain)
	}
	if doc.Budget < int64(2*chain) {
		return fmt.Errorf("%s declares project_doc_max_bytes = %d, under double the %d-byte chain; a bound that fits exactly starts truncating the moment any file in the chain grows", path, doc.Budget, chain)
	}
	if doc.Budget < codexDefaultBudget {
		return fmt.Errorf("%s declares project_doc_max_bytes = %d, under Codex's own %d-byte default; niwa raises a budget, it does not lower one", path, doc.Budget, codexDefaultBudget)
	}
	return nil
}

// theLocationHoldsNCodexConfigFiles counts the .toml files in the payload
// directory at loc. Re-applying that accumulated files rather than reconciling
// them shows up here and nowhere else.
func theLocationHoldsNCodexConfigFiles(ctx context.Context, loc string, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	payload := filepath.Join(dir, codexPayloadDir)
	entries, err := os.ReadDir(payload)
	if err != nil {
		if os.IsNotExist(err) && want == 0 {
			return nil
		}
		return fmt.Errorf("reading %s: %w", payload, err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".toml") {
			count++
		}
	}
	if count != want {
		return fmt.Errorf("%s holds %d .toml files, want %d", payload, count, want)
	}
	return nil
}

func theLocationHoldsNCodexSkillsTrees(ctx context.Context, loc string, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	skillsDir := filepath.Join(dir, codexPayloadDir, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) && want == 0 {
			return nil
		}
		return fmt.Errorf("reading %s: %w", skillsDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != want {
		return fmt.Errorf("%s holds %d skills entries %v, want %d", skillsDir, len(names), names, want)
	}
	return nil
}

// theCodexSkillsTreeMirrors asserts the delivered plugin root is the source
// tree: same file set, byte-identical content, nothing added or omitted.
func theCodexSkillsTreeMirrors(ctx context.Context, plugin, loc, sourceLoc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	tree := filepath.Join(dir, codexPayloadDir, "skills", plugin)
	// Resolving is half the assertion: a dangling link fails here, and the walk
	// below needs a real directory to descend into.
	delivered, err := filepath.EvalSymlinks(tree)
	if err != nil {
		return fmt.Errorf("resolving the skills entry for plugin %q: %w", plugin, err)
	}
	source, err := s.resolveLocation(sourceLoc)
	if err != nil {
		return err
	}
	deliveredFiles, err := readTreeFiles(delivered)
	if err != nil {
		return fmt.Errorf("reading the delivered skills tree: %w", err)
	}
	sourceFiles, err := readTreeFiles(source)
	if err != nil {
		return fmt.Errorf("reading the source plugin tree: %w", err)
	}
	for rel, want := range sourceFiles {
		got, ok := deliveredFiles[rel]
		if !ok {
			return fmt.Errorf("plugin %q: %s is missing from the delivered tree", plugin, rel)
		}
		if got != want {
			return fmt.Errorf("plugin %q: %s differs from its source", plugin, rel)
		}
	}
	for rel := range deliveredFiles {
		if _, ok := sourceFiles[rel]; !ok {
			return fmt.Errorf("plugin %q: the delivered tree carries %s, which the source does not", plugin, rel)
		}
	}
	return nil
}

// readTreeFiles reads every regular file under root, keyed by its
// slash-separated path relative to root. Git metadata is skipped: the source
// tree here is inside a clone, and `.git` is not part of the plugin. The
// delivery's own ownership sentinel is skipped for the same reason -- it is
// niwa's marker, not the plugin's content.
func readTreeFiles(root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || rel == ".niwa-owned" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[rel] = string(data)
		return nil
	})
	return files, err
}

// theInstanceDeclaresNoCodexHooks scans everything niwa writes for Codex -- the
// payload directory inside each repository and the developer's Codex home --
// for hook definitions or hook state. The scan deliberately does not descend
// into a delivered skills tree: a plugin may ship its own Claude-side hooks, and
// those are the plugin's, not something niwa declared.
func theInstanceDeclaresNoCodexHooks(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	roots := []string{filepath.Join(s.homeDir, ".codex")}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	groups, err := os.ReadDir(instRoot)
	if err != nil {
		return fmt.Errorf("reading %s: %w", instRoot, err)
	}
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(instRoot, g.Name()))
		if err != nil {
			continue
		}
		for _, r := range repos {
			// A group directory also carries niwa's own group-level context
			// document, which is a file rather than a repository.
			if !r.IsDir() {
				continue
			}
			roots = append(roots, filepath.Join(instRoot, g.Name(), r.Name(), codexPayloadDir))
		}
	}

	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "skills" {
				return fs.SkipDir
			}
			if strings.Contains(strings.ToLower(d.Name()), "hook") {
				return fmt.Errorf("%s looks like Codex hook state, and niwa writes none", path)
			}
			if !d.Type().IsRegular() {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "#") {
					continue
				}
				if strings.Contains(strings.ToLower(line), "hook") {
					return fmt.Errorf("%s:%d declares a hook: %q", path, i+1, line)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// --- the developer's Codex setup --------------------------------------

func developerCodexConfigPath(s *testState) string {
	return filepath.Join(s.homeDir, ".codex", "config.toml")
}

func developerCodexCredentialPath(s *testState) string {
	return filepath.Join(s.homeDir, ".codex", codexCredentialName)
}

func theDeveloperCodexConfigExistsWithBody(ctx context.Context, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := developerCodexConfigPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ctx, fmt.Errorf("creating the Codex home: %w", err)
	}
	content := strings.TrimRight(body.Content, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ctx, fmt.Errorf("writing %s: %w", path, err)
	}
	s.codexConfigSeed = []byte(content)
	return ctx, nil
}

func theDeveloperCodexHomeHasACredentialFile(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := developerCodexCredentialPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ctx, fmt.Errorf("creating the Codex home: %w", err)
	}
	content := []byte(`{"tokens":{"access_token":"developer-login-state"}}` + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return ctx, fmt.Errorf("writing %s: %w", path, err)
	}
	// Backdate the file so an mtime comparison can catch a rewrite that
	// happens to reproduce the same bytes within the filesystem's timestamp
	// granularity.
	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		return ctx, fmt.Errorf("backdating %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ctx, fmt.Errorf("stating %s: %w", path, err)
	}
	s.codexCredentialBytes = content
	s.codexCredentialModTime = info.ModTime()
	return ctx, nil
}

func theDeveloperCodexCredentialFileIsUnreadable(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := developerCodexCredentialPath(s)
	if err := os.Chmod(path, 0o000); err != nil {
		return ctx, fmt.Errorf("chmod 000 %s: %w", path, err)
	}
	return ctx, nil
}

func theDeveloperCodexCredentialFileIsUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.codexCredentialBytes == nil {
		return fmt.Errorf("no credential snapshot; seed one first")
	}
	path := developerCodexCredentialPath(s)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stating %s: %w", path, err)
	}
	if !info.ModTime().Equal(s.codexCredentialModTime) {
		return fmt.Errorf("%s was touched: mtime %s, was %s", path, info.ModTime(), s.codexCredentialModTime)
	}
	if info.Mode().Perm() == 0 {
		// Unreadable by design in the sandboxed-home scenario; mtime and mode
		// are the whole assertion there.
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !bytes.Equal(data, s.codexCredentialBytes) {
		return fmt.Errorf("%s changed:\ngot:\n%s\nwant:\n%s", path, data, s.codexCredentialBytes)
	}
	return nil
}

// codexTrustKeys parses the developer's Codex config and returns the project
// keys it carries, sorted. Parsing rather than grepping is deliberate: a config
// niwa left unparseable is itself a failure this should report.
func codexTrustKeys(s *testState) ([]string, error) {
	path := developerCodexConfigPath(s)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w\n%s", path, err, data)
	}
	projects, ok := doc["projects"].(map[string]any)
	if !ok {
		return nil, nil
	}
	keys := make([]string, 0, len(projects))
	for key := range projects {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func theDeveloperCodexConfigHasNProjectEntries(ctx context.Context, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	keys, err := codexTrustKeys(s)
	if err != nil {
		return err
	}
	if len(keys) != want {
		return fmt.Errorf("the Codex config carries %d project entries %v, want %d", len(keys), keys, want)
	}
	return nil
}

// canonicalRepoPath resolves every symlink in a managed repo's path, which is
// the key form niwa must write.
func canonicalRepoPath(s *testState, repo, instance string) (string, error) {
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repo)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(repoPath)
}

func theDeveloperCodexConfigTrustsRepo(ctx context.Context, repo, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	canonical, err := canonicalRepoPath(s, repo, instance)
	if err != nil {
		return err
	}
	keys, err := codexTrustKeys(s)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key == canonical {
			return nil
		}
	}
	return fmt.Errorf("no trust entry keyed %q; the config carries %v (an entry keyed by an unresolved path trusts nothing)", canonical, keys)
}

func theDeveloperCodexConfigDoesNotTrustRepo(ctx context.Context, repo, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	canonical, err := canonicalRepoPath(s, repo, instance)
	if err != nil {
		return err
	}
	keys, err := codexTrustKeys(s)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key == canonical {
			return fmt.Errorf("the Codex config trusts %q, which this apply must have withheld", canonical)
		}
	}
	return nil
}

func theDeveloperCodexConfigDoesNotTrustLastWorktree(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	canonical, err := filepath.EvalSymlinks(s.lastSessionWorktreePath)
	if err != nil {
		return fmt.Errorf("canonicalizing %s: %w", s.lastSessionWorktreePath, err)
	}
	keys, err := codexTrustKeys(s)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key == canonical || strings.HasPrefix(key, canonical+string(filepath.Separator)) {
			return fmt.Errorf("the Codex config carries a worktree-keyed entry %q; the repository's own entry already covers its worktrees", key)
		}
	}
	return nil
}

// theDeveloperCodexConfigGrewOnlyByProjectEntries is the additivity check: the
// seeded content survives byte for byte as a prefix, every top-level table
// except `projects` is unchanged, and every added project key names a path
// inside the workspace root -- so a repository outside any niwa instance
// behaves exactly as it did before preparation.
func theDeveloperCodexConfigGrewOnlyByProjectEntries(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.codexConfigSeed == nil {
		return fmt.Errorf("no seeded Codex config; seed one first")
	}
	path := developerCodexConfigPath(s)
	after, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	if !bytes.HasPrefix(after, s.codexConfigSeed) {
		return fmt.Errorf("%s no longer starts with the developer's own content:\ngot:\n%s\nseeded:\n%s", path, after, s.codexConfigSeed)
	}

	var seeded, current map[string]any
	if err := toml.Unmarshal(s.codexConfigSeed, &seeded); err != nil {
		return fmt.Errorf("parsing the seeded config: %w", err)
	}
	if err := toml.Unmarshal(after, &current); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	for key, want := range seeded {
		got, ok := current[key]
		if !ok {
			return fmt.Errorf("%s dropped the developer's %q", path, key)
		}
		if key == "projects" {
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			return fmt.Errorf("%s altered the developer's %q: got %v, want %v", path, key, got, want)
		}
	}
	for key := range current {
		if _, ok := seeded[key]; !ok && key != "projects" {
			return fmt.Errorf("%s gained the top-level key %q; niwa writes nothing global", path, key)
		}
	}

	realRoot, err := filepath.EvalSymlinks(s.workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolving the workspace root: %w", err)
	}
	keys, err := codexTrustKeys(s)
	if err != nil {
		return err
	}
	seededKeys := map[string]bool{}
	if projects, ok := seeded["projects"].(map[string]any); ok {
		for key := range projects {
			seededKeys[key] = true
		}
	}
	for _, key := range keys {
		if seededKeys[key] {
			continue
		}
		if !strings.HasPrefix(key, realRoot+string(filepath.Separator)) {
			return fmt.Errorf("%s gained a project entry %q outside the workspace root %s", path, key, realRoot)
		}
	}
	return nil
}

// --- working-tree assertions ------------------------------------------

func theFileExistsAtLocation(ctx context.Context, name, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
		return fmt.Errorf("expected %s under %s: %w", name, dir, err)
	}
	return nil
}

func theFileDoesNotExistAtLocation(ctx context.Context, name, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	full := filepath.Join(dir, filepath.FromSlash(name))
	if _, err := os.Lstat(full); err == nil {
		return fmt.Errorf("%s exists and must not", full)
	}
	return nil
}

// theFileAtLocationMatchesGitHEAD proves a committed file was not rewritten:
// its working-tree bytes equal the committed blob.
func theFileAtLocationMatchesGitHEAD(ctx context.Context, name, loc string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return err
	}
	committed, err := runGitInDir(dir, "show", "HEAD:"+name)
	if err != nil {
		return fmt.Errorf("git show HEAD:%s in %s: %w\n%s", name, dir, err, committed)
	}
	working, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}
	if string(working) != committed {
		return fmt.Errorf("%s differs from its committed blob:\nworking tree:\n%s\nHEAD:\n%s", name, working, committed)
	}
	return nil
}

func theGitStatusOfEveryRepoIsClean(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	groups, err := os.ReadDir(instRoot)
	if err != nil {
		return fmt.Errorf("reading %s: %w", instRoot, err)
	}
	checked := 0
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(instRoot, g.Name()))
		if err != nil {
			continue
		}
		for _, r := range repos {
			repoDir := filepath.Join(instRoot, g.Name(), r.Name())
			if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
				continue
			}
			out, err := runGitInDir(repoDir, "status", "--porcelain")
			if err != nil {
				return fmt.Errorf("git status in %s: %w\n%s", repoDir, err, out)
			}
			if strings.TrimSpace(out) != "" {
				return fmt.Errorf("repo %s is dirty after apply:\n%s", repoDir, out)
			}
			checked++
		}
	}
	if checked == 0 {
		return fmt.Errorf("instance %q holds no cloned repositories to check", instance)
	}
	return nil
}

func theGitStatusOfLastWorktreeIsClean(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	out, err := runGitInDir(s.lastSessionWorktreePath, "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status in %s: %w\n%s", s.lastSessionWorktreePath, err, out)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("worktree %s is dirty after materialization:\n%s", s.lastSessionWorktreePath, out)
	}
	return nil
}

// theGitExcludeCarriesCodexPatterns pins the pattern text and its multiplicity.
// The multiplicity half is what catches an exclude writer that appends on every
// apply, and the pattern half is what catches a name niwa writes and forgets to
// cover -- which leaves the tree reading dirty and stops a worktree teardown
// from ever reclaiming its worktree.
func theGitExcludeCarriesCodexPatterns(ctx context.Context, repo, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := repoExcludePath(s, instance, repo)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	lines := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		lines[strings.TrimRight(line, "\r")]++
	}
	for _, want := range []string{codexPayloadDir + "/", codexOverrideName} {
		switch lines[want] {
		case 1:
		case 0:
			return fmt.Errorf("%s carries no %q pattern:\n%s", path, want, data)
		default:
			return fmt.Errorf("%s carries the %q pattern %d times:\n%s", path, want, lines[want], data)
		}
	}
	return nil
}

// theCodexPayloadDirCannotBeWritten removes the generated payload and makes its
// directory read-only, so the next apply's write into it fails at the syscall.
// It is how the unwritable-target scenario reaches a real delivery failure
// without asking niwa to pretend one.
func theCodexPayloadDirCannotBeWritten(ctx context.Context, repo, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repo)
	if err != nil {
		return ctx, err
	}
	payload := filepath.Join(repoPath, codexPayloadDir)
	if _, err := os.Stat(codexPayloadPath(repoPath)); err != nil {
		return ctx, fmt.Errorf("repo %q has no generated Codex payload to make unwritable: %w", repo, err)
	}
	if err := os.Remove(codexPayloadPath(repoPath)); err != nil {
		return ctx, fmt.Errorf("removing the generated payload: %w", err)
	}
	if err := os.Chmod(payload, 0o500); err != nil {
		return ctx, fmt.Errorf("chmod 500 %s: %w", payload, err)
	}
	s.restoreOnCleanup = append(s.restoreOnCleanup, payload)
	return ctx, nil
}

// --- the declaration table --------------------------------------------

// capabilityByName resolves a capability from the stable name the declaration
// table and the generated gap list both use.
func capabilityByName(name string) (agentplan.Capability, error) {
	for _, c := range agentplan.All() {
		if c.String() == name {
			return c, nil
		}
	}
	return 0, fmt.Errorf("no capability named %q in the closed set", name)
}

// theCapabilityIsDeclaredUnavailableForCodex asserts a gap is the declaration's
// answer rather than a rule this test knows on its own. A behavior a scenario
// pins as absent has to be absent in the table too, or the guide a developer
// reads and the binary they run tell them different things.
func theCapabilityIsDeclaredUnavailableForCodex(ctx context.Context, name string) error {
	c, err := capabilityByName(name)
	if err != nil {
		return err
	}
	d, err := agentplan.Lookup(c, agent.AgentCodex)
	if err != nil {
		return fmt.Errorf("looking up %s for codex: %w", name, err)
	}
	if d.State != agentplan.StateUnavailable {
		return fmt.Errorf("%s is not declared unavailable for codex (state %d); the scenario below pins behavior the table no longer agrees with", name, d.State)
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("%s is declared unavailable for codex with no reason", name)
	}
	return nil
}

// theGapListCarriesTheDeclaredReason closes the loop to the document a
// developer actually reads: the committed guide's generated section has to
// carry this capability's declared reason verbatim. Together with the drift
// test in internal/agentplan, that makes the scenario, the table, and the guide
// one fact instead of three.
func theGapListCarriesTheDeclaredReason(ctx context.Context, name string) error {
	c, err := capabilityByName(name)
	if err != nil {
		return err
	}
	d, err := agentplan.Lookup(c, agent.AgentCodex)
	if err != nil {
		return fmt.Errorf("looking up %s for codex: %w", name, err)
	}
	data, err := os.ReadFile(codexGuidePath)
	if err != nil {
		return fmt.Errorf("reading the committed guide: %w", err)
	}
	// The generator wraps the reason across lines, so the comparison is made
	// against the guide with its line breaks collapsed rather than against the
	// raw file, which would fail on formatting alone.
	flat := strings.Join(strings.Fields(string(data)), " ")
	want := strings.Join(strings.Fields(d.Reason), " ")
	if !strings.Contains(flat, want) {
		return fmt.Errorf("the gap list in %s does not carry the declared reason for %s:\nwant: %s", codexGuidePath, name, want)
	}
	return nil
}

// theRefusalCarriesTheDeclaredReason closes the last side of the same loop. The
// two steps above tie the table to the guide; this one ties the table to the
// binary, by asserting the message a developer actually hits carries the
// declaration's own words rather than a sentence written beside it. With all
// three, a gap cannot be described three different ways by three different
// surfaces -- which is the failure mode the whole contract exists to prevent,
// and the one a hand-written refusal string reintroduces the moment somebody
// edits the table without editing it.
func theRefusalCarriesTheDeclaredReason(ctx context.Context, name string) error {
	c, err := capabilityByName(name)
	if err != nil {
		return err
	}
	d, err := agentplan.Lookup(c, agent.AgentCodex)
	if err != nil {
		return fmt.Errorf("looking up %s for codex: %w", name, err)
	}
	s := getState(ctx)
	flat := strings.Join(strings.Fields(s.stderr), " ")
	want := strings.Join(strings.Fields(d.Reason), " ")
	if !strings.Contains(flat, want) {
		return fmt.Errorf("the refusal does not carry the declared reason for %s:\nwant: %s\ngot:\n%s", name, want, s.stderr)
	}
	return nil
}

// --- live, codex-gated -------------------------------------------------

// codexIsAvailable gates the live scenarios: `codex` on PATH and a login the
// session can actually use. Either one missing leaves the scenario pending
// rather than failed, so the @critical set stays offline and CI stays green
// with neither present.
//
// The login needs seeding because the sandbox redirects $HOME, and that
// redirection is the point: niwa's trust entries have to land in a config the
// session reads, and niwa must never be pointed at the developer's real one. So
// the developer's own credential file is copied into the sandbox Codex home --
// only the credential file, only when the developer already has one, and only
// into a directory the next scenario deletes. Without it the session would
// reach its 401 and the scenario would fail for a reason that has nothing to do
// with what niwa materialized.
func codexIsAvailable(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		return ctx, godog.ErrPending
	}

	developerHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if developerHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ctx, godog.ErrPending
		}
		developerHome = filepath.Join(home, ".codex")
	}
	credential, err := os.ReadFile(filepath.Join(developerHome, codexCredentialName))
	if err != nil {
		return ctx, godog.ErrPending
	}

	sandboxHome := filepath.Join(s.homeDir, ".codex")
	if err := os.MkdirAll(sandboxHome, 0o755); err != nil {
		return ctx, fmt.Errorf("creating the sandbox Codex home: %w", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxHome, codexCredentialName), credential, 0o600); err != nil {
		return ctx, fmt.Errorf("seeding the sandbox login: %w", err)
	}
	return ctx, nil
}

// codexLiveEnv is the environment a live Codex invocation runs under: the
// scenario sandbox with every NIWA_* variable and CODEX_HOME stripped, which is
// what the criteria mean by "a shell with no environment preparation".
func codexLiveEnv(s *testState) []string {
	var env []string
	for _, kv := range s.buildEnv() {
		if strings.HasPrefix(kv, "NIWA_") || strings.HasPrefix(kv, "CODEX_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// theCodexSandboxCanRunHere gates the one live scenario whose subject is a
// write. Codex confines a session to its own sandbox before letting it touch
// the filesystem, and on Linux that sandbox needs an unprivileged user
// namespace with a uid map. In a container that withholds it -- CI runners and
// restricted developer containers both do -- every write a session attempts is
// blocked whatever niwa prepared, so a failure there would measure the machine
// rather than the preparation. The probe is the mapping itself rather than a
// check for any particular sandbox binary, so it stays true as Codex's
// implementation moves.
func theCodexSandboxCanRunHere(ctx context.Context) (context.Context, error) {
	if runtime.GOOS != "linux" {
		return ctx, nil
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		return ctx, godog.ErrPending
	}
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := exec.CommandContext(probeCtx, "unshare", "--user", "--map-root-user", "true").Run(); err != nil {
		return ctx, godog.ErrPending
	}
	return ctx, nil
}

func iRunCodexExecFrom(ctx context.Context, loc string, prompt *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return ctx, err
	}
	runCtx, cancel := context.WithTimeout(ctx, codexLiveTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "codex", "exec", strings.TrimSpace(prompt.Content))
	cmd.Dir = dir
	cmd.Env = codexLiveEnv(s)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	s.stdout = out.String()
	s.stderr = ""
	s.codexSessionOutput = out.String()
	if cmd.ProcessState == nil {
		return ctx, fmt.Errorf("running codex exec: %w", runErr)
	}
	s.exitCode = cmd.ProcessState.ExitCode()
	return ctx, nil
}

// iStartInteractiveCodexAt starts the Codex TUI under a pty with no input and
// lets it dwell. A trust, review, or approval prompt appears at startup, so the
// transcript after the dwell is what the criterion is decided against.
func iStartInteractiveCodexAt(ctx context.Context, loc string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, err := exec.LookPath("script"); err != nil {
		return ctx, fmt.Errorf("util-linux `script` not on PATH; cannot drive the interactive scenario: %w", err)
	}
	dir, err := s.resolveLocation(loc)
	if err != nil {
		return ctx, err
	}

	runCtx, cancel := context.WithTimeout(ctx, codexInteractiveDwell)
	defer cancel()

	inner := "cd " + shellQuote(dir) + " && exec codex"
	cmd := exec.CommandContext(runCtx, "script", "-q", "-c", inner, "/dev/null")
	cmd.Env = codexLiveEnv(s)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// The TUI never exits on its own, so the dwell's deadline killing it is the
	// expected outcome; the transcript is what matters.
	_ = cmd.Run()
	s.codexSessionOutput = out.String()
	return ctx, nil
}

func theCodexSessionShowsNoTrustPrompt(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	lowered := strings.ToLower(s.codexSessionOutput)
	for _, marker := range []string{"do you trust", "trust this", "allow this folder", "approve", "y/n"} {
		if strings.Contains(lowered, marker) {
			return fmt.Errorf("the session showed a prompt (%q) in:\n%s", marker, s.codexSessionOutput)
		}
	}
	return nil
}

// theCodexSessionReachedReadyState rejects a vacuous pass: a session that
// printed nothing, or that fell over on its own login rather than starting,
// says nothing about whether niwa's preparation raised a prompt.
func theCodexSessionReachedReadyState(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if strings.TrimSpace(s.codexSessionOutput) == "" {
		return fmt.Errorf("the session produced no output at all, so nothing about its start is decidable")
	}
	lowered := strings.ToLower(s.codexSessionOutput)
	for _, broken := range []string{"unauthorized", "failed to connect", "not logged in"} {
		if strings.Contains(lowered, broken) {
			return fmt.Errorf("the session never got past its own login (%q), so its start proves nothing about niwa's preparation:\n%s", broken, s.codexSessionOutput)
		}
	}
	return nil
}
