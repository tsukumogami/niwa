package mirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/tsukumogami/niwa/internal/testhook"
)

// gitBaseConfig is set on every git call in a mirror, whatever the store's
// own config says: a store's config isn't trusted after a fault. Automatic
// maintenance and gc are off so no git run here starts a detached
// "git maintenance run --auto" that repacks a store behind niwa's back, after
// the manifest recorded its packs or while a restore reads them.
var gitBaseConfig = []string{
	"core.fsmonitor=false",
	"protocol.ext.allow=never",
	"maintenance.auto=false",
	"gc.auto=0",
}

// localEnv is the environment every git call here starts from: environ, the
// parent's, with every inherited GIT_* variable removed (GIT_DIR,
// GIT_EXEC_PATH and GIT_ALTERNATE_OBJECT_DIRECTORIES among them, any of which
// would point git somewhere else). A local call gets nothing more of the
// parent's; a network call gets inheritedNetworkEnv back on top of it. gitRun
// adds what niwa sets after both.
func localEnv(environ []string) []string {
	var env []string
	for _, kv := range environ {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// networkVars are the inherited variables a network call keeps, copied back
// verbatim: the user's ssh transport, askpass helper, git:// proxy command,
// TLS trust settings and proxy auth method. A remote that needs them can't
// be reached without them, and none of them points git at another
// repository or object store. GIT_CONFIG_COUNT and its entries are kept
// separately, through networkConfig's filter.
var networkVars = []string{
	"GIT_SSH",
	"GIT_SSH_COMMAND",
	"GIT_SSH_VARIANT",
	"GIT_ASKPASS",
	"GIT_PROXY_COMMAND",
	"GIT_SSL_CAINFO",
	"GIT_SSL_CAPATH",
	"GIT_SSL_NO_VERIFY",
	"GIT_HTTP_PROXY_AUTHMETHOD",
}

// maxConfigCount is the largest GIT_CONFIG_COUNT a network call honours. A
// user's environment config holds a handful of entries; a larger count is
// dropped with its entries rather than read entry by entry.
const maxConfigCount = 64

// configCountPattern is the GIT_CONFIG_COUNT git reads as written: git parses
// it with a base-10 strtoul and refuses trailing text, but strtoul also
// takes leading space and a sign, which this doesn't.
var configCountPattern = regexp.MustCompile(`^[0-9]+$`)

// inheritedNetworkEnv is what a network call keeps of environ, the parent's
// environment: each of networkVars that is set, with its value as given
// (git honours some of them, GIT_SSL_NO_VERIFY among them, when set at all,
// so an empty value is kept too), then networkConfig's entries.
// GIT_TERMINAL_PROMPT isn't among them: niwa sets it to 0 itself on every
// call (see gitRun).
func inheritedNetworkEnv(environ []string) []string {
	vals := map[string]string{}
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "GIT_") {
			vals[name] = value
		}
	}
	var out []string
	for _, name := range networkVars {
		if v, ok := vals[name]; ok {
			out = append(out, name+"="+v)
		}
	}
	return append(out, networkConfig(vals)...)
}

// networkConfig is what a network call keeps of the GIT_CONFIG_COUNT
// entries in vals (the parent's GIT_* variables by name): the entries whose
// key carries remote access (remoteAccessKey), renumbered from 0, and
// GIT_CONFIG_COUNT set to how many were kept.
//
// The filter is an allowlist because an environment entry outranks the
// store's config file, and most keys would undo one of the mirror's guards:
// an include makes git read another file, which could set any key past the
// filter, a remote.* entry could change the mirror's refspec or prune it and
// lose the refs that keep removed commits recoverable, and gc.*, fetch.prune,
// core.hooksPath or core.fsmonitor would undo the retention config or run a
// command. Of the keys kept, git reads the environment's entries before the
// -c pairs, so a -c pair niwa passes (protocol.ext.allow=never,
// protocol.version=2) still wins over an entry for the same key.
//
// A count that isn't plain decimal digits, is above maxConfigCount, or names
// an entry whose key or value isn't set drops the count and every entry: git
// refuses to run with such a count, and passing it would fail the call.
func networkConfig(vals map[string]string) []string {
	raw, ok := vals["GIT_CONFIG_COUNT"]
	if !ok || !configCountPattern.MatchString(raw) {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n > maxConfigCount {
		return nil
	}
	var kept []configEntry
	for i := 0; i < n; i++ {
		key, kok := vals["GIT_CONFIG_KEY_"+strconv.Itoa(i)]
		value, vok := vals["GIT_CONFIG_VALUE_"+strconv.Itoa(i)]
		if !kok || !vok {
			return nil
		}
		if remoteAccessKey(key) {
			kept = append(kept, configEntry{key, value})
		}
	}
	out := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(len(kept))}
	for i, e := range kept {
		out = append(out,
			"GIT_CONFIG_KEY_"+strconv.Itoa(i)+"="+e.key,
			"GIT_CONFIG_VALUE_"+strconv.Itoa(i)+"="+e.value)
	}
	return out
}

