package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The no-profile fixtures under testdata/dispatch_profile are the argv a
// dispatch without --profile launches, recorded from the code as it was before
// dispatch profiles existed. A dispatch without the flag must keep producing
// them byte for byte, the settings document included, so a profile can never
// change a launch that didn't ask for one. The token and the dispatch id come
// from a pinned random source and the workspace root is replaced by a
// placeholder, so nothing in a fixture changes between runs. Run with
// -update-profile-fixtures to rewrite the files.

var updateProfileFixtures = flag.Bool("update-profile-fixtures", false, "rewrite testdata/dispatch_profile from the dispatches the profile fixture tests run")

const profileFixtureRoot = "<workspace>"

// profileFixtureCase is one launch shape: what the workspace declares, what
// the machine config turns on, and which agent is launched.
type profileFixtureCase struct {
	name  string
	decl  string
	host  string
	codex bool
}

// noProfileCases cover a launch with and without each settings contributor
// that a machine or workspace can turn on, and an agent with no settings flag.
var noProfileCases = []profileFixtureCase{
	{name: "plain"},
	{name: "remote_control", host: hostRConDispatch},
	{name: "inbound", host: hostInboundOn},
	{name: "mcp_approval", decl: mcpServersDecl},
	{name: "all", decl: mcpServersDecl, host: hostRConDispatch + "accept_session_messages_on_dispatch = true\n"},
	{name: "codex", codex: true},
}

// profileFixtureLaunch is what one fixture dispatch launched.
type profileFixtureLaunch struct {
	req    launchRequest
	argv   []string
	stderr string
	root   string
	fakes  *dispatchFakes
}

// runProfileFixtureDispatch runs one detached dispatch of c through a real
// Create, with the random source pinned, after prep (when non-nil) has set up
// the dispatcher's environment and flags. It fails the test unless exactly
// one launch happened.
func runProfileFixtureDispatch(t *testing.T, c profileFixtureCase, prep func(t *testing.T)) profileFixtureLaunch {
	t.Helper()
	got, reqs, err := tryProfileFixtureDispatch(t, c, prep)
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, got.stderr)
	}
	if len(reqs) != 1 {
		t.Fatalf("launches = %d, want 1", len(reqs))
	}
	req := reqs[0]
	argv := buildLaunchArgs(req.Spec, req.Mode, req.InstanceDir, req.Prefix+req.Body, req.Passthrough)
	for i, el := range argv {
		argv[i] = strings.ReplaceAll(el, got.root, profileFixtureRoot)
	}
	got.req, got.argv = req, argv
	return got
}

// tryProfileFixtureDispatch runs the dispatch runProfileFixtureDispatch does
// and returns its error and every launch it requested, for a test that
// expects a refusal.
func tryProfileFixtureDispatch(t *testing.T, c profileFixtureCase, prep func(t *testing.T)) (profileFixtureLaunch, []launchRequest, error) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDeclaredDispatchWorkspace(t, c.decl)
	chdir(t, root)
	setHostConfig(t, c.host)
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	stubDispatchRand(t, constByteReader(0xab))
	var reqs []launchRequest
	captureLaunchRequest(f, &reqs)
	dispatchDetach = true
	if c.codex {
		t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))
	}
	if prep != nil {
		prep(t)
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	return profileFixtureLaunch{stderr: stderr, root: root, fakes: f}, reqs, err
}

// checkProfileFixture compares argv with testdata/dispatch_profile/<name>.json,
// or rewrites the file under -update-profile-fixtures.
func checkProfileFixture(t *testing.T, name string, argv []string) {
	t.Helper()
	path := filepath.Join(packageDir, "testdata", "dispatch_profile", name+".json")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(argv); err != nil {
		t.Fatal(err)
	}
	rendered := buf.Bytes()
	if *updateProfileFixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, rendered, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run with -update-profile-fixtures to create it)", path, err)
	}
	if !bytes.Equal(want, rendered) {
		t.Errorf("%s differs from what a dispatch launches now:\n--- file\n%s\n--- dispatch\n%s", path, want, rendered)
	}
}

