// Package fallbacknotice collects what a provisioning run's store fallback
// did that the operator has to hear about: values served from the store of
// last-resolved values, identities with nothing stored to fall back on, and
// store writes that could not happen.
//
// It is a stdlib-only leaf. Its Identity is its own struct rather than
// vault.Identity, so the CLI can hold a collector without importing the
// vault packages, and no method accepts a secret value or provider output:
// nothing recorded here can carry a secret.
//
// The collector is caller-supplied and run-scoped, like keyreport.Collector:
// a command surface creates one, hands it to the applier, and drains it after
// the run returns, including when the run returns an error. It is a sibling
// of the key report rather than part of it, so strict mode and the
// required-key check, which read only the key report, can never count a
// notice as a shortfall.
package fallbacknotice

import (
	"sort"
	"sync"
	"time"
)

// Identity names the provider store a notice is about. It mirrors
// vault.Identity field for field. None of the fields is secret.
type Identity struct {
	Kind        string
	APIDomain   string
	ProjectID   string
	Environment string
	FolderPath  string
}

// Reason says why values were served from the store. The store-fallback
// session maps each vault.Reason to exactly one of these.
type Reason int

const (
	// ReasonLoggedOut: the provider's login was missing, expired or
	// rejected.
	ReasonLoggedOut Reason = iota + 1
	// ReasonTimedOut: the provider call, or the probe that classified
	// it, reached its time bound.
	ReasonTimedOut
	// ReasonUnreachable: the provider could not be reached for any other
	// reason.
	ReasonUnreachable
)

// String names the reason for tests and debugging.
func (r Reason) String() string {
	switch r {
	case ReasonLoggedOut:
		return "logged out"
	case ReasonTimedOut:
		return "timed out"
	case ReasonUnreachable:
		return "unreachable"
	}
	return "none"
}

// ServedNote is one identity whose values were served from the store:
// the first reason recorded for it in the run and the oldest resolution
// time among the values served.
type ServedNote struct {
	Identity Identity
	Reason   Reason
	// OldestResolvedAt is the earliest resolution time of any value
	// served for Identity.
	OldestResolvedAt time.Time
}

// Collector accumulates notices during a run. Build one with New. A nil
// *Collector is valid: every recording method is a no-op and every
// accessor reports that nothing was recorded. It is safe for concurrent
// use.
type Collector struct {
	now func() time.Time

	mu            sync.Mutex
	served        map[Identity]*ServedNote
	misses        map[Identity]bool
	lockTimeouts  map[Identity]bool
	workTreeDir   string
	inWorkTree    bool
	unwritableDir string
	unwritable    bool
}

// New returns an empty collector. now is the clock ages are measured
// against when the notices are rendered; nil means time.Now.
func New(now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{
		now:          now,
		served:       map[Identity]*ServedNote{},
		misses:       map[Identity]bool{},
		lockTimeouts: map[Identity]bool{},
	}
}

// Now returns the collector's clock reading. On a nil collector it is
// time.Now().
func (c *Collector) Now() time.Time {
	if c == nil {
		return time.Now()
	}
	return c.now()
}

// Served records that values for id were served from the store. The
// first reason recorded for id is kept, and the earliest resolvedAt.
func (c *Collector) Served(id Identity, reason Reason, resolvedAt time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	note, ok := c.served[id]
	if !ok {
		c.served[id] = &ServedNote{Identity: id, Reason: reason, OldestResolvedAt: resolvedAt}
		return
	}
	if resolvedAt.Before(note.OldestResolvedAt) {
		note.OldestResolvedAt = resolvedAt
	}
}

// NothingToFallBackOn records that a key of id could not be resolved and
// the store held no value for it.
func (c *Collector) NothingToFallBackOn(id Identity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.misses[id] = true
}

// LockTimeout records that the store update for id was skipped because
// another process held the identity's lock too long.
func (c *Collector) LockTimeout(id Identity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lockTimeouts[id] = true
}

// StoreInWorkTree records that the store directory sits inside a git work
// tree, so the run wrote nothing to it. Only the first call in a run is
// kept.
func (c *Collector) StoreInWorkTree(dir string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.inWorkTree {
		c.inWorkTree = true
		c.workTreeDir = dir
	}
}

// StoreUnwritable records that the store could not be used. dir is empty
// when the store directory itself could not be located. Only the first
// call in a run is kept.
func (c *Collector) StoreUnwritable(dir string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.unwritable {
		c.unwritable = true
		c.unwritableDir = dir
	}
}

// ServedNotes returns every served identity, sorted by identity.
func (c *Collector) ServedNotes() []ServedNote {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ServedNote, 0, len(c.served))
	for _, n := range c.served {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return lessIdentity(out[i].Identity, out[j].Identity) })
	return out
}

// Misses returns every identity with nothing to fall back on, sorted.
func (c *Collector) Misses() []Identity {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return sortedIdentities(c.misses)
}

// LockTimeouts returns every identity whose update hit the lock bound,
// sorted.
func (c *Collector) LockTimeouts() []Identity {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return sortedIdentities(c.lockTimeouts)
}

// WorkTreeDir returns the store directory recorded by StoreInWorkTree and
// whether it was recorded.
func (c *Collector) WorkTreeDir() (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workTreeDir, c.inWorkTree
}

// UnwritableDir returns the store directory recorded by StoreUnwritable
// and whether it was recorded. The directory is empty when it could not be
// located.
func (c *Collector) UnwritableDir() (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unwritableDir, c.unwritable
}

// Empty reports whether nothing was recorded.
func (c *Collector) Empty() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.served) == 0 && len(c.misses) == 0 && len(c.lockTimeouts) == 0 && !c.inWorkTree && !c.unwritable
}

func sortedIdentities(set map[Identity]bool) []Identity {
	out := make([]Identity, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return lessIdentity(out[i], out[j]) })
	return out
}

func lessIdentity(a, b Identity) bool {
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
