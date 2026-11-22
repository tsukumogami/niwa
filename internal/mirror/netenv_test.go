package mirror

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// configEnv returns the GIT_CONFIG_COUNT variables for keys and values, in
// pairs, as a parent environment would hold them.
func configEnv(pairs ...string) []string {
	env := []string{"GIT_CONFIG_COUNT=" + strconv.Itoa(len(pairs)/2)}
	for i := 0; i+1 < len(pairs); i += 2 {
		n := strconv.Itoa(i / 2)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+pairs[i], "GIT_CONFIG_VALUE_"+n+"="+pairs[i+1])
	}
	return env
}

// configVals is environ's GIT_* variables by name, as networkConfig reads them.
func configVals(environ []string) map[string]string {
	vals := map[string]string{}
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		vals[name] = value
	}
	return vals
}

func TestNetworkConfig_KeepsOnlyKeysThatCarryRemoteAccess(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "mixed case is compared case-folded",
			in:   configEnv("HTTP.ExtraHeader", "X-A: 1", "Include.Path", "/elsewhere"),
			want: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=HTTP.ExtraHeader", "GIT_CONFIG_VALUE_0=X-A: 1"},
		},
		{
			name: "a dropped entry between two kept ones renumbers the second",
			in: configEnv(
				"http.proxy", "http://proxy.example:3128",
				"remote.origin.prune", "true",
				"url.https://mirror.example/.insteadOf", "https://example.com/"),
			want: []string{
				"GIT_CONFIG_COUNT=2",
				"GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=http://proxy.example:3128",
				"GIT_CONFIG_KEY_1=url.https://mirror.example/.insteadOf", "GIT_CONFIG_VALUE_1=https://example.com/",
			},
		},
		{
			name: "keys that would undo a guard are dropped",
			in: configEnv(
				"gc.auto", "1",
				"gc.pruneExpire", "now",
				"fetch.prune", "true",
				"fetch.pruneTags", "true",
				"core.hooksPath", "/hooks",
				"core.fsmonitor", "/monitor",
				"core.alternateRefsCommand", "/cmd",
				"include.path", "/elsewhere",
				"includeIf.gitdir:/x/.path", "/elsewhere",
				"remote.origin.fetch", "+refs/heads/*:refs/heads/*",
				"maintenance.auto", "true",
				"uploadpack.allowAnySHA1InWant", "true",
				"safe.directory", "*",
				"extensions.objectFormat", "sha256",
				"url.https://mirror.example/.pushInsteadOf", "https://example.com/",
				"url..insteadOf", "https://example.com/",
				"http.", "x",
				"core.sshCommandX", "/ssh"),
			want: []string{"GIT_CONFIG_COUNT=0"},
		},
		{
			name: "every remote-access key is kept",
			in: configEnv(
				"http.https://example.com/.extraHeader", "X-B: 2",
				"https.proxy", "p",
				"credential.helper", "store",
				"protocol.allow", "user",
				"ssh.variant", "ssh",
				"core.sshCommand", "ssh -i key",
				"core.gitProxy", "/proxy",
				"core.askPass", "/askpass",
				"URL.git@example.com:.INSTEADOF", "https://example.com/"),
			want: append([]string{"GIT_CONFIG_COUNT=9"}, configEnv(
				"http.https://example.com/.extraHeader", "X-B: 2",
				"https.proxy", "p",
				"credential.helper", "store",
				"protocol.allow", "user",
				"ssh.variant", "ssh",
				"core.sshCommand", "ssh -i key",
				"core.gitProxy", "/proxy",
				"core.askPass", "/askpass",
				"URL.git@example.com:.INSTEADOF", "https://example.com/")[1:]...),
		},
		{
			name: "a count with leading zeros is the decimal git reads",
			in:   []string{"GIT_CONFIG_COUNT=01", "GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=p"},
			want: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=p"},
		},
		{
			name: "an empty value is kept as given",
			in:   configEnv("credential.helper", ""),
			want: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0="},
		},
		{
			name: "no count, no entries",
			in:   []string{"GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=p"},
			want: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := networkConfig(configVals(c.in)); !slices.Equal(got, c.want) {
				t.Fatalf("networkConfig(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestNetworkConfig_ABadCountDropsTheCountAndEveryEntry(t *testing.T) {
	entries := configEnv("http.proxy", "p", "credential.helper", "store")[1:]
	for _, count := range []string{"abc", "-1", "+1", " 1", "1 ", "", "0x1", "65", "3", "99999999999999999999"} {
		t.Run(count, func(t *testing.T) {
			in := append([]string{"GIT_CONFIG_COUNT=" + count}, entries...)
			if got := networkConfig(configVals(in)); got != nil {
				t.Fatalf("networkConfig with GIT_CONFIG_COUNT=%q = %q, want nothing", count, got)
			}
		})
	}
	t.Run("a value missing below the count", func(t *testing.T) {
		in := []string{"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=http.proxy", "GIT_CONFIG_VALUE_0=p", "GIT_CONFIG_KEY_1=credential.helper"}
		if got := networkConfig(configVals(in)); got != nil {
			t.Fatalf("networkConfig(%q) = %q, want nothing", in, got)
		}
	})
	t.Run("64 is the largest count kept", func(t *testing.T) {
		var pairs []string
		for i := 0; i < 64; i++ {
			pairs = append(pairs, "http.h"+strconv.Itoa(i)+".extraHeader", "v")
		}
		got := networkConfig(configVals(configEnv(pairs...)))
		if len(got) != 1+2*64 || got[0] != "GIT_CONFIG_COUNT=64" {
			t.Fatalf("networkConfig with 64 entries kept %d variables starting %q, want all 129", len(got), got[:1])
		}
	})
}

// remoteAccessEnv is a value for each of networkVars that does nothing on a
// file:// remote.
func remoteAccessEnv(dir string) []string {
	return []string{
		"GIT_SSH=" + filepath.Join(dir, "no-such-ssh"),
		"GIT_SSH_COMMAND=" + filepath.Join(dir, "no-such-ssh-command"),
		"GIT_SSH_VARIANT=ssh",
		"GIT_ASKPASS=" + filepath.Join(dir, "no-such-askpass"),
		"GIT_PROXY_COMMAND=" + filepath.Join(dir, "no-such-proxy"),
		"GIT_SSL_CAINFO=" + filepath.Join(dir, "no-such-ca.pem"),
		"GIT_SSL_CAPATH=" + filepath.Join(dir, "no-such-ca-dir"),
		"GIT_SSL_NO_VERIFY=",
		"GIT_HTTP_PROXY_AUTHMETHOD=basic",
	}
}

func TestInheritedNetworkEnv_KeepsTheListedVariablesAndNoOther(t *testing.T) {
	listed := remoteAccessEnv("/x")
	environ := append([]string{
		"PATH=/bin",
		"GIT_DIR=/unrelated/repo.git",
		"GIT_WORK_TREE=/unrelated",
		"GIT_EXEC_PATH=/unrelated/libexec",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/unrelated/objects",
		"GIT_OBJECT_DIRECTORY=/unrelated/objects",
		"GIT_INDEX_FILE=/unrelated/index",
		"GIT_CONFIG=/unrelated/config",
		"GIT_CONFIG_GLOBAL=/unrelated/gitconfig",
		"GIT_CONFIG_PARAMETERS='core.hookspath'='/hooks'",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_TRACE=1",
		"GIT_REFLOG_ACTION=unrelated",
	}, listed...)
	environ = append(environ, configEnv("http.extraHeader", "X-C: 3", "remote.origin.prune", "true")...)
	want := append(slices.Clone(listed), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=X-C: 3")
	if got := inheritedNetworkEnv(environ); !slices.Equal(got, want) {
		t.Fatalf("inheritedNetworkEnv\n got %q\nwant %q", got, want)
	}
	for _, kv := range localEnv(environ) {
		if strings.HasPrefix(kv, "GIT_") {
			t.Fatalf("localEnv kept %q", kv)
		}
	}
}

func TestLastWins_KeepsEachNameOnceAtItsLastValue(t *testing.T) {
	got := lastWins([]string{"A=1", "GIT_TERMINAL_PROMPT=1", "B=2", "GIT_TERMINAL_PROMPT=0", "A=3"})
	want := []string{"A=3", "GIT_TERMINAL_PROMPT=0", "B=2"}
	if !slices.Equal(got, want) {
		t.Fatalf("lastWins = %q, want %q", got, want)
	}
}

// setParentEnv sets each "NAME=value" in env for the rest of the test. A
// fixture's own git runs read the parent's environment, so a test sets it
// only once the fixture is built.
func setParentEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		t.Setenv(name, value)
	}
}

// unrelatedRepoEnv points GIT_DIR and GIT_ALTERNATE_OBJECT_DIRECTORIES at a
// repository that has nothing to do with the mirror.
func unrelatedRepoEnv(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "unrelated.git")
	testGit(t, filepath.Dir(dir), "init", "--quiet", "--bare", dir)
	return []string{
		"GIT_DIR=" + dir,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(dir, "objects"),
	}
}

