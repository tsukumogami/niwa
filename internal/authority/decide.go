package authority

// Decider is who settles a decision point, ordered widest first:
// Session < Dispatcher < Person.
type Decider int

const (
	Session Decider = iota
	Dispatcher
	Person
)

var deciderNames = []string{"session", "dispatcher", "person"}

func (d Decider) String() string {
	if d < Session || d > Person {
		return "unknown"
	}
	return deciderNames[d]
}

// Declared is a per-point declaration: a decider and an author-exclusion
// value.
type Declared struct {
	Decider         Decider
	AuthorExclusion bool
}

// Declarations looks up a per-point declaration. No declaration source exists
// in any workspace yet, so every caller passes NoDeclarations; the seam is
// here so a later source can be added without changing Decide.
type Declarations interface {
	Lookup(point string) (Declared, bool)
}

type noDeclarations struct{}

func (noDeclarations) Lookup(string) (Declared, bool) { return Declared{}, false }

// NoDeclarations declares nothing, so every point resolves from its level.
var NoDeclarations Declarations = noDeclarations{}

// ValidPoint reports whether name is a valid decision-point name.
func ValidPoint(name string) bool { return ValidName(name) }

// Answer is the decision-point answer `niwa authority decide` prints.
type Answer struct {
	Point           string
	Level           Level
	Decider         Decider
	AuthorExclusion bool
	// Source is a Layer, or "declaration" when a declaration the level didn't
	// narrow supplied the answer, with " (no dispatch record)" appended when
	// the level came from no record.
	Source string
}

// SourceDeclaration is an answer's source when a declaration supplied it.
const SourceDeclaration = "declaration"

// NoRecordSuffix marks an answer computed with no dispatch record.
const NoRecordSuffix = " (no dispatch record)"

// fromLevel is R2's table: what a level alone means at any point.
func fromLevel(l Level) Declared {
	switch l {
	case Autonomous:
		return Declared{Decider: Session, AuthorExclusion: false}
	case Supervised:
		return Declared{Decider: Dispatcher, AuthorExclusion: true}
	default:
		return Declared{Decider: Person, AuthorExclusion: true}
	}
}

// Decide answers who settles point at level. With no declaration the answer
// comes from the level alone. With one, the level narrows each component to
// the stricter of the two and nothing widens the declared answer; the source
// is "declaration" when the level narrowed neither component and the level's
// source otherwise. noRecord appends NoRecordSuffix to the source. The answer
// never reads the grant.
func Decide(point string, level Level, levelSource Layer, noRecord bool, d Declarations) Answer {
	base := fromLevel(level)
	a := Answer{Point: point, Level: level, Decider: base.Decider, AuthorExclusion: base.AuthorExclusion, Source: string(levelSource)}
	if d == nil {
		d = NoDeclarations
	}
	if decl, ok := d.Lookup(point); ok {
		a.Decider = decl.Decider
		if base.Decider > a.Decider {
			a.Decider = base.Decider
		}
		a.AuthorExclusion = decl.AuthorExclusion || base.AuthorExclusion
		if a.Decider == decl.Decider && a.AuthorExclusion == decl.AuthorExclusion {
			a.Source = SourceDeclaration
		}
	}
	if noRecord {
		a.Source += NoRecordSuffix
	}
	return a
}
