package agentplan

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

func rootPlugins() []PluginTree {
	return []PluginTree{
		{Name: "demo", Root: "/mkt/demo"},
		{Name: "other", Root: "/mkt/other"},
	}
}

// TestRootSkillsPlanIsInertWhileTheRowIsUnavailable is the property that lets
// the delivery land before the declaration flips. A producer gated on a
// capability the table does not declare implemented yields nothing, so the
// vocabulary can exist, be tested, and be reviewed on its own before the change
// that turns it on.
func TestRootSkillsPlanIsInertWhileTheRowIsUnavailable(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := Lookup(RootProjectSkills, ag)
		if err != nil {
			t.Fatalf("Lookup(root-project-skills, %s): %v", ag, err)
		}
		plan, err := For(ag).RootSkillsPlan(RootSkillsInputs{Dir: "/instance", Plugins: rootPlugins()})
		if err != nil {
			t.Fatalf("RootSkillsPlan(%s): %v", ag, err)
		}
		if d.State != StateImplemented && len(plan.Entries) != 0 {
			t.Errorf("RootSkillsPlan(%s) produced %d entries while the row is not implemented", ag, len(plan.Entries))
		}
	}
}

// TestNiwaPluginPlanIsInertWhileTheRowIsUnavailable is the same property for
// row 19.
func TestNiwaPluginPlanIsInertWhileTheRowIsUnavailable(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := Lookup(NiwaPlugin, ag)
		if err != nil {
			t.Fatalf("Lookup(niwa-plugin, %s): %v", ag, err)
		}
		plan, err := For(ag).NiwaPluginPlan(NiwaPluginInputs{Dir: "/instance", Source: "/staged/niwa"})
		if err != nil {
			t.Fatalf("NiwaPluginPlan(%s): %v", ag, err)
		}
		if d.State != StateImplemented && len(plan.Entries) != 0 {
			t.Errorf("NiwaPluginPlan(%s) produced %d entries while the row is not implemented", ag, len(plan.Entries))
		}
	}
}

// TestRootSkillsPlanTagsItsOwnCapability is the distinction the whole
// second-method shape exists for. An entry tagged PluginSkills would answer
// row 5's binding question with row 18's delivery, which is exactly the drift
// the contract's tagging rule is meant to catch.
func TestRootSkillsPlanTagsItsOwnCapability(t *testing.T) {
	p := producerDeliveringRootSkills(t)
	plan, err := p.RootSkillsPlan(RootSkillsInputs{Dir: "/instance", Plugins: rootPlugins()})
	if err != nil {
		t.Fatalf("RootSkillsPlan: %v", err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("RootSkillsPlan produced %d entries, want 2", len(plan.Entries))
	}
	for _, e := range plan.Entries {
		if e.Capability != RootProjectSkills {
			t.Errorf("entry %s tagged %s, want %s", e.Path, e.Capability, RootProjectSkills)
		}
		if e.Op != OpDeliverTree {
			t.Errorf("entry %s has op %v, want OpDeliverTree", e.Path, e.Op)
		}
		if e.Pre != IfSourceExists {
			t.Errorf("entry %s has pre %v, want IfSourceExists", e.Path, e.Pre)
		}
		if !strings.Contains(e.Path, "/instance/") {
			t.Errorf("entry path %q is not under the instance root", e.Path)
		}
	}
}

// TestConfigDocRepoScopedReadsTheLayoutTable pins the predicate the dispatch
// warning gates on. The three cases are the three shapes the table can take,
// and the third is the one a name-based check would get wrong.
func TestConfigDocRepoScopedReadsTheLayoutTable(t *testing.T) {
	cases := []struct {
		agent agent.Agent
		want  bool
		why   string
	}{
		{agent.AgentCodex, true, "Codex's payload layout is scoped to a repository"},
		{agent.AgentClaude, false, "Claude's payload layout is scoped to the instance root"},
		{agent.Agent("nonesuch"), false, "an agent outside the accepted set reads no payload document at all"},
	}
	for _, tc := range cases {
		if got := ConfigDocRepoScoped(tc.agent); got != tc.want {
			t.Errorf("ConfigDocRepoScoped(%q) = %v, want %v -- %s", tc.agent, got, tc.want, tc.why)
		}
	}
}

// TestNiwaPluginTreeNameIsADeliverableName guards the constant against becoming
// something the delivery could not safely use as a single path element.
func TestNiwaPluginTreeNameIsADeliverableName(t *testing.T) {
	if !deliverableName(NiwaPluginTreeName) {
		t.Errorf("NiwaPluginTreeName %q is not a deliverable name", NiwaPluginTreeName)
	}
}

// TestNewDeliveryConstantsAreDistinct keeps the four names from collapsing onto
// each other or onto an existing delivery. Two capabilities bound to one name
// would make the binding registry unable to tell which delivery it registered.
func TestNewDeliveryConstantsAreDistinct(t *testing.T) {
	all := []Delivery{
		DeliveryEnv, DeliveryFiles, DeliveryHooks, DeliveryCodexTrust,
		DeliveryRootSkills, DeliveryRootSettings,
		DeliveryNiwaPluginClaude, DeliveryNiwaPluginCodex,
	}
	seen := map[Delivery]bool{}
	for _, d := range all {
		if d == "" {
			t.Error("a delivery constant is empty")
		}
		if seen[d] {
			t.Errorf("delivery %q is declared twice", d)
		}
		seen[d] = true
	}
}

// producerDeliveringRootSkills returns a producer whose agent is declared to
// deliver row 18, skipping the test when no agent does yet.
//
// The skip is what lets this file assert the delivery's shape before the flip
// without pinning which change turns it on: once a row is implemented the
// assertions run for real, and until then they report themselves as not-yet
// exercised rather than passing vacuously.
func producerDeliveringRootSkills(t *testing.T) Producer {
	t.Helper()
	for _, ag := range agent.All() {
		d, err := Lookup(RootProjectSkills, ag)
		if err != nil {
			t.Fatalf("Lookup(root-project-skills, %s): %v", ag, err)
		}
		if d.State == StateImplemented && len(For(ag).skillsLayout()) > 0 {
			return For(ag)
		}
	}
	t.Skip("no agent both delivers root-project-skills and takes delivered skills trees yet")
	return Producer{}
}
