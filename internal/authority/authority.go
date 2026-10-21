// Package authority owns the authority level: how much a dispatched session
// may decide on its own. It holds the level's order, its one parser, the
// narrowest-wins resolution across the workspace, machine and per-dispatch
// settings, and the per-decision-point answer computed through a seam where
// an organization's declaration can be read later.
//
// It does no I/O and imports nothing from niwa. Config validation, dispatch,
// `niwa authority show` and `niwa authority decide` all call into it, so
// "narrower" has exactly one definition that none of them can drift from.
package authority

import (
	"fmt"
	"regexp"
	"strings"
)

// Level is an authority level. The constants are ordered from widest to
// narrowest, so a larger value is always the narrower one and resolution can
// compare levels directly.
type Level int

const (
	// Autonomous lets the session settle a decision point itself. It is the
	// built-in default because it is what every dispatched session already
	// does, and a workspace that declares nothing must never be told to ask.
	Autonomous Level = iota
	// Supervised hands a decision point to the session that dispatched it.
	Supervised
	// Strict hands a decision point to a person.
	Strict
)

// levelNames is indexed by Level, which keeps String and Parse from
// disagreeing about a spelling.
var levelNames = [...]string{"autonomous", "supervised", "strict"}

// Names returns the accepted level names from widest to narrowest, in the
// order error messages and help text list them. It returns a fresh slice so
// no caller can change what Parse accepts.
func Names() []string {
	out := make([]string, len(levelNames))
	copy(out, levelNames[:])
	return out
}

// String returns the level's configuration spelling.
func (l Level) String() string {
	if l < Autonomous || l > Strict {
		return fmt.Sprintf("Level(%d)", int(l))
	}
	return levelNames[l]
}

// Parse is the one validation boundary for every authority-level setting. It
// trims surrounding whitespace and returns nil for an empty or
// whitespace-only value, which means the setting is not set. Otherwise the
// trimmed value has to match a level name exactly, case included: a value
// like "Strict" is rejected rather than folded, because a configuration that
// is accepted only by accident is one nobody can reason about later.
//
// The error names the offending value and the accepted names but not the
// setting, since only the caller knows which key or flag the value came from.
func Parse(raw string) (*Level, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	for i, name := range levelNames {
		if v == name {
			l := Level(i)
			return &l, nil
		}
	}
	return nil, fmt.Errorf("invalid authority level %q; accepted values are %s",
		v, strings.Join(levelNames[:], ", "))
}

// Source names the setting a resolved level or a decision-point answer came
// from.
type Source string

const (
	SourceFlag        Source = "flag"
	SourceMachine     Source = "machine"
	SourceWorkspace   Source = "workspace"
	SourceDefault     Source = "default"
	SourceDeclaration Source = "declaration"
)

// Inputs are the three settings a dispatch's level resolves from. A nil field
// is a setting that was not given, or was given blank.
type Inputs struct {
	Workspace *Level
	Machine   *Level
	Flag      *Level
}

// Resolved is a level together with the setting that supplied it.
type Resolved struct {
	Level  Level
	Source Source
}

// Ceiling is the widest level a dispatch may run at: the narrower of the
// workspace level (or the Autonomous default when the workspace sets none)
// and the machine level when it is set. It ignores the flag, so it is also
// what a dispatch with no flag resolves to, which is what `niwa authority
// show` prints.
//
// On a tie the machine is named over the workspace, and the workspace over
// the default. That is the order of specificity R8 fixes, and it keeps the
// reported source stable when an operator sets the same level in two places.
func Ceiling(in Inputs) Resolved {
	c := Resolved{Level: Autonomous, Source: SourceDefault}
	if in.Workspace != nil {
		c = Resolved{Level: *in.Workspace, Source: SourceWorkspace}
	}
	if in.Machine != nil && *in.Machine >= c.Level {
		c = Resolved{Level: *in.Machine, Source: SourceMachine}
	}
	return c
}

// WiderError refuses a flag that asks for more authority than the ceiling
// allows. It is refused rather than clamped, so an operator never believes a
// dispatch got room it did not get. It carries the ceiling and its source so
// the message can say which setting to change.
type WiderError struct {
	Requested Level
	Ceiling   Resolved
}

func (e *WiderError) Error() string {
	return fmt.Sprintf("authority level %s is wider than the ceiling %s from %s; a dispatch may only narrow the level",
		e.Requested, e.Ceiling.Level, e.Ceiling.Source)
}

