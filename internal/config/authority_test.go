package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
)

const minimalWorkspace = "[workspace]\nname = \"ws\"\n"

func parseWorkspace(t *testing.T, body string) *ParseResult {
	t.Helper()
	res, err := Parse([]byte(minimalWorkspace + body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return res
}

func parseOverlayBody(t *testing.T, body string) *WorkspaceOverlay {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace-overlay.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := ParseOverlay(path)
	if err != nil {
		t.Fatalf("ParseOverlay: %v", err)
	}
	return o
}

func parseGlobal(t *testing.T, body string) *GlobalConfig {
	t.Helper()
	g, err := ParseGlobalConfig([]byte(body))
	if err != nil {
		t.Fatalf("ParseGlobalConfig: %v", err)
	}
	return g
}

func TestAuthority_BaseDecodes(t *testing.T) {
	res := parseWorkspace(t, `
[authority]
level = "supervised"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
`)
	p, warnings, err := AuthorityPolicy(res.Config, nil, nil)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("AuthorityPolicy: %v, warnings %v", err, warnings)
	}
	if *p.BaseLevel != authority.Supervised || len(*p.BaseCeiling) != 3 || len(p.Roles["coordinator"]) != 3 {
		t.Errorf("policy = %+v", p)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("parse warnings = %v", res.Warnings)
	}
}

// TestAuthority_UnknownKeysReportedOnce: an unknown [authority] key and a
// misspelt role key are invalid values for ValidateConfig and no longer show
// up in the generic unknown-field warning.
func TestAuthority_UnknownKeysReportedOnce(t *testing.T) {
	res := parseWorkspace(t, `
[authority]
ceilng = ["merge-pr"]

[authority.roles.coordinator]
authoritys = ["merge-pr"]
`)
	for _, w := range res.Warnings {
		if strings.Contains(w, "authority") {
			t.Errorf("generic warning still lists an authority key: %q", w)
		}
	}
	if !reflect.DeepEqual(res.Config.Authority.UnknownKeys, []string{"ceilng"}) {
		t.Errorf("UnknownKeys = %v", res.Config.Authority.UnknownKeys)
	}
	_, _, err := AuthorityPolicy(res.Config, nil, nil)
	var ce *authority.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v", err)
	}
	msg := err.Error()
	if strings.Count(msg, "ceilng") != 1 || !strings.Contains(msg, `[authority.roles.coordinator] key "authoritys"`) {
		t.Errorf("error = %q", msg)
	}
}

func TestAuthority_WrongTypesDecode(t *testing.T) {
	res := parseWorkspace(t, "[authority]\nceiling = \"merge-pr\"\nlevel = 3\n")
	g := parseGlobal(t, "[global]\nauthority_ceiling = \"x\"\n")
	_, _, err := AuthorityPolicy(res.Config, nil, g)
	var ce *authority.ConfigError
	if !errors.As(err, &ce) || len(ce.Problems) != 3 {
		t.Fatalf("err = %v, want three invalid values", err)
	}
	if _, err := g.AuthorityCeiling(); err == nil {
		t.Error("AuthorityCeiling accepted a string")
	}
	if _, err := parseGlobal(t, "[global]\nauthority_level = \"bogus\"\n").AuthorityLevel(); err == nil {
		t.Error("AuthorityLevel accepted bogus")
	}
	lvl, err := parseGlobal(t, "[global]\nauthority_level = \" strict \"\n").AuthorityLevel()
	if err != nil || lvl == nil || *lvl != authority.Strict {
		t.Errorf("AuthorityLevel = %v, %v", lvl, err)
	}
	if lvl, err := parseGlobal(t, "").AuthorityLevel(); lvl != nil || err != nil {
		t.Errorf("absent AuthorityLevel = %v, %v", lvl, err)
	}
}

