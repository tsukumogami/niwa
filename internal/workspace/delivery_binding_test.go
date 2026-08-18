package workspace

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestDeliveriesMatchTheBindings checks the second hop of the binding in both
// directions: every delivery the contract names is registered here, and every
// registration here is named by the contract. The first hop -- that each
// binding matches an implemented declaration -- is checked in internal/agentplan,
// and the two together are what make an implemented capability traceable to the
// code that delivers it.
func TestDeliveriesMatchTheBindings(t *testing.T) {
	named := map[agentplan.Delivery]bool{}
	for _, b := range agentplan.Bindings() {
		named[b.Delivery] = true
		if _, ok := deliveries[b.Delivery]; !ok {
			t.Errorf("the contract binds (%s, %s) to delivery %q, which nothing in internal/workspace registers", b.Capability, b.Agent, b.Delivery)
		}
	}
	for name := range deliveries {
		if !named[name] {
			t.Errorf("delivery %q is registered but no declaration is bound to it", name)
		}
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
