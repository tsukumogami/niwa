package agentplan

import (
	"slices"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// TestBindingsMatchTheirDeclarations checks the binding in both drift
// directions over the bound capabilities. A declaration with nothing behind it
// and a delivery nobody declared are the two ways this table turns into
// fiction, and neither is visible from either side alone.
func TestBindingsMatchTheirDeclarations(t *testing.T) {
	bound := BoundCapabilities()

	seen := map[Capability]map[agent.Agent]bool{}
	for _, b := range Bindings() {
		if !slices.Contains(bound, b.Capability) {
			t.Errorf("(%s, %s) is bound to delivery %q, but %s is not a bound capability", b.Capability, b.Agent, b.Delivery, b.Capability)
			continue
		}
		if b.Delivery == "" {
			t.Errorf("(%s, %s) is bound to the empty delivery", b.Capability, b.Agent)
		}
		if seen[b.Capability][b.Agent] {
			t.Errorf("(%s, %s) is bound twice", b.Capability, b.Agent)
		}
		if seen[b.Capability] == nil {
			seen[b.Capability] = map[agent.Agent]bool{}
		}
		seen[b.Capability][b.Agent] = true

		d, err := Lookup(b.Capability, b.Agent)
		if err != nil {
			t.Errorf("(%s, %s) is bound to delivery %q, but the table cannot answer for it: %v", b.Capability, b.Agent, b.Delivery, err)
			continue
		}
		if d.State != StateImplemented {
			t.Errorf("(%s, %s) is bound to delivery %q but is not declared implemented; something delivers a capability nobody declared", b.Capability, b.Agent, b.Delivery)
		}
	}

	for _, c := range bound {
		for _, ag := range agent.All() {
			d, err := Lookup(c, ag)
			if err != nil {
				t.Errorf("Lookup(%s, %s) unexpected error: %v", c, ag, err)
				continue
			}
			switch {
			case d.State == StateImplemented && !seen[c][ag]:
				t.Errorf("(%s, %s) is declared implemented with no delivery bound to it", c, ag)
			case d.State != StateImplemented && seen[c][ag]:
				t.Errorf("(%s, %s) is not implemented yet carries a binding", c, ag)
			}
		}
	}
}

// TestBoundCapabilitiesAreInTheClosedSet keeps the bound set from naming a
// capability the catalog does not carry, which would make the binding checks
// range over a row that cannot be declared.
func TestBoundCapabilitiesAreInTheClosedSet(t *testing.T) {
	all := All()
	for _, c := range BoundCapabilities() {
		if !slices.Contains(all, c) {
			t.Errorf("bound capability %s is outside the closed set", c)
		}
	}
}

// TestBindingAccessorsReturnFreshSlices matches the posture All() takes: a
// caller that iterates the bound set cannot shrink what everyone else checks.
func TestBindingAccessorsReturnFreshSlices(t *testing.T) {
	first := BoundCapabilities()
	first[0] = Capability(0)
	if BoundCapabilities()[0] == Capability(0) {
		t.Error("BoundCapabilities() handed out the package's own slice")
	}

	bindings := Bindings()
	bindings[0].Delivery = ""
	if Bindings()[0].Delivery == "" {
		t.Error("Bindings() handed out the package's own slice")
	}
}
