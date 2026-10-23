package authority

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PolicySchemaV1 is the schema string of `niwa authority policy --json`. The
// document grows additively under the same rule as the record.
const PolicySchemaV1 = "niwa.authority.policy/v1"

// RoleView is one declared role as the policy report shows it.
type RoleView struct {
	Name           string
	Authorities    []Authority // the role's known authorities, R3 order
	OutsideCeiling []Authority // those outside the effective grant ceiling
}

// PolicyReport is what `niwa authority policy` prints: each layer's declared
// values, the effective ceilings with their sources, the roles, and what a
// dispatch with no authority flags would resolve to.
type PolicyReport struct {
	Policy            Policy
	Effective         Effective
	Roles             []RoleView
	NoFlagLevel       Level
	NoFlagLevelSource Layer
	Warnings          []string
}

// BuildPolicyReport assembles the report for a validated policy.
func BuildPolicyReport(p Policy, warnings []string) PolicyReport {
	e := EffectivePolicy(p)
	in := map[Authority]bool{}
	for _, a := range e.GrantCeiling {
		in[a] = true
	}
	names := make([]string, 0, len(p.Roles))
	for name := range p.Roles {
		names = append(names, name)
	}
	sort.Strings(names)
	roles := make([]RoleView, 0, len(names))
	for _, name := range names {
		v := RoleView{Name: name, Authorities: append([]Authority{}, p.Roles[name]...), OutsideCeiling: []Authority{}}
		for _, a := range v.Authorities {
			if !in[a] {
				v.OutsideCeiling = append(v.OutsideCeiling, a)
			}
		}
		roles = append(roles, v)
	}
	if warnings == nil {
		warnings = []string{}
	}
	// A request with no flags can't be refused: it asks for no level and no
	// authority, so it resolves to the level ceiling and an empty grant.
	return PolicyReport{
		Policy:            p,
		Effective:         e,
		Roles:             roles,
		NoFlagLevel:       e.LevelCeiling,
		NoFlagLevelSource: e.LevelCeilingSource,
		Warnings:          warnings,
	}
}

type policyLayerDoc struct {
	Level   *string   `json:"level"`
	Ceiling *[]string `json:"ceiling"`
}

type policyRoleDoc struct {
	Name           string   `json:"name"`
	Authorities    []string `json:"authorities"`
	OutsideCeiling []string `json:"outside_ceiling"`
}

type policyNoFlagDoc struct {
	Level       string     `json:"level"`
	LevelSource string     `json:"level_source"`
	Grant       []grantDoc `json:"grant"`
}

// policyDoc fixes the document's top-level key order.
type policyDoc struct {
	Schema             string          `json:"schema"`
	Workspace          policyLayerDoc  `json:"workspace"`
	Overlay            policyLayerDoc  `json:"overlay"`
	Machine            policyLayerDoc  `json:"machine"`
	LevelCeiling       string          `json:"level_ceiling"`
	LevelCeilingSource string          `json:"level_ceiling_source"`
	GrantCeiling       []string        `json:"grant_ceiling"`
	GrantCeilingSource string          `json:"grant_ceiling_source"`
	Roles              []policyRoleDoc `json:"roles"`
	NoFlagDispatch     policyNoFlagDoc `json:"no_flag_dispatch"`
	Warnings           []string        `json:"warnings"`
}

func names(list []Authority) []string {
	out := make([]string, len(list))
	for i, a := range list {
		out[i] = string(a)
	}
	return out
}

func layerDoc(level *Level, ceiling *[]Authority) policyLayerDoc {
	var d policyLayerDoc
	if level != nil {
		s := level.String()
		d.Level = &s
	}
	if ceiling != nil {
		c := names(*ceiling)
		d.Ceiling = &c
	}
	return d
}

// MarshalJSON writes the niwa.authority.policy/v1 document, indented. A layer
// value that isn't set is null.
func (r PolicyReport) MarshalJSON() ([]byte, error) {
	doc := policyDoc{
		Schema:             PolicySchemaV1,
		Workspace:          layerDoc(r.Policy.BaseLevel, r.Policy.BaseCeiling),
		Overlay:            layerDoc(r.Policy.OverlayLevel, r.Policy.OverlayCeiling),
		Machine:            layerDoc(r.Policy.MachineLevel, r.Policy.MachineCeiling),
		LevelCeiling:       r.Effective.LevelCeiling.String(),
		LevelCeilingSource: string(r.Effective.LevelCeilingSource),
		GrantCeiling:       names(r.Effective.GrantCeiling),
		GrantCeilingSource: string(r.Effective.GrantCeilingSource),
		Roles:              []policyRoleDoc{},
		NoFlagDispatch: policyNoFlagDoc{
			Level:       r.NoFlagLevel.String(),
			LevelSource: string(r.NoFlagLevelSource),
			Grant:       []grantDoc{},
		},
		Warnings: append([]string{}, r.Warnings...),
	}
	for _, role := range r.Roles {
		doc.Roles = append(doc.Roles, policyRoleDoc{
			Name:           role.Name,
			Authorities:    names(role.Authorities),
			OutsideCeiling: names(role.OutsideCeiling),
		})
	}
	return json.MarshalIndent(doc, "", "  ")
}

func levelText(l *Level) string {
	if l == nil {
		return "not set"
	}
	return l.String()
}

func ceilingText(c *[]Authority) string {
	if c == nil {
		return "not set"
	}
	return listText(*c)
}

func listText(list []Authority) string {
	if len(list) == 0 {
		return "(empty)"
	}
	return strings.Join(names(list), ", ")
}

// Text renders the report for a terminal. Warnings are not part of it; the
// caller prints them to stderr.
func (r PolicyReport) Text() string {
	var b strings.Builder
	layer := func(name string, level *Level, ceiling *[]Authority) {
		fmt.Fprintf(&b, "%-10s level %s; ceiling %s\n", name+":", levelText(level), ceilingText(ceiling))
	}
	layer("workspace", r.Policy.BaseLevel, r.Policy.BaseCeiling)
	layer("overlay", r.Policy.OverlayLevel, r.Policy.OverlayCeiling)
	layer("machine", r.Policy.MachineLevel, r.Policy.MachineCeiling)
	fmt.Fprintf(&b, "level ceiling: %s from %s\n", r.Effective.LevelCeiling, r.Effective.LevelCeilingSource)
	fmt.Fprintf(&b, "grant ceiling: %s from %s\n", listText(r.Effective.GrantCeiling), r.Effective.GrantCeilingSource)
	if len(r.Roles) == 0 {
		b.WriteString("roles: none\n")
	} else {
		b.WriteString("roles:\n")
		for _, role := range r.Roles {
			outside := map[Authority]bool{}
			for _, a := range role.OutsideCeiling {
				outside[a] = true
			}
			parts := make([]string, len(role.Authorities))
			for i, a := range role.Authorities {
				parts[i] = string(a)
				if outside[a] {
					parts[i] += " (outside ceiling)"
				}
			}
			list := "(empty)"
			if len(parts) > 0 {
				list = strings.Join(parts, ", ")
			}
			fmt.Fprintf(&b, "  %s: %s\n", role.Name, list)
		}
	}
	fmt.Fprintf(&b, "no-flag dispatch: level %s from %s; grant none\n", r.NoFlagLevel, r.NoFlagLevelSource)
	return b.String()
}
