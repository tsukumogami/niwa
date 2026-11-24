package removal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// gitCall is one git call gitCommand built: the git directory it names and
// its subcommand and arguments.
type gitCall struct {
	gitDir string
	args   []string
}

// callLog records every git call an Env builds, in order.
type callLog struct {
	mu    sync.Mutex
	calls []gitCall
	// before, when set, runs as each call is built, before it starts.
	before func(c gitCall, n int)
}

func (l *callLog) attach(e *Env) {
	e.observe = func(r repoDirs, args []string) {
		l.mu.Lock()
		c := gitCall{gitDir: r.GitDir, args: append([]string(nil), args...)}
		l.calls = append(l.calls, c)
		n := len(l.calls)
		before := l.before
		l.mu.Unlock()
		if before != nil {
			before(c, n)
		}
	}
}

// in returns the subcommands of every call naming gitDir, in order.
func (l *callLog) in(gitDir string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, c := range l.calls {
		if c.gitDir == gitDir {
			out = append(out, c.args[0])
		}
	}
	return out
}

// markerScript writes a script that creates a marker file and passes its
// input through, as a filter would, and returns the script and the marker.
func markerScript(t *testing.T) (script, marker string) {
	t.Helper()
	dir := outsideDir(t)
	marker = filepath.Join(dir, "ran")
	script = filepath.Join(dir, "program.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func assertNoMarker(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a program the repository's configuration names ran")
	}
}

// selectFilter makes every path in the clone select filter x and leaves the
// tracked README stat-dirty with unchanged content, so a status there would
// run the filter's clean program if one were configured.
func selectFilter(t *testing.T, f fixture) {
	t.Helper()
	writeFile(t, filepath.Join(f.clone, ".git", "info", "attributes"), "* filter=x\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(f.clone, "README"), later, later); err != nil {
		t.Fatal(err)
	}
}

// TestSelectFilterMakesAStatusRunTheFilter is the control for the tests
// below that expect no marker: in the same setup, a plain git status with
// the filter configured does run it.
func TestSelectFilterMakesAStatusRunTheFilter(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	selectFilter(t, f)
	gitRun(t, f.clone, "config", "filter.x.clean", script)
	gitRun(t, f.clone, "status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("git status didn't run the filter, so the setup proves nothing: %v", err)
	}
}

