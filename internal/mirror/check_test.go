package mirror

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

// threePacks is a healthy mirror whose manifest lists three packs: the
// build's, then one from each of two refreshes that brought a commit. A
// borrower names its stable path, nothing is left over from a restore, and
// the remote has been moved away, so any call that reached for it would
// fail. oldest is the build's pack, which holds no commit HEAD names.
type threePacks struct {
	fixture
	store, oldest string
}

func newThreePacks(tb testing.TB) threePacks {
	tb.Helper()
	f := built(tb)
	st, err := readState(f.mdir(), "app")
	if err != nil {
		tb.Fatal(err)
	}
	if len(st.Packs) != 1 {
		tb.Fatalf("the build recorded %d packs, want 1", len(st.Packs))
	}
	oldest := st.Packs[0].Name
	opts := f.applyOpts()
	opts.Refresh = true
	for _, content := range []string{"second", "third"} {
		pushCommit(tb, f, "main", content)
		res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil {
			tb.Fatalf("refreshing %s into the mirror: %+v", content, res)
		}
	}
	if st, err = readState(f.mdir(), "app"); err != nil || len(st.Packs) < 3 {
		tb.Fatalf("the manifest lists %d packs (%v), want at least 3", len(st.Packs), err)
	}
	hideRemote(tb, f)
	return threePacks{fixture: f, store: filepath.Join(f.mdir(), st.Store), oldest: oldest}
}

// recordGit records the arguments of every git process the package starts
// until the test ends.
func recordGit(t *testing.T) func() [][]string {
	t.Helper()
	var mu sync.Mutex
	var calls [][]string
	gitHook = func(args []string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, args)
	}
	t.Cleanup(func() { gitHook = nil })
	return func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}

// storeRefs lists every ref of the store with its value.
func (p threePacks) storeRefs(t *testing.T) string {
	t.Helper()
	return testGit(t, p.store, "--git-dir", p.store, "for-each-ref", "--format=%(refname) %(objectname)")
}

// healthyCall runs one EnsureHealthy with Refresh off, recording every git
// process it starts, and requires it healthy with no error of either kind.
func (p threePacks) healthyCall(t *testing.T) [][]string {
	t.Helper()
	calls := recordGit(t)
	res := EnsureHealthy(context.Background(), p.applyOpts(), []Repo{p.repo()})
	gitHook = nil
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want healthy with no error and the log written", res)
	}
	return calls()
}

func (p threePacks) revParse() []string {
	return []string{"--git-dir=" + p.store, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"}
}

func TestHealthyCheck_StartsOneLocalGitProcess(t *testing.T) {
	p := newThreePacks(t)
	refs := p.storeRefs(t)
	packs := treeFiles(t, filepath.Join(p.store, "objects", "pack"))
	state := stateBytes(t, p.fixture)

	calls := p.healthyCall(t)
	if len(calls) != 1 || !slices.Equal(calls[0], p.revParse()) {
		t.Fatalf("the healthy call ran git %q, want exactly %q", calls, p.revParse())
	}
	if p.storeRefs(t) != refs {
		t.Fatal("the healthy call changed the store's refs")
	}
	if !maps.Equal(treeFiles(t, filepath.Join(p.store, "objects", "pack")), packs) {
		t.Fatal("the healthy call changed the store's packs")
	}
	if !bytes.Equal(stateBytes(t, p.fixture), state) {
		t.Fatal("the healthy call changed the state file")
	}
}

func TestHealthyCheck_AMovedCtimeAddsOneVerifyPack(t *testing.T) {
	p := newThreePacks(t)
	st, err := readState(p.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(p.store, "objects", "pack", st.Packs[len(st.Packs)-1].Name)
	time.Sleep(20 * time.Millisecond)
	makeWritable(t, pack)
	if now, err := describePack(pack); err != nil || now.CtimeNs == st.Packs[len(st.Packs)-1].CtimeNs {
		t.Fatalf("the chmod did not move the pack's ctime (%v); the case proves nothing", err)
	}

	calls := p.healthyCall(t)
	want := [][]string{{"verify-pack", "--", idxOf(pack)}, p.revParse()}
	if !slices.EqualFunc(calls, want, slices.Equal) {
		t.Fatalf("the healthy call ran git %q, want exactly %q", calls, want)
	}
}

func TestHealthyCheck_NeverReadsAPackPastItsTrailer(t *testing.T) {
	p := newThreePacks(t)
	pack := filepath.Join(p.store, "objects", "pack", p.oldest)
	info, err := os.Stat(pack)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	makeWritable(t, pack)
	fh, err := os.OpenFile(pack, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Past the 12-byte header, up to the 20-byte trailer.
	if _, err := fh.WriteAt(make([]byte, info.Size()-12-20), 12); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	now, err := describePack(pack)
	if err != nil {
		t.Fatal(err)
	}
	editState(t, p.fixture, func(st *state) {
		for i := range st.Packs {
			if st.Packs[i].Name == p.oldest {
				st.Packs[i].CtimeNs = now.CtimeNs
			}
		}
	})
	if verifyPack(p.gitCtx(t), idxOf(pack)) == nil {
		t.Fatal("verify-pack passes the zeroed pack; the case proves nothing")
	}

	calls := p.healthyCall(t)
	if len(calls) != 1 || !slices.Equal(calls[0], p.revParse()) {
		t.Fatalf("the healthy call ran git %q, want only %q", calls, p.revParse())
	}
}

// BenchmarkHealthyCheck times one healthy call with Refresh off, for
// information only: it fails only on a call that isn't healthy or reports an
// error, and asserts no time figure.
func BenchmarkHealthyCheck(b *testing.B) {
	p := newThreePacks(b)
	opts := p.applyOpts()
	repos := []Repo{p.repo()}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := EnsureHealthy(context.Background(), opts, repos)
		if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil || res[0].LogErr != nil {
			b.Fatalf("result = %+v, want healthy with no error", res)
		}
	}
}
