package removal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/tsukumogami/niwa/internal/testhook"
)

// The re-read before removal and the check on the tombstone.
//
// The rule's verdict is read once, early in a pass, and a unit is removed
// seconds later: after the gate, the on-reap command and the rename to its
// tombstone. A writer that arrives in between could change a repository or
// a record and have that change deleted unseen. So after the gate allows a
// removal the pass reads every repository the walk found again (a fresh
// configuration check, git status, the index flags and a prunable
// worktree's staged entries), compares that with what the rule read, and
// records a snapshot of each repository's refs, HEADs and stashes and a
// hash of every record under .niwa. On the tombstone it takes the same
// snapshot again and compares the two. Any difference keeps the unit.
//
// Every entry is keyed by its path relative to the instance root, or
// "job:"-prefixed in the job directory, so the same repository read under
// <root>/<instance> and under the tombstone compares as one. Every git call
// goes through gitCommand.

// RepoRead is one repository the walk found and what the rule read in it,
// carried on the verdict so the re-read and the tombstone check read the
// same repositories without a second walk.
type RepoRead struct {
	// Rel names the repository: its work tree, or its git directory when
	// it has none, relative to the instance, or "job:"-prefixed in the job
	// directory.
	Rel string

	dirs repoDirs
	// base is the root the walk found it under, in the form the walk used;
	// inInstance is true when that root is the instance, so its paths move
	// with a rename of the instance.
	base       string
	inInstance bool
	linked     bool
	prunable   bool
	// wtInfo and gitInfo are what the walk saw at its directories; a rename
	// keeps both, so a read on the tombstone checks the directories are
	// still the ones the walk found.
	wtInfo, gitInfo fs.FileInfo

	// read is true when every read below ran without an error: a
	// repository whose reads didn't finish can't be compared, and keeps.
	read   bool
	status []statusEntry
	index  []indexEntry
	staged []string
}

// Linked reports whether the repository is a linked worktree's admin
// directory with its work tree.
func (r RepoRead) Linked() bool { return r.linked }

// InInstance reports whether the repository sits in the instance, so it
// moves with a rename of the instance, rather than in the job directory.
func (r RepoRead) InInstance() bool { return r.inInstance }

// Prunable reports whether the repository is a worktree whose working tree
// is gone, judged through its admin directory.
func (r RepoRead) Prunable() bool { return r.prunable }

// repoState is one repository as one snapshot read it.
type repoState struct {
	workTree bool
	// program is the key the snapshot's own configuration read flagged.
	program string
	// rechecked is why the configuration read right before the snapshot's
	// status kept the repository, a key written after the snapshot's own
	// read; the status wasn't started.
	rechecked string
	status    []string
	index     []string
	staged    []string
	refs      map[string]string
	worktrees map[string]string
	stashes   []string
}

// Snapshot is a unit's repositories and records as one read saw them.
type Snapshot struct {
	repos map[string]*repoState
	// records maps each record under .niwa, by its path relative to the
	// instance, to a hash of its content or a note of what is there.
	records map[string]string
	// failed names every read that didn't finish. A failed read can't be
	// compared, so it counts as a difference.
	failed []string
}

// Failed returns the reads the snapshot couldn't make, each naming the
// repository or record and the error.
func (s Snapshot) Failed() []string { return append([]string(nil), s.failed...) }

// maxRecordHashBytes bounds how much of one record under .niwa is hashed. A
// larger file is noted by its size and modification time instead.
const maxRecordHashBytes = 8 << 20

// maxRecordEntries bounds how many entries the record hash reads under
// .niwa; more fails the read, which keeps.
const maxRecordEntries = 4096

