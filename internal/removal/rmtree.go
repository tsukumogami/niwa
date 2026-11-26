package removal

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// The no-follow removal walk.
//
// A tree inside an instance is removed by descriptor, never by name: the
// walk starts from a descriptor for a directory the caller holds (the
// instance's), opens every directory on the way and in the tree relative to
// the one before it with O_DIRECTORY|O_NOFOLLOW, checks each descriptor with
// fstat against the type it read for that entry and against the starting
// directory's device, and removes every entry with unlinkat relative to its
// parent's descriptor. A link is removed as a link and never followed, so a
// directory swapped for a link to somewhere else after the walk read its
// type fails its open and stops the walk; nothing the link points to is
// touched.
//
// The walk reads the whole tree before it removes anything, and stops
// without removing anything on a directory on another device ("another
// filesystem at <path>"), on any .git entry or git directory (a HEAD beside
// objects/ and refs/) other than a worktree's own top-level .git file, and
// on any open or read that fails ("changed during the pass: <path>"). The
// removal then walks the tree again with the same checks and stops the same
// way, keeping whatever it hasn't reached.

// WalkStop is why the removal walk stopped. Path names the entry, relative
// to the directory the walk started from; Reason is the whole keep reason,
// with the path in it. Changed is true for an open, read or removal that
// failed, which a caller reports as a change during the pass; the others are
// a tree the walk won't remove (another filesystem, a repository, a locked
// worktree, a .git file it can't use).
type WalkStop struct {
	Path    string
	Reason  string
	Changed bool
}

func (s *WalkStop) Error() string { return s.Reason }

// WalkChanged reports whether err is a stop the walk made because something
// changed under it: an open, read or removal that failed.
func WalkChanged(err error) bool {
	var s *WalkStop
	return errors.As(err, &s) && s.Changed
}

// beforeOpen is called with the path of each directory the walk is about to
// open, after it read that entry's type and before the open. nil outside
// tests.
var beforeOpen func(path string)

// SetBeforeOpenHook installs h as the walk's test hook: it is called with
// the absolute path of each directory the walk is about to open, after the
// walk read the entry's type and before it opens it, so a test can swap the
// directory for a link in between. It returns a function that puts the
// previous hook back. Only tests call it.
func SetBeforeOpenHook(h func(path string)) (restore func()) {
	prev := beforeOpen
	beforeOpen = h
	return func() { beforeOpen = prev }
}

// treeWalk is one walk from a directory descriptor.
type treeWalk struct {
	// root is the path the starting descriptor was opened at, for the hook.
	root string
	// dev is the starting directory's device; every directory the walk
	// opens must be on it.
	dev uint64
	// remove is false for the read of the tree and true for its removal.
	remove bool
}

// changed is the stop for an open, read or removal at rel that failed.
func changed(rel string) error {
	return &WalkStop{Path: rel, Reason: changedDuringThePass + rel, Changed: true}
}

// repositoryAt is the stop for a repository the walk found at rel.
func repositoryAt(rel string) error {
	return &WalkStop{Path: rel, Reason: "a repository at " + rel}
}

// openDir opens the directory name under parentFd, rel naming it. st is the
// type the walk read for the entry, or nil to read it here. The entry must be
// a directory, open without following a link, be the directory the type read
// saw, and be on the walk's device.
func (w *treeWalk) openDir(parentFd int, name, rel string, st *unix.Stat_t) (int, error) {
	if st == nil {
		st = new(unix.Stat_t)
		if err := unix.Fstatat(parentFd, name, st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return -1, changed(rel)
		}
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return -1, changed(rel)
	}
	if beforeOpen != nil {
		beforeOpen(filepath.Join(w.root, filepath.FromSlash(rel)))
	}
	fd, err := retryEINTR(func() (int, error) { return unix.Openat(parentFd, name, dirOpenFlags, 0) })
	if err != nil {
		return -1, changed(rel)
	}
	var got unix.Stat_t
	if err := unix.Fstat(fd, &got); err != nil || !sameFile(st, &got) {
		unix.Close(fd)
		return -1, changed(rel)
	}
	if statDevice(&got) != w.dev {
		unix.Close(fd)
		return -1, &WalkStop{Path: rel, Reason: "another filesystem at " + rel}
	}
	return fd, nil
}

