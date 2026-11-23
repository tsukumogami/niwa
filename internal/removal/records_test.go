package removal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// asAnotherUser makes every file the reader checks from here on count as
// another user's.
func asAnotherUser(t *testing.T) {
	t.Helper()
	prev := currentUID
	currentUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { currentUID = prev })
}

func mkfifo(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// finishesWithin fails the test when f doesn't return within d, so a read
// that blocks shows as a failure rather than a hung run.
func finishesWithin(t *testing.T, d time.Duration, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("still running after %s", d)
	}
}

func TestReadCappedReadsARegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r")
	writeFile(t, p, "hello")
	b, err := readCapped(p, 5)
	if err != nil || string(b) != "hello" {
		t.Fatalf("readCapped = %q, %v", b, err)
	}
	empty := filepath.Join(t.TempDir(), "e")
	writeFile(t, empty, "")
	if b, err := readCapped(empty, 5); err != nil || len(b) != 0 {
		t.Fatalf("readCapped of an empty file = %q, %v", b, err)
	}
}

func TestReadCappedRefusesALink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, target, "secret")
	link := filepath.Join(dir, "link")
	symlink(t, target, link)
	b, err := readCapped(link, 64)
	if err == nil || len(b) != 0 {
		t.Fatalf("readCapped through a link = %q, %v; want a refusal", b, err)
	}
	if !strings.Contains(err.Error(), "link is a symlink") {
		t.Fatalf("error %q; want the link named", err)
	}
}

func TestReadCappedRefusesAFIFOWithoutBlocking(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	mkfifo(t, fifo)
	var err error
	finishesWithin(t, 5*time.Second, func() { _, err = readCapped(fifo, 64) })
	if err == nil || !strings.Contains(err.Error(), "fifo is not a regular file") {
		t.Fatalf("readCapped of a FIFO: %v; want a refusal naming it", err)
	}
}

func TestReadCappedRefusesAFileOfAnotherOwner(t *testing.T) {
	p := filepath.Join(t.TempDir(), "theirs")
	writeFile(t, p, "x")
	asAnotherUser(t)
	if _, err := readCapped(p, 64); err == nil || !strings.Contains(err.Error(), "theirs is not owned by the current user") {
		t.Fatalf("readCapped of another user's file: %v; want a refusal", err)
	}
}

func TestReadCappedRefusesAFileOverItsCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	writeFile(t, p, "123456")
	if _, err := readCapped(p, 5); err == nil || !strings.Contains(err.Error(), "big is larger than 5 bytes") {
		t.Fatalf("readCapped over the cap: %v; want a refusal", err)
	}
	if _, err := readCapped(filepath.Join(t.TempDir(), "missing"), 5); err == nil || !os.IsNotExist(err) {
		t.Fatalf("readCapped of a missing file: %v; want not-exist", err)
	}
}

func TestReadCappedAtReadsRelativeToTheDirectory(t *testing.T) {
	instance := t.TempDir()
	writeFile(t, filepath.Join(instance, ".niwa", "r"), "inside")
	d := openNiwaDir(instance)
	defer d.close()
	if err := d.usable(); err != nil {
		t.Fatal(err)
	}
	// Moving .niwa away after the open leaves the reads on the directory
	// that was opened, not on whatever the path names now.
	if err := os.Rename(filepath.Join(instance, ".niwa"), filepath.Join(instance, "moved")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(instance, ".niwa", "r"), "swapped")
	b, err := d.read("r", 64)
	if err != nil || string(b) != "inside" {
		t.Fatalf("read = %q, %v; want the opened directory's file", b, err)
	}
}

// testRecord is a fixture record for the strict decoder.
type testRecord struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

const testSchema = "niwa.test/v1"