// TestDispatchProfile_NoProfileMatchesFixtures: a dispatch without --profile
// launches exactly the argv, settings document included, that the code before
// dispatch profiles launched for the same fixture.
func TestDispatchProfile_NoProfileMatchesFixtures(t *testing.T) {
	for _, c := range noProfileCases {
		t.Run(c.name, func(t *testing.T) {
			got := runProfileFixtureDispatch(t, c, nil)
			checkProfileFixture(t, "no_profile_"+c.name, got.argv)
		})
	}
}

// coordinatorProfileDecl is the coordinator profile the design settles on.
const coordinatorProfileDecl = `
[session.dispatch_profiles.coordinator]
drop_tools = ["Artifact", "Workflow", "SendFeedback", "ReportFindings", "EnterWorktree", "AskUserQuestion", "Skill"]
drop_bundled_skills = true
drop_account_connectors = true
`

var coordinatorDenyNames = []any{"Artifact", "Workflow", "SendFeedback", "ReportFindings", "EnterWorktree", "AskUserQuestion", "Skill"}

// profileFixtureDispatchID is the dispatch id the pinned random source mints.
const profileFixtureDispatchID = "abababab-abab-4bab-abab-abababababab"

// withProfile is a prep that asks for the named profile.
func withProfile(name string) func(t *testing.T) {
	return func(t *testing.T) { dispatchProfile = name }
}

// launchSettingsDoc decodes the one --settings document in argv, failing the
// test unless there is exactly one.
func launchSettingsDoc(t *testing.T, argv []string) map[string]any {
	t.Helper()
	var doc map[string]any
	seen := 0
	for i, el := range argv {
		if el != "--settings" || i+1 >= len(argv) {
			continue
		}
		seen++
		if err := json.Unmarshal([]byte(argv[i+1]), &doc); err != nil {
			t.Fatalf("decoding the settings document: %v\n%s", err, argv[i+1])
		}
	}
	if seen != 1 {
		t.Fatalf("argv holds %d --settings elements, want 1: %q", seen, argv)
	}
	return doc
}

// envLookup returns name's value in a KEY=VALUE list.
func envLookup(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// TestDispatchProfile_CoordinatorAddsItsKeys: --profile coordinator puts the
// deny list, the bundled-skills switch and the connectors switch into the one
// settings document beside every other contributor's keys, sets the env value
// and the marker in the process environment too, and records the profile on
// the launch record, the mapping, niwa list and the audit line.
func TestDispatchProfile_CoordinatorAddsItsKeys(t *testing.T) {
	c := profileFixtureCase{name: "profiled", decl: mcpServersDecl + coordinatorProfileDecl, host: hostRConDispatch + "accept_session_messages_on_dispatch = true\n"}
	got := runProfileFixtureDispatch(t, c, withProfile("coordinator"))

	doc := launchSettingsDoc(t, got.argv)
	if want := map[string]any{"deny": coordinatorDenyNames}; !reflect.DeepEqual(doc["permissions"], want) {
		t.Errorf("permissions = %#v, want %#v", doc["permissions"], want)
	}
	if doc["disableBundledSkills"] != true {
		t.Errorf("disableBundledSkills = %#v, want true", doc["disableBundledSkills"])
	}
	for _, key := range []string{"remoteControlAtStartup", "crossSessionInbound", "enabledMcpjsonServers"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the profiled document lost the %s key: %#v", key, doc)
		}
	}
	env, _ := doc["env"].(map[string]any)
	if env["ENABLE_CLAUDEAI_MCP_SERVERS"] != "false" || env["NIWA_DISPATCH_PROFILE"] != "coordinator" {
		t.Errorf("settings env = %#v, want the connectors switch off and the marker", env)
	}
	if v, ok := envLookup(got.req.Env, "ENABLE_CLAUDEAI_MCP_SERVERS"); !ok || v != "false" {
		t.Errorf("process ENABLE_CLAUDEAI_MCP_SERVERS = %q (%v), want false", v, ok)
	}
	if v, _ := envLookup(got.req.Env, "NIWA_DISPATCH_PROFILE"); v != "coordinator" {
		t.Errorf("process NIWA_DISPATCH_PROFILE = %q, want coordinator", v)
	}
	if !strings.Contains(got.stderr, `dispatch profile "coordinator"`) {
		t.Errorf("the audit lines don't name the profile:\n%s", got.stderr)
	}

	rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
	if err != nil {
		t.Fatalf("reading the launch record: %v", err)
	}
	if rec.DispatchProfile != "coordinator" {
		t.Errorf("launch record DispatchProfile = %q, want coordinator", rec.DispatchProfile)
	}
	if rec.Env["NIWA_DISPATCH_PROFILE"] != "coordinator" || rec.Env["ENABLE_CLAUDEAI_MCP_SERVERS"] != "false" {
		t.Errorf("launch record Env = %#v, want the marker and the connectors switch", rec.Env)
	}
	m, err := workspace.ReadSessionMapping(got.root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading the session mapping: %v", err)
	}
	if m.DispatchProfile != "coordinator" {
		t.Errorf("mapping DispatchProfile = %q, want coordinator", m.DispatchProfile)
	}
	_, recs := runListJSON(t)
	found := false
	for _, r := range recs {
		if r["dispatch_profile"] == "coordinator" {
			found = true
		}
	}
	if !found {
		t.Errorf("niwa list --json shows no record with dispatch_profile coordinator: %#v", recs)
	}
}

