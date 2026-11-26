package mirror

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/testhook"
)

// DissociateOptions says which instance niwa dissociate frees and where its
// workspace's mirrors are.
type DissociateOptions struct {
	// Home and WorkspaceRoot place the workspace's mirrors (mirrorsDir) and
	// niwa's empty hooks directory.
	Home, WorkspaceRoot string
	// Instance is the instance's directory. Free space is read on its
	// volume, and each clone is named by its path relative to it.
	Instance string
	// Clones are the instance's clones (hostdir.InstanceClones), listed by
	// the caller so this package never enumerates instances itself.
	Clones []string
}

// DissociateReport is what one run of Dissociate did. Every list names
// clones by their path relative to the instance, <group>/<repo>, in order.
type DissociateReport struct {
	// Changed are the clones that no longer borrow: their borrowed objects
	// are in their own packs and their alternates file is gone. A clone a
	// run finished from an earlier run's leftover file counts too.
	Changed []string
	// Restored are the clones whose leftover alternates.niwa-dissociate an
	// earlier run left behind failed git fsck and was renamed back to
	// alternates, so the clone borrows again. This run then copies and
	// drops it like any other borrower, unless it stops first.
	Restored []string
	// Outside are the clones whose alternates file names an object
	// directory outside this workspace's mirrors. They're left exactly as
	// they are.
	Outside []OutsideClone
}

// OutsideClone is a clone Dissociate left alone because its alternates file
// names Line, which isn't inside this workspace's mirrors.
type OutsideClone struct{ Clone, Line string }

// dissociateStage, when a test sets it, is called with the stage ("copy" or
// "verify") and the clone's directory just before the copy of a clone starts
// and just before its git fsck runs, once its alternates file is set aside.
// An error it returns at the copy stage fails that clone's copy. It's nil
// outside the tests.
var dissociateStage func(stage, clone string) error

// The kinds of clone Dissociate tells apart.
type dissociateKind int

const (
	kindNeverBorrowed dissociateKind = iota
	kindBorrower
	kindLeftover
	kindOutside
)

// dissociateClone is one of the instance's clones, as classify found it.
type dissociateClone struct {
	dir, rel string
	kind     dissociateKind
	repoID   string
	outside  string
}

func (c dissociateClone) gitDir() string { return filepath.Join(c.dir, ".git") }

func (c dissociateClone) infoDir() string { return filepath.Join(c.dir, ".git", "objects", "info") }

// Dissociate makes the instance's clones independent of this workspace's
// mirrors (PRD R8): it copies every object each borrowing clone reads
// through its alternates file into the clone's own pack, then removes the
// alternates file once git fsck passes with the file out of git's reach.
// The caller holds the instance's lock.
//
// It changes nothing when any clone's alternates files can't be read for
// certain, when free space on the instance's volume is below the mirror pack
// sizes of the instance's borrowing repos (spaceNeeded, the restore's figure
// with its 1 GiB floor per repo), or when a copy fails: every copy runs
// before any alternates file is dropped, so a failure part way leaves every
// clone borrowing and readable. A clone that never borrowed is left alone and
// isn't reported, and so is a clone whose alternates file names anything
// outside this workspace's mirrors, which is reported in Outside instead.
//
// A crash between setting a clone's alternates file aside and removing it
// leaves hostdir.AlternatesAside behind. The strict scan reads that file as
// alternates, so the store stays pinned, and the next run finishes the step:
// git fsck with the file still aside, the file removed on a pass or renamed
// back on a failure.
//
// Every git call goes through gitRun, so it carries no inherited GIT_*
// variable (GIT_ALTERNATE_OBJECT_DIRECTORIES among them), and gc,
// maintenance, fsmonitor and hooks are turned off on its command line. No
// config key is written to any clone (PRD R21).
//
// On an error the report still lists what the run changed before it
// stopped.
func Dissociate(ctx context.Context, opts DissociateOptions) (DissociateReport, error) {
	var rep DissociateReport
	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	clones, err := classifyForDissociate(opts, mdir)
	if err != nil {
		return rep, err
	}

	var todo []dissociateClone
	for _, c := range clones {
		switch c.kind {
		case kindOutside:
			rep.Outside = append(rep.Outside, OutsideClone{Clone: c.rel, Line: c.outside})
		case kindBorrower, kindLeftover:
			todo = append(todo, c)
		}
	}
	if len(todo) == 0 {
		return rep, nil
	}

	if err := checkDissociateSpace(mdir, opts.Instance, todo); err != nil {
		return rep, err
	}

	if err := prepareEmptyHooks(opts.Home); err != nil {
		return rep, fmt.Errorf("nothing was changed: preparing the empty hooks directory: %w", err)
	}
	ctx = withEmptyHooks(ctx, emptyHooksDir(opts.Home))

	// Finish what an earlier run left: a clone whose alternates file is
	// still aside either passes git fsck without it and is done, or gets its
	// file back and is copied and dropped below like any other borrower.
	var borrowers []dissociateClone
	for _, c := range todo {
		if c.kind != kindLeftover {
			borrowers = append(borrowers, c)
			continue
		}
		done, err := finishLeftover(ctx, c)
		if err != nil {
			return rep, err
		}
		if done {
			rep.Changed = append(rep.Changed, c.rel)
			continue
		}
		rep.Restored = append(rep.Restored, c.rel)
		borrowers = append(borrowers, c)
	}

	// Copy phase: every borrower, its alternates file left in place, before
	// anything is dropped. A clone already copied when a later one fails
	// just holds duplicates of what it borrows.
	for _, c := range borrowers {
		if err := copyBorrowed(ctx, c); err != nil {
			return rep, fmt.Errorf("copying the borrowed objects into %s failed, so every clone still borrows: %w", c.rel, err)
		}
	}

	// Verify and drop, clone by clone. A failure puts that clone's file back
	// and stops: the clones before it are fully independent, the ones after
	// it still borrow.
	for _, c := range borrowers {
		if err := verifyAndDrop(ctx, c); err != nil {
			return rep, err
		}
		rep.Changed = append(rep.Changed, c.rel)
	}
	sort.Strings(rep.Changed)
	return rep, nil
}

