package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/testhook"
)

// configReadTimeout bounds the configuration read in one repository. An
// include.path can name any file, a FIFO among them, so the read can hang;
// one that does is killed and keeps the unit. A variable so a test can watch
// it fire without waiting ten seconds.
var configReadTimeout = 10 * time.Second

// networkTimeout bounds one network call.
const networkTimeout = 10 * time.Second

// killWait is how long a killed git process group gets to exit before its
// pipes are closed underneath it.
const killWait = 2 * time.Second

// gitOverrides are the -c settings every git call carries. Command-line
// settings take precedence over every configuration file and reach the git
// processes git itself starts, so together they turn off everything a
// repository's own configuration could make these calls run or fetch: hooks,
// fsmonitor, pagers and credential helpers; replace refs and commit-graph
// files, which change the history git reads; and a proxy or a disabled
// certificate check on a network call. The untracked settings make status
// report every untracked file whatever the repository asks, read from the
// directories rather than from a cache the agent could have written, and the
// two stat pins stop a repository from making git trust its index's cached
// stat data more than it does by default.
var gitOverrides = []string{
	"core.fsmonitor=false",
	"core.hooksPath=/dev/null",
	"core.pager=cat",
	"credential.helper=",
	"submodule.recurse=false",
	"fetch.recurseSubmodules=false",
	"core.useReplaceRefs=false",
	"core.commitGraph=false",
	"log.showSignature=false",
	"status.showUntrackedFiles=all",
	"core.untrackedCache=false",
	"http.proxy=",
	"http.sslVerify=true",
	"core.trustctime=true",
	"core.checkStat=default",
}

// allowedProtocols is GIT_ALLOW_PROTOCOL: git itself refuses every other
// transport, so ext::, file:// and local paths never run.
const allowedProtocols = "https:ssh"

// sshCommand overrides any core.sshCommand. Batch mode turns a prompt, an
// unknown host key among them, into a failure, which reads as unreachable
// and keeps.
const sshCommand = "ssh -o BatchMode=yes -o ForwardAgent=no"

// Env is what one pass resolves once and every external program the rule
// runs is started from: the filtered PATH, the absolute paths of git, lsof
// and claude found on it, the home directory and the pass's scratch
// directory.
type Env struct {
	// Root is the workspace root, with symlinks resolved.
	Root string
	// Home is the person's home directory, the only variable git inherits.
	Home string
	// PATH is the filtered PATH, already joined.
	PATH string
	// Git, Lsof and Claude are absolute paths, empty when the filtered PATH
	// has no such program.
	Git, Lsof, Claude string
	// Scratch is the pass's scratch directory; nil when it couldn't be made,
	// and then nothing that needs it runs.
	Scratch *Scratch
	// ScratchErr is why Scratch is nil.
	ScratchErr error

	// allowFile admits file remotes; only the functional suite's test build
	// ever sets it, so its offline fixtures can stand in for a forge.
	allowFile bool
	// caFile is a CA bundle git trusts on network calls; only the functional
	// suite's test build ever sets it, for its https git server.
	caFile string

	// credHelpers are the person's own credential helpers, read once per
	// pass before any unit is judged and passed only on network calls to
	// proof remotes. credErr is why the read failed; it keeps every unit.
	credHelpers []string
	credErr     error

	// gitVersion is git's version, read once per pass before the first
	// configuration read. A version that wasn't read or doesn't parse is the
	// zero value, which counts as older than every version.
	gitVersion gitVersion

	// observe, when set, is told of every git call gitCommand builds, in the
	// order they're built, with the directories the call names. It is a seam
	// for tests, which record the calls to check their order; nothing else
	// sets it.
	observe func(r repoDirs, args []string)

	mu       sync.Mutex
	lsRemote map[string]lsRemoteResult

	// scanStores are the scratch stores ScanFetch filled for niwa destroy's
	// scan, by their objects directory: the only alternates a scan's call
	// may name.
	scanStores map[string]*store
}

// lsRemoteResult is one URL's ls-remote answer, cached for the pass: the
// refs it listed, by name, and the set of ids they name.
type lsRemoteResult struct {
	refs []lsRef
	tips map[string]bool
	err  error
}

// EnvOption adjusts an Env as NewEnv builds it.
type EnvOption func(*Env)

// WithFileRemotes admits file remotes on the pass's git calls. Only tests
// pass it: their remotes are local bare repositories, which every other
// caller's git calls refuse.
func WithFileRemotes() EnvOption { return func(e *Env) { e.allowFile = true } }

// NewEnv resolves the pass's environment: the PATH filtered against root,
// git, lsof and claude on it, a fresh scratch directory under home, git's
// version and the person's credential helpers. A scratch directory that
// can't be made is recorded rather than returned as an error, so a unit that
// never needs it can still be judged; with none, the version and
// credential-helper reads don't run, since no network call can, and a
// version that wasn't read counts as older than 2.45.
func NewEnv(root, home, pathEnv string, opts ...EnvOption) *Env {
	canonRoot := canonical(root)
	dirs := filterPATH(pathEnv, canonRoot)
	e := &Env{
		Root:      canonRoot,
		Home:      home,
		PATH:      strings.Join(dirs, string(os.PathListSeparator)),
		Git:       lookIn(dirs, "git", canonRoot),
		Lsof:      lookIn(dirs, "lsof", canonRoot),
		Claude:    lookIn(dirs, "claude", canonRoot),
		allowFile: testhook.RemovalFileRemotes(),
		caFile:    testhook.RemovalCAFile(),
		lsRemote:  map[string]lsRemoteResult{},
	}
	for _, o := range opts {
		o(e)
	}
	e.Scratch, e.ScratchErr = NewScratch(home, canonRoot)
	if e.Scratch != nil {
		e.gitVersion = e.readGitVersion(context.Background())
		e.credHelpers, e.credErr = e.readCredentialHelpers(context.Background())
	}
	return e
}

