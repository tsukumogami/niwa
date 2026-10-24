package lineage

// MaxWalkLevels is how many levels a walk prints at most, the selected entry
// counting as level 1.
const MaxWalkLevels = 64

// The reasons a walk stops. A reader that meets a value it doesn't know
// treats it as not StopRoot.
const (
	StopRoot         = "root"          // the last entry's parent dispatch id is empty
	StopNotOnRecord  = "not-on-record" // a parent dispatch id has no entry
	StopUnreadable   = "unreadable"    // an entry can't be read or fails validation
	StopLaterVersion = "later-version" // an entry is of a later version
	StopCycle        = "cycle"         // a parent dispatch id was already printed
	StopDepth        = "depth"         // level MaxWalkLevels has a parent dispatch id
)

// Lookup is one read of the store: an entry, a later-version entry's schema,
// an error, or, with every field zero, no entry at all. Raw is the entry's
// bytes as stored, which the walk carries but never reads.
type Lookup struct {
	Entry        *Entry
	Raw          []byte
	LaterVersion string
	Err          error
}

// Found reports whether the read found a file for the id.
func (l Lookup) Found() bool {
	return l.Entry != nil || l.LaterVersion != "" || l.Err != nil
}

// Level is one level of a chain: the dispatch id it was looked up by and
// what the read gave.
type Level struct {
	DispatchID string
	Lookup
}

// Chain is a walk's result: its levels, nearest first, and why it stopped.
// CycleID is the repeated id when Stop is StopCycle.
type Chain struct {
	Levels  []Level
	Stop    string
	CycleID string
}

// Walk follows parent_dispatch ids from start, whatever their source, through
// get, one level per id, and stops at the first of: an empty parent id, a
// parent with no entry, an unreadable or later-version entry, an id already
// printed, or a parent past MaxWalkLevels. start is level 1; its Raw is left
// for the caller, which read it. get is called once per ancestor and never
// for an id already visited.
func Walk(start Entry, get func(id string) Lookup) Chain {
	s := start
	c := Chain{Levels: []Level{{DispatchID: start.DispatchID, Lookup: Lookup{Entry: &s}}}}
	visited := map[string]bool{start.DispatchID: true}
	cur := start
	for {
		parent := cur.ParentDispatch.ID
		switch {
		case parent == "":
			c.Stop = StopRoot
			return c
		case len(c.Levels) >= MaxWalkLevels:
			c.Stop = StopDepth
			return c
		case visited[parent]:
			c.Stop, c.CycleID = StopCycle, parent
			return c
		}
		visited[parent] = true
		l := get(parent)
		c.Levels = append(c.Levels, Level{DispatchID: parent, Lookup: l})
		switch {
		case l.Err != nil:
			c.Stop = StopUnreadable
			return c
		case l.LaterVersion != "":
			c.Stop = StopLaterVersion
			return c
		case l.Entry == nil:
			c.Stop = StopNotOnRecord
			return c
		}
		cur = *l.Entry
	}
}