// TakeSnapshot reads the unit the verdict v describes, with the instance
// that was at instancePath when the rule ran now found at at: the instance's
// own path for the re-read, or its tombstone. Each repository in v.Repos
// whose Rel is in skip is left out; the worktrees a pass removes in between
// are named there. Repositories in the job directory are read where they
// are.
//
// For every repository it checks the directories are still the ones the
// walk found (the same device and inode, no link on the way down), runs a
// fresh configuration check, and reads git status and the index flags of a
// work tree, the staged entries of a worktree whose working tree is gone,
// every ref for-each-ref lists, each worktree's HEAD from git worktree list
// and the stash list. It hashes every record under .niwa at at.
func (e *Env) TakeSnapshot(ctx context.Context, v Verdict, instancePath, at string, skip []string) Snapshot {
	s := Snapshot{repos: map[string]*repoState{}}
	skipped := map[string]bool{}
	for _, rel := range skip {
		skipped[rel] = true
	}
	lab := newSnapshotLabeler(v, instancePath, at)
	moved := filepath.Clean(instancePath) != filepath.Clean(at)
	for _, r := range v.Repos {
		if skipped[r.Rel] {
			continue
		}
		if moved && r.inInstance && r.linked && !r.prunable {
			// A linked worktree's .git file names its admin directory by
			// absolute path, which the rename moved. Its repository can't
			// be read the way it was until the worktree is removed before
			// the rename, so it counts as changed.
			s.failed = append(s.failed, r.Rel+": a linked worktree names its admin directory by a path the rename moved")
			continue
		}
		st, err := e.readRepoState(ctx, r, instancePath, at, lab)
		if err != nil {
			s.failed = append(s.failed, r.Rel+": "+err.Error())
			continue
		}
		s.repos[r.Rel] = st
	}
	records, err := hashRecords(at)
	if err != nil {
		s.failed = append(s.failed, niwaDirName+": "+errText(err))
	} else {
		s.records = records
	}
	return s
}

// rebase moves p from under from to under to, when it sits under from.
func rebase(p, from, to string) string {
	if p == "" || from == "" {
		return p
	}
	p, from = filepath.Clean(p), filepath.Clean(from)
	if !within(p, from) {
		return p
	}
	rel, err := filepath.Rel(from, p)
	if err != nil {
		return p
	}
	return filepath.Join(to, rel)
}

// readRepoState reads one repository for a snapshot.
func (e *Env) readRepoState(ctx context.Context, r RepoRead, instancePath, at string, lab snapshotLabeler) (*repoState, error) {
	if e == nil {
		return nil, errors.New("git can't be run for this pass")
	}
	dirs, base := r.dirs, r.base
	if r.inInstance {
		dirs.GitDir = rebase(rebase(dirs.GitDir, r.base, at), instancePath, at)
		dirs.WorkTree = rebase(rebase(dirs.WorkTree, r.base, at), instancePath, at)
		base = at
	}
	for _, d := range []struct {
		path     string
		recorded fs.FileInfo
	}{{dirs.WorkTree, r.wtInfo}, {dirs.GitDir, r.gitInfo}} {
		if d.path == "" {
			continue
		}
		if err := noLinkBelow(base, d.path); err != nil {
			return nil, fmt.Errorf("changed since the walk: %v", err)
		}
		info, err := os.Lstat(d.path)
		switch {
		case err != nil:
			return nil, fmt.Errorf("changed since the walk: %s", errText(err))
		case info.Mode()&fs.ModeSymlink != 0, !info.IsDir():
			return nil, fmt.Errorf("changed since the walk: %s is no longer a directory", relOrSelf(base, d.path))
		case d.recorded != nil && !os.SameFile(info, d.recorded):
			return nil, fmt.Errorf("changed since the walk: %s is not the directory the walk found", relOrSelf(base, d.path))
		}
	}

	rc, err := e.readConfig(ctx, dirs)
	if err != nil {
		return nil, err
	}
	key, _ := e.refusal(rc)
	st := &repoState{workTree: dirs.WorkTree != "", program: key}
	if key != "" {
		// Nothing else runs in a repository whose configuration names a
		// program; the flagged key is the difference.
		return st, nil
	}
	opts := callOpts{excludesUnset: rc.excludesUnset}
	if dirs.WorkTree != "" {
		testhook.RemovalBeforeStatus(testhook.StageReread, dirs.GitDir, dirs.WorkTree)
		entries, warn, err := repoStatus(ctx, e, dirs, opts)
		var refused *configRefused
		if errors.As(err, &refused) {
			// The re-check right before the status found a key written
			// since this read's own configuration read: the status wasn't
			// started, and the reason is the difference.
			st.rechecked = refused.why
			return st, nil
		}
		if err != nil {
			return nil, err
		}
		if warn != "" {
			return nil, fmt.Errorf("the status read reported a problem: %s", warn)
		}
		st.status = statusKeys(entries)
		index, err := listIndex(ctx, e, dirs, opts)
		if err != nil {
			return nil, err
		}
		st.index = indexKeys(index)
	}
	if r.prunable {
		staged, err := e.readPrunedIndex(ctx, dirs.GitDir)
		if err != nil {
			return nil, err
		}
		st.staged = sortedCopy(staged)
	}
	refs, warn, err := listRefs(ctx, e, dirs, opts)
	if err != nil {
		return nil, err
	}
	if warn != "" {
		return nil, fmt.Errorf("the ref listing reported a problem: %s", warn)
	}
	st.refs = map[string]string{}
	for _, rf := range refs {
		st.refs[rf.Name] = rf.OID
	}
	wt, err := e.readWorktrees(ctx, dirs, opts, lab)
	if err != nil {
		return nil, err
	}
	st.worktrees = wt
	stashes, err := listStashes(ctx, e, dirs, opts)
	if err != nil {
		return nil, err
	}
	st.stashes = stashes
	return st, nil
}

