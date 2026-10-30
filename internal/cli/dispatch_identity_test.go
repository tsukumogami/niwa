package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// identityShape is the alphabet tools reading NIWA_DISPATCH_ID and
// NIWA_WORKSPACE rely on, written out here rather than taken from the
// production pattern so a change to that pattern can't pass on its own.
var identityShape = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// TestApplyLaunchEnv_DispatchIdentity: a launch given a dispatch id and a
// workspace name sets both in its process environment and its settings env,
// replacing inherited values rather than adding beside them.
func TestApplyLaunchEnv_DispatchIdentity(t *testing.T) {
	settings := map[string]any{}
	base := []string{"PATH=/usr/bin", "NIWA_DISPATCH_ID=stale-id", "NIWA_WORKSPACE=stale-ws"}
	env := applyLaunchEnv(claudeLaunchSpec(), settings, base, launchEnvShape{
		ReapKey:    "dispatch-id-1",
		DispatchID: "dispatch-id-1",
		Workspace:  "test-ws",
	})
	if v, _ := envValue(t, env, config.DispatchIDEnv); v != "dispatch-id-1" {
		t.Errorf("process NIWA_DISPATCH_ID = %q, want dispatch-id-1", v)
	}
	if v, _ := envValue(t, env, config.WorkspaceEnv); v != "test-ws" {
		t.Errorf("process NIWA_WORKSPACE = %q, want test-ws", v)
	}
	doc, _ := renderLaunchSettings(settings)
	const want = `{"env":{"NIWA_REAP_KEY":"dispatch-id-1","NIWA_DISPATCH_ID":"dispatch-id-1","NIWA_WORKSPACE":"test-ws"}}`
	if doc != want {
		t.Errorf("settings = %s, want %s", doc, want)
	}
}

// TestApplyLaunchEnv_DispatchIdentityOutsideAlphabet: a value outside
// [A-Za-z0-9._:-]{1,128}, or none, is not set, and an inherited value is
// removed all the same.
func TestApplyLaunchEnv_DispatchIdentityOutsideAlphabet(t *testing.T) {
	for _, bad := range []string{"", "has space", "a/b", "a@b", "a,b=c", "quote'd", "$(x)", strings.Repeat("a", 129)} {
		settings := map[string]any{}
		base := []string{"NIWA_DISPATCH_ID=stale-id", "NIWA_WORKSPACE=stale-ws", "HOME=/h"}
		env := applyLaunchEnv(claudeLaunchSpec(), settings, base, launchEnvShape{DispatchID: bad, Workspace: bad})
		for _, name := range []string{config.DispatchIDEnv, config.WorkspaceEnv} {
			if v, ok := envValue(t, env, name); ok {
				t.Errorf("value %q: process %s = %q, want unset", bad, name, v)
			}
		}
		if _, ok := settings["env"]; ok {
			t.Errorf("value %q: a settings env was written: %v", bad, settings)
		}
		if v, _ := envValue(t, env, "HOME"); v != "/h" {
			t.Errorf("value %q: an unrelated variable was lost", bad)
		}
	}
}

// TestDispatch_IdentityEnv: a worker `niwa dispatch` launches, detached or
// not, carries NIWA_DISPATCH_ID (its dispatch id, the same one its lineage
// attributes and session mapping hold) and NIWA_WORKSPACE (the workspace
// name), in its settings env and its launch environment, replacing whatever
// the dispatcher inherited. Its launch record keeps both for a resume.
func TestDispatch_IdentityEnv(t *testing.T) {
	for _, detach := range []bool{true, false} {
		t.Run(fmt.Sprintf("detach=%v", detach), func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			f := installDispatchFakes(t, root)
			var reqs []launchRequest
			captureLaunchRequest(f, &reqs)
			dispatchDetach = detach
			t.Setenv(config.DispatchIDEnv, "stale-id")
			t.Setenv(config.WorkspaceEnv, "stale-ws")

			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			if len(reqs) != 1 {
				t.Fatalf("launches = %d, want 1", len(reqs))
			}
			mapping := readSingleMappingJSON(t, root)
			dispatchID, _ := mapping["dispatch_id"].(string)
			want := map[string]string{config.DispatchIDEnv: dispatchID, config.WorkspaceEnv: "test-ws"}

			settingsEnv, ok := settingsEnvOf(t, reqs[0].Passthrough)
			if !ok {
				t.Fatalf("no settings document in %v", reqs[0].Passthrough)
			}
			attrs, _ := attributeMap(t, settingsEnv["OTEL_RESOURCE_ATTRIBUTES"])
			for name, w := range want {
				if !identityShape.MatchString(w) {
					t.Fatalf("%s = %q doesn't fit the identity alphabet", name, w)
				}
				if v, _ := envValue(t, reqs[0].Env, name); v != w {
					t.Errorf("launch %s = %q, want %q", name, v, w)
				}
				if settingsEnv[name] != w {
					t.Errorf("settings %s = %q, want %q", name, settingsEnv[name], w)
				}
			}
			if attrs[attrDispatchID] != dispatchID || attrs[attrWorkspace] != "test-ws" {
				t.Errorf("the lineage attributes and the variables disagree: %v", attrs)
			}

			rec := dispatchedLaunchRecord(t, root, f)
			for name, w := range want {
				if rec.Env[name] != w {
					t.Errorf("launch record %s = %q, want %q", name, rec.Env[name], w)
				}
			}
		})
	}
}

