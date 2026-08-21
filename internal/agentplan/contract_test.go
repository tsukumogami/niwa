package agentplan

import (
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// The declaration suite is a table test in the literal sense: the contract is
// data, and these assertions are the whole of what makes it trustworthy. They
// touch no temporary directory, no filesystem, and no built binary, so a
// capability that is claimed but not delivered fails in milliseconds with the
// pair's name in the message rather than as a missing file in a tree diff.

// TestDeclarationTableCoversEveryPairExactlyOnce is the exhaustiveness check.
// Every capability in the closed set has an answer for every agent, exactly one
// answer, and no answer for anything outside the two closed sets. Deleting a
// single row fails it; adding a second row for a pair fails it; adding a
// capability to the catalog without declaring it fails it.
func TestDeclarationTableCoversEveryPairExactlyOnce(t *testing.T) {
	agents := agent.All()
	capabilities := All()

	counts := map[Capability]map[agent.Agent]int{}
	for _, d := range declarations {
		if _, ok := d.Capability.row(); !ok {
			t.Errorf("declaration for %s: capability is outside the closed set", d.Capability)
			continue
		}
		if !slices.Contains(agents, d.Agent) {
			t.Errorf("declaration for %s: agent %q is outside the accepted set", d.Capability, d.Agent)
			continue
		}
		if counts[d.Capability] == nil {
			counts[d.Capability] = map[agent.Agent]int{}
		}
		counts[d.Capability][d.Agent]++
	}

	for _, c := range capabilities {
		for _, ag := range agents {
			switch n := counts[c][ag]; {
			case n == 0:
				t.Errorf("no declaration for (%s, %s): the contract has no answer for a pair it must answer for", c, ag)
			case n > 1:
				t.Errorf("%d declarations for (%s, %s): the pair has more than one answer", n, c, ag)
			}
		}
	}

	if want := len(capabilities) * len(agents); len(declarations) != want {
		t.Errorf("the table has %d rows, want %d (%d capabilities x %d agents)", len(declarations), want, len(capabilities), len(agents))
	}
}

// TestDeclarationsAreWellFormed asserts each row says exactly as much as its
// state permits. An unavailable row without a reason is a gap nobody has to
// explain; an implemented row carrying one is a claim and an excuse at once.
func TestDeclarationsAreWellFormed(t *testing.T) {
	for _, d := range declarations {
		switch d.State {
		case StateImplemented:
			if d.Kind != 0 {
				t.Errorf("(%s, %s) is implemented and carries reason kind %d; both reason fields must be zero", d.Capability, d.Agent, d.Kind)
			}
			if d.Reason != "" {
				t.Errorf("(%s, %s) is implemented and carries reason %q; both reason fields must be zero", d.Capability, d.Agent, d.Reason)
			}
		case StateUnavailable:
			switch d.Kind {
			case ReasonAgentCannotReceive, ReasonNoSuchConcept, ReasonNotBuilt:
			default:
				t.Errorf("(%s, %s) is unavailable with reason kind %d, outside the closed set", d.Capability, d.Agent, d.Kind)
			}
			if d.Reason == "" {
				t.Errorf("(%s, %s) is unavailable with no reason; an unexplained gap is how a contract turns into a wish", d.Capability, d.Agent)
			}
			if len(d.Requires) > 0 {
				t.Errorf("(%s, %s) is unavailable and names %d requirement(s); Requires is legal only when implemented", d.Capability, d.Agent, len(d.Requires))
			}
		default:
			t.Errorf("(%s, %s) has state %d, outside the closed set", d.Capability, d.Agent, d.State)
		}
	}
}

// TestRequiresIsClosed asserts every capability a row depends on is itself
// delivered to the same agent. This is what makes the two-state model honest:
// "implemented, but only when something else holds" is expressible only as an
// edge to a capability niwa also delivers, so a real gap has nowhere to hide.
func TestRequiresIsClosed(t *testing.T) {
	for _, d := range declarations {
		for _, req := range d.Requires {
			required, err := Lookup(req, d.Agent)
			if err != nil {
				t.Errorf("(%s, %s) requires %s, which the table cannot answer for: %v", d.Capability, d.Agent, req, err)
				continue
			}
			if required.State != StateImplemented {
				t.Errorf("(%s, %s) is implemented but requires %s, which is not implemented for %s", d.Capability, d.Agent, req, d.Agent)
			}
			if req == d.Capability {
				t.Errorf("(%s, %s) requires itself", d.Capability, d.Agent)
			}
		}
	}
}

// TestHookDeliveredRowsRestOnTheHooksRow binds the reason of every row that is
// delivered through hooks to the reason hooks themselves are out of reach.
//
// Which rows those are is not written here. A capability is hook-delivered
// because some agent's implemented declaration says it is -- Requires names
// Hooks -- so the set is read off the table and grows on its own when a fourth
// such row is declared. For an agent whose Hooks row is unavailable, three
// things follow and are asserted: the row cannot be implemented, its reason
// kind is the one the Hooks row already carries rather than a second opinion
// about the same obstacle, and its reason says so in words a reader of the
// generated guide meets on its own, without the Hooks entry beside it.
//
// This is a guard rather than a proof, and it is worth being plain about the
// difference. It does not fail on a row that is merely unavailable for the
// wrong reason; it fails on one whose reason has drifted off the obstacle
// entirely -- which is what row 17's did. Its reason cited "the harness
// job-state file" alongside the hook, a mechanism belonging to the dispatch
// path, which is a different row, is implemented for both agents, and reads
// session records this agent demonstrably has (TestLaunchSpecsAreComplete
// walks the description Codex declares for them). Keeping the hook as the
// stated obstacle is what stops the sentence the guide publishes from
// answering a question this row does not own.
func TestHookDeliveredRowsRestOnTheHooksRow(t *testing.T) {
	hookDelivered := map[Capability]bool{}
	for _, d := range declarations {
		if d.State != StateImplemented {
			continue
		}
		if slices.Contains(d.Requires, Hooks) {
			hookDelivered[d.Capability] = true
		}
	}
	if len(hookDelivered) == 0 {
		t.Fatal("no capability declares Hooks in Requires, so this check is asserting nothing; if hook delivery stopped being expressed as a Requires edge, this test has to follow it")
	}

	for _, ag := range agent.All() {
		hooks, err := Lookup(Hooks, ag)
		if err != nil {
			t.Errorf("(%s, %s): %v", Hooks, ag, err)
			continue
		}
		if hooks.State == StateImplemented {
			continue
		}

		for c := range hookDelivered {
			d, err := Lookup(c, ag)
			if err != nil {
				t.Errorf("(%s, %s): %v", c, ag, err)
				continue
			}
			if d.State == StateImplemented {
				t.Errorf("(%s, %s) is implemented, but it is delivered through %s, which is unavailable for %s", c, ag, Hooks, ag)
				continue
			}
			if d.Kind != hooks.Kind {
				t.Errorf("(%s, %s) is unavailable with reason kind %d while %s is unavailable for %s with kind %d: a row blocked only by the hook it rides inherits that row's kind rather than declaring a different one", c, ag, d.Kind, Hooks, ag, hooks.Kind)
			}
			if !strings.Contains(strings.ToLower(d.Reason), "hook") {
				t.Errorf("(%s, %s) is unavailable and delivered through %s, but its reason never says so: %q", c, ag, Hooks, d.Reason)
			}
		}
	}
}

// TestRequiresGraphIsAcyclic asserts the requirement edges form a graph that
// can actually be walked. A cycle would let a set of capabilities vouch for
// each other while nothing underneath them is delivered.
func TestRequiresGraphIsAcyclic(t *testing.T) {
	for _, ag := range agent.All() {
		edges := map[Capability][]Capability{}
		for _, d := range declarations {
			if d.Agent == ag {
				edges[d.Capability] = d.Requires
			}
		}

		// Standard three-colour depth-first search: a capability on the
		// current path that is reached again closes a cycle.
		const (
			unvisited = 0
			onPath    = 1
			done      = 2
		)
		state := map[Capability]int{}
		var path []Capability
		var walk func(c Capability) []Capability
		walk = func(c Capability) []Capability {
			switch state[c] {
			case onPath:
				return append(slices.Clone(path), c)
			case done:
				return nil
			}
			state[c] = onPath
			path = append(path, c)
			for _, req := range edges[c] {
				if cycle := walk(req); cycle != nil {
					return cycle
				}
			}
			path = path[:len(path)-1]
			state[c] = done
			return nil
		}

		for _, c := range All() {
			if cycle := walk(c); cycle != nil {
				names := make([]string, len(cycle))
				for i, member := range cycle {
					names[i] = member.String()
				}
				t.Fatalf("the requirement graph for %s has a cycle: %v", ag, names)
			}
		}
	}
}
