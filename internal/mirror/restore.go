package mirror

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/testhook"
)

// The steps of a restore that can fail, named for stageHook.
const (
	stageClone  = "clone"  // cloning into .tmp/
	stageState  = "state"  // writing the state file under its temporary name
	stageStore  = "store"  // renaming the clone into .store/
	stageLink   = "link"   // making the .link-<rand> link
	stageSwap   = "swap"   // renaming that link over the stable path
	stageRecord = "record" // renaming the state file into place, after the swap
	stageStores = "stores" // writing the store record, before and after the swap

	stageRelinkConfig = "relink-config" // rewriting a recorded store's config before a relink
)

// stageHook, when a test sets it, runs before each step of a restore named
// above, and an error it returns fails that step as a real failure there
// would. The tests use it to prove each failure leaves the mirror as it was;
// it's nil outside them.
var stageHook func(stage string) error

// atStage runs stageHook for stage, when one is set.
func atStage(stage string) error {
	if stageHook == nil {
		return nil
	}
	return stageHook(stage)
}

// restore rebuilds a broken mirror and swaps it in. It runs under the
// mirror's lock and, once it holds it, clears what a crashed restore of the
// same repo left behind and checks again, so a second command that found the
// same fault while the first was restoring finds the mirror healthy and does
// nothing.
//
// When only the link is gone and the state file records a store that passes
// every check a linked store gets, cloned from the remote the config's URL
// names now, a new link to that store, after its config is rewritten from the fixed
// set, is all the restore makes: no network is needed. Otherwise it clones
// r.URL, provided the file system has room for it.
//
// The order is what keeps a failure harmless: the clone lands in .tmp/, takes
// in what the old stores still hold intact, is recorded in the store record
// as never linked, is renamed into .store/, is recorded as linked, and only
// then is a new link renamed over the stable path, the one atomic way to
// replace what borrowers read. The state file is written under a temporary
// name before the swap and renamed into place after it, so it never names a
// store borrowers can't reach. Between the swap and that rename the state
// file still names the old store; a check that lands there, or after a failed
// rename, sees the mismatch as missing and rebuilds, salvaging from the
// linked store. A failure before the swap removes what this restore made and
// puts the link, its store, the state file and the store record back as they
// were.
//
// Outside the free-space path, no store that was ever linked is removed
// here: the one the swap replaced is marked retired in the store record once
// the probe is over, and only a later sweep, on the strict scan's word,
// removes it (see sweep). The free-space path is the exception: when the
// restore's free-space check would refuse, it runs the strict scan under its
// lock and removes retired stores that no alternates line pins, whatever
// their age (see makeRoom).
//
// A real directory the user owns at the stable path, such as a mirror
// someone copied there by hand, is salvaged from where it stands and then
// moved into .store/ under a ".moved" name just before the swap, never
// removed. Anything else there is left in place and reported.
//
// Once a store is in place, the borrowers are probed for refs it can't serve
// (see rebuild), and the refs still lost come back with the outcome.
func restore(ctx context.Context, opts Options, r Repo) rebuildResult {
	if r.URL == "" {
		return failed(errors.New("the workspace config gives no URL to restore from"))
	}
	if err := ensureLayout(opts.Home, opts.WorkspaceRoot); err != nil {
		return failed(err)
	}

	wait := opts.LockWait
	if wait <= 0 {
		wait = DefaultLockWait
	}
	// The test build may shorten the wait (the five-minute default and the
	// SessionStart hook's ten seconds alike); a release build keeps it.
	wait = testhook.MirrorLockWait(wait)
	holder := fmt.Sprintf("niwa pid %d restoring mirror %s", os.Getpid(), r.ID)
	release, err := filelock.WaitFileLock(ctx, LockPath(opts.Home, opts.WorkspaceRoot, r.ID), holder, wait)
	if errors.Is(err, filelock.ErrLockHeld) {
		return rebuildResult{outcome: OutcomeBusy, err: err}
	}
	if err != nil {
		return failed(err)
	}
	defer release()

	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	// Under the lock no other restore of this repo is running, so anything
	// of its own in .tmp/ or .meta/ was left by one that crashed.
	clearLeftovers(mdir, r.ID)
	fault := check(ctx, mdir, r)
	if fault == FaultNone {
		return rebuildResult{outcome: OutcomeHealthy}
	}
	return rebuild(ctx, mdir, r, rebuildArgs{
		fault:     fault,
		borrowers: borrowersOf(opts.Instances, filepath.Join(mdir, r.ID)),
		scan:      opts.Scan,
	})
}