func TestDecodeRecordAcceptsTheFixture(t *testing.T) {
	var r testRecord
	if err := decodeRecord([]byte(`{"schema":"niwa.test/v1","name":"x"}`+"\n"), testSchema, &r); err != nil || r.Name != "x" {
		t.Fatalf("decodeRecord = %+v, %v", r, err)
	}
	instance := t.TempDir()
	writeFile(t, filepath.Join(instance, ".niwa", "keep.json"), `{"schema":"niwa.test/v1","name":"k"}`)
	d := openNiwaDir(instance)
	defer d.close()
	var k testRecord
	if err := readRecordAt(&d, "keep.json", maxKeepBytes, testSchema, &k); err != nil || k.Name != "k" {
		t.Fatalf("readRecordAt = %+v, %v", k, err)
	}
}

func TestDecodeRecordRefusesOverTheCap(t *testing.T) {
	instance := t.TempDir()
	body := `{"schema":"niwa.test/v1","name":"` + strings.Repeat("x", maxRecordBytes) + `"}`
	writeFile(t, filepath.Join(instance, ".niwa", "unit.json"), body)
	d := openNiwaDir(instance)
	defer d.close()
	var r testRecord
	err := readRecordAt(&d, "unit.json", maxRecordBytes, testSchema, &r)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("readRecordAt over the cap: %v; want a refusal", err)
	}
}

func TestDecodeRecordRefusesAnUnknownField(t *testing.T) {
	var r testRecord
	err := decodeRecord([]byte(`{"schema":"niwa.test/v1","name":"x","extra":1}`), testSchema, &r)
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("decodeRecord with an unknown field: %v; want a refusal naming it", err)
	}
}

func TestDecodeRecordRefusesTrailingData(t *testing.T) {
	for _, in := range []string{
		`{"schema":"niwa.test/v1","name":"x"} {}`,
		`{"schema":"niwa.test/v1","name":"x"}x`,
		`{"schema":"niwa.test/v1","name":"x"}{"schema":"niwa.test/v1"}`,
	} {
		var r testRecord
		if err := decodeRecord([]byte(in), testSchema, &r); err == nil || !strings.Contains(err.Error(), "trailing data") {
			t.Errorf("decodeRecord(%q): %v; want trailing data refused", in, err)
		}
	}
}

func TestDecodeRecordRefusesAnUnknownSchemaName(t *testing.T) {
	for _, in := range []string{
		`{"schema":"niwa.other/v1","name":"x"}`,
		`{"schema":"niwa.test/v2","name":"x"}`,
		`{"name":"x"}`,
	} {
		var r testRecord
		if err := decodeRecord([]byte(in), testSchema, &r); err == nil || !strings.Contains(err.Error(), "unknown schema name") {
			t.Errorf("decodeRecord(%q): %v; want the schema refused", in, err)
		}
	}
	var r testRecord
	if err := decodeRecord([]byte(`not json`), testSchema, &r); err == nil {
		t.Error("decodeRecord of text that isn't JSON succeeded")
	}
}

// bareUnit is an instance with no repository, which the rule passes when
// nothing else is in it.
func bareUnit(t *testing.T) (Unit, Policy) {
	t.Helper()
	instance := t.TempDir()
	return Unit{Name: "bare", InstancePath: instance, Liveness: LivenessGone}, Policy{Processes: &Listing{}}
}

