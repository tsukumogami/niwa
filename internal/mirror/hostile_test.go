package mirror

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// gitCtx is the context a test's direct call of check or verifyPack runs
// under: the fixture home's empty-hooks directory, prepared as ensureOne
// prepares it, on the context gitRun reads it from.
func (f fixture) gitCtx(t testing.TB) context.Context {
	t.Helper()
	if err := prepareEmptyHooks(f.home); err != nil {
		t.Fatal(err)
	}
	return withEmptyHooks(context.Background(), emptyHooksDir(f.home))
}

// recordGitProcs records every git process the package starts, with its -c
// pairs and the environment niwa added, until the test ends.
func recordGitProcs(t *testing.T) func() []gitProc {
	t.Helper()
	var mu sync.Mutex
	var procs []gitProc
	gitProcHook = func(p gitProc) {
		mu.Lock()
		defer mu.Unlock()
		procs = append(procs, p)
	}
	t.Cleanup(func() { gitProcHook = nil })
	return func() []gitProc {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(procs)
	}
}

// subcommand is the first argument that isn't an option: the git subcommand.
func subcommand(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

// markerScript writes an executable shell script at path that appends a line
// to marker, and returns path.
func markerScript(t *testing.T, path, marker string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ran >> '"+marker+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// requireNoMarker fails when any of paths exists.
func requireNoMarker(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("marker %s exists (%v): something ran that must not", p, err)
		}
	}
}

// requireNoMarks fails when the marker directory marks holds anything.
func requireNoMarks(t *testing.T, marks string) {
	t.Helper()
	entries, err := os.ReadDir(marks)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("markers %v exist: something ran that must not", names)
	}
}

// plantStoreHook writes an executable hooks/reference-transaction in store
// that appends to marker. git runs that hook for every ref update unless
// core.hooksPath points elsewhere.
func plantStoreHook(t *testing.T, store, marker string) {
	t.Helper()
	markerScript(t, filepath.Join(store, "hooks", "reference-transaction"), marker)
}

// realBorrower clones the fixture's remote and makes it borrow from the
// mirror as the borrowing create will: alternates naming the stable path's
// objects, then a local repack and prune-packed, so it holds none of the
// mirror's objects.
func realBorrower(t *testing.T, f fixture) string {
	t.Helper()
	borrower := filepath.Join(t.TempDir(), "borrower")
	testGit(t, filepath.Dir(borrower), "clone", "--quiet", f.remote, borrower)
	alternates := filepath.Join(borrower, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(StablePath(f.home, f.root, "app")+"/objects\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, borrower, "repack", "-a", "-d", "-l", "-q")
	testGit(t, borrower, "prune-packed")
	return borrower
}

// copyTreeKeepingLinks copies src to dst as copyTree does, but recreates
// each link as a link with the same target rather than reading through it.
func copyTreeKeepingLinks(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, to)
		case d.IsDir():
			return os.MkdirAll(to, 0o755)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(to, data, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// linkOutside copies the file or directory at path to a new directory
// outside the fixture's home, removes it and puts a link to the copy in its
// place. It returns the copy's path.
func linkOutside(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), filepath.Base(path))
	if info.IsDir() {
		copyTreeKeepingLinks(t, path, outside)
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, data, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	return outside
}

// requireMode fails unless path is, by Lstat, a directory (dir) or a regular
// file with mode perm.
func requireMode(t *testing.T, path string, perm os.FileMode, dir bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if dir && !info.IsDir() || !dir && !info.Mode().IsRegular() || info.Mode().Perm() != perm {
		t.Fatalf("%s has mode %s, want %v with mode %04o", path, info.Mode(), map[bool]string{true: "a directory", false: "a regular file"}[dir], perm)
	}
}

// requireLink fails unless path is still a link.
func requireLink(t *testing.T, path string) {
	t.Helper()
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer the planted link: %v (%v)", path, info, err)
	}
}

// requireOutcome fails unless res has fault and outcome and no Err.
func requireOutcome(t *testing.T, res Result, fault Fault, outcome Outcome) {
	t.Helper()
	if res.Fault != fault || res.Outcome != outcome || res.Err != nil {
		t.Fatalf("result = %+v, want fault %q, outcome %s and no error", res, fault, outcome)
	}
}

// route is one way a caller reaches a mirror: EnsureHealthy on a mirror one
// EnsureHealthy built for the fixture's borrower, or Seed on one Seed built.
type route struct {
	name  string
	setup func(t *testing.T) fixture
	opts  func(f fixture) Options
	call  func(t *testing.T, f fixture, opts Options) Result
}

func ensureCall(t *testing.T, f fixture, opts Options) Result {
	t.Helper()
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 {
		t.Fatalf("EnsureHealthy returned %d results, want 1: %+v", len(res), res)
	}
	return res[0]
}

func seedCall(t *testing.T, f fixture, opts Options) Result {
	t.Helper()
	return Seed(context.Background(), opts, f.repo())
}

var (
	ensureRoute = route{"EnsureHealthy", func(t *testing.T) fixture { return built(t) }, fixture.applyOpts, ensureCall}
	seedRoute   = route{"Seed", seeded, fixture.seedOpts, seedCall}
	routes      = []route{ensureRoute, seedRoute}
)

// refreshing is opts with Refresh set.
func refreshing(opts Options) Options {
	opts.Refresh = true
	return opts
}

// moveCtime chmods the linked store's one pack so its ctime moves while its
// bytes stay, which makes the next check run verify-pack on it.
func moveCtime(t *testing.T, f fixture) string {
	t.Helper()
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Packs) != 1 {
		t.Fatalf("the manifest lists %d packs, want 1", len(st.Packs))
	}
	pack := filepath.Join(f.mdir(), st.Store, "objects", "pack", st.Packs[0].Name)
	time.Sleep(20 * time.Millisecond)
	makeWritable(t, pack)
	if now, err := describePack(pack); err != nil || now.CtimeNs == st.Packs[0].CtimeNs {
		t.Fatalf("the chmod did not move the pack's ctime (%v); the case proves nothing", err)
	}
	return pack
}

// gitDirArg is the argument a git process gets to run in dir.
func gitDirArg(dir string) string { return "--git-dir=" + dir }