// rebuildArgs is what a rebuild needs besides the mirror: the fault it
// rebuilds for, the borrowers its probe reads, and the strict scan the
// space-pressure path may call (Options.Scan; nil removes nothing).
type rebuildArgs struct {
	fault     Fault
	borrowers []string
	scan      func() ([]string, error)
}

// rebuildResult is what a restore or rebuild did. salvaged names the old
// stores a new store was built with salvage from, relative to the mirrors
// directory (".store/<name>", or the repo id for a directory found at the
// stable path); probed lists the borrowers the probe read, in order; events
// are the stores removed or kept on the way, each logged as a store line
// after the call's other lines.
type rebuildResult struct {
	outcome  Outcome
	lost     []LostRef
	salvaged []string
	probed   []string
	events   []storeEvent
	err      error
}

// failed is a restore-failed result carrying err.
func failed(err error) rebuildResult {
	return rebuildResult{outcome: OutcomeRestoreFailed, err: err}
}

// rebuild is a restore's work once it holds the mirror's lock and knows the
// fault: relink the recorded store or build a new one, and swap it in. The
// caller holds the lock. It doesn't check again, so a refresh whose deep
// verify found a bad pack the check can't see rebuilds through it too.
//
// It starts from the store record, seeded from the state file when there is
// none (a mirror v0.43.0 built), and removes the stores whose entries say
// they were never linked: under the lock, those are left from a restore
// that crashed before its swap, and no clone can have resolved the link to
// one, so no scan is needed.
//
// Once the swap has put a store in place, and for a new store once the state
// file records it, rebuild probes the borrowers against it, still under the
// caller's lock, and recovers what it can (recoverBorrowers); the refs still
// lost come back with the outcome. Only then does the record mark the store
// the swap replaced retired, so the old store is on disk, and recorded as
// linked, for as long as the probe and recovery run (PRD R13). A rebuild that
// fails before the swap, or whose new store went unrecorded, probes nothing.
// A lost ref is the stated limit of a restore, not a failure: the outcome
// stays restored.
//
// salvaged is nil when nothing was salvaged: a relink, a store cloned from
// another remote, a mirror with no old store, or a rebuild that failed
// before the swap.
func rebuild(ctx context.Context, mdir string, r Repo, args rebuildArgs) rebuildResult {
	stable := filepath.Join(mdir, r.ID)
	prior := snapStores(mdir, r.ID)
	rec := storesOrSeed(mdir, r.ID)
	res := rebuildResult{events: rec.removeNeverLinked(mdir, r.ID)}
	fail := func(err error) rebuildResult {
		res.outcome, res.err = OutcomeRestoreFailed, err
		return res
	}

	moveAside := false
	// noGit lists old stores git mustn't run in: a recorded store whose
	// config couldn't be rewritten still holds the config it was found with.
	var noGit []string
	info, err := os.Lstat(stable)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		relinked, distrusted, err := relink(ctx, mdir, r)
		if err != nil {
			return fail(err)
		}
		if relinked {
			res.outcome = OutcomeRestored
			res.probed = args.borrowers
			res.lost = recoverBorrowers(ctx, mdir, r, args.borrowers)
			res.err = recordRelinked(mdir, r, &rec)
			return res
		}
		if distrusted != "" {
			noGit = append(noGit, distrusted)
		}
	case err != nil:
		return fail(fmt.Errorf("inspecting %s: %w", stable, err))
	case info.Mode()&os.ModeSymlink != 0:
	case info.IsDir() && ownedByUser(info):
		moveAside = true
	default:
		// A file, a fifo or a directory another user owns: a rename over it
		// would destroy it or fail, and moving it isn't niwa's call.
		return fail(fmt.Errorf("%s is not a link; left in place", stable))
	}

	if err := checkFreeSpace(mdir, r.ID); err != nil {
		events, err := makeRoom(mdir, r.ID, &rec, args.scan, err)
		res.events = append(res.events, events...)
		if err != nil {
			return fail(err)
		}
	}

	// Salvage and pin sources are the stores the record lists as linked,
	// cloned from the remote the config names now, under a valid name, in a
	// real directory. A store with no entry, or one from another remote that
	// outlived a url-changed rebuild, is never read: it holds nothing the new
	// store should keep. The sources are listed before the new store exists,
	// so they can never name it.
	var srcs []string
	for _, name := range salvageSources(mdir, r, rec, args.fault) {
		srcs = append(srcs, filepath.Join(mdir, ".store", name))
		res.salvaged = append(res.salvaged, filepath.Join(".store", name))
	}
	if moveAside && !fromOtherRemote(mdir, r, args.fault) {
		srcs = append(srcs, stable)
		res.salvaged = append(res.salvaged, r.ID)
	}
	identity := remoteIdentity(r.URL)
	entry := storeEntry{URL: recordURL(r.URL), Identity: identity}
	// The new store's entry is written as never linked before the store
	// reaches .store/, so a crash after the rename leaves a store the record
	// names and the next restore removes.
	beforeRename := func(name string) error {
		entry.Name = name
		rec.put(entry)
		return writeStores(mdir, r.ID, rec)
	}
	store, stateTmp, err := buildStore(ctx, mdir, r, srcs, noGit, beforeRename)
	if err != nil {
		prior.putBack(mdir, r.ID)
		res.salvaged = nil
		return fail(err)
	}
	discard := func() {
		// Nothing links to the new store yet, so it goes, by its exact name,
		// and the record is put back as it was.
		_ = os.RemoveAll(filepath.Join(mdir, ".store", store))
		_ = os.Remove(stateTmp)
		prior.putBack(mdir, r.ID)
		res.salvaged = nil
	}

	// Marked linked before the swap: a crash between the two keeps the
	// store, since a borrower may have resolved the link to it.
	entry.Linked = true
	rec.put(entry)
	if err := writeStores(mdir, r.ID, rec); err != nil {
		discard()
		return fail(err)
	}

	moved := ""
	if moveAside {
		name, err := newStoreName(r.ID, time.Now())
		if err != nil {
			discard()
			return fail(err)
		}
		moved = filepath.Join(mdir, ".store", name+".moved")
		if err := os.Rename(stable, moved); err != nil {
			discard()
			return fail(fmt.Errorf("moving the directory at %s aside: %w", stable, err))
		}
	}
	if err := swapLink(mdir, r.ID, store); err != nil {
		discard()
		if moved != "" {
			_ = os.Rename(moved, stable)
		}
		return fail(err)
	}
	res.outcome = OutcomeRestored
	if err := recordState(mdir, r.ID, stateTmp); err != nil {
		// The new store is linked and borrowers read it; only its state
		// file is missing, so the next check finds no matching state file
		// and rebuilds, salvaging from this store. Every store stays, the
		// one the swap replaced still recorded as linked.
		_ = os.Remove(stateTmp)
		res.err = fmt.Errorf("recording the new store: %w", err)
		return res
	}
	res.probed = args.borrowers
	res.lost = recoverBorrowers(ctx, mdir, r, args.borrowers)

	// The probe and recovery are over: the replaced store is retired, to be
	// removed by a sweep once no clone names it. A record that can't be
	// written removes nothing either; the next restore retires it.
	rec.retireReplaced(store, time.Now())
	rec.dropVanished(mdir, r.ID)
	if err := writeStores(mdir, r.ID, rec); err != nil {
		res.err = err
	}
	return res
}

