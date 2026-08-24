package agentplan

import (
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the instance-root half of plugin skills, and the reason it is a
// separate producer method rather than a scope field on the repository one is
// worth stating where a reader meets it.
//
// The contract already carries two capabilities here, not one: row 5
// (PluginSkills) is what a session inside a cloned repository receives, and row
// 18 (RootProjectSkills) is what a session started at the instance root
// receives. They are declared independently, so a delivery that served both
// through a single gate would make one row's state unobservable -- flipping
// row 18 would silently change what row 5 delivers, and neither test could tell
// the two apart.
//
// The shape follows the precedent orientation already set in context.go, where
// RootContextPlan sits beside RepoContextPlan and WorktreeContextPlan: one
// method per capability, sharing the low-level path helper rather than a scope
// parameter. The alternative pattern in this package -- one capability with an
// internal scope-keyed layout, which is how the payload document works -- has
// already produced declarations that overclaim, because rows 8, 9 and 12 read
// implemented for Codex with no caveat while PayloadPlan only ever fires inside
// a repository for that agent. Repeating it here would repeat that.
//
// What does NOT differ is the delivered layout. Root and repository deliveries
// both land at the agent's own skillsDir, both deliver whole plugin trees, and
// both were measured to resolve with the same `<plugin>:<skill>` namespace
// (docs/spikes/SPIKE-codex-discovery-mechanics.md, finding 5 and its fifth-pass
// amendment). The root delivery is the same bytes one directory higher; what
// makes it a different capability is who receives it, not what it looks like.

// NiwaPluginTreeName is the delivery name niwa's own plugin takes inside a
// skills directory, and the name a configured plugin may not quietly take from
// it.
//
// It is exported because two rules read it and they must not drift: the root
// plan refuses a configured plugin that would land on this name, and the root
// reconcile keeps this name so a delivery row 19 made is never swept by row
// 18's cleanup. A literal in either place would let one of them move alone.
const NiwaPluginTreeName = "niwa"

// RootSkillsInputs is one instance-root skills delivery: the root directory a
// session started there reads from, and the plugins that belong in it.
//
// It is a distinct type from SkillsInputs rather than a reuse, for the reason
// the file header gives: the two deliveries answer to two capabilities, and a
// shared input type invites a shared gate. The fields are deliberately the
// same, because the delivered layout is the same.
type RootSkillsInputs struct {
	// Dir is the absolute instance root -- a directory niwa owns outright,
	// which is not a repository and holds no project-root marker.
	Dir string

	// Plugins are the resolved plugin trees, in the order the caller resolved
	// them.
	Plugins []PluginTree
}

// NiwaPluginInputs is the delivery of niwa's own plugin into one skills
// directory.
type NiwaPluginInputs struct {
	// Dir is the absolute root of the tree receiving the delivery.
	Dir string

	// Source is the absolute path of the materialized niwa plugin tree. The
	// caller materializes it; this package only says where it lands, because
	// this package never touches the filesystem.
	Source string
}

// RootSkillsPlan declares one tree delivery per resolved plugin at the instance
// root.
//
// Every entry is tagged RootProjectSkills rather than PluginSkills. That tag is
// the whole of what makes row 18's delivery distinguishable from row 5's: the
// plan-shape check asserts an entry's capability is declared implemented for
// its agent, and the binding check asks whether an implemented capability has
// anything behind it. Tagging both deliveries with row 5's capability would
// answer both questions for row 18 without delivering it.
//
// The collision rule is the second thing happening here. niwa's own plugin
// takes the delivery name NiwaPluginTreeName, and a workspace is free to
// configure a plugin called the same thing. Whichever write landed last would
// win silently, so the configured plugin is skipped and the refusal is reported
// -- naming both sources, because a developer who configured that plugin needs
// to know which one they are looking at. The refusal is conditional on row 19
// actually being implemented for this agent: where niwa delivers no plugin of
// its own there is nothing to collide with, and skipping the configured one
// would be a refusal protecting nothing.
func (p Producer) RootSkillsPlan(in RootSkillsInputs) (*Plan, error) {
	ok, err := p.delivers(RootProjectSkills)
	if err != nil {
		return nil, err
	}
	skills := p.skillsDir(in.Dir)
	if !ok || skills == "" {
		return &Plan{}, nil
	}

	niwaDelivers, err := p.delivers(NiwaPlugin)
	if err != nil {
		return nil, err
	}

	plan := &Plan{}
	seen := map[string]bool{}
	for _, pt := range in.Plugins {
		if !deliverableName(pt.Name) || pt.Root == "" || seen[pt.Name] {
			continue
		}
		if niwaDelivers && pt.Name == NiwaPluginTreeName {
			plan.Warnings = append(plan.Warnings, "the configured plugin \""+NiwaPluginTreeName+"\" ("+pt.Root+") is not delivered at the instance root: that name carries niwa's own plugin there. Its per-repository delivery is unaffected.")
			continue
		}
		seen[pt.Name] = true

		// No ExcludeAs, and the omission is deliberate rather than an
		// oversight of the repository plan's shape. That field feeds
		// git-exclude coverage for a path inside a working tree, and the
		// instance root is not one -- it is a directory niwa owns outright,
		// with no repository of its own. The helper that consumes the pattern
		// searches upward for an enclosing repository, so aiming it at a root
		// that happens to sit inside somebody's checkout would write into
		// that repository's exclude file: coverage niwa was never asked for,
		// in a tree it does not own. The instance root has its own narrower
		// mechanism for the files that do need hiding, and a delivered tree
		// of symlinks to already-installed plugin content is not among them.
		path := filepath.Join(skills, pt.Name)
		plan.Entries = append(plan.Entries, Entry{
			Capability: RootProjectSkills,
			Op:         OpDeliverTree,
			Path:       path,
			Source:     pt.Root,
			Mode:       skillsTreeMode,
			Owner:      generationMarker,
			Pre:        IfSourceExists,
		})
	}

	return plan, nil
}

// RootSkillsReconcileSpec says what the caller must clean out of the instance
// root's skills directory: the directory, the names that belong in it, and the
// marker identifying a delivery as niwa's own.
//
// The Keep set carries NiwaPluginTreeName whenever row 19 is implemented, and
// that is not a convenience. Row 19's delivery lands in this same directory
// through a different plan, so a Keep set built only from the configured
// plugins would describe niwa's own tree as de-configured and remove it on
// every apply -- the two rows would fight, and row 18 would win by running
// second.
//
// A closed gate reconciles nothing, for the reason SkillsReconcileSpec
// documents: it stops the delivery, and an empty Keep set would turn that into
// a removal of what an earlier apply delivered.
func (p Producer) RootSkillsReconcileSpec(in RootSkillsInputs) SkillsReconcileSpec {
	skills := p.skillsDir(in.Dir)
	if skills == "" || p.gateClosed {
		return SkillsReconcileSpec{}
	}

	spec := SkillsReconcileSpec{Dir: skills, Marker: generationMarker}
	for _, pt := range in.Plugins {
		if !deliverableName(pt.Name) || pt.Root == "" {
			continue
		}
		if pt.Name == NiwaPluginTreeName {
			// Skipped rather than kept: the entry below adds the name once
			// when row 19 delivers it, and adding it here too would keep a
			// name this agent may not deliver at all.
			continue
		}
		spec.Keep = append(spec.Keep, pt.Name)
	}

	if niwaDelivers, err := p.delivers(NiwaPlugin); err == nil && niwaDelivers {
		spec.Keep = append(spec.Keep, NiwaPluginTreeName)
	}
	return spec
}

// NiwaPluginPlan declares the delivery of niwa's own plugin tree into one
// skills directory.
//
// It is one entry, tagged NiwaPlugin, at the agent's own skills layout. The
// source is handed in rather than computed: the tree is embedded in the binary
// and has to reach disk before a link can name it, and putting it there is
// filesystem work this package does not do.
func (p Producer) NiwaPluginPlan(in NiwaPluginInputs) (*Plan, error) {
	ok, err := p.delivers(NiwaPlugin)
	if err != nil {
		return nil, err
	}
	skills := p.skillsDir(in.Dir)
	if !ok || skills == "" || in.Source == "" {
		return &Plan{}, nil
	}

	return &Plan{Entries: []Entry{{
		Capability: NiwaPlugin,
		Op:         OpDeliverTree,
		Path:       filepath.Join(skills, NiwaPluginTreeName),
		Source:     in.Source,
		Mode:       skillsTreeMode,
		Owner:      generationMarker,
		Pre:        IfSourceExists,
	}}}, nil
}

// ConfigDocRepoScoped reports whether this agent reads its generated
// configuration document only from inside a cloned repository.
//
// It exists because something outside this package needs to say "the
// configuration half of the project layer does not reach a session started at
// the instance root" and there is no declaration row that means it. The
// contract's rows are scoped by who receives a capability and never by where
// from, which is a deliberate schema decision rather than an oversight -- so
// the honest gate for that sentence is the layout table itself, read through a
// predicate, rather than a row invented to carry it or the agent's name spelled
// out at the call site.
//
// An agent with no payload layout at all returns false: it reads no generated
// configuration document anywhere, so there is no scope for one to be confined
// to, and reporting true would make a caller warn about a document that does
// not exist for it.
func ConfigDocRepoScoped(ag agent.Agent) bool {
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return false
	}
	layout, ok := payloadLayouts[resolved]
	if !ok {
		return false
	}
	return layout.scope == PayloadInRepo
}
