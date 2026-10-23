package authority

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func ceil(as ...Authority) *[]Authority {
	out := append([]Authority{}, as...)
	return &out
}

var all = []Authority{MergePR, CloseIssue, CutRelease}

func TestEffectivePolicy_LevelCeiling(t *testing.T) {
	cases := []struct {
		name                   string
		base, overlay, machine *Level
		wantLevel              Level
		wantSource             Layer
	}{
		{"nothing set", nil, nil, nil, Autonomous, LayerBuiltIn},
		{"workspace only", lvl(Supervised), nil, nil, Supervised, LayerWorkspace},
		{"overlay narrows", lvl(Autonomous), lvl(Supervised), nil, Supervised, LayerOverlay},
		{"overlay wider does not widen", lvl(Strict), lvl(Autonomous), nil, Strict, LayerWorkspace},
		{"machine narrows", lvl(Supervised), lvl(Supervised), lvl(Strict), Strict, LayerMachine},
		{"tie goes to machine", lvl(Strict), lvl(Strict), lvl(Strict), Strict, LayerMachine},
		{"tie overlay over workspace", lvl(Supervised), lvl(Supervised), lvl(Autonomous), Supervised, LayerOverlay},
		{"machine only", nil, nil, lvl(Supervised), Supervised, LayerMachine},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := EffectivePolicy(Policy{BaseLevel: c.base, OverlayLevel: c.overlay, MachineLevel: c.machine})
			if e.LevelCeiling != c.wantLevel || e.LevelCeilingSource != c.wantSource {
				t.Errorf("got %s from %s, want %s from %s", e.LevelCeiling, e.LevelCeilingSource, c.wantLevel, c.wantSource)
			}
		})
	}
}

func TestEffectivePolicy_GrantCeiling(t *testing.T) {
	cases := []struct {
		name                   string
		base, overlay, machine *[]Authority
		want                   []Authority
		source                 Layer
	}{
		{"no layer declares one", nil, nil, nil, []Authority{}, LayerNone},
		{"base declared empty", ceil(), nil, nil, []Authority{}, LayerWorkspace},
		{"machine declared empty, no base", nil, nil, ceil(), []Authority{}, LayerMachine},
		{"overlay declared empty, no base", nil, ceil(), nil, []Authority{}, LayerOverlay},
		{"both narrowing layers, no base", nil, ceil(MergePR), ceil(MergePR), []Authority{}, LayerMachine},
		{"base only", ceil(all...), nil, nil, all, LayerWorkspace},
		{"overlay narrowed", ceil(all...), ceil(MergePR, CloseIssue), nil, []Authority{MergePR, CloseIssue}, LayerOverlay},
		{"machine narrowed", ceil(all...), nil, ceil(MergePR), []Authority{MergePR}, LayerMachine},
		{"both narrowed, machine wins", ceil(all...), ceil(MergePR, CloseIssue), ceil(MergePR, CutRelease), []Authority{MergePR}, LayerMachine},
		{"narrowing layers that remove nothing", ceil(MergePR), ceil(all...), ceil(all...), []Authority{MergePR}, LayerWorkspace},
		{"overlay outside base adds nothing", ceil(MergePR), ceil(CutRelease), nil, []Authority{}, LayerOverlay},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := EffectivePolicy(Policy{BaseCeiling: c.base, OverlayCeiling: c.overlay, MachineCeiling: c.machine})
			if !reflect.DeepEqual(e.GrantCeiling, c.want) || e.GrantCeilingSource != c.source {
				t.Errorf("got %v from %s, want %v from %s", e.GrantCeiling, e.GrantCeilingSource, c.want, c.source)
			}
		})
	}
}

