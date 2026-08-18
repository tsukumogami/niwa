package agentplan

import (
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the producer side of plugin skills: the workspace declares a set
// of plugins, and the agent that reads skills out of a delivered tree gets one
// tree per plugin, at the layout its own discovery looks in.
//
// The measured rule this encodes (docs/spikes/SPIKE-codex-discovery-mechanics.md,
// finding 5) is narrow and worth stating: a plugin tree delivered at
// `.codex/skills/<plugin>` loads with the same `<plugin>:<skill>` namespace
// Claude Code produces, with no rewriting of skill content -- and it loads even
// from an untrusted layer, which is why this row carries no `Requires:
// DirectoryTrust` edge while the configuration keys beside it do.
//
// The unit of delivery is the whole plugin directory rather than the skill
// directories inside it. Namespacing derives from the nearest plugin manifest
// above a skill on disk, so a delivered plugin tree yields the namespaced names
// for free; copying skills loose would flatten the namespace and orphan every
// plugin-root file the skills reference.

// skillsTreeMode is the permission a delivered skills tree is created with. It
// is a directory, so it needs the execute bit to be traversable.
const skillsTreeMode = 0o755

// treeMarkerFileName is the sentinel the executor writes inside a tree it
// delivered by copying, holding the entry's Owner line.
//
// A symlink identifies itself: only niwa plants one at these names, so finding
// one is enough to know the delivery is niwa's to repair. A copy is
// indistinguishable from a directory somebody else put there, and the fallback
// has to be able to refresh its own delivery wholesale -- otherwise a plugin
// that drops a file leaves it behind forever. The sentinel is what makes the
// copy recognizable, and it is deliberately the only thing niwa adds to a tree
// it otherwise reproduces verbatim.
const treeMarkerFileName = ".niwa-delivered-tree"

// TreeMarkerFileName is the sentinel name the executor writes and reads. It is
// exported because the executor lives in internal/workspace and the name, like
// every other name in a delivered layout, is decided here.
func TreeMarkerFileName() string { return treeMarkerFileName }

// PluginTree is one configured plugin resolved to the installed tree it will be
// delivered from: the name it is delivered under, and the absolute root of the
// tree itself.
//
// Resolving Root is the caller's job and deliberately so -- it involves reading
// a marketplace manifest, and for a remote marketplace fetching its content --
// but where the tree lands is decided here.
type PluginTree struct {
	// Name is the plugin's own name, which is also the name the delivered tree
	// takes. The agent derives the skill namespace from the manifest inside the
	// tree rather than from this name, so it is what a human reads in the
	// layout rather than what the namespace depends on.
	Name string

	// Root is the absolute path of the whole plugin tree.
	Root string
}

// SkillsInputs is one skills delivery: the directory a session reads from, and
// the plugins that belong in it.
type SkillsInputs struct {
	// Dir is the absolute root of the tree receiving the delivery: a cloned
	// repository, or a worktree of one.
	Dir string

	// Plugins are the resolved plugin trees, in the order the caller resolved
	// them.
	Plugins []PluginTree
}

// skillsLayout is where one agent's session looks for delivered plugin trees,
// as path segments below the receiving directory.
//
// The map answers for the agents that read skills out of a delivered tree and
// for no others. An agent absent from it gets the zero layout and no entries --
// which is a statement about how its skills arrive, not about whether it has
// any. Claude Code is the case in point: its row 5 is implemented and always has
// been, through marketplace registration and its own plugin system, so a tree
// delivered beside its settings would be bytes nothing reads.
var skillsLayouts = map[agent.Agent][]string{
	agent.AgentCodex: {".codex", "skills"},
}

// skillsLayout returns this producer's agent's layout, nil when its skills do
// not arrive as delivered trees.
func (p Producer) skillsLayout() []string {
	resolved, err := agent.ParseAgent(string(p.ag))
	if err != nil {
		return nil
	}
	return skillsLayouts[resolved]
}

// skillsDir is the absolute directory the delivered trees belong in, empty when
// this agent takes none.
func (p Producer) skillsDir(dir string) string {
	layout := p.skillsLayout()
	if dir == "" || len(layout) == 0 {
		return ""
	}
	return filepath.Join(append([]string{dir}, layout...)...)
}

// SkillsPlan declares one tree delivery per resolved plugin.
//
// The entries are gated on their source existing rather than checked here: a
// plugin whose tree went missing between resolution and the write is a no-op,
// which is the same answer the caller's own missing-root report gives, and this
// package does not touch the filesystem to find out.
//
// They are deliberately not Managed. The managed-file record hashes the file it
// records, and neither a symlink to a directory nor a directory is something it
// can hash; the set is reconciled against the configured plugins instead, which
// is what SkillsReconcileSpec exists for.
func (p Producer) SkillsPlan(in SkillsInputs) (*Plan, error) {
	ok, err := p.delivers(PluginSkills)
	if err != nil {
		return nil, err
	}
	skills := p.skillsDir(in.Dir)
	if !ok || skills == "" {
		return &Plan{}, nil
	}

	plan := &Plan{}
	seen := map[string]bool{}
	for _, pt := range in.Plugins {
		if !deliverableName(pt.Name) || pt.Root == "" || seen[pt.Name] {
			continue
		}
		seen[pt.Name] = true

		path := filepath.Join(skills, pt.Name)
		plan.Entries = append(plan.Entries, Entry{
			Capability: PluginSkills,
			Op:         OpDeliverTree,
			Path:       path,
			Source:     pt.Root,
			Mode:       skillsTreeMode,
			Owner:      generationMarker,
			Pre:        IfSourceExists,
			ExcludeAs:  excludePattern(in.Dir, path),
		})
	}

	return plan, nil
}

// SkillsReconcileSpec says what the caller must clean out of the skills
// directory before -- or after -- applying the plan: the directory itself, the
// names that belong in it, and the marker that identifies a delivery as niwa's
// own.
//
// It is the same seam ContextProbeSpec is. The producer knows the layout and the
// desired set; removing a de-configured plugin's delivery is filesystem work,
// which belongs on the other side of the boundary. Without it a plugin dropped
// from the configuration would keep delivering its skills to every session
// forever, since nothing else in the pipeline tracks these paths.
//
// A zero spec means this agent takes no delivered skills and there is nothing to
// reconcile.
type SkillsReconcileSpec struct {
	// Dir is the absolute skills directory, empty when the agent takes none.
	Dir string

	// Keep are the delivery names that belong in Dir.
	Keep []string

	// Marker is the line identifying a delivered tree as niwa's own, read from
	// the sentinel TreeMarkerFileName names inside it. A symlink is niwa's by
	// shape and needs no marker.
	Marker string
}

// SkillsReconcileSpec returns what the caller must reconcile for one delivery.
func (p Producer) SkillsReconcileSpec(in SkillsInputs) SkillsReconcileSpec {
	skills := p.skillsDir(in.Dir)
	if skills == "" {
		return SkillsReconcileSpec{}
	}

	spec := SkillsReconcileSpec{Dir: skills, Marker: generationMarker}
	for _, pt := range in.Plugins {
		if !deliverableName(pt.Name) || pt.Root == "" {
			continue
		}
		spec.Keep = append(spec.Keep, pt.Name)
	}
	return spec
}

// deliverableName reports whether name is safe to use as the single directory
// element a delivery takes.
//
// The delivered path is the only place a configured plugin's name reaches the
// filesystem, and it reaches it as a path element joined onto a directory in the
// developer's tree. A name carrying a separator or a parent reference would
// place the delivery somewhere the workspace never declared -- so the check
// lives here, where the path is built, rather than as a containment test at the
// far end: the delivery is a symlink out of the tree by design, which is exactly
// the shape a resolving containment check cannot tell from an escape.
func deliverableName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`) && filepath.Base(name) == name
}
