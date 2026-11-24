package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The compaction window: a dispatch profile's auto_compact_window reaches a
// Claude worker as CLAUDE_CODE_AUTO_COMPACT_WINDOW in its launch settings
// document's env object and nowhere else, every launch drops an inherited
// value, and resume and retask keep the window a worker launched with.

const windowEnv = config.AutoCompactWindowEnv

// windowedProfileDecl is a profile that sets only a window.
const windowedProfileDecl = `
[session.dispatch_profiles.worker]
auto_compact_window = 400000
`

// windowedCoordinatorDecl is the coordinator profile with a window too.
const windowedCoordinatorDecl = coordinatorProfileDecl + "auto_compact_window = 400000\n"

// defaultWorkerDecl makes the windowed worker profile the default, beside the
// coordinator profile.
const defaultWorkerDecl = "\n[session]\ndefault_dispatch_profile = \"worker\"\n" + windowedProfileDecl + coordinatorProfileDecl

// inheritedWindow is the value a launching shell exports in these tests. No
// launch may pass it on.
const inheritedWindow = "900000"

// exportWindow is a prep that exports the window, as a developer's shell or a
// dispatching worker might.
func exportWindow(t *testing.T) { t.Setenv(windowEnv, inheritedWindow) }

// both runs every prep in order.
func both(preps ...func(t *testing.T)) func(t *testing.T) {
	return func(t *testing.T) {
		for _, p := range preps {
			p(t)
		}
	}
}

// settingsEnv returns the env object of the one --settings document in argv,
// nil when the document has none.
func settingsEnv(t *testing.T, argv []string) map[string]any {
	t.Helper()
	env, _ := launchSettingsDoc(t, argv)["env"].(map[string]any)
	return env
}

// recordedSettingsEnv returns the env object of a recorded settings document.
func recordedSettingsEnv(t *testing.T, doc string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("decoding the recorded settings document: %v\n%s", err, doc)
	}
	env, _ := m["env"].(map[string]any)
	return env
}

// TestDispatchWindow_SettingsOnly: a windowed profile puts the window in the
// settings env object as a decimal string, keeps it out of the process
// environment and the record's Env, and leaves the both-places rule standing
// for the profile's other env value and its marker.
func TestDispatchWindow_SettingsOnly(t *testing.T) {
	for _, c := range []struct {
		name, decl, profile string
	}{
		{"window only", windowedProfileDecl, "worker"},
		{"window and drops", windowedCoordinatorDecl, "coordinator"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := runProfileFixtureDispatch(t, profileFixtureCase{name: "windowed", decl: c.decl}, withProfile(c.profile))
			env := settingsEnv(t, got.argv)
			if env[windowEnv] != "400000" {
				t.Errorf("settings env %s = %#v, want \"400000\"", windowEnv, env[windowEnv])
			}
			if v, ok := envLookup(got.req.Env, windowEnv); ok {
				t.Errorf("the process environment carries %s=%s", windowEnv, v)
			}
			if v, _ := envLookup(got.req.Env, config.DispatchProfileEnv); v != c.profile {
				t.Errorf("process %s = %q, want %q", config.DispatchProfileEnv, v, c.profile)
			}
			if c.profile == "coordinator" {
				if v, ok := envLookup(got.req.Env, "ENABLE_CLAUDEAI_MCP_SERVERS"); !ok || v != "false" {
					t.Errorf("process ENABLE_CLAUDEAI_MCP_SERVERS = %q (%v), want false in both places still", v, ok)
				}
			}
			rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
			if err != nil {
				t.Fatalf("reading the launch record: %v", err)
			}
			if _, ok := rec.Env[windowEnv]; ok {
				t.Errorf("the launch record's Env holds %s: %#v", windowEnv, rec.Env)
			}
			if recordedSettingsEnv(t, rec.Settings)[windowEnv] != "400000" {
				t.Errorf("the recorded settings document lacks the window: %s", rec.Settings)
			}
			if rec.DispatchProfile != c.profile {
				t.Errorf("record DispatchProfile = %q, want %q", rec.DispatchProfile, c.profile)
			}
		})
	}
}