// checksNamed returns the keeping checks of one name.
func checksNamed(v Verdict, name CheckName) []Check {
	var out []Check
	for _, c := range v.Checks {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

func TestEvaluateKeepsOnARefusedDispatchRetain(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, marker string){
		"link": func(t *testing.T, marker string) {
			target := filepath.Join(t.TempDir(), "reason")
			writeFile(t, target, "planted reason")
			symlink(t, target, marker)
		},
		"FIFO": func(t *testing.T, marker string) { mkfifo(t, marker) },
		"over 1 KiB": func(t *testing.T, marker string) {
			writeFile(t, marker, strings.Repeat("r", maxReasonBytes+1))
		},
	} {
		t.Run(name, func(t *testing.T) {
			u, p := bareUnit(t)
			plant(t, filepath.Join(u.InstancePath, DispatchRetainMarker))
			var v Verdict
			finishesWithin(t, 20*time.Second, func() { v = Evaluate(context.Background(), u, p) })
			first, _ := v.First()
			if v.Kind != Keep || first.Name != CheckHold || first.Evidence != "held by .niwa/dispatch-retain (its reason could not be read)" {
				t.Fatalf("verdict %s %+v; want the hold with its reason unreadable", v.Kind, v.Checks)
			}
			if strings.Contains(first.Evidence, "planted") {
				t.Fatalf("evidence %q carries the link target's content", first.Evidence)
			}
		})
	}
	t.Run("another owner", func(t *testing.T) {
		u, p := bareUnit(t)
		writeFile(t, filepath.Join(u.InstancePath, DispatchRetainMarker), "their reason")
		s := &evalState{niwa: openNiwaDir(u.InstancePath)}
		defer s.niwa.close()
		asAnotherUser(t)
		keeps := checkHold(context.Background(), u, p, s)
		if len(keeps) != 1 || keeps[0].Evidence != "held by .niwa/dispatch-retain (its reason could not be read)" {
			t.Fatalf("hold %+v; want another user's marker held and unread", keeps)
		}
	})
}

func TestEvaluateKeepsOnARefusedInstanceJSON(t *testing.T) {
	wantNamed := func(t *testing.T, keeps []Check) {
		t.Helper()
		for _, c := range keeps {
			if len(c.Paths) == 1 && c.Paths[0] == ".niwa/instance.json" && strings.HasPrefix(c.Evidence, "unreadable: .niwa/instance.json: ") {
				return
			}
		}
		t.Fatalf("keeps %+v; want .niwa/instance.json named as unreadable", keeps)
	}
	for name, plant := range map[string]func(t *testing.T, state string){
		"link": func(t *testing.T, state string) {
			target := filepath.Join(t.TempDir(), "instance.json")
			writeFile(t, target, "{}")
			symlink(t, target, state)
		},
		"FIFO": func(t *testing.T, state string) { mkfifo(t, state) },
		"over the cap": func(t *testing.T, state string) {
			writeFile(t, state, `{"managed_files":[]}`+strings.Repeat(" ", maxStateBytes))
		},
	} {
		t.Run(name, func(t *testing.T) {
			u, p := bareUnit(t)
			plant(t, filepath.Join(u.InstancePath, ".niwa", "instance.json"))
			var v Verdict
			finishesWithin(t, 20*time.Second, func() { v = Evaluate(context.Background(), u, p) })
			if v.Kind != Keep {
				t.Fatalf("verdict %s; want keep", v.Kind)
			}
			wantNamed(t, checksNamed(v, CheckOutside))
			wantClass(t, v, ".niwa/instance.json", ClassUnique, "")
		})
	}
	t.Run("another owner", func(t *testing.T) {
		u, _ := bareUnit(t)
		writeFile(t, filepath.Join(u.InstancePath, ".niwa", "instance.json"), "{}")
		s := &evalState{niwa: openNiwaDir(u.InstancePath)}
		defer s.niwa.close()
		asAnotherUser(t)
		wantNamed(t, s.managedFiles(u.InstancePath).failures)
	})
	t.Run("absent or not parsing keeps nothing", func(t *testing.T) {
		u, p := bareUnit(t)
		if err := os.MkdirAll(filepath.Join(u.InstancePath, ".niwa"), 0o755); err != nil {
			t.Fatal(err)
		}
		if v := Evaluate(context.Background(), u, p); v.Kind != Reap {
			t.Fatalf("no instance.json: verdict %s %+v; want reap", v.Kind, v.Checks)
		}
		writeFile(t, filepath.Join(u.InstancePath, ".niwa", "instance.json"), "not json")
		if v := Evaluate(context.Background(), u, p); v.Kind != Reap {
			t.Fatalf("instance.json that doesn't parse: verdict %s %+v; want reap", v.Kind, v.Checks)
		}
	})
}

func TestEvaluateCountsACacheDirTagLinkAsNoBuildTree(t *testing.T) {
	f := classFixture(t)
	tag := filepath.Join(f.clone, "target", "CACHEDIR.TAG")
	if err := os.Remove(tag); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "CACHEDIR.TAG")
	writeFile(t, real, cacheDirSignature+"\n")
	symlink(t, real, tag)
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	pc, ok := classOf(v, "tools/app/target/")
	if !ok || pc.Class == ClassBuildTree {
		t.Fatalf("target/ classed %+v (found %v); want no build tree through a linked tag", pc, ok)
	}
	wantClass(t, v, "tools/app/target/", ClassDisposable, NoticeGitIgnored)
}

