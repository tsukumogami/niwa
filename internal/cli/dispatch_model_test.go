package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestResolveDispatchModel pins the resolution contract under Claude: categories
// map to a concrete versionless name, known vendor names pass through
// lowercased with no warning, and anything else is forwarded UNCHANGED with a
// warning (never rejected), so a full model id or a not-yet-known alias still
// launches. The zero-value agent resolves as Claude.
func TestResolveDispatchModel(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantModel string
		wantWarn  bool
	}{
		{"empty forwards nothing", "", "", false},
		{"whitespace only forwards nothing", "   ", "", false},
		{"category fast", "fast", "haiku", false},
		{"category balanced", "balanced", "sonnet", false},
		{"category powerful", "powerful", "opus", false},
		{"category is case-insensitive", "Powerful", "opus", false},
		{"vendor opus passthrough", "opus", "opus", false},
		{"vendor sonnet passthrough", "sonnet", "sonnet", false},
		{"vendor fable passthrough", "fable", "fable", false},
		{"vendor haiku passthrough", "haiku", "haiku", false},
		{"vendor name is lowercased", "Opus", "opus", false},
		{"surrounding whitespace trimmed", "  balanced  ", "sonnet", false},
		{"unknown full id forwarded with warning", "claude-opus-4-8", "claude-opus-4-8", true},
		{"unknown alias forwarded with warning", "gpt-4o", "gpt-4o", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Explicit Claude and the zero-value agent must resolve identically.
			for _, ag := range []agent.Agent{agent.AgentClaude, agent.Agent("")} {
				spec, ok := agentplan.For(ag).LaunchSpec()
				if !ok {
					t.Fatalf("no launch spec for agent %q", ag)
				}
				gotModel, gotWarn := resolveDispatchModel(spec, tc.in)
				if gotModel != tc.wantModel {
					t.Errorf("resolveDispatchModel(%q, %q) model = %q, want %q", ag, tc.in, gotModel, tc.wantModel)
				}
				if (gotWarn != "") != tc.wantWarn {
					t.Errorf("resolveDispatchModel(%q, %q) warning = %q, want warn=%v", ag, tc.in, gotWarn, tc.wantWarn)
				}
			}
		})
	}
}

// TestResolveDispatchModelReadsTheSpecItIsGiven asserts the resolver carries no
// vocabulary of its own. Given a spec with different categories, different
// known names, and a different binary, every answer changes accordingly --
// including the warning, which names the binary the value is being forwarded
// to. A resolver that had a table inside it would pass the Claude cases above
// and fail every one of these.
func TestResolveDispatchModelReadsTheSpecItIsGiven(t *testing.T) {
	spec := agentplan.LaunchSpec{
		Binary:          "othertool",
		ModelCategories: map[string]string{"fast": "tiny-1", "balanced": "mid-1", "powerful": "big-1"},
		KnownModels:     []string{"big-1", "mid-1", "tiny-1"},
	}

	for _, tc := range []struct {
		in        string
		wantModel string
		wantWarn  bool
	}{
		{"fast", "tiny-1", false},
		{"Powerful", "big-1", false},
		{"mid-1", "mid-1", false},
		// A name this spec does not know, even though another agent's spec
		// does. The resolver must not recognize it.
		{"haiku", "haiku", true},
		{"", "", false},
	} {
		gotModel, gotWarn := resolveDispatchModel(spec, tc.in)
		if gotModel != tc.wantModel {
			t.Errorf("resolveDispatchModel(synthetic, %q) model = %q, want %q", tc.in, gotModel, tc.wantModel)
		}
		if (gotWarn != "") != tc.wantWarn {
			t.Errorf("resolveDispatchModel(synthetic, %q) warning = %q, want warn=%v", tc.in, gotWarn, tc.wantWarn)
		}
		if tc.wantWarn && !strings.Contains(gotWarn, spec.Binary) {
			t.Errorf("warning %q does not name the binary the value is forwarded to (%q)", gotWarn, spec.Binary)
		}
	}
}

