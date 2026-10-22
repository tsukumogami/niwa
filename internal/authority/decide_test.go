package authority

import (
	"strings"
	"testing"
)

type fakeDeclarations map[string]Declared

func (f fakeDeclarations) Lookup(point string) (Declared, bool) {
	d, ok := f[point]
	return d, ok
}

func TestDecide_NoDeclaration(t *testing.T) {
	cases := []struct {
		level     Level
		decider   Decider
		exclusion bool
		source    Layer
	}{
		{Autonomous, Session, false, LayerBuiltIn},
		{Supervised, Dispatcher, true, LayerWorkspace},
		{Strict, Person, true, LayerFlag},
	}
	for _, c := range cases {
		a := Decide("merge-own-pr", c.level, c.source, false, NoDeclarations)
		if a.Decider != c.decider || a.AuthorExclusion != c.exclusion || a.Source != string(c.source) || a.Level != c.level || a.Point != "merge-own-pr" {
			t.Errorf("%s: %+v", c.level, a)
		}
		a = Decide("merge-own-pr", c.level, c.source, true, nil)
		if a.Source != string(c.source)+" (no dispatch record)" {
			t.Errorf("%s with no record: source %q", c.level, a.Source)
		}
	}
}

func TestDecide_DeclarationSeam(t *testing.T) {
	dispatcherOff := fakeDeclarations{"merge-own-pr": {Decider: Dispatcher, AuthorExclusion: false}}
	personOn := fakeDeclarations{"merge-own-pr": {Decider: Person, AuthorExclusion: true}}
	cases := []struct {
		name      string
		point     string
		level     Level
		source    Layer
		decl      Declarations
		decider   Decider
		exclusion bool
		want      string
	}{
		{"declaration at autonomous", "merge-own-pr", Autonomous, LayerWorkspace, dispatcherOff, Dispatcher, false, "declaration"},
		{"strict narrows both", "merge-own-pr", Strict, LayerFlag, dispatcherOff, Person, true, "flag"},
		{"supervised narrows exclusion", "merge-own-pr", Supervised, LayerWorkspace, dispatcherOff, Dispatcher, true, "workspace"},
		{"person on at autonomous", "merge-own-pr", Autonomous, LayerWorkspace, personOn, Person, true, "declaration"},
		{"other point untouched", "accept-prd", Autonomous, LayerWorkspace, dispatcherOff, Session, false, "workspace"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := Decide(c.point, c.level, c.source, false, c.decl)
			if a.Decider != c.decider || a.AuthorExclusion != c.exclusion || a.Source != c.want {
				t.Errorf("got %s/%v/%s, want %s/%v/%s", a.Decider, a.AuthorExclusion, a.Source, c.decider, c.exclusion, c.want)
			}
		})
	}
}

func TestValidPoint(t *testing.T) {
	for _, ok := range []string{"a", "merge-2", strings.Repeat("a", 64)} {
		if !ValidPoint(ok) {
			t.Errorf("ValidPoint(%q) = false", ok)
		}
	}
	for _, bad := range []string{"Merge_Now", "2merge", "-merge", "", strings.Repeat("a", 65)} {
		if ValidPoint(bad) {
			t.Errorf("ValidPoint(%q) = true", bad)
		}
	}
}