// readWorktrees runs git worktree list --porcelain -z and returns each
// worktree's record, keyed by its path relative to the instance (or
// "job:"-prefixed), the value being every field git printed for it: its
// HEAD, its branch or detached state, and its bare, locked and prunable
// flags.
func (e *Env) readWorktrees(ctx context.Context, r repoDirs, o callOpts, lab snapshotLabeler) (map[string]string, error) {
	cmd, err := e.gitCommand(ctx, r, o, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %v", err)
	}
	// The listing is parsed by the rule's own parser first, so a record it
	// refuses fails the read here too.
	if _, err := parseWorktreeEntries(out); err != nil {
		return nil, err
	}
	wt := map[string]string{}
	for _, block := range strings.Split(string(out[:len(out)-2]), "\x00\x00") {
		fields := strings.Split(block, "\x00")
		path := strings.TrimPrefix(fields[0], "worktree ")
		rest := append([]string(nil), fields[1:]...)
		sort.Strings(rest)
		wt[lab.label(path)] = strings.Join(rest, "; ")
	}
	return wt, nil
}

// snapshotLabeler names a path git printed by where it sits: relative to
// the instance in any form it has had in this pass, "job:"-prefixed in a
// job directory, as given anywhere else.
type snapshotLabeler struct {
	instance []string
	job      []string
}

func newSnapshotLabeler(v Verdict, instancePath, at string) snapshotLabeler {
	var l snapshotLabeler
	add := func(list *[]string, p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		for _, have := range *list {
			if have == p {
				return
			}
		}
		*list = append(*list, p)
	}
	add(&l.instance, instancePath)
	add(&l.instance, at)
	add(&l.instance, canonical(at))
	for _, r := range v.Repos {
		if r.inInstance {
			add(&l.instance, r.base)
		} else {
			add(&l.job, r.base)
		}
	}
	return l
}