// recordRelinked makes the store a relink linked again the record's linked
// store, retiring any other linked one, and writes the record.
func recordRelinked(mdir string, r Repo, rec *storesFile) error {
	st, err := readState(mdir, r.ID)
	if err != nil {
		return nil
	}
	name := filepath.Base(st.Store)
	if rec.index(name) < 0 {
		rec.put(storeEntry{Name: name, URL: recordURL(st.URL), Identity: remoteIdentity(st.URL), Linked: true})
	}
	rec.retireReplaced(name, time.Now())
	rec.dropVanished(mdir, r.ID)
	return writeStores(mdir, r.ID, *rec)
}

// salvageSources returns, sorted, the names of the stores in rec a rebuild
// of r for fault may salvage from: linked at some point, cloned from the
// remote r.URL names (by identity, so another spelling of it counts), under
// a valid store name of r.ID, and a real directory in a real .store/.
//
// The state file is the other record of where the linked store came from.
// When it says another remote (fromOtherRemote), the store it names is left
// out whatever its entry says, so a store is read only when both records
// agree it came from this remote.
func salvageSources(mdir string, r Repo, rec storesFile, fault Fault) []string {
	identity := remoteIdentity(r.URL)
	distrusted := ""
	if fromOtherRemote(mdir, r, fault) {
		if st, err := readState(mdir, r.ID); err == nil {
			distrusted = filepath.Base(st.Store)
		}
	}
	var names []string
	for _, e := range rec.Stores {
		if e.Name == distrusted {
			continue
		}
		entryIdentity := e.Identity
		if entryIdentity == "" {
			entryIdentity = remoteIdentity(e.URL)
		}
		if e.Linked && entryIdentity == identity && validStoreName(r.ID, e.Name) && storeDirSafe(mdir, e.Name) {
			names = append(names, e.Name)
		}
	}
	slices.Sort(names)
	return names
}

