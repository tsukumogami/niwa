package fallbacknotice

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the RenderContext fixtures in testdata/")

// renderNow is the fixed clock every rendering test measures ages against.
var renderNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// idProd is an identity with every field distinct, so a rendering that
// swaps two fields fails the golden comparison.
var idProd = Identity{
	Kind:        "infisical",
	APIDomain:   "https://app.infisical.com",
	ProjectID:   "proj-123",
	Environment: "prod",
	FolderPath:  "/backend",
}

func clock() func() time.Time { return func() time.Time { return renderNow } }

// The served warning and the nothing-to-fall-back-on line are the design's
// Decision Outcome text; these goldens are the byte-for-byte check.
func TestRenderTextServedGolden(t *testing.T) {
	cases := []struct {
		reason Reason
		want   string
	}{
		{ReasonLoggedOut, "warning: using stored values that may be stale for infisical project proj-123 (env prod, path /backend, https://app.infisical.com): the provider is logged out or expired; the oldest value is 3 days old. Run `infisical login` to refresh them.\n"},
		{ReasonTimedOut, "warning: using stored values that may be stale for infisical project proj-123 (env prod, path /backend, https://app.infisical.com): the provider timed out; the oldest value is 3 days old. Run `infisical login` to refresh them.\n"},
		{ReasonUnreachable, "warning: using stored values that may be stale for infisical project proj-123 (env prod, path /backend, https://app.infisical.com): the provider is unreachable; the oldest value is 3 days old. Run `infisical login` to refresh them.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.reason.String(), func(t *testing.T) {
			c := New(clock())
			c.Served(idProd, tc.reason, renderNow.Add(-72*time.Hour))
			if got := c.RenderText(); got != tc.want {
				t.Errorf("RenderText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestRenderTextMissGolden(t *testing.T) {
	c := New(clock())
	c.NothingToFallBackOn(idProd)
	want := "warning: infisical project proj-123 (env prod, path /backend, https://app.infisical.com) could not be used and no previously resolved value exists to fall back on. Run `infisical login`.\n"
	if got := c.RenderText(); got != want {
		t.Errorf("RenderText:\n got %q\nwant %q", got, want)
	}
}

func TestRenderTextStoreWarningsGolden(t *testing.T) {
	cases := []struct {
		name   string
		record func(c *Collector)
		want   string
	}{
		{"lock timeout", func(c *Collector) { c.LockTimeout(idProd) },
			"warning: another niwa process held the secret store lock for infisical project proj-123 (env prod, path /backend, https://app.infisical.com) too long; values resolved for it in this run were not stored.\n"},
		{"work tree", func(c *Collector) { c.StoreInWorkTree("/home/u/.local/state/niwa/secret-cache") },
			"warning: the secret store directory /home/u/.local/state/niwa/secret-cache is inside a git work tree; niwa stored no values in it this run.\n"},
		{"unwritable", func(c *Collector) { c.StoreUnwritable("/home/u/.local/state/niwa/secret-cache") },
			"warning: the secret store directory /home/u/.local/state/niwa/secret-cache could not be read or written; values resolved in this run were not stored.\n"},
		{"unwritable, no directory", func(c *Collector) { c.StoreUnwritable("") },
			"warning: the secret store directory could not be located (HOME is unset or not absolute); values resolved in this run were not stored.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(clock())
			// Recorded three times: each warning prints once per run.
			for i := 0; i < 3; i++ {
				tc.record(c)
			}
			if got := c.RenderText(); got != tc.want {
				t.Errorf("RenderText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// R20, first reason wins: a later reason recorded for the same identity
// never overrides the first. The mapping from vault.Reason is covered by
// storefallback's TestMappedFirstReasonWinsInTheRendering.
func TestRenderFirstReasonWins(t *testing.T) {
	cases := []struct {
		name  string
		order []Reason
		want  string
	}{
		{"logged out then timed out", []Reason{ReasonLoggedOut, ReasonTimedOut}, "the provider is logged out or expired;"},
		{"timed out then logged out", []Reason{ReasonTimedOut, ReasonLoggedOut}, "the provider timed out;"},
		{"unreachable then logged out", []Reason{ReasonUnreachable, ReasonLoggedOut}, "the provider is unreachable;"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(clock())
			for _, r := range tc.order {
				c.Served(idProd, r, renderNow.Add(-time.Hour))
			}
			for name, got := range map[string]string{"text": c.RenderText(), "context": c.RenderContext()} {
				if !strings.Contains(got, tc.want) {
					t.Errorf("%s rendering lacks %q:\n%s", name, tc.want, got)
				}
			}
		})
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		age  time.Duration
		want string
	}{
		{-time.Minute, "less than a minute"},
		{0, "less than a minute"},
		{30 * time.Second, "less than a minute"},
		{59*time.Second + 999*time.Millisecond, "less than a minute"},
		{60 * time.Second, "1 minute"},
		{time.Minute, "1 minute"},
		{119 * time.Second, "1 minute"},
		{2 * time.Minute, "2 minutes"},
		{59 * time.Minute, "59 minutes"},
		{60 * time.Minute, "1 hour"},
		{time.Hour, "1 hour"},
		{47 * time.Hour, "47 hours"},
		{48*time.Hour - time.Second, "47 hours"},
		{48 * time.Hour, "2 days"},
		{71 * time.Hour, "2 days"},
		{30 * 24 * time.Hour, "30 days"},
	}
	for _, tc := range cases {
		if got := formatAge(tc.age); got != tc.want {
			t.Errorf("formatAge(%v) = %q, want %q", tc.age, got, tc.want)
		}
	}
}

// R20: one warning per identity, aged by its oldest served value.
func TestRenderOneWarningPerIdentityWithTheOldestAge(t *testing.T) {
	c := New(clock())
	c.Served(idProd, ReasonLoggedOut, renderNow.Add(-3*24*time.Hour))
	c.Served(idProd, ReasonLoggedOut, renderNow.Add(-5*24*time.Hour))
	got := c.RenderText()
	if n := strings.Count(got, "may be stale"); n != 1 {
		t.Fatalf("got %d served warnings, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "the oldest value is 5 days old") {
		t.Errorf("warning does not read 5 days:\n%s", got)
	}
}

// R20's field list, checked against both renderings: a rendering that
// drops any field fails.
func TestRenderServedCarriesEveryR20Field(t *testing.T) {
	c := New(clock())
	c.Served(idProd, ReasonLoggedOut, renderNow.Add(-50*time.Hour))
	want := []string{
		"infisical", "https://app.infisical.com", "proj-123", "env prod", "path /backend",
		"logged out or expired", "may be stale", "2 days", "Run `infisical login` to refresh them",
	}
	for name, got := range map[string]string{"text": c.RenderText(), "context": c.RenderContext()} {
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s rendering lacks %q:\n%s", name, w, got)
			}
		}
	}
	if ctx := c.RenderContext(); !strings.Contains(ctx, "Ask the operator to run `infisical login`; do not run it yourself") {
		t.Errorf("context rendering does not hand the login to the operator:\n%s", ctx)
	}
}

func TestRenderMissCarriesTheIdentity(t *testing.T) {
	c := New(clock())
	c.NothingToFallBackOn(idProd)
	for name, got := range map[string]string{"text": c.RenderText(), "context": c.RenderContext()} {
		for _, w := range []string{"infisical", "https://app.infisical.com", "proj-123", "env prod", "path /backend",
			"no previously resolved value exists to fall back on", "infisical login"} {
			if !strings.Contains(got, w) {
				t.Errorf("%s rendering lacks %q:\n%s", name, w, got)
			}
		}
	}
}

// Output order: served, then misses, then store warnings, each sorted by
// identity. Recording in the opposite order changes nothing.
func TestRenderIsDeterministic(t *testing.T) {
	idOther := idProd
	idOther.ProjectID = "proj-000"
	record := func(reverse bool) *Collector {
		c := New(clock())
		steps := []func(){
			func() { c.Served(idProd, ReasonTimedOut, renderNow.Add(-time.Hour)) },
			func() { c.Served(idOther, ReasonLoggedOut, renderNow.Add(-2*time.Hour)) },
			func() { c.NothingToFallBackOn(idProd) },
			func() { c.NothingToFallBackOn(idOther) },
			func() { c.LockTimeout(idProd) },
			func() { c.LockTimeout(idOther) },
			func() { c.StoreInWorkTree("/state") },
			func() { c.StoreUnwritable("/state") },
		}
		if reverse {
			for i := len(steps) - 1; i >= 0; i-- {
				steps[i]()
			}
		} else {
			for _, s := range steps {
				s()
			}
		}
		return c
	}
	a, b := record(false), record(true)
	if a.RenderText() != b.RenderText() || a.RenderContext() != b.RenderContext() {
		t.Fatalf("recording order changed the output:\n%s\n---\n%s", a.RenderText(), b.RenderText())
	}
	lines := strings.Split(strings.TrimSuffix(a.RenderText(), "\n"), "\n")
	prefixes := []string{
		"warning: using stored values that may be stale for infisical project proj-000",
		"warning: using stored values that may be stale for infisical project proj-123",
		"warning: infisical project proj-000",
		"warning: infisical project proj-123",
		"warning: another niwa process held the secret store lock for infisical project proj-000",
		"warning: another niwa process held the secret store lock for infisical project proj-123",
		"warning: the secret store directory /state is inside a git work tree",
		"warning: the secret store directory /state could not be read or written",
	}
	if len(lines) != len(prefixes) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(prefixes), a.RenderText())
	}
	for i, p := range prefixes {
		if !strings.HasPrefix(lines[i], p) {
			t.Errorf("line %d = %q, want prefix %q", i, lines[i], p)
		}
	}
}

func TestRenderSanitisesAndCapsFields(t *testing.T) {
	long := strings.Repeat("x", 300)
	id := Identity{
		Kind:        "infisical",
		APIDomain:   long,
		ProjectID:   "evil\nwarning: forged\r\u2028line",
		Environment: "prod",
		FolderPath:  "/",
	}
	c := New(clock())
	c.Served(id, ReasonLoggedOut, renderNow)
	c.NothingToFallBackOn(id)
	c.StoreInWorkTree("/state\n" + long)
	for name, got := range map[string]string{"text": c.RenderText(), "context": c.RenderContext()} {
		for _, bad := range []string{"\r", "\u2028", "evil\n"} {
			if strings.Contains(got, bad) {
				t.Errorf("%s rendering keeps %q:\n%q", name, bad, got)
			}
		}
		if !strings.Contains(got, "project evilwarning: forgedline (") {
			t.Errorf("%s rendering did not strip the project's control characters:\n%q", name, got)
		}
		if strings.Contains(got, strings.Repeat("x", 201)) {
			t.Errorf("%s rendering holds a field over 200 characters", name)
		}
		if !strings.Contains(got, strings.Repeat("x", 200)) {
			t.Errorf("%s rendering cut a field below 200 characters", name)
		}
	}
	if n := strings.Count(c.RenderText(), "\n"); n != 3 {
		t.Errorf("text rendering has %d lines, want 3:\n%s", n, c.RenderText())
	}
}

func TestRenderEmpty(t *testing.T) {
	var nilC *Collector
	for name, c := range map[string]*Collector{"nil": nilC, "new": New(clock())} {
		if got := c.RenderText(); got != "" {
			t.Errorf("%s: RenderText = %q, want empty", name, got)
		}
		if got := c.RenderContext(); got != "" {
			t.Errorf("%s: RenderContext = %q, want empty", name, got)
		}
	}
}

// RenderContext fixtures, one per notice kind, in testdata/. Run with
// -update to rewrite them after a deliberate wording change.
func TestRenderContextGolden(t *testing.T) {
	cases := []struct {
		name   string
		record func(c *Collector)
	}{
		{"served", func(c *Collector) { c.Served(idProd, ReasonLoggedOut, renderNow.Add(-5*24*time.Hour)) }},
		{"nothing-to-fall-back-on", func(c *Collector) { c.NothingToFallBackOn(idProd) }},
		{"store-in-work-tree", func(c *Collector) { c.StoreInWorkTree("/home/u/.local/state/niwa/secret-cache") }},
		{"lock-timeout", func(c *Collector) { c.LockTimeout(idProd) }},
		{"store-unwritable", func(c *Collector) { c.StoreUnwritable("/home/u/.local/state/niwa/secret-cache") }},
		{"store-unwritable-no-directory", func(c *Collector) { c.StoreUnwritable("") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(clock())
			tc.record(c)
			got := c.RenderContext()
			path := filepath.Join("testdata", "context-"+tc.name+".txt")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			if got != string(want) {
				t.Errorf("RenderContext differs from %s:\n got %q\nwant %q", path, got, want)
			}
		})
	}
}

// The collector is shared by concurrent resolutions; run with -race.
func TestCollectorRecordsConcurrently(t *testing.T) {
	c := New(clock())
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := idProd
			id.ProjectID = fmt.Sprintf("p%d", i%4)
			c.Served(id, ReasonLoggedOut, renderNow.Add(-time.Duration(i)*time.Hour))
			c.NothingToFallBackOn(id)
			c.LockTimeout(id)
			c.StoreInWorkTree("/state")
			c.StoreUnwritable("/state")
			_ = c.RenderText()
			_ = c.RenderContext()
		}(i)
	}
	wg.Wait()
	if n := strings.Count(c.RenderText(), "may be stale"); n != 4 {
		t.Errorf("got %d served warnings, want 4", n)
	}
}