func TestHostile_EmptyHooksIsMadeOnlyWhenAMirrorIsChecked(t *testing.T) {
	requireEmptyHooks := func(t *testing.T, home string) {
		t.Helper()
		dir := emptyHooksDir(home)
		requireMode(t, dir, 0o700, true)
		info, _ := os.Lstat(dir)
		if !ownedByUser(info) {
			t.Fatalf("%s is not owned by the user", dir)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Fatalf("%s holds %d entries (%v), want none", dir, len(entries), err)
		}
	}

	t.Run("the first Seed", func(t *testing.T) {
		f := newFixture(t)
		requireRestored(t, "Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
		requireEmptyHooks(t, f.home)
	})
	t.Run("the first EnsureHealthy of a borrowed mirror", func(t *testing.T) {
		f := built(t)
		requireEmptyHooks(t, f.home)
	})
	t.Run("an EnsureHealthy that keeps no repo", func(t *testing.T) {
		f := newFixture(t)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 0 {
			t.Fatalf("EnsureHealthy with no borrower = %+v, want no result", res)
		}
		if _, err := os.Lstat(filepath.Join(f.home, ".niwa")); !os.IsNotExist(err) {
			t.Fatalf("~/.niwa exists after an EnsureHealthy that kept no repo (%v)", err)
		}
	})
	t.Run("a refused Seed", func(t *testing.T) {
		f := newFixture(t)
		for _, repo := range []Repo{
			{ID: "", URL: f.repo().URL},
			{ID: ".meta", URL: f.repo().URL},
			{ID: "../app", URL: f.repo().URL},
			{ID: strings.Repeat("a", 101), URL: f.repo().URL},
			{ID: "app", URL: ""},
		} {
			if res := Seed(context.Background(), f.seedOpts(), repo); res.Outcome != OutcomeRestoreFailed {
				t.Fatalf("Seed(%q, %q) = %+v, want restore-failed", repo.ID, repo.URL, res)
			}
			if _, err := os.Lstat(emptyHooksDir(f.home)); !os.IsNotExist(err) {
				t.Fatalf("a refused Seed of %q made empty-hooks (%v)", repo.ID, err)
			}
		}
		emptyDir(t, f.home)
	})
}

// requireProcs fails unless every process carries the fixed -c pairs and
// GIT_TERMINAL_PROMPT=0, every process but a clone or fetch also carries the
// local-read variables and the empty helper pairs, a clone or fetch carries
// none of them, and every subcommand in want ran at least once.
func requireProcs(t *testing.T, home string, procs []gitProc, want ...string) {
	t.Helper()
	if len(procs) == 0 {
		t.Fatal("no git process was recorded")
	}
	base := []string{
		"core.hooksPath=" + emptyHooksDir(home),
		"core.fsmonitor=false",
		"protocol.ext.allow=never",
		"maintenance.auto=false",
		"gc.auto=0",
	}
	localEnv := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_OPTIONAL_LOCKS=0"}
	emptyHelpers := []string{"credential.helper=", "core.sshCommand="}
	seen := map[string]bool{}
	for _, p := range procs {
		sub := subcommand(p.Args)
		seen[sub] = true
		for _, kv := range base {
			if !slices.Contains(p.Config, kv) {
				t.Fatalf("git %q ran with -c %q, want %s among them", p.Args, p.Config, kv)
			}
		}
		if !slices.Contains(p.Env, "GIT_TERMINAL_PROMPT=0") {
			t.Fatalf("git %q ran with %q added, want GIT_TERMINAL_PROMPT=0", p.Args, p.Env)
		}
		network := sub == "clone" || sub == "fetch"
		for _, kv := range localEnv {
			name, _, _ := strings.Cut(kv, "=")
			has := slices.ContainsFunc(p.Env, func(e string) bool { return strings.HasPrefix(e, name+"=") })
			if network && has {
				t.Fatalf("git %s %q carries %s, which a network call mustn't", sub, p.Args, name)
			}
			if !network && !slices.Contains(p.Env, kv) {
				t.Fatalf("git %s %q ran with %q added, want %s", sub, p.Args, p.Env, kv)
			}
		}
		for _, kv := range emptyHelpers {
			if network == slices.Contains(p.Config, kv) {
				t.Fatalf("git %s %q ran with -c %q: %s must ride every local call and no network one", sub, p.Args, p.Config, kv)
			}
		}
	}
	for _, sub := range want {
		if !seen[sub] {
			t.Fatalf("no git %s was recorded among %d processes", sub, len(procs))
		}
	}
}

// requireSubsIn fails unless each subcommand in want ran with --git-dir=dir.
func requireSubsIn(t *testing.T, procs []gitProc, dir string, want ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range procs {
		if slices.Contains(p.Args, gitDirArg(dir)) {
			seen[subcommand(p.Args)] = true
		}
	}
	for _, sub := range want {
		if !seen[sub] {
			t.Fatalf("no git %s ran in %s", sub, dir)
		}
	}
}

func TestHostile_EveryGitProcessCarriesTheFixedConfigAndEnvironment(t *testing.T) {
	t.Run("EnsureHealthy healthy check", func(t *testing.T) {
		f := built(t)
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "rev-parse")
	})
	t.Run("EnsureHealthy moved ctime", func(t *testing.T) {
		f := built(t)
		moveCtime(t, f)
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "verify-pack", "rev-parse")
	})
	t.Run("EnsureHealthy restore by clone with salvage and pin", func(t *testing.T) {
		f := built(t)
		old := f.store(t)
		_, original := goneCommit(t, f)
		info, err := os.Stat(original)
		if err != nil {
			t.Fatal(err)
		}
		makeWritable(t, original)
		if err := os.Truncate(original, info.Size()/2); err != nil {
			t.Fatal(err)
		}
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultPackChanged, OutcomeRestored)
		all := procs()
		requireProcs(t, f.home, all, "clone", "verify-pack", "for-each-ref", "cat-file", "rev-list", "update-ref")
		requireSubsIn(t, all, old, "for-each-ref")
		if len(keptRefs(t, f)) == 0 {
			t.Fatal("the restore pinned nothing; the case didn't reach pinKept's writes")
		}
	})
	t.Run("EnsureHealthy relink whose probe walks a borrower", func(t *testing.T) {
		f := built(t)
		borrower := realBorrower(t, f)
		if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
			t.Fatal(err)
		}
		opts := f.applyOpts()
		opts.Instances = []string{borrower}
		procs := recordGitProcs(t)
		res := ensureCall(t, f, opts)
		requireOutcome(t, res, FaultMissing, OutcomeRestored)
		all := procs()
		requireProcs(t, f.home, all, "rev-parse", "for-each-ref", "rev-list")
		requireSubsIn(t, all, filepath.Join(borrower, ".git"), "for-each-ref", "rev-list")
	})
	t.Run("EnsureHealthy recovery by id", func(t *testing.T) {
		f := newLostFixture(t, lostSetup{})
		f.dropTopic(t, f.remote, true)
		f.deleteMirror(t)
		procs := recordGitProcs(t)
		res := f.ensure(t, f.opts())
		requireOutcome(t, res, FaultMissing, OutcomeRestored)
		if len(res.Unrecoverable) != 0 {
			t.Fatalf("the recovery left %v unrecoverable", res.Unrecoverable)
		}
		all := procs()
		requireProcs(t, f.home, all, "clone", "fetch", "for-each-ref", "rev-list", "cat-file", "update-ref")
		requireSubsIn(t, all, filepath.Join(f.a, ".git"), "for-each-ref", "rev-list")
	})
	t.Run("EnsureHealthy recovery that reports a lost branch", func(t *testing.T) {
		f := newLostFixture(t, lostSetup{})
		f.dropTopic(t, f.remote, false)
		f.deleteMirror(t)
		procs := recordGitProcs(t)
		res := f.ensure(t, f.opts())
		requireOutcome(t, res, FaultMissing, OutcomeRestored)
		if len(res.Unrecoverable) == 0 {
			t.Fatal("nothing was reported lost; the case didn't reach the lost files")
		}
		all := procs()
		requireProcs(t, f.home, all, "clone", "fetch", "for-each-ref", "rev-list", "cat-file", "ls-tree")
		requireSubsIn(t, all, filepath.Join(f.a, ".git"), "for-each-ref", "rev-list", "cat-file", "ls-tree")
	})
	t.Run("EnsureHealthy refresh with its deep verify due", func(t *testing.T) {
		f := built(t)
		editState(t, f, func(st *state) { st.LastDeepVerify = stateTime(time.Now().Add(-25 * time.Hour)) })
		pushCommit(t, f, "main", "second")
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, f.refreshOpts()), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "rev-parse", "verify-pack", "fetch")
	})

	t.Run("Seed first build", func(t *testing.T) {
		f := newFixture(t)
		procs := recordGitProcs(t)
		requireRestored(t, "Seed", seedCall(t, f, f.seedOpts()))
		requireProcs(t, f.home, procs(), "clone")
	})
	t.Run("Seed healthy check", func(t *testing.T) {
		f := seeded(t)
		procs := recordGitProcs(t)
		requireOutcome(t, seedCall(t, f, f.seedOpts()), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "rev-parse")
	})
	t.Run("Seed moved ctime", func(t *testing.T) {
		f := seeded(t)
		moveCtime(t, f)
		procs := recordGitProcs(t)
		requireOutcome(t, seedCall(t, f, f.seedOpts()), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "verify-pack", "rev-parse")
	})
	t.Run("Seed relink whose probe walks a borrower", func(t *testing.T) {
		f := seeded(t)
		borrower := realBorrower(t, f)
		if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
			t.Fatal(err)
		}
		opts := f.seedOpts()
		opts.Instances = []string{borrower}
		procs := recordGitProcs(t)
		requireOutcome(t, seedCall(t, f, opts), FaultMissing, OutcomeRestored)
		all := procs()
		requireProcs(t, f.home, all, "rev-parse", "for-each-ref", "rev-list")
		requireSubsIn(t, all, filepath.Join(borrower, ".git"), "for-each-ref", "rev-list")
	})
	t.Run("Seed refresh", func(t *testing.T) {
		f := seeded(t)
		pushCommit(t, f, "main", "second")
		procs := recordGitProcs(t)
		requireOutcome(t, seedCall(t, f, refreshing(f.seedOpts())), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "rev-parse", "fetch")
	})
}

