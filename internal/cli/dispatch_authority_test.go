package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// These tests drive runDispatch through the dispatch fakes with a workspace
// authority_level in workspace.toml, a machine authority_level under a
// temporary XDG_CONFIG_HOME, and --authority, and check what the dispatch
// resolves, records, sends and refuses.

// authorityLineRe is the one stderr line a launching dispatch prints about its
// authority level.
var authorityLineRe = regexp.MustCompile(`(?m)^niwa dispatch: authority level (autonomous|supervised|strict) from (flag|machine|workspace|default)$`)

// authorityDispatch is one dispatch's inputs. A nil workspace or machine
// pointer leaves the key out entirely; a non-nil one writes it raw.
type authorityDispatch struct {
	workspace  *string
	machine    *string
	machineRaw string // a whole machine config body, used instead of machine
	flag       string // --authority
	permission string // --permission-mode
	harness    string
	wsTOML     string // a whole workspace.toml, used instead of workspace
}

// authorityResult is what one dispatch produced.
type authorityResult struct {
	root   string
	stderr string
	err    error
	fakes  *dispatchFakes
	pass   []string
	env    []string
	// atLaunch is the authority record the fake launcher read from the
	// instance when it was called, and atLaunchErr the error reading it.
	atLaunch    workspace.AuthorityRecord
	atLaunchErr error
	atLaunchSet bool
}

func strp(s string) *string { return &s }

// writeAuthorityWorkspace writes a workspace root whose workspace.toml holds
// the given authority_level (or none), or the raw document when raw is set.
func writeAuthorityWorkspace(t *testing.T, level *string, raw string) string {
	t.Helper()
	root := canonicalTempDir(t)
	dir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := raw
	if body == "" {
		body = "[workspace]\nname = \"test-ws\"\n"
		if level != nil {
			body += fmt.Sprintf("authority_level = %q\n", *level)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, config.ConfigFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runAuthorityDispatch(t *testing.T, in authorityDispatch) authorityResult {
	t.Helper()
	root := writeAuthorityWorkspace(t, in.workspace, in.wsTOML)
	chdir(t, root)
	host := in.machineRaw
	if in.machine != nil {
		host = fmt.Sprintf("[global]\nauthority_level = %q\n", *in.machine)
	}
	setHostConfig(t, host)
	f := installDispatchFakes(t, root)
	r := authorityResult{root: root, fakes: f}
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		r.pass = append([]string(nil), req.Passthrough...)
		r.env = req.Env
		r.atLaunch, r.atLaunchErr = workspace.ReadAuthorityRecord(req.InstanceDir)
		r.atLaunchSet = true
		return nil
	}
	dispatchAuthority = in.flag
	dispatchPermissionMode = in.permission
	dispatchHarness = in.harness
	dispatchDetach = true

	_, r.stderr, r.err = runDispatchCmd(t, "do a thing")
	return r
}

// requireLaunched asserts a dispatch launched at level from source: one
// authority line naming them, the instance record present when the worker was
// launched, the record still in place with mode 0600, and the mapping
// carrying both fields.
func requireLaunched(t *testing.T, r authorityResult, level, source string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", r.err, r.stderr)
	}
	lines := authorityLineRe.FindAllStringSubmatch(r.stderr, -1)
	if len(lines) != 1 {
		t.Fatalf("want exactly one authority line, got %d\nstderr:\n%s", len(lines), r.stderr)
	}
	if lines[0][1] != level || lines[0][2] != source {
		t.Errorf("authority line = %q, want level %s from %s", lines[0][0], level, source)
	}
	if !r.atLaunchSet {
		t.Fatal("the worker was never launched")
	}
	want := workspace.AuthorityRecord{Level: level, Source: source}
	if r.atLaunchErr != nil || r.atLaunch != want {
		t.Errorf("authority record at launch = %+v (err %v), want %+v", r.atLaunch, r.atLaunchErr, want)
	}
	info, err := os.Stat(workspace.AuthorityRecordPath(r.fakes.instancePath))
	if err != nil {
		t.Fatalf("the authority record is gone after the dispatch: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("authority record mode = %v, want 0600", info.Mode().Perm())
	}
	raw := readSingleMappingJSON(t, r.root)
	if raw["authority_level"] != level || raw["authority_source"] != source {
		t.Errorf("mapping authority_level/authority_source = %v/%v, want %s/%s", raw["authority_level"], raw["authority_source"], level, source)
	}
}

// requireRefused asserts a dispatch was refused before anything was created:
// an error naming each of want, no authority line, no provision, no launch,
// no mapping, and nothing but the config directory in the workspace root.
func requireRefused(t *testing.T, r authorityResult, want ...string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("the dispatch must be refused\nstderr:\n%s", r.stderr)
	}
	for _, w := range want {
		if !strings.Contains(r.err.Error(), w) {
			t.Errorf("refusal %q does not name %q", r.err, w)
		}
	}
	if authorityLineRe.MatchString(r.stderr) {
		t.Errorf("a refused dispatch printed an authority line:\n%s", r.stderr)
	}
	if r.fakes.provisionCalled != 0 || r.fakes.launchCalled != 0 {
		t.Errorf("a refused dispatch provisioned %d and launched %d", r.fakes.provisionCalled, r.fakes.launchCalled)
	}
	if files := sessionMappingFiles(t, r.root); len(files) != 0 {
		t.Errorf("a refused dispatch left session mappings %v", files)
	}
	entries, err := os.ReadDir(r.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != config.ConfigDir {
			t.Errorf("a refused dispatch left %s in the workspace root", e.Name())
		}
	}
}

// listJSONRecords runs `niwa list --json` from root and returns its records
// keyed by instance path.
func listJSONRecords(t *testing.T, root string) map[string]map[string]any {
	t.Helper()
	chdir(t, root)
	prev := listJSON
	listJSON = true
	t.Cleanup(func() { listJSON = prev })
	cmd, stdout, _ := newLegacyCmd()
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("list --json is not a JSON array: %v\n%s", err, stdout)
	}
	out := map[string]map[string]any{}
	for _, rec := range decoded {
		p, _ := rec["path"].(string)
		out[p] = rec
	}
	return out
}

