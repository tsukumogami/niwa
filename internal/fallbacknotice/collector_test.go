package fallbacknotice

import (
	"sync"
	"testing"
	"time"
)

var (
	idA = Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "a", Environment: "dev", FolderPath: "/"}
	idB = Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "b", Environment: "dev", FolderPath: "/"}
)

func TestNilCollectorIsANoOp(t *testing.T) {
	var c *Collector
	c.Served(idA, ReasonLoggedOut, time.Now())
	c.NothingToFallBackOn(idA)
	c.LockTimeout(idA)
	c.StoreInWorkTree("/x")
	c.StoreUnwritable("/x")
	if !c.Empty() || c.ServedNotes() != nil || c.Misses() != nil || c.LockTimeouts() != nil {
		t.Fatal("nil collector reported records")
	}
	if _, ok := c.WorkTreeDir(); ok {
		t.Fatal("nil collector reported a work-tree notice")
	}
	if _, ok := c.UnwritableDir(); ok {
		t.Fatal("nil collector reported an unwritable notice")
	}
}

func TestServedKeepsFirstReasonAndOldestTime(t *testing.T) {
	c := New(nil)
	t1 := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	t0 := t1.Add(-48 * time.Hour)
	c.Served(idA, ReasonLoggedOut, t1)
	c.Served(idA, ReasonTimedOut, t0)
	c.Served(idA, ReasonUnreachable, t1.Add(time.Hour))
	notes := c.ServedNotes()
	if len(notes) != 1 || notes[0].Reason != ReasonLoggedOut || !notes[0].OldestResolvedAt.Equal(t0) {
		t.Fatalf("notes = %+v", notes)
	}
}

func TestAccessorsAreSortedAndDeduplicated(t *testing.T) {
	c := New(nil)
	c.NothingToFallBackOn(idB)
	c.NothingToFallBackOn(idA)
	c.NothingToFallBackOn(idB)
	c.LockTimeout(idB)
	c.LockTimeout(idA)
	c.Served(idB, ReasonTimedOut, time.Now())
	c.Served(idA, ReasonTimedOut, time.Now())
	if m := c.Misses(); len(m) != 2 || m[0] != idA || m[1] != idB {
		t.Fatalf("misses = %+v", m)
	}
	if l := c.LockTimeouts(); len(l) != 2 || l[0] != idA {
		t.Fatalf("lock timeouts = %+v", l)
	}
	if s := c.ServedNotes(); len(s) != 2 || s[0].Identity != idA {
		t.Fatalf("served = %+v", s)
	}
}

func TestStoreWarningsKeepTheFirstRecord(t *testing.T) {
	c := New(nil)
	if !c.Empty() {
		t.Fatal("new collector not empty")
	}
	c.StoreInWorkTree("/first")
	c.StoreInWorkTree("/second")
	c.StoreUnwritable("")
	c.StoreUnwritable("/later")
	if d, ok := c.WorkTreeDir(); !ok || d != "/first" {
		t.Fatalf("work tree = %q, %v", d, ok)
	}
	if d, ok := c.UnwritableDir(); !ok || d != "" {
		t.Fatalf("unwritable = %q, %v", d, ok)
	}
	if c.Empty() {
		t.Fatal("collector with store warnings reads empty")
	}
}

func TestClock(t *testing.T) {
	at := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	if got := New(func() time.Time { return at }).Now(); !got.Equal(at) {
		t.Fatalf("Now = %v", got)
	}
}

func TestConcurrentRecording(t *testing.T) {
	c := New(nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := idA
			if i%2 == 0 {
				id = idB
			}
			c.Served(id, ReasonUnreachable, time.Now())
			c.NothingToFallBackOn(id)
			c.LockTimeout(id)
			c.StoreUnwritable("/x")
			_ = c.ServedNotes()
		}(i)
	}
	wg.Wait()
	if len(c.ServedNotes()) != 2 || len(c.Misses()) != 2 {
		t.Fatal("concurrent records lost")
	}
}
