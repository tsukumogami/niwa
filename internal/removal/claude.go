package removal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// claudeOverrideFlags are the claude rm flags that make Claude Code delete
// what it would otherwise refuse to. niwa never passes either.
var claudeOverrideFlags = []string{"--discard-unpushed", "--force-remove-worktree"}

// NewClaudeEnv resolves what a claude call on the removal path needs and
// nothing else: the PATH filtered against root, claude on it, and a fresh
// scratch directory under home. It is NewEnv for a command that runs no git,
// so it reads no credential helpers. A scratch directory that can't be made
// is recorded, and every claude call then refuses.
func NewClaudeEnv(root, home, pathEnv string) *Env {
	canonRoot := canonical(root)
	dirs := filterPATH(pathEnv, canonRoot)
	e := &Env{
		Root:     canonRoot,
		Home:     home,
		PATH:     strings.Join(dirs, string(os.PathListSeparator)),
		Claude:   lookIn(dirs, "claude", canonRoot),
		lsRemote: map[string]lsRemoteResult{},
	}
	e.Scratch, e.ScratchErr = NewScratch(home, canonRoot)
	return e
}

// ClaudeCommand builds a claude call made on the removal path: by the
// absolute path resolved from the filtered PATH, with that PATH in its
// environment, run from the empty claude directory under the pass's scratch
// directory, checked for owner and mode first, so no instance's project
// settings or hooks apply to the call. The rest of the environment is the
// person's, since claude needs their authentication. It refuses either of
// claude rm's override flags. The process runs in its own group, which the
// context's end kills.
func (e *Env) ClaudeCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	if e == nil || e.Claude == "" {
		return nil, errors.New("no claude on the filtered PATH")
	}
	for _, a := range args {
		for _, flag := range claudeOverrideFlags {
			if a == flag || strings.HasPrefix(a, flag+"=") {
				return nil, fmt.Errorf("refusing to pass %s to claude", flag)
			}
		}
	}
	dir, err := e.scratchDir(scratchClaude)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, e.Claude, args...)
	cmd.Dir = dir
	cmd.Env = replaceEnv(os.Environ(), "PATH", e.PATH)
	groupKill(cmd)
	return cmd, nil
}

// replaceEnv returns env with name set to value, every earlier entry for
// name dropped.
func replaceEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == name {
			continue
		}
		out = append(out, kv)
	}
	return append(out, name+"="+value)
}

// JobDirFor is the job directory of a session under jobsRoot: the directory
// named by the id, or else the first, by name, whose name is a leading slice
// of the id at least 8 characters long, the layout Claude Code writes. Empty
// when the session has none, and for an id that is empty, "." or "..", or
// holds a path separator or a NUL.
func JobDirFor(jobsRoot, id string) string {
	if jobsRoot == "" || !validJobID(id) {
		return ""
	}
	full := filepath.Join(jobsRoot, id)
	if _, err := os.Lstat(full); err == nil {
		return full
	}
	entries, err := os.ReadDir(jobsRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) >= 8 && len(name) <= len(id) && id[:len(name)] == name {
			return filepath.Join(jobsRoot, name)
		}
	}
	return ""
}

// vetJobDirFor is JobDirFor for the vet alone: the directory named by the
// id, or else the one directory whose name is a leading slice of the id at
// least 8 characters long. Where JobDirFor takes the first of several such
// directories, the vet can't tell which one Claude Code will remove, so it
// fails naming the jobs root. Empty, with no error, when there is none.
func vetJobDirFor(jobsRoot, id string) (string, error) {
	full := filepath.Join(jobsRoot, id)
	if _, err := os.Lstat(full); err == nil {
		return full, nil
	}
	entries, err := os.ReadDir(jobsRoot)
	if err != nil {
		return "", vetFail(jobsRoot, "the jobs root can't be listed at %s: %s", jobsRoot, errText(err))
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if len(name) >= 8 && len(name) <= len(id) && id[:len(name)] == name {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return filepath.Join(jobsRoot, found[0]), nil
	}
	return "", vetFail(jobsRoot, "more than one job directory in %s matches session %s (%s)", jobsRoot, id, strings.Join(found, ", "))
}

func validJobID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\\x00")
}

