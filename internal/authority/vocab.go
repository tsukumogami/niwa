package authority

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Level is an authority level. Levels are ordered widest first, so a larger
// value is narrower: Autonomous < Supervised < Strict.
type Level int

const (
	Autonomous Level = iota
	Supervised
	Strict
)

var levelNames = []string{"autonomous", "supervised", "strict"}

// String returns the level's name, or "unknown" for a value outside the three.
func (l Level) String() string {
	if l < Autonomous || l > Strict {
		return "unknown"
	}
	return levelNames[l]
}

// LevelNames returns the accepted level names, widest first.
func LevelNames() []string {
	return append([]string(nil), levelNames...)
}

// levelByName matches a stored level exactly, with no trimming: a record or a
// config value is compared as written.
func levelByName(s string) (Level, bool) {
	for i, n := range levelNames {
		if s == n {
			return Level(i), true
		}
	}
	return 0, false
}

// ParseLevel trims raw and matches it exactly (lowercase) against the three
// level names. A blank value means not set and returns nil with no error.
func ParseLevel(raw string) (*Level, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	if l, ok := levelByName(v); ok {
		return &l, nil
	}
	return nil, &UnknownValueError{Key: "level", Value: v, Accepted: LevelNames()}
}

// Authority is a named authority a grant may hold.
type Authority string

const (
	MergePR    Authority = "merge-pr"
	CloseIssue Authority = "close-issue"
	CutRelease Authority = "cut-release"
)

var knownAuthorities = []Authority{MergePR, CloseIssue, CutRelease}

// Authorities returns the authorities this niwa knows, in R3 order: the order
// every list of authorities niwa prints or stores uses.
func Authorities() []Authority {
	return append([]Authority(nil), knownAuthorities...)
}

// AuthorityNames returns Authorities as strings, for accepted-value messages.
func AuthorityNames() []string {
	out := make([]string, len(knownAuthorities))
	for i, a := range knownAuthorities {
		out[i] = string(a)
	}
	return out
}

// rank is the authority's position in R3 order, or -1 when this niwa doesn't
// know it.
func (a Authority) rank() int {
	for i, k := range knownAuthorities {
		if a == k {
			return i
		}
	}
	return -1
}

// Known reports whether this niwa knows the authority.
func (a Authority) Known() bool { return a.rank() >= 0 }

// sortAuthorities orders known authorities in R3 order.
func sortAuthorities(list []Authority) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].rank() < list[j].rank() })
}

// namePattern is the shape of every role, authority and decision-point name,
// and of each half of a grant source.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// NamePatternText is namePattern as printed in accepted-value messages.
const NamePatternText = "^[a-z][a-z0-9-]{0,63}$"

// ValidName reports whether s matches the role and authority name pattern.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// ParseAuthority trims raw and matches it exactly against the authorities
// this niwa knows. An unknown name comes back as an *UnknownValueError whose
// WellFormed field says whether it matches the name pattern, which is how a
// config reader tells an authority a newer niwa might know (dropped with a
// warning) from a typo (an invalid value).
func ParseAuthority(raw string) (Authority, error) {
	v := strings.TrimSpace(raw)
	a := Authority(v)
	if a.Known() {
		return a, nil
	}
	return "", &UnknownValueError{Key: "authority", Value: v, Accepted: AuthorityNames(), WellFormed: ValidName(v)}
}

// UnknownValueError is a value outside its vocabulary: an unknown level, an
// unknown authority, or an undeclared role.
type UnknownValueError struct {
	Key      string   // what was being parsed: "level", "authority", "--grant", "--role", ...
	Value    string   // the value as given (trimmed)
	Accepted []string // the accepted values
	// WellFormed is true when Value matches the name pattern, so it could be
	// an authority a newer niwa knows.
	WellFormed bool
	// IfNone, when set, replaces the accepted-values clause when Accepted is
	// empty, so a vocabulary nobody declared isn't printed as one whose only
	// value is "none".
	IfNone string
}

func (e *UnknownValueError) Error() string {
	if len(e.Accepted) == 0 && e.IfNone != "" {
		return fmt.Sprintf("invalid %s %s; %s", e.Key, quote(e.Value), e.IfNone)
	}
	accepted := "none"
	if len(e.Accepted) > 0 {
		accepted = strings.Join(e.Accepted, ", ")
	}
	return fmt.Sprintf("invalid %s %s; accepted values are %s", e.Key, quote(e.Value), accepted)
}

// quote renders a value for a message: quoted, with control bytes and other
// non-printable characters escaped, so nothing a config or a flag holds can
// reach a terminal raw.
func quote(s string) string { return strconv.Quote(s) }

// EmptyEntryError is a comma list with an empty entry, such as "merge-pr,".
type EmptyEntryError struct {
	Raw string // the argument that held the empty entry
}

func (e *EmptyEntryError) Error() string {
	return fmt.Sprintf("empty entry in %s", quote(e.Raw))
}

// ParseNameList is the one comma-list parser for --grant, --role and
// `niwa config set authority-ceiling`. Each argument is split on commas and
// every entry trimmed, so ["merge-pr,close-issue"] and ["merge-pr",
// "close-issue"] give the same list. An empty entry anywhere, including a
// blank argument, is refused with an *EmptyEntryError. It checks no
// vocabulary and gives "none" no meaning: only `config set` reads "none" as
// the empty list, and it does so before calling here.
func ParseNameList(raw []string) ([]string, error) {
	var out []string
	for _, arg := range raw {
		for _, part := range strings.Split(arg, ",") {
			entry := strings.TrimSpace(part)
			if entry == "" {
				return nil, &EmptyEntryError{Raw: arg}
			}
			out = append(out, entry)
		}
	}
	return out, nil
}

// Layer names where a level or ceiling came from.
type Layer string

const (
	LayerFlag      Layer = "flag"
	LayerMachine   Layer = "machine"
	LayerOverlay   Layer = "overlay"
	LayerWorkspace Layer = "workspace"
	LayerBuiltIn   Layer = "built-in"
	// LayerNone is the grant ceiling's source when no layer declares a
	// ceiling at all: the workspace never opted in to grants.
	LayerNone Layer = "none"
	// LayerNewerVersion is reader-only: every source of a record stored in a
	// later schema version. Nothing ever writes it.
	LayerNewerVersion Layer = "newer-version"
)
