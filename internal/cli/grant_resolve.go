package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// grantResolver reads what a dispatch holds now, under one policy, rather
// than the list dispatch froze into its record. `niwa authority check`
// answers with it and the never-wider check caps a child by it, so a role or
// ceiling change reaches both the same way.
//
// A dispatch holds authority.CurrentGrant of its record, capped by what its
// dispatchers hold now, each resolved the same way. The dispatchers are found
// through the lineage store, which outlives instances. A dispatcher whose
// instance directory is gone is resolved from the copy of its record dispatch
// kept at the workspace root; with no copy (a dispatch an older niwa made) it
// is passed over to its own dispatchers, since it can't act any more and
// nothing says what it held. Anything else that keeps the dispatchers from
// being established or read caps the dispatch at its own recorded grant
// instead, so a failed lookup can narrow the answer but never widen it.
type grantResolver struct {
	root   string
	policy authority.Policy
}

// grant is what rec's dispatch holds now.
func (r grantResolver) grant(rec authority.Record) []authority.GrantEntry {
	return r.resolve(rec, map[string]bool{})
}

func (r grantResolver) resolve(rec authority.Record, seen map[string]bool) []authority.GrantEntry {
	cap, ok := r.dispatchersCap(rec.DispatchID, &rec, seen)
	if !ok {
		cap = &authority.DispatcherCap{DispatchID: rec.DispatchID, Held: authority.HeldAuthorities(rec.Grant)}
	}
	return authority.CurrentGrant(r.policy, rec, cap)
}

// dispatchersCap is the most dispatch id may hold because of who dispatched
// it: what its dispatchers hold now, nil when it has none. rec is its record,
// nil when its instance is gone. ok is false when the dispatchers can't be
// established or one of them can't be read, when the walk meets a dispatch it
// has already passed on this path, or when it runs past the lineage walk's
// limit.
func (r grantResolver) dispatchersCap(id string, rec *authority.Record, seen map[string]bool) (*authority.DispatcherCap, bool) {
	if seen[id] || len(seen) >= lineage.MaxWalkLevels {
		return nil, false
	}
	// Each path up the lineage keeps its own set, so two dispatchers sharing
	// an ancestor aren't read as a cycle.
	path := make(map[string]bool, len(seen)+1)
	for k := range seen {
		path[k] = true
	}
	path[id] = true

	entry, ok := readLineage(r.root, id)
	if !ok || entry.DispatchID != id {
		return nil, false
	}
	ids, ok := dispatchersOf(r.root, entry, rec)
	if !ok {
		return nil, false
	}
	var cap *authority.DispatcherCap
	for _, d := range ids {
		held, ok := r.held(d, path)
		if !ok {
			return nil, false
		}
		cap = intersectCaps(cap, held)
	}
	return cap, true
}

// held is the most a child of dispatch id may hold now: its current grant,
// from the record in its instance or, once the instance is gone, from the
// copy at the workspace root; and what its own dispatchers allow when the
// instance is gone with no copy.
func (r grantResolver) held(id string, seen map[string]bool) (*authority.DispatcherCap, bool) {
	entry, ok := readLineage(r.root, id)
	if !ok || entry.DispatchID != id || entry.Instance == "." || entry.Instance == ".." || filepath.Base(entry.Instance) != entry.Instance {
		return nil, false
	}
	instance := filepath.Join(r.root, entry.Instance)
	var rec authority.Record
	var present bool
	_, err := os.Lstat(instance)
	switch {
	case err == nil:
		rec, present, err = workspace.ReadAuthorityRecord(instance)
	case errors.Is(err, fs.ErrNotExist):
		rec, present, err = workspace.ReadAuthorityCopy(r.root, id)
		if err == nil && !present {
			return r.dispatchersCap(id, nil, seen)
		}
	}
	if err != nil || !present || rec.DispatchID != id {
		return nil, false
	}
	return &authority.DispatcherCap{DispatchID: id, Held: authority.HeldAuthorities(r.resolve(rec, seen))}, true
}

// dispatchersOf names the dispatches that capped entry's dispatch when it
// was dispatched, the way dispatcherCap found them: the parent dispatch (the
// record's parent link, or the dispatch the calling session's mapping named
// at the workspace root), and the dispatch a handed calling session's own
// mapping names. rec is the dispatch's record, nil when its instance is
// gone; a record and an entry that disagree establish nothing.
//
// A dispatch made inside an instance with no record has no parent dispatch
// on record, but that instance's dispatch, if it had one, capped it at
// nothing; the calling session's observed or unverified link is the trace it
// leaves, and it establishes nothing either.
func dispatchersOf(root string, entry lineage.Entry, rec *authority.Record) ([]string, bool) {
	parent := entry.ParentDispatch
	if rec != nil {
		recorded := rec.ParentDispatchID
		if recorded != "" && parent.ID != recorded {
			return nil, false
		}
		if recorded == "" && parent.ID != "" && parent.Source != lineage.SourceMapped {
			return nil, false
		}
	}
	var ids []string
	switch {
	case parent.ID != "":
		if !authority.ValidDispatchID(parent.ID) {
			return nil, false
		}
		ids = append(ids, parent.ID)
	case entry.ParentSession.Source == lineage.SourceObserved, entry.ParentSession.Source == lineage.SourceUnverified:
		return nil, false
	}
	if entry.ParentSession.Source == lineage.SourceHanded && workspace.ValidSessionID(entry.ParentSession.ID) {
		m, err := workspace.ReadSessionMapping(root, entry.ParentSession.ID)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// The session's mapping is gone with its instance, or it never
			// had one; either way it caps nothing now.
		case err != nil:
			return nil, false
		case m.Origin == workspace.MappingOriginDispatch && authority.ValidDispatchID(m.DispatchID) && (len(ids) == 0 || ids[0] != m.DispatchID):
			ids = append(ids, m.DispatchID)
		}
	}
	return ids, true
}

// intersectCaps is the narrower of two caps: nil is no cap, and with both a
// child may hold only what both allow.
func intersectCaps(a, b *authority.DispatcherCap) *authority.DispatcherCap {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	inB := map[authority.Authority]bool{}
	for _, x := range b.Held {
		inB[x] = true
	}
	held := []authority.Authority{}
	for _, x := range a.Held {
		if inB[x] {
			held = append(held, x)
		}
	}
	return &authority.DispatcherCap{DispatchID: a.DispatchID, Held: held}
}
