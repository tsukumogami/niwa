package authority

import (
	"fmt"
	"sort"
	"strings"
)

// RawLayer is one config layer's [authority] values as decoded, still
// untyped. The config loaders decode these keys loosely so that a wrong type
// (ceiling = "merge-pr", level = 3) reaches ValidateConfig as a bad value
// instead of failing the whole config decode, which would stop niwa apply
// from pulling the fix.
type RawLayer struct {
	Level   any // nil when the key is absent
	Ceiling any // nil when the key is absent; a list when set
	// Roles is the base layer's [authority.roles] table: role name to a
	// table holding `authorities`. ValidateConfig reads it from the base
	// layer only; an overlay's roles are a tombstone the config package
	// reports, and the machine layer has none.
	Roles any
	// UnknownKeys are the undecoded keys under the layer's table, relative
	// to it: "ceilng" under [authority], "authority_levle" under [global].
	UnknownKeys []string
}

// Policy is what the three config layers declare, validated and typed.
type Policy struct {
	BaseLevel, OverlayLevel, MachineLevel *Level
	// BaseCeiling is nil when [authority] ceiling is absent and a non-nil
	// (possibly empty) list when it is declared. A list that Decision 6's
	// drop of unknown names emptied stays non-nil: it is still declared.
	BaseCeiling *[]Authority
	// OverlayCeiling and MachineCeiling are nil when not declared, and then
	// narrow nothing. Declared, they narrow, even when empty.
	OverlayCeiling, MachineCeiling *[]Authority
	// Roles maps each declared role to its known authorities, in R3 order.
	Roles map[string][]Authority
}

// InvalidValueError is one invalid config value: the key, the value as
// written, and what the key accepts.
type InvalidValueError struct {
	Key      string
	Value    string
	Accepted string
}

func (e *InvalidValueError) Error() string {
	return fmt.Sprintf("invalid %s %s; accepted values are %s", e.Key, quote(e.Value), e.Accepted)
}

// ConfigError is every invalid value ValidateConfig found. Dispatch and the
// authority commands refuse on it; niwa apply prints each problem as a
// warning and carries on.
type ConfigError struct {
	Problems []*InvalidValueError
}

func (e *ConfigError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.Error()
	}
	return strings.Join(lines, "\n")
}

// layerNames is how messages name one layer's keys.
type layerNames struct {
	table    string // the table unknown keys are reported under
	level    string
	ceiling  string
	accepted string // the keys the table accepts
}

var (
	baseNames = layerNames{
		table: "[authority]", level: "[authority] level", ceiling: "[authority] ceiling",
		accepted: "level, ceiling, roles",
	}
	overlayNames = layerNames{
		table: "overlay [authority]", level: "overlay [authority] level", ceiling: "overlay [authority] ceiling",
		accepted: "level, ceiling",
	}
	machineNames = layerNames{
		table: "[global]", level: "[global] authority_level", ceiling: "[global] authority_ceiling",
		accepted: "authority_level, authority_ceiling",
	}
)

// BaseCeilingKey, OverlayCeilingKey and MachineCeilingKey name each layer's
// ceiling key the way warnings and messages print it.
const (
	BaseCeilingKey    = "[authority] ceiling"
	OverlayCeilingKey = "overlay [authority] ceiling"
	MachineCeilingKey = "[global] authority_ceiling"
)

// ValidateConfig checks the three raw layers and returns the typed Policy.
//
// It reports every invalid value it finds (a wrong type, a level outside the
// three, a ceiling or role entry failing the name pattern, a bad role name, a
// misspelt key inside a role table, an unknown key under [authority] or an
// unknown authority_ key under [global]) in one *ConfigError naming the key,
// the quoted value and the accepted values. An authority name that matches
// the name pattern but isn't one this niwa knows is not an error (design
// Decision 6): it is dropped from the list that names it, with a warning, so
// a config written for a newer niwa keeps working here. The Policy is filled
// in from the valid values even when an error is returned, so a caller that
// only warns (niwa apply) can still report on the rest.
func ValidateConfig(base, overlay, machine RawLayer) (Policy, []string, error) {
	v := &validator{}
	var p Policy
	p.BaseLevel = v.level(base.Level, baseNames)
	p.OverlayLevel = v.level(overlay.Level, overlayNames)
	p.MachineLevel = v.level(machine.Level, machineNames)
	p.BaseCeiling = v.list(base.Ceiling, baseNames.ceiling)
	p.OverlayCeiling = v.list(overlay.Ceiling, overlayNames.ceiling)
	p.MachineCeiling = v.list(machine.Ceiling, machineNames.ceiling)
	p.Roles = v.roles(base.Roles)
	v.unknownKeys(base.UnknownKeys, baseNames)
	v.unknownKeys(overlay.UnknownKeys, overlayNames)
	v.unknownKeys(machine.UnknownKeys, machineNames)
	if len(v.problems) > 0 {
		return p, v.warnings, &ConfigError{Problems: v.problems}
	}
	return p, v.warnings, nil
}