// hostileGlobalConfig points HOME and XDG_CONFIG_HOME at a directory whose
// .gitconfig sends trace2 events to one marker and names as core.fsmonitor a
// script that writes another. git reads trace2.* only from system and global
// config, so the first marker shows which calls read the user's config.
func hostileGlobalConfig(t *testing.T) (trace, fsmonitor string) {
	t.Helper()
	home := t.TempDir()
	marks := t.TempDir()
	trace = filepath.Join(marks, "trace2-events")
	fsmonitor = filepath.Join(marks, "fsmonitor-ran")
	script := markerScript(t, filepath.Join(t.TempDir(), "fsmonitor.sh"), fsmonitor)
	writeFile(t, filepath.Join(home, ".gitconfig"),
		"[trace2]\n\teventTarget = "+trace+"\n[core]\n\tfsmonitor = "+script+"\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	return trace, fsmonitor
}

func TestHostile_AGlobalConfigReachesOnlyTheNetworkCalls(t *testing.T) {
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			f := r.setup(t)
			borrower := realBorrower(t, f)
			opts := r.opts(f)
			opts.Instances = []string{borrower}
			trace, fsmonitor := hostileGlobalConfig(t)

			requireOutcome(t, r.call(t, f, opts), FaultNone, OutcomeHealthy)
			moveCtime(t, f)
			requireOutcome(t, r.call(t, f, opts), FaultNone, OutcomeHealthy)
			if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
				t.Fatal(err)
			}
			res := r.call(t, f, opts)
			requireOutcome(t, res, FaultMissing, OutcomeRestored)
			if len(res.Unrecoverable) != 0 {
				t.Fatalf("the relink left %v unrecoverable", res.Unrecoverable)
			}
			requireNoMarker(t, trace, fsmonitor)

			commit := pushCommit(t, f, "main", "traced")
			requireOutcome(t, r.call(t, f, refreshing(opts)), FaultNone, OutcomeHealthy)
			if !f.storeHas(t, commit) {
				t.Fatalf("the store lacks the refreshed commit %s", commit)
			}
			if data, err := os.ReadFile(trace); err != nil || len(data) == 0 {
				t.Fatalf("the refresh's fetch wrote no trace2 event (%v): git didn't read the global config, so the case proves nothing", err)
			}
			requireNoMarker(t, fsmonitor)
		})
	}
}

func TestHostile_AHookPlantedInTheStoreNeverRuns(t *testing.T) {
	// The control: this git runs a reference-transaction hook on a ref
	// update, so its absence below is the store's hook not running.
	work := filepath.Join(t.TempDir(), "work")
	testGit(t, filepath.Dir(work), "init", "--quiet", work)
	control := filepath.Join(t.TempDir(), "control-ran")
	markerScript(t, filepath.Join(work, ".git", "hooks", "reference-transaction"), control)
	writeFile(t, filepath.Join(work, "a.txt"), "a\n")
	testGit(t, work, "add", "a.txt")
	testGit(t, work, "commit", "--quiet", "-m", "a")
	if _, err := os.Lstat(control); err != nil {
		t.Fatalf("git ran no reference-transaction hook on a commit (%v); the case proves nothing", err)
	}

	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			f := r.setup(t)
			marker := filepath.Join(t.TempDir(), "hook-ran")
			plantStoreHook(t, f.store(t), marker)
			commit := pushCommit(t, f, "main", "second")
			logged := len(f.logLines(t))

			res := r.call(t, f, refreshing(r.opts(f)))
			requireOutcome(t, res, FaultNone, OutcomeHealthy)
			requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"}, [3]string{"refresh", "", "healthy"})
			if !f.storeHas(t, commit) {
				t.Fatalf("the store lacks the refreshed commit %s", commit)
			}
			requireNoMarker(t, marker)
		})
	}
}