func TestAuthority_OverlayDecodes(t *testing.T) {
	o := parseOverlayBody(t, `
[authority]
level = "strict"
ceiling = []
levle = "x"

[authority.roles.x]
authorities = ["merge-pr"]
`)
	if o.Authority.Level != "strict" {
		t.Errorf("level = %v", o.Authority.Level)
	}
	if c, ok := o.Authority.Ceiling.([]any); !ok || len(c) != 0 {
		t.Errorf("ceiling = %#v, want a present empty list", o.Authority.Ceiling)
	}
	if !reflect.DeepEqual(o.Authority.UnknownKeys, []string{"levle"}) {
		t.Errorf("UnknownKeys = %v", o.Authority.UnknownKeys)
	}
	res := parseWorkspace(t, "[authority]\nceiling = [\"merge-pr\"]\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\"]\n")
	p, warnings, err := AuthorityPolicy(res.Config, o, nil)
	if err == nil || !strings.Contains(err.Error(), `overlay [authority] key "levle"`) {
		t.Errorf("err = %v", err)
	}
	if p.OverlayCeiling == nil || len(*p.OverlayCeiling) != 0 {
		t.Errorf("overlay ceiling = %v", p.OverlayCeiling)
	}
	if !reflect.DeepEqual(p.Roles, map[string][]authority.Authority{"coordinator": {authority.MergePR}}) {
		t.Errorf("an overlay role changed the roles: %v", p.Roles)
	}
	tombstones := 0
	for _, w := range warnings {
		if strings.Contains(w, "[authority.roles]") {
			tombstones++
		}
	}
	if tombstones != 1 {
		t.Errorf("warnings = %v, want the roles tombstone once", warnings)
	}
	for _, w := range o.TombstoneWarnings() {
		if strings.Contains(w, "authority") {
			t.Errorf("TombstoneWarnings also reports the roles table: %q", w)
		}
	}
	all := AuthorityWarnings(res.Config, o, nil)
	if len(all) != 2 {
		t.Errorf("AuthorityWarnings = %v, want the invalid key and the tombstone", all)
	}
}

func TestAuthority_GlobalEmptyCeilingRoundTrip(t *testing.T) {
	g := parseGlobal(t, "[global]\nauthority_ceiling = []\nauthority_levle = \"x\"\n")
	c, err := g.AuthorityCeiling()
	if err != nil || c == nil || len(*c) != 0 {
		t.Fatalf("AuthorityCeiling = %v, %v; want present and empty", c, err)
	}
	if !reflect.DeepEqual(g.AuthorityRawLayer().UnknownKeys, []string{"authority_levle"}) {
		t.Errorf("UnknownKeys = %v", g.AuthorityRawLayer().UnknownKeys)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	g = parseGlobal(t, "[global]\nauthority_ceiling = []\n")
	g.Global.CloneProtocol = "ssh"
	if err := SaveGlobalConfigTo(path, g); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := reread.AuthorityCeiling(); err != nil || c == nil || len(*c) != 0 {
		t.Errorf("after a rewrite AuthorityCeiling = %v, %v", c, err)
	}

	// An absent key stays absent through a rewrite.
	g = parseGlobal(t, "[global]\nclone_protocol = \"ssh\"\n")
	if err := SaveGlobalConfigTo(path, g); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "authority") {
		t.Errorf("an absent ceiling was written:\n%s", body)
	}
}

// TestAuthority_ThreeLayersReachResolve decodes each layer from its own file
// format and resolves them together: every narrowing is attributed to the
// layer that made it, which a merged config could not do.
func TestAuthority_ThreeLayersReachResolve(t *testing.T) {
	res := parseWorkspace(t, "[authority]\nlevel = \"autonomous\"\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n")
	o := parseOverlayBody(t, "[authority]\nlevel = \"supervised\"\n")
	g := parseGlobal(t, "[global]\nauthority_ceiling = [\"merge-pr\", \"close-issue\"]\n")
	p, _, err := AuthorityPolicy(res.Config, o, g)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := authority.Resolve(p, authority.Request{Grants: []authority.Authority{authority.CloseIssue}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Level != authority.Supervised || rec.LevelSource != authority.LayerOverlay {
		t.Errorf("level %s from %s, want supervised from overlay", rec.Level, rec.LevelSource)
	}
	if rec.GrantCeilingSource != authority.LayerMachine || !reflect.DeepEqual(rec.GrantCeiling, []authority.Authority{authority.MergePR, authority.CloseIssue}) {
		t.Errorf("grant ceiling %v from %s", rec.GrantCeiling, rec.GrantCeilingSource)
	}
	if _, err := authority.Resolve(p, authority.Request{Grants: []authority.Authority{authority.CutRelease}}); err == nil {
		t.Error("the machine ceiling did not narrow the grant")
	}
}

func TestAuthority_DroppedNameWarnings(t *testing.T) {
	res := parseWorkspace(t, "[authority]\nceiling = [\"merge-pr\", \"teardown\"]\n")
	o := parseOverlayBody(t, "[authority]\nceiling = [\"cut-release\"]\n")
	g := parseGlobal(t, "[global]\nauthority_ceiling = [\"teardown\"]\n")
	warnings := AuthorityWarnings(res.Config, o, g)
	want := []string{
		`[authority] ceiling names "teardown", an authority this niwa doesn't know; it is ignored and grants nothing`,
		`[global] authority_ceiling names "teardown", an authority this niwa doesn't know; it is ignored and grants nothing`,
		`overlay [authority] ceiling names cut-release, which the workspace [authority] ceiling lacks; it stays ungrantable`,
	}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings =\n%s\nwant\n%s", strings.Join(warnings, "\n"), strings.Join(want, "\n"))
	}
}
