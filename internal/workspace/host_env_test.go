package workspace

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// hostEnvOverlay is the shape the host-local layer exists for: a personal
// overlay that turns a session storage backend on for every host, with an
// inline [claude.env.vars] switch and promoted credentials it supplies through
// [env.secrets], plus one more inline var that must keep arriving.
func hostEnvOverlay() config.GlobalOverride {
	return config.GlobalOverride{
		Claude: &config.ClaudeOverride{Env: config.ClaudeEnvConfig{
			Promote: []string{"KOTO_SESSION_CLOUD_BUCKET", "KOTO_SESSION_CLOUD_SECRET_KEY"},
			Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
				"KOTO_SESSION_BACKEND": {Plain: "cloud"},
				"OVERLAY_FLAG":         {Plain: "on"},
			}},
		}},
		Env: config.EnvConfig{Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
			"KOTO_SESSION_CLOUD_BUCKET":     {Plain: "overlay-bucket"},
			"KOTO_SESSION_CLOUD_SECRET_KEY": {Plain: "overlay-secret"},
		}}},
	}
}

// hostEnvCtx merges the personal overlay into a one-repo team config the way
// apply does, attaches the host layer, and returns the repo's materialize
// context.
func hostEnvCtx(t *testing.T, host *config.HostEnvLayer) *MaterializeContext {
	t.Helper()
	team := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws"},
		Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
			"TEAM_KEY": {Plain: "team"},
		}}},
	}
	merged, err := MergeGlobalOverride(team, hostEnvOverlay(), t.TempDir())
	if err != nil {
		t.Fatalf("MergeGlobalOverride: %v", err)
	}
	merged.HostEnv = host
	return &MaterializeContext{
		Config:    merged,
		Effective: MergeOverrides(merged, "app"),
		RepoName:  "app",
		ConfigDir: t.TempDir(),
		Stderr:    &bytes.Buffer{},
	}
}

func TestHostEnvClearsAndOverridesOverlayKeys(t *testing.T) {
	ctx := hostEnvCtx(t, &config.HostEnvLayer{
		Env: config.HostEnvTable{Vars: map[string]string{"KOTO_SESSION_CLOUD_SECRET_KEY": "host-secret"}},
		Claude: config.HostEnvTable{
			Unset: []string{"KOTO_SESSION_BACKEND", "KOTO_SESSION_CLOUD_BUCKET"},
		},
	})

	session, _, err := resolveClaudeEnvVars(ctx)
	if err != nil {
		t.Fatalf("resolveClaudeEnvVars: %v", err)
	}
	want := map[string]string{
		"KOTO_SESSION_CLOUD_SECRET_KEY": "host-secret",
		"OVERLAY_FLAG":                  "on",
	}
	if !reflect.DeepEqual(session, want) {
		t.Errorf("session env = %v, want %v", session, want)
	}
	if s := ctx.Stderr.(*bytes.Buffer).String(); s != "" {
		t.Errorf("a key the host cleared must not warn, stderr = %q", s)
	}

	repoEnv, _, err := ResolveEnvVars(ctx)
	if err != nil {
		t.Fatalf("ResolveEnvVars: %v", err)
	}
	// [claude.env] unset clears the session only: the repo env file still
	// carries the overlay's bucket, and the host's [env] value replaced the
	// overlay's secret.
	wantEnv := map[string]string{
		"TEAM_KEY":                      "team",
		"KOTO_SESSION_CLOUD_BUCKET":     "overlay-bucket",
		"KOTO_SESSION_CLOUD_SECRET_KEY": "host-secret",
	}
	if !reflect.DeepEqual(repoEnv, wantEnv) {
		t.Errorf("repo env = %v, want %v", repoEnv, wantEnv)
	}
}

func TestHostEnvClaudeVarReplacesOverlayVar(t *testing.T) {
	ctx := hostEnvCtx(t, &config.HostEnvLayer{
		Claude: config.HostEnvTable{Vars: map[string]string{"KOTO_SESSION_BACKEND": "local"}},
	})
	session, _, err := resolveClaudeEnvVars(ctx)
	if err != nil {
		t.Fatalf("resolveClaudeEnvVars: %v", err)
	}
	want := map[string]string{
		"KOTO_SESSION_BACKEND":          "local",
		"KOTO_SESSION_CLOUD_BUCKET":     "overlay-bucket",
		"KOTO_SESSION_CLOUD_SECRET_KEY": "overlay-secret",
		"OVERLAY_FLAG":                  "on",
	}
	if !reflect.DeepEqual(session, want) {
		t.Errorf("session env = %v, want %v", session, want)
	}
}