// TestDispatch_IdentityEnv_Codex: an agent with no settings document still
// gets both variables in its launch environment.
func TestDispatch_IdentityEnv_Codex(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var reqs []launchRequest
	captureLaunchRequest(f, &reqs)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	dispatchID, _ := readSingleMappingJSON(t, root)["dispatch_id"].(string)
	if v, _ := envValue(t, reqs[0].Env, config.DispatchIDEnv); v == "" || v != dispatchID {
		t.Errorf("launch NIWA_DISPATCH_ID = %q, want the dispatch id %q", v, dispatchID)
	}
	if v, _ := envValue(t, reqs[0].Env, config.WorkspaceEnv); v != "test-ws" {
		t.Errorf("launch NIWA_WORKSPACE = %q, want test-ws", v)
	}
}

// TestDispatch_IdentityEnv_WorkspaceNameOutsideAlphabet: a workspace name
// longer than 128 characters is left out of NIWA_WORKSPACE with a warning
// that names the variable and doesn't quote the name; the dispatch id is
// still set and the dispatch goes ahead.
func TestDispatch_IdentityEnv_WorkspaceNameOutsideAlphabet(t *testing.T) {
	root := setupDispatchWorkspace(t)
	long := strings.Repeat("w", 129)
	if err := os.WriteFile(filepath.Join(root, config.ConfigDir, config.ConfigFile), []byte("[workspace]\nname = \""+long+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var reqs []launchRequest
	captureLaunchRequest(f, &reqs)
	dispatchDetach = true
	t.Setenv(config.WorkspaceEnv, "stale-ws")

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if _, ok := envValue(t, reqs[0].Env, config.WorkspaceEnv); ok {
		t.Error("NIWA_WORKSPACE was set from a name outside its alphabet")
	}
	if settingsEnv, _ := settingsEnvOf(t, reqs[0].Passthrough); settingsEnv[config.WorkspaceEnv] != "" {
		t.Errorf("settings NIWA_WORKSPACE = %q, want unset", settingsEnv[config.WorkspaceEnv])
	}
	if v, _ := envValue(t, reqs[0].Env, config.DispatchIDEnv); !identityShape.MatchString(v) {
		t.Errorf("launch NIWA_DISPATCH_ID = %q, want the dispatch id", v)
	}
	if !strings.Contains(stderr, config.WorkspaceEnv+" left out") || strings.Contains(stderr, long) {
		t.Errorf("stderr = %q, want a warning naming %s and not quoting the name", stderr, config.WorkspaceEnv)
	}
}

// TestWatchReviewLaunch_NoDispatchIdentity: a `niwa watch` review and its
// continuation are not dispatches, so neither carries NIWA_DISPATCH_ID or
// NIWA_WORKSPACE, even when the process running watch inherited them.
func TestWatchReviewLaunch_NoDispatchIdentity(t *testing.T) {
	root, home := setupWatchCharEnv(t)
	fakes := installWatchCharFakes(t)
	stubWatchTrust(t, home, false)
	client := newPullHeadServer(t)
	cmd, _, _ := newWatchCharCmd()
	t.Setenv(config.DispatchIDEnv, "inherited-id")
	t.Setenv(config.WorkspaceEnv, "inherited-ws")

	if err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{}); err != nil {
		t.Fatalf("stageReview: %v", err)
	}
	rec, err := watch.LoadStagedRecord(root, "watch_acme_widget_7")
	if err != nil {
		t.Fatal(err)
	}
	launches := map[string]launchRequest{
		"review":       fakes.launches[0],
		"continuation": watchResumeLaunch(rec.InstancePath, rec.Handle, watchCharSessionID, "again", false, watchLaunchEnv(root, rec.ReapKey)),
	}
	for what, l := range launches {
		settingsEnv, _ := settingsEnvOf(t, l.Passthrough)
		for _, name := range []string{config.DispatchIDEnv, config.WorkspaceEnv} {
			if v, ok := envValue(t, l.Env, name); ok {
				t.Errorf("%s launch %s = %q, want unset", what, name, v)
			}
			if v, ok := settingsEnv[name]; ok {
				t.Errorf("%s settings %s = %q, want unset", what, name, v)
			}
		}
	}
}

// TestResume_KeepsDispatchIdentity: a launch record holding the two
// variables passes the resume check, and the resumed worker gets the
// recorded values in place of inherited ones.
func TestResume_KeepsDispatchIdentity(t *testing.T) {
	const id = resumeTestDispatchID
	rec := workspace.LaunchRecord{
		DispatchID:   id,
		SessionID:    resumeTestSessionID,
		Agent:        string(agent.AgentClaude),
		InstanceName: resumeTestInstance,
		Settings:     `{"env":{"NIWA_REAP_KEY":"` + id + `","NIWA_DISPATCH_ID":"` + id + `","NIWA_WORKSPACE":"test-ws"}}`,
		Env:          map[string]string{config.ReapKeyEnv: id, config.DispatchIDEnv: id, config.WorkspaceEnv: "test-ws"},
	}
	if err := validateLaunchRecord(rec, claudeLaunchSpec(), nil); err != nil {
		t.Fatalf("a record with the dispatch identity was refused: %v", err)
	}
	env := resumeEnv([]string{"NIWA_DISPATCH_ID=stale-id", "NIWA_WORKSPACE=stale-ws"}, nil, rec.Env)
	if v, _ := envValue(t, env, config.DispatchIDEnv); v != id {
		t.Errorf("resumed NIWA_DISPATCH_ID = %q, want %q", v, id)
	}
	if v, _ := envValue(t, env, config.WorkspaceEnv); v != "test-ws" {
		t.Errorf("resumed NIWA_WORKSPACE = %q, want test-ws", v)
	}
}
