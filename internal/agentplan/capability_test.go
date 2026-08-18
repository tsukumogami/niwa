package agentplan

import (
	"slices"
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
		{"niwa's own debt for codex", DotenvFiles, agent.AgentCodex, StateUnavailable, ReasonNotBuilt},
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

// codexFinalGaps is the Codex column at its target, unavailable half: the
// thirteen rows that stay unavailable once every Codex delivery has landed,
// each with the reason kind the PRD's matrix gives it. Eleven are inherent to
// the agent; the last two name a route that exists and is out of this work's
// scope.
//
// Writing them out here is what makes an accidental flip fail with a name in
// the message. A row missing from this map is one whose delivery is still
// pending, and TestCodexColumnStatesWhatIsDelivered checks it is unavailable
// with the not-built kind until it lands.
var codexFinalGaps = map[Capability]ReasonKind{
	RootSessionOrientation:  ReasonAgentCannotReceive,
	MarketplaceRegistration: ReasonAgentCannotReceive,
	SubagentTypes:           ReasonNoSuchConcept,
	Hooks:                   ReasonAgentCannotReceive,
	WorkSummaryHooks:        ReasonAgentCannotReceive,
	PRBodyHook:              ReasonAgentCannotReceive,
	WorktreeHookDelegation:  ReasonNoSuchConcept,
	EphemeralSessions:       ReasonAgentCannotReceive,
	RootProjectSkills:       ReasonAgentCannotReceive,
	NiwaPlugin:              ReasonNotBuilt,
	RemoteControl:           ReasonNoSuchConcept,
	DispatchKeepAlive:       ReasonNoSuchConcept,
	DispatchLaunch:          ReasonNotBuilt,
}

// codexDelivered is what niwa delivers to Codex today. Directory trust is the
// first row, and it is deliberately the first: every trust-gated row downstream
// names it in Requires, and the closure test refuses such an edge while it is
// unavailable. The list grows one entry per delivery, in the change that lands
// the delivery -- never before it.
var codexDelivered = []Capability{
	WorkspaceOrientation,
	RepoOrientationDoc,
	WorktreeOrientationDoc,
	PluginSkills,
	MCPServers,
	SessionEnvironment,
	ApprovalPosture,
	DirectoryTrust,
	GitExcludeBookkeeping,
}

// TestCodexColumnStatesWhatIsDelivered pins the Codex column against two
// things at once: what niwa delivers today, and what the column looks like when
// it is finished. A row that flips without its delivery fails here, and so does
// a row whose final reason kind is edited away from the one the matrix settled
// on -- which is the drift the reason kinds exist to make visible.
func TestCodexColumnStatesWhatIsDelivered(t *testing.T) {
	for _, c := range All() {
		d, err := Lookup(c, agent.AgentCodex)
		if err != nil {
			t.Fatalf("Lookup(%s, codex) unexpected error: %v", c, err)
		}

		if slices.Contains(codexDelivered, c) {
			if d.State != StateImplemented {
				t.Errorf("capability %s is delivered to codex but declared unavailable", c)
			}
			continue
		}

		if d.State == StateImplemented {
			t.Errorf("capability %s is declared delivered for codex, but nothing here delivers it yet", c)
			continue
		}

		want, final := codexFinalGaps[c]
		if !final {
			want = ReasonNotBuilt
		}
		if d.Kind != want {
			if final {
				t.Errorf("(%s, codex) is unavailable with reason kind %d, want %d: the matrix settled this row's reason", c, d.Kind, want)
			} else {
				t.Errorf("(%s, codex) is unavailable with reason kind %d, want %d: a row still awaiting its delivery is niwa's own debt", c, d.Kind, want)
			}
		}
	}
}

// TestDirectoryTrustIsCodexOnly pins row 23 in both columns. Trust is the one
// row where Claude is the agent with the gap, and the reason is the shape of
// the agent rather than anything niwa has left undone: Claude Code keeps no
// per-directory trust record to write into.
func TestDirectoryTrustIsCodexOnly(t *testing.T) {
	codex, err := Lookup(DirectoryTrust, agent.AgentCodex)
	if err != nil {
		t.Fatalf("Lookup(directory-trust, codex) unexpected error: %v", err)
	}
	if codex.State != StateImplemented {
		t.Errorf("directory trust is not implemented for codex; every trust-gated row downstream depends on it")
	}

	claude, err := Lookup(DirectoryTrust, agent.AgentClaude)
	if err != nil {
		t.Fatalf("Lookup(directory-trust, claude) unexpected error: %v", err)
	}
	if claude.State != StateUnavailable || claude.Kind != ReasonNoSuchConcept {
		t.Errorf("directory trust for claude is state %d kind %d, want unavailable/no-such-concept", claude.State, claude.Kind)
	}
}