// relink points a new link at the store the state file records, when that
// store passes every check a linked store gets (checkStore, which also
// refuses a store cloned from another remote than r.URL). The check is
// lock-free and read-only and leaves the store's config as it found it, so
// before the swap, under the caller's lock, the config is rewritten whole
// from the fixed set (writeStoreConfig): no key planted in it reaches the
// fetch by id the probe may run next.
//
// It reports whether it relinked; false with no error means nothing changed
// and the caller builds a new store. When the config couldn't be rewritten,
// distrusted is the recorded store's path: it still holds the config it was
// found with, so the caller runs no git in it.
func relink(ctx context.Context, mdir string, r Repo) (relinked bool, distrusted string, err error) {
	rec, err := readState(mdir, r.ID)
	if err != nil || checkStore(ctx, mdir, r, rec) != FaultNone {
		return false, "", nil
	}
	store := filepath.Join(mdir, rec.Store)
	err = atStage(stageRelinkConfig)
	if err == nil {
		err = writeStoreConfig(store, r.URL)
	}
	if err != nil {
		return false, store, nil
	}
	if err := swapLink(mdir, r.ID, filepath.Base(rec.Store)); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// fromOtherRemote reports whether the mirror's stores were cloned from
// another remote than r.URL: the check said so, or the state file records
// another remote behind a fault found first. Remotes compare by identity
// (urlChanged), so a store recorded under another spelling of r.URL's remote
// is salvaged from. A state file that can't be read
// says nothing either way.
func fromOtherRemote(mdir string, r Repo, fault Fault) bool {
	if fault == FaultURLChanged {
		return true
	}
	rec, err := readState(mdir, r.ID)
	return err == nil && urlChanged(r, rec)
}

// recordState renames the state file written under its temporary name into
// place and syncs .meta/ so the rename survives a crash.
func recordState(mdir, repoID, stateTmp string) error {
	if err := atStage(stageRecord); err != nil {
		return err
	}
	if err := os.Rename(stateTmp, statePath(mdir, repoID)); err != nil {
		return err
	}
	_ = syncDir(metaDir(mdir))
	return nil
}

// buildStore clones r into a temporary directory, writes the fixed config
// into it, salvages into it from the old stores at srcs and pins what they
// hold that the clone doesn't reach, writes its state file under a temporary
// name, calls beforeRename with the store's name (the caller records it
// there), and renames it into .store/. It returns the store's name and the
// state file's temporary path. On failure nothing it made is left behind.
//
// Salvage reads object files only, so it reads every store in srcs; the pins
// need git in each old store, so the stores in noGit give none.
func buildStore(ctx context.Context, mdir string, r Repo, srcs, noGit []string, beforeRename func(name string) error) (string, string, error) {
	suffix, err := randHex()
	if err != nil {
		return "", "", err
	}
	tmp := filepath.Join(mdir, ".tmp", r.ID+"-"+suffix)
	if err := atStage(stageClone); err != nil {
		return "", "", fmt.Errorf("cloning the mirror: %w", err)
	}
	if err := cloneMirror(ctx, r.URL, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("cloning the mirror: %w", err)
	}
	if err := writeStoreConfig(tmp, r.URL); err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	salvage(ctx, mdir, r.ID, srcs, tmp)
	var pinFrom []string
	for _, src := range srcs {
		if !slices.Contains(noGit, src) {
			pinFrom = append(pinFrom, src)
		}
	}
	pinKept(ctx, pinFrom, tmp)

	// The clone ran under fsckObjects and every salvaged pack passed git
	// verify-pack, so the build is both a refresh and a deep verify.
	built := time.Now()
	name, err := newStoreName(r.ID, built)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	// The manifest is read in .tmp/: a rename of the store directory moves
	// none of the ctimes it records.
	packs, err := manifest(tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("listing the new store's packs: %w", err)
	}
	if err := atStage(stageState); err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("writing the state file: %w", err)
	}
	stateTmp, err := writeStateTemp(mdir, state{
		Schema:         stateSchema,
		RepoID:         r.ID,
		URL:            recordURL(r.URL),
		Identity:       remoteIdentity(r.URL),
		Store:          filepath.Join(".store", name),
		Packs:          packs,
		LastRefresh:    stateTime(built),
		LastDeepVerify: stateTime(built),
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	err = beforeRename(name)
	if err == nil {
		err = atStage(stageStore)
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(mdir, ".store", name))
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		_ = os.Remove(stateTmp)
		return "", "", fmt.Errorf("moving the new store into place: %w", err)
	}
	return name, stateTmp, nil
}

// swapLink points the stable path at .store/<store>: a new link is made under
// a temporary name (Symlink never replaces an existing entry) and renamed over
// the stable path, then the directory is synced so the swap survives a crash.
// The sync is best effort: once the rename has landed the link names the new
// store, so an error after it must not make the caller remove that store.
func swapLink(mdir, repoID, store string) error {
	suffix, err := randHex()
	if err != nil {
		return err
	}
	tmpLink := filepath.Join(mdir, ".link-"+suffix)
	err = atStage(stageLink)
	if err == nil {
		err = os.Symlink(filepath.Join(".store", store), tmpLink)
	}
	if err != nil {
		return fmt.Errorf("making the new link: %w", err)
	}
	err = atStage(stageSwap)
	if err == nil {
		err = os.Rename(tmpLink, filepath.Join(mdir, repoID))
	}
	if err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("swapping the new link into place: %w", err)
	}
	_ = syncDir(mdir)
	return nil
}