// VetError is a job-directory vet that failed: the path it names, the same
// on every run over the same tree, and why.
type VetError struct {
	Path   string
	Reason string
}

func (e *VetError) Error() string { return e.Reason }

// VetJobDir is the vet that runs before every claude rm. Claude Code deletes
// a session's job directory by its own means, which niwa can't see, so the
// directory is checked first and a failure means no call is made.
//
// jobsRoot is the jobs root removal.ResolveJobsRoot gives, home the person's
// home directory, and id the session's full id as claude agents --json --all
// lists it (sessionId), the id the rule's walk finds the job directory by.
// The vet:
//
//   - finds the job directory as JobDirFor does, and fails naming the path
//     it looked for when there is none, and naming the jobs root when more
//     than one directory's name is a leading slice of the id;
//   - checks the chain to the jobs root: below the home directory, which may
//     itself be a link, no component may be a link, and a jobs root outside
//     the home is checked the same way from its config directory down;
//   - Lstats the jobs root and the job directory: each must be a real
//     directory, not a link, owned by the person's uid, with no group or
//     other write permission, and the job directory must sit on the jobs
//     root's device;
//   - walks the job directory with Lstat, follows no link, reads each
//     directory's entries sorted by name, and fails on the first entry that
//     is neither a regular file nor a directory, on an entry on another
//     device ("another filesystem at <path>"), and on any Lstat or directory
//     read that fails.
//
// A context that ends before the vet does fails it too. Every failure is a
// *VetError.
func VetJobDir(ctx context.Context, jobsRoot, home, id string) error {
	return defaultJobDirVet().run(ctx, jobsRoot, home, id)
}

// jobDirVet is the vet with its file-system reads as seams, so a test can
// make the other-uid and other-device cases a test host can't make without
// root.
type jobDirVet struct {
	// lstat reads one path without following a link.
	lstat func(string) (os.FileInfo, error)
	// ids reads the owner and the device from what lstat returned.
	ids func(os.FileInfo) (uid uint32, dev uint64, ok bool)
	// uid is the person's uid.
	uid uint32
	// readNames lists a directory's entries, opened without following a
	// link and checked to be the directory lstat read.
	readNames func(dir string, want os.FileInfo) ([]string, error)
}

func defaultJobDirVet() jobDirVet {
	return jobDirVet{lstat: os.Lstat, ids: statIDs, uid: uint32(os.Getuid()), readNames: vetDirNames}
}

// statIDs reads the owner's uid and the device from a stat result.
func statIDs(info os.FileInfo) (uint32, uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return st.Uid, uint64(st.Dev), true
}