func TestHostEnvEnvUnsetDropsKeyFromRepoEnvAndPromote(t *testing.T) {
	ctx := hostEnvCtx(t, &config.HostEnvLayer{
		Env: config.HostEnvTable{Unset: []string{"KOTO_SESSION_CLOUD_SECRET_KEY"}},
	})
	repoEnv, _, err := ResolveEnvVars(ctx)
	if err != nil {
		t.Fatalf("ResolveEnvVars: %v", err)
	}
	if _, ok := repoEnv["KOTO_SESSION_CLOUD_SECRET_KEY"]; ok {
		t.Errorf("repo env still carries the cleared key: %v", repoEnv)
	}
	ctx.StrictSecrets = true
	session, _, err := resolveClaudeEnvVars(ctx)
	if err != nil {
		t.Fatalf("a promoted key the host cleared must not refuse under strict mode: %v", err)
	}
	if _, ok := session["KOTO_SESSION_CLOUD_SECRET_KEY"]; ok {
		t.Errorf("session env still carries the cleared key: %v", session)
	}
	if session["KOTO_SESSION_CLOUD_BUCKET"] != "overlay-bucket" {
		t.Errorf("the other promoted key must still arrive: %v", session)
	}
}

// The host layer is later than a discovered repo env file, which is the
// highest-priority config source.
func TestHostEnvWinsOverDiscoveredRepoEnvFile(t *testing.T) {
	ctx := hostEnvCtx(t, &config.HostEnvLayer{
		Env: config.HostEnvTable{Vars: map[string]string{"FROM_FILE": "host"}},
	})
	envFile := filepath.Join(t.TempDir(), "app.env")
	if err := os.WriteFile(envFile, []byte("FROM_FILE=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx.DiscoveredEnv = &DiscoveredEnv{RepoFiles: map[string]string{"app": envFile}}
	repoEnv, _, err := ResolveEnvVars(ctx)
	if err != nil {
		t.Fatalf("ResolveEnvVars: %v", err)
	}
	if repoEnv["FROM_FILE"] != "host" {
		t.Errorf("FROM_FILE = %q, want the host value", repoEnv["FROM_FILE"])
	}
}

func TestHostEnvDoesNotMutateSharedSessionEnv(t *testing.T) {
	ctx := hostEnvCtx(t, &config.HostEnvLayer{
		Claude: config.HostEnvTable{Unset: []string{"SHARED"}},
	})
	ctx.SessionEnv = map[string]string{"SHARED": "v"}
	before := maps.Clone(ctx.SessionEnv)
	if _, _, err := resolveClaudeEnvVars(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ctx.SessionEnv, before) {
		t.Errorf("SessionEnv mutated: %v", ctx.SessionEnv)
	}
}

func TestHostEnvNilLeavesOutputsUnchanged(t *testing.T) {
	without := hostEnvCtx(t, nil)
	with := hostEnvCtx(t, &config.HostEnvLayer{})
	for _, ctx := range []*MaterializeContext{without, with} {
		ctx.ConfigDir = without.ConfigDir
	}
	a, aSrc, err := resolveClaudeEnvVars(without)
	if err != nil {
		t.Fatal(err)
	}
	b, bSrc, err := resolveClaudeEnvVars(with)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(aSrc, bSrc) {
		t.Errorf("an empty host layer changed the session env: %v vs %v", a, b)
	}
	c, cSrc, err := ResolveEnvVars(without)
	if err != nil {
		t.Fatal(err)
	}
	d, dSrc, err := ResolveEnvVars(with)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, d) || !reflect.DeepEqual(cSrc, dSrc) {
		t.Errorf("an empty host layer changed the repo env: %v vs %v", c, d)
	}
}

func TestHostEnvOverridesNamesTheLayer(t *testing.T) {
	team := &config.WorkspaceConfig{
		Env: config.EnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
			"TEAM_KEY": {Plain: "team"},
		}}},
		Repos: map[string]config.RepoOverride{"app": {Claude: &config.ClaudeOverride{
			Env: config.ClaudeEnvConfig{Promote: []string{"TEAM_KEY"}},
		}}},
	}
	personal := hostEnvOverlay()
	host := &config.HostEnvLayer{
		Env: config.HostEnvTable{
			Vars:  map[string]string{"KOTO_SESSION_CLOUD_SECRET_KEY": "host-secret-value"},
			Unset: []string{"TEAM_KEY"},
		},
		Claude: config.HostEnvTable{
			Vars:  map[string]string{"NEW_KEY": "host-new-value"},
			Unset: []string{"KOTO_SESSION_BACKEND", "KOTO_SESSION_CLOUD_BUCKET", "TEAM_KEY"},
		},
	}
	got := HostEnvOverrides(team, &personal, host)
	want := []HostEnvOverride{
		{Table: "env unset", Key: "TEAM_KEY", Over: hostOverLayerWorkspace},
		{Table: "env.vars", Key: "KOTO_SESSION_CLOUD_SECRET_KEY", Over: hostOverLayerPersonal},
		{Table: "claude.env unset", Key: "KOTO_SESSION_BACKEND", Over: hostOverLayerPersonal},
		{Table: "claude.env unset", Key: "KOTO_SESSION_CLOUD_BUCKET", Over: hostOverLayerPersonal},
		{Table: "claude.env unset", Key: "TEAM_KEY", Over: hostOverLayerWorkspace},
		{Table: "claude.env.vars", Key: "NEW_KEY", Over: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HostEnvOverrides =\n%v\nwant\n%v", got, want)
	}

	// The lines name tables, keys and layers, and never a value: not the
	// host's, and not the overlay's, even for a promoted secret.
	var lines []string
	for _, o := range got {
		lines = append(lines, o.Line())
	}
	joined := strings.Join(lines, "\n")
	for _, v := range []string{"host-secret-value", "host-new-value", "overlay-secret", "overlay-bucket", "cloud", "team\""} {
		if strings.Contains(joined, v) {
			t.Errorf("report carries value %q:\n%s", v, joined)
		}
	}
	if !strings.Contains(joined, `host-local claude.env unset "KOTO_SESSION_BACKEND" wins over the personal overlay`) {
		t.Errorf("report = \n%s", joined)
	}
	if !strings.Contains(joined, `host-local claude.env.vars "NEW_KEY": no other config layer sets this key`) {
		t.Errorf("report = \n%s", joined)
	}

	if HostEnvOverrides(team, &personal, nil) != nil {
		t.Error("a nil host layer must report nothing")
	}
}