// classifyForDissociate sorts the instance's clones into the kinds
// Dissociate acts on, reading each alternates file strictly
// (hostdir.ReadAlternatesStrict). Only a directory whose .git is a real
// directory is a clone here; anything else is skipped. A clone holding both
// an alternates file and a leftover one, or a file that can't be read for
// certain, is an error, returned before anything changes.
func classifyForDissociate(opts DissociateOptions, mdir string) ([]dissociateClone, error) {
	mdirReal, err := filepath.EvalSymlinks(mdir)
	if err != nil {
		mdirReal = ""
	}
	var out []dissociateClone
	clones := append([]string(nil), opts.Clones...)
	sort.Strings(clones)
	for _, dir := range clones {
		info, err := os.Lstat(filepath.Join(dir, ".git"))
		if err != nil || !info.IsDir() {
			continue
		}
		c := dissociateClone{dir: dir, rel: relClone(opts.Instance, dir)}
		altPath := filepath.Join(c.infoDir(), "alternates")
		asidePath := filepath.Join(c.infoDir(), hostdir.AlternatesAside)
		hasAlt := exists(altPath)
		hasAside := exists(asidePath)
		var path string
		switch {
		case hasAlt && hasAside:
			return nil, fmt.Errorf("nothing was changed: %s has both an alternates file and %s; remove whichever is stale and run niwa dissociate again", CleanText(c.rel), hostdir.AlternatesAside)
		case hasAlt:
			c.kind, path = kindBorrower, altPath
		case hasAside:
			c.kind, path = kindLeftover, asidePath
		default:
			c.kind = kindNeverBorrowed
			out = append(out, c)
			continue
		}
		lines, err := hostdir.ReadAlternatesStrict(path)
		if err != nil {
			return nil, fmt.Errorf("nothing was changed: reading the alternates of %s: %w", CleanText(c.rel), err)
		}
		if len(lines) == 0 && c.kind == kindBorrower {
			// An alternates file naming nothing borrows nothing; git reads
			// the clone the same without it, and so does this run.
			c.kind = kindNeverBorrowed
			out = append(out, c)
			continue
		}
		objects := filepath.Join(c.dir, ".git", "objects")
		for _, line := range lines {
			id, inside := mirrorLine(objects, line, mdir, mdirReal)
			if !inside {
				c.kind, c.outside = kindOutside, line
				break
			}
			if c.repoID == "" {
				c.repoID = id
			}
		}
		if c.repoID == "" {
			c.repoID = filepath.Base(c.dir)
		}
		out = append(out, c)
	}
	return out, nil
}

