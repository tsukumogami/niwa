package workspace

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/pluginrecord"
)

func writeToolsManifest(t *testing.T, repoDir, name string) {
	t.Helper()
	dir := filepath.Join(repoDir, ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"` + name + `","plugins":[]}`
	if err := os.WriteFile(filepath.Join(dir, "marketplace.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMarketplaceRegistrationName(t *testing.T) {
	base := t.TempDir()
	toolsDir := filepath.Join(base, "tools")
	writeToolsManifest(t, toolsDir, "tsukumogami")
	// bare has a manifest present (so the source resolves) but with no
	// declared name, exercising the ref-name fallback.
	bareDir := filepath.Join(base, "bare")
	bareManifestDir := filepath.Join(bareDir, ".claude-plugin")
	if err := os.MkdirAll(bareManifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bareManifestDir, "marketplace.json"), []byte(`{"plugins":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	repoIndex := map[string]string{"tools": toolsDir, "bare": bareDir}

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"github uses repo name", "tsukumogami/shirabe", "shirabe"},
		{"local uses manifest name", "repo:tools/.claude-plugin/marketplace.json", "tsukumogami"},
		{"local falls back to ref name", "repo:bare/.claude-plugin/marketplace.json", "bare"},
		{"unrecognized source", "not-a-valid-ref", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := marketplaceRegistrationName(tc.source, repoIndex)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("marketplaceRegistrationName(%q) = %q, want %q", tc.source, got, tc.want)
			}
		})
	}
}

func TestReconcileMarketplaceRegistry_BuildsDesiredAndReports(t *testing.T) {
	base := t.TempDir()
	toolsDir := filepath.Join(base, "tools")
	writeToolsManifest(t, toolsDir, "tsukumogami")
	repoIndex := map[string]string{"tools": toolsDir}

	var captured map[string]pluginrecord.MarketplaceWant
	var buf bytes.Buffer
	a := &Applier{
		Reporter: NewReporter(&buf),
		reconcileMarketplaces: func(desired map[string]pluginrecord.MarketplaceWant) (pluginrecord.ReconcileReport, error) {
			captured = desired
			return pluginrecord.ReconcileReport{Updated: []string{"shirabe"}}, nil
		},
	}
	cfg := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Marketplaces: config.MarketplaceConfigs{
				{Source: "tsukumogami/shirabe", AutoUpdate: false},
				{Source: "repo:tools/.claude-plugin/marketplace.json", AutoUpdate: true},
			},
		},
	}

	a.reconcileMarketplaceRegistry(cfg, repoIndex, base)

	if len(captured) != 2 {
		t.Fatalf("desired = %v, want 2 entries", captured)
	}
	if v, ok := captured["shirabe"]; !ok || v != (pluginrecord.MarketplaceWant{}) {
		t.Errorf("desired[shirabe] = %+v (ok=%v), want autoUpdate false and no path", v, ok)
	}
	// tools resolves to an instance clone, not the managed copy, so the
	// entry's path is left alone.
	if v, ok := captured["tsukumogami"]; !ok || v != (pluginrecord.MarketplaceWant{AutoUpdate: true}) {
		t.Errorf("desired[tsukumogami] = %+v (ok=%v), want autoUpdate true and no path", v, ok)
	}
	if !strings.Contains(buf.String(), "auto-update policy") {
		t.Errorf("expected a report log mentioning the policy update, got %q", buf.String())
	}
}

// TestReconcileMarketplaceRegistry_PathFromManagedCopy asserts a repo:
// marketplace resolved to the workspace's managed copy asks for its entry to
// point there.
func TestReconcileMarketplaceRegistry_PathFromManagedCopy(t *testing.T) {
	workspaceRoot := t.TempDir()
	managed := managedMarketplaceCurrent(workspaceRoot, "tools")
	writeToolsManifest(t, managed, "tsukumogami")

	var captured map[string]pluginrecord.MarketplaceWant
	var buf bytes.Buffer
	a := &Applier{
		Reporter: NewReporter(&buf),
		reconcileMarketplaces: func(desired map[string]pluginrecord.MarketplaceWant) (pluginrecord.ReconcileReport, error) {
			captured = desired
			return pluginrecord.ReconcileReport{Repointed: []string{"tsukumogami"}}, nil
		},
	}
	cfg := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Marketplaces: config.MarketplaceConfigs{{Source: "repo:tools/.claude-plugin/marketplace.json"}},
		},
	}

	a.reconcileMarketplaceRegistry(cfg, map[string]string{"tools": managed}, workspaceRoot)

	if got := captured["tsukumogami"]; got.Path != managed {
		t.Errorf("desired[tsukumogami].Path = %q, want %q", got.Path, managed)
	}
	if !strings.Contains(buf.String(), "managed copy: tsukumogami") {
		t.Errorf("expected a report log naming the repointed marketplace, got %q", buf.String())
	}
}

func TestReconcileMarketplaceRegistry_FailSafe(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Marketplaces: config.MarketplaceConfigs{{Source: "tsukumogami/shirabe"}},
		},
	}

	// Seam returns an error: must not panic or propagate.
	var buf bytes.Buffer
	a := &Applier{
		Reporter: NewReporter(&buf),
		reconcileMarketplaces: func(map[string]pluginrecord.MarketplaceWant) (pluginrecord.ReconcileReport, error) {
			return pluginrecord.ReconcileReport{}, os.ErrPermission
		},
	}
	a.reconcileMarketplaceRegistry(cfg, nil, "")

	// Nil seam: silent no-op, no panic.
	b := &Applier{Reporter: NewReporter(&bytes.Buffer{})}
	b.reconcileMarketplaceRegistry(cfg, nil, "")
}