// TestGrantCeilingSource_NoneVersusDeclaredEmpty pins the difference a later
// gate depends on: never opting in versus declaring nothing grantable.
func TestGrantCeilingSource_NoneVersusDeclaredEmpty(t *testing.T) {
	resolve := func(base, overlay, machine RawLayer) Record {
		t.Helper()
		p, _ := mustValidate(t, base, overlay, machine)
		rec, err := Resolve(p, Request{})
		if err != nil {
			t.Fatal(err)
		}
		e := EffectivePolicy(p)
		if e.GrantCeilingSource != rec.GrantCeilingSource || len(e.GrantCeiling) != 0 || len(rec.GrantCeiling) != 0 {
			t.Fatalf("EffectivePolicy %+v and Resolve %+v disagree", e, rec)
		}
		return rec
	}
	rec := resolve(RawLayer{}, RawLayer{}, RawLayer{})
	if rec.GrantCeilingSource != LayerNone {
		t.Errorf("no ceiling anywhere: source %s, want none", rec.GrantCeilingSource)
	}
	b, _ := rec.MarshalJSON()
	if !strings.Contains(string(b), `"grant_ceiling_source": "none"`) {
		t.Errorf("record does not carry grant_ceiling_source none:\n%s", b)
	}
	if rec := resolve(RawLayer{Ceiling: list()}, RawLayer{}, RawLayer{}); rec.GrantCeilingSource != LayerWorkspace {
		t.Errorf("base ceiling = []: source %s", rec.GrantCeilingSource)
	}
	if rec := resolve(RawLayer{}, RawLayer{}, RawLayer{Ceiling: list()}); rec.GrantCeilingSource != LayerMachine {
		t.Errorf("machine authority_ceiling = []: source %s", rec.GrantCeilingSource)
	}
	if rec := resolve(RawLayer{}, RawLayer{Ceiling: list()}, RawLayer{}); rec.GrantCeilingSource != LayerOverlay {
		t.Errorf("overlay ceiling = []: source %s", rec.GrantCeilingSource)
	}
	p, warnings := mustValidate(t, RawLayer{Ceiling: list("teardown")}, RawLayer{}, RawLayer{})
	if p.BaseCeiling == nil || len(*p.BaseCeiling) != 0 || len(warnings) != 1 {
		t.Fatalf("base [teardown]: BaseCeiling %v warnings %v", p.BaseCeiling, warnings)
	}
	if rec := resolve(RawLayer{Ceiling: list("teardown")}, RawLayer{}, RawLayer{}); rec.GrantCeilingSource != LayerWorkspace {
		t.Errorf("base [teardown]: source %s, want workspace, never none", rec.GrantCeilingSource)
	}
}

// TestResolve_ThreeSeparateLayers shows base, overlay and machine reaching
// Resolve as separate inputs: each narrowing is attributed to its own layer.
func TestResolve_ThreeSeparateLayers(t *testing.T) {
	p := Policy{
		BaseLevel: lvl(Autonomous), OverlayLevel: lvl(Supervised),
		BaseCeiling: ceil(all...), MachineCeiling: ceil(MergePR, CloseIssue),
		Roles: map[string][]Authority{"coordinator": all},
	}
	rec, err := Resolve(p, Request{Grants: []Authority{CloseIssue}})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Level != Supervised || rec.LevelSource != LayerOverlay || rec.LevelCeilingSource != LayerOverlay {
		t.Errorf("level %s from %s, ceiling from %s", rec.Level, rec.LevelSource, rec.LevelCeilingSource)
	}
	if rec.GrantCeilingSource != LayerMachine {
		t.Errorf("grant ceiling source %s", rec.GrantCeilingSource)
	}
}

func TestResolve_Level(t *testing.T) {
	p := Policy{BaseLevel: lvl(Supervised)}
	cases := []struct {
		req        *Level
		want       Level
		wantSource Layer
	}{
		{nil, Supervised, LayerWorkspace},
		{lvl(Supervised), Supervised, LayerFlag},
		{lvl(Strict), Strict, LayerFlag},
	}
	for _, c := range cases {
		rec, err := Resolve(p, Request{Level: c.req})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Level != c.want || rec.LevelSource != c.wantSource {
			t.Errorf("request %v: %s from %s, want %s from %s", c.req, rec.Level, rec.LevelSource, c.want, c.wantSource)
		}
	}
	rec, err := Resolve(Policy{}, Request{})
	if err != nil || rec.Level != Autonomous || rec.LevelSource != LayerBuiltIn {
		t.Errorf("no level anywhere: %s from %s (%v)", rec.Level, rec.LevelSource, err)
	}
}