// remoteAccessPrefixes are the sections whose every key carries remote
// access: http transport settings (proxy, TLS, extra headers), credential
// helpers, protocol policy and ssh variant.
var remoteAccessPrefixes = []string{"http.", "https.", "credential.", "protocol.", "ssh."}

// remoteAccessKeys are the single keys outside those sections that do.
var remoteAccessKeys = []string{"core.sshcommand", "core.gitproxy", "core.askpass"}

// remoteAccessKey reports whether key, compared case-folded as git compares
// section and key names, carries remote access: under one of
// remoteAccessPrefixes, url.<base>.insteadOf, or one of remoteAccessKeys.
func remoteAccessKey(key string) bool {
	k := strings.ToLower(key)
	for _, p := range remoteAccessPrefixes {
		if strings.HasPrefix(k, p) && len(k) > len(p) {
			return true
		}
	}
	if slices.Contains(remoteAccessKeys, k) {
		return true
	}
	const urlPrefix, insteadOf = "url.", ".insteadof"
	return strings.HasPrefix(k, urlPrefix) && strings.HasSuffix(k, insteadOf) &&
		len(k) > len(urlPrefix)+len(insteadOf)
}

// The network notes a log line can carry: names, never values, of what a
// network call honoured that a reader of the log should know about.
const (
	noteSSLNoVerify  = "ssl-no-verify"
	noteProxyCommand = "proxy-command"
)

// networkNotes collects the notes of the network calls one mirror's check,
// restore and refresh made.
type networkNotes struct {
	mu   sync.Mutex
	seen map[string]bool
}

// networkNotesKey keys a mirror's networkNotes on a context.
type networkNotesKey struct{}

// withNetworkNotes returns ctx carrying a new, empty networkNotes.
func withNetworkNotes(ctx context.Context) context.Context {
	return context.WithValue(ctx, networkNotesKey{}, &networkNotes{seen: map[string]bool{}})
}

// noteNetworkCall records on ctx's networkNotes, if it has one, what
// inherited, the entries a network call kept of the parent's environment,
// honours: GIT_SSL_NO_VERIFY, and a proxy command from GIT_PROXY_COMMAND or
// a kept core.gitProxy entry.
func noteNetworkCall(ctx context.Context, inherited []string) {
	n, ok := ctx.Value(networkNotesKey{}).(*networkNotes)
	if !ok {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, kv := range inherited {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "GIT_SSL_NO_VERIFY":
			n.seen[noteSSLNoVerify] = true
		case name == "GIT_PROXY_COMMAND":
			n.seen[noteProxyCommand] = true
		case strings.HasPrefix(name, "GIT_CONFIG_KEY_") && strings.EqualFold(value, "core.gitProxy"):
			n.seen[noteProxyCommand] = true
		}
	}
}

// networkNotesFrom returns the notes ctx's networkNotes holds, in a fixed
// order, or nil.
func networkNotesFrom(ctx context.Context) []string {
	n, ok := ctx.Value(networkNotesKey{}).(*networkNotes)
	if !ok {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, note := range []string{noteSSLNoVerify, noteProxyCommand} {
		if n.seen[note] {
			out = append(out, note)
		}
	}
	return out
}

// lastWins returns env with each name once, at its last value, in the
// order each name first appears. exec does the same with a repeated name, so
// the probe records exactly what the process is given.
func lastWins(env []string) []string {
	index := map[string]int{}
	var out []string
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if i, ok := index[name]; ok {
			out[i] = kv
			continue
		}
		index[name] = len(out)
		out = append(out, kv)
	}
	return out
}

// localReadEnv is added, after localEnv's scrub, to every git call that doesn't
// reach the remote: no system or global config is read, so nothing the
// user's or the system's config names (a trace2 target, a helper, a pager)
// runs for a read of local files, and git takes no optional lock, so a check
// that runs beside another command never blocks it.
var localReadEnv = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=/dev/null",
	"GIT_OPTIONAL_LOCKS=0",
}

// localReadConfig is set by -c on every git call that doesn't reach the
// remote, so no credential helper or ssh command a store's config names can
// run. The three calls that reach the remote keep the user's own, which they
// need for credentials.
var localReadConfig = []string{
	"credential.helper=",
	"core.sshCommand=",
}

