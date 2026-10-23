package authority

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func list(names ...string) []any {
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}

func roleTable(roles map[string][]string) map[string]any {
	out := map[string]any{}
	for name, auths := range roles {
		out[name] = map[string]any{"authorities": list(auths...)}
	}
	return out
}

func mustValidate(t *testing.T, base, overlay, machine RawLayer) (Policy, []string) {
	t.Helper()
	p, warnings, err := ValidateConfig(base, overlay, machine)
	if err != nil {
		t.Fatalf("ValidateConfig: %v", err)
	}
	return p, warnings
}

func configErr(t *testing.T, base, overlay, machine RawLayer) *ConfigError {
	t.Helper()
	_, _, err := ValidateConfig(base, overlay, machine)
	var ce *ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("ValidateConfig err = %v, want *ConfigError", err)
	}
	return ce
}

func TestValidateConfig_FullBase(t *testing.T) {
	p, warnings := mustValidate(t, RawLayer{
		Level:   "supervised",
		Ceiling: list("cut-release", "merge-pr", "merge-pr"),
		Roles:   roleTable(map[string][]string{"coordinator": {"merge-pr", "close-issue", "cut-release"}, "empty": {}}),
	}, RawLayer{}, RawLayer{})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if p.BaseLevel == nil || *p.BaseLevel != Supervised {
		t.Errorf("BaseLevel = %v", p.BaseLevel)
	}
	if p.BaseCeiling == nil || !reflect.DeepEqual(*p.BaseCeiling, []Authority{MergePR, CutRelease}) {
		t.Errorf("BaseCeiling = %v, want de-duplicated and in R3 order", p.BaseCeiling)
	}
	if got := p.Roles["coordinator"]; !reflect.DeepEqual(got, []Authority{MergePR, CloseIssue, CutRelease}) {
		t.Errorf("coordinator = %v", got)
	}
	if got, ok := p.Roles["empty"]; !ok || len(got) != 0 {
		t.Errorf("empty role = %v, %v; a role with no authorities is valid and grants nothing", got, ok)
	}
	if p.OverlayCeiling != nil || p.MachineCeiling != nil || p.OverlayLevel != nil || p.MachineLevel != nil {
		t.Errorf("unset layers came back set: %+v", p)
	}
}

func TestValidateConfig_CeilingPresence(t *testing.T) {
	p, _ := mustValidate(t, RawLayer{}, RawLayer{}, RawLayer{})
	if p.BaseCeiling != nil {
		t.Errorf("absent base ceiling = %v, want nil", *p.BaseCeiling)
	}
	p, _ = mustValidate(t, RawLayer{Ceiling: list()}, RawLayer{Ceiling: []any{}}, RawLayer{Ceiling: []string{}})
	for name, c := range map[string]*[]Authority{"base": p.BaseCeiling, "overlay": p.OverlayCeiling, "machine": p.MachineCeiling} {
		if c == nil || len(*c) != 0 {
			t.Errorf("%s ceiling = [] decoded as %v, want present and empty", name, c)
		}
	}
}