// TestDispatchWindow_InheritedIsDropped: a dispatch from a shell that exports
// the window passes it on in neither place, under no profile (Claude and
// Codex, every no-profile fixture still byte for byte), under a profile with
// no window, and under a windowed profile, which sets its own value.
func TestDispatchWindow_InheritedIsDropped(t *testing.T) {
	for _, c := range noProfileCases {
		t.Run("no profile/"+c.name, func(t *testing.T) {
			got := runProfileFixtureDispatch(t, c, exportWindow)
			checkProfileFixture(t, "no_profile_"+c.name, got.argv)
			if v, ok := envLookup(got.req.Env, windowEnv); ok {
				t.Errorf("the worker inherited %s=%s", windowEnv, v)
			}
		})
	}
	t.Run("profile with no window", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "profiled", decl: coordinatorProfileDecl}, both(exportWindow, withProfile("coordinator")))
		if v, ok := settingsEnv(t, got.argv)[windowEnv]; ok {
			t.Errorf("settings env carries %s=%v", windowEnv, v)
		}
		if v, ok := envLookup(got.req.Env, windowEnv); ok {
			t.Errorf("the worker inherited %s=%s", windowEnv, v)
		}
	})
	t.Run("windowed profile", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "windowed", decl: windowedProfileDecl}, both(exportWindow, withProfile("worker")))
		if v := settingsEnv(t, got.argv)[windowEnv]; v != "400000" {
			t.Errorf("settings env %s = %#v, want the profile's \"400000\"", windowEnv, v)
		}
		if v, ok := envLookup(got.req.Env, windowEnv); ok {
			t.Errorf("the process environment carries %s=%s", windowEnv, v)
		}
	})
}

// TestDispatchDefaultProfile: with default_dispatch_profile set, a dispatch
// that names no profile resolves to it and applies its window, and the
// record, the mapping, niwa list and the audit line name it. --profile wins
// over the default.
func TestDispatchDefaultProfile(t *testing.T) {
	t.Run("default applies", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "default", decl: defaultWorkerDecl}, nil)
		env := settingsEnv(t, got.argv)
		if env[windowEnv] != "400000" || env[config.DispatchProfileEnv] != "worker" {
			t.Errorf("settings env = %#v, want the worker profile's window and marker", env)
		}
		if v, ok := envLookup(got.req.Env, windowEnv); ok {
			t.Errorf("the process environment carries %s=%s", windowEnv, v)
		}
		if !strings.Contains(got.stderr, `launched with dispatch profile "worker"`) {
			t.Errorf("the audit line doesn't name the default profile:\n%s", got.stderr)
		}
		rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
		if err != nil {
			t.Fatalf("reading the launch record: %v", err)
		}
		if rec.DispatchProfile != "worker" {
			t.Errorf("record DispatchProfile = %q, want worker", rec.DispatchProfile)
		}
		m, err := workspace.ReadSessionMapping(got.root, dispatchTestSessionID)
		if err != nil {
			t.Fatalf("reading the session mapping: %v", err)
		}
		if m.DispatchProfile != "worker" {
			t.Errorf("mapping DispatchProfile = %q, want worker", m.DispatchProfile)
		}
		_, recs := runListJSON(t)
		found := false
		for _, r := range recs {
			if r["dispatch_profile"] == "worker" {
				found = true
			}
		}
		if !found {
			t.Errorf("niwa list --json shows no record with dispatch_profile worker: %#v", recs)
		}
	})
	t.Run("--profile wins", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "default", decl: defaultWorkerDecl}, withProfile("coordinator"))
		env := settingsEnv(t, got.argv)
		if env[config.DispatchProfileEnv] != "coordinator" {
			t.Errorf("settings env marker = %#v, want coordinator", env[config.DispatchProfileEnv])
		}
		if _, ok := env[windowEnv]; ok {
			t.Errorf("the coordinator profile has no window, yet the settings env carries one: %#v", env)
		}
		rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
		if err != nil {
			t.Fatalf("reading the launch record: %v", err)
		}
		if rec.DispatchProfile != "coordinator" {
			t.Errorf("record DispatchProfile = %q, want coordinator", rec.DispatchProfile)
		}
	})
}

