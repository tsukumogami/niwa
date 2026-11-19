package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// rawLogLines returns the lines of the log file at path, as written.
func rawLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		return nil
	}
	if data[len(data)-1] != '\n' {
		t.Fatalf("%s doesn't end with a newline", path)
	}
	return strings.Split(string(data[:len(data)-1]), "\n")
}

// decodeLine decodes one log line as one JSON object.
func decodeLine(t *testing.T, raw string) map[string]any {
	t.Helper()
	var line map[string]any
	if err := json.Unmarshal([]byte(raw), &line); err != nil {
		t.Fatalf("log line %q is not one JSON object: %v", raw, err)
	}
	return line
}

// requireCut fails unless every string in v, at any depth, is printable
// ASCII of at most nameMax bytes. It returns the strings it saw.
func requireCut(t *testing.T, v any) []string {
	t.Helper()
	var seen []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			for i := 0; i < len(x); i++ {
				if x[i] < 0x20 || x[i] >= 0x7f {
					t.Fatalf("log string %q holds byte %#x", x, x[i])
				}
			}
			if len(x) > nameMax {
				t.Fatalf("log string is %d bytes, want at most %d", len(x), nameMax)
			}
			seen = append(seen, x)
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(v)
	return seen
}

func TestLog_CutsAHostileCommand(t *testing.T) {
	f := built(t)
	before := len(rawLogLines(t, logPath(f.mdir())))
	opts := f.applyOpts()
	opts.Command = "apply\x1b[2J\n{\"forged\":1}" + strings.Repeat("a", 300)
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want healthy with the log written", res)
	}
	lines := rawLogLines(t, logPath(f.mdir()))[before:]
	if len(lines) != 1 {
		t.Fatalf("the call added %d lines, want 1: %q", len(lines), lines)
	}
	line := decodeLine(t, lines[0])
	requireCut(t, line)
	command, _ := line["command"].(string)
	if !strings.HasPrefix(command, `apply\x1b[2J\x0a{"forged":1}aaa`) {
		t.Fatalf("command = %q, want the escapes in place of ESC and newline", command)
	}
	if _, ok := line["forged"]; ok {
		t.Fatal("the command forged a key into the line")
	}
}

func TestLog_CutsAHostileLostRef(t *testing.T) {
	f := built(t)
	before := len(rawLogLines(t, logPath(f.mdir())))
	hostile := "\x1b\n\"\\‮\xff" + strings.Repeat("b", 300)
	lost := LostRef{
		Instance: hostile, Repo: "app", Ref: hostile, MissingCommit: hostile,
		OwnCommits: []string{hostile}, LostFiles: []LostFile{{Path: hostile}},
	}
	restoreLine := logLine{Time: logTime(time.Now()), Command: "apply", Mirror: "app", Event: eventRestore,
		Fault: FaultMissing, Result: OutcomeRestored}
	if err := appendLog(f.mdir(), unrecoverableLines(restoreLine, []LostRef{lost})...); err != nil {
		t.Fatal(err)
	}
	lines := rawLogLines(t, logPath(f.mdir()))[before:]
	if len(lines) != 1 {
		t.Fatalf("appendLog added %d lines, want 1: %q", len(lines), lines)
	}
	line := decodeLine(t, lines[0])
	requireCut(t, line)
	want := `\x1b\x0a"\x5c\xe2\x80\xae\xff`
	for _, key := range []string{"instance", "ref", "missing_commit"} {
		if s, _ := line[key].(string); !strings.HasPrefix(s, want) {
			t.Fatalf("%s = %q, want it to start %q", key, s, want)
		}
	}
	own, _ := line["own_commits"].([]any)
	files, _ := line["lost_files"].([]any)
	if len(own) != 1 || len(files) != 1 {
		t.Fatalf("own_commits %v, lost_files %v, want one each", own, files)
	}
	if s, _ := own[0].(string); !strings.HasPrefix(s, want) {
		t.Fatalf("own_commits[0] = %q, want it to start %q", s, want)
	}
	file, _ := files[0].(map[string]any)
	if s, _ := file["path"].(string); !strings.HasPrefix(s, want) {
		t.Fatalf("lost_files[0].path = %q, want it to start %q", s, want)
	}
	if lost.OwnCommits[0] != hostile || lost.LostFiles[0].Path != hostile {
		t.Fatal("appendLog changed the caller's LostRef")
	}
}