// emptyHooksKey keys the empty hooks directory's path on a context.
type emptyHooksKey struct{}

// withEmptyHooks returns ctx carrying dir, the empty hooks directory every git
// call made under it passes as core.hooksPath.
func withEmptyHooks(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, emptyHooksKey{}, dir)
}

// emptyHooksFrom returns the empty hooks directory ctx carries, if any.
func emptyHooksFrom(ctx context.Context) (string, bool) {
	dir, ok := ctx.Value(emptyHooksKey{}).(string)
	return dir, ok && dir != ""
}

// runGit runs a git subcommand with the base config plus config (each
// "key=value", passed as -c), in the cleaned environment, as a call that
// reads local files only. The error carries git's stderr.
func runGit(ctx context.Context, config []string, args ...string) error {
	_, err := gitOutput(ctx, config, "", args...)
	return err
}

// gitOutput runs git as runGit does, with stdin as its input, and returns
// what it printed on stdout.
func gitOutput(ctx context.Context, config []string, stdin string, args ...string) (string, error) {
	return gitExec(ctx, nil, config, stdin, args...)
}

// gitHook, when a test sets it, is called with the arguments of every git
// process the package starts, those after the -c pairs, just before the
// process starts. gitRun is the package's one place a process starts, so a
// test that sets it sees every git run a call makes, as probeHook and
// stageHook let tests see the probe and a restore's stages. It's nil outside
// the tests.
var gitHook func(args []string)

// gitProc is what gitProcHook sees of one git process: every -c pair it
// carries, the environment niwa added after localEnv's scrub, what a network
// call kept of the parent's environment (inheritedNetworkEnv; empty on a
// local call), and its arguments after the -c pairs.
type gitProc struct{ Config, Env, Inherited, Args []string }

// gitProcHook, when a test sets it, is called beside gitHook for every git
// process the package starts. It's nil outside the tests.
var gitProcHook func(gitProc)

// gitExec runs git as gitOutput does, with env added to the cleaned
// environment, as a call that reads local files only (see gitRun).
func gitExec(ctx context.Context, env, config []string, stdin string, args ...string) (string, error) {
	return gitRun(ctx, "", env, config, stdin, args...)
}

// The network calls, as gitExecNetwork names them to the test build's
// environment probe.
const (
	callClone     = "clone"
	callFetch     = "fetch"
	callFetchByID = "fetch-by-id"
)

// gitExecNetwork runs git as gitExec does, for call, one of the three calls
// that reach the remote (cloneMirror, fetchMirror and fetchByID): it keeps
// the user's system and global config, their credential helper and ssh
// command, and the remote-access variables of their environment
// (inheritedNetworkEnv), since the remote may need them.
func gitExecNetwork(ctx context.Context, call string, env, config []string, stdin string, args ...string) (string, error) {
	return gitRun(ctx, call, env, config, stdin, args...)
}

// gitRun is the one place the package starts a git process. It refuses to
// start one, before gitHook is called, unless ctx carries the empty hooks
// directory (withEmptyHooks) and that directory is a real, empty directory
// the user owns with mode 0700 (checkEmptyHooks): a hook planted there, or a
// link put in its place, would run inside every git call.
//
// netCall names a network call (callClone, callFetch or callFetchByID) and
// is empty for a local one. Every process starts from localEnv's scrub of
// the parent's environment; a network call gets inheritedNetworkEnv back on
// top, and its notes go on ctx (noteNetworkCall).
//
// Every process carries gitBaseConfig and core.hooksPath naming that
// directory, so no hook in a store, and no core.hooksPath its config sets,
// runs. A call that isn't network also carries localReadConfig and
// localReadEnv. GIT_TERMINAL_PROMPT=0 rides every call, whatever the parent's
// environment says, since nobody is there to answer: a mirror call runs
// inside niwa commands and the SessionStart hook, where a credential prompt
// would hang the command. A credential helper and GIT_ASKPASS still work.
// config and env, the caller's, come last.
func gitRun(ctx context.Context, netCall string, env, config []string, stdin string, args ...string) (string, error) {
	network := netCall != ""
	hooks, ok := emptyHooksFrom(ctx)
	if !ok {
		return "", errors.New("refusing to run git: no empty-hooks directory was set up for it")
	}
	if err := checkEmptyHooks(hooks); err != nil {
		return "", fmt.Errorf("refusing to run git: %w", err)
	}
	pairs := make([]string, 0, len(gitBaseConfig)+1+len(localReadConfig)+len(config))
	pairs = append(pairs, gitBaseConfig...)
	pairs = append(pairs, "core.hooksPath="+hooks)
	if !network {
		pairs = append(pairs, localReadConfig...)
	}
	pairs = append(pairs, config...)
	added := []string{"GIT_TERMINAL_PROMPT=0"}
	if !network {
		added = append(added, localReadEnv...)
	}
	added = append(added, env...)
	parent := os.Environ()
	var inherited []string
	if network {
		inherited = inheritedNetworkEnv(parent)
	}

	full := make([]string, 0, 2*len(pairs)+len(args))
	for _, kv := range pairs {
		full = append(full, "-c", kv)
	}
	full = append(full, args...)
	// The first argument that isn't an option names the subcommand.
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			sub = a
			break
		}
	}
	if gitHook != nil {
		gitHook(slices.Clone(args))
	}
	if gitProcHook != nil {
		gitProcHook(gitProc{Config: slices.Clone(pairs), Env: slices.Clone(added), Inherited: slices.Clone(inherited), Args: slices.Clone(args)})
	}
	procEnv := lastWins(append(append(localEnv(parent), inherited...), added...))
	if network {
		noteNetworkCall(ctx, inherited)
		testhook.ProbeGitEnv(testhook.KindNetwork, netCall, procEnv)
	} else {
		testhook.ProbeGitEnv(testhook.KindLocal, sub, procEnv)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	makeDisposable(ctx, cmd)
	cmd.Env = procEnv
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", sub, err, msg)
		}
		return "", fmt.Errorf("git %s: %w", sub, err)
	}
	return stdout.String(), nil
}