func TestEvaluateFinishesWithAFIFOInEveryRecordPlace(t *testing.T) {
	f := classFixture(t)
	tag := filepath.Join(f.clone, "target", "CACHEDIR.TAG")
	state := filepath.Join(f.instance, ".niwa", "instance.json")
	ignore := filepath.Join(f.instance, ".gitignore")
	for _, p := range []string{tag, state, ignore} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{
		tag,
		state,
		ignore,
		filepath.Join(f.instance, DispatchRetainMarker),
		filepath.Join(f.instance, ".niwa", "keep.json"),
	} {
		mkfifo(t, p)
	}
	job := filepath.Join(t.TempDir(), "job")
	writeFile(t, filepath.Join(job, "scratch.md"), "notes")
	u := f.unit()
	u.JobDir = job

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var v Verdict
	finishesWithin(t, 30*time.Second, func() { v = Evaluate(ctx, u, classPolicy(t, f)) })
	if ctx.Err() != nil {
		t.Fatalf("Evaluate ran into its deadline")
	}
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	if hold := checksNamed(v, CheckHold); len(hold) != 1 || !strings.Contains(hold[0].Evidence, "its reason could not be read") {
		t.Errorf("hold %+v; want the FIFO marker held and unread", hold)
	}
	outside := checksNamed(v, CheckOutside)
	var stateNamed, briefNamed bool
	for _, c := range outside {
		if len(c.Paths) == 1 && c.Paths[0] == ".niwa/instance.json" && strings.HasPrefix(c.Evidence, "unreadable:") {
			stateNamed = true
		}
		if strings.Contains(c.Evidence, "a handoff brief may be present") {
			briefNamed = true
		}
	}
	if !stateNamed || !briefNamed {
		t.Errorf("outside %+v; want instance.json unreadable and job scratch kept for the brief", outside)
	}
	if pc, _ := classOf(v, "tools/app/target/"); pc.Class == ClassBuildTree {
		t.Errorf("target/ is a build tree through a FIFO tag")
	}
	wantClass(t, v, ".gitignore", ClassUnique, "")
}

