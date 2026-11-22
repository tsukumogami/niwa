package mirror

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// deepVerifyEvery is how long a store goes between deep verifies: git
// verify-pack on every pack it holds, which reads each pack's bytes, where
// the offline check compares only sizes, trailers and ctimes.
const deepVerifyEvery = 24 * time.Hour

// refresh brings a mirror the check just found healthy up to date from the
// remote, dropping nothing it ever fetched. res is that check's Result and is
// returned with what the refresh did.
//
// It only tries the mirror's lock: a held lock means another command is
// restoring or refreshing the mirror, and the refresh is skipped with no log
// line and no error. Under the lock it rewrites the store's config from the
// fixed set, deep verifies the store when the last deep verify is a day old
// or was never recorded, fetches every ref with no pruning, and records the
// new manifest, then logs one refresh line.
//
// The deep verify runs before the fetch, so a pack whose bytes rotted with
// its size, trailer and ctime unchanged is caught before the fetch reads it.
// Such a pack makes the refresh, still holding the lock, rebuild the mirror
// with fault pack-changed and log a restore line in place of the refresh
// line; the Result then carries that fault and the restore's outcome, and
// the refs the rebuild's probe of the borrowers left lost, each logged on an
// unrecoverable line after the restore line, and a store line for each store
// the rebuild removed or kept after those.
//
// Any other failure leaves the link, the store's packs and the state file as
// they were, logs nothing, and returns the healthy Result with Err set: the
// mirror is still usable, only not up to date.
//
// Its line carries the check's check_ms, and took_ms read from elapsedNow at
// the refresh's start and end. A failed log write is kept in LogErr unless
// the check line's already failed.
func refresh(ctx context.Context, opts Options, mdir string, r Repo, res Result) Result {
	if r.URL == "" {
		res.Err = errors.New("the workspace config gives no URL to refresh from")
		return res
	}
	holder := fmt.Sprintf("niwa pid %d refreshing mirror %s", os.Getpid(), r.ID)
	release, err := filelock.TryFileLock(LockPath(opts.Home, opts.WorkspaceRoot, r.ID), holder)
	if errors.Is(err, filelock.ErrLockHeld) {
		return res
	}
	if err != nil {
		res.Err = fmt.Errorf("taking the mirror's lock: %w", err)
		return res
	}
	defer release()

	// Another command may have rebuilt the mirror between the check and the
	// lock. The store refreshed is the one the link and the state file both
	// name now; when they disagree the mirror is mid-change, and the next
	// command's check deals with it.
	rec, err := readState(mdir, r.ID)
	if err != nil {
		res.Err = fmt.Errorf("reading the state file: %w", err)
		return res
	}
	if target, err := os.Readlink(filepath.Join(mdir, r.ID)); err != nil || target != rec.Store || !validLinkTarget(r.ID, target) {
		return res
	}

	start := time.Now()
	tookStart := elapsedNow()
	line := logLine{
		Time:    logTime(start),
		Command: opts.Command,
		Mirror:  r.ID,
		CheckMs: res.CheckDuration.Milliseconds(),
	}
	logged := func(err error) {
		if err != nil && res.LogErr == nil {
			res.LogErr = err
		}
	}
	rotten, err := refreshStore(ctx, mdir, r, rec, start)
	if rotten {
		res.Fault = FaultPackChanged
		rb := rebuild(ctx, mdir, r, rebuildArgs{
			fault:     FaultPackChanged,
			borrowers: borrowersOf(opts.Instances, filepath.Join(mdir, r.ID)),
			scan:      opts.Scan,
		})
		res.Outcome, res.Unrecoverable, res.Err = rb.outcome, rb.lost, rb.err
		line.Event = eventRestore
		line.SalvagedFrom = rb.salvaged
		line.Probed = rb.probed
		line.Fault = res.Fault
		line.Result = res.Outcome
		took := elapsedNow().Sub(tookStart).Milliseconds()
		line.TookMs = &took
		line.NetworkNotes = networkNotesFrom(ctx)
		lines := append([]any{line}, unrecoverableLines(line, res.Unrecoverable)...)
		logged(appendLog(mdir, append(lines, storeLines(line, rb.events)...)...))
		return res
	}
	if err != nil {
		res.Err = err
		return res
	}
	line.Event = eventRefresh
	line.Result = OutcomeHealthy
	took := elapsedNow().Sub(tookStart).Milliseconds()
	line.TookMs = &took
	line.NetworkNotes = networkNotesFrom(ctx)
	logged(appendLog(mdir, line))
	return res
}

// refreshStore does a refresh's work on the store rec records, at now. It
// reports rotten when the deep verify found a pack git verify-pack refuses;
// nothing past the config was changed then. A failure at any step returns an
// error and records no manifest.
func refreshStore(ctx context.Context, mdir string, r Repo, rec state, now time.Time) (rotten bool, err error) {
	store := filepath.Join(mdir, rec.Store)
	if err := writeStoreConfig(store, r.URL); err != nil {
		return false, err
	}
	deep := deepVerifyDue(rec, now)
	if deep {
		if bad, err := verifyEveryPack(ctx, store); err != nil {
			return bad, err
		}
	}
	if err := fetchMirror(ctx, store, r.URL); err != nil {
		return false, fmt.Errorf("fetching from the remote: %w", err)
	}
	packs, err := manifest(store)
	if err != nil {
		return false, fmt.Errorf("listing the store's packs: %w", err)
	}

	next := rec
	next.URL = recordURL(r.URL)
	next.Identity = remoteIdentity(r.URL)
	next.Packs = packs
	next.LastRefresh = stateTime(now)
	if deep {
		next.LastDeepVerify = stateTime(now)
	}
	stateTmp, err := writeStateTemp(mdir, next)
	if err != nil {
		return false, err
	}
	if err := recordState(mdir, r.ID, stateTmp); err != nil {
		_ = os.Remove(stateTmp)
		return false, fmt.Errorf("recording the refreshed store: %w", err)
	}
	return false, nil
}

// deepVerifyDue reports whether rec's last deep verify is at least
// deepVerifyEvery before now, or isn't recorded in a form that can be read.
func deepVerifyDue(rec state, now time.Time) bool {
	last, err := time.Parse(time.RFC3339, rec.LastDeepVerify)
	return err != nil || now.Sub(last) >= deepVerifyEvery
}

// verifyEveryPack runs git verify-pack on each pack in store, by its .idx.
// It reports bad with the first refusal git itself makes. An error with bad
// false says nothing about the packs: they couldn't be listed, git couldn't
// be run, or the command was cancelled, and none of those is a reason to
// rebuild. A store with no pack directory holds only loose objects and has
// nothing to verify.
func verifyEveryPack(ctx context.Context, store string) (bad bool, err error) {
	dir := filepath.Join(store, "objects", "pack")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("listing the store's packs: %w", err)
	}
	for _, e := range entries {
		if !validPackName(e.Name()) {
			continue
		}
		idx := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".pack")+".idx")
		if err := verifyPack(ctx, idx); err != nil {
			var exit *exec.ExitError
			refused := errors.As(err, &exit) && ctx.Err() == nil
			return refused, fmt.Errorf("deep verify of %s: %w", e.Name(), err)
		}
	}
	return false, nil
}
