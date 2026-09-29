//go:build unix

package storefallback

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// A held lock skips that identity's update with a notice, and the flush
// goes on to the next identity. flock belongs to the open file
// description, so holding it here contends with Update exactly as
// another process would. The wait takes the store's full 2 s bound.
func TestFlushLockTimeoutNotesAndContinues(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the store's lock bound")
	}
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"A": {Value: []byte("stored-a")}})
	locks, _ := filepath.Glob(filepath.Join(dir, "*.lock"))
	if len(locks) != 1 {
		t.Fatalf("lock files = %v", locks)
	}
	f, err := os.OpenFile(locks[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}

	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	s.put(sessionID, "A", store.Entry{Value: []byte("a-2")})
	s.put(mintedID, "B", store.Entry{Value: []byte("b-1")})
	s.Flush()

	if got := notices.LockTimeouts(); len(got) != 1 || got[0] != noticeIdentity(sessionID) {
		t.Fatalf("lock timeouts = %+v", got)
	}
	if string(load(t, dir, sessionID)["A"].Value) != "stored-a" {
		t.Fatal("the locked identity was written")
	}
	if string(load(t, dir, mintedID)["B"].Value) != "b-1" {
		t.Fatal("the flush stopped at the locked identity")
	}
	if _, unwritable := notices.UnwritableDir(); unwritable {
		t.Fatal("a lock timeout was reported as an unwritable store")
	}
}