// TestDispatchDefaultProfileSkippedForCodex: a Codex dispatch that names no
// profile skips the default with exactly one line naming the profile and the
// agent, records no profile and launches what it did before. An explicit
// --profile still refuses.
func TestDispatchDefaultProfileSkippedForCodex(t *testing.T) {
	got := runProfileFixtureDispatch(t, profileFixtureCase{name: "codex", decl: defaultWorkerDecl, codex: true}, exportWindow)
	checkProfileFixture(t, "no_profile_codex", got.argv)
	want := skippedDefaultProfileLine("worker", agent.AgentCodex)
	if strings.Count(got.stderr, want) != 1 || strings.Count(got.stderr, "default dispatch profile") != 1 {
		t.Errorf("stderr doesn't hold the skip line exactly once:\n%s", got.stderr)
	}
	for _, w := range []string{`"worker"`, `"codex"`} {
		if !strings.Contains(want, w) {
			t.Errorf("the skip line doesn't name %s: %s", w, want)
		}
	}
	if strings.Contains(got.stderr, "launched with dispatch profile") {
		t.Errorf("a skipped default printed the audit line:\n%s", got.stderr)
	}
	if v, ok := envLookup(got.req.Env, windowEnv); ok {
		t.Errorf("the worker inherited %s=%s", windowEnv, v)
	}
	rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
	if err != nil {
		t.Fatalf("reading the launch record: %v", err)
	}
	if rec.DispatchProfile != "" {
		t.Errorf("record DispatchProfile = %q, want none", rec.DispatchProfile)
	}

	t.Run("explicit --profile refuses", func(t *testing.T) {
		got, reqs, err := tryProfileFixtureDispatch(t, profileFixtureCase{decl: defaultWorkerDecl, codex: true}, withProfile("worker"))
		if err == nil || !strings.Contains(err.Error(), "--profile does not apply") {
			t.Fatalf("err = %v, want the --profile refusal; stderr:\n%s", err, got.stderr)
		}
		if len(reqs) != 0 {
			t.Errorf("launched %d", len(reqs))
		}
	})
}

// TestWatchLaunchDropsInheritedWindow: niwa watch's launch never carries the
// window, and never takes a default profile.
func TestWatchLaunchDropsInheritedWindow(t *testing.T) {
	t.Setenv(windowEnv, inheritedWindow)
	req := watchLaunchRequest(t.TempDir(), "review it", nil, launchEnvShape{ReapKey: "watch-reap-key"})
	if v, ok := envLookup(req.Env, windowEnv); ok {
		t.Errorf("the review inherited %s=%s", windowEnv, v)
	}
	env := settingsEnv(t, req.Passthrough)
	if _, ok := env[windowEnv]; ok {
		t.Errorf("the review's settings env carries %s: %#v", windowEnv, env)
	}
	if _, ok := env[config.DispatchProfileEnv]; ok {
		t.Errorf("the review's settings env carries a profile marker: %#v", env)
	}
}

// TestResumeEnvDropsInheritedWindow: the environment resume and retask's wake
// start from loses an inherited window.
func TestResumeEnvDropsInheritedWindow(t *testing.T) {
	env := resumeEnv([]string{windowEnv + "=" + inheritedWindow, "HOME=/h"}, nil, map[string]string{config.ReapKeyEnv: resumeTestDispatchID})
	if v, ok := envLookup(env, windowEnv); ok {
		t.Errorf("resumeEnv kept %s=%s", windowEnv, v)
	}
	if v, _ := envLookup(env, "HOME"); v != "/h" {
		t.Errorf("resumeEnv lost HOME: %q", env)
	}
}

