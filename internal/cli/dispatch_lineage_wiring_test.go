package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// lineageLaunch is what a lineage test reads off a launch: the raw
// pass-through, the settings document's lineage value, and the value the
// launched process's environment carries ("" when niwa left it alone).
type lineageLaunch struct {
	pass     []string
	settings string
	env      string
	envSet   bool
}

func captureLineageLaunch(f *dispatchFakes, got *lineageLaunch) {
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		got.pass = req.Passthrough
		for i := 0; i+1 < len(req.Passthrough); i++ {
			if req.Passthrough[i] != "--settings" {
				continue
			}
			var doc struct {
				Env map[string]string `json:"env"`
			}
			if err := json.Unmarshal([]byte(req.Passthrough[i+1]), &doc); err == nil {
				got.settings = doc.Env["OTEL_RESOURCE_ATTRIBUTES"]
			}
		}
		for _, kv := range req.Env {
			if v, ok := strings.CutPrefix(kv, "OTEL_RESOURCE_ATTRIBUTES="); ok {
				got.env, got.envSet = v, true
			}
		}
		return nil
	}
}

// attributeMap parses a composed value into key -> value, and the keys in order.
func attributeMap(t *testing.T, value string) (map[string]string, []string) {
	t.Helper()
	m := map[string]string{}
	var order []string
	for _, e := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("entry %q in %q is not key=value", e, value)
		}
		m[k] = v
		order = append(order, k)
	}
	return m, order
}

func writeUserSettings(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDispatch_Lineage_FullSet(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got lineageLaunch
	captureLineageLaunch(f, &got)

	brief := filepath.Join(root, "brief.md")
	if err := os.WriteFile(brief, []byte("the brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserSettings(t, `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team.id=platform,host.name=h1"}}`)
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "team.id=other,extra.key=1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "caller-session-0001")
	dispatchName = "auth layer"
	dispatchSkill = "shirabe:scope"
	dispatchBrief = brief
	dispatchDetach = true

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if got.settings == "" {
		t.Fatalf("the settings document carries no lineage value; pass-through %v", got.pass)
	}
	if !got.envSet || got.env != got.settings {
		t.Fatalf("the launch environment must carry the same value as the settings document:\n env      %q\n settings %q", got.env, got.settings)
	}
	attrs, order := attributeMap(t, got.settings)
	if !strings.HasPrefix(got.settings, "team.id=platform,host.name=h1,extra.key=1,") {
		t.Errorf("inherited entries must come first, settings before environment, conflicts to settings: %q", got.settings)
	}
	wantOrder := []string{"team.id", "host.name", "extra.key", "niwa.dispatch.id", "niwa.dispatch.slug", "niwa.parent.session.id", "niwa.requested.skill", "niwa.brief.id", "niwa.workspace"}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("key order = %v, want %v", order, wantOrder)
	}
	if !dispatchIDPattern.MatchString(attrs["niwa.dispatch.id"]) {
		t.Errorf("niwa.dispatch.id = %q, want a version-4 UUID", attrs["niwa.dispatch.id"])
	}
	checks := map[string]string{
		"niwa.dispatch.slug":     "auth-layer",
		"niwa.parent.session.id": "caller-session-0001",
		"niwa.requested.skill":   "shirabe:scope",
		"niwa.brief.id":          briefDigest([]byte("the brief\n")),
		"niwa.workspace":         "test-ws",
	}
	for k, want := range checks {
		if attrs[k] != want {
			t.Errorf("%s = %q, want %q", k, attrs[k], want)
		}
	}
	if strings.Contains(got.settings, brief) || strings.Contains(got.settings, "the brief") {
		t.Errorf("no attribute may carry the brief's path or content: %q", got.settings)
	}

	raw := readSingleMappingJSON(t, root)
	if raw["dispatch_id"] != attrs["niwa.dispatch.id"] {
		t.Errorf("mapping dispatch_id = %v, want the worker's %q", raw["dispatch_id"], attrs["niwa.dispatch.id"])
	}
	if raw["parent_session_id"] != "caller-session-0001" {
		t.Errorf("mapping parent_session_id = %v", raw["parent_session_id"])
	}

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	annotateFromSessionMappings(records, root, t.TempDir(), time.Now())
	if len(records) != 1 || records[0].DispatchID != attrs["niwa.dispatch.id"] || records[0].ParentSessionID != "caller-session-0001" {
		t.Errorf("niwa list records = %+v, want the dispatch id and parent", records)
	}
}

func TestDispatch_Lineage_DefaultsWithoutFlags(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got lineageLaunch
	captureLineageLaunch(f, &got)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	attrs, order := attributeMap(t, got.settings)
	if attrs["niwa.brief.id"] != briefDigest([]byte("do a thing")) {
		t.Errorf("without --brief the brief id is the prompt's digest, got %q", attrs["niwa.brief.id"])
	}
	for _, absent := range []string{"niwa.dispatch.slug", "niwa.parent.session.id", "niwa.requested.skill"} {
		if _, ok := attrs[absent]; ok {
			t.Errorf("%s must be absent without its input, got %q", absent, attrs[absent])
		}
	}
	if order[0] != "niwa.dispatch.id" {
		t.Errorf("with nothing inherited niwa's entries come first, got %v", order)
	}
	raw := readSingleMappingJSON(t, root)
	if _, ok := raw["parent_session_id"]; ok {
		t.Errorf("a dispatch with no calling session records no parent: %v", raw["parent_session_id"])
	}
}

func TestDispatch_Lineage_MalformedInheritedSendsNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got lineageLaunch
	captureLineageLaunch(f, &got)
	writeUserSettings(t, `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team.id=has space"}}`)
	dispatchDetach = true

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("a bad inherited value must not fail the dispatch: %v", err)
	}
	if got.settings != "" || got.envSet {
		t.Fatalf("nothing may be sent when the inherited value can't be carried: settings %q env %q", got.settings, got.env)
	}
	if !strings.Contains(stderr, "leaving the worker's attributes as they are") {
		t.Errorf("stderr must warn, got %q", stderr)
	}
	if strings.Contains(stderr, "has space") {
		t.Errorf("the warning must not quote the inherited value: %q", stderr)
	}
	if f.launchCalled != 1 {
		t.Errorf("the worker must still launch, launched %d times", f.launchCalled)
	}
}

