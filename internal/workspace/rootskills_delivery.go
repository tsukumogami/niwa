package workspace

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// This file is the instance-root half of the plugin skills delivery. Its
// repository half is InstallRepoSkills in pluginskills.go, and the two are
// deliberately separate call paths for the reason internal/agentplan's
// rootskills.go states at length: they answer to two capabilities, and one
// function serving both would make one row's state unobservable.
//
// What they share is the resolution. The configured plugins are resolved to
// trees once per apply, and both deliveries are made from that same set --
// resolution reads marketplace manifests and fetches remote content, and doing
// that a second time for the root would put a second network round trip in
// front of every apply for bytes already on disk.
//
// "Root" here is the instance root, and writeRootSkills in root_materializer.go
// is a different thing wearing a similar name: that one writes niwa's own
// embedded skills to the workspace root, the directory above the instances.
// This one delivers the workspace's configured plugin trees into one instance.

// RootSkillsMaterializer delivers the workspace's resolved plugin trees into
// the instance root's skills directory, for one agent.
//
// It is a materializer rather than a function so the capability contract can
// name it: the declaration table says which agent receives root-installed
// skills, the binding says which delivery serves that row, and the registry in
// delivery_binding.go says what the delivery is. A function would deliver the
// same bytes with nothing tying it to the row it answers for.
//
// It never learns which agent it is delivering for. The producer decides the
// layout, whether anything is delivered at all, and which names belong in the
// directory afterwards -- which is why the Codex skills path appears nowhere in
// this package.
type RootSkillsMaterializer struct {
	// Plugins are the resolved plugin trees, as the apply resolved them once
	// for every delivery it makes.
	Plugins []agentplan.PluginTree

	// Producer is this agent's producer, already gated on the workspace-level
	// enablement lookup. The instance root belongs to no repository, so the
	// gate that applies is the one asked with no repository name.
	Producer agentplan.Producer

	// Warnings is what the delivery has to tell the user, appended across
	// calls: an entry in the skills directory niwa did not put there, and a
	// configured plugin refused at the root because niwa's own tree takes its
	// name.
	//
	// It is a field rather than a return value because the Materializer
	// interface returns written paths and an error, and neither of those is
	// what a warning is. The caller reads it after Materialize returns, which
	// is safe because the pipeline builds one of these per agent per apply.
	Warnings []string
}

// Name is the delivery name the contract binds this materializer under.
func (m *RootSkillsMaterializer) Name() string { return string(agentplan.DeliveryRootSkills) }

// Materialize reconciles the instance root's skills directory against the
// configured plugins and delivers what belongs there.
//
// The order is reconcile-then-deliver, matching InstallRepoSkills: removing
// what no longer belongs before writing what does is what makes three applies
// leave the same set as one. The reconcile spec's Keep set is the producer's,
// and it carries niwa's own tree name wherever that tree is delivered, so this
// pass never sweeps a delivery the niwa-plugin procedure made into the same
// directory.
//
// ctx.RepoDir is the instance root. The field is named for the repositories
// most materializers write into; what it means here is the directory being
// materialized, which is what the root settings materializer already reads it
// as.
func (m *RootSkillsMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	in := agentplan.RootSkillsInputs{Dir: ctx.RepoDir, Plugins: m.Plugins}

	warnings, err := reconcileSkillsDir(m.Producer.RootSkillsReconcileSpec(in))
	if err != nil {
		return nil, err
	}
	m.Warnings = append(m.Warnings, warnings...)

	// No containment pass over the plan, for the reason InstallRepoSkills
	// states: a delivery is a link out of the receiving tree by design, so a
	// check that resolves symlinks would read every successful second apply as
	// an escape. A delivery name being a single path element is what keeps the
	// write inside the directory, and the producer applies that rule where it
	// builds the path.
	plan, err := m.Producer.RootSkillsPlan(in)
	if err != nil {
		return nil, err
	}
	m.Warnings = append(m.Warnings, plan.Warnings...)

	written, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, err
	}

	// The root plan declares no ExcludeAs, so there is nothing here to hand to
	// a git-exclude writer -- and that is the point rather than an omission.
	// The instance root is not a working tree, and the helper that consumes
	// exclude patterns searches upward for an enclosing repository, so a
	// pattern from here would land in the exclude file of whatever repository
	// happens to contain the instance. What the instance root needs is covered
	// by EnsureInstanceGitignore, which writes at the root itself. A
	// non-empty set means the producer grew coverage this executor cannot
	// deliver honestly, so it fails rather than dropping it silently.
	if len(excludes) > 0 {
		return nil, fmt.Errorf("the instance-root skills delivery declared git-exclude coverage (%v), which only a working tree can carry", excludes)
	}

	return written, nil
}
