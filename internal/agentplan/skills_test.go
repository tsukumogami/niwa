package agentplan

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

func TestSkillsPlanDeliversOneTreePerPlugin(t *testing.T) {
	repo := filepath.Join("/instance", "public", "app")
	plan, err := For(agent.AgentCodex).SkillsPlan(SkillsInputs{
		Dir: repo,
		Plugins: []PluginTree{
			{Name: "shirabe", Root: "/instance/.niwa/marketplaces/tools/shirabe"},
			{Name: "tsukumogami", Root: "/instance/private/tools/plugins/tsukumogami"},
		},
	})
	if err != nil {
		t.Fatalf("SkillsPlan: %v", err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("declared %d entries, want one per plugin", len(plan.Entries))
	}

	first := plan.Entries[0]
	if first.Op != OpDeliverTree {
		t.Errorf("op = %d, want OpDeliverTree", first.Op)
	}
	if first.Capability != PluginSkills {
		t.Errorf("capability = %s, want plugin-skills", first.Capability)
	}
	// The layout is the measured one: a plugin tree at .codex/skills/<plugin>
	// resolves under the <plugin>:<skill> namespace with no rewriting.
	want := filepath.Join(repo, ".codex", "skills", "shirabe")
	if first.Path != want {
		t.Errorf("path = %s, want %s", first.Path, want)
	}
	if first.Source != "/instance/.niwa/marketplaces/tools/shirabe" {
		t.Errorf("source = %s, want the resolved plugin tree", first.Source)
	}
	if first.Pre != IfSourceExists {
		t.Errorf("precondition = %d, want IfSourceExists", first.Pre)
	}
	if first.Owner == "" {
		t.Error("no owner line; the copy fallback could not recognize its own delivery")
	}
	if first.Managed {
		t.Error("the delivery is Managed; the managed-file record cannot hash a tree")
	}
	if first.ExcludeAs != ".codex/skills/shirabe" {
		t.Errorf("ExcludeAs = %q, want the delivered name relative to the working tree", first.ExcludeAs)
	}
}

// TestSkillsPlanCarriesExcludeCoverageForEveryDelivery is the property that
// keeps a delivered name from making a working tree read dirty -- which is what
// stops a worktree teardown from reclaiming its worktree.
func TestSkillsPlanCarriesExcludeCoverageForEveryDelivery(t *testing.T) {
	repo := "/instance/public/app"
	plan, err := For(agent.AgentCodex).SkillsPlan(SkillsInputs{
		Dir: repo,
		Plugins: []PluginTree{
			{Name: "a", Root: "/trees/a"},
			{Name: "b", Root: "/trees/b"},
		},
	})
	if err != nil {
		t.Fatalf("SkillsPlan: %v", err)
	}
	for _, e := range plan.Entries {
		if e.ExcludeAs == "" {
			t.Errorf("%s carries no git-exclude pattern", e.Path)
			continue
		}
		if strings.HasPrefix(e.ExcludeAs, "/") || strings.Contains(e.ExcludeAs, "..") {
			t.Errorf("%s has exclude pattern %q, want a path relative to the working tree", e.Path, e.ExcludeAs)
		}
	}
}

func TestSkillsPlanSkipsUnresolvedAndDuplicatePlugins(t *testing.T) {
	plan, err := For(agent.AgentCodex).SkillsPlan(SkillsInputs{
		Dir: "/instance/public/app",
		Plugins: []PluginTree{
			{Name: "a", Root: "/trees/a"},
			{Name: "", Root: "/trees/nameless"},
			{Name: "b", Root: ""},
			{Name: "a", Root: "/trees/a-again"},
		},
	})
	if err != nil {
		t.Fatalf("SkillsPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("declared %d entries, want only the one resolvable plugin", len(plan.Entries))
	}
	if plan.Entries[0].Source != "/trees/a" {
		t.Errorf("source = %s, want the first resolution of the name", plan.Entries[0].Source)
	}
}

// TestSkillsPlanRefusesANameThatIsNotOnePathElement covers the one place a
// configured plugin's name reaches the filesystem. A name carrying a separator
// or a parent reference would place a delivery outside the directory the
// workspace declared.
func TestSkillsPlanRefusesANameThatIsNotOnePathElement(t *testing.T) {
	for _, name := range []string{"..", ".", "a/b", `a\b`, "../../etc", "/abs"} {
		plan, err := For(agent.AgentCodex).SkillsPlan(SkillsInputs{
			Dir:     "/instance/public/app",
			Plugins: []PluginTree{{Name: name, Root: "/trees/a"}},
		})
		if err != nil {
			t.Fatalf("SkillsPlan(%q): %v", name, err)
		}
		if len(plan.Entries) != 0 {
			t.Errorf("name %q produced entry at %s, want no delivery", name, plan.Entries[0].Path)
		}
		spec := For(agent.AgentCodex).SkillsReconcileSpec(SkillsInputs{
			Dir:     "/instance/public/app",
			Plugins: []PluginTree{{Name: name, Root: "/trees/a"}},
		})
		if len(spec.Keep) != 0 {
			t.Errorf("name %q survives into the desired set as %v", name, spec.Keep)
		}
	}
}

// TestSkillsPlanIsEmptyForAnAgentWhoseSkillsArriveOtherwise pins the shape of
// the Claude answer. Row 5 is implemented for Claude and always has been --
// through marketplace registration and its own plugin system -- so a tree
// delivered beside its settings would be bytes nothing reads.
func TestSkillsPlanIsEmptyForAnAgentWhoseSkillsArriveOtherwise(t *testing.T) {
	plan, err := For(agent.AgentClaude).SkillsPlan(SkillsInputs{
		Dir:     "/instance/public/app",
		Plugins: []PluginTree{{Name: "a", Root: "/trees/a"}},
	})
	if err != nil {
		t.Fatalf("SkillsPlan: %v", err)
	}
	if len(plan.Entries) != 0 {
		t.Errorf("declared %d entries for an agent that takes no delivered trees", len(plan.Entries))
	}

	spec := For(agent.AgentClaude).SkillsReconcileSpec(SkillsInputs{Dir: "/instance/public/app"})
	if spec.Dir != "" {
		t.Errorf("reconcile spec names %s; there is nothing to reconcile", spec.Dir)
	}
}

func TestSkillsReconcileSpecNamesTheDirectoryAndTheDesiredSet(t *testing.T) {
	repo := "/instance/public/app"
	spec := For(agent.AgentCodex).SkillsReconcileSpec(SkillsInputs{
		Dir: repo,
		Plugins: []PluginTree{
			{Name: "a", Root: "/trees/a"},
			{Name: "gone", Root: ""},
		},
	})
	if spec.Dir != filepath.Join(repo, ".codex", "skills") {
		t.Errorf("spec.Dir = %s, want the skills directory", spec.Dir)
	}
	if len(spec.Keep) != 1 || spec.Keep[0] != "a" {
		t.Errorf("spec.Keep = %v, want only the resolvable plugin", spec.Keep)
	}
	if spec.Marker == "" {
		t.Error("spec carries no marker; a delivered copy could not be told from foreign content")
	}
}
