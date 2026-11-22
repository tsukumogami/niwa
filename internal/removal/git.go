package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	mu       sync.Mutex
	lsRemote map[string]lsRemoteResult
}

type lsRemoteResult struct {
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
// git, lsof and claude on it, and a fresh scratch directory under home. A
// scratch directory that can't be made is recorded rather than returned as
// an error, so a unit that never needs it can still be judged.
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
		lsRemote:  map[string]lsRemoteResult{},
	}
	for _, o := range opts {
		o(e)
	}
	e.Scratch, e.ScratchErr = NewScratch(home, canonRoot)
	return e
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
	return []string{
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
}

// gitCommand builds a git call on niwa's removal path. It is the only
// builder of git commands there: git starts by the absolute path resolved
// from the filtered PATH, with the fixed environment and the overrides
// above, --git-dir and --work-tree from niwa's walk rather than discovery,
// and --ignore-submodules=all and --untracked-files=all on every status, so
// git never starts a status of its own inside a submodule.
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
	argv = append(argv, args...)
	if args[0] == "status" {
		argv = append(argv, "--ignore-submodules=all", "--untracked-files=all")
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

// run runs cmd and returns its standard output, or an error carrying the
// last line git wrote to standard error.
func run(cmd *exec.Cmd) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := lastLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	return stdout.Bytes(), nil
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

// namesProgram reports whether a setting names a program git might run
// during the rule's calls: a filter, diff or merge driver, a credential
// helper or an fsmonitor hook. Keys are matched as git config --list prints
// them, with section and variable names in lowercase.
func namesProgram(key, value string) bool {
	section, _, _ := strings.Cut(key, ".")
	variable := key[strings.LastIndex(key, ".")+1:]
	hasSubsection := strings.Count(key, ".") >= 2
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
	case key == "core.fsmonitor":
		return !fsmonitorBooleans[strings.ToLower(strings.TrimSpace(value))]
	}
	return false
}

// repoConfig is what the configuration read says about one repository.
type repoConfig struct {
	// programKey is the first key from a scope other than command that
	// names a program, empty when there is none.
	programKey string
	// excludesUnset is true when the repository's own configuration names
	// no core.excludesFile.
	excludesUnset bool
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
		if rc.programKey == "" && namesProgram(en.Key, en.Value) {
			rc.programKey = en.Key
		}
	}
	return rc, nil
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

// liveTips asks the clone URL for every ref it holds, once per URL per
// pass. The call runs in no repository, from an empty directory under the
// scratch directory, so nothing in the unit's repository (its insteadOf,
// uploadpack, credential or proxy settings) applies to it.
func (e *Env) liveTips(ctx context.Context, url string) (map[string]bool, error) {
	e.mu.Lock()
	if res, ok := e.lsRemote[url]; ok {
		e.mu.Unlock()
		return res.tips, res.err
	}
	e.mu.Unlock()

	tips, err := e.lsRemoteOnce(ctx, url)
	if ctx.Err() == nil {
		// A call cut short by the pass's deadline isn't an answer about the
		// URL, so it isn't remembered.
		e.mu.Lock()
		e.lsRemote[url] = lsRemoteResult{tips: tips, err: err}
		e.mu.Unlock()
	}
	return tips, err
}

func (e *Env) lsRemoteOnce(ctx context.Context, url string) (map[string]bool, error) {
	if strings.HasPrefix(url, "-") {
		return nil, fmt.Errorf("clone URL not allowed: it starts with -")
	}
	dir, err := e.scratchDir(scratchNet)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	cmd, err := e.gitCommand(ctx, repoDirs{}, callOpts{dir: dir}, "ls-remote", "--", url)
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, err
	}
	return parseLsRemote(out)
}

// statusEntry is one git status --porcelain=v2 entry: its kind and path.
type statusEntry struct {
	Kind string // "changed" or "untracked"
	Path string
}

// parseStatus reads git status --porcelain=v2 -z output. Any entry keeps the
// unit, so it records ordinary changed, untracked and ignored entries and
// treats every other record, a rename or an unmerged entry among them, as
// one that doesn't parse, which keeps as well.
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
	}
	return entries, nil
}