func TestEvaluateKeepsWhenNiwaIsALink(t *testing.T) {
	u, p := bareUnit(t)
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "dispatch-retain"), "linked reason")
	writeFile(t, filepath.Join(target, "instance.json"), "{}")
	before := snapshot(t, target)
	symlink(t, target, filepath.Join(u.InstancePath, ".niwa"))

	v := Evaluate(context.Background(), u, p)
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	for _, name := range []CheckName{CheckHold, CheckRecords} {
		got := checksNamed(v, name)
		if len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != ".niwa" || !strings.Contains(got[0].Evidence, ".niwa is a symlink") {
			t.Errorf("%s check %+v; want .niwa named as a symlink", name, got)
		}
		if len(got) == 1 && strings.Contains(got[0].Evidence, "linked reason") {
			t.Errorf("%s evidence %q read through the link", name, got[0].Evidence)
		}
	}
	if after := snapshot(t, target); after != before {
		t.Fatalf("the link's target changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEvaluateKeepsWhenNiwaIsOwnedByAnother(t *testing.T) {
	u, p := bareUnit(t)
	writeFile(t, filepath.Join(u.InstancePath, ".niwa", "instance.json"), "{}")
	asAnotherUser(t)
	v := Evaluate(context.Background(), u, p)
	for _, name := range []CheckName{CheckHold, CheckRecords} {
		got := checksNamed(v, name)
		if len(got) != 1 || got[0].Paths[0] != ".niwa" || !strings.Contains(got[0].Evidence, ".niwa is not owned by the current user") {
			t.Errorf("%s check %+v; want .niwa named as another user's", name, got)
		}
	}
}

func TestEvaluateKeepsWhenNiwaIsNotADirectory(t *testing.T) {
	u, p := bareUnit(t)
	writeFile(t, filepath.Join(u.InstancePath, ".niwa"), "a file")
	v := Evaluate(context.Background(), u, p)
	got := checksNamed(v, CheckRecords)
	if len(got) != 1 || got[0].Paths[0] != ".niwa" || !strings.Contains(got[0].Evidence, ".niwa is not a directory") {
		t.Fatalf("records check %+v; want .niwa named", got)
	}
}

func TestEvaluateKeepsWhenPidsIsALink(t *testing.T) {
	u, p := bareUnit(t)
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "1.json"), "{}")
	before := snapshot(t, target)
	symlink(t, target, filepath.Join(u.InstancePath, ".niwa", "pids"))
	v := Evaluate(context.Background(), u, p)
	got := checksNamed(v, CheckRecords)
	if len(got) != 1 || len(got[0].Paths) != 1 || got[0].Paths[0] != ".niwa/pids" || !strings.Contains(got[0].Evidence, ".niwa/pids is a symlink") {
		t.Fatalf("records check %+v; want .niwa/pids named as a symlink", got)
	}
	if after := snapshot(t, target); after != before {
		t.Fatalf("the link's target changed")
	}
}

func TestEvaluateKeepsWhenPidsIsNotADirectory(t *testing.T) {
	u, p := bareUnit(t)
	writeFile(t, filepath.Join(u.InstancePath, ".niwa", "pids"), "x")
	got := checksNamed(Evaluate(context.Background(), u, p), CheckRecords)
	if len(got) != 1 || got[0].Paths[0] != ".niwa/pids" || !strings.Contains(got[0].Evidence, ".niwa/pids is not a directory") {
		t.Fatalf("records check %+v; want .niwa/pids named", got)
	}
}

func TestEvaluateKeepsWhenPidsIsOwnedByAnother(t *testing.T) {
	u, p := bareUnit(t)
	if err := os.MkdirAll(filepath.Join(u.InstancePath, ".niwa", "pids"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &evalState{niwa: openNiwaDir(u.InstancePath)}
	defer s.niwa.close()
	if err := s.niwa.usable(); err != nil {
		t.Fatal(err)
	}
	asAnotherUser(t)
	got := checkRecords(context.Background(), u, p, s)
	if len(got) != 1 || got[0].Paths[0] != ".niwa/pids" || !strings.Contains(got[0].Evidence, ".niwa/pids is not owned by the current user") {
		t.Fatalf("records check %+v; want .niwa/pids named as another user's", got)
	}
}

func TestEvaluateKeepsNothingOnRecordsWhenPidsIsAbsentOrAPlainDirectory(t *testing.T) {
	u, p := bareUnit(t)
	if err := os.MkdirAll(filepath.Join(u.InstancePath, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := checksNamed(Evaluate(context.Background(), u, p), CheckRecords); len(got) != 0 {
		t.Fatalf("records check %+v with no pids; want none", got)
	}
	if err := os.MkdirAll(filepath.Join(u.InstancePath, ".niwa", "pids"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := checksNamed(Evaluate(context.Background(), u, p), CheckRecords); len(got) != 0 {
		t.Fatalf("records check %+v with a plain pids directory; want none", got)
	}
	// With no .niwa at all, the records check keeps nothing either.
	u2, _ := bareUnit(t)
	if got := checksNamed(Evaluate(context.Background(), u2, p), CheckRecords); len(got) != 0 {
		t.Fatalf("records check %+v with no .niwa; want none", got)
	}
}