// requireInherited fails unless every network process among procs kept
// exactly want of the parent's environment and every local one kept nothing,
// and at least one of each ran.
func requireInherited(t *testing.T, procs []gitProc, want []string, network ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, p := range procs {
		sub := subcommand(p.Args)
		if sub == "clone" || sub == "fetch" {
			if !slices.Equal(p.Inherited, want) {
				t.Fatalf("network git %q kept\n %q\nwant %q", p.Args, p.Inherited, want)
			}
			seen[sub] = true
			continue
		}
		if len(p.Inherited) != 0 {
			t.Fatalf("local git %q kept %q of the parent's environment, want nothing", p.Args, p.Inherited)
		}
	}
	for _, sub := range network {
		if !seen[sub] {
			t.Fatalf("no network git %s ran among %d processes", sub, len(procs))
		}
	}
}

func TestNetworkEnv_NetworkCallsKeepTheVariablesAndLocalCallsNone(t *testing.T) {
	parent := func(t *testing.T) (set, want []string) {
		listed := remoteAccessEnv(t.TempDir())
		set = append(slices.Clone(listed), unrelatedRepoEnv(t)...)
		set = append(set, "GIT_TERMINAL_PROMPT=1", "GIT_TRACE_UNLISTED=1")
		set = append(set, configEnv("include.path", "/no-such-include", "http.extraHeader", "X-D: 4")...)
		want = append(listed, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=X-D: 4")
		return set, want
	}

	t.Run("seed", func(t *testing.T) {
		f := newFixture(t)
		set, want := parent(t)
		setParentEnv(t, set)
		procs := recordGitProcs(t)
		requireRestored(t, "Seed", seedCall(t, f, f.seedOpts()))
		requireProcs(t, f.home, procs(), "clone")
		requireInherited(t, procs(), want, "clone")
	})
	t.Run("refresh", func(t *testing.T) {
		f := built(t)
		commit := pushCommit(t, f, "main", "second")
		set, want := parent(t)
		setParentEnv(t, set)
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, refreshing(f.applyOpts())), FaultNone, OutcomeHealthy)
		requireInherited(t, procs(), want, "fetch")
		if !f.storeHas(t, commit) {
			t.Fatalf("the refresh didn't bring %s", commit)
		}
	})
	t.Run("restore and fetch by id", func(t *testing.T) {
		f := newLostFixture(t, lostSetup{})
		f.dropTopic(t, f.remote, true)
		f.deleteMirror(t)
		set, want := parent(t)
		setParentEnv(t, set)
		procs := recordGitProcs(t)
		res := f.ensure(t, f.opts())
		requireOutcome(t, res, FaultMissing, OutcomeRestored)
		if len(res.Unrecoverable) != 0 {
			t.Fatalf("the recovery left %v unrecoverable", res.Unrecoverable)
		}
		all := procs()
		requireProcs(t, f.home, all, "clone", "fetch", "for-each-ref", "rev-list")
		requireInherited(t, all, want, "clone", "fetch")
		fetches := 0
		for _, p := range all {
			if subcommand(p.Args) == "fetch" {
				fetches++
			}
		}
		if fetches == 0 {
			t.Fatal("no fetch by id ran")
		}
	})
	t.Run("healthy check of a mirror with a borrower", func(t *testing.T) {
		f := built(t)
		borrower := realBorrower(t, f)
		opts := f.applyOpts()
		opts.Instances = []string{borrower}
		set, _ := parent(t)
		setParentEnv(t, set)
		procs := recordGitProcs(t)
		requireOutcome(t, ensureCall(t, f, opts), FaultNone, OutcomeHealthy)
		moveCtime(t, f)
		requireOutcome(t, ensureCall(t, f, opts), FaultNone, OutcomeHealthy)
		requireProcs(t, f.home, procs(), "rev-parse", "verify-pack")
		requireInherited(t, procs(), nil)
	})
}

