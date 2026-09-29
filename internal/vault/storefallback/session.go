// Package storefallback serves values from the store of last-resolved
// values when a vault provider can't be asked, and keeps that store up to
// date when it can.
//
// A Session belongs to one provisioning run. Its Wrap decorates the
// providers of the bundles provisioning resolves through; nothing else is
// wrapped, so credential sync, status checks and onboarding never read or
// write the store. The decorator records every successful resolution,
// evicts keys the provider says are gone, and, for an unauthenticated or
// unreachable failure only, returns the stored value instead. Changes are
// buffered in the session and written by Flush, at most one store update
// per identity, when the run ends. What was served, missed or couldn't be
// written goes to a fallbacknotice.Collector.
package storefallback

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// Session buffers one run's store reads and changes. It is safe for
// concurrent use by the providers it wraps.
type Session struct {
	dir     string
	notices *fallbacknotice.Collector
	now     func() time.Time

	mu sync.Mutex
	// loaded caches store.Load per identity, so the store is read at
	// most once per identity per run.
	loaded  map[vault.Identity]map[string]store.Entry
	changes map[vault.Identity]*change
	// disabled is set once the store has proved unusable in this run.
	// After that nothing more is read from it or written to it.
	disabled bool
}

// change is the buffered store update for one identity. Later events on
// a key replace earlier ones, so what Flush writes is the run's last word
// on each key.
type change struct {
	puts      map[string]store.Entry
	evictKeys map[string]bool
	evictAll  bool
}

// NewSession returns a session over the store directory dir, reporting to
// notices (which may be nil). dir is empty when store.Dir failed; the
// session then serves nothing, writes nothing and notes the store as
// unwritable the first time it needs it. now stamps resolution times; nil
// means time.Now.
func NewSession(dir string, notices *fallbacknotice.Collector, now func() time.Time) *Session {
	if now == nil {
		now = time.Now
	}
	return &Session{
		dir:     dir,
		notices: notices,
		now:     now,
		loaded:  map[vault.Identity]map[string]store.Entry{},
		changes: map[vault.Identity]*change{},
	}
}

// changeFor returns the buffered change for id, creating it. Callers hold
// mu.
func (s *Session) changeFor(id vault.Identity) *change {
	c, ok := s.changes[id]
	if !ok {
		c = &change{puts: map[string]store.Entry{}, evictKeys: map[string]bool{}}
		s.changes[id] = c
	}
	return c
}

// put buffers a resolved value.
func (s *Session) put(id vault.Identity, key string, e store.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.changeFor(id)
	delete(c.evictKeys, key)
	c.puts[key] = e
}

// evictKey buffers the removal of one key.
func (s *Session) evictKey(id vault.Identity, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.changeFor(id)
	delete(c.puts, key)
	c.evictKeys[key] = true
}

// evictAll buffers the removal of every key stored for id. Values
// buffered before it are dropped; the store applies puts after the
// eviction, so any buffered later still land.
func (s *Session) evictAll(id vault.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.changeFor(id)
	c.puts = map[string]store.Entry{}
	c.evictKeys = map[string]bool{}
	c.evictAll = true
}

// lookup returns the stored entry for key under id. The identity's
// entries are loaded on first use and cached for the run. A store that
// can't be read counts as holding nothing.
func (s *Session) lookup(id vault.Identity, key string) (store.Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled {
		return store.Entry{}, false
	}
	entries, ok := s.loaded[id]
	if !ok {
		if s.dir == "" {
			s.disableLocked()
			return store.Entry{}, false
		}
		var err error
		entries, err = store.Load(s.dir, id)
		if err != nil {
			// Load reports only a store that fails the ownership
			// checks. That store is unusable for the whole run.
			s.disableLocked()
			return store.Entry{}, false
		}
		s.loaded[id] = entries
	}
	e, ok := entries[key]
	return e, ok
}

// disableLocked turns the store off for the rest of the run and notes it
// once. Callers hold mu.
func (s *Session) disableLocked() {
	if s.disabled {
		return
	}
	s.disabled = true
	s.notices.StoreUnwritable(s.dir)
}

// Flush writes the buffered changes: one store.Update per identity that
// has any, and nothing for an identity that only served values. It
// checks once, before the first write, that the store isn't inside a git
// work tree. Failures become notices; none is returned, and none stops
// provisioning. The buffer is cleared, so a second Flush writes nothing.
func (s *Session) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]vault.Identity, 0, len(s.changes))
	for id, c := range s.changes {
		if c.evictAll || len(c.puts) > 0 || len(c.evictKeys) > 0 {
			ids = append(ids, id)
		}
	}
	changes := s.changes
	s.changes = map[vault.Identity]*change{}
	if len(ids) == 0 || s.disabled {
		return
	}
	if s.dir == "" {
		s.disableLocked()
		return
	}
	if store.InWorkTree(s.dir) {
		s.notices.StoreInWorkTree(s.dir)
		return
	}

	sort.Slice(ids, func(i, j int) bool { return lessIdentity(ids[i], ids[j]) })
	for _, id := range ids {
		c := changes[id]
		evictKeys := make([]string, 0, len(c.evictKeys))
		for k := range c.evictKeys {
			evictKeys = append(evictKeys, k)
		}
		sort.Strings(evictKeys)
		err := store.Update(s.dir, id, c.puts, evictKeys, c.evictAll)
		switch {
		case err == nil:
		case errors.Is(err, store.ErrLockTimeout):
			s.notices.LockTimeout(noticeIdentity(id))
		case errors.Is(err, store.ErrInWorkTree):
			s.notices.StoreInWorkTree(s.dir)
			return
		default:
			// store.ErrUnwritable, or anything Update might add.
			s.disableLocked()
			return
		}
	}
}

func lessIdentity(a, b vault.Identity) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.APIDomain != b.APIDomain {
		return a.APIDomain < b.APIDomain
	}
	if a.ProjectID != b.ProjectID {
		return a.ProjectID < b.ProjectID
	}
	if a.Environment != b.Environment {
		return a.Environment < b.Environment
	}
	return a.FolderPath < b.FolderPath
}