// TestResolveDispatchModelPerAgent restores, table-driven, the coverage the
// two-pull-request split briefly dropped.
//
// Before the split, three tests pinned the Codex vocabulary by its literal
// values. The first pull request removed the per-agent model table -- it was a
// delivery no declaration stood behind -- and took those tests with it, and the
// second brought the vocabulary back inside the launch description without
// bringing back anything that checked how it resolves. The completeness suite
// only asks whether a category maps to something non-empty, which a table of
// three empty-ish placeholders would satisfy.
//
// So these read the expected values out of each agent's own declaration rather
// than restating them. That keeps the coverage without pinning names niwa
// deliberately stays out of the business of versioning.
func TestResolveDispatchModelPerAgent(t *testing.T) {
	for _, ag := range agentplan.LaunchableAgents() {
		t.Run(string(ag), func(t *testing.T) {
			spec, ok := agentplan.For(ag).LaunchSpec()
			if !ok {
				t.Fatalf("no launch spec for %s", ag)
			}

			// Every portable category resolves to this agent's own concrete
			// name, and does so case-insensitively.
			for _, category := range agentplan.ModelCategories() {
				want := spec.ModelCategories[category]
				if got, warn := resolveDispatchModel(spec, category); got != want || warn != "" {
					t.Errorf("category %q resolved to %q (warning %q), want %q with no warning", category, got, warn, want)
				}
				if got, _ := resolveDispatchModel(spec, strings.ToUpper(category)); got != want {
					t.Errorf("category %q resolved to %q when upper-cased, want %q", category, got, want)
				}
			}

			// Every name this agent knows passes through unchanged and
			// unremarked.
			for _, known := range spec.KnownModelNames() {
				if got, warn := resolveDispatchModel(spec, known); got != known || warn != "" {
					t.Errorf("known name %q resolved to %q (warning %q), want it forwarded silently", known, got, warn)
				}
			}

			// And a name another agent knows is not a name this one does. This
			// is the assertion that makes the vocabulary per-agent rather than
			// pooled: it is forwarded, because niwa never blocks a launch over
			// a name it does not recognize, but it warns.
			for _, other := range agentplan.LaunchableAgents() {
				if other == ag {
					continue
				}
				otherSpec, ok := agentplan.For(other).LaunchSpec()
				if !ok {
					continue
				}
				for _, name := range otherSpec.KnownModelNames() {
					if slices.Contains(spec.KnownModelNames(), name) {
						continue
					}
					got, warn := resolveDispatchModel(spec, name)
					if got != name {
						t.Errorf("%s's name %q was not forwarded under %s: got %q", other, name, ag, got)
					}
					if warn == "" {
						t.Errorf("%s's name %q was recognized under %s; the vocabularies are per-agent", other, name, ag)
					}
				}
			}
		})
	}
}

// TestDispatchModelCategoriesDifferByAgent is the whole point of the per-agent
// map, and it is the check most likely to pass vacuously if the map ever
// collapses into one shared table: every portable category must resolve to a
// different concrete model for each agent, because a category that resolved to
// the same thing everywhere would not need to be per-agent at all.
func TestDispatchModelCategoriesDifferByAgent(t *testing.T) {
	launchable := agentplan.LaunchableAgents()
	if len(launchable) < 2 {
		t.Skipf("only %d launchable agent(s); categories cannot be shown to differ", len(launchable))
	}

	for _, category := range agentplan.ModelCategories() {
		seen := map[string]agent.Agent{}
		for _, ag := range launchable {
			spec, ok := agentplan.For(ag).LaunchSpec()
			if !ok {
				t.Fatalf("no launch spec for %s", ag)
			}
			got, _ := resolveDispatchModel(spec, category)
			if got == "" {
				t.Fatalf("category %q resolved empty for %s", category, ag)
			}
			if prev, dup := seen[got]; dup {
				t.Errorf("category %q resolves to %q for both %s and %s", category, got, prev, ag)
			}
			seen[got] = ag
		}
	}
}

// passthroughModel returns the value following the first "--model" element in a
// passthrough argv, or "" when absent. It ignores other flags (e.g. a
// remote-control --settings pair) so model assertions stay hermetic.
func passthroughModel(pass []string) string {
	for i := 0; i+1 < len(pass); i++ {
		if pass[i] == "--model" {
			return pass[i+1]
		}
	}
	return ""
}

// TestDispatch_ModelFlag_ResolvesCategory checks the --model flag flows through
// resolution: a capability category reaches the worker as its concrete name.
func TestDispatch_ModelFlag_ResolvesCategory(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "") // isolate: no host default, no remote-control injection
	f := installDispatchFakes(t, root)
	dispatchModel = "powerful"
	dispatchDetach = true
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := passthroughModel(pass); got != "opus" {
		t.Fatalf("category powerful should forward --model opus, got %q (full %v)", got, pass)
	}
}

// TestDispatch_ModelDefault_FromConfig checks the [global] dispatch_model default
// fills in when --model is unset, resolving through the category table.
func TestDispatch_ModelDefault_FromConfig(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndispatch_model = \"fast\"\n")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := passthroughModel(pass); got != "haiku" {
		t.Fatalf("config default fast should forward --model haiku, got %q (full %v)", got, pass)
	}
}

// TestDispatch_ModelFlag_OverridesConfigDefault checks an explicit --model wins
// over the [global] dispatch_model default.
func TestDispatch_ModelFlag_OverridesConfigDefault(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndispatch_model = \"fast\"\n")
	f := installDispatchFakes(t, root)
	dispatchModel = "powerful"
	dispatchDetach = true
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := passthroughModel(pass); got != "opus" {
		t.Fatalf("--model powerful should override config default, want opus, got %q (full %v)", got, pass)
	}
}

// TestDispatch_NoModel_ForwardsNothing checks that with neither flag nor config
// default set, no --model element is forwarded (today's behavior preserved).
func TestDispatch_NoModel_ForwardsNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "") // no default, no remote-control
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := passthroughModel(pass); got != "" {
		t.Fatalf("no model set should forward no --model, got %q (full %v)", got, pass)
	}
}