// configEntry is one key of a store's config, "section.name" or
// "section.subsection.name", and its value.
type configEntry struct{ key, value string }

// retentionConfig is the fixed set every store's config holds, so nothing a
// mirror ever fetched is dropped: no fetch prunes a ref, no gc or maintenance
// runs on its own, nothing unreachable or in a reflog expires, every ref
// update is logged, and every object a fetch brings is checked and lands in a
// pack. The store is written from this set whole, so no other key survives.
var retentionConfig = []configEntry{
	{"fetch.prune", "false"},
	{"gc.auto", "0"},
	{"maintenance.auto", "false"},
	{"gc.pruneExpire", "never"},
	{"core.logAllRefUpdates", "always"},
	{"gc.reflogExpire", "never"},
	{"gc.reflogExpireUnreachable", "never"},
	{"transfer.fsckObjects", "true"},
	{"fetch.fsckObjects", "true"},
	{"transfer.unpackLimit", "1"},
	{"fetch.unpackLimit", "1"},
}

// storeConfigEntries is the whole config of a store cloned from url: the
// repository's own two keys, the fixed retention set, and the remote, whose
// URL is the recorded form so no credential the config's URL carries reaches
// disk.
func storeConfigEntries(url string) []configEntry {
	entries := []configEntry{
		{"core.repositoryformatversion", "0"},
		{"core.bare", "true"},
	}
	entries = append(entries, retentionConfig...)
	return append(entries,
		configEntry{"remote.origin.url", recordURL(url)},
		configEntry{"remote.origin.fetch", "+refs/*:refs/*"},
	)
}

// renderConfig writes entries in git's config file format, one section per
// distinct section in the order each first appears. A value with a character
// git would read specially is quoted; one with a control character is
// refused, since it would end the line.
func renderConfig(entries []configEntry) (string, error) {
	type section struct {
		header string
		lines  []string
	}
	var order []string
	sections := map[string]*section{}
	for _, e := range entries {
		dot := strings.LastIndex(e.key, ".")
		if dot <= 0 || dot == len(e.key)-1 {
			return "", fmt.Errorf("config key %q has no section and name", e.key)
		}
		sec, name := e.key[:dot], e.key[dot+1:]
		value, err := configValue(e.value)
		if err != nil {
			return "", fmt.Errorf("config key %s: %w", e.key, err)
		}
		s, ok := sections[sec]
		if !ok {
			header := "[" + sec + "]"
			if first, sub, found := strings.Cut(sec, "."); found {
				header = fmt.Sprintf("[%s %q]", first, sub)
			}
			s = &section{header: header}
			sections[sec] = s
			order = append(order, sec)
		}
		s.lines = append(s.lines, "\t"+name+" = "+value)
	}
	var b strings.Builder
	for _, sec := range order {
		b.WriteString(sections[sec].header + "\n")
		for _, line := range sections[sec].lines {
			b.WriteString(line + "\n")
		}
	}
	return b.String(), nil
}