func TestResolve_Refusals(t *testing.T) {
	p := Policy{
		MachineLevel: lvl(Supervised),
		BaseCeiling:  ceil(MergePR),
		Roles:        map[string][]Authority{"coordinator": all, "closer": {CloseIssue}},
	}
	t.Run("wider level", func(t *testing.T) {
		_, err := Resolve(p, Request{Level: lvl(Autonomous)})
		var le *LevelWiderError
		if !errors.As(err, &le) {
			t.Fatalf("err = %v, want *LevelWiderError", err)
		}
		msg := err.Error()
		for _, want := range []string{"--level autonomous", "supervised", "machine"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message %q lacks %q", msg, want)
			}
		}
	})
	t.Run("outside ceiling lists every offender with its route", func(t *testing.T) {
		_, err := Resolve(p, Request{Grants: []Authority{CloseIssue}, Roles: []string{"coordinator"}})
		var oe *OutsideCeilingError
		if !errors.As(err, &oe) {
			t.Fatalf("err = %v, want *OutsideCeilingError", err)
		}
		want := []Offender{
			{Authority: CloseIssue, Routes: []string{"--grant", "role coordinator"}},
			{Authority: CutRelease, Routes: []string{"role coordinator"}},
		}
		if !reflect.DeepEqual(oe.Offenders, want) {
			t.Errorf("offenders = %+v, want %+v", oe.Offenders, want)
		}
		lines := strings.Split(err.Error(), "\n")
		if len(lines) != 2 {
			t.Fatalf("message has %d lines, want one per offender:\n%s", len(lines), err)
		}
		if !strings.Contains(lines[0], "close-issue") || !strings.Contains(lines[0], "--grant, role coordinator") ||
			!strings.Contains(lines[0], "merge-pr from workspace") {
			t.Errorf("line %q", lines[0])
		}
		if !strings.Contains(lines[1], "cut-release") || !strings.Contains(lines[1], "role coordinator") {
			t.Errorf("line %q", lines[1])
		}
	})
	t.Run("no ceiling declared names the key", func(t *testing.T) {
		_, err := Resolve(Policy{}, Request{Grants: []Authority{MergePR}})
		var oe *OutsideCeilingError
		if !errors.As(err, &oe) || !strings.Contains(err.Error(), BaseCeilingKey) || !strings.Contains(err.Error(), "empty") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unknown grant", func(t *testing.T) {
		_, err := Resolve(p, Request{Grants: []Authority{"teardown"}})
		var ue *UnknownValueError
		if !errors.As(err, &ue) || ue.Key != "--grant" || !strings.Contains(err.Error(), `"teardown"`) ||
			!strings.Contains(err.Error(), "merge-pr, close-issue, cut-release") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unknown role", func(t *testing.T) {
		_, err := Resolve(p, Request{Roles: []string{"reviewer"}})
		var ue *UnknownValueError
		if !errors.As(err, &ue) || ue.Key != "--role" || !strings.Contains(err.Error(), `"reviewer"`) ||
			!strings.Contains(err.Error(), "closer, coordinator") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no roles declared names the table", func(t *testing.T) {
		_, err := Resolve(Policy{}, Request{Roles: []string{"coordinator"}})
		want := `invalid --role "coordinator"; no roles are declared in the workspace config's [authority.roles]`
		if err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
}

func TestResolve_Ordering(t *testing.T) {
	p := Policy{
		BaseCeiling: ceil(all...),
		Roles: map[string][]Authority{
			"zeta":  {CloseIssue, MergePR},
			"alpha": {CloseIssue},
		},
	}
	rec, err := Resolve(p, Request{
		Grants: []Authority{CutRelease, CloseIssue, CloseIssue},
		Roles:  []string{"zeta", "alpha", "zeta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rec.Roles, []string{"alpha", "zeta"}) {
		t.Errorf("roles = %v", rec.Roles)
	}
	want := []GrantEntry{
		{Authority: MergePR, Sources: []Source{"role:zeta"}},
		{Authority: CloseIssue, Sources: []Source{"dispatch", "role:alpha", "role:zeta"}},
		{Authority: CutRelease, Sources: []Source{"dispatch"}},
	}
	if !reflect.DeepEqual(rec.Grant, want) {
		t.Errorf("grant = %+v, want %+v", rec.Grant, want)
	}
	if rec.Schema != SchemaV1 || !rec.Present || rec.UnknownGrant == nil || len(rec.UnknownGrant) != 0 {
		t.Errorf("record header %+v", rec)
	}
}

func TestResolve_RecordTooLarge(t *testing.T) {
	roles := map[string][]Authority{}
	var names []string
	for i := 0; i < 1200; i++ {
		name := fmt.Sprintf("role-%059d", i)
		roles[name] = []Authority{MergePR}
		names = append(names, name)
	}
	p := Policy{BaseCeiling: ceil(all...), Roles: roles}
	_, err := Resolve(p, Request{Roles: names})
	var te *RecordTooLargeError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want *RecordTooLargeError", err)
	}
	if te.Size <= MaxRecordBytes {
		t.Errorf("size %d not over %d", te.Size, MaxRecordBytes)
	}
	if _, err := Resolve(p, Request{Roles: names[:10]}); err != nil {
		t.Errorf("a small request was refused: %v", err)
	}
}

func TestEffectivePolicy_AbsentBaseCeilingGrantsNothing(t *testing.T) {
	_, err := Resolve(Policy{Roles: map[string][]Authority{"r": {MergePR}}}, Request{Roles: []string{"r"}})
	var oe *OutsideCeilingError
	if !errors.As(err, &oe) {
		t.Errorf("a role grant with no ceiling anywhere: err = %v, want *OutsideCeilingError", err)
	}
	rec, err := Resolve(Policy{Roles: map[string][]Authority{"r": {}}}, Request{Roles: []string{"r"}})
	if err != nil || len(rec.Grant) != 0 || !reflect.DeepEqual(rec.Roles, []string{"r"}) {
		t.Errorf("an empty role: %+v, %v", rec, err)
	}
}