func TestLog_RefusesAnyOtherLineType(t *testing.T) {
	f := built(t)
	path := logPath(f.mdir())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	good := logLine{Time: logTime(time.Now()), Command: "apply", Mirror: "app", Event: eventCheck, Result: OutcomeHealthy}
	for _, bad := range []any{map[string]string{"event": "check"}, "a line", &good, LostRef{Repo: "app"}} {
		if err := appendLog(f.mdir(), good, bad); err == nil {
			t.Fatalf("appendLog accepted a %T", bad)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("a refused %T changed the log", bad)
		}
	}
}

// TestLog_AReportedLostRefIsLoggedUnchanged: a name report already cut to
// printable ASCII with no backslash, here one capped at nameMax, passes the
// log's cut unchanged. (A name report escaped holds a backslash, which the
// log's cut escapes again; only names without one are promised unchanged.)
func TestLog_AReportedLostRefIsLoggedUnchanged(t *testing.T) {
	f := built(t)
	id := strings.Repeat("c", 40)
	l := lostRef{
		clone:   "/ws/a/tools/app",
		ref:     borrowerRef{name: "refs/heads/keep-" + strings.Repeat("k", 300), id: id},
		missing: id,
		own:     []string{strings.Repeat("d", 40)},
	}
	lost := l.report(context.Background(), f.store(t), "app")
	if len(lost.Ref) != nameMax {
		t.Fatalf("report left the ref %d bytes, want it capped at %d", len(lost.Ref), nameMax)
	}
	before := len(rawLogLines(t, logPath(f.mdir())))
	restoreLine := logLine{Time: logTime(time.Now()), Command: "apply", Mirror: "app", Event: eventRestore,
		Fault: FaultMissing, Result: OutcomeRestored}
	if err := appendLog(f.mdir(), unrecoverableLines(restoreLine, []LostRef{lost})...); err != nil {
		t.Fatal(err)
	}
	lines := rawLogLines(t, logPath(f.mdir()))[before:]
	if len(lines) != 1 {
		t.Fatalf("appendLog added %d lines, want 1", len(lines))
	}
	line := decodeLine(t, lines[0])
	own, _ := line["own_commits"].([]any)
	if line["instance"] != lost.Instance || line["ref"] != lost.Ref || line["missing_commit"] != lost.MissingCommit ||
		line["remote_tracking"] != lost.RemoteTracking || len(own) != 1 || own[0] != lost.OwnCommits[0] {
		t.Fatalf("logged %v, want the LostRef's fields %+v unchanged", line, lost)
	}
}