func TestCanonicalKey(t *testing.T) {
	cases := map[string]string{
		"Core.FSMonitor":                  "core.fsmonitor",
		"Credential.Helper":               "credential.helper",
		"Filter.MyDriver.Clean":           "filter.MyDriver.clean",
		"credential.https://Host.HELPER":  "credential.https://Host.helper",
		"GPG.SSH.Program":                 "gpg.SSH.program",
		"core.sshCommand":                 "core.sshcommand",
		"nodot":                           "nodot",
		"Diff.External":                   "diff.external",
		"remote.Origin.Promisor":          "remote.Origin.promisor",
		"extensions.partialClone":         "extensions.partialclone",
		"merge.Ours.Driver":               "merge.Ours.driver",
		"diff.Pdf.TextConv":               "diff.Pdf.textconv",
		"includeIf.gitdir:/X/.path":       "includeif.gitdir:/X/.path",
		"url.https://h/.insteadOf":        "url.https://h/.insteadof",
		"filter.lfs.process":              "filter.lfs.process",
		"core.fsmonitorHookVersion":       "core.fsmonitorhookversion",
		"sequence.editor":                 "sequence.editor",
		"gpg.x509.program":                "gpg.x509.program",
		"user.name":                       "user.name",
		"branch.Main.remote":              "branch.Main.remote",
		"Filter.X.Required":               "filter.X.required",
		"credential.helper":               "credential.helper",
		"difftool.Vim.Cmd":                "difftool.Vim.cmd",
		"core.hooksPath":                  "core.hookspath",
		"credential.https://h/x.y.helper": "credential.https://h/x.y.helper",
	}
	for in, want := range cases {
		if got := canonicalKey(in); got != want {
			t.Errorf("canonicalKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNamesProgramRefusesEveryListedKeyAndPattern holds one case per key on
// the refused list and per pattern, each written in git's canonical case
// and in another case, and the keys that name no program.
func TestNamesProgramRefusesEveryListedKeyAndPattern(t *testing.T) {
	refused := []struct{ key, value string }{
		{"filter.x.clean", "cat"},
		{"filter.x.smudge", "cat"},
		{"filter.x.process", "cat"},
		{"diff.external", "x"},
		{"diff.x.textconv", "x"},
		{"diff.x.command", "x"},
		{"merge.x.driver", "x"},
		{"credential.helper", "store"},
		{"credential.https://host.helper", "store"},
		{"gpg.program", "x"},
		{"gpg.ssh.program", "x"},
		{"gpg.x509.program", "x"},
		{"core.fsmonitor", "/tmp/hook.sh"},
		// *.program
		{"browser.tool.program", "x"},
		// *.command
		{"tool.x.command", "x"},
		// *Command
		{"core.sshCommand", "x"},
		// *.helper
		{"tool.helper", "x"},
	}
	for _, c := range refused {
		for _, key := range []string{c.key, strings.ToUpper(c.key[:1]) + c.key[1:], mixCase(c.key)} {
			t.Run(key, func(t *testing.T) {
				if !namesProgram(key, c.value) {
					t.Fatalf("namesProgram(%q, %q) = false, want true", key, c.value)
				}
			})
		}
	}
	allowed := []struct{ key, value string }{
		{"filter.x.required", "true"},
		{"core.fsmonitor", "true"},
		{"Core.FSMonitor", "FALSE"},
		{"core.hooksPath", "/dev/null"},
		{"user.name", "x"},
		{"remote.origin.url", "x"},
		{"sequence.editor", "x"},
		{"core.bare", "false"},
		{"remote.origin.promisor", "true"},
		{"commandline.flag", "x"},
	}
	for _, c := range allowed {
		if namesProgram(c.key, c.value) {
			t.Errorf("namesProgram(%q, %q) = true, want false", c.key, c.value)
		}
	}
}

// mixCase upper-cases the section and variable names of key, leaving a
// subsection as written.
func mixCase(key string) string {
	first, last := strings.Index(key, "."), strings.LastIndex(key, ".")
	if first < 0 {
		return strings.ToUpper(key)
	}
	return strings.ToUpper(key[:first]) + key[first:last] + strings.ToUpper(key[last:])
}

// TestEvaluateRefusesEachProgramNamingKey sets each key on the refused list,
// and one of each pattern, in a clone's configuration, and checks the unit
// keeps with the key named and nothing past the configuration read runs in
// that repository.
func TestEvaluateRefusesEachProgramNamingKey(t *testing.T) {
	keys := []struct{ set, want string }{
		{"filter.x.clean", "filter.x.clean"},
		{"filter.x.smudge", "filter.x.smudge"},
		{"filter.x.process", "filter.x.process"},
		{"diff.external", "diff.external"},
		{"diff.x.textconv", "diff.x.textconv"},
		{"diff.x.command", "diff.x.command"},
		{"merge.x.driver", "merge.x.driver"},
		{"credential.helper", "credential.helper"},
		{"credential.https://host.helper", "credential.https://host.helper"},
		{"gpg.program", "gpg.program"},
		{"gpg.ssh.program", "gpg.ssh.program"},
		{"gpg.x509.program", "gpg.x509.program"},
		{"core.fsmonitor", "core.fsmonitor"},
		{"browser.tool.program", "browser.tool.program"},
		{"tool.x.command", "tool.x.command"},
		{"core.sshCommand", "core.sshcommand"},
		{"tool.helper", "tool.helper"},
		{"Core.FSMonitor", "core.fsmonitor"},
		{"Credential.Helper", "credential.helper"},
	}
	for _, k := range keys {
		t.Run(k.set, func(t *testing.T) {
			f := checkedOut(t)
			script, marker := markerScript(t)
			selectFilter(t, f)
			gitRun(t, f.clone, "config", k.set, script)
			p := f.policy(t)
			var log callLog
			log.attach(p.Env)

			v := Evaluate(context.Background(), f.unit(), p)
			keepWith(t, v, "tools/app: repository configuration names a program: "+k.want)
			if got := log.in(filepath.Join(f.clone, ".git")); strings.Join(got, ",") != "config" {
				t.Errorf("git calls in the repository %v; want the configuration read alone", got)
			}
			assertNoMarker(t, marker)
		})
	}
}

func TestEvaluateRefusesAKeyInTheWorktreeScope(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	selectFilter(t, f)
	gitRun(t, f.clone, "config", "extensions.worktreeConfig", "true")
	gitRun(t, f.clone, "config", "--worktree", "filter.x.clean", script)
	if strings.Contains(gitRun(t, f.clone, "config", "--local", "--list"), "filter.x.clean") {
		t.Fatal("the driver landed in the local file, not config.worktree")
	}
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app: repository configuration names a program: filter.x.clean")
	assertNoMarker(t, marker)
}

func TestEvaluateRefusesAKeyInAnIncludedFile(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	inc := filepath.Join(outsideDir(t), "included")
	writeFile(t, inc, "[credential]\n\thelper = "+script+"\n")
	gitRun(t, f.clone, "config", "include.path", inc)
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app: repository configuration names a program: credential.helper")
	assertNoMarker(t, marker)
}

// TestEvaluateRunsNoSigningProgramOnASignedStash keeps a clone whose stash
// holds a signed commit, with log.showSignature on and a gpg.program that
// writes a marker, and runs no signing program.
func TestEvaluateRunsNoSigningProgramOnASignedStash(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	tree := gitRun(t, f.clone, "rev-parse", "HEAD^{tree}")
	head := gitRun(t, f.clone, "rev-parse", "HEAD")
	body := "tree " + tree + "\nparent " + head + "\n" +
		"author t <t@example.com> 0 +0000\ncommitter t <t@example.com> 0 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE\n -----END PGP SIGNATURE-----\n\nOn main: signed\n"
	obj := filepath.Join(outsideDir(t), "commit")
	writeFile(t, obj, body)
	id := gitRun(t, f.clone, "hash-object", "-t", "commit", "-w", obj)
	gitRun(t, f.clone, "update-ref", "--create-reflog", "-m", "On main: signed", "refs/stash", id)
	gitRun(t, f.clone, "config", "log.showSignature", "true")
	gitRun(t, f.clone, "config", "gpg.program", script)

	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	keepWith(t, v, "tools/app: repository configuration names a program: gpg.program")
	assertNoMarker(t, marker)
}

// TestConfigReadIsFirstInEveryRepositoryBeforeAnyOtherCall records the git
// calls of one evaluation across a clone, a nested repository and a
// worktree whose working tree is gone, and checks each one's configuration
// read comes before the first other call in any of them, the pruned
// index's diff-index included.
func TestConfigReadIsFirstInEveryRepositoryBeforeAnyOtherCall(t *testing.T) {
	f := checkedOut(t)
	prunableWorktree(t, f, nil)
	commitRepo(t, filepath.Join(f.clone, "vendor", "lib"))
	p := f.policy(t)
	var log callLog
	log.attach(p.Env)

	v := Evaluate(context.Background(), f.unit(), p)
	if len(v.Repos) < 3 {
		t.Fatalf("repositories %+v; want the clone, the nested repository and the pruned worktree", v.Repos)
	}
	firstOther := -1
	for i, c := range log.calls {
		if c.args[0] != "config" {
			firstOther = i
			break
		}
	}
	if firstOther < 0 {
		t.Fatalf("calls %+v: no call past the configuration reads", log.calls)
	}
	sawDiffIndex := false
	for _, r := range v.Repos {
		calls := log.in(r.dirs.GitDir)
		if len(calls) == 0 || calls[0] != "config" {
			t.Errorf("%s: calls %v; want the configuration read first", r.Rel, calls)
		}
		read := -1
		for i, c := range log.calls {
			if c.gitDir == r.dirs.GitDir && c.args[0] == "config" {
				read = i
				break
			}
		}
		if read < 0 || read > firstOther {
			t.Errorf("%s: configuration read at call %d, after the first other call at %d", r.Rel, read, firstOther)
		}
		for _, c := range calls {
			if c == "diff-index" {
				sawDiffIndex = true
			}
		}
	}
	if !sawDiffIndex {
		t.Errorf("no diff-index ran for the pruned worktree: %+v", log.calls)
	}
}

// TestRecheckBeforeTheRulesStatusKeepsAKeyPlantedAfterTheFirstRead plants a
// filter after the pass's first configuration read and before the rule's
// status, and checks the status never starts.
func TestRecheckBeforeTheRulesStatusKeepsAKeyPlantedAfterTheFirstRead(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	selectFilter(t, f)
	p := f.policy(t)
	gitDir := filepath.Join(f.clone, ".git")
	var log callLog
	var once sync.Once
	log.before = func(c gitCall, _ int) {
		if c.gitDir == gitDir && c.args[0] != "config" {
			once.Do(func() { gitRun(t, f.clone, "config", "filter.x.clean", script) })
		}
	}
	log.attach(p.Env)

	v := Evaluate(context.Background(), f.unit(), p)
	keepWith(t, v, "tools/app: changed during the pass: repository configuration names a program: filter.x.clean")
	for _, c := range log.in(gitDir) {
		if c == "status" {
			t.Fatal("the status started after the re-check found the key")
		}
	}
	assertNoMarker(t, marker)
}

// TestRecheckBeforeTheSnapshotsStatusKeepsAKeyPlantedAfterItsOwnRead plants
// a filter after the snapshot re-read's own configuration read and before
// its status.
func TestRecheckBeforeTheSnapshotsStatusKeepsAKeyPlantedAfterItsOwnRead(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	selectFilter(t, f)
	p := f.policy(t)
	v := reapVerdict(t, f, p)

	gitDir := filepath.Join(f.clone, ".git")
	var log callLog
	reads := 0
	log.before = func(c gitCall, _ int) {
		if c.gitDir == gitDir && c.args[0] == "config" {
			reads++
			if reads == 2 {
				gitRun(t, f.clone, "config", "filter.x.clean", script)
			}
		}
	}
	log.attach(p.Env)
	d := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil).RuleDifferences(v)
	if !containsPart(d, "repository configuration names a program: filter.x.clean in tools/app") {
		t.Fatalf("differences %v; want the planted key named", d)
	}
	for _, c := range log.in(gitDir) {
		if c == "status" {
			t.Fatal("the snapshot's status started after the re-check found the key")
		}
	}
	assertNoMarker(t, marker)
}

// TestReadAfterTheStatusKeepsAKeyPlantedDuringIt plants a key after the
// rule's status and before the configuration read right after it, and checks
// the unit keeps with the key named and nothing past that read runs.
func TestReadAfterTheStatusKeepsAKeyPlantedDuringIt(t *testing.T) {
	f := checkedOut(t)
	script, marker := markerScript(t)
	p := f.policy(t)
	gitDir := filepath.Join(f.clone, ".git")
	var log callLog
	sawStatus := false
	var once sync.Once
	log.before = func(c gitCall, _ int) {
		if c.gitDir != gitDir {
			return
		}
		if c.args[0] == "status" {
			sawStatus = true
		} else if sawStatus && c.args[0] == "config" {
			once.Do(func() { gitRun(t, f.clone, "config", "credential.helper", script) })
		}
	}
	log.attach(p.Env)

	v := Evaluate(context.Background(), f.unit(), p)
	keepWith(t, v, "tools/app: changed during the pass: repository configuration names a program: credential.helper")
	calls := strings.Join(log.in(gitDir), ",")
	if !strings.HasSuffix(calls, ",status,config") {
		t.Fatalf("git calls in the repository %v; want the status followed by the configuration read alone", calls)
	}
	assertNoMarker(t, marker)
}

func TestRecheckConfig(t *testing.T) {
	f := checkedOut(t)
	p := f.policy(t)
	gitDir := filepath.Join(f.clone, ".git")
	if key, reason := p.Env.RecheckConfig(context.Background(), gitDir, f.clone); key != "" || reason != "" {
		t.Fatalf("RecheckConfig on a clean clone = %q, %q; want nothing", key, reason)
	}
	gitRun(t, f.clone, "config", "Filter.x.Clean", "cat")
	key, reason := p.Env.RecheckConfig(context.Background(), gitDir, f.clone)
	if key != "filter.x.clean" || reason != "changed during the pass: repository configuration names a program: filter.x.clean" {
		t.Fatalf("RecheckConfig = %q, %q; want the key and its keep reason", key, reason)
	}
	key, reason = p.Env.RecheckConfig(context.Background(), filepath.Join(f.instance, "missing", ".git"), "")
	if key != "" || reason == "" {
		t.Fatalf("RecheckConfig on a missing repository = %q, %q; want a failed read that keeps", key, reason)
	}
	if key, reason := (*Env)(nil).RecheckConfig(context.Background(), gitDir, f.clone); key != "" || reason == "" {
		t.Fatalf("RecheckConfig with no Env = %q, %q; want a reason that keeps", key, reason)
	}
}

func TestParseGitVersion(t *testing.T) {
	cases := map[string]gitVersion{
		"git version 2.39.5 (Apple Git-154)\n": {2, 39},
		"git version 2.45.0":                   {2, 45},
		"git version 3.0":                      {3, 0},
		"git version 2.45.1.windows.1":         {2, 45},
		"git version banana":                   {},
		"git version 2":                        {},
		"git version 2.x.1":                    {},
		"version 2.45.0":                       {},
		"":                                     {},
		"git version -2.50":                    {},
	}
	for in, want := range cases {
		if got := parseGitVersion(in); got != want {
			t.Errorf("parseGitVersion(%q) = %+v, want %+v", in, got, want)
		}
	}
	if (gitVersion{}).atLeast(2, 45) || !(gitVersion{2, 45}).atLeast(2, 45) || (gitVersion{2, 44}).atLeast(2, 45) || !(gitVersion{3, 0}).atLeast(2, 45) {
		t.Fatal("atLeast compares wrongly")
	}
}

func TestNewEnvReadsGitsVersion(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	if p.Env.gitVersion.major < 2 {
		t.Fatalf("git version %+v; want the host git's", p.Env.gitVersion)
	}
}

// TestPartialCloneKeepsOnAnOlderGit fakes git's version and checks a
// partial clone keeps right after the configuration read, with no later
// call in it, while a git that honors GIT_NO_LAZY_FETCH judges it on.
func TestPartialCloneKeepsOnAnOlderGit(t *testing.T) {
	for _, key := range []struct{ set, value, want string }{
		{"remote.origin.promisor", "true", "remote.origin.promisor"},
		{"extensions.partialClone", "origin", "extensions.partialclone"},
	} {
		t.Run(key.set, func(t *testing.T) {
			f := checkedOut(t)
			gitRun(t, f.clone, "config", key.set, key.value)
			gitDir := filepath.Join(f.clone, ".git")

			p := f.policy(t)
			p.Env.gitVersion = gitVersion{2, 44}
			var log callLog
			log.attach(p.Env)
			v := Evaluate(context.Background(), f.unit(), p)
			keepWith(t, v, "tools/app: partial clone needs git 2.45: "+key.want)
			if got := log.in(gitDir); strings.Join(got, ",") != "config" {
				t.Errorf("git calls in the repository %v; want the configuration read alone", got)
			}

			p = f.policy(t)
			p.Env.gitVersion = gitVersion{2, 45}
			v = Evaluate(context.Background(), f.unit(), p)
			noKeepWith(t, v, "partial clone")
		})
	}
}

// fakeGit writes a git on its own PATH directory that runs body first and
// then hands every call it doesn't answer to the real git, logging each
// call's arguments to log.
func fakeGit(t *testing.T, body string) (dir, log string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	dir = outsideDir(t)
	log = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" + body + "\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// TestAVersionThatDoesNotParseKeepsAPartialClone runs the pass with a git
// whose version line doesn't parse: it counts as older than 2.45, so a
// partial clone keeps.
func TestAVersionThatDoesNotParseKeepsAPartialClone(t *testing.T) {
	f := checkedOut(t)
	gitRun(t, f.clone, "config", "remote.origin.promisor", "true")
	dir, _ := fakeGit(t, `for a; do if [ "$a" = version ]; then echo "git version banana"; exit 0; fi; done`)
	e := NewEnv(f.root, f.home, dir, WithFileRemotes())
	t.Cleanup(e.Close)
	if e.gitVersion != (gitVersion{}) {
		t.Fatalf("git version %+v; want the zero version for a line that doesn't parse", e.gitVersion)
	}
	p := f.policy(t)
	p.Env = e
	v := Evaluate(context.Background(), f.unit(), p)
	keepWith(t, v, "tools/app: partial clone needs git 2.45: remote.origin.promisor")
}

// TestOwnershipRefusalKeepsWithGitsRefusalLine has git refuse the
// configuration read for the repository's ownership, as it does for a
// repository another uid owns, and checks the keep carries git's refusal
// line, never its safe.directory advice, and that niwa adds no
// safe.directory entry.
func TestOwnershipRefusalKeepsWithGitsRefusalLine(t *testing.T) {
	f := checkedOut(t)
	dir, log := fakeGit(t, `for a; do if [ "$a" = --list ]; then
printf 'fatal: detected dubious ownership in repository at %s\n' "$PWD" >&2
printf 'To add an exception for this directory, call:\n\n\tgit config --global --add safe.directory %s\n' "$PWD" >&2
exit 128; fi; done`)
	e := NewEnv(f.root, f.home, dir, WithFileRemotes())
	t.Cleanup(e.Close)
	p := f.policy(t)
	p.Env = e
	v := Evaluate(context.Background(), f.unit(), p)
	c := keepWith(t, v, "tools/app: reading the repository configuration: fatal: detected dubious ownership in repository at")
	if strings.Contains(c.Evidence, "safe.directory") {
		t.Errorf("evidence %q carries git's advice line", c.Evidence)
	}
	noKeepWith(t, v, "safe.directory")
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "safe.directory") {
		t.Fatalf("niwa named safe.directory in a git call:\n%s", calls)
	}
}

func TestFailureLine(t *testing.T) {
	cases := map[string]string{
		"fatal: detected dubious ownership in repository at '/r'\nTo add an exception for this directory, call:\n\n\tgit config --global --add safe.directory /r\n": "fatal: detected dubious ownership in repository at '/r'",
		"error: one\nfatal: two\nhint: three\n": "fatal: two",
		"warning: x\nsomething broke\n":         "something broke",
		"":                                      "",
	}
	for in, want := range cases {
		if got := failureLine(in); got != want {
			t.Errorf("failureLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A failed ls-remote follows its fatal line with git's access-rights advice.
// The kept line is the fatal one, not the advice that ends standard error.
func TestFailureLineKeepsTheFatalLineOfAnAccessRightsFailure(t *testing.T) {
	in := "fatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n"
	want := "fatal: Could not read from remote repository."
	if got := failureLine(in); got != want {
		t.Errorf("failureLine(%q) = %q, want %q", in, got, want)
	}
}
