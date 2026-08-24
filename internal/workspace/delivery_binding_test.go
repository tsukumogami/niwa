package workspace

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestDeliveriesMatchTheBindings checks the second hop of the binding in both
// directions: every delivery the contract names is registered here, and every
// registration here is named by the contract. The first hop -- that each
// binding matches an implemented declaration -- is checked in internal/agentplan,
// and the two together are what make an implemented capability traceable to the
// code that delivers it.
//
// Which registry a binding must appear in is the capability's route, not a
// choice this test makes: a procedure-routed capability registered as a
// materializer would be a side effect outside the instance pretending to be a
// write into it.
func TestDeliveriesMatchTheBindings(t *testing.T) {
	namedMaterializers := map[agentplan.Delivery]bool{}
	namedProcedures := map[agentplan.Delivery]bool{}

	for _, b := range agentplan.Bindings() {
		switch b.Capability.Route() {
		case agentplan.RouteProcedure:
			namedProcedures[b.Delivery] = true
			if _, ok := procedures[b.Delivery]; !ok {
				t.Errorf("the contract binds (%s, %s) to procedure-routed delivery %q, which nothing in internal/workspace registers", b.Capability, b.Agent, b.Delivery)
			}
		default:
			namedMaterializers[b.Delivery] = true
			if _, ok := deliveries[b.Delivery]; !ok {
				t.Errorf("the contract binds (%s, %s) to delivery %q, which nothing in internal/workspace registers", b.Capability, b.Agent, b.Delivery)
			}
		}
	}

	// A registration nothing is bound to is either dead code or a delivery
	// staged ahead of its binding, and the contract is what tells the two
	// apart: a name in agentplan.PendingDeliveries is waiting on a capability
	// the table has not bound yet, and every other unbound name is dead. The
	// pending list cannot quietly cover a name forever -- agentplan fails the
	// moment an entry's capability becomes bound, which is the change that has
	// to delete the entry.
	pending := agentplan.PendingDeliveries()
	for name := range deliveries {
		if _, staged := pending[name]; staged {
			continue
		}
		if !namedMaterializers[name] {
			t.Errorf("delivery %q is registered but no declaration is bound to it", name)
		}
	}
	for name := range procedures {
		if _, staged := pending[name]; staged {
			continue
		}
		if !namedProcedures[name] {
			t.Errorf("procedure %q is registered but no declaration is bound to it", name)
		}
	}
}

// TestRegisteredProceduresAreWhatTheyClaim is the procedure half of the
// name check below: a registration filed under a name that is not the
// procedure's own reads as correct forever.
func TestRegisteredProceduresAreWhatTheyClaim(t *testing.T) {
	for name, p := range procedures {
		if p == nil {
			t.Errorf("procedure %q is registered as nil", name)
			continue
		}
		if got := p.Name(); got != string(name) {
			t.Errorf("procedure %q is registered to the procedure named %q", name, got)
		}
	}
}

// TestProcedureForAnswersFromTheTable is the lookup the pipeline makes. It is
// what keeps directory trust from being a hardcoded second pass: the pipeline
// ranges over the agents and asks here, so a capability reaches an agent
// because the table says so and for no other reason.
func TestProcedureForAnswersFromTheTable(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := agentplan.Lookup(agentplan.DirectoryTrust, ag)
		if err != nil {
			t.Fatalf("Lookup(directory-trust, %s) unexpected error: %v", ag, err)
		}
		p, ok := procedureFor(agentplan.DirectoryTrust, ag)
		switch {
		case d.State == agentplan.StateImplemented && !ok:
			t.Errorf("directory trust is implemented for %s but no procedure answers for it", ag)
		case d.State != agentplan.StateImplemented && ok:
			t.Errorf("a procedure answers for directory trust for %s, which the table does not declare implemented", ag)
		case ok && p.Name() != string(agentplan.DeliveryCodexTrust):
			t.Errorf("directory trust for %s resolved to procedure %q", ag, p.Name())
		}
	}

	// A capability nothing registers a procedure for answers no, rather than
	// answering with whatever the map's zero value is.
	if _, ok := procedureFor(agentplan.MarketplaceRegistration, agent.AgentClaude); ok {
		t.Error("marketplace registration resolved to a procedure; it is implemented but not yet bound")
	}
}

// TestRegisteredDeliveriesAreWhatTheyClaim asserts each registration is filed
// under the materializer's own name. Without it the map is a set of assertions
// about itself, and a copy-paste that registers the wrong materializer under a
// name reads as correct forever.
func TestRegisteredDeliveriesAreWhatTheyClaim(t *testing.T) {
	for name, m := range deliveries {
		if m == nil {
			t.Errorf("delivery %q is registered as nil", name)
			continue
		}
		if got := m.Name(); got != string(name) {
			t.Errorf("delivery %q is registered to the materializer named %q", name, got)
		}
	}
}