// gitVersion is a git version's major and minor numbers.
type gitVersion struct {
	major, minor int
}

// atLeast reports whether v is major.minor or later.
func (v gitVersion) atLeast(major, minor int) bool {
	return v.major > major || (v.major == major && v.minor >= minor)
}

// lazyFetchOffVersion is the first git version that honors
// GIT_NO_LAZY_FETCH. An older git can fetch a partial clone's missing
// objects from its promisor remote in the middle of any call that reads an
// object.
var lazyFetchOffVersion = gitVersion{2, 45}

// versionReadTimeout bounds the version read.
const versionReadTimeout = 10 * time.Second

// readGitVersion runs git version once, in no repository, from an empty
// directory under the scratch directory, and parses what it prints. A read
// that fails or doesn't parse gives the zero version.
func (e *Env) readGitVersion(ctx context.Context) gitVersion {
	dir, err := e.scratchDir(scratchVersion)
	if err != nil {
		return gitVersion{}
	}
	ctx, cancel := context.WithTimeout(ctx, versionReadTimeout)
	defer cancel()
	cmd, err := e.gitCommand(ctx, repoDirs{GitDir: filepath.Join(dir, noRepository)}, callOpts{dir: dir}, "version")
	if err != nil {
		return gitVersion{}
	}
	out, err := run(cmd)
	if err != nil {
		return gitVersion{}
	}
	return parseGitVersion(string(out))
}

// parseGitVersion reads "git version <major>.<minor>[.<rest>]" and returns
// the major and minor numbers, or the zero version when the line isn't in
// that form.
func parseGitVersion(s string) gitVersion {
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), "git version ")
	if !ok {
		return gitVersion{}
	}
	if i := strings.IndexByte(rest, ' '); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(rest, ".")
	if len(parts) < 2 {
		return gitVersion{}
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || major < 0 || minor < 0 {
		return gitVersion{}
	}
	return gitVersion{major, minor}
}

// Close removes the pass's scratch directory.
func (e *Env) Close() {
	if e != nil && e.Scratch != nil {
		e.Scratch.Remove()
	}
}

