package workspace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// Layer sentinels. Each names exactly one level of the chain, so an assertion
// on the composed override says which layer reached the file and which did not.
const (
	wtInstanceSentinel = "INSTANCE-LAYER-SENTINEL"
	wtGroupSentinel    = "GROUP-LAYER-SENTINEL"
	wtRepoSentinel     = "REPO-LAYER-SENTINEL"
)

// codexWorktreeFixture builds an instance with a Codex payload, a real git
// clone at <instanceRoot>/tools/app, and a real linked worktree of it -- a
// `git worktree add` tree, not a synthetic gitlink, because the whole finding
// this issue rests on is that the real thing needs no special mechanism (its
// `.git` is a regular file and Codex's marker check accepts it).
//
// Returns the config, the config dir, the instance root, the clone, and the
// worktree.
func codexWorktreeFixture(t *testing.T) (*config.WorkspaceConfig, string, string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()

	configDir := filepath.Join(tmpDir, "config")
	contentDir := filepath.Join(configDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeContent := func(name, body string) {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeContent("ws.md", "# workspace\n\n"+wtInstanceSentinel+"\n")
	writeContent("grp.md", "# group {group_name}\n\n"+wtGroupSentinel+"\n")
	writeContent("app.md", "# {repo_name}\n\n"+wtRepoSentinel+"\n")

	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "myws", ContentDir: "claude"},
		Claude: config.ClaudeConfig{
			Content: config.ContentConfig{
				Workspace: config.ContentEntry{Source: "ws.md"},
				Groups:    map[string]config.ContentEntry{"tools": {Source: "grp.md"}},
				Repos:     map[string]config.RepoContentEntry{"app": {Source: "app.md"}},
			},
		},
	}

	instanceRoot := filepath.Join(tmpDir, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instanceRoot, workspaceContextFile), []byte("# workspace context\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The instance payload `niwa apply` writes. The worktree path never creates
	// it; it only delivers it.
	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	if err := os.MkdirAll(payloadDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payloadDir, codexPayloadConfigName), []byte(renderCodexPayloadConfig(131072)), 0o644); err != nil {
		t.Fatal(err)
	}

	clone := filepath.Join(instanceRoot, "tools", "app")
	if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, tmpDir, "init", clone)
	if err := os.WriteFile(filepath.Join(clone, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, clone, "add", "README")
	runGitWT(t, clone, "commit", "-m", "init")

	worktreePath := filepath.Join(instanceRoot, ".niwa", "worktrees", "app-abc123")
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitWT(t, clone, "worktree", "add", worktreePath, "-b", "wtbranch")

	return cfg, configDir, instanceRoot, worktreePath, clone
}

// applyWorktree runs ApplyToWorktree over the fixture and returns the written
// files and whatever the run reported.
func applyWorktree(t *testing.T, cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath string) ([]string, string) {
	t.Helper()
	var out bytes.Buffer
	written, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "tools", "app",
		"ship-the-thing", "wtbranch", WorktreeApplyOptions{Stderr: &out})
	if err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	return written, out.String()
}

func worktreeOverride(t *testing.T, worktreePath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(worktreePath, CodexOverrideFileName))
	if err != nil {
		t.Fatalf("reading worktree %s: %v", CodexOverrideFileName, err)
	}
	return string(data)
}

// TestApplyToWorktreeCodexOverrideCarriesTheWholeChain is the criterion the
// composition rule exists for, in its worktree form. Codex's walk stops at the
// worktree root, so an override holding the framing alone -- which names the
// repository and the branch and would pass any "the file exists and mentions
// this worktree" check -- would leave the session with no workspace context at
// all. The instance sentinel is what fails when the file collapses to
// worktree-only content.
func TestApplyToWorktreeCodexOverrideCarriesTheWholeChain(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)

	// The worktree checks out a real branch and can carry a committed context
	// file just as a clone can. It is committed here, on the worktree's own
	// branch, so the inline is exercised against tracked content.
	commitInRepo(t, worktreePath, agent.AgentCodex.RootContextFileName(), "# app\n\nCOMMITTED-CONTEXT\n")

	applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)

	got := worktreeOverride(t, worktreePath)
	if first, _, _ := strings.Cut(got, "\n"); first != CodexGenerationMarker {
		t.Errorf("first line = %q, want the generation marker", first)
	}
	for _, want := range []string{
		wtInstanceSentinel,
		wtGroupSentinel,
		wtRepoSentinel,
		"COMMITTED-CONTEXT",
		worktreeContextHeading,
		"ship-the-thing",
		"wtbranch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("composed worktree override is missing %q:\n%s", want, got)
		}
	}

	// Outermost first, framing last: a session reads one general-to-specific
	// document, and truncation eats the tail rather than the workspace layers.
	if strings.Index(got, wtInstanceSentinel) > strings.Index(got, worktreeContextHeading) {
		t.Errorf("framing must be appended after the workspace layers, got:\n%s", got)
	}

	// The payload reaches the worktree through the same link a clone gets, with
	// its target computed for the worktree's own location.
	link := filepath.Join(worktreePath, CodexPayloadDirName)
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("reading %s: %v", link, err)
	}
	wantTarget, err := filepath.Abs(filepath.Join(instanceRoot, CodexPayloadDirName))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(target) != wantTarget {
		t.Errorf("payload link target = %q, want %q", target, wantTarget)
	}
}

