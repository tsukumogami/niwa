package agentplan

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// TestGatedProducerDeclaresNothing asserts the gate's structural property: a
// closed gate empties every plan the producer declares, so the executor
// downstream never sees an entry it has to decide about. The plans are asked
// for one by one rather than through a helper, because the point is that no
// producer method has its own opinion about the gate.
func TestGatedProducerDeclaresNothing(t *testing.T) {
	for _, ag := range agent.All() {
		t.Run(string(ag), func(t *testing.T) {
			p := For(ag).Gated(false)

			plans := map[string]func() (*Plan, error){
				"root": func() (*Plan, error) {
					return p.RootContextPlan(RootContextInputs{Dir: "/w", Body: []byte("x"), HasBody: true})
				},
				"group": func() (*Plan, error) {
					return p.GroupContextPlan(RootContextInputs{Dir: "/w/g", Body: []byte("x"), HasBody: true})
				},
				"repo": func() (*Plan, error) {
					return p.RepoContextPlan(RepoContextInputs{Dir: "/w/g/r", Body: []byte("x"), HasBody: true})
				},
				"worktree": func() (*Plan, error) {
					return p.WorktreeContextPlan(WorktreeContextInputs{Dir: "/w/t", Heading: "## H", Body: []byte("x")})
				},
				"skills": func() (*Plan, error) {
					return p.SkillsPlan(SkillsInputs{Dir: "/w/g/r", Plugins: []PluginTree{{Name: "shirabe", Root: "/src"}}})
				},
				"payload": func() (*Plan, error) {
					return p.PayloadPlan(PayloadInputs{
						Scope: PayloadInRepo,
						Dir:   "/w/g/r",
						Env:   map[string]string{"K": "v"},
					})
				},
			}

			for name, produce := range plans {
				plan, err := produce()
				if err != nil {
					t.Fatalf("%s plan: %v", name, err)
				}
				if len(plan.Entries) != 0 {
					t.Errorf("%s plan declared %d entries behind a closed gate", name, len(plan.Entries))
				}
			}

			if spec := p.SkillsReconcileSpec(SkillsInputs{Dir: "/w/g/r"}); spec.Dir != "" {
				t.Errorf("a closed gate reconciles nothing, got spec for %s", spec.Dir)
			}
		})
	}
}

// TestGateDoesNotChangeTheDeclarationTable keeps the gate and the table
// separate. A gate is a workspace's choice for one scope; a declaration is a
// fact about an agent. Folding one into the other is how the gap list would
// start reporting "unavailable" for a capability the workspace merely turned
// off.
func TestGateDoesNotChangeTheDeclarationTable(t *testing.T) {
	for _, ag := range agent.All() {
		for _, c := range All() {
			before, err := Lookup(c, ag)
			if err != nil {
				t.Fatalf("Lookup(%s, %s): %v", c, ag, err)
			}
			_ = For(ag).Gated(false)
			after, err := Lookup(c, ag)
			if err != nil {
				t.Fatalf("Lookup(%s, %s) after gating: %v", c, ag, err)
			}
			if before.State != after.State {
				t.Errorf("gating changed the declared state of %s for %s", c, ag)
			}
		}
	}
}

// TestOpenGateMatchesUngated pins the other half: Gated(true) is the same
// producer as the ungated one, so threading a gate through a call site that
// always resolves true cannot change what is delivered.
func TestOpenGateMatchesUngated(t *testing.T) {
	for _, ag := range agent.All() {
		in := RepoContextInputs{Dir: "/w/g/r", Body: []byte("body"), HasBody: true}

		plain, err := For(ag).RepoContextPlan(in)
		if err != nil {
			t.Fatalf("ungated plan for %s: %v", ag, err)
		}
		open, err := For(ag).Gated(true).RepoContextPlan(in)
		if err != nil {
			t.Fatalf("open-gate plan for %s: %v", ag, err)
		}
		if len(plain.Entries) != len(open.Entries) {
			t.Errorf("%s: open gate declared %d entries, ungated declared %d", ag, len(open.Entries), len(plain.Entries))
		}
	}
}