func TestHostile_AnEntryInEmptyHooksStopsEveryGitCall(t *testing.T) {
	for _, entry := range []string{"file", "link"} {
		for _, r := range routes {
			t.Run(entry+" via "+r.name, func(t *testing.T) {
				f := r.setup(t)
				marker := filepath.Join(t.TempDir(), "hook-ran")
				plantStoreHook(t, f.store(t), marker)
				commit := pushCommit(t, f, "main", "second")

				hooks := emptyHooksDir(f.home)
				outside := t.TempDir()
				markerScript(t, filepath.Join(outside, "reference-transaction"), marker)
				var planted string
				if entry == "file" {
					planted = markerScript(t, filepath.Join(hooks, "reference-transaction"), marker)
				} else {
					planted = filepath.Join(hooks, "outside")
					if err := os.Symlink(outside, planted); err != nil {
						t.Fatal(err)
					}
				}
				plantedFiles := treeFiles(t, hooks)
				outsideFiles := treeFiles(t, outside)
				target := f.linkTarget(t)
				state := stateBytes(t, f)

				calls := recordGit(t)
				opts := refreshing(r.opts(f))
				res := r.call(t, f, opts)
				if got := calls(); len(got) != 0 {
					t.Fatalf("git ran with an entry in empty-hooks: %q", got)
				}
				if res.Fault != FaultRepoUnreadable || res.Outcome != OutcomeRestoreFailed || res.Err == nil || !strings.Contains(res.Err.Error(), "empty-hooks") {
					t.Fatalf("result = %+v, want repo-unreadable/restore-failed with an error naming empty-hooks", res)
				}
				if !maps.Equal(treeFiles(t, hooks), plantedFiles) {
					t.Fatal("the entry planted in empty-hooks changed")
				}
				if !maps.Equal(treeFiles(t, outside), outsideFiles) {
					t.Fatal("the outside directory changed")
				}
				requireNoMarker(t, marker)
				if f.linkTarget(t) != target || !bytes.Equal(stateBytes(t, f), state) {
					t.Fatal("the link or the state file changed")
				}

				if err := os.Remove(planted); err != nil {
					t.Fatal(err)
				}
				requireOutcome(t, r.call(t, f, opts), FaultNone, OutcomeHealthy)
				if !f.storeHas(t, commit) {
					t.Fatalf("the store lacks the refreshed commit %s", commit)
				}
				requireNoMarker(t, marker)
			})
		}
	}

	t.Run("empty-hooks is a link", func(t *testing.T) {
		outside := t.TempDir()
		marker := filepath.Join(t.TempDir(), "hook-ran")
		markerScript(t, filepath.Join(outside, "reference-transaction"), marker)
		outsideFiles := treeFiles(t, outside)
		linkHooks := func(t *testing.T, home string) string {
			t.Helper()
			hooks := emptyHooksDir(home)
			if err := os.RemoveAll(hooks); err != nil {
				t.Fatal(err)
			}
			mkdir(t, filepath.Dir(hooks))
			if err := os.Symlink(outside, hooks); err != nil {
				t.Fatal(err)
			}
			return hooks
		}

		f := built(t)
		hooks := linkHooks(t, f.home)
		target := f.linkTarget(t)
		storeFiles := treeFiles(t, f.store(t))
		state := stateBytes(t, f)
		logged := len(f.logLines(t))
		calls := recordGit(t)
		res := ensureCall(t, f, f.applyOpts())
		if got := calls(); len(got) != 0 {
			t.Fatalf("git ran with empty-hooks a link: %q", got)
		}
		if res.Fault != FaultRepoUnreadable || res.Outcome != OutcomeRestoreFailed || res.Err == nil || !strings.Contains(res.Err.Error(), "empty-hooks") {
			t.Fatalf("result = %+v, want repo-unreadable/restore-failed with an error naming empty-hooks", res)
		}
		requireEvents(t, newLines(t, f, logged),
			[3]string{"check", string(FaultRepoUnreadable), string(OutcomeRestoreFailed)},
			[3]string{"restore", string(FaultRepoUnreadable), string(OutcomeRestoreFailed)})
		if f.linkTarget(t) != target || !maps.Equal(treeFiles(t, f.store(t)), storeFiles) || !bytes.Equal(stateBytes(t, f), state) {
			t.Fatal("the link, the store or the state file changed")
		}
		if !maps.Equal(treeFiles(t, outside), outsideFiles) {
			t.Fatal("the outside directory changed")
		}
		requireLink(t, hooks)
		requireNoMarker(t, marker)

		g := newFixture(t)
		gHooks := linkHooks(t, g.home)
		gres := Seed(context.Background(), g.seedOpts(), g.repo())
		if gres.Fault != FaultMissing || gres.Outcome != OutcomeRestoreFailed || gres.Err == nil {
			t.Fatalf("Seed = %+v, want missing/restore-failed", gres)
		}
		requireNothingBuilt(t, g)
		if got := calls(); len(got) != 0 {
			t.Fatalf("git ran with empty-hooks a link: %q", got)
		}
		requireNoMarker(t, marker)
		if !maps.Equal(treeFiles(t, outside), outsideFiles) {
			t.Fatal("the outside directory changed")
		}

		if err := os.Remove(hooks); err != nil {
			t.Fatal(err)
		}
		requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultNone, OutcomeHealthy)
		if err := os.Remove(gHooks); err != nil {
			t.Fatal(err)
		}
		requireRestored(t, "Seed with the link removed", Seed(context.Background(), g.seedOpts(), g.repo()))
		requireNoMarker(t, marker)
	})
}