// openParent opens every directory on the way to rel under rootFd and
// returns a descriptor for rel's parent and rel's last component. The
// descriptor is rootFd itself when rel has one component; any other is the
// caller's to close.
func (w *treeWalk) openParent(rootFd int, rel string) (int, string, error) {
	parts := strings.Split(rel, "/")
	cur := rootFd
	shown := ""
	for _, part := range parts[:len(parts)-1] {
		shown = path.Join(shown, part)
		next, err := w.openDir(cur, part, shown, nil)
		if cur != rootFd {
			unix.Close(cur)
		}
		if err != nil {
			return -1, "", err
		}
		cur = next
	}
	return cur, parts[len(parts)-1], nil
}

// cleanRel checks rel is a clean relative path that stays below the
// starting directory.
func cleanRel(rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%q is not a path below the instance", rel)
	}
	return rel, nil
}

// isGitDirAt reports whether the directory open at fd is a git directory by
// its structure: a regular HEAD beside objects/ and refs/ directories, none
// of them a link.
func isGitDirAt(fd int) bool {
	var st unix.Stat_t
	if unix.Fstatat(fd, "HEAD", &st, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		return false
	}
	for _, sub := range []string{"objects", "refs"} {
		if unix.Fstatat(fd, sub, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
			return false
		}
	}
	return true
}

// walkDir walks the directory open at fd, rel naming it: it stops on a git
// directory or a .git entry (a regular .git file is let through when
// gitFile is set, for a worktree's top level, and is neither entered nor
// removed), and enters every directory. Removing, it removes each entry
// relative to fd, each directory once it is empty. The directory itself is
// left for the caller.
func (w *treeWalk) walkDir(fd int, rel string, gitFile bool) error {
	if isGitDirAt(fd) {
		return repositoryAt(rel)
	}
	names, err := readDirNames(fd)
	if err != nil {
		return changed(rel)
	}
	stats := make([]unix.Stat_t, len(names))
	for i, name := range names {
		child := path.Join(rel, name)
		if err := unix.Fstatat(fd, name, &stats[i], unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return changed(child)
		}
		if name == ".git" && !(gitFile && uint32(stats[i].Mode)&unix.S_IFMT == unix.S_IFREG) {
			return repositoryAt(child)
		}
	}
	for i, name := range names {
		if name == ".git" {
			continue // the worktree's own .git file, removed last
		}
		child := path.Join(rel, name)
		if uint32(stats[i].Mode)&unix.S_IFMT == unix.S_IFDIR {
			cfd, err := w.openDir(fd, name, child, &stats[i])
			if err != nil {
				return err
			}
			err = w.walkDir(cfd, child, false)
			unix.Close(cfd)
			if err != nil {
				return err
			}
			if w.remove {
				if err := unix.Unlinkat(fd, name, unix.AT_REMOVEDIR); err != nil {
					return changed(child)
				}
			}
			continue
		}
		if w.remove {
			// unlinkat with no flag removes a link as a link and refuses a
			// directory, so an entry swapped for a directory since its type
			// was read stops the walk here.
			if err := unix.Unlinkat(fd, name, 0); err != nil {
				return changed(child)
			}
		}
	}
	return nil
}

// RemoveTreeAt removes the directory rel names, relative to the directory
// open at rootFd, and everything in it, through the no-follow walk. root is
// the path rootFd was opened at; the walk opens nothing by it and names it
// only to the test hook. Every directory on the way to rel and in the tree
// must be on rootFd's device. The tree is read in full first and nothing is
// removed when that read stops; the removal then stops the same way at the
// first entry that changed. rootFd stays open.
func RemoveTreeAt(rootFd int, root, rel string) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return &WalkStop{Path: rel, Reason: err.Error()}
	}
	dev, err := fdDevice(rootFd)
	if err != nil {
		return changed(".")
	}
	w := &treeWalk{root: root, dev: dev}
	parent, name, err := w.openParent(rootFd, rel)
	if err != nil {
		return err
	}
	if parent != rootFd {
		defer unix.Close(parent)
	}
	fd, err := w.openDir(parent, name, rel, nil)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := w.walkDir(fd, rel, false); err != nil {
		return err
	}
	w.remove = true
	if err := w.walkDir(fd, rel, false); err != nil {
		return err
	}
	if err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil {
		return changed(rel)
	}
	return nil
}

