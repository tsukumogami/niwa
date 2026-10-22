package authority

import (
	"fmt"
	"sort"
	"strings"
)

// MaxRecordBytes caps an encoded authority record. Resolve refuses a request
// whose record would exceed it, and the reader refuses a stored file larger
// than it, so a record dispatch writes is always one the reader accepts.
const MaxRecordBytes = 64 << 10

// Effective is what the policy resolves to before any request.
type Effective struct {
	LevelCeiling       Level
	LevelCeilingSource Layer
	GrantCeiling       []Authority
	GrantCeilingSource Layer
}

// EffectivePolicy narrows the three layers to the level ceiling and the grant
// ceiling, each with the layer that supplied it.
//
// The level ceiling is the narrowest declared level; a tie goes to machine,
// then overlay, then workspace, and with no level anywhere it is the built-in
// autonomous.
//
// The grant ceiling is the base ceiling (empty when absent) intersected with
// each narrowing layer that declares one. Its source is none when no layer
// declares a ceiling at all, so a later gate can tell a workspace that never
// opted in from one that declared an empty ceiling. Otherwise it is machine
// when the machine list removed an authority from the base list, else overlay
// when the overlay list did, else workspace when the base declares one, else
// the narrowing layer that declares one, machine first.
func EffectivePolicy(p Policy) Effective {
	e := Effective{LevelCeiling: Autonomous, LevelCeilingSource: LayerBuiltIn}
	found := false
	for _, c := range []struct {
		level *Level
		layer Layer
	}{
		{p.MachineLevel, LayerMachine},
		{p.OverlayLevel, LayerOverlay},
		{p.BaseLevel, LayerWorkspace},
	} {
		if c.level == nil {
			continue
		}
		if !found || *c.level > e.LevelCeiling {
			e.LevelCeiling, e.LevelCeilingSource = *c.level, c.layer
			found = true
		}
	}

	if p.BaseCeiling == nil && p.OverlayCeiling == nil && p.MachineCeiling == nil {
		e.GrantCeiling, e.GrantCeilingSource = []Authority{}, LayerNone
		return e
	}
	var base []Authority
	if p.BaseCeiling != nil {
		base = *p.BaseCeiling
	}
	ceiling := append([]Authority{}, base...)
	if p.OverlayCeiling != nil {
		ceiling = intersect(ceiling, *p.OverlayCeiling)
	}
	if p.MachineCeiling != nil {
		ceiling = intersect(ceiling, *p.MachineCeiling)
	}
	sortAuthorities(ceiling)
	e.GrantCeiling = ceiling

	switch {
	case p.MachineCeiling != nil && len(intersect(base, *p.MachineCeiling)) < len(base):
		e.GrantCeilingSource = LayerMachine
	case p.OverlayCeiling != nil && len(intersect(base, *p.OverlayCeiling)) < len(base):
		e.GrantCeilingSource = LayerOverlay
	case p.BaseCeiling != nil:
		e.GrantCeilingSource = LayerWorkspace
	case p.MachineCeiling != nil:
		e.GrantCeilingSource = LayerMachine
	default:
		e.GrantCeilingSource = LayerOverlay
	}
	return e
}

// intersect keeps the entries of a that b also holds, in a's order.
func intersect(a, b []Authority) []Authority {
	in := map[Authority]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []Authority{}
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}

// Request is what one dispatch asks for: --level, --grant and --role.
type Request struct {
	Level  *Level
	Grants []Authority
	Roles  []string
}

// Source is where a granted authority came from: "dispatch" for --grant,
// "role:<name>" for a role.
type Source string

// SourceDispatch is the source of an authority granted with --grant.
const SourceDispatch Source = "dispatch"

// RoleSource is the source of an authority granted through a role.
func RoleSource(role string) Source { return Source("role:" + role) }

// GrantEntry is one granted authority with every source it came from,
// dispatch first and then roles by name.
type GrantEntry struct {
	Authority Authority
	Sources   []Source
}

// RawGrantEntry is a stored grant entry this build can't hold: an authority
// it doesn't know, or a known one whose sources are all of kinds it doesn't
// know. Names are kept as stored.
type RawGrantEntry struct {
	Authority string
	Sources   []string
}

// LevelWiderError refuses a --level wider than the level ceiling.
type LevelWiderError struct {
	Requested     Level
	Ceiling       Level
	CeilingSource Layer
}

func (e *LevelWiderError) Error() string {
	return fmt.Sprintf("--level %s is wider than the authority level ceiling %s from %s",
		e.Requested, e.Ceiling, e.CeilingSource)
}

// Offender is one requested authority outside the grant ceiling, with every
// route that asked for it: "--grant" or "role <name>".
type Offender struct {
	Authority Authority
	Routes    []string
}

// OutsideCeilingError refuses a request naming an authority outside the
// effective grant ceiling. It lists every offending authority, not just the
// first.
type OutsideCeilingError struct {
	Offenders     []Offender
	Ceiling       []Authority
	CeilingSource Layer
}