// canonical resolves symlinks in p, falling back to the cleaned path when
// it can't be resolved.
func canonical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// within reports whether p is dir or lies under it.
func within(p, dir string) bool {
	if p == dir {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// filterPATH drops every PATH entry niwa won't start a program from: an
// empty or relative entry, which resolves against whatever directory a
// command runs in, an entry that doesn't resolve, and one that resolves,
// through symlinks, inside the workspace root, where an agent could have
// planted a binary. Each entry kept is its resolved form, once.
//
// This is a narrow control: the agent runs as the person and can write other
// directories on their PATH, which niwa can't tell from the person's own.
func filterPATH(pathEnv, root string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		resolved, err := filepath.EvalSymlinks(entry)
		if err != nil {
			continue
		}
		if within(resolved, root) || seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// lookIn finds name in dirs as an executable regular file, refusing one that
// is a link resolving inside root, and returns its absolute path.
func lookIn(dirs []string, name, root string) string {
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(p); err != nil || within(resolved, root) {
			continue
		}
		return p
	}
	return ""
}

// repoDirs is where one git call runs: the git directory and work tree from
// niwa's own walk, both empty for a call that runs in no repository.
type repoDirs struct {
	GitDir   string
	WorkTree string
}

// callOpts are the per-call choices gitCommand makes.
type callOpts struct {
	// excludesUnset adds -c core.excludesFile= because the repository's own
	// configuration names no excludes file, so git's per-user ignore file
	// never decides anything.
	excludesUnset bool
	// dir is the working directory; it must be set for a call in no
	// repository, which runs from an empty directory under the scratch
	// directory.
	dir string
	// config are -c settings added after the fixed overrides: a network
	// call's credential helpers and redirect setting.
	config []string
}

// gitEnv is the whole environment a git call gets. Nothing is inherited
// except HOME.
func (e *Env) gitEnv() []string {
	protocols := allowedProtocols
	if e.allowFile {
		protocols += ":file"
	}
	ceiling := e.Root
	if e.Scratch != nil {
		ceiling += string(os.PathListSeparator) + e.Scratch.Dir
	}
	env := []string{
		"PATH=" + e.PATH,
		"HOME=" + e.Home,
		"LANG=C",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"GIT_ALLOW_PROTOCOL=" + protocols,
		"GIT_SSH_COMMAND=" + sshCommand,
		"GIT_PAGER=cat",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_GRAFT_FILE=/dev/null",
		"GIT_CEILING_DIRECTORIES=" + ceiling,
	}
	if e.caFile != "" {
		env = append(env, "GIT_SSL_CAINFO="+e.caFile)
	}
	return env
}

// networkConfig are the -c settings every network call to a proof remote
// adds after the fixed overrides: redirects off, so a proof host can't hand
// the call to another host whose refs would then count as its own, and the
// person's credential helpers after the reset the overrides make, so no
// other helper runs.
func (e *Env) networkConfig() []string {
	out := []string{"http.followRedirects=false"}
	for _, h := range e.credHelpers {
		out = append(out, "credential.helper="+h)
	}
	return out
}

// gitCommand builds a git call on niwa's removal path. It is the only
// builder of git commands there: git starts by the absolute path resolved
// from the filtered PATH, with the fixed environment and the overrides
// above, --git-dir and --work-tree from niwa's walk rather than discovery,
// --ignore-submodules=all and --untracked-files=all on every status, so git
// never starts a status of its own inside a submodule, and
// --no-show-signature on every log-family call, so no signature check runs
// gpg.program.
//
// The process runs in its own group, and the context's end kills the whole
// group, so nothing a call started outlives the pass.
func (e *Env) gitCommand(ctx context.Context, r repoDirs, o callOpts, args ...string) (*exec.Cmd, error) {
	if e == nil || e.Git == "" {
		return nil, errors.New("no git on the filtered PATH")
	}
	if len(args) == 0 {
		return nil, errors.New("no git subcommand")
	}
	argv := []string{"--no-pager"}
	if r.GitDir != "" {
		argv = append(argv, "--git-dir="+r.GitDir)
	}
	if r.WorkTree != "" {
		argv = append(argv, "--work-tree="+r.WorkTree)
	}
	for _, kv := range gitOverrides {
		argv = append(argv, "-c", kv)
	}
	if o.excludesUnset {
		argv = append(argv, "-c", "core.excludesFile=")
	}
	for _, kv := range o.config {
		argv = append(argv, "-c", kv)
	}
	argv = append(argv, args...)
	switch {
	case args[0] == "status":
		argv = append(argv, "--ignore-submodules=all", "--untracked-files=all")
	case args[0] == "log", args[0] == "stash" && len(args) > 1 && args[1] == "list":
		argv = append(argv, "--no-show-signature")
	}

	if e.observe != nil {
		e.observe(r, args)
	}
	cmd := exec.CommandContext(ctx, e.Git, argv...)
	cmd.Env = e.gitEnv()
	switch {
	case o.dir != "":
		cmd.Dir = o.dir
	case r.WorkTree != "":
		cmd.Dir = r.WorkTree
	case r.GitDir != "":
		cmd.Dir = r.GitDir
	default:
		return nil, errors.New("a git call outside a repository needs a directory to run from")
	}
	groupKill(cmd)
	return cmd, nil
}

// scanEnvAllowed are the fixed environment entries ScanCommand lets a scan's
// call add to the fixed environment: neither changes what git may run or
// contact. The one other entry it admits is scanAlternates naming only
// scratch stores ScanFetch made.
var scanEnvAllowed = map[string]bool{
	"GIT_LITERAL_PATHSPECS=1": true,
	"GIT_TERMINAL_PROMPT=0":   true,
}

// scanAlternates is the variable a scan's local calls name the scratch
// stores ScanFetch filled with, so they read the remote's commits without
// those objects ever being written into the scanned repository.
const scanAlternates = "GIT_ALTERNATE_OBJECT_DIRECTORIES"

// scanLsRemoteFlags are the only ls-remote options a scan's ls-remote may
// carry.
var scanLsRemoteFlags = map[string]bool{"--symref": true, "--heads": true, "--refs": true}

// scanDirs is where the scan's call in the working tree at dir runs: a dir
// whose .git is a directory names its git directory and work tree
// explicitly; any other dir is named as the work tree, and git finds the git
// directory from its .git file, bounded by the ceiling directories.
func scanDirs(dir string) repoDirs {
	r := repoDirs{WorkTree: dir}
	if st, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && st.IsDir() {
		r.GitDir = filepath.Join(dir, ".git")
	}
	return r
}

// scanEnvEntry checks one environment entry a scan's call asks for: one of
// scanEnvAllowed, or scanAlternates naming only stores ScanFetch made in this
// pass, each of which passes the store check, its config byte for byte
// included, right now. Anything else refuses the call.
func (e *Env) scanEnvEntry(kv string) error {
	if scanEnvAllowed[kv] {
		return nil
	}
	key, value, _ := strings.Cut(kv, "=")
	if key != scanAlternates || value == "" {
		return fmt.Errorf("the scan's git call can't set %s", key)
	}
	for _, objects := range filepath.SplitList(value) {
		e.mu.Lock()
		st := e.scanStores[objects]
		e.mu.Unlock()
		if st == nil {
			return fmt.Errorf("the scan's git call can't set %s to %s: not a scratch store this pass filled", key, objects)
		}
		if err := st.check(); err != nil {
			return err
		}
		if err := st.checkConfig(); err != nil {
			return err
		}
	}
	return nil
}

// scanRemoteURL is the URL in the repository's own remote.<name>.url, the
// first one its configuration lists, read with the bounded configuration
// read. It is the only URL a scan's network call contacts; insteadOf and
// every other rewrite in the repository's configuration never apply, since
// the call itself runs in no repository.
func (e *Env) scanRemoteURL(ctx context.Context, r repoDirs, name string) (string, error) {
	rc, err := e.readConfig(ctx, r)
	if err != nil {
		return "", err
	}
	for _, re := range rc.remotes {
		if re.Name != name {
			continue
		}
		if strings.HasPrefix(re.URL, "-") {
			return "", fmt.Errorf("remote %s: clone URL not allowed: it starts with -", name)
		}
		return re.URL, nil
	}
	return "", fmt.Errorf("remote %s has no url in the repository's configuration", name)
}

// scanLsRemote builds a scan's ls-remote of the remote args names the way
// the removal pass reads a proof remote: from the empty network directory
// under the scratch directory, with a --git-dir that doesn't exist, so no
// repository's configuration is read, against the URL in the repository's
// own remote.<name>.url, with the network settings: redirects off and only
// the person's own credential helpers.
func (e *Env) scanLsRemote(ctx context.Context, r repoDirs, env []string, args []string) (*exec.Cmd, error) {
	var flags, rest []string
	name := ""
	for _, a := range args[1:] {
		switch {
		case name != "":
			rest = append(rest, a)
		case strings.HasPrefix(a, "-"):
			if !scanLsRemoteFlags[a] {
				return nil, fmt.Errorf("the scan's ls-remote can't pass %s", a)
			}
			flags = append(flags, a)
		default:
			name = a
		}
	}
	if name == "" {
		return nil, errors.New("the scan's ls-remote names no remote")
	}
	if e.credErr != nil {
		return nil, fmt.Errorf("reading the credential helpers failed: %v", e.credErr)
	}
	url, err := e.scanRemoteURL(ctx, r, name)
	if err != nil {
		return nil, err
	}
	dir, err := e.scratchDir(scratchNet)
	if err != nil {
		return nil, err
	}
	argv := append([]string{"ls-remote"}, flags...)
	argv = append(argv, "--", url)
	argv = append(argv, rest...)
	cmd, err := e.gitCommand(ctx, repoDirs{GitDir: filepath.Join(dir, noRepository)}, callOpts{dir: dir, config: e.networkConfig()}, argv...)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, env...)
	return cmd, nil
}

// ScanFetch brings the history of ids from the remote named remote of the
// repository at dir into a fresh scratch store, the way the removal pass
// fetches a proof remote's tips: from the store's empty run directory, in no
// repository but the store, against the URL in the repository's own
// remote.<name>.url, with the network settings, under the network timeout
// and the store's size cap. Nothing is written into the repository at dir.
// Each id must arrive as a commit. It returns the store's objects directory,
// which the scan's later calls in that repository may name in
// GIT_ALTERNATE_OBJECT_DIRECTORIES; ScanCommand admits it there and checks
// the store again each time. Any failure is an error, which carries git's
// refusal line, and the scan then judges nothing landed on that remote.
func (e *Env) ScanFetch(ctx context.Context, dir, remote string, ids []string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the scan's fetch needs an absolute directory, not %q", dir)
	}
	if len(ids) == 0 {
		return "", errors.New("the scan's fetch names no commit")
	}
	for _, id := range ids {
		if !isObjectID(id) {
			return "", fmt.Errorf("the scan's fetch can only ask for object ids, not %q", id)
		}
	}
	if e == nil || e.Git == "" {
		return "", errors.New("no git on the filtered PATH")
	}
	if e.credErr != nil {
		return "", fmt.Errorf("reading the credential helpers failed: %v", e.credErr)
	}
	url, err := e.scanRemoteURL(ctx, scanDirs(dir), remote)
	if err != nil {
		return "", err
	}
	st, err := e.newStore()
	if err != nil {
		return "", err
	}
	if err := st.fetch(ctx, url, ids); err != nil {
		st.remove()
		return "", fmt.Errorf("fetching from %s: %v", remote, err)
	}
	types, err := st.objectTypes(ctx, ids)
	if err != nil {
		st.remove()
		return "", err
	}
	for _, id := range ids {
		if types[id] != "commit" {
			st.remove()
			return "", fmt.Errorf("fetching from %s: %s didn't arrive as a commit", remote, id)
		}
	}
	objects := filepath.Join(st.dir, "objects")
	e.mu.Lock()
	if e.scanStores == nil {
		e.scanStores = map[string]*store{}
	}
	e.scanStores[objects] = st
	e.mu.Unlock()
	return objects, nil
}