func TestDispatch_Lineage_RefusalsLeaveNothingBehind(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		setup func(root string)
	}{
		{"skill without plugin", func(string) { dispatchSkill = "scope" }},
		{"brief outside the workspace", func(string) { dispatchBrief = outside }},
		{"brief is a directory", func(root string) { dispatchBrief = root }},
		{"brief is missing", func(root string) { dispatchBrief = filepath.Join(root, "nope.md") }},
		{"brief is larger than the cap", func(root string) {
			big := filepath.Join(root, "big.md")
			if err := os.WriteFile(big, make([]byte, maxLineageFileBytes+1), 0o644); err != nil {
				t.Fatal(err)
			}
			dispatchBrief = big
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			f := installDispatchFakes(t, root)
			c.setup(root)
			if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
				t.Fatal("the dispatch must be refused")
			}
			if f.provisionCalled != 0 || f.launchCalled != 0 {
				t.Errorf("a refusal must come before anything is created: provisioned %d, launched %d", f.provisionCalled, f.launchCalled)
			}
			if files := sessionMappingFiles(t, root); len(files) != 0 {
				t.Errorf("a refusal must leave no session mapping, found %v", files)
			}
		})
	}
}

func TestDispatch_Lineage_TwoDispatchesTwoIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		root := setupDispatchWorkspace(t)
		chdir(t, root)
		f := installDispatchFakes(t, root)
		var got lineageLaunch
		captureLineageLaunch(f, &got)
		dispatchDetach = true
		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		attrs, _ := attributeMap(t, got.settings)
		id := attrs["niwa.dispatch.id"]
		if seen[id] {
			t.Fatalf("two dispatches share the id %q", id)
		}
		seen[id] = true
	}
}

// TestDispatch_Lineage_RemoteControlUnchanged is the golden comparison: with
// remote control and inbound acceptance on, the settings document is exactly
// what it was before lineage, plus the lineage key.
func TestDispatch_Lineage_RemoteControlUnchanged(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch+"accept_session_messages_on_dispatch = true\n")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var got lineageLaunch
	captureLineageLaunch(f, &got)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.settings == "" {
		t.Fatal("the lineage key is missing beside the other contributors")
	}
	stripped := withoutLineageSettings(t, got.pass)
	docs := launchSettingsDocs(t, stripped)
	if len(docs) != 1 || len(docs[0]) != 2 {
		t.Fatalf("without the lineage key the document must hold exactly the two other contributors, got %v", docs)
	}
}

func TestDispatch_Lineage_CodexUnchanged(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got lineageLaunch
	captureLineageLaunch(f, &got)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))
	writeUserSettings(t, `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"team.id=platform"}}`)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.settings != "" || got.envSet {
		t.Fatalf("a Codex launch must carry no lineage: settings %q env %q", got.settings, got.env)
	}
	for _, a := range got.pass {
		if a == "--settings" || strings.Contains(a, "OTEL_RESOURCE_ATTRIBUTES") {
			t.Fatalf("a Codex launch changed: %v", got.pass)
		}
	}
}

// tokenThenFail serves the instance token's bytes and then fails, so the
// dispatch id -- read next, from the same source -- is the read that breaks.
type tokenThenFail struct{ served bool }

func (r *tokenThenFail) Read(p []byte) (int, error) {
	if r.served {
		return 0, errors.New("random source exhausted")
	}
	r.served = true
	for i := range p {
		p[i] = 0xab
	}
	return len(p), nil
}

func TestDispatch_Lineage_DispatchIDReadFailureProvisionsNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	stubDispatchRand(t, &tokenThenFail{})
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "dispatch id") {
		t.Fatalf("err = %v, want the dispatch id failure", err)
	}
	if f.provisionCalled != 0 || f.launchCalled != 0 {
		t.Errorf("a failed id read must come before anything is created: provisioned %d, launched %d", f.provisionCalled, f.launchCalled)
	}
	if files := sessionMappingFiles(t, root); len(files) != 0 {
		t.Errorf("a failed id read must leave no session mapping, found %v", files)
	}
}
