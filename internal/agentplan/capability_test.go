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
		{"inherent gap for codex", MarketplaceRegistration, agent.AgentCodex, StateUnavailable, ReasonAgentCannotReceive},
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

// codexFinalGaps is the Codex column at its target, unavailable half: the nine
// rows that stay unavailable once every Codex delivery has landed, each with
// the reason kind the PRD's matrix gives it.
//
// Every one of them is now inherent to the agent -- five its own mechanics put
// out of reach, four naming surface that exists only in the other harness. The
// not-built kind, the one category a developer could act on, is empty for this
// column: niwa owes Codex nothing that a route exists for. That is a fact about
// today rather than a rule, which is why the kind survives in the checks below
// and in the guide's renderer; a capability added tomorrow with a route and no
// delivery lands there again.
//
// Writing the rows out here is what makes an accidental flip fail with a name
// in the message. A row missing from this map is one whose delivery is still
// pending, and TestCodexColumnStatesWhatIsDelivered checks it is unavailable
// with the not-built kind until it lands.
//
// Three rows left this map after the matrix was drawn. Rows 2 and 18 both
// rested on one reason measurement showed is false -- that Codex reads nothing
// at a directory with no project-root marker above it, when a session's own
// working directory always contributes -- and both are delivered now, row 2 as
// the composed root document and row 18 as the skills trees beside it. Row 19
// left as its wiring was built, for both agents.
//
// The PRD is amended to match rather than left to disagree, because this
// comment names it as the authority and a citation that contradicts its source
// is worse than no citation. See the amendments under the matrix in
// docs/prds/PRD-agent-capability-contract.md.
var codexFinalGaps = map[Capability]ReasonKind{
	MarketplaceRegistration: ReasonAgentCannotReceive,
	SubagentTypes:           ReasonNoSuchConcept,
	Hooks:                   ReasonAgentCannotReceive,
	WorkSummaryHooks:        ReasonAgentCannotReceive,
	PRBodyHook:              ReasonAgentCannotReceive,
	WorktreeHookDelegation:  ReasonNoSuchConcept,
	EphemeralSessions:       ReasonAgentCannotReceive,
	RemoteControl:           ReasonNoSuchConcept,
	DispatchKeepAlive:       ReasonNoSuchConcept,
}

// codexDelivered is what niwa delivers to Codex today: fifteen rows against the
// nine final gaps in codexFinalGaps, which is the whole column with nothing
// pending between them. Directory trust is the first, and deliberately so --
// every trust-gated row downstream names it in Requires, and the closure test
// refuses such an edge while it is unavailable. The list grew one entry per
// delivery, in the change that landed the delivery, never before it.
//
// The two instance-root rows at the end are the ones that close the column.
// Neither needs trust: skills are the one part of the project layer measured to
// load from an untrusted directory, which is what let them reach a root nobody
// wrote a trust entry for. The configuration keys beside them still do need it,
// and still land only inside a repository -- a scope the declaration table has
// no axis to state, so no row here says it.
//
// The two agent-agnostic rows -- dotenv files and file distribution -- have no
// Codex-specific delivery at all: the dotenv writer and the file distributor
// put the same bytes on disk for whoever opens the session, so what changed for
// them was the declaration and its binding, not the code that writes.
var codexDelivered = []Capability{
	WorkspaceOrientation,
	RootSessionOrientation,
	RepoOrientationDoc,
	WorktreeOrientationDoc,
	PluginSkills,
	MCPServers,
	SessionEnvironment,
	ApprovalPosture,
	DirectoryTrust,
	GitExcludeBookkeeping,
	DotenvFiles,
	FileDistribution,
	DispatchLaunch,
	RootProjectSkills,
	NiwaPlugin,
}

// TestCodexColumnTotals pins the shape of the finished column as a pair of
// counts, which is the form the requirements state it in and the form a
// reviewer checks it in. The row-by-row test below says which rows; this one
// says how many, so a flip that swaps one row for another -- passing every
// per-row check by moving a name from one list to the other -- still has to
// face a number somebody wrote down on purpose.
func TestCodexColumnTotals(t *testing.T) {
	const wantImplemented, wantUnavailable = 15, 9

	implemented, unavailable := 0, 0
	for _, c := range All() {
		d, err := Lookup(c, agent.AgentCodex)
		if err != nil {
			t.Fatalf("Lookup(%s, codex) unexpected error: %v", c, err)
		}
		switch d.State {
		case StateImplemented:
			implemented++
		case StateUnavailable:
			unavailable++
		}
	}
	if implemented != wantImplemented || unavailable != wantUnavailable {
		t.Errorf("the Codex column reads %d implemented / %d unavailable, want %d / %d",
			implemented, unavailable, wantImplemented, wantUnavailable)
	}
}

// TestCodexColumnStatesWhatIsDelivered pins the Codex column against two
// things at once: what niwa delivers today, and what the column looks like when
// it is finished. A row that flips without its delivery fails here, and so does
// a row whose final reason kind is edited away from the one the matrix settled
// on -- which is the drift the reason kinds exist to make visible.
//
// Today those two things coincide: every Codex row is either delivered or an
// inherent gap, so the pending branch below matches nothing. It is kept because
// what it guards is the next capability added to the closed set, not the last
// one removed from the pending side -- a new row with a route and no delivery
// must declare niwa's own debt rather than borrow an inherent reason.
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