// ScanCommand builds one git call niwa destroy's scan makes in the working
// tree at dir, through gitCommand: the git found on the filtered PATH, the
// fixed environment and overrides, --ignore-submodules=all and
// --untracked-files=all on a status and --no-show-signature on a log-family
// call. scanDirs says how the call names the repository. env may only hold
// the entries scanEnvEntry admits; any other entry refuses the call.
//
// An ls-remote runs in no repository, against the URL in the repository's
// own remote.<name>.url (scanLsRemote), and a fetch is refused: the scan's
// fetch goes into a scratch store through ScanFetch, never into the
// repository it scans.
//
// Right before a status it runs the configuration check on the repository
// again, under the same bound as every configuration read: status is the
// call that can start a filter. A key that names a program refuses the call
// with an error naming the key, and a read that fails refuses it too; either
// way the status isn't started. The read also decides the status's excludes
// override, as on the removal path.
func (e *Env) ScanCommand(ctx context.Context, dir string, env []string, args ...string) (*exec.Cmd, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("the scan's git call needs an absolute directory, not %q", dir)
	}
	if e == nil {
		return nil, errors.New("no git on the filtered PATH")
	}
	for _, kv := range env {
		if err := e.scanEnvEntry(kv); err != nil {
			return nil, err
		}
	}
	r := scanDirs(dir)
	if len(args) > 0 {
		switch args[0] {
		case "ls-remote":
			return e.scanLsRemote(ctx, r, env, args)
		case "fetch":
			return nil, errors.New("the scan's fetch goes into a scratch store, never into the repository it scans")
		}
	}
	o := callOpts{dir: dir}
	if len(args) > 0 && args[0] == "status" {
		rc, err := e.recheckConfig(ctx, r)
		if err != nil {
			return nil, err
		}
		o.excludesUnset = rc.excludesUnset
	}
	cmd, err := e.gitCommand(ctx, r, o, args...)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(cmd.Env, env...)
	return cmd, nil
}

