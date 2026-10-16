package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// zetaServerTOML declares one stdio server in the base workspace config.
const zetaServerTOML = `
[mcp.servers.zeta]
command = "zeta-mcp"
`

// newMCPApprovalFixture is a posture fixture whose MCP collision reads land in
// scratch directories rather than the developer's own configuration.
func newMCPApprovalFixture(t *testing.T) *postureFixture {
	t.Helper()
	t.Setenv("CODEX_HOME", "")
	f := newPostureFixture(t, "")
	f.applier.DeveloperHome = t.TempDir()
	return f
}

// recordedApprovals reads the approved servers back from the saved state.
func recordedApprovals(t *testing.T, instanceRoot string) []string {
	t.Helper()
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state.ApprovedMCPServers
}

// rootSettingsApprovals reads enabledMcpjsonServers out of the instance root's
// settings document.
func rootSettingsApprovals(t *testing.T, instanceRoot string) ([]string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(instanceRoot, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}
	return approvedServersIn(t, data)
}

// TestApplyRecordsBaseAndOverlayMCPApprovals checks that a server declared in
// the base config and one declared only in the overlay are both recorded,
// sorted, and equal to what the instance root's settings document approves.
func TestApplyRecordsBaseAndOverlayMCPApprovals(t *testing.T) {
	run := applyWithOverlay(t, zetaServerTOML, `
[mcp.servers.example]
url = "https://mcp.example.com"
`)
	if run.err != nil {
		t.Fatalf("Apply: %v", run.err)
	}

	want := []string{"example", "zeta"}
	got := recordedApprovals(t, run.instanceRoot)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ApprovedMCPServers = %v, want %v", got, want)
	}
	settings, ok := rootSettingsApprovals(t, run.instanceRoot)
	if !ok {
		t.Fatalf("settings.json carries no %s", config.EnabledMcpjsonServersKey)
	}
	if !reflect.DeepEqual(got, settings) {
		t.Errorf("ApprovedMCPServers = %v, settings.json %s = %v; want them equal", got, config.EnabledMcpjsonServersKey, settings)
	}
}

// TestCreateRecordsMCPApprovals checks a real Create records the declared
// server, and that a workspace declaring none leaves no key in the raw state
// file.
func TestCreateRecordsMCPApprovals(t *testing.T) {
	t.Run("declared", func(t *testing.T) {
		f := newMCPApprovalFixture(t)
		f.create(t, f.writeConfig(t, zetaServerTOML))
		if got, want := recordedApprovals(t, f.instanceRoot), []string{"zeta"}; !reflect.DeepEqual(got, want) {
			t.Errorf("ApprovedMCPServers = %v, want %v", got, want)
		}
	})
	t.Run("none declared", func(t *testing.T) {
		f := newMCPApprovalFixture(t)
		f.create(t, f.writeConfig(t, ""))
		raw, err := os.ReadFile(filepath.Join(f.instanceRoot, ".niwa", StateFile))
		if err != nil {
			t.Fatalf("reading instance state: %v", err)
		}
		if strings.Contains(string(raw), "approved_mcp_servers") {
			t.Errorf("instance.json carries approved_mcp_servers with no server declared:\n%s", raw)
		}
	})
}

// TestApplyRecomputesMCPApprovals checks that Apply records the current
// config's servers rather than carrying the earlier state's value, even when
// the earlier state names servers the config never declared.
func TestApplyRecomputesMCPApprovals(t *testing.T) {
	f := newMCPApprovalFixture(t)
	f.create(t, f.writeConfig(t, zetaServerTOML))

	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.ApprovedMCPServers = []string{"planted", "zeta"}
	if err := SaveState(f.instanceRoot, state); err != nil {
		t.Fatal(err)
	}
	if err := f.applier.Apply(context.Background(), f.writeConfig(t, zetaServerTOML), f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got, want := recordedApprovals(t, f.instanceRoot), []string{"zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Apply: ApprovedMCPServers = %v, want %v", got, want)
	}

	if err := f.applier.Apply(context.Background(), f.writeConfig(t, ""), f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := recordedApprovals(t, f.instanceRoot); got != nil {
		t.Errorf("after Apply with no server: ApprovedMCPServers = %v, want none", got)
	}
}

// TestApplyReviewInstanceRecordsNoMCPApprovals checks that an apply in an
// instance niwa watch marked for review records nothing, while the settings
// document still approves the declared server as before.
func TestApplyReviewInstanceRecordsNoMCPApprovals(t *testing.T) {
	f := newMCPApprovalFixture(t)
	cfg := f.writeConfig(t, zetaServerTOML)
	f.create(t, cfg)

	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.ReviewInstance = true
	if err := SaveState(f.instanceRoot, state); err != nil {
		t.Fatal(err)
	}
	if err := f.applier.Apply(context.Background(), cfg, f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := recordedApprovals(t, f.instanceRoot); got != nil {
		t.Errorf("review instance: ApprovedMCPServers = %v, want none", got)
	}
	if got, _ := rootSettingsApprovals(t, f.instanceRoot); !reflect.DeepEqual(got, []string{"zeta"}) {
		t.Errorf("review instance: settings.json approves %v, want [zeta]", got)
	}
}

// TestRecordedMCPApprovals covers the decision on its own, including the
// exempt instance-root payload, which no producer's layout produces today.
func TestRecordedMCPApprovals(t *testing.T) {
	cfg := approvalWorkspace("zeta", "example")
	mcpPath := filepath.Join(t.TempDir(), ".mcp.json")

	tests := []struct {
		name     string
		install  *PayloadInstall
		existing *InstanceState
		want     []string
	}{
		{
			name:    "written",
			install: &PayloadInstall{Written: []string{mcpPath}},
			want:    []string{"example", "zeta"},
		},
		{
			name:    "exempt",
			install: &PayloadInstall{Exempt: []string{mcpPath}},
		},
		{
			name:    "nothing written",
			install: &PayloadInstall{},
		},
		{
			name:     "review marker",
			install:  &PayloadInstall{Written: []string{mcpPath}},
			existing: &InstanceState{ReviewInstance: true},
		},
		{
			name:     "ordinary existing state",
			install:  &PayloadInstall{Written: []string{mcpPath}},
			existing: &InstanceState{},
			want:     []string{"example", "zeta"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := recordedMCPApprovals(cfg, payloadBacksApprovals(tc.install), tc.existing)
			if err != nil {
				t.Fatalf("recordedMCPApprovals: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("recordedMCPApprovals = %v, want %v", got, tc.want)
			}
		})
	}
}