// configValue is v as a config file value: as it is when git reads it back
// unchanged, otherwise in double quotes with backslashes and quotes escaped.
func configValue(v string) (string, error) {
	for _, c := range v {
		if c < 0x20 || c == 0x7f {
			return "", fmt.Errorf("value holds a control character")
		}
	}
	if !strings.ContainsAny(v, "\"\\;#") && strings.TrimSpace(v) == v {
		return v, nil
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`, nil
}

// writeStoreConfig replaces store's config with the fixed one for url. It's
// written under a temporary name in the store and renamed over config, so a
// check that reads it without the lock never sees half a file. The file is
// 0600, set explicitly rather than left to the umask.
func writeStoreConfig(store, url string) error {
	text, err := renderConfig(storeConfigEntries(url))
	if err != nil {
		return err
	}
	suffix, err := randHex()
	if err != nil {
		return err
	}
	tmp := filepath.Join(store, ".niwa-config-"+suffix)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing the store's config: %w", err)
	}
	werr := f.Chmod(0o600)
	if werr == nil {
		_, werr = f.WriteString(text)
	}
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, filepath.Join(store, "config"))
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing the store's config: %w", werr)
	}
	return nil
}

// fetchReflogAction is the reflog message a refresh's ref updates carry. git
// otherwise writes its own command line there, the URL included, and the
// store keeps its reflogs forever.
const fetchReflogAction = "GIT_REFLOG_ACTION=niwa mirror refresh"

// fetchMirror brings every ref of url into store and prunes none: a ref the
// remote deleted stays, and a ref the remote moved is forced to its new value
// with the old one kept in the store's reflog. url is the config's, as given,
// on the command line only, so a credential it carries works but is never
// written to the store: the reflog message is fixed, and no FETCH_HEAD, which
// would name the URL, is written.
func fetchMirror(ctx context.Context, store, url string) error {
	_, err := gitExecNetwork(ctx, callFetch, []string{fetchReflogAction}, nil, "",
		"--git-dir="+store, "fetch", "--no-prune", "--no-write-fetch-head", "--quiet", "--", url, "+refs/*:refs/*")
	return err
}

// fetchByID fetches the commits ids, and what they reach that the store
// lacks, into store from url, the config's URL as given, on the command line
// only. It runs with protocol version 2, whose fetch serves a commit by id
// even when no advertised ref names it, and fetches to no destination, so it
// writes no ref, no tag and no FETCH_HEAD, and the URL reaches no reflog.
// The store's own config holds fsckObjects and unpack limits of 1, so what
// arrives is checked and lands as a pack. Nothing names the fetched commits
// afterwards; the caller pins them.
//
// Every id must be a full-length commit id; anything else is refused before
// git runs, so nothing a remote or a borrower chose can reach the command
// line as an option. The ids follow "--" and the URL.
func fetchByID(ctx context.Context, store, url string, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("no commit ids to fetch")
	}
	for _, id := range ids {
		if !commitIDPattern.MatchString(id) {
			return fmt.Errorf("refusing to fetch %q: not a full-length commit id", id)
		}
	}
	args := append([]string{"--git-dir=" + store,
		"fetch", "--no-write-fetch-head", "--no-tags", "--quiet", "--", url}, ids...)
	_, err := gitExecNetwork(ctx, callFetchByID, []string{fetchReflogAction}, []string{"protocol.version=2"}, "", args...)
	return err
}

// verifyPack runs git verify-pack on a pack's .idx, which checks the index
// and every object in the pack it names against their checksums. It reads
// local files only.
func verifyPack(ctx context.Context, idx string) error {
	return runGit(ctx, nil, "verify-pack", "--", idx)
}

// headCommitReadable reports whether the bare repo at store resolves HEAD to
// a commit it can read.
func headCommitReadable(ctx context.Context, store string) error {
	return runGit(ctx, nil, "--git-dir="+store, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
}

// cloneMirror clones url as a bare mirror into dir. The clone runs with no
// template, so the new store has no hooks, and with fsckObjects and unpack
// limits of 1 set for the process itself, since the new store has no config
// to hold them yet: a malformed object from the remote is refused before any
// borrower can read it, and everything lands as a pack.
//
// url is the workspace config's, as given. git records it in the new store's
// config, which the restore then replaces whole with the fixed one
// (writeStoreConfig) before anything else reads the store.
func cloneMirror(ctx context.Context, url, dir string) error {
	_, err := gitExecNetwork(ctx, callClone, nil,
		[]string{
			"transfer.fsckObjects=true",
			"fetch.fsckObjects=true",
			"transfer.unpackLimit=1",
			"fetch.unpackLimit=1",
		},
		"", "clone", "--mirror", "--template=", "--quiet", "--", url, dir)
	return err
}