// fillLog writes valid JSON lines to path until it's exactly size bytes.
func fillLog(t *testing.T, path string, size int) []byte {
	t.Helper()
	const filler = `{"time":"2026-01-01T00:00:00Z","command":"fill","mirror":"app","event":"check","fault":"","result":"healthy","check_ms":0}` + "\n"
	const padFrame = len(`{"pad":""}` + "\n")
	var b bytes.Buffer
	for size-b.Len() > len(filler)+padFrame {
		b.WriteString(filler)
	}
	b.WriteString(`{"pad":"` + strings.Repeat("x", size-b.Len()-padFrame) + `"}` + "\n")
	if b.Len() != size {
		t.Fatalf("filled %d bytes, want %d", b.Len(), size)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// metaNames lists .meta/, sorted.
func metaNames(t *testing.T, f fixture) []string {
	t.Helper()
	entries, err := os.ReadDir(metaDir(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func requireMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("%s = %v (%v), want a 0600 file", path, info, err)
	}
}

func TestLog_RotatesAtOneMiB(t *testing.T) {
	f := built(t)
	path := logPath(f.mdir())
	healthy := func() {
		t.Helper()
		res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].LogErr != nil {
			t.Fatalf("result = %+v, want healthy with the log written", res)
		}
	}

	// One byte under the mark: the line goes on the end, nothing rotates.
	under := fillLog(t, path, logMax-1)
	healthy()
	if _, err := os.Lstat(path + ".1"); !os.IsNotExist(err) {
		t.Fatalf("a log one byte under the mark rotated (%v)", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, under) || len(rawLogLines(t, path)) != bytes.Count(under, []byte("\n"))+1 {
		t.Fatal("the line wasn't appended to the log under the mark")
	}

	// At the mark: the full file becomes mirror.log.1, the line starts a new
	// mirror.log, and a second fill replaces that generation.
	for gen := 1; gen <= 2; gen++ {
		full := fillLog(t, path, logMax)
		healthy()
		old, err := os.ReadFile(path + ".1")
		if err != nil {
			t.Fatalf("generation %d: %v", gen, err)
		}
		if !bytes.Equal(old, full) {
			t.Fatalf("generation %d: mirror.log.1 doesn't hold the full file's bytes", gen)
		}
		lines := rawLogLines(t, path)
		if len(lines) != 1 {
			t.Fatalf("generation %d: the new mirror.log holds %d lines, want the one check line", gen, len(lines))
		}
		if l := decodeLine(t, lines[0]); l["event"] != "check" || l["result"] != "healthy" {
			t.Fatalf("generation %d: the new log's line = %v", gen, l)
		}
		requireMode0600(t, path)
		requireMode0600(t, path+".1")
		if got := metaNames(t, f); !slices.Equal(got, []string{"app.json", "mirror.log", "mirror.log.1"}) {
			t.Fatalf("generation %d: .meta holds %v", gen, got)
		}
	}
}

func TestLog_StaysTwoFilesUnderSustainedWrites(t *testing.T) {
	f := built(t)
	line := logLine{Time: logTime(time.Now()), Command: "from-hook", Mirror: "app", Event: eventCheck, Result: OutcomeHealthy}
	encoded, err := encodeLogLine(line)
	if err != nil {
		t.Fatal(err)
	}
	for written := 0; written < 3*logMax; written += len(encoded) {
		if err := appendLog(f.mdir(), line); err != nil {
			t.Fatal(err)
		}
	}
	if got := metaNames(t, f); !slices.Equal(got, []string{"app.json", "mirror.log", "mirror.log.1"}) {
		t.Fatalf(".meta holds %v, want app.json, mirror.log and mirror.log.1", got)
	}
	for _, name := range []string{"mirror.log", "mirror.log.1"} {
		info, err := os.Stat(filepath.Join(metaDir(f.mdir()), name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() >= logMax+int64(len(encoded)) {
			t.Fatalf("%s is %d bytes, want under %d", name, info.Size(), logMax+len(encoded))
		}
	}
}

func TestLog_ConcurrentAppendsStayWhole(t *testing.T) {
	f := built(t)
	path := logPath(f.mdir())
	fillLog(t, path, logMax-1024)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < 2000; n++ {
				line := logLine{Time: logTime(time.Now()), Command: fmt.Sprintf("g%d-%d", g, n), Mirror: "app",
					Event: eventCheck, Result: OutcomeHealthy}
				if err := appendLog(f.mdir(), line); err != nil {
					errs <- err
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path + ".1"); err != nil {
		t.Fatalf("the log never rotated: %v", err)
	}
	for _, p := range []string{path + ".1", path} {
		for _, raw := range rawLogLines(t, p) {
			decodeLine(t, raw)
		}
	}
	if got := metaNames(t, f); !slices.Equal(got, []string{"app.json", "mirror.log", "mirror.log.1"}) {
		t.Fatalf(".meta holds %v", got)
	}
}

func TestLog_ARestoreStraddlesTheRotation(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, false)
	f.deleteMirror(t)
	path := logPath(f.mdir())
	filled := fillLog(t, path, logMax-100)

	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || len(res.Unrecoverable) != 3 || res.LogErr != nil {
		t.Fatalf("result = %+v, want restored with three lost refs and the log written", res)
	}
	old, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(old, filled) {
		t.Fatal("mirror.log.1 doesn't start with the full log's bytes")
	}
	tail := rawLogLines(t, path+".1")
	var events []string
	events = append(events, decodeLine(t, tail[len(tail)-1])["event"].(string))
	if len(tail) != bytes.Count(filled, []byte("\n"))+1 {
		t.Fatalf("mirror.log.1 gained %d lines, want only the check line", len(tail)-bytes.Count(filled, []byte("\n")))
	}
	for _, raw := range rawLogLines(t, path) {
		events = append(events, decodeLine(t, raw)["event"].(string))
	}
	want := []string{"check", "restore", "unrecoverable", "unrecoverable", "unrecoverable"}
	if !slices.Equal(events, want) {
		t.Fatalf("mirror.log.1 then mirror.log read %v, want %v", events, want)
	}
}

func TestResultNotice_ALogThatCantBeWritten(t *testing.T) {
	stable := "/h/.niwa/w-0123/mirrors/app"
	log := "/h/.niwa/w-0123/mirrors/.meta/mirror.log"
	logErr := errors.New("\x1b[31mopen /x: forged\nline")
	failed := " The mirror log at " + log + " could not be written."
	lost := []LostRef{{Repo: "app"}, {Repo: "app"}, {Repo: "app"}}

	cases := []Result{
		{RepoID: "app", StablePath: stable, Outcome: OutcomeHealthy},
		{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored},
		{RepoID: "app", StablePath: stable, Fault: FaultPackChanged, Outcome: OutcomeRestoreFailed},
		{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeBusy},
	}
	for _, c := range cases {
		working := c.Notice()
		c.LogErr = logErr
		got := c.Notice()
		var want string
		if c.Outcome == OutcomeHealthy {
			if working != "" {
				t.Fatalf("a healthy notice with a working log = %q, want empty", working)
			}
			want = "niwa mirror app: result healthy." + failed
		} else {
			stem, ok := strings.CutSuffix(working, " Mirror log: "+log)
			if !ok {
				t.Fatalf("the %s notice %q doesn't end naming the log", c.Outcome, working)
			}
			want = stem + failed
		}
		if got != want {
			t.Errorf("Notice() for %s with LogErr =\n%q\nwant\n%q", c.Outcome, got, want)
		}
		for _, bad := range []string{"forged", "open /x", "\x1b", "\n"} {
			if strings.Contains(got, bad) {
				t.Errorf("Notice() for %s carries %q: %q", c.Outcome, bad, got)
			}
		}
	}

	// With refs lost and the log not written, the notice counts them and
	// doesn't say the log names them.
	withLost := Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored, Unrecoverable: lost, LogErr: logErr}
	got := withLost.Notice()
	want := "niwa mirror app: fault missing, result restored. It was rebuilt from the remote, and the clones that borrow from it read normally. Except 3 refs in those clones, which are unrecoverable: they were built on commits the remote no longer serves." + failed
	if got != want {
		t.Errorf("Notice() for three lost refs with LogErr =\n%q\nwant\n%q", got, want)
	}
	for _, bad := range []string{"forged", "open /x", "\x1b", "\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("Notice() for three lost refs carries %q: %q", bad, got)
		}
	}
}

// fakeClock replaces elapsedNow with a clock that moves step forward on each
// read, until the test ends, and returns a func that moves it by d.
func fakeClock(t *testing.T, step time.Duration) (advance func(time.Duration)) {
	t.Helper()
	var mu sync.Mutex
	now := time.Unix(1_700_000_000, 0)
	elapsedNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(step)
		return now
	}
	t.Cleanup(func() { elapsedNow = time.Now })
	return func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
}

// setProbeHook runs hook as each probe of the borrowers starts, until the
// test ends.
func setProbeHook(t *testing.T, hook func(store string)) {
	t.Helper()
	probeHook = hook
	t.Cleanup(func() { probeHook = nil })
}

// requireFigures fails unless line has the event, check_ms and took_ms given
// (took < 0: none) and a time within a minute of the real clock.
func requireFigures(t *testing.T, line map[string]any, event string, checkMs, tookMs float64) {
	t.Helper()
	if line["event"] != event || line["check_ms"] != checkMs {
		t.Fatalf("line %v, want a %s line with check_ms %v", line, event, checkMs)
	}
	took, ok := line["took_ms"]
	if tookMs < 0 && ok || tookMs >= 0 && took != tookMs {
		t.Fatalf("line %v, want took_ms %v", line, tookMs)
	}
	stamp, _ := line["time"].(string)
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil || at.Sub(time.Now()).Abs() > time.Minute {
		t.Fatalf("line time %q isn't the real clock's (%v)", stamp, err)
	}
}

func TestLog_CheckMsIsTheCheckDuration(t *testing.T) {
	f := built(t)
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	lines := f.logLines(t)
	if got := lines[len(lines)-1]["check_ms"]; got != float64(res[0].CheckDuration.Milliseconds()) {
		t.Fatalf("check_ms = %v, want the Result's %d", got, res[0].CheckDuration.Milliseconds())
	}
	res = EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	lines = f.logLines(t)
	check, refreshed := lines[len(lines)-2], lines[len(lines)-1]
	if refreshed["event"] != "refresh" || refreshed["check_ms"] != check["check_ms"] ||
		check["check_ms"] != float64(res[0].CheckDuration.Milliseconds()) {
		t.Fatalf("check %v and refresh %v, want both carrying the Result's check_ms %d", check, refreshed, res[0].CheckDuration.Milliseconds())
	}
}

func TestLog_FakeClockFiguresOfAHealthyCheckAndARefresh(t *testing.T) {
	f := built(t)
	fakeClock(t, 42*time.Millisecond)

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].CheckDuration != 42*time.Millisecond || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want healthy with a 42ms check", res)
	}
	lines := newLines(t, f, logged)
	if len(lines) != 1 {
		t.Fatalf("log gained %v, want one check line", lines)
	}
	requireFigures(t, lines[0], "check", 42, -1)

	logged = len(f.logLines(t))
	res = EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].Err != nil || res[0].LogErr != nil {
		t.Fatalf("refresh = %+v, want healthy", res)
	}
	lines = newLines(t, f, logged)
	if len(lines) != 2 {
		t.Fatalf("log gained %v, want a check and a refresh line", lines)
	}
	requireFigures(t, lines[0], "check", 42, -1)
	requireFigures(t, lines[1], "refresh", 42, 42)
}