// leftoverPatterns match, for one repo id, what a restore makes under a
// temporary name and a crash can leave behind: its clone, and the directory
// a recovering unpack works in, .tmp/<id>-<8 hex>; its state file,
// .meta/.<id>.json-<8 hex>; and its store record, .meta/.<id>.stores-<8 hex>.
// None matches an entry of a repo whose id only starts with this one.
func leftoverPatterns(repoID string) (tmp, stateTmp, storesTmp *regexp.Regexp) {
	id := regexp.QuoteMeta(repoID)
	return regexp.MustCompile(`^` + id + `-[0-9a-f]{8}$`),
		regexp.MustCompile(`^\.` + id + `\.json-[0-9a-f]{8}$`),
		regexp.MustCompile(`^\.` + id + `\.stores-[0-9a-f]{8}$`)
}

// leftovers lists the paths of repoID's leftovers in .tmp/ and .meta/. A
// .tmp/ or .meta/ that isn't a real directory the user owns, a link to
// somewhere else say, isn't read, so nothing is ever removed through it.
func leftovers(mdir, repoID string) []string {
	tmpRe, stateRe, storesRe := leftoverPatterns(repoID)
	var paths []string
	for _, d := range []struct {
		dir string
		res []*regexp.Regexp
	}{
		{filepath.Join(mdir, ".tmp"), []*regexp.Regexp{tmpRe}},
		{metaDir(mdir), []*regexp.Regexp{stateRe, storesRe}},
	} {
		if !realOwnedDir(d.dir) {
			continue
		}
		entries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			for _, re := range d.res {
				if re.MatchString(e.Name()) {
					paths = append(paths, filepath.Join(d.dir, e.Name()))
					break
				}
			}
		}
	}
	return paths
}

