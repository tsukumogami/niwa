package keyreport

import (
	"reflect"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func entry(scope, key string, cause config.UnresolvedCause) Entry {
	return Entry{Scope: scope, Key: key, Cause: cause}
}

// TestReportSortsByCauseThenScopeThenKey pins the order the report is rendered
// in. Both producers feed the collector out of order -- one is a map walk, the
// other a pool of goroutines -- so the order has to come from the sort rather
// than from insertion.
func TestReportSortsByCauseThenScopeThenKey(t *testing.T) {
	c := New()
	c.Add(entry("env.secrets", "ZULU", config.CauseKeyNotFound))
	c.Add(entry("env.vars", "ALPHA", CauseNoSource))
	c.Add(entry("env.secrets", "ALPHA", config.CauseKeyNotFound))
	c.Add(entry("claude.env.vars", "MIKE", CauseNoSource))

	var got []string
	for _, e := range c.Report() {
		got = append(got, string(e.Cause)+"|"+e.Scope+"|"+e.Key)
	}
	want := []string{
		"key-not-found|env.secrets|ALPHA",
		"key-not-found|env.secrets|ZULU",
		"no-source|claude.env.vars|MIKE",
		"no-source|env.vars|ALPHA",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Report order =\n  %v\nwant\n  %v", got, want)
	}
}

// TestReportIndependentOfInsertionOrder is the determinism guarantee stated
// directly: the same set of shortfalls recorded in a different sequence renders
// identically. Without this, a report's order would track goroutine scheduling.
func TestReportIndependentOfInsertionOrder(t *testing.T) {
	entries := []Entry{
		entry("instance.env.vars", "C", config.CauseProviderUnreachable),
		entry("env.secrets", "A", CauseNoSource),
		entry("env.secrets", "B", config.CauseKeyNotFound),
	}

	forward := New()
	for _, e := range entries {
		forward.Add(e)
	}
	backward := New()
	for i := len(entries) - 1; i >= 0; i-- {
		backward.Add(entries[i])
	}

	if !reflect.DeepEqual(forward.Report(), backward.Report()) {
		t.Errorf("report depends on insertion order:\n  %v\n  %v", forward.Report(), backward.Report())
	}
}

// TestAddDeduplicates covers the same shortfall arriving from two producers:
// the post-merge walk sees the mark, and a per-repo materializer reports the
// same key again when it omits it. R6 asks for one consolidated report.
func TestAddDeduplicates(t *testing.T) {
	c := New()
	c.Add(Entry{Scope: "env.secrets", Key: "K", Cause: config.CauseKeyNotFound, Description: "first"})
	c.Add(Entry{Scope: "env.secrets", Key: "K", Cause: config.CauseKeyNotFound, Description: "second"})

	got := c.Report()
	if len(got) != 1 {
		t.Fatalf("Report() has %d entries, want 1: %v", len(got), got)
	}
	if got[0].Description != "first" {
		t.Errorf("description = %q, want the first record to win", got[0].Description)
	}
}

// TestSameKeyDifferentCauseIsKeptSeparate: two causes for one key are two
// facts, and collapsing them would hide the one carrying a remedy.
func TestSameKeyDifferentCauseIsKeptSeparate(t *testing.T) {
	c := New()
	c.Add(entry("env.secrets", "K", config.CauseKeyNotFound))
	c.Add(entry("env.secrets", "K", config.CauseProviderUnreachable))
	if got := len(c.Report()); got != 2 {
		t.Errorf("Report() has %d entries, want 2", got)
	}
}

// TestAddMarkIgnoresNilMark lets callers hand MaybeSecret.Unresolved straight
// in without testing it first, which is what keeps the walk that feeds this
// free of branching.
func TestAddMarkIgnoresNilMark(t *testing.T) {
	c := New()
	c.AddMark("env.secrets", "K", nil)
	if !c.Empty() {
		t.Errorf("a nil mark recorded an entry: %v", c.Report())
	}
}

// TestAddMarkCarriesEveryMarkField: the mark is the only source for cause,
// level, description and provider kind, so dropping one silently would empty a
// column of the report.
func TestAddMarkCarriesEveryMarkField(t *testing.T) {
	c := New()
	c.AddMark("env.secrets", "K", &config.Unresolved{
		Cause:        config.CauseClientNotInstalled,
		Level:        config.LevelRecommended,
		Description:  "what the key is for",
		ProviderKind: "fake",
	})
	want := Entry{
		Scope:        "env.secrets",
		Key:          "K",
		Cause:        config.CauseClientNotInstalled,
		Level:        config.LevelRecommended,
		Description:  "what the key is for",
		ProviderKind: "fake",
	}
	if got := c.Report(); len(got) != 1 || got[0] != want {
		t.Errorf("Report() = %v, want [%v]", got, want)
	}
}

// TestNilCollectorIsInert: a surface that was never wired with a collector must
// not need a guard at every call site.
func TestNilCollectorIsInert(t *testing.T) {
	var c *Collector
	c.Add(entry("env.secrets", "K", CauseNoSource))
	c.AddMark("env.secrets", "K", &config.Unresolved{Cause: CauseNoSource})
	if !c.Empty() {
		t.Error("nil collector reports non-empty")
	}
	if got := c.Report(); got != nil {
		t.Errorf("nil collector Report() = %v, want nil", got)
	}
}

// TestConcurrentAdd exercises the mutex. Records originate inside the per-repo
// clone worker pool, so accumulation is genuinely concurrent; run with -race to
// see this fail without the lock.
func TestConcurrentAdd(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Add(Entry{Scope: "repos.app.env.secrets", Key: string(rune('A' + i)), Cause: CauseNoSource})
		}(i)
	}
	wg.Wait()
	if got := len(c.Report()); got != 32 {
		t.Errorf("Report() has %d entries, want 32", got)
	}
}
