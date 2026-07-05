package cli

import (
	"testing"
)

// TestResolveDispatchModel pins the resolution contract: categories map to a
// concrete versionless name, known vendor names pass through lowercased with no
// warning, and anything else is forwarded UNCHANGED with a warning (never
// rejected), so a full model id or a not-yet-known alias still launches.
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
			gotModel, gotWarn := resolveDispatchModel(tc.in)
			if gotModel != tc.wantModel {
				t.Errorf("resolveDispatchModel(%q) model = %q, want %q", tc.in, gotModel, tc.wantModel)
			}
			if (gotWarn != "") != tc.wantWarn {
				t.Errorf("resolveDispatchModel(%q) warning = %q, want warn=%v", tc.in, gotWarn, tc.wantWarn)
			}
		})
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