func TestDispatch_Authority_Resolution(t *testing.T) {
	cases := []struct {
		name          string
		in            authorityDispatch
		level, source string
	}{
		{"workspace strict", authorityDispatch{workspace: strp("strict")}, "strict", "workspace"},
		{"workspace autonomous, flag supervised", authorityDispatch{workspace: strp("autonomous"), flag: "supervised"}, "supervised", "flag"},
		{"workspace autonomous, no flag", authorityDispatch{workspace: strp("autonomous")}, "autonomous", "workspace"},
		{"workspace supervised, flag supervised ties to the flag", authorityDispatch{workspace: strp("supervised"), flag: "supervised"}, "supervised", "flag"},
		{"machine strict over workspace autonomous", authorityDispatch{workspace: strp("autonomous"), machine: strp("strict")}, "strict", "machine"},
		{"machine autonomous under workspace strict", authorityDispatch{workspace: strp("strict"), machine: strp("autonomous")}, "strict", "workspace"},
		{"machine strict ties workspace strict", authorityDispatch{workspace: strp("strict"), machine: strp("strict")}, "strict", "machine"},
		{"nothing configured", authorityDispatch{}, "autonomous", "default"},
		{"workspace value is trimmed", authorityDispatch{workspace: strp(" strict ")}, "strict", "workspace"},
		{"empty workspace value is unset", authorityDispatch{workspace: strp("")}, "autonomous", "default"},
		{"whitespace workspace value is unset", authorityDispatch{workspace: strp("   ")}, "autonomous", "default"},
		{"flag narrows the default", authorityDispatch{flag: "strict"}, "strict", "flag"},
		{"flag value is trimmed", authorityDispatch{flag: " supervised "}, "supervised", "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runAuthorityDispatch(t, tc.in)
			requireLaunched(t, r, tc.level, tc.source)
		})
	}
}

// TestDispatch_Authority_WorkspaceStrictReadBack: the workspace level reaches
// the mapping and `niwa list --json` shows both fields for the instance.
func TestDispatch_Authority_WorkspaceStrictReadBack(t *testing.T) {
	r := runAuthorityDispatch(t, authorityDispatch{workspace: strp("strict")})
	requireLaunched(t, r, "strict", "workspace")
	rec := listJSONRecords(t, r.root)[r.fakes.instancePath]
	if rec == nil {
		t.Fatalf("list --json has no record for %s", r.fakes.instancePath)
	}
	if rec["authority_level"] != "strict" || rec["authority_source"] != "workspace" {
		t.Errorf("list --json authority fields = %v/%v, want strict/workspace", rec["authority_level"], rec["authority_source"])
	}
}