// envWrapper writes a stand-in for ssh, as sshWrapper does, that also
// writes the GIT_* part of the environment git started it with to envFile.
func envWrapper(t *testing.T) (command, envFile string) {
	t.Helper()
	dir := t.TempDir()
	envFile = filepath.Join(dir, "env")
	script := filepath.Join(dir, "ssh-wrapper.sh")
	body := "#!/bin/sh\n" +
		"env | grep '^GIT_' > '" + envFile + "'\n" +
		"for last; do :; done\n" +
		"case \"$last\" in\n" +
		"git-upload-pack\\ *) exec sh -c \"git ${last#git-}\" ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "'" + script + "'", envFile
}

// The transport git runs for a clone sees the remote-access variables, and
// GIT_TERMINAL_PROMPT=0 whatever the parent's says. git itself clears the
// repository variables (GIT_DIR, GIT_CONFIG_COUNT and the like) before it
// starts a transport, so those are checked through gitProc above instead.
func TestNetworkEnv_TheTransportSeesTheVariablesAndNoPrompt(t *testing.T) {
	f := newFixture(t)
	command, envFile := envWrapper(t)
	setParentEnv(t, []string{
		"GIT_SSH_COMMAND=" + command,
		"GIT_SSH_VARIANT=simple",
		"GIT_ASKPASS=/no-such-askpass",
		"GIT_SSL_CAINFO=/no-such-ca.pem",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_TRACE_UNLISTED=1",
	})
	repo := Repo{ID: "app", URL: sshOnlyHost + ":" + f.remote}
	if res := Seed(context.Background(), f.seedOpts(), repo); res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seeding through the wrapper = %+v", res)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	for _, kv := range []string{"GIT_SSH_COMMAND=" + command, "GIT_SSH_VARIANT=simple", "GIT_ASKPASS=/no-such-askpass", "GIT_SSL_CAINFO=/no-such-ca.pem", "GIT_TERMINAL_PROMPT=0"} {
		if !slices.Contains(got, kv) {
			t.Errorf("the transport's environment %q lacks %q", got, kv)
		}
	}
	for _, kv := range got {
		if strings.HasPrefix(kv, "GIT_TRACE_UNLISTED=") || kv == "GIT_TERMINAL_PROMPT=1" {
			t.Errorf("the transport's environment carries %q", kv)
		}
	}
}