// vetDirNames opens dir with O_NOFOLLOW|O_DIRECTORY, checks it is the
// directory want describes (same device and inode), and returns its entry
// names sorted.
func vetDirNames(dir string, want os.FileInfo) ([]string, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), dir)
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !sameInode(opened, want) {
		return nil, errors.New("it changed while it was read")
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// sameInode reports whether a and b describe the same file, by device and
// inode.
func sameInode(a, b os.FileInfo) bool {
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && sa.Dev == sb.Dev && sa.Ino == sb.Ino
}

func vetFail(path, format string, args ...any) error {
	return &VetError{Path: path, Reason: fmt.Sprintf(format, args...)}
}

func (v jobDirVet) run(ctx context.Context, jobsRoot, home, id string) error {
	if jobsRoot == "" || !filepath.IsAbs(jobsRoot) || filepath.Clean(jobsRoot) != jobsRoot {
		return vetFail(jobsRoot, "the jobs root %q isn't a clean absolute path", jobsRoot)
	}
	if err := v.expired(ctx, jobsRoot); err != nil {
		return err
	}
	for _, p := range vetChain(jobsRoot, home) {
		info, err := v.lstat(p)
		if err != nil {
			return vetFail(p, "the jobs root's chain can't be read at %s: %s", p, errText(err))
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return vetFail(p, "a symbolic link at %s, on the way to the jobs root", p)
		}
		if !info.IsDir() {
			return vetFail(p, "%s, on the way to the jobs root, isn't a directory", p)
		}
	}
	rootInfo, err := v.lstat(jobsRoot)
	if err != nil {
		return vetFail(jobsRoot, "the jobs root can't be read at %s: %s", jobsRoot, errText(err))
	}
	rootDev, err := v.checkDir(jobsRoot, rootInfo)
	if err != nil {
		return err
	}

	if !validJobID(id) {
		return vetFail(filepath.Join(jobsRoot, id), "the session id %q can't name a job directory", id)
	}
	jobDir, err := vetJobDirFor(jobsRoot, id)
	if err != nil {
		return err
	}
	if jobDir == "" {
		missing := filepath.Join(jobsRoot, id)
		return vetFail(missing, "no job directory for session %s: %s is missing", id, missing)
	}
	info, err := v.lstat(jobDir)
	if err != nil {
		return vetFail(jobDir, "the job directory can't be read at %s: %s", jobDir, errText(err))
	}
	dev, err := v.checkDir(jobDir, info)
	if err != nil {
		return err
	}
	if dev != rootDev {
		return vetFail(jobDir, "another filesystem at %s", jobDir)
	}
	return v.walk(ctx, jobDir, info, rootDev)
}

// expired fails the vet, naming where it stopped, once ctx has ended.
func (v jobDirVet) expired(ctx context.Context, at string) error {
	if err := ctx.Err(); err != nil {
		return vetFail(at, "the vet ran out of time at %s", at)
	}
	return nil
}

// checkDir is the check the jobs root and the job directory each pass: a
// real directory, not a link, owned by the person's uid, with no group or
// other write permission. It returns the directory's device.
func (v jobDirVet) checkDir(p string, info os.FileInfo) (uint64, error) {
	if info.Mode()&fs.ModeSymlink != 0 {
		return 0, vetFail(p, "a symbolic link at %s", p)
	}
	if !info.IsDir() {
		return 0, vetFail(p, "%s isn't a directory", p)
	}
	uid, dev, ok := v.ids(info)
	if !ok {
		return 0, vetFail(p, "the owner and device of %s can't be read", p)
	}
	if uid != v.uid {
		return 0, vetFail(p, "%s is owned by uid %d, not the person's", p, uid)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return 0, vetFail(p, "%s is writable by group or others (mode %o)", p, info.Mode().Perm())
	}
	return dev, nil
}

// walk checks every entry under dir, depth first, in name order.
func (v jobDirVet) walk(ctx context.Context, dir string, dirInfo os.FileInfo, rootDev uint64) error {
	names, err := v.readNames(dir, dirInfo)
	if err != nil {
		return vetFail(dir, "the directory %s can't be read: %s", dir, errText(err))
	}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if err := v.expired(ctx, p); err != nil {
			return err
		}
		info, err := v.lstat(p)
		if err != nil {
			return vetFail(p, "%s can't be read: %s", p, errText(err))
		}
		mode := info.Mode()
		if !mode.IsRegular() && !mode.IsDir() {
			return vetFail(p, "%s at %s", entryKind(mode), p)
		}
		_, dev, ok := v.ids(info)
		if !ok {
			return vetFail(p, "the device of %s can't be read", p)
		}
		if dev != rootDev {
			return vetFail(p, "another filesystem at %s", p)
		}
		if mode.IsDir() {
			if err := v.walk(ctx, p, info, rootDev); err != nil {
				return err
			}
		}
	}
	return nil
}

// entryKind names what a non-regular, non-directory entry is.
func entryKind(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	default:
		return "an irregular file"
	}
}

// vetChain is every directory on the way to jobsRoot that must not be a
// link, jobsRoot included: each component below home when jobsRoot lies
// under it, and otherwise the config directory jobsRoot sits in and
// jobsRoot itself.
func vetChain(jobsRoot, home string) []string {
	if home != "" && filepath.IsAbs(home) {
		h := filepath.Clean(home)
		if rel, err := filepath.Rel(h, jobsRoot); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			var chain []string
			p := h
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				p = filepath.Join(p, part)
				chain = append(chain, p)
			}
			return chain
		}
	}
	return []string{filepath.Dir(jobsRoot), jobsRoot}
}
