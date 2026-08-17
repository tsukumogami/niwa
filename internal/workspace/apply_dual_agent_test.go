package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// dualAgentFixture writes a workspace whose config declares content at every
// level (instance, group, repository) plus one repo that is already "cloned",
// so a Create produces the full Claude-facing tree without network access.
// defaultAgent is written into [workspace].default_agent verbatim; an empty
// string omits the key, standing for a config that predates the setting.
// It returns the config dir, the workspace root, and the loaded config.
func dualAgentFixture(t *testing.T, defaultAgent string) (string, string, *config.WorkspaceConfig) {
	t.Helper()

	root := t.TempDir()
	niwaDir := filepath.Join(root, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	agentLine := ""
	if defaultAgent != "" {
		agentLine = "default_agent = \"" + defaultAgent + "\"\n"
	}
	configTOML := `
[workspace]
name = "ws"
content_dir = "claude"
` + agentLine + `
[groups.tools]

[claude.content.workspace]
source = "ws.md"

[claude.content.groups.tools]
source = "grp.md"

[claude.content.repos.app]
source = "app.md"

[repos.app]
url = "https://example.invalid/app.git"
group = "tools"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"ws.md":  "instance layer for {workspace_name}\n",
		"grp.md": "group layer for {group_name}\n",
		"app.md": "repo layer for {repo_name}\n",
	} {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Pre-create the repo checkout with a .git marker so the cloner is a no-op.
	if err := os.MkdirAll(filepath.Join(root, "ws", "tools", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return niwaDir, root, result.Config
}

// createDualAgentInstance runs Create the way the CLI does: the session agent is
// resolved from the workspace default, and the resolved value is handed to the
// applier. It returns the instance root.
func createDualAgentInstance(t *testing.T, defaultAgent string) string {
	t.Helper()

	niwaDir, root, cfg := dualAgentFixture(t, defaultAgent)

	ag, err := agent.ResolveAgent("", "", cfg.Workspace.DefaultAgent)
	if err != nil {
		t.Fatalf("resolving agent for default %q: %v", defaultAgent, err)
	}

	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	applier.Agent = ag

	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create with default_agent %q: %v", defaultAgent, err)
	}
	return instanceRoot
}

// claudeFacingTree walks an instance root and returns every path a Claude
// session reads -- the CLAUDE.md tree, the .claude/ directories (settings and
// skills live there), and the workspace-context file -- keyed by path relative
// to the instance root, valued by content.
//
// Content carries absolute paths (@imports, hook commands) that differ between
// two applies into different temp directories, so each run's roots are replaced
// by placeholders. That normalization is location-only: it cannot mask a
// difference the workspace's default_agent would cause.
func claudeFacingTree(t *testing.T, instanceRoot string) map[string]string {
	t.Helper()

	normalize := strings.NewReplacer(
		instanceRoot, "{instance}",
		filepath.Dir(instanceRoot), "{root}",
	)

	tree := map[string]string{}
	err := filepath.Walk(instanceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(instanceRoot, path)
		if relErr != nil {
			return relErr
		}
		base := filepath.Base(rel)
		inClaudeDir := false
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			if part == ".claude" {
				inClaudeDir = true
				break
			}
		}
		if !inClaudeDir && !strings.HasPrefix(base, "CLAUDE") && base != "workspace-context.md" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		tree[rel] = normalize.Replace(string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", instanceRoot, err)
	}
	return tree
}

// TestCreate_ClaudeTreeIdenticalAcrossDefaultAgent pins PRD R2: what niwa
// prepares for Claude does not depend on the workspace's default_agent. The
// same config applied with the setting at "claude", at "codex", and absent
// entirely produces the same Claude-facing paths with the same content.
//
// This is a cross-setting check, not a before/after one: it cannot see a
// regression that changed the Claude tree uniformly under every setting. That
// half of R2 is carried by the rest of the suite, which this change leaves
// alone.
func TestCreate_ClaudeTreeIdenticalAcrossDefaultAgent(t *testing.T) {
	baseline := claudeFacingTree(t, createDualAgentInstance(t, "claude"))

	// Guard against a vacuous comparison: the tree must actually carry the
	// content this workspace configures at each level.
	for _, want := range []string{"CLAUDE.md", filepath.Join("tools", "CLAUDE.md"), filepath.Join("tools", "app", "CLAUDE.local.md")} {
		if _, ok := baseline[want]; !ok {
			t.Fatalf("baseline Claude tree missing %s; got %v", want, treePaths(baseline))
		}
	}
	if len(baseline) < 4 {
		t.Fatalf("baseline Claude tree looks too small: %v", treePaths(baseline))
	}

	for _, defaultAgent := range []string{"codex", ""} {
		t.Run("default_agent="+defaultAgent, func(t *testing.T) {
			got := claudeFacingTree(t, createDualAgentInstance(t, defaultAgent))
			for path, content := range baseline {
				gotContent, ok := got[path]
				if !ok {
					t.Errorf("%s missing from the Claude tree", path)
					continue
				}
				if gotContent != content {
					t.Errorf("%s content differs from the claude-default apply", path)
				}
			}
			for path := range got {
				if _, ok := baseline[path]; !ok {
					t.Errorf("%s appears in the Claude tree only under this setting", path)
				}
			}
		})
	}
}

// TestCreate_InstanceRootServesBothAgents asserts every apply leaves an
// instance root both agents can read, whatever default_agent says: CLAUDE.md
// and AGENTS.md both exist and both carry the instance-level content
// (DESIGN-dual-agent-workspace Decision 7A, PRD R1).
func TestCreate_InstanceRootServesBothAgents(t *testing.T) {
	for _, defaultAgent := range []string{"claude", "codex", ""} {
		t.Run("default_agent="+defaultAgent, func(t *testing.T) {
			instanceRoot := createDualAgentInstance(t, defaultAgent)
			for _, ag := range materializedAgents {
				name := ag.RootContextFileName()
				data, err := os.ReadFile(filepath.Join(instanceRoot, name))
				if err != nil {
					t.Fatalf("reading instance-root %s: %v", name, err)
				}
				if !strings.Contains(string(data), "instance layer for ws") {
					t.Errorf("instance-root %s missing the instance-level content; got:\n%s", name, data)
				}
			}
		})
	}
}

func treePaths(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