// RemoveWorktree removes the linked worktree whose work tree is workTree and
// whose admin directory is adminDir, both inside instance, through the
// no-follow walk from a descriptor for the instance. It keeps the worktree,
// with the path named, when its top-level .git file is not a regular file
// (opened O_NOFOLLOW|O_NONBLOCK) or doesn't name adminDir, when the admin
// directory holds a locked file, or when the walk finds a repository below
// the tree's top level or a git directory in the admin directory, another
// filesystem, or anything that changed. Otherwise it removes the tree with
// its .git file last, then the admin directory, then worktrees/ if it is
// empty. It runs no git.
func RemoveWorktree(instance, workTree, adminDir string) error {
	return worktreeAt(instance, nil, workTree, adminDir, true)
}

// CheckWorktree is RemoveWorktree's read of the worktree and its admin
// directory with nothing removed: it returns what RemoveWorktree would stop
// on before its first removal.
func CheckWorktree(instance, workTree, adminDir string) error {
	return worktreeAt(instance, nil, workTree, adminDir, false)
}

// RemoveMovedWorktree is RemoveWorktree for an instance renamed to instance
// (its tombstone) from any of the paths in originals, with workTree and
// adminDir given at their paths inside instance. A worktree's .git file names
// its admin directory by the path git wrote, which the rename didn't change,
// so the file is matched against the admin directory under each original
// path, mapped onto instance, as well as under instance itself. Everything
// else is RemoveWorktree's: the same walk from a descriptor for instance, the
// same stops, and no git.
func RemoveMovedWorktree(instance string, originals []string, workTree, adminDir string) error {
	return worktreeAt(instance, originals, workTree, adminDir, true)
}

// worktreeAt opens the instance and runs the worktree's removal, or only its
// read, from that descriptor. originals are the paths the instance had
// before a rename, for the .git file's match. The instance is opened with
// O_NOFOLLOW, like every directory the walk opens: an instance or tombstone
// swapped for a link stops the walk with nothing removed, rather than
// sending it into the link's target.
func worktreeAt(instance string, originals []string, workTree, adminDir string, remove bool) error {
	wtRel, err := relBelow(instance, workTree)
	if err != nil {
		return &WalkStop{Path: workTree, Reason: "the worktree is not inside the instance: " + workTree}
	}
	adminRel, err := relBelow(instance, adminDir)
	if err != nil {
		return &WalkStop{Path: adminDir, Reason: "the worktree's admin directory is not inside the instance: " + adminDir}
	}
	fd, err := retryEINTR(func() (int, error) { return unix.Open(instance, dirOpenFlags, 0) })
	if err != nil {
		return changed(".")
	}
	defer unix.Close(fd)
	return removeWorktreeAt(fd, instance, originals, wtRel, adminRel, remove)
}

// relBelow is p relative to base, as text, refused when it isn't below it.
func relBelow(base, p string) (string, error) {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(p))
	if err != nil {
		return "", err
	}
	return cleanRel(rel)
}

// RemoveWorktreeAt is RemoveWorktree from the directory open at rootFd,
// root being the path it was opened at, with the work tree and the admin
// directory named relative to it. With remove false it reads and checks
// everything and removes nothing. rootFd stays open.
func RemoveWorktreeAt(rootFd int, root, wtRel, adminRel string, remove bool) error {
	return removeWorktreeAt(rootFd, root, nil, wtRel, adminRel, remove)
}