func TestDispatch_Authority_Refusals(t *testing.T) {
	cases := []struct {
		name string
		in   authorityDispatch
		want []string
	}{
		{"flag wider than the workspace", authorityDispatch{workspace: strp("supervised"), flag: "autonomous"},
			[]string{"--authority", "autonomous", "supervised", "workspace"}},
		{"flag wider than the machine", authorityDispatch{workspace: strp("autonomous"), machine: strp("strict"), flag: "supervised"},
			[]string{"--authority", "supervised", "strict", "machine"}},
		{"flag wider than the machine for Codex", authorityDispatch{machine: strp("strict"), flag: "autonomous", harness: "codex"},
			[]string{"autonomous", "strict", "machine"}},
		{"invalid flag", authorityDispatch{flag: "bypass"},
			append([]string{"--authority", `"bypass"`}, authority.Names()...)},
		{"capitalized flag", authorityDispatch{flag: "Strict"},
			append([]string{"--authority", `"Strict"`}, authority.Names()...)},
		{"invalid machine value", authorityDispatch{machine: strp("STRICT")},
			append([]string{"authority_level", `"STRICT"`, "config.toml"}, authority.Names()...)},
		{"invalid machine value for Codex", authorityDispatch{machine: strp("STRICT"), harness: "codex"},
			append([]string{"authority_level", `"STRICT"`}, authority.Names()...)},
		{"invalid workspace value", authorityDispatch{workspace: strp("Strict")},
			append([]string{"authority_level", `"Strict"`, config.ConfigFile}, authority.Names()...)},
		{"workspace TOML syntax error", authorityDispatch{wsTOML: "[workspace\nname = \"test-ws\"\nauthority_level = \"strict\"\n"},
			[]string{config.ConfigFile}},
		{"machine TOML syntax error", authorityDispatch{machineRaw: "[global\nauthority_level = \"strict\"\n"},
			[]string{"config.toml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runAuthorityDispatch(t, tc.in)
			requireRefused(t, r, tc.want...)
		})
	}
}

// TestDispatch_Authority_SecondDispatchWithoutTheFlag: a flag narrows only the
// dispatch it is given to.
func TestDispatch_Authority_SecondDispatchWithoutTheFlag(t *testing.T) {
	first := runAuthorityDispatch(t, authorityDispatch{workspace: strp("autonomous"), flag: "supervised"})
	requireLaunched(t, first, "supervised", "flag")
	second := runAuthorityDispatch(t, authorityDispatch{workspace: strp("autonomous")})
	requireLaunched(t, second, "autonomous", "workspace")
}

// TestDispatch_Authority_IndependentOfPermissionMode: the level never changes
// what --permission-mode forwards, and the permission mode never changes the
// level.
func TestDispatch_Authority_IndependentOfPermissionMode(t *testing.T) {
	t.Run("strict with bypassPermissions", func(t *testing.T) {
		r := runAuthorityDispatch(t, authorityDispatch{flag: "strict", permission: "bypassPermissions"})
		requireLaunched(t, r, "strict", "flag")
		requirePermissionMode(t, r.pass, "bypassPermissions")
	})
	t.Run("workspace strict with bypassPermissions", func(t *testing.T) {
		r := runAuthorityDispatch(t, authorityDispatch{workspace: strp("strict"), permission: "bypassPermissions"})
		requireLaunched(t, r, "strict", "workspace")
		requirePermissionMode(t, r.pass, "bypassPermissions")
	})
	for _, level := range authority.Names() {
		t.Run(level+" with no permission flag", func(t *testing.T) {
			r := runAuthorityDispatch(t, authorityDispatch{flag: level})
			requireLaunched(t, r, level, "flag")
			requireNoPermissionMode(t, r.pass)
		})
		t.Run(level+" with plan", func(t *testing.T) {
			r := runAuthorityDispatch(t, authorityDispatch{flag: level, permission: "plan"})
			requireLaunched(t, r, level, "flag")
			requirePermissionMode(t, r.pass, "plan")
		})
	}
}