// mirrorLine reports whether one alternates line of a clone whose objects
// directory is objects lies inside this workspace's mirrors directory mdir
// (mdirReal resolved, empty when it doesn't resolve), and the repo id its
// first path element under mdir names. A relative line is joined to the
// clone's objects directory, as git reads it. The line counts as inside when
// either its cleaned path is under mdir or what it resolves to is under
// mdirReal, so a borrower whose mirror was deleted still counts as one (its
// copy then fails cleanly) and macOS's two spellings of the temp directory
// still match.
func mirrorLine(objects, line, mdir, mdirReal string) (string, bool) {
	p := strings.TrimSpace(line)
	if !filepath.IsAbs(p) {
		p = filepath.Join(objects, p)
	}
	p = filepath.Clean(p)
	if id, ok := under(mdir, p); ok {
		return id, true
	}
	if mdirReal == "" {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	return under(mdirReal, resolved)
}

// under reports whether p lies inside dir, and the first element of p's path
// below dir when that names a repo's stable path rather than one of niwa's
// own entries (.store, .meta).
func under(dir, p string) (string, bool) {
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	first, _, _ := strings.Cut(rel, string(filepath.Separator))
	if strings.HasPrefix(first, ".") {
		return "", true
	}
	return first, true
}

// checkDissociateSpace refuses the run, before anything changes, when free
// space on the instance's volume is below the sum of spaceNeeded over the
// distinct repos todo's clones borrow from: the pack sizes the mirror state
// files record, or minFreeUnknown for a repo with none recorded. A free-space
// read that fails doesn't block, as it doesn't block a restore: a copy that
// fills the disk fails cleanly, and nothing is dropped until every copy has
// succeeded.
func checkDissociateSpace(mdir, instance string, todo []dissociateClone) error {
	seen := map[string]bool{}
	var need uint64
	for _, c := range todo {
		if seen[c.repoID] {
			continue
		}
		seen[c.repoID] = true
		need += spaceNeeded(mdir, c.repoID)
	}
	free, err := freeSpace(instance)
	if err != nil {
		return nil
	}
	if free < need {
		return fmt.Errorf("nothing was changed: not enough free space to copy the borrowed objects: %d bytes free, %d needed", free, need)
	}
	return nil
}

// finishLeftover finishes a clone an earlier run left with its alternates
// file set aside: git fsck with the file still aside, then the file removed
// on a pass (done is true), or renamed back to alternates on a failure (done
// is false, and the clone borrows again).
func finishLeftover(ctx context.Context, c dissociateClone) (bool, error) {
	aside := filepath.Join(c.infoDir(), hostdir.AlternatesAside)
	if _, err := gitExec(ctx, nil, nil, "", "--git-dir="+c.gitDir(), "fsck", "--no-progress"); err == nil {
		if err := os.Remove(aside); err != nil {
			return false, fmt.Errorf("removing the leftover %s of %s: %w", hostdir.AlternatesAside, CleanText(c.rel), err)
		}
		_ = syncDir(c.infoDir())
		return true, nil
	}
	if err := os.Rename(aside, filepath.Join(c.infoDir(), "alternates")); err != nil {
		return false, fmt.Errorf("putting back the alternates file of %s: %w", CleanText(c.rel), err)
	}
	_ = syncDir(c.infoDir())
	return false, nil
}

// copyBorrowed writes every object the clone reads, through its alternates
// file or its own store, into the clone's own pack: git repack -a -d with no
// -l, as git clone --dissociate does. pack-objects is given every ref, every
// reflog and every index, the linked worktrees' HEADs and indexes among
// them, so a worktree of the clone, which shares its object store, is
// covered too.
func copyBorrowed(ctx context.Context, c dissociateClone) error {
	if err := testhook.Fault(testhook.LabelDissociateCopy); err != nil {
		return err
	}
	if err := testhook.Fault(testhook.LabelDissociateCopy + ":" + c.rel); err != nil {
		return err
	}
	if dissociateStage != nil {
		if err := dissociateStage("copy", c.dir); err != nil {
			return err
		}
	}
	_, err := gitExec(ctx, nil, nil, "", "--git-dir="+c.gitDir(), "repack", "-a", "-d", "-q")
	return err
}

// verifyAndDrop sets the clone's alternates file aside, which puts the
// mirror out of git's reach (git reads only that file and
// GIT_ALTERNATE_OBJECT_DIRECTORIES, which gitRun strips), runs a full git
// fsck, and removes the file on a pass. On a failure it renames the file
// back, so the clone borrows and reads as before, and returns the error.
func verifyAndDrop(ctx context.Context, c dissociateClone) error {
	alt := filepath.Join(c.infoDir(), "alternates")
	aside := filepath.Join(c.infoDir(), hostdir.AlternatesAside)
	if err := os.Rename(alt, aside); err != nil {
		return fmt.Errorf("setting the alternates file of %s aside: %w", CleanText(c.rel), err)
	}
	_ = syncDir(c.infoDir())
	var err error
	if dissociateStage != nil {
		err = dissociateStage("verify", c.dir)
	}
	if err == nil {
		_, err = gitExec(ctx, nil, nil, "", "--git-dir="+c.gitDir(), "fsck", "--no-progress")
	}
	if err != nil {
		if rerr := os.Rename(aside, alt); rerr != nil {
			return fmt.Errorf("git fsck in %s failed with the mirror out of reach (%v), and putting its alternates file back failed too: %w", CleanText(c.rel), err, rerr)
		}
		_ = syncDir(c.infoDir())
		return fmt.Errorf("git fsck in %s failed with the mirror out of reach, so its alternates file was put back: %w", CleanText(c.rel), err)
	}
	if err := os.Remove(aside); err != nil {
		return fmt.Errorf("removing the alternates file of %s once git fsck passed: %w", CleanText(c.rel), err)
	}
	_ = syncDir(c.infoDir())
	return nil
}

// relClone names clone by its path relative to instance, with forward
// slashes, or by its full path when it isn't under instance.
func relClone(instance, clone string) string {
	rel, err := filepath.Rel(instance, clone)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return clone
	}
	return filepath.ToSlash(rel)
}

// exists reports whether path has an entry, a dangling link included.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}