func TestLog_FakeClockPutsTheProbeInTookMs(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	advance := fakeClock(t, 42*time.Millisecond)
	probed := 0
	setProbeHook(t, func(string) { probed++; advance(300 * time.Millisecond) })

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].CheckDuration != 42*time.Millisecond || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want restored with a 42ms check", res)
	}
	if probed != 1 {
		t.Fatalf("the borrowers were probed %d times, want once", probed)
	}
	lines := f.logLines(t)
	if len(lines) != 2 {
		t.Fatalf("log = %v, want a check and a restore line", lines)
	}
	requireFigures(t, lines[0], "check", 42, -1)
	requireFigures(t, lines[1], "restore", 42, 342)
}

func TestLog_FakeClockFiguresOfTheDeepVerifyRebuild(t *testing.T) {
	f := built(t)
	rotPack(t, f)
	editState(t, f, func(st *state) { st.LastDeepVerify = stateTime(time.Now().Add(-25 * time.Hour)) })
	advance := fakeClock(t, 42*time.Millisecond)
	setProbeHook(t, func(string) { advance(300 * time.Millisecond) })

	logged := len(f.logLines(t))
	res := EnsureHealthy(context.Background(), f.refreshOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultPackChanged || res[0].Outcome != OutcomeRestored || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want pack-changed/restored", res)
	}
	lines := newLines(t, f, logged)
	if len(lines) != 2 {
		t.Fatalf("log gained %v, want a check and a restore line", lines)
	}
	requireFigures(t, lines[0], "check", 42, -1)
	requireFigures(t, lines[1], "restore", 42, 342)
}