// otelValues returns the OTEL_RESOURCE_ATTRIBUTES value in the launch
// environment and in the settings document's env, and whether each is set.
func otelValues(t *testing.T, r authorityResult) (env string, envSet bool, settings string, settingsSet bool) {
	t.Helper()
	for _, kv := range r.env {
		if v, ok := strings.CutPrefix(kv, "OTEL_RESOURCE_ATTRIBUTES="); ok {
			env, envSet = v, true
		}
	}
	for _, doc := range launchSettingsDocs(t, r.pass) {
		e, ok := doc["env"].(map[string]any)
		if !ok {
			continue
		}
		if v, ok := e["OTEL_RESOURCE_ATTRIBUTES"].(string); ok {
			settings, settingsSet = v, true
		}
	}
	return
}

func TestDispatch_Authority_LineageAttribute(t *testing.T) {
	for _, level := range authority.Names() {
		t.Run("Claude "+level, func(t *testing.T) {
			r := runAuthorityDispatch(t, authorityDispatch{flag: level})
			requireLaunched(t, r, level, "flag")
			env, envSet, settings, settingsSet := otelValues(t, r)
			want := "niwa.authority.level=" + level
			if !envSet || !slices.Contains(strings.Split(env, ","), want) {
				t.Errorf("launch environment OTEL_RESOURCE_ATTRIBUTES = %q, want it to hold %s", env, want)
			}
			if !settingsSet || !slices.Contains(strings.Split(settings, ","), want) {
				t.Errorf("--settings env OTEL_RESOURCE_ATTRIBUTES = %q, want it to hold %s", settings, want)
			}
		})
	}

	t.Run("Codex gets no attribute and still records the level", func(t *testing.T) {
		r := runAuthorityDispatch(t, authorityDispatch{workspace: strp("supervised"), harness: "codex"})
		requireLaunched(t, r, "supervised", "workspace")
		for _, kv := range append(append([]string(nil), r.env...), r.pass...) {
			if strings.Contains(kv, "niwa.authority.level") {
				t.Fatalf("a Codex launch carries niwa.authority.level: %q", kv)
			}
		}
		if r.env == nil && strings.Contains(os.Getenv("OTEL_RESOURCE_ATTRIBUTES"), "niwa.authority.level") {
			t.Fatal("the inherited environment carries niwa.authority.level")
		}
		if rec := listJSONRecords(t, r.root)[r.fakes.instancePath]; rec["authority_level"] != "supervised" || rec["authority_source"] != "workspace" {
			t.Errorf("list --json for a Codex instance = %v", rec)
		}
	})
}

// TestDispatch_Authority_NothingConfiguredChangesOnlyTheAttribute: with no
// workspace key, no machine key and no flag, the Claude argv is the one
// dispatch always built, and the settings document is the pre-feature one with
// niwa.authority.level=autonomous appended to the lineage value and nothing
// else.
func TestDispatch_Authority_NothingConfiguredChangesOnlyTheAttribute(t *testing.T) {
	r := runAuthorityDispatch(t, authorityDispatch{})
	requireLaunched(t, r, "autonomous", "default")

	_, _, settings, ok := otelValues(t, r)
	if !ok {
		t.Fatalf("no lineage value in %v", r.pass)
	}
	attrs, _ := attributeMap(t, settings)
	// The pre-feature value, built from the same inputs by the entry renderer
	// with no authority level.
	before := strings.Join(lineageEntries(lineageInputs{
		DispatchID: attrs[attrDispatchID],
		BriefID:    briefDigest([]byte("do a thing")),
		Workspace:  "test-ws",
	}, &strings.Builder{}), ",")
	if settings != before+",niwa.authority.level=autonomous" {
		t.Fatalf("lineage value = %q, want the pre-feature %q plus the authority entry", settings, before)
	}
	doc, ok := renderLaunchSettings(map[string]any{"env": resourceAttributesEnv{value: settings}})
	if !ok {
		t.Fatal("rendering the expected document failed")
	}
	want := append(buildDispatchPassthrough(claudeLaunchSpec().Flags, "", "", ""), claudeLaunchSpec().Flags.Settings, doc)
	if !slices.Equal(r.pass, want) {
		t.Fatalf("argv = %q\nwant  %q", r.pass, want)
	}
}