// dispatchRecordWith runs a real dispatch of decl under profile, in a subtest
// so the dispatch fakes are gone before what follows, and returns its launch
// record moved onto the resume fixture's identities.
func dispatchRecordWith(t *testing.T, decl, profile string) workspace.LaunchRecord {
	t.Helper()
	var rec workspace.LaunchRecord
	t.Run("dispatch", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "profiled", decl: decl}, withProfile(profile))
		r, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
		if err != nil {
			t.Fatalf("reading the dispatch's launch record: %v", err)
		}
		rec = r
	})
	if rec.DispatchProfile != profile || rec.Settings == "" {
		t.Fatalf("the dispatch recorded profile %q, settings %q", rec.DispatchProfile, rec.Settings)
	}
	rec.DispatchID = resumeTestDispatchID
	rec.SessionID = resumeTestSessionID
	rec.InstanceName = resumeTestInstance
	rec.WrittenAt = time.Now().UTC().Add(-time.Hour)
	return rec
}

// TestResumeKeepsRecordedWindow: resume relaunches with the window the record
// holds when the profile's window has since been changed, removed or added,
// and with none for a record that holds none. The relaunch's process
// environment never carries the window, though the shell exports one. A
// changed drop list still refuses.
func TestResumeKeepsRecordedWindow(t *testing.T) {
	windowed := dispatchRecordWith(t, windowedCoordinatorDecl, "coordinator")
	plain := dispatchRecordWith(t, coordinatorProfileDecl, "coordinator")
	changedWindow := strings.Replace(windowedCoordinatorDecl, "400000", "200000", 1)
	changedDrops := strings.Replace(windowedCoordinatorDecl, `, "Skill"]`, `]`, 1)
	for _, c := range []struct {
		name   string
		decl   string
		rec    func(f *resumeFixture) workspace.LaunchRecord
		window string
		refuse string
	}{
		{name: "window unchanged", decl: windowedCoordinatorDecl, rec: func(*resumeFixture) workspace.LaunchRecord { return windowed }, window: "400000"},
		{name: "window changed", decl: changedWindow, rec: func(*resumeFixture) workspace.LaunchRecord { return windowed }, window: "400000"},
		{name: "window removed", decl: coordinatorProfileDecl, rec: func(*resumeFixture) workspace.LaunchRecord { return windowed }, window: "400000"},
		{name: "window added", decl: windowedCoordinatorDecl, rec: func(*resumeFixture) workspace.LaunchRecord { return plain }},
		{name: "record with no profile", decl: windowedCoordinatorDecl, rec: func(f *resumeFixture) workspace.LaunchRecord { return f.record() }},
		{name: "drop list changed", decl: changedDrops, rec: func(*resumeFixture) workspace.LaunchRecord { return windowed }, refuse: "drop_tools"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t, c.decl)
			rec := c.rec(f)
			f.writeRecord(t, rec)
			f.writeTranscript(t, resumeTestSessionID)
			t.Setenv(windowEnv, inheritedWindow)

			var launchedEnv []string
			prevLaunch := resumeLaunchFunc
			resumeLaunchFunc = func(ctx context.Context, bin string, args []string, dir string, env []string) error {
				launchedEnv = env
				return prevLaunch(ctx, bin, args, dir, env)
			}
			t.Cleanup(func() { resumeLaunchFunc = prevLaunch })

			_, stderr, err := runResumeCmd(t, resumeTestInstance)
			if c.refuse != "" {
				if err == nil || !strings.Contains(err.Error(), c.refuse) {
					t.Fatalf("err = %v, want a refusal naming %s", err, c.refuse)
				}
				if calls := f.calls(t); len(calls) != 0 {
					t.Errorf("stub claude was called: %q", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("resume: %v\nstderr:\n%s", err, stderr)
			}
			var launch []string
			for _, call := range f.calls(t) {
				if a := argsOf(call); len(a) > 0 && a[0] == "--bg" {
					launch = a
				}
			}
			i := indexOf(launch, "--settings")
			if i < 0 || i+1 >= len(launch) || launch[i+1] != rec.Settings {
				t.Fatalf("the relaunch's --settings is not the recorded document:\n got %q\nwant %q", launch, rec.Settings)
			}
			got, ok := recordedSettingsEnv(t, launch[i+1])[windowEnv]
			switch {
			case c.window == "" && ok:
				t.Errorf("the relaunch carries a window, %v, the record doesn't hold", got)
			case c.window != "" && got != c.window:
				t.Errorf("the relaunch's window = %v, want the recorded %s", got, c.window)
			}
			if v, ok := envLookup(launchedEnv, windowEnv); ok {
				t.Errorf("the relaunch's process environment carries %s=%s", windowEnv, v)
			}
		})
	}
}

