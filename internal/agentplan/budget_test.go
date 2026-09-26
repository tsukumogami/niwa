package agentplan

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/tsukumogami/niwa/internal/agent"
)

// These cover the one key in the generated payload document niwa sizes rather
// than relays: the bound on the context chain a session reads. The failure it
// exists against is silent -- Codex spends one counter across the whole chain,
// outermost-first, and cuts the overflow with nothing in the text and nothing
// on stderr -- so what is asserted here is the arithmetic of the declared value
// and the two cases niwa deliberately writes no key at all in.

// declaredBudget reads the sized bound back out of a generated document, and
// reports zero when the document declares none.
func declaredBudget(t *testing.T, content []byte) int64 {
	t.Helper()
	var doc map[string]any
	if _, err := toml.Decode(string(content), &doc); err != nil {
		t.Fatalf("the generated document does not parse: %v\n%s", err, content)
	}
	raw, present := doc[codexDocBudgetKey]
	if !present {
		return 0
	}
	value, ok := raw.(int64)
	if !ok {
		t.Fatalf("%s decoded as %T, not an integer:\n%s", codexDocBudgetKey, raw, content)
	}
	return value
}

// TestChainInsideTheDefaultDeclaresNoBudget is the case niwa stays out of. A
// project layer outranks the developer's own configuration, so restating a
// default that already covers the chain would take a raised budget away from
// somebody who never asked niwa to touch it.
func TestChainInsideTheDefaultDeclaresNoBudget(t *testing.T) {
	for _, chain := range []int{0, 1, codexDocBudget / 4, codexDocBudget / 2} {
		if got := codexBudgetFor(chain); got != 0 {
			t.Errorf("a %d-byte chain sized a %d-byte budget; the %d-byte default already covers it", chain, got, codexDocBudget)
		}
	}

	plan := codexPlan(t, PayloadInputs{
		Servers:           []MCPServer{stdioServer()},
		ContextChainBytes: codexDocBudget / 2,
	})
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries, want 1", len(plan.Entries))
	}
	if got := declaredBudget(t, plan.Entries[0].Content); got != 0 {
		t.Errorf("the generated document declares %s = %d for a chain the default covers", codexDocBudgetKey, got)
	}
}

// TestOverflowingChainDeclaresACoveringBudget is the gap this closes: past the
// default, niwa writes the key that raises the bound, and the value covers the
// chain with room for it to grow rather than fitting it exactly.
func TestOverflowingChainDeclaresACoveringBudget(t *testing.T) {
	chain := codexDocBudget*3 + 17
	plan := codexPlan(t, PayloadInputs{ContextChainBytes: chain})
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries for an over-budget chain, want 1", len(plan.Entries))
	}

	got := declaredBudget(t, plan.Entries[0].Content)
	if got < int64(chain) {
		t.Fatalf("the declared %s = %d does not even cover the %d-byte chain it was sized from", codexDocBudgetKey, got, chain)
	}
	if got < int64(chain*codexBudgetHeadroom) {
		t.Errorf("the declared %s = %d is under %dx the %d-byte chain; an exact-fit bound starts cutting the innermost layer the moment anything grows", codexDocBudgetKey, got, codexBudgetHeadroom, chain)
	}
	if got%codexDocBudget != 0 {
		t.Errorf("the declared %s = %d is not a whole multiple of %d, so a one-line content edit rewrites it on every apply", codexDocBudgetKey, got, codexDocBudget)
	}
	if got < codexDocBudget {
		t.Errorf("the declared %s = %d is under the agent's own %d-byte default", codexDocBudgetKey, got, codexDocBudget)
	}
}

// TestABudgetAloneProducesADocument covers the workspace that declares no
// servers, no environment and no posture but composes a chain past the default.
// There is nothing else to hang the write on, and the write is the whole of what
// keeps the repository layer of that chain from being cut in silence.
func TestABudgetAloneProducesADocument(t *testing.T) {
	plan := codexPlan(t, PayloadInputs{ContextChainBytes: codexDocBudget * 2})
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries for an over-budget chain and nothing else, want 1", len(plan.Entries))
	}
	if got := plan.Entries[0].Capability; got != RepoOrientationDoc {
		t.Errorf("entry capability = %s, want %s: the budget is spent on the orientation documents", got, RepoOrientationDoc)
	}
	if got := plan.Entries[0].Mode; got != payloadFileMode {
		t.Errorf("entry mode = %o, want %o", got, payloadFileMode)
	}
}

// TestBudgetSizingMovesInSteps pins the rounding. The declared value is written
// into a file every apply rewrites, so a bound that tracked the chain byte for
// byte would churn the generated configuration on every edit to a context file.
// It moves when the chain crosses a step, and not for the edits in between.
func TestBudgetSizingMovesInSteps(t *testing.T) {
	base := codexDocBudget*3 + 17
	first := codexBudgetFor(base)
	for _, edit := range []int{1, 100, 1000} {
		if got := codexBudgetFor(base + edit); got != first {
			t.Errorf("a %d-byte edit moved the budget from %d to %d", edit, first, got)
		}
	}
	if got := codexBudgetFor(base * 4); got <= first {
		t.Errorf("quadrupling the chain left the budget at %d, want more than %d", got, first)
	}
}

