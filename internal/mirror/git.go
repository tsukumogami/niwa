package mirror

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// gitEnv is the environment every git call here runs with: the parent's, with
// every inherited GIT_* variable removed (GIT_DIR, GIT_EXEC_PATH and
// GIT_ALTERNATE_OBJECT_DIRECTORIES among them, any of which would point git
// somewhere else), and no terminal prompt, since nobody is there to answer.
func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

// runGit runs a git subcommand with the base config plus config (each
// "key=value", passed as -c), in the cleaned environment. The error carries
// git's stderr.
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
// process starts. gitExec is the package's one place a process starts, so a
// test that sets it sees every git run a call makes, as probeHook and
// stageHook let tests see the probe and a restore's stages. It's nil outside
// the tests.
var gitHook func(args []string)

// gitExec runs git as gitOutput does, with env added to the cleaned
// environment.
func gitExec(ctx context.Context, env, config []string, stdin string, args ...string) (string, error) {
	full := make([]string, 0, 2*(len(gitBaseConfig)+len(config))+len(args))
	for _, kv := range append(append([]string{}, gitBaseConfig...), config...) {
		full = append(full, "-c", kv)
	}
	full = append(full, args...)
	if gitHook != nil {
		gitHook(slices.Clone(args))
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(gitEnv(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// The first argument that isn't an option names the subcommand.
		sub := args[0]
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				sub = a
				break
			}
		}
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
// check that reads it without the lock never sees half a file.
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
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("writing the store's config: %w", err)
	}
	_, werr := f.WriteString(text)
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
	_, err := gitExec(ctx, []string{fetchReflogAction}, nil, "",
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
	_, err := gitExec(ctx, []string{fetchReflogAction}, []string{"protocol.version=2"}, "", args...)
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
	return runGit(ctx,
		[]string{
			"transfer.fsckObjects=true",
			"fetch.fsckObjects=true",
			"transfer.unpackLimit=1",
			"fetch.unpackLimit=1",
		},
		"clone", "--mirror", "--template=", "--quiet", "--", url, dir)
}