// TestDispatchProfile_NoProfileRecordsNone: a dispatch without the flag writes
// no profile on its launch record or mapping and prints no profile line.
func TestDispatchProfile_NoProfileRecordsNone(t *testing.T) {
	got := runProfileFixtureDispatch(t, profileFixtureCase{name: "plain", decl: coordinatorProfileDecl}, nil)
	checkProfileFixture(t, "no_profile_plain", got.argv)
	rec, err := workspace.ReadLaunchRecord(got.root, profileFixtureDispatchID)
	if err != nil {
		t.Fatalf("reading the launch record: %v", err)
	}
	if rec.DispatchProfile != "" {
		t.Errorf("launch record DispatchProfile = %q, want none", rec.DispatchProfile)
	}
	for _, name := range []string{"NIWA_DISPATCH_PROFILE", "ENABLE_CLAUDEAI_MCP_SERVERS"} {
		if _, ok := rec.Env[name]; ok {
			t.Errorf("launch record Env holds %s: %#v", name, rec.Env)
		}
	}
	m, err := workspace.ReadSessionMapping(got.root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading the session mapping: %v", err)
	}
	if m.DispatchProfile != "" {
		t.Errorf("mapping DispatchProfile = %q, want none", m.DispatchProfile)
	}
	if strings.Contains(got.stderr, "dispatch profile") {
		t.Errorf("an unprofiled dispatch printed a profile line:\n%s", got.stderr)
	}
}

// TestDispatchProfile_InheritedProfileStopsHere: a dispatch without --profile
// from inside a profiled session (the marker and the switch in its
// environment) launches a worker with neither, and its argv still matches the
// no-profile fixture, for Claude and Codex alike. Without the marker an
// exported switch is kept.
func TestDispatchProfile_InheritedProfileStopsHere(t *testing.T) {
	for _, c := range []profileFixtureCase{{name: "plain"}, {name: "codex", codex: true}} {
		t.Run(c.name, func(t *testing.T) {
			got := runProfileFixtureDispatch(t, c, func(t *testing.T) {
				t.Setenv("NIWA_DISPATCH_PROFILE", "coordinator")
				t.Setenv("ENABLE_CLAUDEAI_MCP_SERVERS", "false")
			})
			checkProfileFixture(t, "no_profile_"+c.name, got.argv)
			for _, name := range []string{"NIWA_DISPATCH_PROFILE", "ENABLE_CLAUDEAI_MCP_SERVERS"} {
				if v, ok := envLookup(got.req.Env, name); ok {
					t.Errorf("the worker inherited %s=%s", name, v)
				}
			}
		})
	}
	t.Run("no marker", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{name: "plain"}, func(t *testing.T) {
			t.Setenv("NIWA_DISPATCH_PROFILE", "")
			os.Unsetenv("NIWA_DISPATCH_PROFILE")
			t.Setenv("ENABLE_CLAUDEAI_MCP_SERVERS", "false")
		})
		checkProfileFixture(t, "no_profile_plain", got.argv)
		if v, ok := envLookup(got.req.Env, "ENABLE_CLAUDEAI_MCP_SERVERS"); !ok || v != "false" {
			t.Errorf("an exported switch without the marker was not kept: %q (%v)", v, ok)
		}
	})
}

