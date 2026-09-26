package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
)

// TestPromoteDeclaredWithNoValue is the escaped-defect regression. A key
// declared in a requirement sub-table with no binding and no value carries no
// mark — the resolver never visits it — so the promote branch's unresolved set
// had nothing to match on and the key took the arm meant for a typo in the
// promote list. Every promote path is covered here: the per-repo materializer,
// the workspace root (where it escaped), and the worktree re-materialization.
func TestPromoteDeclaredWithNoValue(t *testing.T) {
	const desc = "GitHub token. Without one, API calls are unauthenticated."

	t.Run("per-repo path omits and reports", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, map[string]config.MaybeSecret{"OTHER": {Plain: "v"}})
		ctx.Effective.Env.Secrets.Recommended = map[string]string{"GH_TOKEN": desc}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"GH_TOKEN"}}
		keys := keyreport.New()
		ctx.Keys = keys

		got, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("a declared key with no value is a shortfall, not a typo: %v", err)
		}
		if _, ok := got["GH_TOKEN"]; ok {
			t.Errorf("a key with no value was promoted anyway: %v", got)
		}
		report := keys.Report()
		if len(report) != 1 || report[0].Key != "GH_TOKEN" {
			t.Fatalf("report = %v, want the omitted key", report)
		}
		if report[0].Level != config.LevelRecommended || report[0].Cause != keyreport.CauseNoSource {
			t.Errorf("report entry lost the declaration's metadata: %+v", report[0])
		}
		if report[0].Description != desc {
			t.Errorf("report entry dropped the author's description: %+v", report[0])
		}
	})

	t.Run("workspace root path installs settings", func(t *testing.T) {
		cfg := &config.WorkspaceConfig{}
		cfg.Env.Secrets.Recommended = map[string]string{"GH_TOKEN": desc}
		cfg.Claude.Env.Promote = []string{"GH_TOKEN"}
		cfg.Claude.Env.Vars.Values = map[string]config.MaybeSecret{"KEPT": {Plain: "yes"}}

		configDir, instanceRoot := rootSettingsDirs(t)
		if _, err := (&RootSettingsMaterializer{}).Materialize(&MaterializeContext{Config: cfg, ConfigDir: configDir, RepoDir: instanceRoot}); err != nil {
			t.Fatalf("workspace root settings must survive a promoted key with no value: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(instanceRoot, ".claude", "settings.json"))
		if err != nil {
			t.Fatalf("reading root settings: %v", err)
		}
		var doc struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if _, ok := doc.Env["GH_TOKEN"]; ok {
			t.Errorf("a key with no value reached the settings env block: %v", doc.Env)
		}
		if doc.Env["KEPT"] != "yes" {
			t.Errorf("the omission took the rest of the env block with it: %v", doc.Env)
		}
	})

	t.Run("workspace root path still rejects a typo", func(t *testing.T) {
		cfg := &config.WorkspaceConfig{}
		cfg.Env.Secrets.Recommended = map[string]string{"GH_TOKEN": desc}
		cfg.Claude.Env.Promote = []string{"GH_TOEKN"}

		configDir, instanceRoot := rootSettingsDirs(t)
		_, err := (&RootSettingsMaterializer{}).Materialize(&MaterializeContext{Config: cfg, ConfigDir: configDir, RepoDir: instanceRoot})
		if err == nil {
			t.Fatal("a promoted key declared nowhere must stay a hard error")
		}
		if !strings.Contains(err.Error(), "GH_TOEKN") {
			t.Errorf("error = %q, want it to name the misspelled key", err)
		}
	})

	t.Run("optional declarations count too", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, nil)
		ctx.Effective.Env.Secrets.Optional = map[string]string{"TAVILY_API_KEY": "search provider"}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"TAVILY_API_KEY"}}
		if _, _, err := resolveClaudeEnvVars(ctx); err != nil {
			t.Fatalf("an optional declaration is still a declaration: %v", err)
		}
	})

	t.Run("worktree path omits and reports", func(t *testing.T) {
		// The worktree path recovers its set from the records in the clone's
		// env file, and this shape leaves no record behind — nothing marked
		// it. The declaration walk is what covers the key on that path.
		ctx, _ := unresolvedEnvCtx(t, nil)
		ctx.Effective.Env.Secrets.Recommended = map[string]string{"GH_TOKEN": desc}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"GH_TOKEN"}}
		ctx.InheritedEnv = map[string]string{"OTHER": "x"}
		ctx.InheritedUnresolved = map[string]unresolvedEnvKey{}
		ctx.Keys = keyreport.New()

		got, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("worktree re-materialization must tolerate the same shape: %v", err)
		}
		if _, ok := got["GH_TOKEN"]; ok {
			t.Errorf("a key with no value was promoted anyway: %v", got)
		}
		if ctx.Keys.Empty() {
			t.Error("the omission was not reported")
		}
	})

	t.Run("strict mode still refuses", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, nil)
		ctx.Effective.Env.Secrets.Recommended = map[string]string{"GH_TOKEN": desc}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"GH_TOKEN"}}
		ctx.StrictSecrets = true

		_, _, err := resolveClaudeEnvVars(ctx)
		if err == nil {
			t.Fatal("strict mode must refuse a promoted key with no value, whichever shape it is")
		}
		if !errors.Is(err, ErrStrictSecrets) {
			t.Errorf("refusal must wrap ErrStrictSecrets, got: %v", err)
		}
	})
}

// rootSettingsDirs returns a config dir and an instance root for a workspace
// root settings install.
func rootSettingsDirs(t *testing.T) (configDir, instanceRoot string) {
	t.Helper()
	tmp := t.TempDir()
	configDir = filepath.Join(tmp, "config")
	instanceRoot = filepath.Join(tmp, "instance")
	for _, d := range []string{configDir, instanceRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return configDir, instanceRoot
}