func TestHostile_LinksPlantedInTheStoreAreRebuiltNotFollowed(t *testing.T) {
	cases := []struct {
		name  string
		rel   func(t *testing.T, f fixture) string
		fault Fault
	}{
		{"config", func(*testing.T, fixture) string { return "config" }, FaultRepoUnreadable},
		{"objects", func(*testing.T, fixture) string { return "objects" }, FaultObjectsEmpty},
		{"packed-refs", func(t *testing.T, f fixture) string {
			store := f.store(t)
			if _, err := os.Lstat(filepath.Join(store, "packed-refs")); err != nil {
				testGit(t, store, "--git-dir", store, "pack-refs", "--all")
			}
			return "packed-refs"
		}, FaultRepoUnreadable},
		{"pack", func(t *testing.T, f fixture) string {
			packs := f.packs(t)
			if len(packs) != 1 {
				t.Fatalf("the store holds %d packs, want 1", len(packs))
			}
			rel, _ := filepath.Rel(f.store(t), packs[0])
			return rel
		}, FaultPackChanged},
	}
	for _, c := range cases {
		for _, r := range routes {
			t.Run(c.name+" via "+r.name, func(t *testing.T) {
				f := r.setup(t)
				old := f.linkTarget(t)
				oldStore := filepath.Join(f.mdir(), old)
				rel := c.rel(t, f)
				planted := filepath.Join(oldStore, rel)
				outside := linkOutside(t, planted)
				outsideFiles := treeFiles(t, outside)
				logged := len(f.logLines(t))

				calls := recordGit(t)
				res := r.call(t, f, r.opts(f))
				requireOutcome(t, res, c.fault, OutcomeRestored)
				requireEvents(t, newLines(t, f, logged),
					[3]string{"check", string(c.fault), "restored"},
					[3]string{"restore", string(c.fault), "restored"})
				if now := f.linkTarget(t); now == old || !validLinkTarget("app", now) {
					t.Fatalf("link = %q, want a new store, not %q", now, old)
				}
				if got, want := configList(t, f.store(t)), wantConfig(f.repo().URL); !slices.Equal(got, want) {
					t.Fatalf("config =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
				}
				requireManifestMatches(t, f)
				if _, err := os.Lstat(oldStore); !os.IsNotExist(err) {
					t.Fatalf("the old store is still there (%v)", err)
				}
				if !maps.Equal(treeFiles(t, outside), outsideFiles) {
					t.Fatalf("the outside copy of %s changed", rel)
				}
				for _, args := range calls() {
					if c.name == "pack" {
						if subcommand(args) == "verify-pack" && slices.Contains(args, idxOf(planted)) {
							t.Fatalf("git verify-pack ran on the linked pack's index: %q", args)
						}
					} else if slices.Contains(args, gitDirArg(oldStore)) {
						t.Fatalf("git ran in the old store with %s linked: %q", rel, args)
					}
				}
			})
		}
	}
}

func TestHostile_ALinkedStateFileIsReplaced(t *testing.T) {
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			f := r.setup(t)
			sp := statePath(f.mdir(), "app")
			outside := linkOutside(t, sp)
			outsideBytes, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			logged := len(f.logLines(t))

			res := r.call(t, f, r.opts(f))
			requireOutcome(t, res, FaultMissing, OutcomeRestored)
			requireEvents(t, newLines(t, f, logged),
				[3]string{"check", "missing", "restored"},
				[3]string{"restore", "missing", "restored"})
			requireMode(t, sp, 0o600, false)
			st, err := readState(f.mdir(), "app")
			if err != nil || st.Store != f.linkTarget(t) {
				t.Fatalf("state = %+v (%v), want it to name the link's target %q", st, err, f.linkTarget(t))
			}
			if now, err := os.ReadFile(outside); err != nil || !bytes.Equal(now, outsideBytes) {
				t.Fatalf("the outside copy of the state file changed (%v)", err)
			}
		})
	}
}

func TestHostile_ALinkedOrFifoLogIsNeverWrittenThrough(t *testing.T) {
	for _, kind := range []string{"link", "fifo"} {
		for _, r := range routes {
			t.Run(kind+" via "+r.name, func(t *testing.T) {
				f := seeded(t)
				lp := logPath(f.mdir())
				if err := os.Remove(lp); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "outside.log")
				writeFile(t, outside, "outside\n")
				wantType := os.ModeSymlink
				if kind == "link" {
					if err := os.Symlink(outside, lp); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := syscall.Mkfifo(lp, 0o600); err != nil {
						t.Fatal(err)
					}
					wantType = os.ModeNamedPipe
				}

				res := r.call(t, f, r.opts(f))
				if res.Fault != FaultNone || res.Outcome != OutcomeHealthy || res.Err != nil || res.LogErr == nil {
					t.Fatalf("result = %+v, want healthy with no error and LogErr set", res)
				}
				if data, err := os.ReadFile(outside); err != nil || string(data) != "outside\n" {
					t.Fatalf("the outside file changed: %q (%v)", data, err)
				}
				if info, err := os.Lstat(lp); err != nil || info.Mode().Type() != wantType {
					t.Fatalf("the planted %s at the log's path is gone: %v (%v)", kind, info, err)
				}

				if err := os.Remove(lp); err != nil {
					t.Fatal(err)
				}
				res = r.call(t, f, r.opts(f))
				if res.Outcome != OutcomeHealthy || res.LogErr != nil {
					t.Fatalf("result with the %s removed = %+v, want healthy with the log written", kind, res)
				}
				lines := rawLogLines(t, lp)
				if len(lines) == 0 || decodeLine(t, lines[len(lines)-1])["event"] != "check" {
					t.Fatalf("the log holds %q, want its last line a check", lines)
				}
			})
		}
	}

	for _, r := range routes {
		t.Run("rotation over a linked mirror.log.1 via "+r.name, func(t *testing.T) {
			f := seeded(t)
			lp := logPath(f.mdir())
			outside := filepath.Join(t.TempDir(), "outside.1")
			writeFile(t, outside, "outside\n")
			if err := os.Chmod(outside, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, lp+".1"); err != nil {
				t.Fatal(err)
			}
			filled := fillLog(t, lp, logMax)

			res := r.call(t, f, r.opts(f))
			if res.Outcome != OutcomeHealthy || res.LogErr != nil {
				t.Fatalf("result = %+v, want healthy with the log written", res)
			}
			requireMode(t, lp+".1", 0o600, false)
			if data, err := os.ReadFile(lp + ".1"); err != nil || !bytes.Equal(data, filled) {
				t.Fatalf("mirror.log.1 doesn't hold the full log's bytes (%v)", err)
			}
			lines := rawLogLines(t, lp)
			if len(lines) != 1 || decodeLine(t, lines[0])["event"] != "check" {
				t.Fatalf("the new mirror.log holds %q, want one check line", lines)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "outside\n" {
				t.Fatalf("the outside file changed: %q (%v)", data, err)
			}
			requireMode(t, outside, 0o644, false)
		})
	}
}

