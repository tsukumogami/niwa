package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/envformat"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/keyreport"
)

// strictWorkspaceFixture writes a workspace whose single declared secret has
// no source at all -- no vault provider is configured, so nothing ever tries
// to resolve it. That is the shape the tolerant default exists to let through,
// which makes it the right shape to prove strict mode stops.
func strictWorkspaceFixture(t *testing.T, name string) (cfg *config.WorkspaceConfig, niwaDir, workspaceRoot, instanceRoot string) {
	t.Helper()

	configTOML := "[workspace]\nname = \"" + name + "\"\n" + `
[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[env.secrets.required]
ANTHROPIC_API_KEY = "API key for Claude Code sessions"
`

	workspaceRoot = t.TempDir()
	niwaDir = filepath.Join(workspaceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	instanceRoot = filepath.Join(workspaceRoot, name)
	if err := os.MkdirAll(filepath.Join(instanceRoot, "default", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return parsed.Config, niwaDir, workspaceRoot, instanceRoot
}

func strictApplier(t *testing.T, strict bool) (*Applier, *keyreport.Collector) {
	t.Helper()
	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}
	a := NewApplier(mockClient)
	a.Cloner = &Cloner{}
	collector := keyreport.New()
	a.Keys = collector
	a.StrictSecrets = strict
	return a, collector
}

// TestStrictModeTurnsACollectedMarkFatal is the gate beside the post-merge
// check. The key here is one checkRequiredKeys deliberately tolerates -- no
// provider was ever configured, so nothing establishes a fault with an owner --
// and strict mode is what turns it into a refusal.
func TestStrictModeTurnsACollectedMarkFatal(t *testing.T) {
	cfg, niwaDir, workspaceRoot, instanceRoot := strictWorkspaceFixture(t, "strict-ws")
	applier, _ := strictApplier(t, true)

	_, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name)
	if err == nil {
		t.Fatal("strict mode must refuse a run with a declared key it could not supply")
	}
	if !errors.Is(err, ErrStrictSecrets) {
		t.Errorf("error must wrap ErrStrictSecrets so a caller can tell a refusal from a defect, got: %v", err)
	}
	if !strings.Contains(err.Error(), "strict") {
		t.Errorf("error must say strict mode caused it, got: %v", err)
	}

	// A strict failure leaves no instance behind, which is create's existing
	// failure semantics rather than a new rule: runPipeline's error path removes
	// the directory it made.
	if _, statErr := os.Stat(instanceRoot); !os.IsNotExist(statErr) {
		t.Errorf("instance directory survived a strict failure: stat(%s) err = %v", instanceRoot, statErr)
	}
}

// TestTolerantModeMaterializesTheSameWorkspace is the control: the fixture
// above must be a run that succeeds when nothing asked for strictness, or the
// test above proves nothing about strict mode.
func TestTolerantModeMaterializesTheSameWorkspace(t *testing.T) {
	cfg, niwaDir, workspaceRoot, _ := strictWorkspaceFixture(t, "tolerant-ws")
	applier, collector := strictApplier(t, false)

	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot, cfg.Workspace.Name); err != nil {
		t.Fatalf("tolerant mode must materialize despite the shortfall, got: %v", err)
	}
	if collector.Empty() {
		t.Error("the run reported no unresolved key, so the strict test above is not exercising a shortfall")
	}
}

// TestStrictShortfallErrorReadsTheCollector covers the gate's two degenerate
// inputs directly: nothing collected is not a failure, and a caller that
// supplied no collector cannot enumerate what it would fail on and so must not
// fail.
func TestStrictShortfallErrorReadsTheCollector(t *testing.T) {
	if err := strictShortfallError(nil); err != nil {
		t.Errorf("a nil collector must not fail a run, got: %v", err)
	}
	if err := strictShortfallError(keyreport.New()); err != nil {
		t.Errorf("an empty report must not fail a run, got: %v", err)
	}

	c := keyreport.New()
	c.Add(keyreport.Entry{Scope: "env.secrets", Key: "ONE", Cause: keyreport.CauseNoSource})
	err := strictShortfallError(c)
	if err == nil {
		t.Fatal("a collected shortfall must fail under strict mode")
	}
	if !strings.Contains(err.Error(), "1 declared env key has") {
		t.Errorf("single-key error should read as singular, got: %v", err)
	}
	// The error counts, it does not list: the surface renders the report on the
	// way out, and repeating the keys here would print them twice.
	if strings.Contains(err.Error(), "ONE") {
		t.Errorf("error must not duplicate the report's enumeration, got: %v", err)
	}
}