// clearLeftovers removes repoID's leftovers by exact name. Each is looked at
// with Lstat, so a link is removed itself and what it names is never touched.
// The caller holds the mirror's lock: a restore in progress holds it too, so
// nothing removed here belongs to one that's still running.
func clearLeftovers(mdir, repoID string) {
	for _, path := range leftovers(mdir, repoID) {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			_ = os.RemoveAll(path)
		} else {
			_ = os.Remove(path)
		}
	}
}

// clearLeftoversIfIdle clears repoID's leftovers after a healthy check. A
// crash before the swap leaves the mirror healthy, so no restore would ever
// run to clear what it left. The lock is only tried: when another command
// holds it, the leftovers may be its restore at work, and they're left for a
// later check.
func clearLeftoversIfIdle(opts Options, mdir, repoID string) {
	if len(leftovers(mdir, repoID)) == 0 {
		return
	}
	holder := fmt.Sprintf("niwa pid %d clearing mirror %s leftovers", os.Getpid(), repoID)
	release, err := filelock.TryFileLock(LockPath(opts.Home, opts.WorkspaceRoot, repoID), holder)
	if err != nil {
		return
	}
	defer release()
	clearLeftovers(mdir, repoID)
}

// makeRoom is the restore's answer to a free-space check that refused
// (refusal): with a strict scan to call, it calls it under the lock the
// caller holds, removes every retired store no clone's alternates resolves
// into, whatever its age, and checks the free space again. A refused
// restore leaves every borrower broken, which is worse than reclaiming a
// retired store early. With no scan, a scan that fails, or still too little
// room, the refusal stands and nothing more is removed.
func makeRoom(mdir, repoID string, rec *storesFile, scan func() ([]string, error), refusal error) ([]storeEvent, error) {
	if scan == nil {
		return nil, refusal
	}
	clones, err := scan()
	if err != nil {
		return nil, refusal
	}
	events, removed := removeUnpinned(mdir, repoID, rec, clones, 0, reasonSpace, time.Now())
	if removed {
		// The stores are gone either way; a record that can't be written
		// here keeps their entries until dropVanished prunes them.
		_ = writeStores(mdir, repoID, *rec)
	}
	if err := checkFreeSpace(mdir, repoID); err != nil {
		return events, err
	}
	return events, nil
}