// groupKill puts cmd in its own process group and makes a cancelled context
// kill the whole group.
func groupKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = killWait
}

// scratchDir returns one of the pass directory's empty directories after
// checking that directory, the pass directory and the scratch root above it
// for owner and mode.
func (e *Env) scratchDir(name string) (string, error) {
	if e.Scratch == nil {
		if e.ScratchErr != nil {
			return "", fmt.Errorf("scratch directory unusable: %v", e.ScratchErr)
		}
		return "", errors.New("no scratch directory")
	}
	return e.Scratch.Use(name)
}

// run runs cmd and returns its standard output, or an error carrying git's
// refusal line from standard error (see failureLine).
func run(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := failureLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// runWarn runs cmd like run, and also returns the last line git wrote to
// standard error when it exited 0: a warning such as a broken ref that git
// skipped, which a caller keeps on rather than reads past.
func runWarn(cmd *exec.Cmd) ([]byte, string, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := failureLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, "", errors.New(msg)
	}
	return stdout.Bytes(), lastWarning(stderr.String()), nil
}

// failureLine is the line a failed git call's standard error is reported
// by: the last line git marked fatal, when there is one, and the last line
// otherwise. git can follow its refusal with advice of its own, as it does
// after refusing a repository for its ownership, where the last line is the
// command that would add a safe.directory entry rather than the refusal.
func failureLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "fatal:") {
			return l
		}
	}
	return lastLine(s)
}

// lastWarning is the last line of standard error that isn't one of git's
// advice hints, which say how to silence a message rather than report a
// problem (the graft-file deprecation hint every history read prints under
// GIT_GRAFT_FILE among them).
func lastWarning(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" && !strings.HasPrefix(l, "hint:") {
			return l
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// configEntry is one setting from git config --list --show-scope.
type configEntry struct {
	Scope string
	Key   string
	Value string
}

// knownScopes are the scopes git config --show-scope prints.
var knownScopes = map[string]bool{
	"system": true, "global": true, "local": true, "worktree": true, "command": true,
}

// parseConfigList reads git config --list --show-scope -z output: each entry
// is "<scope>\0<key>\n<value>\0", or "<scope>\0<key>\0" for a key with no
// value. Anything else is an error, which keeps.
func parseConfigList(out []byte) ([]configEntry, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("configuration listing is not NUL-terminated")
	}
	fields := strings.Split(string(out[:len(out)-1]), "\x00")
	if len(fields)%2 != 0 {
		return nil, errors.New("configuration listing does not parse")
	}
	entries := make([]configEntry, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		scope := fields[i]
		if !knownScopes[scope] {
			return nil, fmt.Errorf("configuration listing names an unknown scope %q", scope)
		}
		key, value, _ := strings.Cut(fields[i+1], "\n")
		if key == "" {
			return nil, errors.New("configuration listing has an entry with no key")
		}
		entries = append(entries, configEntry{Scope: scope, Key: key, Value: value})
	}
	return entries, nil
}

// fsmonitorBooleans are the values of core.fsmonitor that turn the built-in
// monitor on or off rather than naming a hook.
var fsmonitorBooleans = map[string]bool{
	"": true, "true": true, "false": true, "yes": true, "no": true,
	"on": true, "off": true, "1": true, "0": true,
}

// canonicalKey returns a configuration key as git reads it: the section and
// variable names in lowercase, a subsection between them as written. git
// config --list already prints keys this way; a key that reaches the rule
// some other way is compared in the same form.
func canonicalKey(key string) string {
	first := strings.Index(key, ".")
	last := strings.LastIndex(key, ".")
	if first < 0 {
		return strings.ToLower(key)
	}
	if first == last {
		return strings.ToLower(key)
	}
	return strings.ToLower(key[:first]) + key[first:last] + strings.ToLower(key[last:])
}