func (l snapshotLabeler) label(p string) string {
	p = filepath.Clean(p)
	for _, list := range []struct {
		bases  []string
		prefix string
	}{{l.instance, ""}, {l.job, jobPrefix}} {
		for _, base := range list.bases {
			if !within(p, base) {
				continue
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if list.prefix == "" {
				return rel
			}
			if rel == "." {
				return list.prefix
			}
			return list.prefix + rel
		}
	}
	return p
}

// statusKeys and indexKeys turn the status and index reads into sorted
// comparable lines.
func statusKeys(entries []statusEntry) []string {
	out := make([]string, 0, len(entries))
	for _, en := range entries {
		out = append(out, en.Kind+" "+en.Path)
	}
	sort.Strings(out)
	return out
}

func indexKeys(entries []indexEntry) []string {
	out := make([]string, 0, len(entries))
	for _, en := range entries {
		out = append(out, string(en.Tag)+" "+en.Path)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// RuleDifferences compares the re-read s with what the rule read for v and
// returns every difference, each naming the repository and the path or key
// that differs. A read the rule didn't finish, a read the re-read couldn't
// make, or a flagged key is a difference too.
func (s Snapshot) RuleDifferences(v Verdict) []string {
	var out []string
	out = append(out, s.failed...)
	for _, r := range v.Repos {
		st, ok := s.repos[r.Rel]
		if !ok {
			continue // a failed read, already named
		}
		if !r.read {
			out = append(out, r.Rel+": the rule's reads of it didn't finish")
			continue
		}
		if st.program != "" {
			out = append(out, "key "+st.program+" in "+r.Rel)
			continue
		}
		if st.rechecked != "" {
			out = append(out, st.rechecked+" in "+r.Rel)
			continue
		}
		out = append(out, pathDiffs(r.Rel, statusKeys(r.status), st.status)...)
		out = append(out, pathDiffs(r.Rel, indexKeys(r.index), st.index)...)
		out = append(out, pathDiffs(r.Rel, sortedCopy(r.staged), st.staged)...)
	}
	return out
}

// CompareSnapshots compares a later snapshot with an earlier one and
// returns every difference. A repository in the earlier snapshot that the
// later one lacks, a flagged key, a status, index or staged entry that is
// new or gone, a ref that is new, has moved or is gone, a stash entry that
// is new or gone, a worktree whose HEAD, branch or flags changed, a record
// under .niwa that is new, changed or gone, and every read the later
// snapshot couldn't make is a difference.
//
// lost names the repositories a pass removed in between (the worktrees it
// removes before the rename); their entries may be gone from the later
// snapshot, as repositories and as entries in another repository's
// worktree listing. It is empty until the pass removes worktrees.
func CompareSnapshots(before, after Snapshot, lost []string) []string {
	gone := map[string]bool{}
	for _, rel := range lost {
		gone[rel] = true
	}
	var out []string
	out = append(out, after.failed...)
	var rels []string
	for rel := range before.repos {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		b := before.repos[rel]
		a, ok := after.repos[rel]
		if !ok {
			if !gone[rel] && !failedNames(after.failed, rel) {
				out = append(out, rel+": gone")
			}
			continue
		}
		if a.program != "" {
			out = append(out, "key "+a.program+" in "+rel)
			continue
		}
		if a.rechecked != "" {
			out = append(out, a.rechecked+" in "+rel)
			continue
		}
		out = append(out, pathDiffs(rel, b.status, a.status)...)
		out = append(out, pathDiffs(rel, b.index, a.index)...)
		out = append(out, pathDiffs(rel, b.staged, a.staged)...)
		out = append(out, mapDiffs("ref ", rel, b.refs, a.refs, nil)...)
		out = append(out, mapDiffs("worktree ", rel, b.worktrees, a.worktrees, gone)...)
		out = append(out, listDiffs("stash ", rel, b.stashes, a.stashes)...)
	}
	for rel := range after.repos {
		if _, ok := before.repos[rel]; !ok {
			out = append(out, rel+": a repository the earlier read didn't see")
		}
	}
	out = append(out, mapDiffs("", "", before.records, after.records, nil)...)
	return out
}

// failedNames reports whether a failed read names the repository rel.
func failedNames(failed []string, rel string) bool {
	for _, f := range failed {
		if strings.HasPrefix(f, rel+": ") {
			return true
		}
	}
	return false
}

// pathDiffs names each entry in one sorted list and not the other, by the
// path it carries after its tag.
func pathDiffs(rel string, before, after []string) []string {
	var out []string
	b := map[string]bool{}
	for _, k := range before {
		b[k] = true
	}
	a := map[string]bool{}
	for _, k := range after {
		a[k] = true
	}
	name := func(k string) string {
		_, p, _ := strings.Cut(k, " ")
		if rel == "." || rel == "" {
			return p
		}
		return rel + "/" + p
	}
	for _, k := range after {
		if !b[k] {
			out = append(out, name(k))
		}
	}
	for _, k := range before {
		if !a[k] {
			out = append(out, name(k)+" (gone)")
		}
	}
	return dedupe(out)
}

// mapDiffs names each key that is new, changed or gone between two maps,
// as "<kind><key> in <rel>". A key whose entry may be lost is allowed to
// be gone.
func mapDiffs(kind, rel string, before, after map[string]string, lost map[string]bool) []string {
	var keys []string
	seen := map[string]bool{}
	for k := range before {
		keys = append(keys, k)
		seen[k] = true
	}
	for k := range after {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	where := ""
	if rel != "" {
		where = " in " + rel
	}
	var out []string
	for _, k := range keys {
		bv, inBefore := before[k]
		av, inAfter := after[k]
		switch {
		case inBefore && !inAfter:
			if lost[k] {
				continue
			}
			out = append(out, kind+k+where+" (gone)")
		case !inBefore && inAfter:
			out = append(out, kind+k+where+" (new)")
		case bv != av:
			out = append(out, kind+k+where)
		}
	}
	return out
}

// listDiffs names each entry new to or gone from a list.
func listDiffs(kind, rel string, before, after []string) []string {
	b := map[string]string{}
	for _, k := range before {
		b[k] = ""
	}
	a := map[string]string{}
	for _, k := range after {
		a[k] = ""
	}
	return mapDiffs(kind, rel, b, a, nil)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// hashRecords hashes every record under at/.niwa: each regular file
// directly in it and in its pids directory, through the shared no-follow,
// owner-checked reader. Any other entry is noted by its type, so a mark
// file, a link or a directory added later is a difference too. A .niwa
// that isn't there is noted as absent; one the rule can't use fails the
// read.
func hashRecords(at string) (map[string]string, error) {
	d := openNiwaDir(at)
	defer d.close()
	if d.absent {
		return map[string]string{niwaDirName: "absent"}, nil
	}
	if err := d.usable(); err != nil {
		return nil, err
	}
	out := map[string]string{}
	count := 0
	if err := hashDirAt(d.fd, niwaDirName, out, &count, true); err != nil {
		return nil, err
	}
	return out, nil
}

// hashDirAt notes every entry of the directory open at dirfd under label,
// descending into pids when descend is set.
func hashDirAt(dirfd int, label string, out map[string]string, count *int, descend bool) error {
	names, err := readDirNames(dirfd)
	if err != nil {
		return fmt.Errorf("%s: %v", label, err)
	}
	for _, name := range names {
		*count++
		if *count > maxRecordEntries {
			return fmt.Errorf("%s holds more than %d entries", niwaDirName, maxRecordEntries)
		}
		key := label + "/" + name
		var st unix.Stat_t
		if err := unix.Fstatat(dirfd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("%s: %v", key, err)
		}
		switch uint32(st.Mode) & unix.S_IFMT {
		case unix.S_IFREG:
			out[key] = hashRecordAt(dirfd, name, key, &st)
		case unix.S_IFDIR:
			if descend && name == "pids" {
				fd, err := retryEINTR(func() (int, error) { return unix.Openat(dirfd, name, dirOpenFlags, 0) })
				if err != nil {
					return openError(key, err)
				}
				var got unix.Stat_t
				err = unix.Fstat(fd, &got)
				if err == nil && !sameFile(&st, &got) {
					err = fmt.Errorf("%s changed while it was opened", key)
				}
				if err == nil {
					err = hashDirAt(fd, key, out, count, false)
				}
				unix.Close(fd)
				if err != nil {
					return err
				}
				out[key] = "directory"
				continue
			}
			out[key] = fmt.Sprintf("directory, owner %d", st.Uid)
		case unix.S_IFLNK:
			buf := make([]byte, 4096)
			n, err := unix.Readlinkat(dirfd, name, buf)
			if err != nil {
				out[key] = "symlink, unreadable"
			} else {
				out[key] = "symlink to " + string(buf[:n])
			}
		default:
			out[key] = fmt.Sprintf("type %o", uint32(st.Mode)&unix.S_IFMT)
		}
	}
	return nil
}

// hashRecordAt hashes the regular file name in the directory open at dirfd
// through the shared reader's open and checks. What it can't read is noted
// with the reason, which compares like any other value.
func hashRecordAt(dirfd int, name, key string, lst *unix.Stat_t) string {
	if int64(lst.Uid) != int64(currentUID()) {
		return fmt.Sprintf("another user's file (uid %d), size %d, modified %d", lst.Uid, lst.Size, lst.Mtim.Nano())
	}
	if lst.Size > maxRecordHashBytes {
		return fmt.Sprintf("size %d, modified %d", lst.Size, lst.Mtim.Nano())
	}
	b, err := readCappedAt(dirfd, name, maxRecordHashBytes)
	if err != nil {
		return "unreadable: " + errText(err)
	}
	sum := sha256.Sum256(b)
	return "sha256 " + hex.EncodeToString(sum[:])
}

// readDirNames lists the directory open at dirfd, sorted, without closing
// the descriptor.
func readDirNames(dirfd int) ([]string, error) {
	dup, err := unix.Dup(dirfd)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(dup), "dir")
	defer f.Close()
	if _, err := unix.Seek(dup, 0, 0); err != nil {
		return nil, err
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}