// TestDispatchProfile_RefusesBeforeProvisioning: each refusal exits non-zero,
// names its cause, and provisions and launches nothing. Dropping CronCreate
// refuses with keep-alive off as well as on; dropping ScheduleWakeup doesn't.
func TestDispatchProfile_RefusesBeforeProvisioning(t *testing.T) {
	dropping := func(tool string) string {
		return "\n[session.dispatch_profiles.lean]\ndrop_tools = [\"" + tool + "\"]\n"
	}
	cases := []struct {
		name    string
		c       profileFixtureCase
		profile string
		want    string
	}{
		{"unknown profile", profileFixtureCase{decl: coordinatorProfileDecl}, "nope", `unknown dispatch profile "nope"`},
		{"codex", profileFixtureCase{decl: coordinatorProfileDecl, codex: true}, "coordinator", "Codex profile needs its own spelling"},
		{"drops ToolSearch", profileFixtureCase{decl: dropping("ToolSearch")}, "lean", "drops ToolSearch"},
		{"drops CronCreate, keep-alive off", profileFixtureCase{decl: dropping("CronCreate")}, "lean", "drops CronCreate"},
		{"drops CronCreate, keep-alive on", profileFixtureCase{decl: dropping("CronCreate"), host: hostRCAndKeepAlive}, "lean", "drops CronCreate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reqs, err := tryProfileFixtureDispatch(t, tc.c, withProfile(tc.profile))
			if err == nil {
				t.Fatalf("dispatch went ahead; stderr:\n%s", got.stderr)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			if got.fakes.provisionCalled != 0 || len(reqs) != 0 {
				t.Errorf("provisioned %d, launched %d; want neither", got.fakes.provisionCalled, len(reqs))
			}
		})
	}
	t.Run("drops ScheduleWakeup", func(t *testing.T) {
		got := runProfileFixtureDispatch(t, profileFixtureCase{decl: dropping("ScheduleWakeup")}, withProfile("lean"))
		doc := launchSettingsDoc(t, got.argv)
		if want := map[string]any{"deny": []any{"ScheduleWakeup"}}; !reflect.DeepEqual(doc["permissions"], want) {
			t.Errorf("permissions = %#v, want %#v", doc["permissions"], want)
		}
	})
}

// TestProfileEnvNamesAgreeAcrossPackages holds the one list config load
// refuses in [session.dispatch_env] equal to the names an agent's profile can
// set, which is the list a launch strips from an inherited profile.
func TestProfileEnvNamesAgreeAcrossPackages(t *testing.T) {
	fromConfig := slices.Clone(config.ProfileEnvNames)
	slices.Sort(fromConfig)
	if got := agentplan.AllProfileEnvNames(); !slices.Equal(got, fromConfig) {
		t.Errorf("agentplan.AllProfileEnvNames() = %v, config.ProfileEnvNames = %v; they must match", got, fromConfig)
	}
}

// TestProfileDenyList: the deny list marshals to exactly {"deny":[...]},
// inside a rendered document too, and refuses a name outside the tool-name
// grammar, which makes renderLaunchSettings panic rather than send it.
func TestProfileDenyList(t *testing.T) {
	got, err := json.Marshal(profileDenyList{names: []string{"Workflow", "Skill"}})
	if err != nil || string(got) != `{"deny":["Workflow","Skill"]}` {
		t.Fatalf("marshal = %s, %v", got, err)
	}
	if got, err := json.Marshal(profileDenyList{}); err != nil || string(got) != `{"deny":[]}` {
		t.Fatalf("empty marshal = %s, %v", got, err)
	}
	if _, err := json.Marshal(profileDenyList{names: []string{`Bash(rm -rf)`}}); err == nil {
		t.Fatal("a name outside the tool-name grammar marshalled")
	}
	doc, ok := renderLaunchSettings(map[string]any{"permissions": profileDenyList{names: []string{"Workflow"}}})
	if !ok || doc != `{"permissions":{"deny":["Workflow"]}}` {
		t.Fatalf("renderLaunchSettings = (%s, %v)", doc, ok)
	}
	defer func() {
		if recover() == nil {
			t.Error("renderLaunchSettings did not panic on a deny name outside the grammar")
		}
	}()
	renderLaunchSettings(map[string]any{"permissions": profileDenyList{names: []string{"allow"}}})
}

