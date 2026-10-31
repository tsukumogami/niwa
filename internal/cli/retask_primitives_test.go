package cli

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/watch"
)

func TestClaudeSessionRow_DecodesCwd(t *testing.T) {
	const sid = "33333333-3333-4333-8333-333333333333"
	var rows []claudeSessionRow
	in := `[{"id":"abcd1234","sessionId":"` + sid + `","cwd":"/ws/inst","pid":42,"status":"idle"}]`
	if err := json.Unmarshal([]byte(in), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Cwd != "/ws/inst" {
		t.Fatalf("rows = %+v, want one row with Cwd /ws/inst", rows)
	}
	if !rows[0].running() {
		t.Error("a row with a pid and status is not running")
	}

	rows = nil
	if err := json.Unmarshal([]byte(`[{"id":"abcd1234","sessionId":"`+sid+`"}]`), &rows); err != nil {
		t.Fatal(err)
	}
	if rows[0].Cwd != "" {
		t.Errorf("absent cwd decoded as %q", rows[0].Cwd)
	}
	if rows[0].running() {
		t.Error("a row with no pid or status is running")
	}
}

func TestRootedAtInstance(t *testing.T) {
	parent := t.TempDir()
	inst := filepath.Join(parent, "inst")
	for _, d := range []string{filepath.Join(inst, "public", "niwa"), filepath.Join(parent, "inst-2"), filepath.Join(parent, "instance")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A symlink elsewhere that points into the instance.
	into := filepath.Join(t.TempDir(), "into")
	if err := os.Symlink(filepath.Join(inst, "public"), into); err != nil {
		t.Fatal(err)
	}
	// A symlinked root that reaches the instance's parent.
	rootLink := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(parent, rootLink); err != nil {
		t.Fatal(err)
	}
	realInst, err := filepath.EvalSymlinks(inst)
	if err != nil {
		t.Fatal(err)
	}

	const (
		mapped = "44444444-4444-4444-8444-444444444444"
		other  = "55555555-5555-4555-8555-555555555555"
	)
	row := func(id, sid, cwd string) claudeSessionRow {
		return claudeSessionRow{ID: id, SessionID: sid, Cwd: cwd}
	}
	ids := func(rows []claudeSessionRow) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}

	cases := []struct {
		name     string
		row      claudeSessionRow
		instance string
		mapped   string
		want     bool
	}{
		{"cwd equal to the instance", row("a", other, inst), inst, mapped, true},
		{"cwd inside a repo", row("a", other, filepath.Join(inst, "public", "niwa")), inst, mapped, true},
		{"sibling inst-2", row("a", other, filepath.Join(parent, "inst-2")), inst, mapped, false},
		{"sibling instance", row("a", other, filepath.Join(parent, "instance")), inst, mapped, false},
		{"parent", row("a", other, parent), inst, mapped, false},
		{"empty cwd", row("a", other, ""), inst, mapped, false},
		{"trailing slash", row("a", other, inst+"/"), inst, mapped, true},
		{"dot-dot step", row("a", other, inst+"/x/.."), inst, mapped, true},
		{"cwd is a symlink into the instance", row("a", other, into), inst, mapped, true},
		{"instance through a symlinked root, cwd real", row("a", other, realInst), filepath.Join(rootLink, "inst"), mapped, true},
		{"the mapped session", row("a", mapped, inst), inst, mapped, false},
		{"shares the mapped short id, other session", row("mapped-short", other, inst), inst, mapped, true},
		{"empty mapped id excludes nothing", row("a", "", inst), inst, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := []claudeSessionRow{tc.row}
			if tc.name == "shares the mapped short id, other session" {
				rows = append(rows, row("mapped-short", mapped, inst))
			}
			got := sessionsRootedAtInstance(rows, tc.instance, tc.mapped)
			included := len(got) == 1 && got[0].SessionID == tc.row.SessionID
			if included != tc.want || len(got) > 1 {
				t.Fatalf("rooted rows = %v (sessions %+v), want included=%v", ids(got), got, tc.want)
			}
		})
	}
}

