package workspace

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestSecretSafetyTravelsWithTheFirstSecretWrite is the one test both halves of
// the hygiene obligation are asserted in, deliberately together.
//
// The obligation is not two separate improvements that happen to be adjacent.
// The moment a resolved session environment lands in a generated configuration,
// that file holds credentials, and the window between the write and the
// protections is exactly the exposure. Splitting the assertions would let one
// half regress while the other still passes, and a reviewer reading either one
// alone would not see that the pair is the point.
//
// Three things are checked, from the same generated write:
//
//   - the mode is secretFileMode -- 0o600, the same constant every other
//     secret-bearing file niwa writes uses -- on an entry whose capability
//     delivers resolved environment values;
//   - the repository the file lands in gets git-exclude patterns covering
//     every Codex-side name niwa writes there -- the payload directory and the
//     context document beside it -- so a niwa-written secret is never reported
//     as an untracked file and the tree never reads dirty;
//   - the instance root's .gitignore covers the payload name too, because the
//     ".local" infix that pattern relies on cannot appear in a generated
//     configuration: it has to sit at the name its agent reads.
func TestSecretSafetyTravelsWithTheFirstSecretWrite(t *testing.T) {
	repo := t.TempDir()
	producer := agentplan.For(agent.AgentCodex)

	env := map[string]string{"API_TOKEN": "s3cr3t"}
	install, err := InstallPayloadConfig(PayloadRequest{
		Scope: agentplan.PayloadInRepo,
		Dir:   repo,
		Env:   env,
	}, producer)
	if err != nil {
		t.Fatalf("InstallPayloadConfig: %v", err)
	}

	// The write happened at all: an empty install would pass every assertion
	// below vacuously.
	path := filepath.Join(repo, ".codex", "config.toml")
	if len(install.Written) != 1 || install.Written[0] != path {
		t.Fatalf("wrote %v, want [%s]", install.Written, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the generated configuration: %v", err)
	}
	if !strings.Contains(string(data), "s3cr3t") {
		t.Fatalf("the generated configuration does not carry the resolved value, so this test is not about a secret-bearing file:\n%s", data)
	}

	// 1. The mode.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != secretFileMode {
		t.Errorf("the configuration carrying resolved environment values is mode %o, want %o", got, secretFileMode)
	}

	// 2. The repository-side exclude patterns, over every Codex-side name a
	//    repository receives -- the payload directory here, and the context
	//    document the same repository gets from its own producer. Both are
	//    collected into the one exclude block the apply writes for the tree.
	repoPatterns := append([]string{}, install.Excludes...)
	cfg, configDir, instanceRoot := setupWorkspaceContentFixture(t)
	content, err := InstallRepoContent(cfg, configDir, "", instanceRoot, "public", "myapp", agent.AgentCodex)
	if err != nil {
		t.Fatalf("InstallRepoContent: %v", err)
	}
	repoPatterns = append(repoPatterns, content.Excludes...)
	for _, want := range []string{".codex/", "AGENTS.override.md"} {
		if !slices.Contains(repoPatterns, want) {
			t.Errorf("the repository's exclude patterns %v do not cover %q", repoPatterns, want)
		}
	}

	// 3. The instance root's coverage, from the same declaration source.
	patterns := agentplan.InstanceExcludePatterns()
	if !slices.Contains(patterns, ".codex/") {
		t.Errorf("instance exclude patterns = %v, want the generated configuration's directory covered", patterns)
	}

	root := t.TempDir()
	if err := EnsureInstanceGitignore(root, patterns...); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("reading the instance .gitignore: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(gitignore)), "\n")
	for _, pattern := range append([]string{instanceGitignorePattern}, patterns...) {
		if !slices.Contains(lines, pattern) {
			t.Errorf("the instance .gitignore does not cover %q:\n%s", pattern, gitignore)
		}
	}
}

// TestPayloadModeIsTheSecretModeForEveryAgent asserts the mode is a property of
// the generated configuration rather than of the one agent whose document the
// test above happens to write. Both agents' payloads can carry vault-resolved
// values, so neither is allowed to land at the ordinary 0o644.
func TestPayloadModeIsTheSecretModeForEveryAgent(t *testing.T) {
	servers := []agentplan.MCPServer{{
		Name:    "files",
		Command: "npx",
		Env:     map[string]string{"TOKEN": "abc"},
	}}

	for _, ag := range agent.All() {
		producer := agentplan.For(ag)
		for _, scope := range []agentplan.PayloadScope{agentplan.PayloadAtInstanceRoot, agentplan.PayloadInRepo} {
			dir := t.TempDir()
			install, err := InstallPayloadConfig(PayloadRequest{
				Scope:   scope,
				Dir:     dir,
				Servers: servers,
				Env:     map[string]string{"API_TOKEN": "s3cr3t"},
			}, producer)
			if err != nil {
				t.Fatalf("InstallPayloadConfig(%s, scope %d): %v", ag, scope, err)
			}
			for _, written := range install.Written {
				info, err := os.Stat(written)
				if err != nil {
					t.Fatalf("stat %s: %v", written, err)
				}
				if got := info.Mode().Perm(); got != secretFileMode {
					t.Errorf("%s wrote %s at mode %o, want %o", ag, written, got, secretFileMode)
				}
			}
		}
	}
}