func (e *OutsideCeilingError) Error() string {
	ceiling := "empty"
	if len(e.Ceiling) > 0 {
		names := make([]string, len(e.Ceiling))
		for i, a := range e.Ceiling {
			names[i] = string(a)
		}
		ceiling = strings.Join(names, ", ")
	}
	var where string
	if e.CeilingSource == LayerNone {
		where = fmt.Sprintf("the grant ceiling is empty because no layer declares one; declare it in the workspace config's %s", BaseCeilingKey)
	} else {
		where = fmt.Sprintf("the grant ceiling is %s from %s", ceiling, e.CeilingSource)
	}
	lines := make([]string, len(e.Offenders))
	for i, o := range e.Offenders {
		lines[i] = fmt.Sprintf("authority %s (requested by %s) is outside the grant ceiling: %s",
			o.Authority, strings.Join(o.Routes, ", "), where)
	}
	return strings.Join(lines, "\n")
}

// RecordTooLargeError refuses a request whose record would exceed
// MaxRecordBytes.
type RecordTooLargeError struct {
	Size int
}

func (e *RecordTooLargeError) Error() string {
	return fmt.Sprintf("the authority record for this request would be %d bytes, over the %d-byte limit; request fewer roles",
		e.Size, MaxRecordBytes)
}

// Resolve resolves a request against the policy and returns the record's
// policy fields; dispatch stamps the identity fields (dispatch id and parent
// links) itself. It refuses with *UnknownValueError (an unknown authority on
// --grant or an undeclared role), *LevelWiderError, *OutsideCeilingError or
// *RecordTooLargeError. A request is never trimmed: it gets everything it
// asked for or nothing.
func Resolve(p Policy, r Request) (Record, error) {
	e := EffectivePolicy(p)

	for _, a := range r.Grants {
		if !a.Known() {
			return Record{}, &UnknownValueError{Key: "--grant", Value: string(a), Accepted: AuthorityNames(), WellFormed: ValidName(string(a))}
		}
	}
	roles := dedupSorted(r.Roles)
	for _, role := range roles {
		if _, ok := p.Roles[role]; !ok {
			return Record{}, &UnknownValueError{Key: "--role", Value: role, Accepted: declaredRoles(p)}
		}
	}

	level, levelSource := e.LevelCeiling, e.LevelCeilingSource
	if r.Level != nil {
		if *r.Level < e.LevelCeiling {
			return Record{}, &LevelWiderError{Requested: *r.Level, Ceiling: e.LevelCeiling, CeilingSource: e.LevelCeilingSource}
		}
		level, levelSource = *r.Level, LayerFlag
	}

	sources := map[Authority][]Source{}
	routes := map[Authority][]string{}
	add := func(a Authority, s Source, route string) {
		for _, have := range sources[a] {
			if have == s {
				return
			}
		}
		sources[a] = append(sources[a], s)
		routes[a] = append(routes[a], route)
	}
	for _, a := range r.Grants {
		add(a, SourceDispatch, "--grant")
	}
	for _, role := range roles {
		for _, a := range p.Roles[role] {
			add(a, RoleSource(role), "role "+role)
		}
	}

	inCeiling := map[Authority]bool{}
	for _, a := range e.GrantCeiling {
		inCeiling[a] = true
	}
	var grant []GrantEntry
	var offenders []Offender
	for _, a := range knownAuthorities {
		if len(sources[a]) == 0 {
			continue
		}
		if !inCeiling[a] {
			offenders = append(offenders, Offender{Authority: a, Routes: routes[a]})
			continue
		}
		grant = append(grant, GrantEntry{Authority: a, Sources: sources[a]})
	}
	if len(offenders) > 0 {
		return Record{}, &OutsideCeilingError{Offenders: offenders, Ceiling: e.GrantCeiling, CeilingSource: e.GrantCeilingSource}
	}
	if grant == nil {
		grant = []GrantEntry{}
	}

	rec := Record{
		Schema:               SchemaV1,
		Present:              true,
		ParentDispatchSource: ParentSourceNone,
		Level:                level,
		LevelSource:          levelSource,
		LevelCeiling:         e.LevelCeiling,
		LevelCeilingSource:   e.LevelCeilingSource,
		GrantCeiling:         e.GrantCeiling,
		GrantCeilingSource:   e.GrantCeilingSource,
		Roles:                roles,
		Grant:                grant,
		UnknownGrant:         []RawGrantEntry{},
	}

	// Size the record with every identity field at its longest, so the
	// record dispatch stamps later can't cross the reader's cap.
	probe := rec
	probe.DispatchID = strings.Repeat("0", dispatchIDLen)
	probe.ParentDispatchID = strings.Repeat("0", dispatchIDLen)
	probe.ParentSessionID = strings.Repeat("0", MaxSessionIDLen)
	probe.ParentDispatchSource = ParentSourceAnchor
	b, err := probe.MarshalJSON()
	if err != nil {
		return Record{}, err
	}
	if len(b) > MaxRecordBytes {
		return Record{}, &RecordTooLargeError{Size: len(b)}
	}
	return rec, nil
}

// dedupSorted returns the distinct entries in name order, never nil.
func dedupSorted(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func declaredRoles(p Policy) []string {
	out := make([]string, 0, len(p.Roles))
	for name := range p.Roles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