// writeJobFixture writes state.json for a job directory under jobsDir.
func writeJobFixture(t *testing.T, jobsDir, dirName, body string) string {
	t.Helper()
	dir := filepath.Join(jobsDir, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestJobState_KindsShapeNeverBreaksLiveness(t *testing.T) {
	jobsDir := t.TempDir()
	const sid = "66666666-6666-4666-8666-666666666666"
	cwd := filepath.Join(t.TempDir(), "inst")
	writeJobFixture(t, jobsDir, "66666666", `{"sessionId":"`+sid+`","cwd":"`+cwd+`","state":"working","tempo":"idle","inFlight":{"tasks":1,"kinds":{"x":1}}}`)

	if !sessionLive(jobsDir, sid, time.Now()) {
		t.Error("an object-shaped kinds made sessionLive false")
	}
	if !instanceHasLiveJob(jobsDir, cwd) {
		t.Error("an object-shaped kinds made instanceHasLiveJob false")
	}
	js, ok := readJobState(jobsDir, sid)
	if !ok {
		t.Fatal("readJobState failed on an object-shaped kinds")
	}
	if len(js.InFlight.Kinds) != 0 || js.InFlight.Tasks != 1 {
		t.Errorf("InFlight = %+v, want 1 task and no kinds", js.InFlight)
	}

	writeJobFixture(t, jobsDir, "66666666", `{"sessionId":"`+sid+`","inFlight":{"tasks":2,"kinds":["session_cron","local_bash"]}}`)
	js, ok = readJobState(jobsDir, sid)
	if !ok || strings.Join(js.InFlight.Kinds, ",") != "session_cron,local_bash" {
		t.Errorf("kinds = %v (ok %v), want [session_cron local_bash]", js.InFlight.Kinds, ok)
	}
}

func withJobStateOwner(t *testing.T, uid func() int) {
	t.Helper()
	orig := jobStateOwnerUID
	jobStateOwnerUID = uid
	t.Cleanup(func() { jobStateOwnerUID = orig })
}

func TestReadJobStateChecked(t *testing.T) {
	const sid = "77777777-7777-4777-8777-777777777777"
	valid := `{"sessionId":"` + sid + `","state":"done","tempo":"idle","cwd":"/ws/inst"}`

	notGone := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("checked read accepted an unsafe or mismatched state")
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("a present but unusable entry reads as gone: %v", err)
		}
	}

	t.Run("regular file decodes", func(t *testing.T) {
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, "77777777", valid)
		js, err := readJobStateChecked(jobsDir, sid)
		if err != nil {
			t.Fatal(err)
		}
		if js.State != "done" || js.Cwd != "/ws/inst" {
			t.Errorf("decoded %+v", js)
		}
	})

	t.Run("exact id directory", func(t *testing.T) {
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, sid, valid)
		if _, err := readJobStateChecked(jobsDir, sid); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		jobsDir := t.TempDir()
		target := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(target, []byte(valid), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(jobsDir, "77777777")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "state.json")); err != nil {
			t.Fatal(err)
		}
		_, err := readJobStateChecked(jobsDir, sid)
		notGone(t, err)
	})

	t.Run("fifo", func(t *testing.T) {
		jobsDir := t.TempDir()
		dir := filepath.Join(jobsDir, "77777777")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Mkfifo(filepath.Join(dir, "state.json"), 0o600); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := readJobStateChecked(jobsDir, sid)
			done <- err
		}()
		select {
		case err := <-done:
			notGone(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("checked read hung on a FIFO")
		}
	})

	t.Run("directory", func(t *testing.T) {
		jobsDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(jobsDir, "77777777", "state.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := readJobStateChecked(jobsDir, sid)
		notGone(t, err)
	})

	t.Run("other owner", func(t *testing.T) {
		withJobStateOwner(t, func() int { return os.Getuid() + 1 })
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, "77777777", valid)
		_, err := readJobStateChecked(jobsDir, sid)
		notGone(t, err)
	})

	t.Run("another session", func(t *testing.T) {
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, "77777777", `{"sessionId":"77777777-0000-4000-8000-000000000000","state":"done"}`)
		_, err := readJobStateChecked(jobsDir, sid)
		notGone(t, err)
	})

	t.Run("empty session id", func(t *testing.T) {
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, "77777777", `{"sessionId":"","state":"done"}`)
		_, err := readJobStateChecked(jobsDir, sid)
		notGone(t, err)
	})

	t.Run("missing entry", func(t *testing.T) {
		jobsDir := t.TempDir()
		_, err := readJobStateChecked(jobsDir, sid)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing entry error = %v, want fs.ErrNotExist", err)
		}
		// A job directory without a state file is gone too.
		if err := os.Mkdir(filepath.Join(jobsDir, "77777777"), 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := readJobStateChecked(jobsDir, sid); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("entry without state.json error = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestReadSavedOptions(t *testing.T) {
	const sid = "88888888-8888-4888-8888-888888888888"
	// The shape of a dispatched worker's state.json (Claude Code 2.1.314),
	// values scrubbed: the saved option argv is respawnFlags, a flat array of
	// strings.
	state := func(flags string) string {
		doc := `{"state":"done","detail":"","tempo":"idle","inFlight":{"tasks":0,"queued":0,"kinds":[],"drainableMonitors":0},` +
			`"template":"bg","intent":"","name":"w-1","nameSource":"flag","sessionId":"` + sid + `","resumeSessionId":"` + sid + `",` +
			`"daemonShort":"88888888","cliVersion":"2.1.314","cwd":"/ws/inst","backend":"daemon"`
		if flags != "" {
			doc += `,"respawnFlags":` + flags
		}
		return doc + `}`
	}
	read := func(t *testing.T, body string) ([]string, error) {
		t.Helper()
		jobsDir := t.TempDir()
		writeJobFixture(t, jobsDir, "88888888", body)
		return readSavedOptions(jobsDir, sid)
	}

	t.Run("flat string array returned in order", func(t *testing.T) {
		want := []string{"--permission-mode", "bypassPermissions", "--name", "w-1", "--model", "m", "--settings", `{"env":{"K":"v"}}`}
		flags, _ := json.Marshal(want)
		got, err := read(t, state(string(flags)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("empty array", func(t *testing.T) {
		got, err := read(t, state(`[]`))
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("got %#v, want an empty non-nil slice", got)
		}
	})

	for _, bad := range []struct{ name, flags string }{
		{"absent", ""},
		{"null", `null`},
		{"object", `{"a":"b"}`},
		{"number element", `[1]`},
		{"nested array", `[["a"]]`},
		{"null element", `["a",null]`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			if got, err := read(t, state(bad.flags)); err == nil {
				t.Fatalf("accepted %s as %q", bad.flags, got)
			}
		})
	}

	t.Run("symlinked state file", func(t *testing.T) {
		jobsDir := t.TempDir()
		target := filepath.Join(t.TempDir(), "state.json")
		if err := os.WriteFile(target, []byte(state(`["--name","w-1"]`)), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(jobsDir, "88888888")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "state.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := readSavedOptions(jobsDir, sid); err == nil {
			t.Fatal("read saved options through a symlink")
		}
	})

	t.Run("other owner", func(t *testing.T) {
		withJobStateOwner(t, func() int { return os.Getuid() + 1 })
		if _, err := read(t, state(`["--name","w-1"]`)); err == nil {
			t.Fatal("read saved options from another user's file")
		}
	})

	t.Run("errors never carry a value", func(t *testing.T) {
		_, err := read(t, state(`["--settings","{\"env\":{\"K\":\"s3cr3t\"}}",5]`))
		if err == nil {
			t.Fatal("accepted a non-string element")
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Fatalf("error carries an option value: %v", err)
		}
	})
}

func TestJobActivityProjection(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantCron    bool
		wantNonCron int
	}{
		{"one cron task", `{"tasks":1,"kinds":["session_cron"]}`, true, 0},
		{"cron and bash", `{"tasks":2,"kinds":["session_cron","bash"]}`, false, 1},
		{"kinds short of tasks", `{"tasks":2,"kinds":["session_cron"]}`, false, 2},
		{"no kinds", `{"tasks":1}`, false, 1},
		{"nothing in flight", `{"tasks":0,"kinds":[]}`, false, 0},
		{"object kinds", `{"tasks":1,"kinds":{"x":1}}`, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var js jobState
			if err := json.Unmarshal([]byte(`{"state":"working","tempo":"idle","inFlight":`+tc.body+`}`), &js); err != nil {
				t.Fatal(err)
			}
			a := jobActivityFromState(js)
			if a.CronOnly != tc.wantCron || a.NonCronInFlight != tc.wantNonCron {
				t.Fatalf("CronOnly=%v NonCronInFlight=%d, want %v %d", a.CronOnly, a.NonCronInFlight, tc.wantCron, tc.wantNonCron)
			}
			if !a.Readable || a.InFlightTasks != js.InFlight.Tasks || a.State != "working" || a.Tempo != "idle" {
				t.Fatalf("projection lost a field: %+v", a)
			}
		})
	}

	// An armed idle worker read through recordJobActivity is continuable.
	jobsDir := t.TempDir()
	const sid = "99999999-9999-4999-8999-999999999999"
	writeJobFixture(t, jobsDir, "99999999", `{"sessionId":"`+sid+`","state":"working","tempo":"idle","inFlight":{"tasks":1,"kinds":["session_cron"]}}`)
	a := recordJobActivity(jobsDir, watch.StagedRecord{SessionID: sid})
	if got := watch.ClassifySessionActivity(a); got != watch.ActivityDetachedIdle {
		t.Fatalf("armed idle worker classifies %v, want detached-idle", got)
	}
}
