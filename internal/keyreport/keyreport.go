// Package keyreport accumulates the declared environment keys a niwa run could
// not supply and renders them as one consolidated report per command surface.
//
// It is a leaf with respect to internal/workspace and internal/vault: it imports
// internal/config for the mark types that ride on a resolved value, and nothing
// else from this codebase. That is what lets the resolver, the workspace applier
// and the CLI all hold the same collector without an import cycle. Unlike
// internal/envformat and internal/gitexclude, which are leaves because they are
// stdlib-only, this package is deliberately NOT stdlib-only: the cause and level
// vocabulary belongs to config beside MaybeSecret, and copying it here would
// leave two enums free to drift.
//
// The collector is caller-supplied and run-scoped: a command surface creates
// one, hands it to the applier, and drains it after the run returns — including
// when the run returns an error. That is not incidental. Applier.Create removes
// the instance directory and returns a bare error, so a report returned up the
// call stack would be discarded on exactly the failure path where a user most
// needs every key enumerated.
package keyreport

import (
	"sort"
	"sync"

	"github.com/tsukumogami/niwa/internal/config"
)

// CauseNoSource is the cause for a key declared in a requirement sub-table with
// no entry in the values map at all. Nothing ever tried to resolve it, because
// no vault:// reference or literal was written for it anywhere in the merged
// configuration — which is the exact shape a contributor with no provider
// configured hits on a first run.
//
// It lives here rather than in config's UnresolvedCause set because it never
// rides on a value: there is no MaybeSecret to hang a mark on, and that absence
// is precisely what distinguishes it. config's set stays closed over the causes
// the resolver can mark.
const CauseNoSource = config.UnresolvedCause("no-source")

// Entry is one key the run could not supply, with everything a renderer needs.
// It carries no secret material: a shortfall has no value by construction, and
// the description is configuration text rather than a resolved value.
type Entry struct {
	// Scope is the dotted table the key was declared in, for example
	// "env.secrets" or "repos.app.claude.env.vars".
	Scope string

	// Key is the environment variable (or settings) name.
	Key string

	// Cause is why the key has no value: one of config's four mark causes, or
	// CauseNoSource for the declared-but-never-referenced shape.
	Cause config.UnresolvedCause

	// Level is the key's placement in the required / recommended / optional
	// sub-tables, or config.LevelUnspecified when it appears in none of them.
	Level config.RequirementLevel

	// Description is the author-supplied text from the requirement sub-table.
	// It is free text out of a TOML file and must be treated as untrusted:
	// renderers sanitize it before it reaches a terminal or an agent.
	Description string

	// ProviderKind is the backend kind of the provider that was asked, empty
	// when none was reached.
	ProviderKind string
}

// entryKey deduplicates records of the same shortfall. The same key can be
// offered by more than one producer — the post-merge walk sees the mark, and a
// per-repo materializer may report the same key again when it omits it — and
// R6 asks for one consolidated report, not one line per producer.
type entryKey struct {
	scope string
	key   string
	cause config.UnresolvedCause
}

// Collector accumulates entries during a run. The zero value is not usable;
// call New. A nil *Collector is valid and every method on it is a no-op, so a
// call site that was never wired with one does not need a guard.
type Collector struct {
	mu      sync.Mutex
	entries []Entry
	seen    map[entryKey]bool
}

// New returns an empty collector ready for concurrent use.
func New() *Collector {
	return &Collector{seen: make(map[entryKey]bool)}
}

// Add records one unresolved key, ignoring a repeat of a shortfall already
// recorded. It is safe to call from several goroutines: the per-repo
// materializers run inside the clone worker pool, so accumulation is genuinely
// concurrent even though the post-merge walk that feeds it is not.
func (c *Collector) Add(e Entry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[entryKey]bool)
	}
	k := entryKey{scope: e.Scope, key: e.Key, cause: e.Cause}
	if c.seen[k] {
		return
	}
	c.seen[k] = true
	c.entries = append(c.entries, e)
}

// AddMark records a key whose resolved value carries an Unresolved mark. A nil
// mark records nothing, so callers can hand it MaybeSecret.Unresolved directly
// without testing it first.
func (c *Collector) AddMark(scope, key string, u *config.Unresolved) {
	if c == nil || u == nil {
		return
	}
	c.Add(Entry{
		Scope:        scope,
		Key:          key,
		Cause:        u.Cause,
		Level:        u.Level,
		Description:  u.Description,
		ProviderKind: u.ProviderKind,
	})
}

// Report returns the accumulated entries sorted by cause, then scope, then key.
//
// Sorting happens on read rather than on write on purpose. The producers are a
// map walk and a pool of goroutines, so an order that depended on insertion
// would depend on both map iteration and goroutine scheduling. Sorting here
// makes the order a function of the input alone, which is what determinism
// across runs against unchanged input requires.
//
// The returned slice is a copy; the caller may sort or filter it further
// without disturbing the collector.
func (c *Collector) Report() []Entry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	out := make([]Entry, len(c.entries))
	copy(out, c.entries)
	c.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Cause != out[j].Cause {
			return out[i].Cause < out[j].Cause
		}
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Empty reports whether the run recorded no shortfall at all, which is the
// common case and the one where a surface renders nothing.
func (c *Collector) Empty() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries) == 0
}