func TestNetworkEnv_ConfigEntriesReachTheProcessAndNiwasPairsWin(t *testing.T) {
	f := newFixture(t)
	if err := prepareEmptyHooks(f.home); err != nil {
		t.Fatal(err)
	}
	ctx := withEmptyHooks(context.Background(), emptyHooksDir(f.home))
	setParentEnv(t, configEnv(
		"protocol.ext.allow", "always",
		"include.path", filepath.Join(t.TempDir(), "never-read"),
		"http.extraHeader", "X-E: 5"))
	get := func(key string) (string, error) {
		out, err := gitExecNetwork(ctx, callFetch, nil, nil, "", "config", "--get", key)
		return strings.TrimSpace(out), err
	}
	if v, err := get("http.extraHeader"); err != nil || v != "X-E: 5" {
		t.Fatalf("http.extraHeader = %q (%v), want the kept entry's value", v, err)
	}
	if v, err := get("protocol.ext.allow"); err != nil || v != "never" {
		t.Fatalf("protocol.ext.allow = %q (%v), want niwa's own never over the environment's always", v, err)
	}
	if v, err := get("include.path"); err == nil {
		t.Fatalf("include.path reached the process as %q", v)
	}
}

func TestNetworkEnv_TheLogNamesWhatWasHonouredAndNoValue(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	sentinels := []string{"niwa-sentinel-ssh", "niwa-sentinel-askpass", "niwa-sentinel-config", "niwa-sentinel-proxy"}
	setParentEnv(t, append([]string{
		"GIT_SSH_COMMAND=/" + sentinels[0],
		"GIT_ASKPASS=/" + sentinels[1],
		"GIT_PROXY_COMMAND=/" + sentinels[3],
		"GIT_SSL_NO_VERIFY=1",
	}, configEnv("http.extraHeader", "X-Sentinel: "+sentinels[2])...))
	requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultMissing, OutcomeRestored)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.mdir(), ".store")); err != nil {
		t.Fatal(err)
	}
	requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultMissing, OutcomeRestored)
	requireOutcome(t, ensureCall(t, f, refreshing(f.applyOpts())), FaultNone, OutcomeHealthy)

	data, err := os.ReadFile(logPath(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sentinels {
		if strings.Contains(string(data), s) {
			t.Fatalf("the mirror log carries %q:\n%s", s, data)
		}
	}
	events := map[string]int{}
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatal(err)
		}
		event, _ := line["event"].(string)
		notes, has := line["network_notes"]
		switch event {
		case eventRestore, eventRefresh:
			events[event]++
			b, _ := json.Marshal(notes)
			if string(b) != `["ssl-no-verify","proxy-command"]` {
				t.Fatalf("%s line %s carries network_notes %s", event, raw, b)
			}
		default:
			if has {
				t.Fatalf("%s line %s carries network_notes", event, raw)
			}
		}
	}
	if events[eventRestore] != 2 || events[eventRefresh] != 1 {
		t.Fatalf("the log holds %v restore and refresh lines, want 2 and 1", events)
	}
}

func TestNetworkEnv_NoNoteWithoutAnHonouredVariable(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	setParentEnv(t, configEnv("core.gitProxy", "/proxy-for-git-urls"))
	requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultMissing, OutcomeRestored)
	t.Setenv("GIT_CONFIG_COUNT", "0")
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.mdir(), ".store")); err != nil {
		t.Fatal(err)
	}
	requireOutcome(t, ensureCall(t, f, f.applyOpts()), FaultMissing, OutcomeRestored)
	data, err := os.ReadFile(logPath(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var line struct {
			Event        string   `json:"event"`
			NetworkNotes []string `json:"network_notes"`
		}
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatal(err)
		}
		if line.Event == eventRestore {
			notes = append(notes, strings.Join(line.NetworkNotes, ","))
		}
	}
	if want := []string{"proxy-command", ""}; !slices.Equal(notes, want) {
		t.Fatalf("the restore lines' notes = %q, want %q", notes, want)
	}
}