func TestApplierHostEnvRefusesInvalidLayer(t *testing.T) {
	a := &Applier{
		Reporter: NewReporter(&bytes.Buffer{}),
		MachineConfig: &config.GlobalConfig{Claude: config.HostClaudeTable{Env: config.HostEnvTable{
			Vars:  map[string]string{"K": "v"},
			Unset: []string{"K"},
		}}},
	}
	if _, err := a.hostEnv(); err == nil || !strings.Contains(err.Error(), "K") {
		t.Fatalf("hostEnv() error = %v, want a refusal naming the key", err)
	}
	a.MachineConfig = &config.GlobalConfig{}
	if layer, err := a.hostEnv(); err != nil || layer != nil {
		t.Fatalf("hostEnv() = %v, %v; want no layer and no error", layer, err)
	}
}

// The worktree path re-materializes the session env from the clone's env file,
// so it applies the host [claude.env] table itself.
func TestApplyToWorktreeAppliesHostClaudeEnv(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := worktreeWithCloneEnv(t, "KOTO_SESSION_BACKEND=cloud\nOTHER_KEY=present\n")
	cfg.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"KOTO_SESSION_BACKEND", "OTHER_KEY"}}
	cfg.HostEnv = &config.HostEnvLayer{Claude: config.HostEnvTable{Unset: []string{"KOTO_SESSION_BACKEND"}}}

	var stderr bytes.Buffer
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "p", "b",
		WorktreeApplyOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("ApplyToWorktree: %v", err)
	}
	env := worktreeSettingsEnv(t, worktreePath)
	if _, ok := env["KOTO_SESSION_BACKEND"]; ok {
		t.Errorf("the worktree session env carries a key the host cleared: %v", env)
	}
	if env["OTHER_KEY"] != "present" {
		t.Errorf("OTHER_KEY = %v, want the clone's value", env["OTHER_KEY"])
	}
	if stderr.Len() != 0 {
		t.Errorf("a key the host cleared must not warn, stderr = %q", stderr.String())
	}
}
