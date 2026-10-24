package lineage

import (
	"errors"
	"fmt"
	"testing"
)

func walkID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

// walkEntry is entry n, whose parent is entry parent (0 for none).
func walkEntry(n, parent int) Entry {
	e := Entry{Schema: SchemaEntryV1, DispatchID: walkID(n), ParentDispatch: Link{Source: SourceNone}}
	if parent != 0 {
		e.ParentDispatch = Link{ID: walkID(parent), Source: SourceMapped}
	}
	return e
}

// store is a get over a map of lookups, counting the reads of each id.
type store struct {
	lookups map[string]Lookup
	reads   map[string]int
}

func (s *store) get(id string) Lookup {
	s.reads[id]++
	return s.lookups[id]
}

func chainOf(n int) *store {
	s := &store{lookups: map[string]Lookup{}, reads: map[string]int{}}
	for i := 2; i <= n; i++ {
		e := walkEntry(i, i+1)
		if i == n {
			e = walkEntry(i, 0)
		}
		s.lookups[walkID(i)] = Lookup{Entry: &e}
	}
	return s
}

func TestWalk_Stops(t *testing.T) {
	later := &store{lookups: map[string]Lookup{walkID(2): {LaterVersion: "niwa.lineage.entry/v2"}}, reads: map[string]int{}}
	bad := &store{lookups: map[string]Lookup{walkID(2): {Err: errors.New("lineage entry is malformed")}}, reads: map[string]int{}}
	missing := &store{lookups: map[string]Lookup{}, reads: map[string]int{}}
	cycle := &store{lookups: map[string]Lookup{}, reads: map[string]int{}}
	back := walkEntry(2, 1)
	cycle.lookups[walkID(2)] = Lookup{Entry: &back}

	cases := []struct {
		name   string
		start  Entry
		s      *store
		levels int
		stop   string
		cycle  string
	}{
		{"root", walkEntry(1, 0), missing, 1, StopRoot, ""},
		{"not on record", walkEntry(1, 2), missing, 2, StopNotOnRecord, ""},
		{"unreadable", walkEntry(1, 2), bad, 2, StopUnreadable, ""},
		{"later version", walkEntry(1, 2), later, 2, StopLaterVersion, ""},
		{"cycle", walkEntry(1, 2), cycle, 2, StopCycle, walkID(1)},
		{"self parent", walkEntry(1, 1), missing, 1, StopCycle, walkID(1)},
		{"exactly 64", walkEntry(1, 2), chainOf(64), 64, StopRoot, ""},
		{"70 deep", walkEntry(1, 2), chainOf(70), 64, StopDepth, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Walk(tc.start, tc.s.get)
			if len(c.Levels) != tc.levels || c.Stop != tc.stop || c.CycleID != tc.cycle {
				t.Fatalf("levels %d stop %q cycle %q; want %d %q %q", len(c.Levels), c.Stop, c.CycleID, tc.levels, tc.stop, tc.cycle)
			}
			for i, l := range c.Levels {
				if l.DispatchID != walkID(i+1) {
					t.Errorf("level %d is %s", i+1, l.DispatchID)
				}
			}
			for id, n := range tc.s.reads {
				if n != 1 || id == walkID(1) {
					t.Errorf("%s read %d times", id, n)
				}
			}
		})
	}
}

func TestWalk_LevelsCarryTheirLookups(t *testing.T) {
	s := &store{lookups: map[string]Lookup{}, reads: map[string]int{}}
	parent := walkEntry(2, 0)
	s.lookups[walkID(2)] = Lookup{Entry: &parent, Raw: []byte(`{"stored":true}`)}
	c := Walk(walkEntry(1, 2), s.get)
	if c.Levels[0].Entry == nil || c.Levels[0].Raw != nil {
		t.Errorf("level 1 = %+v", c.Levels[0])
	}
	if string(c.Levels[1].Raw) != `{"stored":true}` || c.Levels[1].Entry.DispatchID != walkID(2) {
		t.Errorf("level 2 = %+v", c.Levels[1])
	}
	if !c.Levels[1].Found() || (Lookup{}).Found() {
		t.Error("Found")
	}
}

func TestIsObserved(t *testing.T) {
	for v, want := range map[string]bool{"observed": true, "mapped": false, "handed": false, "unverified": false, "none": false, "vouched": false, "": false, "Observed": false} {
		if IsObserved(v) != want {
			t.Errorf("IsObserved(%q) = %v", v, !want)
		}
	}
}