// namesProgram reports whether a setting names a program git might run: a
// filter, diff or merge driver, a credential helper, a signing program, an
// fsmonitor hook, or any key whose name says it names a program (a variable
// named program, helper or cmd, or one ending in command, which covers both
// *.command and *Command). It also refuses keys that name a program no niwa
// call runs today: core.askPass, core.editor, core.gitProxy, and a remote's
// vcs and uploadpack. Section and variable names compare case-insensitively
// and a subsection as written.
func namesProgram(key, value string) bool {
	key = canonicalKey(key)
	section, _, _ := strings.Cut(key, ".")
	variable := key[strings.LastIndex(key, ".")+1:]
	hasSubsection := strings.Count(key, ".") >= 2
	if section == "" || variable == "" || !strings.Contains(key, ".") {
		return false
	}
	switch {
	case section == "filter" && hasSubsection && (variable == "clean" || variable == "smudge" || variable == "process"):
		return true
	case key == "diff.external":
		return true
	case section == "diff" && hasSubsection && (variable == "textconv" || variable == "command"):
		return true
	case section == "merge" && hasSubsection && variable == "driver":
		return true
	case section == "credential" && variable == "helper":
		return true
	case key == "gpg.program", key == "gpg.ssh.program", key == "gpg.x509.program":
		return true
	case key == "core.fsmonitor":
		return !fsmonitorBooleans[strings.ToLower(strings.TrimSpace(value))]
	case key == "core.askpass", key == "core.editor", key == "core.gitproxy":
		return true
	case section == "remote" && hasSubsection && (variable == "vcs" || variable == "uploadpack"):
		return true
	case variable == "program", variable == "helper", variable == "cmd", strings.HasSuffix(variable, "command"):
		return true
	}
	return false
}

// repoConfig is what the configuration read says about one repository.
type repoConfig struct {
	// programKey is the first key from a scope other than command that
	// names a program, in its canonical form, empty when there is none.
	programKey string
	// partialKey is the first key that makes the repository a partial
	// clone, empty when there is none.
	partialKey string
	// excludesUnset is true when the repository's own configuration names
	// no core.excludesFile.
	excludesUnset bool
	// sparse is true when the repository's own configuration turns on a
	// sparse checkout.
	sparse bool
	// bare is the repository's own core.bare, and worktree its
	// core.worktree, empty when it names none.
	bare     bool
	worktree string
	// remotes are the repository's remote.<name>.url values in the order
	// git listed them. They decide which remotes are proof remotes; the
	// URL a network call contacts is never one of these strings.
	remotes []remoteEntry
}

// remoteEntry is one remote.<name>.url value.
type remoteEntry struct {
	Name, URL string
}

// remoteURLKey splits a configuration key of the form remote.<name>.url,
// matched as git config --list prints it (section and variable in
// lowercase, the remote's name as written), and returns the name.
func remoteURLKey(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "remote.")
	if !ok {
		return "", false
	}
	name, ok := strings.CutSuffix(rest, ".url")
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// readConfig runs the configuration read, the first git call in every
// repository, under its own short context. Reading configuration runs no
// program, so it is safe before anything else; --includes follows every
// include.path, which is why it is bounded.
func (e *Env) readConfig(ctx context.Context, r repoDirs) (repoConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, configReadTimeout)
	defer cancel()
	// The read itself runs without the excludes override: whether to add it
	// is what this read decides, and the setting changes nothing it prints
	// beyond the command-scope line every override adds.
	cmd, err := e.gitCommand(ctx, r, callOpts{}, "config", "--list", "--show-scope", "--includes", "-z")
	if err != nil {
		return repoConfig{}, err
	}
	out, err := run(cmd)
	if err != nil {
		return repoConfig{}, fmt.Errorf("reading the repository configuration: %v", err)
	}
	entries, err := parseConfigList(out)
	if err != nil {
		return repoConfig{}, err
	}
	rc := repoConfig{excludesUnset: true}
	for _, en := range entries {
		if en.Scope == "command" {
			continue
		}
		if strings.EqualFold(en.Key, "core.excludesFile") {
			rc.excludesUnset = false
		}
		if strings.EqualFold(en.Key, "core.sparseCheckout") && gitTrue(en.Value) {
			rc.sparse = true
		}
		if strings.EqualFold(en.Key, "core.bare") {
			rc.bare = gitTrue(en.Value)
		}
		if strings.EqualFold(en.Key, "core.worktree") {
			rc.worktree = en.Value
		}
		if name, ok := remoteURLKey(en.Key); ok {
			rc.remotes = append(rc.remotes, remoteEntry{Name: name, URL: en.Value})
		}
		if rc.programKey == "" && namesProgram(en.Key, en.Value) {
			rc.programKey = canonicalKey(en.Key)
		}
		if rc.partialKey == "" && partialCloneKey(en.Key, en.Value) {
			rc.partialKey = canonicalKey(en.Key)
		}
	}
	return rc, nil
}

// partialCloneKey reports whether a setting makes the repository a partial
// clone: extensions.partialClone naming a remote, or a remote.<name>.promisor
// set to any value git doesn't read as false. git reads a nonzero integer
// such as 2 as true, so only gitFalse's values leave the promisor unset; an
// empty value counts as set, which is stricter than git.
func partialCloneKey(key, value string) bool {
	key = canonicalKey(key)
	if key == "extensions.partialclone" {
		return strings.TrimSpace(value) != ""
	}
	if rest, ok := strings.CutPrefix(key, "remote."); ok {
		if name, ok := strings.CutSuffix(rest, ".promisor"); ok && name != "" {
			return !gitFalse(value)
		}
	}
	return false
}