// Resolve returns the level a dispatch runs at: the narrowest of the
// workspace level (or the default), the machine level and the flag. A flag
// wider than the Ceiling is refused with a *WiderError. A flag equal to the
// ceiling wins the tie, so the flag is named first, then the machine, the
// workspace and the default.
func Resolve(in Inputs) (Resolved, error) {
	c := Ceiling(in)
	if in.Flag == nil {
		return c, nil
	}
	if *in.Flag < c.Level {
		return Resolved{}, &WiderError{Requested: *in.Flag, Ceiling: c}
	}
	return Resolved{Level: *in.Flag, Source: SourceFlag}, nil
}

// Decider is who settles a decision point. The constants are ordered from
// widest to narrowest, matching Level, so a larger value is the stricter
// decider.
type Decider int

const (
	// DeciderSession is the session that reached the point.
	DeciderSession Decider = iota
	// DeciderCoordinator is the session that dispatched it.
	DeciderCoordinator
	// DeciderPerson is a person.
	DeciderPerson
)

var deciderNames = [...]string{"session", "coordinator", "person"}

// String returns the decider's printed spelling.
func (d Decider) String() string {
	if d < DeciderSession || d > DeciderPerson {
		return fmt.Sprintf("Decider(%d)", int(d))
	}
	return deciderNames[d]
}

// Declared is an organization's declaration for one decision point: who
// decides it and whether the author of the work is excluded from deciding.
type Declared struct {
	Decider         Decider
	AuthorExclusion bool
}

// Declarations looks up the declaration for a decision point. It is the seam
// a later reader of the organization's declaration plugs into; Decide takes
// it as an argument so that reader changes the one line that picks the
// implementation and no caller.
type Declarations interface {
	Lookup(point string) (Declared, bool)
}

type noDeclarations struct{}

func (noDeclarations) Lookup(string) (Declared, bool) { return Declared{}, false }

// NoDeclarations finds no declaration for any point. No declaration source
// exists in any workspace yet, so every caller passes it and every point
// resolves from the level alone.
var NoDeclarations Declarations = noDeclarations{}

// pointPattern is the decision-point name rule. Names are lowercase so two
// spellings can never name the same point, and short enough to print on one
// line.
var pointPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidPoint reports whether name is a well-formed decision-point name. Any
// well-formed name is accepted; until a declaration names its points, every
// name resolves from the level.
func ValidPoint(name string) bool {
	return pointPattern.MatchString(name)
}

// Answer is who decides one decision point at one resolved level.
type Answer struct {
	Point           string
	Level           Level
	Decider         Decider
	AuthorExclusion bool
	Source          Source
	// NoRecord marks an answer computed from the configuration because no
	// dispatch record existed for the caller. Decide never sets it; the
	// caller that fell back does, and it prints as a suffix on the source.
	NoRecord bool
}

// levelDefaults is what each level means at a decision point with no
// declaration: autonomous lets the session settle it with the author free to
// decide, supervised hands it to the coordinator and strict to a person, both
// with the author excluded.
func levelDefaults(l Level) Declared {
	switch l {
	case Autonomous:
		return Declared{Decider: DeciderSession, AuthorExclusion: false}
	case Supervised:
		return Declared{Decider: DeciderCoordinator, AuthorExclusion: true}
	default:
		// Strict, and any out-of-range value: an unknown level must never
		// answer wider than the narrowest one known.
		return Declared{Decider: DeciderPerson, AuthorExclusion: true}
	}
}

// Decide answers who decides point at the resolved level r. With no
// declaration for the point the answer is the level's own meaning, sourced
// from r.
//
// A declared point is never answered wider than the level allows: each
// component, decider and author exclusion, becomes the stricter of the
// declared value and the level's own. The answer reports the declaration as
// its source only when neither component was narrowed; when the level
// changed either one, the level is what decided the answer, so its source is
// reported instead.
//
// Decide does not check the point name; callers validate it with ValidPoint
// first, because a malformed name is a usage error, not an answer. A nil
// decls is treated as NoDeclarations.
func Decide(point string, r Resolved, decls Declarations) Answer {
	base := levelDefaults(r.Level)
	ans := Answer{
		Point:           point,
		Level:           r.Level,
		Decider:         base.Decider,
		AuthorExclusion: base.AuthorExclusion,
		Source:          r.Source,
	}
	if decls == nil {
		return ans
	}
	declared, ok := decls.Lookup(point)
	if !ok {
		return ans
	}

	decider := declared.Decider
	if base.Decider > decider {
		decider = base.Decider
	}
	exclusion := declared.AuthorExclusion || base.AuthorExclusion

	ans.Decider = decider
	ans.AuthorExclusion = exclusion
	if decider == declared.Decider && exclusion == declared.AuthorExclusion {
		ans.Source = SourceDeclaration
	}
	return ans
}