func TestHostile_LinkedLayoutDirectoriesAreRefused(t *testing.T) {
	cases := []struct {
		name string
		dir  func(f fixture) string
	}{
		{".store", func(f fixture) string { return filepath.Join(f.mdir(), ".store") }},
		{".meta", func(f fixture) string { return metaDir(f.mdir()) }},
		{"mirrors", func(f fixture) string { return f.mdir() }},
		{"key directory", func(f fixture) string { return keyDir(f.home, f.root) }},
	}
	for _, c := range cases {
		for _, r := range routes {
			t.Run(c.name+" via "+r.name, func(t *testing.T) {
				f := r.setup(t)
				dir := c.dir(f)
				logged := len(f.logLines(t))
				outside := linkOutside(t, dir)
				outsideFiles := treeFiles(t, outside)

				calls := recordGit(t)
				res := r.call(t, f, r.opts(f))
				if res.Fault != FaultMissing || res.Outcome != OutcomeRestoreFailed || res.Err == nil || !strings.Contains(res.Err.Error(), dir) {
					t.Fatalf("result = %+v, want missing/restore-failed with an error naming %s", res, dir)
				}
				if got := calls(); len(got) != 0 {
					t.Fatalf("git ran with %s linked: %q", c.name, got)
				}
				if c.name == ".store" {
					requireEvents(t, newLines(t, f, logged),
						[3]string{"check", "missing", "restore-failed"},
						[3]string{"restore", "missing", "restore-failed"})
				} else if res.LogErr == nil {
					t.Fatalf("result = %+v, want LogErr set with %s linked", res, c.name)
				}
				if !maps.Equal(treeFiles(t, outside), outsideFiles) {
					t.Fatalf("the outside copy of %s changed", c.name)
				}
				requireLink(t, dir)
			})
		}
	}

	for _, r := range routes {
		t.Run(".tmp via "+r.name, func(t *testing.T) {
			f := r.setup(t)
			tmp := filepath.Join(f.mdir(), ".tmp")
			if err := os.Remove(tmp); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, "app-1234abcd", "x"), "x")
			if err := os.Symlink(outside, tmp); err != nil {
				t.Fatal(err)
			}
			outsideFiles := treeFiles(t, outside)

			requireOutcome(t, r.call(t, f, r.opts(f)), FaultNone, OutcomeHealthy)
			if !maps.Equal(treeFiles(t, outside), outsideFiles) {
				t.Fatal("a healthy check changed the directory .tmp links to")
			}

			if err := os.Remove(f.packs(t)[0]); err != nil {
				t.Fatal(err)
			}
			res := r.call(t, f, r.opts(f))
			if res.Fault != FaultPackMissing || res.Outcome != OutcomeRestoreFailed || res.Err == nil || !strings.Contains(res.Err.Error(), ".tmp") {
				t.Fatalf("result = %+v, want pack-missing/restore-failed with an error naming .tmp", res)
			}
			if !maps.Equal(treeFiles(t, outside), outsideFiles) {
				t.Fatal("the restore changed the directory .tmp links to")
			}
			requireLink(t, tmp)
		})
	}
}

