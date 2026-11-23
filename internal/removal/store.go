package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// storeCap bounds a scratch store's size. A fetch or a local pack that grows
// the store past it is killed and the unit kept. A variable so a test can
// watch the cap fire without writing a gigabyte.
var storeCap int64 = 1 << 30

// storePoll is how often the store's size is read while a call that writes
// into it runs.
var storePoll = 50 * time.Millisecond

// afterStoreLayout, when set, runs on each store's directory right after
// niwa lays it out. Only tests set it, to tamper with a store the way an
// agent with write access to it could, between its creation and its use.
var afterStoreLayout func(dir string)

// storeConfig is the whole of a scratch store's config, written by niwa and
// compared byte for byte before each network call. It turns off everything
// that would run or write beside the call itself: automatic gc and
// maintenance, and the commit-graph a fetch would write.
var storeConfig = []byte("[core]\n" +
	"\trepositoryformatversion = 0\n" +
	"\tbare = true\n" +
	"\tlogallrefupdates = false\n" +
	"[gc]\n" +
	"\tauto = 0\n" +
	"[maintenance]\n" +
	"\tauto = false\n" +
	"[fetch]\n" +
	"\twriteCommitGraph = false\n")

// storeForbidden are the files a scratch store never holds, since each would
// send its object reads somewhere else: a commondir, and alternates of either
// kind. niwa writes none of them, so finding one keeps.
var storeForbidden = []string{
	"commondir",
	filepath.Join("objects", "info", "alternates"),
	filepath.Join("objects", "info", "http-alternates"),
}

// storeDirs are the directories niwa lays a scratch store out with.
var storeDirs = []string{
	"objects",
	filepath.Join("objects", "info"),
	filepath.Join("objects", "pack"),
	"refs",
	filepath.Join("refs", "heads"),
	filepath.Join("refs", "tags"),
}

// store is one repository's scratch object store: a bare repository niwa
// lays out itself under the pass's scratch directory, with no refs and no
// alternates, which every ancestry and merged-by-content check runs in.
// Every object in it came through index-pack, which hashes each one from its
// content, so nothing the agent wrote under another object's id stands in
// for the object a proof remote has.
type store struct {
	dir string
	e   *Env
}

// newStore makes a fresh scratch store at <pass>/store-<n>. Mkdir rather
// than MkdirAll: a directory already at the path, planted or left, fails the
// creation, and the unit keeps. git init isn't used, so no template is
// copied in.
func (e *Env) newStore() (*store, error) {
	if e.Scratch == nil {
		if e.ScratchErr != nil {
			return nil, fmt.Errorf("scratch directory unusable: %v", e.ScratchErr)
		}
		return nil, errors.New("no scratch directory")
	}
	if err := e.Scratch.check(); err != nil {
		return nil, err
	}
	e.Scratch.mu.Lock()
	e.Scratch.storeSeq++
	n := e.Scratch.storeSeq
	e.Scratch.mu.Unlock()
	st := &store{dir: filepath.Join(e.Scratch.Dir, "store-"+strconv.Itoa(n)), e: e}
	if err := os.Mkdir(st.dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating the scratch store: %w", err)
	}
	if err := st.layOut(); err != nil {
		st.remove()
		return nil, err
	}
	if afterStoreLayout != nil {
		afterStoreLayout(st.dir)
	}
	if err := st.check(); err != nil {
		st.remove()
		return nil, err
	}
	return st, nil
}

// layOut writes the store's directories, HEAD and config, each new.
func (st *store) layOut() error {
	if err := os.Chmod(st.dir, 0o700); err != nil {
		return fmt.Errorf("creating the scratch store: %w", err)
	}
	for _, d := range storeDirs {
		if err := os.Mkdir(filepath.Join(st.dir, d), 0o700); err != nil {
			return fmt.Errorf("creating the scratch store: %w", err)
		}
	}
	for name, data := range map[string][]byte{
		"HEAD":   []byte("ref: refs/heads/none\n"),
		"config": storeConfig,
	} {
		if err := writeNew(filepath.Join(st.dir, name), data); err != nil {
			return fmt.Errorf("creating the scratch store: %w", err)
		}
	}
	return nil
}

// writeNew writes a file that must not exist yet, at mode 0600.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// check is the check made before each use of the store: the owner and mode
// of the store, the pass directory and the scratch root; the store's own
// directories real directories, not links; and none of the files that would
// send object reads elsewhere.
func (st *store) check() error {
	if err := st.e.Scratch.check(); err != nil {
		return err
	}
	if err := checkPrivateDir(st.dir); err != nil {
		return err
	}
	for _, d := range storeDirs {
		info, err := os.Lstat(filepath.Join(st.dir, d))
		if err != nil {
			return fmt.Errorf("scratch store check: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("scratch store check: %s is not a directory", d)
		}
	}
	for _, name := range storeForbidden {
		_, err := os.Lstat(filepath.Join(st.dir, name))
		switch {
		case err == nil:
			return fmt.Errorf("scratch store check: the store holds %s", filepath.ToSlash(name))
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("scratch store check: %w", err)
		}
	}
	return nil
}

// checkConfig re-reads the store's config, refusing a link, and compares it
// byte for byte with what niwa wrote. Any difference keeps: a
// url.<base>.insteadOf written there would send the next call elsewhere.
func (st *store) checkConfig() error {
	b, err := readCapped(filepath.Join(st.dir, "config"), 64<<10)
	if err != nil {
		return fmt.Errorf("scratch store check: reading its configuration: %v", err)
	}
	if !bytes.Equal(b, storeConfig) {
		return errors.New("scratch store check: its configuration is not the one niwa wrote")
	}
	return nil
}

// use checks the store, and the empty directory its calls run from, before a
// local call, and returns that directory.
func (st *store) use() (string, error) {
	if err := st.check(); err != nil {
		return "", err
	}
	return st.e.scratchDir(scratchNet)
}

// useNetwork is use with the configuration compare a network call needs.
func (st *store) useNetwork() (string, error) {
	dir, err := st.use()
	if err != nil {
		return "", err
	}
	if err := st.checkConfig(); err != nil {
		return "", err
	}
	return dir, nil
}

// dirs is where a call against the store runs: the store as --git-dir and no
// work tree.
func (st *store) dirs() repoDirs { return repoDirs{GitDir: st.dir} }

// remove deletes the store. It holds only objects a proof remote sent and a
// pack of the unit's own objects, never anyone's only copy.
func (st *store) remove() {
	if st != nil {
		_ = os.RemoveAll(st.dir)
	}
}

// size is the store's size on disk: every regular file under it, read
// without following a link.
func (st *store) size() int64 {
	var total int64
	_ = filepath.WalkDir(st.dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// capped returns a context that ends when the store grows past storeCap, and
// a stop function that ends the watch and reports whether the cap fired.
// The calls that write into the store run under it.
func (st *store) capped(ctx context.Context) (context.Context, func() bool) {
	ctx, cancel := context.WithCancel(ctx)
	var (
		mu       sync.Mutex
		exceeded bool
	)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(storePoll)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if st.size() > storeCap {
					mu.Lock()
					exceeded = true
					mu.Unlock()
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() bool {
		close(done)
		<-finished
		cancel()
		// A last read catches a call that finished between two polls.
		mu.Lock()
		defer mu.Unlock()
		if !exceeded && st.size() > storeCap {
			exceeded = true
		}
		return exceeded
	}
}