// removeWorktreeAt is RemoveWorktreeAt with the paths the instance had
// before a rename, for the .git file's match.
func removeWorktreeAt(rootFd int, root string, originals []string, wtRel, adminRel string, remove bool) error {
	wtRel, err := cleanRel(wtRel)
	if err != nil {
		return &WalkStop{Path: wtRel, Reason: err.Error()}
	}
	adminRel, err = cleanRel(adminRel)
	if err != nil {
		return &WalkStop{Path: adminRel, Reason: err.Error()}
	}
	dev, err := fdDevice(rootFd)
	if err != nil {
		return changed(".")
	}
	w := &treeWalk{root: root, dev: dev}

	// The admin directory, opened first: a locked worktree is kept before
	// its tree is read.
	aParent, aName, err := w.openParent(rootFd, adminRel)
	if err != nil {
		return err
	}
	if aParent != rootFd {
		defer unix.Close(aParent)
	}
	afd, err := w.openDir(aParent, aName, adminRel, nil)
	if err != nil {
		return err
	}
	defer unix.Close(afd)
	if err := lockedAt(afd, adminRel); err != nil {
		return err
	}

	// The work tree and its .git file, read through the shared reader: a
	// regular file of the user's, opened without following a link or
	// waiting on a FIFO, one line naming the admin directory.
	wParent, wName, err := w.openParent(rootFd, wtRel)
	if err != nil {
		return err
	}
	if wParent != rootFd {
		defer unix.Close(wParent)
	}
	wfd, err := w.openDir(wParent, wName, wtRel, nil)
	if err != nil {
		return err
	}
	defer unix.Close(wfd)
	gitFile := wtRel + "/.git"
	b, err := readCappedAt(wfd, ".git", maxGitFileBytes)
	if err != nil {
		return &WalkStop{Path: gitFile, Reason: "unreadable git file: " + gitFile + ": " + err.Error()}
	}
	line, ok := oneLine(b)
	target, ok2 := strings.CutPrefix(line, "gitdir: ")
	if !ok || !ok2 || !namesAdminDir(root, originals, wtRel, adminRel, target) {
		return &WalkStop{Path: gitFile, Reason: "unreadable git file: " + gitFile + " doesn't name the worktree's admin directory " + adminRel}
	}

	// The read of both trees, then their removal.
	if err := w.walkDir(wfd, wtRel, true); err != nil {
		return err
	}
	if err := w.walkDir(afd, adminRel, false); err != nil {
		return err
	}
	if !remove {
		return nil
	}
	// The lock is read again before anything goes, and once more right
	// before the admin directory goes, so a lock taken while the tree is
	// read or removed keeps the admin directory.
	if err := lockedAt(afd, adminRel); err != nil {
		return err
	}
	w.remove = true
	if err := w.walkDir(wfd, wtRel, true); err != nil {
		return err
	}
	if err := unix.Unlinkat(wfd, ".git", 0); err != nil {
		return changed(gitFile)
	}
	if err := unix.Unlinkat(wParent, wName, unix.AT_REMOVEDIR); err != nil {
		return changed(wtRel)
	}
	if err := lockedAt(afd, adminRel); err != nil {
		return err
	}
	if err := w.walkDir(afd, adminRel, false); err != nil {
		return err
	}
	if err := unix.Unlinkat(aParent, aName, unix.AT_REMOVEDIR); err != nil {
		return changed(adminRel)
	}
	// worktrees/ goes when the admin directory was its last entry.
	if trees := path.Dir(adminRel); path.Base(trees) == "worktrees" && trees != "." {
		gParent, gName, err := w.openParent(rootFd, trees)
		if err == nil {
			err := unix.Unlinkat(gParent, gName, unix.AT_REMOVEDIR)
			if gParent != rootFd {
				unix.Close(gParent)
			}
			if err != nil && !errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) {
				return changed(trees)
			}
		}
	}
	return nil
}

// lockedAt is the stop for a locked file in the admin directory open at
// afd, adminRel naming it: a locked worktree is kept, and a read of the
// entry that fails is a change.
func lockedAt(afd int, adminRel string) error {
	var st unix.Stat_t
	switch err := unix.Fstatat(afd, "locked", &st, unix.AT_SYMLINK_NOFOLLOW); {
	case err == nil:
		return &WalkStop{Path: adminRel + "/locked", Reason: "the worktree is locked: " + adminRel + "/locked"}
	case !errors.Is(err, unix.ENOENT):
		return changed(adminRel + "/locked")
	}
	return nil
}

// namesAdminDir reports whether target, the path a worktree's .git file
// names, is its admin directory: as text, resolved from the work tree, in
// the form root was given in or with its links resolved, the form git
// writes, or under any of originals, the paths the instance at root had
// before a rename (each in the form the caller read it in while the
// instance was still there).
func namesAdminDir(root string, originals []string, wtRel, adminRel, target string) bool {
	if target == "" {
		return false
	}
	bases := []string{filepath.Clean(root), canonical(root)}
	for _, o := range originals {
		if o != "" {
			bases = append(bases, filepath.Clean(o))
		}
	}
	for _, base := range bases {
		got := resolveFrom(filepath.Join(base, filepath.FromSlash(wtRel)), target)
		if got == filepath.Join(base, filepath.FromSlash(adminRel)) {
			return true
		}
	}
	return false
}

// NoFollowRemover is the no-follow walk's worktree removal as a value, for
// a caller that takes a worktree remover by interface (the worktree
// package's WorktreeRemover) without importing this package.
var NoFollowRemover noFollowRemover

type noFollowRemover struct{}

// RemoveWorktree is RemoveWorktree.
func (noFollowRemover) RemoveWorktree(instance, workTree, adminDir string) error {
	return RemoveWorktree(instance, workTree, adminDir)
}