// gitFalse reports whether git reads a configuration value as false: false,
// no or off in any case, or an integer zero, with an optional sign, a 0x
// prefix and a k, m or g unit. Any other value, an empty one or one with
// spaces included, isn't false here, so a caller that keeps on a set value
// keeps on it.
func gitFalse(v string) bool {
	s := strings.ToLower(v)
	switch s {
	case "false", "no", "off":
		return true
	}
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	if s != "" && strings.ContainsRune("kmg", rune(s[len(s)-1])) {
		s = s[:len(s)-1]
	}
	if rest, ok := strings.CutPrefix(s, "0x"); ok {
		s = rest
	}
	return s != "" && strings.Trim(s, "0") == ""
}

// refusal says why the configuration read keeps a repository, with the key
// it names: a key that names a program, or, with a git older than 2.45, a
// partial clone, since that git would fetch a missing object from the
// promisor remote in the middle of any call that reads one. Both are empty
// when the repository's configuration keeps nothing.
func (e *Env) refusal(rc repoConfig) (key, why string) {
	if rc.programKey != "" {
		return rc.programKey, "repository configuration names a program: " + rc.programKey
	}
	if rc.partialKey != "" && !e.gitVersion.atLeast(lazyFetchOffVersion.major, lazyFetchOffVersion.minor) {
		return rc.partialKey, "partial clone needs git 2.45: " + rc.partialKey
	}
	return "", ""
}

// changedDuringThePass prefixes what a re-check found during the pass.
const changedDuringThePass = "changed during the pass: "

// configRefused is the error a status call returns when the configuration
// read right before it keeps the repository: the call isn't started.
type configRefused struct {
	key, why string
}

func (c *configRefused) Error() string { return changedDuringThePass + c.why }

// recheckConfig runs the configuration read on a repository again, under
// the same bound as the first, right before a call that could start a
// program the configuration names. It returns what it read and nil when
// nothing keeps, a *configRefused naming the key when the configuration now
// keeps the repository, and the read's own error when it fails, which keeps
// too.
func (e *Env) recheckConfig(ctx context.Context, r repoDirs) (repoConfig, error) {
	rc, err := e.readConfig(ctx, r)
	if err != nil {
		return rc, err
	}
	if key, why := e.refusal(rc); why != "" {
		return rc, &configRefused{key: key, why: why}
	}
	return rc, nil
}

// RecheckConfig runs the configuration read on the repository at gitDir,
// with workTree as its work tree (empty for a repository with none), under
// the same 10-second bound as the pass's first read. A caller runs it right
// before a step that starts no git process of its own but that a planted
// key would still make unsafe, such as removing a worktree. It runs no
// identity check, so its caller checks the directories itself.
//
// key is the key that keeps the repository, and reason the keep reason,
// "changed during the pass: repository configuration names a program:
// <key>" (or the partial-clone reason on a git older than 2.45). A read that
// fails gives an empty key and the failure as the reason. Both are empty
// when nothing keeps.
func (e *Env) RecheckConfig(ctx context.Context, gitDir, workTree string) (key, reason string) {
	if e == nil {
		return "", "git can't be run for this pass"
	}
	_, err := e.recheckConfig(ctx, repoDirs{GitDir: gitDir, WorkTree: workTree})
	var refused *configRefused
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &refused):
		return refused.key, refused.Error()
	default:
		return "", err.Error()
	}
}

// gitTrue reports whether a configuration value is one git reads as true.
// A key with no value is true.
func gitTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "true", "yes", "on", "1":
		return true
	}
	return false
}

// ref is one ref for-each-ref lists.
type ref struct {
	OID  string
	Name string
}

// parseRefs reads for-each-ref output in the format
// "%(objectname)%00%(refname)%00", one record a line. A record that doesn't
// parse is an error, which keeps.
func parseRefs(out []byte) ([]ref, error) {
	var refs []ref
	rest := string(out)
	for rest != "" {
		rec, tail, ok := strings.Cut(rest, "\x00\n")
		if !ok {
			return nil, errors.New("ref listing does not parse")
		}
		rest = tail
		oid, name, ok := strings.Cut(rec, "\x00")
		if !ok || !isObjectID(oid) || !strings.HasPrefix(name, "refs/") || strings.ContainsAny(name, "\x00\n") {
			return nil, fmt.Errorf("ref listing has a record that does not parse: %q", rec)
		}
		refs = append(refs, ref{OID: oid, Name: name})
	}
	return refs, nil
}

// isObjectID reports whether s is a full SHA-1 or SHA-256 object id.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// parseLsRemote reads ls-remote output, "<oid>\t<ref>" a line, into the set
// of live tips, peeled tag targets included. ls-remote offers no -z, and a
// ref name can't hold a newline or a tab, so a line is a record.
func parseLsRemote(out []byte) (map[string]bool, error) {
	tips := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		oid, name, ok := strings.Cut(line, "\t")
		if !ok || !isObjectID(oid) || name == "" {
			return nil, fmt.Errorf("remote ref listing has a line that does not parse: %q", line)
		}
		tips[oid] = true
	}
	return tips, nil
}

// lsRef is one line of ls-remote output: an id and the ref name the remote
// lists it under, a peeled tag's "^{}" suffix included.
type lsRef struct {
	OID, Name string
}