// windowRecordCase is the base record and config the recorded-window checks
// start from: a worker profile that drops bundled skills and sets a window.
func windowRecordCase(window string) workspace.LaunchRecord {
	env := `"` + config.DispatchProfileEnv + `":"worker"`
	if window != "" {
		env = `"` + windowEnv + `":` + jsonString(window) + `,` + env
	}
	return workspace.LaunchRecord{
		DispatchID:      resumeTestDispatchID,
		SessionID:       resumeTestSessionID,
		Agent:           string(agent.AgentClaude),
		InstanceName:    resumeTestInstance,
		DispatchProfile: "worker",
		Settings:        `{"disableBundledSkills":true,"env":{` + env + `}}`,
		Env:             map[string]string{config.DispatchProfileEnv: "worker"},
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// checkWindowRecord runs the launch-record check resume and retask make, for
// rec against decl.
func checkWindowRecord(t *testing.T, decl string, rec workspace.LaunchRecord) error {
	t.Helper()
	result, err := config.Parse([]byte("[workspace]\nname = \"rs\"\n" + decl))
	if err != nil {
		t.Fatalf("parsing %q: %v", decl, err)
	}
	profile, err := resolveRecordProfile(rec, result.Config, agent.AgentClaude)
	if err != nil {
		return err
	}
	return validateLaunchRecord(rec, claudeLaunchSpec(), nil, profile)
}

// TestRecordedWindowChecks: a recorded window passes when it is a decimal
// integer inside the bounds in the settings document's env object of a
// record that names a profile, whatever the profile's window is now; any
// other shape or place refuses, quoting the value.
func TestRecordedWindowChecks(t *testing.T) {
	const skills = "\n[session.dispatch_profiles.worker]\ndrop_bundled_skills = true\n"
	for name, decl := range map[string]string{
		"same window":    skills + "auto_compact_window = 400000\n",
		"changed window": skills + "auto_compact_window = 200000\n",
		"no window now":  skills,
	} {
		for _, w := range []string{"", "100000", "400000", "1000000"} {
			t.Run(name+"/"+w, func(t *testing.T) {
				if err := checkWindowRecord(t, decl, windowRecordCase(w)); err != nil {
					t.Errorf("refused: %v", err)
				}
			})
		}
	}

	decl := skills + "auto_compact_window = 400000\n"
	for _, bad := range []string{"99999", "1000001", "0", "-400000", "+400000", "0400000", "4e5", "400000.0", "abc", "", " 400000"} {
		t.Run("value "+jsonString(bad), func(t *testing.T) {
			rec := windowRecordCase("400000")
			rec.Settings = strings.Replace(rec.Settings, `"400000"`, jsonString(bad), 1)
			err := checkWindowRecord(t, decl, rec)
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range []string{windowEnv, `"` + strings.ReplaceAll(bad, `"`, `\"`) + `"`} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("refusal lacks %s: %v", w, err)
				}
			}
		})
	}

	t.Run("in the record's Env", func(t *testing.T) {
		rec := windowRecordCase("400000")
		rec.Env = map[string]string{config.DispatchProfileEnv: "worker", windowEnv: "400000"}
		err := checkWindowRecord(t, decl, rec)
		if err == nil || !strings.Contains(err.Error(), "record's environment") {
			t.Errorf("err = %v, want a refusal for the window in the record's Env", err)
		}
	})
	t.Run("in the record's Env only", func(t *testing.T) {
		rec := windowRecordCase("")
		rec.Env = map[string]string{config.DispatchProfileEnv: "worker", windowEnv: "400000"}
		if err := checkWindowRecord(t, decl, rec); err == nil {
			t.Error("accepted")
		}
	})
	t.Run("record names no profile", func(t *testing.T) {
		rec := windowRecordCase("")
		rec.DispatchProfile = ""
		rec.Env = nil
		rec.Settings = `{"env":{"` + windowEnv + `":"400000"}}`
		err := checkWindowRecord(t, decl, rec)
		if err == nil || !strings.Contains(err.Error(), "names none") {
			t.Errorf("err = %v, want a refusal for a window on a record with no profile", err)
		}
	})
	t.Run("top-level settings key", func(t *testing.T) {
		rec := windowRecordCase("")
		rec.Settings = `{"disableBundledSkills":true,"` + windowEnv + `":"400000","env":{"` + config.DispatchProfileEnv + `":"worker"}}`
		if err := checkWindowRecord(t, decl, rec); err == nil {
			t.Error("accepted")
		}
	})
	t.Run("drop changed", func(t *testing.T) {
		changed := "\n[session.dispatch_profiles.worker]\ndrop_account_connectors = true\nauto_compact_window = 400000\n"
		if err := checkWindowRecord(t, changed, windowRecordCase("400000")); err == nil {
			t.Error("a record whose drops differ from the profile's now was accepted")
		}
	})
}