// withNoProfile is a prep that passes --no-profile.
func withNoProfile(t *testing.T) { dispatchNoProfile = true }

// TestDispatchNoProfileFlag: --no-profile is a boolean flag, off by default,
// and the --profile usage text points at it.
func TestDispatchNoProfileFlag(t *testing.T) {
	f := dispatchCmd.Flags().Lookup("no-profile")
	if f == nil {
		t.Fatal("niwa dispatch has no --no-profile flag")
	}
	if f.Value.Type() != "bool" || f.DefValue != "false" {
		t.Errorf("--no-profile is a %s defaulting to %q, want a bool defaulting to false", f.Value.Type(), f.DefValue)
	}
	if p := dispatchCmd.Flags().Lookup("profile"); p == nil || !strings.Contains(p.Usage, "--no-profile") {
		t.Errorf("the --profile usage text doesn't point at --no-profile")
	}
}

// TestResolveDispatchLaunchProfile_NoProfile: the opt-out resolves to the zero
// profile with no skip notice, whether or not a default is set and for an
// agent with no profile support too. A non-empty --profile beside it refuses,
// naming both flags.
func TestResolveDispatchLaunchProfile_NoProfile(t *testing.T) {
	defaulted := &config.WorkspaceConfig{}
	defaulted.Session.DefaultDispatchProfile = "worker"
	cases := []struct {
		name string
		cfg  *config.WorkspaceConfig
		ag   agent.Agent
	}{
		{"default set", defaulted, agent.AgentClaude},
		{"no default", &config.WorkspaceConfig{}, agent.AgentClaude},
		{"no config", nil, agent.AgentClaude},
		{"codex with a default", defaulted, agent.AgentCodex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profile, skipped, err := resolveDispatchLaunchProfile("", true, tc.cfg, tc.ag)
			if err != nil {
				t.Fatalf("err = %v, want none", err)
			}
			if skipped != "" {
				t.Errorf("skipped = %q, want none: the skip notice is the Codex default's alone", skipped)
			}
			if !reflect.DeepEqual(profile, dispatchLaunchProfile{}) {
				t.Errorf("profile = %#v, want the zero profile", profile)
			}
		})
	}
	t.Run("with a non-empty --profile", func(t *testing.T) {
		_, _, err := resolveDispatchLaunchProfile("worker", true, defaulted, agent.AgentClaude)
		if err == nil {
			t.Fatal("--profile worker with --no-profile resolved")
		}
		for _, want := range []string{"--no-profile", `--profile "worker"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q doesn't name %s", err, want)
			}
		}
	})
}

// TestDispatchNoProfile_RefusesWithProfile: --no-profile with a non-empty
// --profile exits non-zero naming both flags, and provisions and launches
// nothing, for a declared profile and an undeclared one alike.
func TestDispatchNoProfile_RefusesWithProfile(t *testing.T) {
	for _, name := range []string{"coordinator", "nope"} {
		t.Run(name, func(t *testing.T) {
			got, reqs, err := tryProfileFixtureDispatch(t, profileFixtureCase{decl: defaultWorkerDecl}, both(withProfile(name), withNoProfile))
			if err == nil {
				t.Fatalf("dispatch went ahead; stderr:\n%s", got.stderr)
			}
			for _, want := range []string{"--no-profile", "--profile " + `"` + name + `"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q doesn't name %s", err, want)
				}
			}
			if got.fakes.provisionCalled != 0 || len(reqs) != 0 {
				t.Errorf("provisioned %d, launched %d; want neither", got.fakes.provisionCalled, len(reqs))
			}
		})
	}
}