// parseLsRemoteRefs reads ls-remote output like parseLsRemote, keeping each
// line's ref name, so a remote's HEAD and its tags can be read by name.
func parseLsRemoteRefs(out []byte) ([]lsRef, error) {
	var refs []lsRef
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		oid, name, ok := strings.Cut(line, "\t")
		if !ok || !isObjectID(oid) || name == "" || strings.Contains(name, "\t") {
			return nil, fmt.Errorf("remote ref listing has a line that does not parse: %q", line)
		}
		refs = append(refs, lsRef{OID: oid, Name: name})
	}
	return refs, nil
}

// tipSet is the set of ids refs name.
func tipSet(refs []lsRef) map[string]bool {
	tips := make(map[string]bool, len(refs))
	for _, r := range refs {
		tips[r.OID] = true
	}
	return tips
}

// cachedLsRemote returns the pass's answer for url, made once per URL per
// pass by call and shared by every evaluation in the pass, the tombstone
// re-check's included.
func (e *Env) cachedLsRemote(ctx context.Context, url string, call func() ([]lsRef, error)) lsRemoteResult {
	e.mu.Lock()
	if res, ok := e.lsRemote[url]; ok {
		e.mu.Unlock()
		return res
	}
	e.mu.Unlock()

	refs, err := call()
	res := lsRemoteResult{refs: refs, tips: tipSet(refs), err: err}
	if err != nil {
		res.refs, res.tips = nil, nil
	}
	if ctx.Err() == nil {
		// A call cut short by the pass's deadline isn't an answer about the
		// URL, so it isn't remembered.
		e.mu.Lock()
		e.lsRemote[url] = res
		e.mu.Unlock()
	}
	return res
}

// liveTips asks url for every ref it holds, once per URL per pass, in no
// repository: from an empty directory under the scratch directory, checked
// empty before each use, with an explicit --git-dir that doesn't exist, so
// git discovers no repository there or above it and reads no repository's
// configuration (its insteadOf, uploadpack, credential or proxy settings).
// No production code calls it any more: the rule reads a proof remote
// through a scratch store (remoteRefs). Only tests call it, and
// lsRemoteFrom, to pin how a call in no repository behaves; removing both
// is left to a later change.
func (e *Env) liveTips(ctx context.Context, url string) (map[string]bool, error) {
	res := e.cachedLsRemote(ctx, url, func() ([]lsRef, error) {
		if strings.HasPrefix(url, "-") {
			return nil, fmt.Errorf("clone URL not allowed: it starts with -")
		}
		dir, err := e.scratchDir(scratchNet)
		if err != nil {
			return nil, err
		}
		return e.lsRemoteRefs(ctx, repoDirs{GitDir: filepath.Join(dir, noRepository)}, dir, url)
	})
	return res.tips, res.err
}

// noRepository is the --git-dir every call in no repository names: a path
// under the empty directory the call runs from, which doesn't exist, so git
// runs as it does outside any repository and never looks for one.
const noRepository = "no-repository"

// lsRemoteFrom runs ls-remote for url from dir, in no repository.
func (e *Env) lsRemoteFrom(ctx context.Context, dir, url string) (map[string]bool, error) {
	refs, err := e.lsRemoteRefs(ctx, repoDirs{GitDir: filepath.Join(dir, noRepository)}, dir, url)
	if err != nil {
		return nil, err
	}
	return tipSet(refs), nil
}

// lsRemoteRefs runs ls-remote for url from dir with r as its git directory,
// under the network timeout and with the network settings, and returns
// every ref it lists.
func (e *Env) lsRemoteRefs(ctx context.Context, r repoDirs, dir, url string) ([]lsRef, error) {
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	cmd, err := e.gitCommand(ctx, r, callOpts{dir: dir, config: e.networkConfig()}, "ls-remote", "--", url)
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, err
	}
	return parseLsRemoteRefs(out)
}

// statusEntry is one git status --porcelain=v2 entry: its kind and path.
type statusEntry struct {
	Kind string // "changed", "renamed", "unmerged" or "untracked"
	Path string
}

// parseStatus reads git status --porcelain=v2 -z output. A record that
// doesn't parse is an error, which keeps.
func parseStatus(out []byte) ([]statusEntry, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("status is not NUL-terminated")
	}
	recs := strings.Split(string(out[:len(out)-1]), "\x00")
	var entries []statusEntry
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 2 || rec[1] != ' ' {
			return nil, fmt.Errorf("status has a record that does not parse: %q", rec)
		}
		var fields int
		var kind string
		switch rec[0] {
		case '1':
			fields, kind = 8, "changed"
		case '2':
			fields, kind = 9, "renamed"
		case 'u':
			fields, kind = 10, "unmerged"
		case '?':
			fields, kind = 1, "untracked"
		case '!':
			fields, kind = 1, "ignored"
		default:
			return nil, fmt.Errorf("status has a record that does not parse: %q", rec)
		}
		parts := strings.SplitN(rec, " ", fields+1)
		if len(parts) != fields+1 || parts[fields] == "" {
			return nil, fmt.Errorf("status has a record that does not parse: %q", rec)
		}
		entries = append(entries, statusEntry{Kind: kind, Path: parts[fields]})
		if rec[0] == '2' {
			// A rename's original path is the next NUL-terminated field.
			i++
			if i >= len(recs) || recs[i] == "" {
				return nil, errors.New("status has a rename with no original path")
			}
		}
	}
	return entries, nil
}