// TestDispatch_Authority_FailureAfterTheRecordLeavesNothing: a failure after
// the authority record is written -- here the launch -- rolls back the
// instance, and the record with it.
func TestDispatch_Authority_FailureAfterTheRecordLeavesNothing(t *testing.T) {
	root := writeAuthorityWorkspace(t, strp("strict"), "")
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	destroyInstanceFunc = realDestroyInstance // installDispatchFakes restores the fake
	var recordAtLaunch error
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		_, recordAtLaunch = workspace.ReadAuthorityRecord(req.InstanceDir)
		return errors.New("launch failed")
	}
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "launch failed") {
		t.Fatalf("err = %v, want the launch failure", err)
	}
	if recordAtLaunch != nil {
		t.Fatalf("precondition: the record must exist when the launch fails: %v", recordAtLaunch)
	}
	if _, err := os.Stat(workspace.AuthorityRecordPath(f.instancePath)); !os.IsNotExist(err) {
		t.Errorf("the authority record outlived the rollback: %v", err)
	}
	if _, err := os.Stat(f.instancePath); !os.IsNotExist(err) {
		t.Errorf("the instance outlived the rollback: %v", err)
	}
	if files := sessionMappingFiles(t, root); len(files) != 0 {
		t.Errorf("a failed dispatch left session mappings %v", files)
	}
}

// writeAuthorityMapping writes a session mapping for instancePath with the given
// creation time and authority fields.
func writeAuthorityMapping(t *testing.T, root, sessionID, instancePath string, created time.Time, level, source string) {
	t.Helper()
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:       sessionID,
		InstanceName:    filepath.Base(instancePath),
		InstancePath:    instancePath,
		Ephemeral:       true,
		Origin:          "dispatch",
		Created:         created,
		AuthorityLevel:  level,
		AuthoritySource: source,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestList_AuthorityFields(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	t.Run("legacy mapping omits both", func(t *testing.T) {
		root := writeAuthorityWorkspace(t, nil, "")
		inst := seedInstance(t, root, "test-ws-1", 1)
		writeAuthorityMapping(t, root, "11111111-2222-3333-4444-555555555555", inst, base, "", "")
		rec := listJSONRecords(t, root)[inst]
		if rec == nil {
			t.Fatal("no record")
		}
		for _, k := range []string{"authority_level", "authority_source"} {
			if _, ok := rec[k]; ok {
				t.Errorf("a legacy mapping's record carries %s: %v", k, rec)
			}
		}
	})

	t.Run("newest of two mappings wins", func(t *testing.T) {
		root := writeAuthorityWorkspace(t, nil, "")
		inst := seedInstance(t, root, "test-ws-1", 1)
		// The newer one sorts first by session id, so the choice is by
		// creation time, not by file order.
		writeAuthorityMapping(t, root, "11111111-2222-3333-4444-555555555555", inst, base.Add(time.Hour), "strict", "flag")
		writeAuthorityMapping(t, root, "99999999-2222-3333-4444-555555555555", inst, base, "autonomous", "default")
		rec := listJSONRecords(t, root)[inst]
		if rec["authority_level"] != "strict" || rec["authority_source"] != "flag" {
			t.Errorf("record = %v, want the newest mapping's strict/flag", rec)
		}
	})

	t.Run("a newest legacy mapping hides an older one", func(t *testing.T) {
		root := writeAuthorityWorkspace(t, nil, "")
		inst := seedInstance(t, root, "test-ws-1", 1)
		writeAuthorityMapping(t, root, "11111111-2222-3333-4444-555555555555", inst, base, "strict", "flag")
		writeAuthorityMapping(t, root, "99999999-2222-3333-4444-555555555555", inst, base.Add(time.Hour), "", "")
		if rec := listJSONRecords(t, root)[inst]; rec["authority_level"] != nil || rec["authority_source"] != nil {
			t.Errorf("record = %v, want no authority fields from an older mapping", rec)
		}
	})

	t.Run("malformed values are left out", func(t *testing.T) {
		for _, pair := range [][2]string{{"bogus", "flag"}, {"strict", "declaration"}, {"Strict", "flag"}, {"strict", ""}, {"", "flag"}} {
			root := writeAuthorityWorkspace(t, nil, "")
			inst := seedInstance(t, root, "test-ws-1", 1)
			writeAuthorityMapping(t, root, "11111111-2222-3333-4444-555555555555", inst, base, pair[0], pair[1])
			if rec := listJSONRecords(t, root)[inst]; rec["authority_level"] != nil || rec["authority_source"] != nil {
				t.Errorf("mapping %q/%q produced record %v", pair[0], pair[1], rec)
			}
		}
	})
}
