package agentplan

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

func TestAllIsTheClosedSet(t *testing.T) {
	all := All()
	if len(all) != 24 {
		t.Fatalf("All() returned %d capabilities, want the matrix's 24", len(all))
	}
	seen := map[Capability]bool{}
	for _, c := range all {
		if seen[c] {
			t.Fatalf("capability %s appears twice in the catalog", c)
		}
		seen[c] = true
		if c.String() == "" {
			t.Fatalf("capability %d has no name", uint8(c))
		}
		switch c.Route() {
		case RoutePlan, RouteProcedure, RouteLaunch:
		default:
			t.Fatalf("capability %s has route %d, outside the closed set", c, c.Route())
		}
	}
}

func TestAllReturnsAFreshSlice(t *testing.T) {
	first := All()
	first[0] = Capability(0)
	if All()[0] == Capability(0) {
		t.Fatal("All() handed out the package's own slice; a caller can narrow the closed set")
	}
}

func TestUnknownCapabilityHasNoNameAndNoRoute(t *testing.T) {
	unknown := Capability(200)
	if got := unknown.String(); got != "capability(200)" {
		t.Fatalf("Capability(200).String() = %q, want the numeric form", got)
	}
	if got := unknown.Route(); got != 0 {
		t.Fatalf("Capability(200).Route() = %d, want the zero route", got)
	}
	if Capability(0).Route() != 0 {
		t.Fatal("the zero capability resolved to a route; it must not read as a real row")
	}
}

func TestLookupIsFailClosed(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		agent      agent.Agent
	}{
		{"capability outside the closed set", Capability(200), agent.AgentClaude},
		{"the zero capability", Capability(0), agent.AgentClaude},
		{"agent outside the accepted set", Hooks, agent.Agent("gemini")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Lookup(tt.capability, tt.agent)
			if err == nil {
				t.Fatalf("Lookup(%s, %q) = %+v, want an error", tt.capability, tt.agent, got)
			}
			if got.State != 0 {
				t.Fatalf("Lookup returned state %d alongside its error; the zero declaration is the only safe answer", got.State)
			}
		})
	}
}

func TestLookupAnswersEachDeclaredPair(t *testing.T) {
	tests := []struct {
		name       string
		capability Capability
		agent      agent.Agent
		wantState  State
		wantKind   ReasonKind
	}{
		{"implemented for claude", Hooks, agent.AgentClaude, StateImplemented, 0},
		{"the empty agent is claude", Hooks, agent.Agent(""), StateImplemented, 0},
		{"inherent gap for codex", RootSessionOrientation, agent.AgentCodex, StateUnavailable, ReasonAgentCannotReceive},
		{"niwa's own debt for codex", MCPServers, agent.AgentCodex, StateUnavailable, ReasonNotBuilt},
		{"claude's one gap", DirectoryTrust, agent.AgentClaude, StateUnavailable, ReasonNoSuchConcept},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Lookup(tt.capability, tt.agent)
			if err != nil {
				t.Fatalf("Lookup(%s, %q) unexpected error: %v", tt.capability, tt.agent, err)
			}
			if d.State != tt.wantState {
				t.Fatalf("Lookup(%s, %q).State = %d, want %d", tt.capability, tt.agent, d.State, tt.wantState)
			}
			if d.Kind != tt.wantKind {
				t.Fatalf("Lookup(%s, %q).Kind = %d, want %d", tt.capability, tt.agent, d.Kind, tt.wantKind)
			}
			if tt.wantState == StateUnavailable && d.Reason == "" {
				t.Fatalf("Lookup(%s, %q) is unavailable with no reason", tt.capability, tt.agent)
			}
		})
	}
}

// TestCodexColumnStatesMainsTruth pins the posture this contract lands with:
// nothing is delivered for Codex today, so every Codex row is unavailable. The
// rows that flip when Codex delivery lands are the ReasonNotBuilt ones, and
// this test is what makes that flip a deliberate edit rather than a drift.
func TestCodexColumnStatesMainsTruth(t *testing.T) {
	notBuilt := 0
	for _, c := range All() {
		d, err := Lookup(c, agent.AgentCodex)
		if err != nil {
			t.Fatalf("Lookup(%s, codex) unexpected error: %v", c, err)
		}
		if d.State != StateUnavailable {
			t.Fatalf("capability %s is declared delivered for codex, but nothing delivers it yet", c)
		}
		if d.Kind == ReasonNotBuilt {
			notBuilt++
		}
	}
	if notBuilt != 13 {
		t.Fatalf("%d codex rows are not-built, want 13 (the 11 that Codex delivery implements, plus the plugin wiring and the launch path)", notBuilt)
	}
}