// TestApplyToWorktreeCodexRefreshesItsOwnOverride covers the refresh half: a
// second apply after a content change delivers the new content and leaves none
// of the old, and it recognizes the file it wrote last time by its marker
// rather than reporting a conflict -- which is what the standalone worktree
// path needs, since it persists no managed-file records to recognize it by.
func TestApplyToWorktreeCodexRefreshesItsOwnOverride(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)

	applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)
	if got := worktreeOverride(t, worktreePath); !strings.Contains(got, wtRepoSentinel) {
		t.Fatalf("first apply did not deliver the repo layer:\n%s", got)
	}

	if err := os.WriteFile(filepath.Join(configDir, "claude", "app.md"), []byte("# app\n\nREPO-LAYER-REVISED\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, reported := applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)
	if strings.Contains(reported, "occupied by something niwa did not write") {
		t.Errorf("re-apply must recognize its own override by its marker, got:\n%s", reported)
	}

	got := worktreeOverride(t, worktreePath)
	if !strings.Contains(got, "REPO-LAYER-REVISED") {
		t.Errorf("refreshed override is missing the new content:\n%s", got)
	}
	if strings.Contains(got, wtRepoSentinel) {
		t.Errorf("refreshed override still carries the previous content:\n%s", got)
	}
	if n := strings.Count(got, CodexGenerationMarker); n != 1 {
		t.Errorf("marker appears %d times, want 1 (regeneration, not append):\n%s", n, got)
	}
}

// TestApplyToWorktreeWritesNoTrustEntry pins the measured finding: trust
// resolves through the worktree's `.git` file to the main repository root, so
// the repository's single entry already covers every worktree of it. A
// per-worktree entry would be redundant, and it would accumulate one line in
// the developer's own Codex config for every worktree that ever existed.
func TestApplyToWorktreeWritesNoTrustEntry(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)

	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	written, _ := applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)

	configPath, err := CodexConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(configPath); err == nil {
		t.Fatalf("worktree apply wrote the developer's Codex config:\n%s", data)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stating %s: %v", configPath, err)
	}
	for _, p := range written {
		if strings.HasPrefix(p, codexHome) {
			t.Errorf("worktree apply wrote %s outside the instance", p)
		}
	}
}

// TestApplyToWorktreeCodexLeavesGitStatusClean checks the promise that a
// prepared worktree is still a working tree its owner can use: the two names
// niwa plants are covered by the managed exclude block, which resolves from the
// worktree through the shared common git dir.
func TestApplyToWorktreeCodexLeavesGitStatusClean(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)

	applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)

	for _, name := range []string{CodexPayloadDirName, CodexOverrideFileName} {
		if _, err := os.Lstat(filepath.Join(worktreePath, name)); err != nil {
			t.Fatalf("expected %s in the worktree: %v", name, err)
		}
	}
	if out := gitStatusPorcelainWT(t, worktreePath); out != "" {
		t.Errorf("a freshly prepared worktree must read clean, got:\n%s", out)
	}
}

// TestApplyToWorktreeCodexConflictsDegradeAndReport applies the conflict rule
// in a worktree exactly as it applies in a clone: a name the checkout commits
// is left untouched, the degradation is reported rather than skipped quietly,
// and a `.codex` conflict suppresses the composed override with it (the
// override's budget is declared in the payload the refused delivery would have
// put in reach).
func TestApplyToWorktreeCodexConflictsDegradeAndReport(t *testing.T) {
	t.Run("committed override", func(t *testing.T) {
		cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)
		commitInRepo(t, worktreePath, CodexOverrideFileName, "# theirs\n\nCOMMITTED-OVERRIDE\n")

		_, reported := applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)

		if got := worktreeOverride(t, worktreePath); !strings.Contains(got, "COMMITTED-OVERRIDE") {
			t.Errorf("niwa wrote over a committed %s:\n%s", CodexOverrideFileName, got)
		}
		if !strings.Contains(reported, CodexOverrideFileName) || !strings.Contains(reported, "occupied by something niwa did not write") {
			t.Errorf("the refusal must be reported, got:\n%s", reported)
		}
		// An override conflict alone leaves the payload delivery intact.
		if _, err := os.Lstat(filepath.Join(worktreePath, CodexPayloadDirName)); err != nil {
			t.Errorf("payload delivery must survive an override-only conflict: %v", err)
		}
	})

	t.Run("committed payload directory", func(t *testing.T) {
		cfg, configDir, instanceRoot, worktreePath, _ := codexWorktreeFixture(t)
		commitInRepo(t, worktreePath, filepath.Join(CodexPayloadDirName, "config.toml"), "# theirs\n")

		_, reported := applyWorktree(t, cfg, configDir, instanceRoot, worktreePath)

		if _, err := os.Stat(filepath.Join(worktreePath, CodexOverrideFileName)); !os.IsNotExist(err) {
			t.Errorf("a .codex conflict must suppress the composed override too (err = %v)", err)
		}
		committed, err := os.ReadFile(filepath.Join(worktreePath, CodexPayloadDirName, "config.toml"))
		if err != nil || !strings.Contains(string(committed), "theirs") {
			t.Errorf("the committed .codex content was modified: %q, %v", committed, err)
		}
		if !strings.Contains(reported, CodexPayloadDirName) || !strings.Contains(reported, "no composed") {
			t.Errorf("the whole-repository degradation must be reported, got:\n%s", reported)
		}
	})
}
