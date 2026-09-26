package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestWireKeyReportRendersWhateverTheRunRecorded covers the shape every surface
// uses: the collector is attached before the run and rendered by a deferred
// call afterwards, so a run that fails reports its keys exactly as one that
// succeeds. Create removes the instance directory on failure, so there is
// nothing to read back off disk at that point -- the collector is the only
// surviving record.
func TestWireKeyReportRendersWhateverTheRunRecorded(t *testing.T) {
	var buf bytes.Buffer
	applier := &workspace.Applier{}
	render := wireKeyReport(applier, &buf)

	if applier.Keys == nil {
		t.Fatal("wireKeyReport did not attach a collector to the applier")
	}
	applier.Keys.Add(keyreport.Entry{
		Scope:       "env.secrets",
		Key:         "GITHUB_TOKEN",
		Cause:       keyreport.CauseNoSource,
		Level:       config.LevelRequired,
		Description: "GitHub PAT with repo:read scope",
	})
	render()

	got := buf.String()
	for _, want := range []string{"GITHUB_TOKEN", "required", "GitHub PAT with repo:read scope"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered report is missing %q:\n%s", want, got)
		}
	}
}

// TestWireKeyReportSilentWhenNothingIsMissing: the common run supplies
// everything it declared and must print nothing extra.
func TestWireKeyReportSilentWhenNothingIsMissing(t *testing.T) {
	var buf bytes.Buffer
	render := wireKeyReport(&workspace.Applier{}, &buf)
	render()
	if buf.Len() != 0 {
		t.Errorf("rendered %q for a run with no shortfall, want nothing", buf.String())
	}
}

// TestSessionStartInjectionCarriesKeyReport is the hook delivery mechanism. The
// hook's stderr never reaches the agent and a non-zero exit emits no structured
// output at all, so a report that does not travel inside additionalContext
// reaches nobody.
func TestSessionStartInjectionCarriesKeyReport(t *testing.T) {
	dir := t.TempDir()
	out, err := buildSessionStartInjection(dir, []keyreport.Entry{{
		Scope:       "env.secrets",
		Key:         "ANTHROPIC_API_KEY",
		Cause:       keyreport.CauseNoSource,
		Level:       config.LevelRequired,
		Description: "key the agent uses to reach the model",
	}})
	if err != nil {
		t.Fatalf("buildSessionStartInjection: %v", err)
	}

	var inj sessionStartInjection
	if err := json.Unmarshal(out, &inj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ctx := inj.HookSpecificOutput.AdditionalContext
	for _, want := range []string{
		dir,
		"cd " + dir,
		"ANTHROPIC_API_KEY",
		"key the agent uses to reach the model",
		"Do not guess or fabricate values",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("additionalContext is missing %q:\n%s", want, ctx)
		}
	}
}