type validator struct {
	problems []*InvalidValueError
	warnings []string
}

func (v *validator) invalid(key, value, accepted string) {
	v.problems = append(v.problems, &InvalidValueError{Key: key, Value: value, Accepted: accepted})
}

// render prints a decoded value of the wrong type for a message.
func render(x any) string {
	switch t := x.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", t)
	}
}

func (v *validator) level(raw any, names layerNames) *Level {
	if raw == nil {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		v.invalid(names.level, render(raw), strings.Join(levelNames, ", "))
		return nil
	}
	l, err := ParseLevel(s)
	if err != nil {
		v.invalid(names.level, strings.TrimSpace(s), strings.Join(levelNames, ", "))
		return nil
	}
	return l
}

// asList accepts the shapes a decoded list arrives in: []any from TOML and
// []string from a typed caller.
func asList(raw any) ([]any, bool) {
	switch t := raw.(type) {
	case []any:
		return t, true
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out, true
	}
	return nil, false
}

// list validates a list of authority names. It returns nil only for an
// absent key; a declared list comes back non-nil, de-duplicated and in R3
// order, with unknown well-formed names dropped and warned about.
func (v *validator) list(raw any, key string) *[]Authority {
	if raw == nil {
		return nil
	}
	accepted := "a list of " + strings.Join(AuthorityNames(), ", ")
	items, ok := asList(raw)
	if !ok {
		v.invalid(key, render(raw), accepted)
		return nil
	}
	out := []Authority{}
	seen := map[Authority]bool{}
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			v.invalid(key, render(item), accepted)
			continue
		}
		a, err := ParseAuthority(s)
		if err != nil {
			uv := err.(*UnknownValueError)
			if uv.WellFormed {
				v.warnings = append(v.warnings, fmt.Sprintf(
					"%s names %s, an authority this niwa doesn't know; it is ignored and grants nothing",
					key, quote(uv.Value)))
				continue
			}
			v.invalid(key, uv.Value, accepted)
			continue
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sortAuthorities(out)
	return &out
}

func (v *validator) roles(raw any) map[string][]Authority {
	out := map[string][]Authority{}
	if raw == nil {
		return out
	}
	table, ok := raw.(map[string]any)
	if !ok {
		v.invalid("[authority.roles]", render(raw), "a table of roles, each with an authorities list")
		return out
	}
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !ValidName(name) {
			v.invalid("[authority.roles] role name", name, "names matching "+NamePatternText)
			continue
		}
		roleKey := "[authority.roles." + name + "]"
		body, ok := table[name].(map[string]any)
		if !ok {
			v.invalid(roleKey, render(table[name]), "a table with an authorities list")
			continue
		}
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k != "authorities" {
				v.invalid(roleKey+" key", k, "authorities")
			}
		}
		list := v.list(body["authorities"], roleKey+" authorities")
		if list == nil {
			out[name] = []Authority{}
			continue
		}
		out[name] = *list
	}
	return out
}

func (v *validator) unknownKeys(keys []string, names layerNames) {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for _, k := range sorted {
		v.invalid(names.table+" key", k, names.accepted)
	}
}

// CeilingWarnings returns one warning for each authority an overlay or
// machine ceiling names that the base ceiling lacks. The intersection drops
// such an authority, so it stays ungrantable; the warning says so. Names this
// niwa doesn't know never reach here (ValidateConfig dropped them with their
// own warning), so a name gets one warning, not two.
func CeilingWarnings(p Policy) []string {
	base := map[Authority]bool{}
	if p.BaseCeiling != nil {
		for _, a := range *p.BaseCeiling {
			base[a] = true
		}
	}
	var out []string
	check := func(list *[]Authority, key string) {
		if list == nil {
			return
		}
		for _, a := range *list {
			if !base[a] {
				out = append(out, fmt.Sprintf(
					"%s names %s, which the workspace %s lacks; it stays ungrantable",
					key, a, BaseCeilingKey))
			}
		}
	}
	check(p.OverlayCeiling, OverlayCeilingKey)
	check(p.MachineCeiling, MachineCeilingKey)
	return out
}