// TestRetaskKeepsRecordedWindow: a worker a windowed profile launched passes
// retask's checks when the profile's window has since been changed, removed
// or added, its wake adds no flag (Claude Code re-applies the saved settings
// document, window included) and its process environment carries no
// inherited window. A changed drop list still refuses.
func TestRetaskKeepsRecordedWindow(t *testing.T) {
	windowed := dispatchRecordWith(t, windowedCoordinatorDecl, "coordinator")
	plain := dispatchRecordWith(t, coordinatorProfileDecl, "coordinator")
	changedWindow := strings.Replace(windowedCoordinatorDecl, "400000", "200000", 1)
	changedDrops := strings.Replace(windowedCoordinatorDecl, `, "Skill"]`, `]`, 1)
	for _, c := range []struct {
		name   string
		decl   string
		rec    workspace.LaunchRecord
		refuse string
	}{
		{name: "window changed", decl: changedWindow, rec: windowed},
		{name: "window removed", decl: coordinatorProfileDecl, rec: windowed},
		{name: "window added", decl: windowedCoordinatorDecl, rec: plain},
		{name: "drop list changed", decl: changedDrops, rec: windowed, refuse: "drop_tools"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newRetaskFixture(t, func(rf *retaskFixture, s *retaskSetup) {
				appendWorkspaceConfig(t, rf.root, c.decl)
				s.Record = c.rec
				s.Mapping.DispatchProfile = c.rec.DispatchProfile
			})
			t.Setenv(windowEnv, inheritedWindow)
			var wakeEnv []string
			prevWake := retaskWakeFunc
			retaskWakeFunc = func(ctx context.Context, bin string, args []string, dir string, env []string) error {
				wakeEnv = env
				return prevWake(ctx, bin, args, dir, env)
			}
			t.Cleanup(func() { retaskWakeFunc = prevWake })

			_, stderr, err := rf.run(t, resumeTestInstance, retaskTestPrompt)
			if c.refuse != "" {
				msg := assertRefusal(t, err, resumeTestInstance, retaskInvalidRecord)
				if !strings.Contains(msg, c.refuse) {
					t.Errorf("refusal lacks %q: %s", c.refuse, msg)
				}
				rf.assertNoAction(t)
				return
			}
			if err != nil {
				t.Fatalf("retask: %v\nstderr:\n%s", err, stderr)
			}
			wakes := wakeCalls(rf.stubCalls(t))
			if len(wakes) != 1 {
				t.Fatalf("%d wakes, want 1", len(wakes))
			}
			if indexOf(wakes[0], "--settings") >= 0 {
				t.Errorf("the wake passed a settings document of its own: %q", wakes[0])
			}
			if wakeEnv == nil {
				t.Fatal("the wake ran without going through the wake seam")
			}
			if v, ok := envLookup(wakeEnv, windowEnv); ok {
				t.Errorf("the wake's environment carries %s=%s", windowEnv, v)
			}
		})
	}
}
