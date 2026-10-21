package authority

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func lvl(l Level) *Level { return &l }

func TestLevelOrderAndNames(t *testing.T) {
	if !(Autonomous < Supervised && Supervised < Strict) {
		t.Fatalf("levels are not ordered widest to narrowest: %d %d %d", Autonomous, Supervised, Strict)
	}
	if want := []string{"autonomous", "supervised", "strict"}; !reflect.DeepEqual(Names(), want) {
		t.Fatalf("Names() = %v, want %v", Names(), want)
	}
	for _, l := range []Level{Autonomous, Supervised, Strict} {
		got, err := Parse(l.String())
		if err != nil || got == nil || *got != l {
			t.Fatalf("Parse(%q) = %v, %v; want %v", l.String(), got, err, l)
		}
	}
	// Names hands out a copy, so a caller can't change what Parse accepts.
	n := Names()
	n[0] = "loose"
	if _, err := Parse("loose"); err == nil {
		t.Fatal("mutating the slice Names returned changed what Parse accepts")
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    *Level
		wantErr bool
	}{
		{"autonomous", "autonomous", lvl(Autonomous), false},
		{"supervised", "supervised", lvl(Supervised), false},
		{"strict", "strict", lvl(Strict), false},
		{"surrounding whitespace is trimmed", " strict ", lvl(Strict), false},
		{"tabs and newlines are trimmed", "\tsupervised\n", lvl(Supervised), false},
		{"empty is unset", "", nil, false},
		{"whitespace only is unset", "   ", nil, false},
		{"capitalized is rejected", "Strict", nil, true},
		{"all caps is rejected", "STRICT", nil, true},
		{"unknown word is rejected", "loose", nil, true},
		{"permission mode spelling is rejected", "bypass", nil, true},
		{"inner whitespace is rejected", "str ict", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, nil; want an error", tc.in, got)
				}
				if got != nil {
					t.Fatalf("Parse(%q) returned %v alongside an error", tc.in, *got)
				}
				msg := err.Error()
				for _, part := range append([]string{`"` + strings.TrimSpace(tc.in) + `"`}, Names()...) {
					if !strings.Contains(msg, part) {
						t.Fatalf("error %q does not mention %q", msg, part)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error %v", tc.in, err)
			}
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestStringOutOfRange(t *testing.T) {
	if got := Level(7).String(); got != "Level(7)" {
		t.Errorf("Level(7).String() = %q", got)
	}
	if got := Decider(-1).String(); got != "Decider(-1)" {
		t.Errorf("Decider(-1).String() = %q", got)
	}
}

func TestCeiling(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Resolved
	}{
		{"nothing set", Inputs{}, Resolved{Autonomous, SourceDefault}},
		{"workspace only", Inputs{Workspace: lvl(Supervised)}, Resolved{Supervised, SourceWorkspace}},
		{"workspace autonomous beats the default on a tie", Inputs{Workspace: lvl(Autonomous)}, Resolved{Autonomous, SourceWorkspace}},
		{"machine only", Inputs{Machine: lvl(Strict)}, Resolved{Strict, SourceMachine}},
		{"machine autonomous beats the default on a tie", Inputs{Machine: lvl(Autonomous)}, Resolved{Autonomous, SourceMachine}},
		{"machine narrower than workspace", Inputs{Workspace: lvl(Supervised), Machine: lvl(Strict)}, Resolved{Strict, SourceMachine}},
		{"machine wider than workspace", Inputs{Workspace: lvl(Strict), Machine: lvl(Autonomous)}, Resolved{Strict, SourceWorkspace}},
		{"machine ties workspace", Inputs{Workspace: lvl(Strict), Machine: lvl(Strict)}, Resolved{Strict, SourceMachine}},
		{"flag is ignored", Inputs{Workspace: lvl(Supervised), Flag: lvl(Strict)}, Resolved{Supervised, SourceWorkspace}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Ceiling(tc.in); got != tc.want {
				t.Fatalf("Ceiling = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Resolved
	}{
		{"nothing set", Inputs{}, Resolved{Autonomous, SourceDefault}},
		{"workspace strict", Inputs{Workspace: lvl(Strict)}, Resolved{Strict, SourceWorkspace}},
		{"flag narrows the workspace", Inputs{Workspace: lvl(Autonomous), Flag: lvl(Supervised)}, Resolved{Supervised, SourceFlag}},
		{"no flag keeps the workspace", Inputs{Workspace: lvl(Autonomous)}, Resolved{Autonomous, SourceWorkspace}},
		{"flag ties the workspace", Inputs{Workspace: lvl(Supervised), Flag: lvl(Supervised)}, Resolved{Supervised, SourceFlag}},
		{"flag narrows the default", Inputs{Flag: lvl(Strict)}, Resolved{Strict, SourceFlag}},
		{"flag ties the default", Inputs{Flag: lvl(Autonomous)}, Resolved{Autonomous, SourceFlag}},
		{"machine narrows the workspace", Inputs{Workspace: lvl(Autonomous), Machine: lvl(Strict)}, Resolved{Strict, SourceMachine}},
		{"machine wider than the workspace", Inputs{Workspace: lvl(Strict), Machine: lvl(Autonomous)}, Resolved{Strict, SourceWorkspace}},
		{"machine ties the workspace", Inputs{Workspace: lvl(Strict), Machine: lvl(Strict)}, Resolved{Strict, SourceMachine}},
		{"flag ties the machine", Inputs{Machine: lvl(Supervised), Flag: lvl(Supervised)}, Resolved{Supervised, SourceFlag}},
		{"flag narrower than everything", Inputs{Workspace: lvl(Autonomous), Machine: lvl(Supervised), Flag: lvl(Strict)}, Resolved{Strict, SourceFlag}},
		{"all three tie", Inputs{Workspace: lvl(Strict), Machine: lvl(Strict), Flag: lvl(Strict)}, Resolved{Strict, SourceFlag}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.in)
			if err != nil {
				t.Fatalf("Resolve: unexpected error %v", err)
			}
			if got != tc.want {
				t.Fatalf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveRefusesAWiderFlag(t *testing.T) {
	cases := []struct {
		name        string
		in          Inputs
		wantCeiling Resolved
	}{
		{"wider than the workspace", Inputs{Workspace: lvl(Supervised), Flag: lvl(Autonomous)}, Resolved{Supervised, SourceWorkspace}},
		{"wider than the machine", Inputs{Workspace: lvl(Autonomous), Machine: lvl(Strict), Flag: lvl(Supervised)}, Resolved{Strict, SourceMachine}},
		{"wider than a machine that ties the workspace", Inputs{Workspace: lvl(Strict), Machine: lvl(Strict), Flag: lvl(Autonomous)}, Resolved{Strict, SourceMachine}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.in)
			if err == nil {
				t.Fatalf("Resolve = %+v, nil; want a refusal", got)
			}
			if got != (Resolved{}) {
				t.Errorf("Resolve returned %+v alongside its refusal", got)
			}
			var we *WiderError
			if !errors.As(err, &we) {
				t.Fatalf("error %T is not a *WiderError", err)
			}
			if we.Requested != *tc.in.Flag || we.Ceiling != tc.wantCeiling {
				t.Fatalf("WiderError = %+v, want requested %v ceiling %+v", we, *tc.in.Flag, tc.wantCeiling)
			}
			msg := err.Error()
			for _, part := range []string{tc.in.Flag.String(), tc.wantCeiling.Level.String(), string(tc.wantCeiling.Source)} {
				if !strings.Contains(msg, part) {
					t.Errorf("refusal %q does not name %q", msg, part)
				}
			}
		})
	}
}

func TestWiderErrorText(t *testing.T) {
	err := &WiderError{Requested: Autonomous, Ceiling: Resolved{Supervised, SourceWorkspace}}
	want := "authority level autonomous is wider than the ceiling supervised from workspace; a dispatch may only narrow the level"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestDeciderOrderAndNames(t *testing.T) {
	if !(DeciderSession < DeciderCoordinator && DeciderCoordinator < DeciderPerson) {
		t.Fatal("deciders are not ordered widest to narrowest")
	}
	for d, want := range map[Decider]string{DeciderSession: "session", DeciderCoordinator: "coordinator", DeciderPerson: "person"} {
		if d.String() != want {
			t.Errorf("%d.String() = %q, want %q", int(d), d.String(), want)
		}
	}
}

func TestValidPoint(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"single letter", "a", true},
		{"letters digits and hyphen", "merge-2", true},
		{"plain word", "merge", true},
		{"64 characters", "a" + strings.Repeat("b", 63), true},
		{"65 characters", "a" + strings.Repeat("b", 64), false},
		{"uppercase and underscore", "Merge_Now", false},
		{"leading digit", "2merge", false},
		{"leading hyphen", "-merge", false},
		{"empty", "", false},
		{"uppercase only", "MERGE", false},
		{"path separator", "a/b", false},
		{"dot", "a.b", false},
		{"trailing newline", "merge\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidPoint(tc.in); got != tc.want {
				t.Fatalf("ValidPoint(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// fakeDeclarations answers from a map, standing in for the organization
// declaration reader that does not exist yet.
type fakeDeclarations map[string]Declared

func (f fakeDeclarations) Lookup(point string) (Declared, bool) {
	d, ok := f[point]
	return d, ok
}

func TestDecideWithoutDeclarations(t *testing.T) {
	cases := []struct {
		r         Resolved
		decider   Decider
		exclusion bool
	}{
		{Resolved{Autonomous, SourceWorkspace}, DeciderSession, false},
		{Resolved{Supervised, SourceFlag}, DeciderCoordinator, true},
		{Resolved{Strict, SourceMachine}, DeciderPerson, true},
		{Resolved{Autonomous, SourceDefault}, DeciderSession, false},
	}
	for _, tc := range cases {
		for _, decls := range []Declarations{NoDeclarations, nil} {
			got := Decide("merge", tc.r, decls)
			want := Answer{Point: "merge", Level: tc.r.Level, Decider: tc.decider, AuthorExclusion: tc.exclusion, Source: tc.r.Source}
			if got != want {
				t.Errorf("Decide(merge, %+v, %v) = %+v, want %+v", tc.r, decls, got, want)
			}
		}
	}
}

// TestDecideDeclarationSeam covers the declaration cases from the PRD's
// acceptance criteria, plus the narrowing of each component on its own.
func TestDecideDeclarationSeam(t *testing.T) {
	coordinatorOff := fakeDeclarations{"merge": {Decider: DeciderCoordinator, AuthorExclusion: false}}
	personOn := fakeDeclarations{"merge": {Decider: DeciderPerson, AuthorExclusion: true}}
	sessionOn := fakeDeclarations{"merge": {Decider: DeciderSession, AuthorExclusion: true}}
	sessionOff := fakeDeclarations{"merge": {Decider: DeciderSession, AuthorExclusion: false}}

	cases := []struct {
		name      string
		point     string
		r         Resolved
		decls     Declarations
		decider   Decider
		exclusion bool
		source    Source
	}{
		{"declared point at autonomous keeps the declaration", "merge", Resolved{Autonomous, SourceWorkspace}, coordinatorOff, DeciderCoordinator, false, SourceDeclaration},
		{"undeclared point at autonomous follows the level", "accept", Resolved{Autonomous, SourceWorkspace}, coordinatorOff, DeciderSession, false, SourceWorkspace},
		{"strict level narrows both components and reports its source", "merge", Resolved{Strict, SourceFlag}, coordinatorOff, DeciderPerson, true, SourceFlag},
		{"a stricter declaration stays as declared", "merge", Resolved{Autonomous, SourceDefault}, personOn, DeciderPerson, true, SourceDeclaration},
		{"supervised narrows only the exclusion", "merge", Resolved{Supervised, SourceMachine}, coordinatorOff, DeciderCoordinator, true, SourceMachine},
		{"supervised narrows only the decider", "merge", Resolved{Supervised, SourceWorkspace}, sessionOn, DeciderCoordinator, true, SourceWorkspace},
		{"a declaration equal to the level reports the declaration", "merge", Resolved{Supervised, SourceWorkspace}, fakeDeclarations{"merge": {DeciderCoordinator, true}}, DeciderCoordinator, true, SourceDeclaration},
		{"a stricter declaration at strict is not narrowed", "merge", Resolved{Strict, SourceFlag}, personOn, DeciderPerson, true, SourceDeclaration},
		{"a wide declaration at autonomous is kept", "merge", Resolved{Autonomous, SourceWorkspace}, sessionOff, DeciderSession, false, SourceDeclaration},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.point, tc.r, tc.decls)
			want := Answer{Point: tc.point, Level: tc.r.Level, Decider: tc.decider, AuthorExclusion: tc.exclusion, Source: tc.source}
			if got != want {
				t.Fatalf("Decide = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDecideNeverSetsNoRecord(t *testing.T) {
	if Decide("merge", Resolved{Strict, SourceWorkspace}, NoDeclarations).NoRecord {
		t.Fatal("Decide set NoRecord; only the caller that fell back may set it")
	}
}