func TestHostile_LoosenedModesAreTightened(t *testing.T) {
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			f := seeded(t)
			mdir := f.mdir()
			one := logPath(mdir) + ".1"
			writeFile(t, one, "old\n")
			// The workspace key directory is loosened too but isn't in dirs:
			// it's shared with the plugin store, so the mirror leaves its
			// mode as it is.
			key := keyDir(f.home, f.root)
			dirs := []string{
				mdir, metaDir(mdir), filepath.Join(mdir, ".store"), filepath.Join(mdir, ".tmp"),
				filepath.Join(f.home, ".niwa", "locks"), emptyHooksDir(f.home),
			}
			files := []string{statePath(mdir, "app"), logPath(mdir), one, LockPath(f.home, f.root, "app")}
			if err := os.Chmod(key, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, d := range dirs {
				if err := os.Chmod(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range files {
				if err := os.Chmod(file, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			calls := recordGit(t)
			requireOutcome(t, r.call(t, f, r.opts(f)), FaultNone, OutcomeHealthy)
			requireMode(t, key, 0o755, true)
			for _, d := range dirs {
				requireMode(t, d, 0o700, true)
			}
			for _, file := range files {
				requireMode(t, file, 0o600, false)
			}
			revParse := []string{gitDirArg(f.store(t)), "rev-parse", "--verify", "--quiet", "HEAD^{commit}"}
			if got := calls(); len(got) != 1 || !slices.Equal(got[0], revParse) {
				t.Fatalf("the call ran git %q, want exactly %q", got, revParse)
			}
		})
	}

	t.Run("a fifo at mirror.log.1", func(t *testing.T) {
		for _, r := range routes {
			f := seeded(t)
			one := logPath(f.mdir()) + ".1"
			if err := syscall.Mkfifo(one, 0o644); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(one)
			if err != nil {
				t.Fatal(err)
			}
			requireOutcome(t, r.call(t, f, r.opts(f)), FaultNone, OutcomeHealthy)
			after, err := os.Lstat(one)
			if err != nil || after.Mode() != before.Mode() {
				t.Fatalf("%s: the fifo's mode went from %s to %v (%v)", r.name, before.Mode(), after, err)
			}
		}
	})

	t.Run("a restore that salvages from an old store", func(t *testing.T) {
		f := built(t)
		old := f.store(t)
		packs := f.packs(t)
		if len(packs) != 1 {
			t.Fatalf("the store holds %d packs, want 1", len(packs))
		}
		if err := os.Remove(packs[0]); err != nil {
			t.Fatal(err)
		}
		loose := testGitStdin(t, old, "only the old store holds this\n", "--git-dir", old, "hash-object", "-w", "--stdin")
		requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultPackMissing, OutcomeRestored)
		store := f.store(t)
		requireMode(t, filepath.Join(store, "config"), 0o600, false)
		entries, err := os.ReadDir(filepath.Join(store, "objects"))
		if err != nil {
			t.Fatal(err)
		}
		made := 0
		for _, e := range entries {
			if looseDirPattern.MatchString(e.Name()) {
				requireMode(t, filepath.Join(store, "objects", e.Name()), 0o700, true)
				made++
			}
		}
		if made == 0 || !f.storeHas(t, loose) {
			t.Fatalf("salvage made %d fan-out directories and the store has the loose object: %v; the case proves nothing", made, f.storeHas(t, loose))
		}
	})

	t.Run("a refresh", func(t *testing.T) {
		f := built(t)
		config := filepath.Join(f.store(t), "config")
		if err := os.Chmod(config, 0o644); err != nil {
			t.Fatal(err)
		}
		pushCommit(t, f, "main", "second")
		requireOutcome(t, ensureCall(t, f, f.refreshOpts()), FaultNone, OutcomeHealthy)
		requireMode(t, config, 0o600, false)
	})
}

// hostileKeyNames are the store config keys TestHostile_AHostileStoreConfig
// plants, in order.
var hostileKeyNames = []string{
	"core.fsmonitor", "core.sshCommand", "core.hooksPath", "include.path", "includeIf",
	"credential.helper", "uploadpack.packObjectsHook", "protocol.allow", "url insteadOf",
	"remote.origin.url", "fetch.prune",
}

// hostileKey returns the key and value planted for name. Every script it
// names writes a marker into marks, and the include target sets
// core.fsmonitor and core.hooksPath to marker items too.
func hostileKey(t *testing.T, name, marks string) (key, value string) {
	t.Helper()
	scripts := t.TempDir()
	script := func(n string) string {
		return markerScript(t, filepath.Join(scripts, n+".sh"), filepath.Join(marks, n))
	}
	hooks := filepath.Join(scripts, "hooks")
	markerScript(t, filepath.Join(hooks, "reference-transaction"), filepath.Join(marks, "hooks-path-hook"))
	include := filepath.Join(scripts, "included.config")
	writeFile(t, include, "[core]\n\tfsmonitor = "+script("included-fsmonitor")+"\n\thooksPath = "+hooks+"\n")
	switch name {
	case "core.fsmonitor":
		return name, script("fsmonitor")
	case "core.sshCommand":
		return name, script("ssh-command")
	case "core.hooksPath":
		return name, hooks
	case "include.path":
		return name, include
	case "includeIf":
		return "includeIf.gitdir:/.path", include
	case "credential.helper":
		return name, script("credential-helper")
	case "uploadpack.packObjectsHook":
		return name, script("pack-objects-hook")
	case "protocol.allow":
		return name, "always"
	case "url insteadOf":
		return "url.file:///nowhere/.insteadOf", "file://"
	case "remote.origin.url":
		return name, filepath.Join(t.TempDir(), "other.git")
	case "fetch.prune":
		return name, "true"
	}
	t.Fatalf("no hostile key %q", name)
	return "", ""
}

func TestHostile_AHostileStoreConfigIsInertAndRewritten(t *testing.T) {
	for _, name := range hostileKeyNames {
		for _, r := range routes {
			t.Run(name+" via "+r.name, func(t *testing.T) {
				f := r.setup(t)
				marks := t.TempDir()
				key, value := hostileKey(t, name, marks)
				store := f.store(t)
				cfg := filepath.Join(store, "config")
				testGit(t, store, "config", "--file", cfg, key, value)
				written, err := os.ReadFile(cfg)
				if err != nil {
					t.Fatal(err)
				}
				target := f.linkTarget(t)
				state := stateBytes(t, f)
				logged := len(f.logLines(t))

				calls := recordGit(t)
				requireOutcome(t, r.call(t, f, r.opts(f)), FaultNone, OutcomeHealthy)
				revParse := []string{gitDirArg(store), "rev-parse", "--verify", "--quiet", "HEAD^{commit}"}
				if got := calls(); len(got) != 1 || !slices.Equal(got[0], revParse) {
					t.Fatalf("the check ran git %q, want exactly %q", got, revParse)
				}
				if now, err := os.ReadFile(cfg); err != nil || !bytes.Equal(now, written) {
					t.Fatalf("the check changed the store's config (%v)", err)
				}
				if f.linkTarget(t) != target || !bytes.Equal(stateBytes(t, f), state) {
					t.Fatal("the check changed the link or the state file")
				}
				requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"})
				requireNoMarks(t, marks)

				commit := pushCommit(t, f, "main", "second")
				requireOutcome(t, r.call(t, f, refreshing(r.opts(f))), FaultNone, OutcomeHealthy)
				if !f.storeHas(t, commit) {
					t.Fatalf("the store lacks the refreshed commit %s", commit)
				}
				if got, want := configList(t, store), wantConfig(f.repo().URL); !slices.Equal(got, want) {
					t.Fatalf("config after the refresh =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
				}
				if left, _ := filepath.Glob(filepath.Join(store, ".niwa-config-*")); len(left) != 0 {
					t.Fatalf("the refresh left %v", left)
				}
				requireNoMarks(t, marks)
			})
		}
	}

	for _, r := range routes {
		t.Run("repositoryformatversion 99 via "+r.name, func(t *testing.T) {
			f := r.setup(t)
			marks := t.TempDir()
			store := f.store(t)
			// A hook in the store is what would run if git ran it.
			plantStoreHook(t, store, filepath.Join(marks, "hook-ran"))
			testGit(t, store, "config", "--file", filepath.Join(store, "config"), "core.repositoryformatversion", "99")
			old := f.linkTarget(t)

			requireOutcome(t, r.call(t, f, r.opts(f)), FaultRepoUnreadable, OutcomeRestored)
			if now := f.linkTarget(t); now == old || !validLinkTarget("app", now) {
				t.Fatalf("link = %q, want a new store, not %q", now, old)
			}
			if got, want := configList(t, f.store(t)), wantConfig(f.repo().URL); !slices.Equal(got, want) {
				t.Fatalf("config =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			requireNoMarks(t, marks)
		})
	}
}

// relinkFixture is a lostFixture whose recorded store lacks topic and whose
// stable link alone is gone, with a config planted in the recorded store
// that would turn the fetch by id away: the remote's URL rewritten to an
// absent path and the file protocol refused. It returns the fixture and the
// recorded store's link target.
func relinkFixture(t *testing.T) (lostFixture, string) {
	t.Helper()
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, true)
	f.deleteMirror(t)
	// The first restore builds a store by clone, which the hidden ref keeps
	// topic out of, and its recovery fetch is held back, so the store it
	// records lacks topic and a later relink has something to fetch by id.
	var allow atomic.Bool
	setFetchHook(t, func(ctx context.Context, store, url string, ids []string) error {
		if !allow.Load() {
			return errors.New("held back by the test")
		}
		return fetchByID(ctx, store, url, ids)
	})
	if res := f.ensure(t, f.opts()); res.Outcome != OutcomeRestored {
		t.Fatalf("building the recorded store: %+v", res)
	}
	if f.storeHas(t, f.topic) {
		t.Fatal("the recorded store already holds topic; the case proves nothing")
	}
	allow.Store(true)

	recorded := f.linkTarget(t)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(f.mdir(), recorded)
	cfg := filepath.Join(store, "config")
	absent := "file://" + filepath.Join(t.TempDir(), "absent.git")
	testGit(t, store, "config", "--file", cfg, "url."+f.repo().URL+".insteadOf", absent)
	testGit(t, store, "config", "--file", cfg, "url."+absent+".insteadOf", f.repo().URL)
	testGit(t, store, "config", "--file", cfg, "protocol.file.allow", "never")
	// The control: under the planted config, git can't reach the remote.
	if out, err := gitTry(store, "--git-dir", store, "ls-remote", f.repo().URL); err == nil {
		t.Fatalf("ls-remote under the planted config reached the remote:\n%s\nthe case proves nothing", out)
	}
	return f, recorded
}

// markRelinkConfig records, through stageHook, how many git processes calls
// held when the relink reached its config rewrite, and fails the rewrite
// when fail is set. The mark is -1 until then.
func markRelinkConfig(t *testing.T, calls func() [][]string, fail bool) *int {
	t.Helper()
	mark := -1
	stageHook = func(s string) error {
		if s != stageRelinkConfig {
			return nil
		}
		mark = len(calls())
		if fail {
			return errors.New("injected failure at " + s)
		}
		return nil
	}
	t.Cleanup(func() { stageHook = nil })
	return &mark
}

func TestHostile_ARelinkRewritesAPlantedConfigBeforeItsFetch(t *testing.T) {
	relinkRoutes := []struct {
		name string
		call func(t *testing.T, f lostFixture) Result
	}{
		{"EnsureHealthy", func(t *testing.T, f lostFixture) Result { return f.ensure(t, f.opts()) }},
		{"Seed", func(t *testing.T, f lostFixture) Result { return Seed(context.Background(), f.opts(), f.repo()) }},
	}
	for _, r := range relinkRoutes {
		t.Run("rewritten via "+r.name, func(t *testing.T) {
			f, recorded := relinkFixture(t)
			calls := recordGit(t)
			mark := markRelinkConfig(t, calls, false)

			res := r.call(t, f)
			requireOutcome(t, res, FaultMissing, OutcomeRestored)
			if len(res.Unrecoverable) != 0 {
				t.Fatalf("the relink left %v unrecoverable", res.Unrecoverable)
			}
			if f.linkTarget(t) != recorded {
				t.Fatalf("link = %q, want the recorded store %q", f.linkTarget(t), recorded)
			}
			got := calls()
			if *mark < 0 {
				t.Fatal("the relink never reached its config rewrite")
			}
			fetches := 0
			for i, args := range got {
				switch subcommand(args) {
				case "clone":
					t.Fatalf("the relink cloned: %q", args)
				case "fetch":
					fetches++
					if i < *mark {
						t.Fatalf("git fetch ran before the config rewrite: %q", args)
					}
				}
			}
			if fetches == 0 {
				t.Fatal("no fetch by id ran; the case proves nothing")
			}
			if got, want := configList(t, f.store(t)), wantConfig(f.repo().URL); !slices.Equal(got, want) {
				t.Fatalf("config =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			if !f.storeHas(t, f.topic) {
				t.Fatalf("the store lacks the deleted commit %s", f.topic)
			}
		})

		// The rewrite fails. The remote still hides topic, so the new store's
		// recovery fetches it by id, into the new store through the stable
		// path; nothing runs in the recorded store.
		t.Run("failed rewrite via "+r.name, func(t *testing.T) {
			f, recorded := relinkFixture(t)
			recordedStore := filepath.Join(f.mdir(), recorded)
			calls := recordGit(t)
			mark := markRelinkConfig(t, calls, true)

			res := r.call(t, f)
			requireOutcome(t, res, FaultMissing, OutcomeRestored)
			if now := f.linkTarget(t); now == recorded || !validLinkTarget("app", now) {
				t.Fatalf("link = %q, want a new store, not the recorded %q", now, recorded)
			}
			got := calls()
			if *mark < 0 {
				t.Fatal("the relink never reached its config rewrite")
			}
			if !slices.ContainsFunc(got[*mark:], func(args []string) bool { return subcommand(args) == "clone" }) {
				t.Fatalf("no clone ran after the failed rewrite: %q", got[*mark:])
			}
			for _, args := range got[*mark:] {
				if slices.Contains(args, gitDirArg(recordedStore)) {
					t.Fatalf("git ran in the recorded store after its rewrite failed: %q", args)
				}
				if subcommand(args) == "fetch" && !slices.Contains(args, gitDirArg(StablePath(f.home, f.root, "app"))) {
					t.Fatalf("a fetch ran somewhere but the new store's stable path: %q", args)
				}
			}
			if !f.storeHas(t, f.topic) {
				t.Fatalf("the new store lacks the deleted commit %s", f.topic)
			}
		})

		// The same failure with the remote serving topic again, so the new
		// store's clone holds it: no fetch runs at all.
		t.Run("failed rewrite with the remote serving topic via "+r.name, func(t *testing.T) {
			f, recorded := relinkFixture(t)
			recordedStore := filepath.Join(f.mdir(), recorded)
			testGit(t, f.remote, "update-ref", "refs/heads/topic", f.topic)
			calls := recordGit(t)
			mark := markRelinkConfig(t, calls, true)

			res := r.call(t, f)
			requireOutcome(t, res, FaultMissing, OutcomeRestored)
			if len(res.Unrecoverable) != 0 {
				t.Fatalf("the restore left %v unrecoverable", res.Unrecoverable)
			}
			if now := f.linkTarget(t); now == recorded || !validLinkTarget("app", now) {
				t.Fatalf("link = %q, want a new store, not the recorded %q", now, recorded)
			}
			got := calls()
			if *mark < 0 {
				t.Fatal("the relink never reached its config rewrite")
			}
			if !slices.ContainsFunc(got[*mark:], func(args []string) bool { return subcommand(args) == "clone" }) {
				t.Fatalf("no clone ran after the failed rewrite: %q", got[*mark:])
			}
			for _, args := range got {
				if subcommand(args) == "fetch" {
					t.Fatalf("a fetch ran: %q", args)
				}
			}
			for _, args := range got[*mark:] {
				if slices.Contains(args, gitDirArg(recordedStore)) {
					t.Fatalf("git ran in the recorded store after its rewrite failed: %q", args)
				}
			}
			if !f.storeHas(t, f.topic) {
				t.Fatalf("the new store lacks topic %s", f.topic)
			}
		})
	}
}

func TestHostile_CleanTextIsCleanName(t *testing.T) {
	cases := map[string]string{
		"refs/heads/plain":                      "refs/heads/plain",
		"a\x1bb\nc":                             `a\x1bb\x0ac`,
		`back\slash`:                            `back\x5cslash`,
		"caf\xc3\xa9":                           `caf\xc3\xa9`,
		strings.Repeat("x", 300):                strings.Repeat("x", nameMax),
		strings.Repeat("x", nameMax-2) + "\x1b": strings.Repeat("x", nameMax-2),
	}
	for in, want := range cases {
		if got := CleanText(in); got != want || got != cleanName(in) {
			t.Errorf("CleanText(%q) = %q, cleanName = %q, want %q", in, got, cleanName(in), want)
		}
	}
}

func TestHostile_NoticeDoesNotPointAtALogThatWasNotWritten(t *testing.T) {
	stable := "/h/.niwa/w-0123/mirrors/app"
	log := "/h/.niwa/w-0123/mirrors/.meta/mirror.log"
	failed := " The mirror log at " + log + " could not be written."

	two := Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored,
		Unrecoverable: []LostRef{{Repo: "app"}, {Repo: "app"}}, LogErr: errors.New("open /x: denied")}
	got := two.Notice()
	if !strings.Contains(got, "Except 2 refs") || !strings.HasSuffix(got, failed) || strings.Contains(got, "names them") {
		t.Fatalf("Notice() with LogErr = %q, want the count, the unwritten log and no claim the log names them", got)
	}
	two.LogErr = nil
	want := "niwa mirror app: fault missing, result restored. It was rebuilt from the remote, and the clones that borrow from it read normally. Except 2 refs in those clones, which are unrecoverable: they were built on commits the remote no longer serves. The mirror log names them. Mirror log: " + log
	if got := two.Notice(); got != want {
		t.Fatalf("Notice() with a working log =\n%q\nwant\n%q", got, want)
	}

	one := Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored,
		Unrecoverable: []LostRef{{Repo: "app"}}, LogErr: errors.New("open /x: denied")}
	if got := one.Notice(); !strings.Contains(got, "Except 1 ref") || strings.Contains(got, "names it") || !strings.HasSuffix(got, failed) {
		t.Fatalf("Notice() for one ref with LogErr = %q", got)
	}
}
