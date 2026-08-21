package agentplan

import (
	"slices"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// spellingCase is one agent's user-facing spelling, written out by hand.
type spellingCase struct {
	ag         agent.Agent
	binary     string
	resumeArgs []string
	hintVerbs  []string
}

// TestLaunchSpecSpellingsAreLiteral is the one test here that types the strings
// out instead of reading them from the table.
//
// Every other check on this table is written against the declarations, which is
// what keeps them meaningful as the table changes -- and which also means they
// compare the table against itself. Spell Codex's binary `kodex` and its resume
// verb `resurrect` and all of them still pass: the expected value moved with the
// production value. What niwa would then do is preflight a binary that does not
// exist and tell a developer to run `kodex resurrect <id>`.
//
// These strings are not niwa's to choose. They are the name of somebody else's
// executable and the verbs that executable accepts, so the only check that can
// catch a typo in one is a check that knows what they are independently of the
// table. That makes this a change-detector on purpose: editing a spelling here
// is meant to require editing it twice, once in the table and once in a test
// whose failure message says what the agent actually accepts.
//
// The functional suite pins the same strings by running the binaries, which is
// the stronger evidence and the slower one. This is the copy that fails in the
// second before a commit.
func TestLaunchSpecSpellingsAreLiteral(t *testing.T) {
	cases := []spellingCase{
		{
			ag:         agent.AgentClaude,
			binary:     "claude",
			resumeArgs: []string{"attach"},
			hintVerbs:  []string{"attach", "logs", "stop"},
		},
		{
			ag:         agent.AgentCodex,
			binary:     "codex",
			resumeArgs: []string{"resume"},
			hintVerbs:  []string{"resume"},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.ag), func(t *testing.T) {
			spec, ok := For(tc.ag).LaunchSpec()
			if !ok {
				t.Fatalf("(%s) has no launch spec; if the row was removed deliberately, remove it here too", tc.ag)
			}
			if spec.Binary != tc.binary {
				t.Errorf("(%s) binary is %q; the executable is named %q, and niwa looks it up on PATH by this string", tc.ag, spec.Binary, tc.binary)
			}
			if !slices.Equal(spec.ResumeArgs, tc.resumeArgs) {
				t.Errorf("(%s) resume arguments are %q; %q is what the binary accepts, and niwa execs this to step back into a session", tc.ag, spec.ResumeArgs, tc.resumeArgs)
			}
			if !slices.Equal(spec.HintVerbs, tc.hintVerbs) {
				t.Errorf("(%s) hint verbs are %q, want exactly %q; these are printed as `%s <verb> <handle>` for a developer to type", tc.ag, spec.HintVerbs, tc.hintVerbs, tc.binary)
			}
		})
	}

	// An agent added to the table with no row here would keep its spellings
	// unpinned, which is the state this test exists to end.
	for ag := range launchSpecs {
		if !slices.ContainsFunc(cases, func(c spellingCase) bool { return c.ag == ag }) {
			t.Errorf("(%s) has a launch spec whose spellings nothing pins; add its binary and verbs to this test, read off the agent's own documentation rather than off the table", ag)
		}
	}
}