// TestValidateConfig_UnknownAuthorityNames covers design Decision 6: a
// well-formed name this niwa doesn't know is dropped with a warning naming
// the name and the key, and a list it empties stays declared.
func TestValidateConfig_UnknownAuthorityNames(t *testing.T) {
	t.Run("base ceiling", func(t *testing.T) {
		p, warnings := mustValidate(t, RawLayer{Ceiling: list("merge-pr", "teardown")}, RawLayer{}, RawLayer{})
		if !reflect.DeepEqual(*p.BaseCeiling, []Authority{MergePR}) {
			t.Errorf("BaseCeiling = %v", *p.BaseCeiling)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], `"teardown"`) || !strings.Contains(warnings[0], "[authority] ceiling") {
			t.Errorf("warnings = %v, want one naming teardown and [authority] ceiling", warnings)
		}
	})
	t.Run("base ceiling of only unknown names stays declared", func(t *testing.T) {
		p, warnings := mustValidate(t, RawLayer{Ceiling: list("teardown")}, RawLayer{}, RawLayer{})
		if p.BaseCeiling == nil || len(*p.BaseCeiling) != 0 {
			t.Errorf("BaseCeiling = %v, want non-nil and empty", p.BaseCeiling)
		}
		if len(warnings) != 1 {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("role", func(t *testing.T) {
		p, warnings := mustValidate(t, RawLayer{Roles: roleTable(map[string][]string{"coordinator": {"merge-pr", "teardown"}})}, RawLayer{}, RawLayer{})
		if !reflect.DeepEqual(p.Roles["coordinator"], []Authority{MergePR}) {
			t.Errorf("coordinator = %v", p.Roles["coordinator"])
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "coordinator") || !strings.Contains(warnings[0], `"teardown"`) {
			t.Errorf("warnings = %v, want one naming the role and teardown", warnings)
		}
	})
	t.Run("overlay and machine ceilings become present and empty", func(t *testing.T) {
		p, warnings := mustValidate(t, RawLayer{Ceiling: list("merge-pr")}, RawLayer{Ceiling: list("teardown")}, RawLayer{Ceiling: list("teardown")})
		if p.OverlayCeiling == nil || len(*p.OverlayCeiling) != 0 {
			t.Errorf("OverlayCeiling = %v", p.OverlayCeiling)
		}
		if p.MachineCeiling == nil || len(*p.MachineCeiling) != 0 {
			t.Errorf("MachineCeiling = %v", p.MachineCeiling)
		}
		if len(warnings) != 2 || !strings.Contains(warnings[0], OverlayCeilingKey) || !strings.Contains(warnings[1], MachineCeilingKey) {
			t.Errorf("warnings = %v", warnings)
		}
		// Only the dropped-name warning, never also the base-lacks one.
		if cw := CeilingWarnings(p); len(cw) != 0 {
			t.Errorf("CeilingWarnings = %v, want none for a dropped name", cw)
		}
	})
	t.Run("merge and release", func(t *testing.T) {
		p, warnings := mustValidate(t, RawLayer{Ceiling: list("merge"), Roles: roleTable(map[string][]string{"r": {"release"}})}, RawLayer{}, RawLayer{})
		if len(*p.BaseCeiling) != 0 || len(p.Roles["r"]) != 0 || len(warnings) != 2 {
			t.Errorf("policy %+v warnings %v", p, warnings)
		}
	})
	t.Run("malformed names stay invalid", func(t *testing.T) {
		for _, bad := range []string{"Merge-PR", "merge pr"} {
			ce := configErr(t, RawLayer{Ceiling: list(bad)}, RawLayer{}, RawLayer{})
			if len(ce.Problems) != 1 || ce.Problems[0].Key != BaseCeilingKey || ce.Problems[0].Value != bad {
				t.Errorf("%q: problems = %v", bad, ce)
			}
		}
	})
}

func TestValidateConfig_WrongTypes(t *testing.T) {
	cases := []struct {
		name                   string
		base, overlay, machine RawLayer
		key                    string
	}{
		{"base ceiling string", RawLayer{Ceiling: "merge-pr"}, RawLayer{}, RawLayer{}, "[authority] ceiling"},
		{"base level int", RawLayer{Level: int64(3)}, RawLayer{}, RawLayer{}, "[authority] level"},
		{"machine ceiling string", RawLayer{}, RawLayer{}, RawLayer{Ceiling: "x"}, "[global] authority_ceiling"},
		{"overlay level unknown", RawLayer{}, RawLayer{Level: "unbounded"}, RawLayer{}, "overlay [authority] level"},
		{"machine level mixed case", RawLayer{}, RawLayer{}, RawLayer{Level: "Strict"}, "[global] authority_level"},
		{"ceiling entry not a string", RawLayer{Ceiling: []any{int64(1)}}, RawLayer{}, RawLayer{}, "[authority] ceiling"},
		{"roles not a table", RawLayer{Roles: "x"}, RawLayer{}, RawLayer{}, "[authority.roles]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := configErr(t, c.base, c.overlay, c.machine)
			if len(ce.Problems) != 1 || ce.Problems[0].Key != c.key {
				t.Fatalf("problems = %v, want one for %s", ce, c.key)
			}
			if !strings.Contains(ce.Error(), "accepted values are") {
				t.Errorf("error %q does not name the accepted values", ce)
			}
		})
	}
}

func TestValidateConfig_UnknownKeys(t *testing.T) {
	ce := configErr(t,
		RawLayer{UnknownKeys: []string{"ceilng"}},
		RawLayer{UnknownKeys: []string{"levle"}},
		RawLayer{UnknownKeys: []string{"authority_levle"}})
	want := []string{
		`invalid [authority] key "ceilng"; accepted values are level, ceiling, roles`,
		`invalid overlay [authority] key "levle"; accepted values are level, ceiling`,
		`invalid [global] key "authority_levle"; accepted values are authority_level, authority_ceiling`,
	}
	if got := strings.Split(ce.Error(), "\n"); !reflect.DeepEqual(got, want) {
		t.Errorf("errors =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestValidateConfig_RoleTable(t *testing.T) {
	ce := configErr(t, RawLayer{Roles: map[string]any{
		"coordinator": map[string]any{"authoritys": list("merge-pr")},
		"Bad":         map[string]any{"authorities": list()},
	}}, RawLayer{}, RawLayer{})
	msg := ce.Error()
	if !strings.Contains(msg, `[authority.roles.coordinator] key "authoritys"`) {
		t.Errorf("error %q does not name the role and the misspelt key", msg)
	}
	if !strings.Contains(msg, `role name "Bad"`) {
		t.Errorf("error %q does not name the bad role name", msg)
	}
}

func TestValidateConfig_EscapesControlBytes(t *testing.T) {
	ce := configErr(t, RawLayer{Level: "str\x1b[31mict"}, RawLayer{}, RawLayer{})
	msg := ce.Error()
	if strings.ContainsRune(msg, '\x1b') {
		t.Errorf("error %q carries a raw escape byte", msg)
	}
	if !strings.Contains(msg, `"str\x1b[31mict"`) {
		t.Errorf("error %q does not quote the escaped value", msg)
	}
	if !strings.Contains(msg, "[authority] level") || !strings.Contains(msg, "autonomous, supervised, strict") {
		t.Errorf("error %q does not name the key and accepted values", msg)
	}
}

func TestCeilingWarnings_NamesTheLayer(t *testing.T) {
	p, _ := mustValidate(t, RawLayer{Ceiling: list("merge-pr")}, RawLayer{Ceiling: list("cut-release")}, RawLayer{Ceiling: list("cut-release")})
	got := CeilingWarnings(p)
	if len(got) != 2 {
		t.Fatalf("CeilingWarnings = %v", got)
	}
	if !strings.Contains(got[0], "cut-release") || !strings.Contains(got[0], "overlay") {
		t.Errorf("overlay warning %q", got[0])
	}
	if !strings.Contains(got[1], "cut-release") || !strings.Contains(got[1], MachineCeilingKey) {
		t.Errorf("machine warning %q", got[1])
	}
	if e := EffectivePolicy(p); len(e.GrantCeiling) != 0 {
		t.Errorf("cut-release became grantable: %v", e.GrantCeiling)
	}
}