// TestAgentWithNoBudgetRouteDeclaresNone is the other side of the per-agent
// table: Claude reads its context documents whole, so no chain measurement
// reaches it and nothing here writes a bound into its document.
func TestAgentWithNoBudgetRouteDeclaresNone(t *testing.T) {
	plan, err := For(agent.AgentClaude).PayloadPlan(PayloadInputs{
		Scope:             PayloadAtInstanceRoot,
		Dir:               "/instance",
		Servers:           []MCPServer{stdioServer()},
		ContextChainBytes: codexDocBudget * 8,
	})
	if err != nil {
		t.Fatalf("PayloadPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan declared %d entries, want 1", len(plan.Entries))
	}
	if strings.Contains(string(plan.Entries[0].Content), codexDocBudgetKey) {
		t.Errorf("Claude's document carries %s:\n%s", codexDocBudgetKey, plan.Entries[0].Content)
	}
}

// TestUnusableBudgetsAreRefused covers validation-before-write for the budget,
// the same discipline the servers, the environment and the posture get. Both
// refusals are about what the written key would do to a session: one would take
// away a bound niwa was never asked to touch, the other would declare one that
// truncates the day it is written.
func TestUnusableBudgetsAreRefused(t *testing.T) {
	cases := map[string]struct{ budget, chain int }{
		"a bound under the agent's own default":  {budget: codexDocBudget - 1, chain: 10},
		"a negative bound":                       {budget: -1, chain: 10},
		"a bound that does not cover its chain":  {budget: codexDocBudget, chain: codexDocBudget * 2},
		"a bound equal to the chain it must fit": {budget: codexDocBudget, chain: codexDocBudget + 1},
	}
	for name, tc := range cases {
		if err := validateDocBudget(tc.budget, tc.chain); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err := validateDocBudget(0, codexDocBudget/2); err != nil {
		t.Errorf("declaring no budget at all was rejected: %v", err)
	}
	if err := validateDocBudget(codexDocBudget*2, codexDocBudget+1); err != nil {
		t.Errorf("a covering budget was rejected: %v", err)
	}
}

// TestGeneratedBudgetCheckCatchesADamagedOne exercises the last gate directly.
// It runs over the decoded document rather than over the inputs, because what a
// session loads is the bytes -- including the absent arm, which is the one that
// keeps niwa from writing a bound nobody asked for.
func TestGeneratedBudgetCheckCatchesADamagedOne(t *testing.T) {
	decode := func(t *testing.T, body string) map[string]any {
		t.Helper()
		var doc map[string]any
		if _, err := toml.Decode(body, &doc); err != nil {
			t.Fatalf("fixture does not parse: %v", err)
		}
		return doc
	}

	if err := checkCodexDocBudget(decode(t, ""), 0); err != nil {
		t.Errorf("a document with no budget, where none was sized, was rejected: %v", err)
	}
	if err := checkCodexDocBudget(decode(t, "project_doc_max_bytes = 65536\n"), 65536); err != nil {
		t.Errorf("a document carrying the sized budget was rejected: %v", err)
	}

	damaged := map[string]struct {
		body   string
		sized  int
		reason string
	}{
		"a bound nobody sized":      {body: "project_doc_max_bytes = 65536\n", sized: 0},
		"a sized bound left out":    {body: "", sized: 65536},
		"a bound of the wrong size": {body: "project_doc_max_bytes = 65536\n", sized: 131072},
		"a bound written as text":   {body: "project_doc_max_bytes = \"65536\"\n", sized: 65536},
	}
	for name, tc := range damaged {
		if err := checkCodexDocBudget(decode(t, tc.body), tc.sized); err == nil {
			t.Errorf("%s passed the check", name)
		}
	}
}

// TestARefusedDocumentStillReportsTheOverflow is the case that survives the
// declaration. The repository commits its own file at the path the bound would
// have gone in, so niwa writes nothing there -- and an over-budget chain is back
// to being cut in silence unless the refusal says so.
func TestARefusedDocumentStillReportsTheOverflow(t *testing.T) {
	const owned = "/repo/.codex/config.toml"
	plan := codexPlan(t, PayloadInputs{
		Servers:           []MCPServer{stdioServer()},
		ContextChainBytes: codexDocBudget * 2,
		Probe:             ContextProbe{Dir: "/repo", OwnedPath: owned, Foreign: true},
	})
	if len(plan.Entries) != 0 {
		t.Fatalf("the plan carries %d entries for an occupied path", len(plan.Entries))
	}
	var reported bool
	for _, w := range plan.Warnings {
		if strings.Contains(w, codexDocBudgetKey) && strings.Contains(w, owned) {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the refusal says nothing about the budget it did not write: %v", plan.Warnings)
	}

	// A chain the default covers loses nothing to the refusal, so the refusal
	// says nothing about a budget.
	fits := codexPlan(t, PayloadInputs{
		Servers:           []MCPServer{stdioServer()},
		ContextChainBytes: codexDocBudget / 2,
		Probe:             ContextProbe{Dir: "/repo", OwnedPath: owned, Foreign: true},
	})
	for _, w := range fits.Warnings {
		if strings.Contains(w, codexDocBudgetKey) {
			t.Errorf("a chain the default covers was reported as an overflow: %q", w)
		}
	}
}