// TestOverlayCannotSetStrictMode is R13's structural half. It is asserted
// against the merge rather than the parser because that is where the guarantee
// actually lives: WorkspaceOverlay carries no workspace configuration
// MergeWorkspaceOverlay reads, and the merge never assigns merged.Workspace at
// all. A future merge that started copying the table would fail here.
func TestOverlayCannotSetStrictMode(t *testing.T) {
	base := &config.WorkspaceConfig{}
	base.Workspace.Name = "ws"

	strict := true
	overlay := &config.WorkspaceOverlay{}
	overlay.Workspace.StrictSecrets = &strict

	merged, err := MergeWorkspaceOverlay(base, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	if merged.Workspace.StrictSecrets != nil {
		t.Errorf("overlay set strict mode on the merged config (%v); a visibility overlay must not be able to change what a contributor's first run does",
			*merged.Workspace.StrictSecrets)
	}

	// The same overlay against a workspace that turned strict mode ON must not
	// be able to turn it off either -- the tombstone is inert in both
	// directions.
	off := false
	overlay.Workspace.StrictSecrets = &off
	base.Workspace.StrictSecrets = &strict
	merged, err = MergeWorkspaceOverlay(base, overlay, t.TempDir())
	if err != nil {
		t.Fatalf("MergeWorkspaceOverlay: %v", err)
	}
	if merged.Workspace.StrictSecrets == nil || !*merged.Workspace.StrictSecrets {
		t.Error("overlay de-escalated the base workspace's strict mode")
	}
}

// TestOverlayStrictModeWarnsAndDoesNothing runs the tombstone end to end
// through the pipeline that parses the overlay: an overlay that turns strict
// mode on gets the author a warning, and the run it was trying to make strict
// still materializes.
func TestOverlayStrictModeWarnsAndDoesNothing(t *testing.T) {
	configTOML := `
[workspace]
name = "overlay-strict-ws"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"

[env.secrets.required]
ANTHROPIC_API_KEY = "API key for Claude Code sessions"
`
	niwaDir, instanceRoot := setupTestWorkspace(t, configTOML, nil, []struct{ group, name string }{
		{"all", "repo1"},
	})
	parsed, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	overlayDir := setupOverlayDir(t, "[workspace]\nstrict_secrets = true\n")

	if err := SaveState(instanceRoot, &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "overlay-strict-ws",
		InstanceNumber: 1,
		Root:           instanceRoot,
		OverlayURL:     "testorg/overlay-strict-ws-overlay",
		OverlayCommit:  "abc123",
	}); err != nil {
		t.Fatal(err)
	}

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	}
	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	applier.Keys = keyreport.New()
	var out strings.Builder
	applier.Reporter = NewReporterWithTTY(&out, false)
	applier.cloneOrSync = func(_ context.Context, _, dir string) (bool, int, error) {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			return false, 0, err
		}
		data, readErr := os.ReadFile(filepath.Join(overlayDir, "workspace-overlay.toml"))
		if readErr != nil {
			return false, 0, readErr
		}
		return false, 0, os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), data, 0o644)
	}
	applier.headSHA = func(string) (string, error) { return "abc123", nil }

	if err := applier.Apply(context.Background(), parsed.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("the overlay's strict_secrets took effect and failed the run: %v", err)
	}
	if applier.Keys.Empty() {
		t.Fatal("no shortfall was collected, so this run would not have failed even under strict mode")
	}
	if !strings.Contains(out.String(), "strict_secrets") {
		t.Errorf("no warning told the overlay author their setting is inert:\n%s", out.String())
	}
}

// promoteCtx builds the minimal MaterializeContext the promote branch reads:
// one promoted key that resolved to nothing, present in the unresolved set.
func promoteCtx(strict bool, worktree bool) *MaterializeContext {
	cfg := &config.WorkspaceConfig{}
	cfg.Claude.Env.Promote = []string{"ANTHROPIC_API_KEY"}

	omitted := map[string]unresolvedEnvKey{
		"ANTHROPIC_API_KEY": {
			Scope: "env.secrets",
			Record: envformat.Record{
				Level: string(config.LevelRequired),
				Cause: string(config.CauseProviderUnreachable),
			},
		},
	}

	ctx := &MaterializeContext{
		Config:        cfg,
		Effective:     EffectiveConfig{Claude: cfg.Claude},
		Keys:          keyreport.New(),
		StrictSecrets: strict,
	}
	if worktree {
		// A non-nil InheritedEnv is what identifies the worktree path, and its
		// unresolved set comes from the clone's records rather than from marks.
		ctx.InheritedEnv = map[string]string{}
		ctx.InheritedUnresolved = omitted
	} else {
		ctx.UnresolvedEnv = omitted
	}
	return ctx
}

// TestPromoteStrictArm: the promote branch gets its own strict arm because it
// runs per-repo, after the post-merge gate has already passed.
func TestPromoteStrictArm(t *testing.T) {
	_, _, err := resolveClaudeEnvVars(promoteCtx(true, false))
	if err == nil {
		t.Fatal("strict mode must refuse to promote a key that has no value")
	}
	if !errors.Is(err, ErrStrictSecrets) {
		t.Errorf("promote refusal must wrap ErrStrictSecrets, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("promote refusal must name the key, got: %v", err)
	}
}

// TestPromoteTolerantArmStillOmitsAndReports is the control for the arm above.
func TestPromoteTolerantArmStillOmitsAndReports(t *testing.T) {
	ctx := promoteCtx(false, false)
	vars, _, err := resolveClaudeEnvVars(ctx)
	if err != nil {
		t.Fatalf("tolerant promotion must omit and report, got: %v", err)
	}
	if _, ok := vars["ANTHROPIC_API_KEY"]; ok {
		t.Error("an unresolved key must be omitted from the promoted vars, not promoted empty")
	}
	if ctx.Keys.Empty() {
		t.Error("the omission was not reported")
	}
}

// TestWorktreeRematerializationIsNeverStrict is R21, recorded where it can
// actually break. The exemption holds by omission -- the worktree path resolves
// no secrets, so it reaches no consult site and never sets StrictSecrets -- and
// this pins the behaviour that omission produces, so an implementer who later
// threads strictness into that path (the stale comment this work deleted used
// to invite exactly that) fails here.
func TestWorktreeRematerializationIsNeverStrict(t *testing.T) {
	in := repoMaterializeInputs{
		InheritedEnv:        map[string]string{},
		InheritedUnresolved: map[string]unresolvedEnvKey{},
	}
	if in.StrictSecrets {
		t.Fatal("the worktree path's materializer inputs default to strict")
	}

	ctx := promoteCtx(false, true)
	if _, _, err := resolveClaudeEnvVars(ctx); err != nil {
		t.Fatalf("worktree re-materialization must tolerate an omitted promoted key, got: %v", err)
	}
}
