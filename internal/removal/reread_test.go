package removal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reapVerdict checks out the clone's main branch, evaluates the fixture's
// unit and fails unless the rule lets it go.
func reapVerdict(t *testing.T, f fixture, p Policy) Verdict {
	t.Helper()
	checkoutMain(t, f)
	v := Evaluate(context.Background(), f.unit(), p)
	if v.Kind != Reap {
		t.Fatalf("verdict %s, checks %+v; want reap", v.Kind, v.Checks)
	}
	return v
}

// checkoutMain gives the clone a local main branch and a checkout: the
// fixture's remote HEAD may name a branch the seed never pushed, which
// leaves a fresh clone with no checkout.
func checkoutMain(t *testing.T, f fixture) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(f.clone, "README")); err == nil {
		return
	}
	gitRun(t, f.clone, "checkout", "-q", "-b", "main", "origin/main")
}

// withInstanceRecord gives the fixture's instance a .niwa holding one record.
func withInstanceRecord(t *testing.T, f fixture) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.instance, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.instance, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func containsPart(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestVerdictCarriesTheWalksRepositories(t *testing.T) {
	f := newFixture(t)
	v := reapVerdict(t, f, f.policy(t))
	if len(v.Repos) != 1 || v.Repos[0].Rel != "tools/app" {
		t.Fatalf("verdict repositories %+v; want tools/app", v.Repos)
	}
	r := v.Repos[0]
	if !r.read || !r.InInstance() || r.Linked() || r.Prunable() {
		t.Fatalf("repository read %+v; want a finished read of a clone in the instance", r)
	}
	if len(r.index) == 0 {
		t.Fatal("the rule's index read was not carried")
	}
}

func TestRuleDifferencesNoneOnAnUnchangedUnit(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	v := reapVerdict(t, f, p)
	snap := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil)
	if d := snap.RuleDifferences(v); len(d) != 0 {
		t.Fatalf("differences on an unchanged unit: %v", d)
	}
}

func TestRuleDifferencesNameWhatChanged(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f fixture)
		want   string
	}{
		{"an untracked file", func(t *testing.T, f fixture) {
			if err := os.WriteFile(filepath.Join(f.clone, "late.txt"), []byte("late\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "tools/app/late.txt"},
		{"a modified tracked file", func(t *testing.T, f fixture) {
			if err := os.WriteFile(filepath.Join(f.clone, "README"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "tools/app/README"},
		{"a flagged index entry", func(t *testing.T, f fixture) {
			gitRun(t, f.clone, "update-index", "--assume-unchanged", "README")
		}, "tools/app/README"},
		{"a key that names a program", func(t *testing.T, f fixture) {
			gitRun(t, f.clone, "config", "core.fsmonitor", "/bin/echo")
		}, "key core.fsmonitor in tools/app"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			p := f.policy(t)
			v := reapVerdict(t, f, p)
			c.change(t, f)
			d := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil).RuleDifferences(v)
			if !containsPart(d, c.want) {
				t.Fatalf("differences %v; want one naming %q", d, c.want)
			}
		})
	}
}

// renameToTombstone renames the fixture's instance as the pass does and
// returns the new path.
func renameToTombstone(t *testing.T, f fixture) string {
	t.Helper()
	tomb := filepath.Join(f.root, ".niwa-tombstone-ws-1-0011aabb")
	if err := os.Rename(f.instance, tomb); err != nil {
		t.Fatal(err)
	}
	return tomb
}

func TestCompareSnapshotsAcrossTheRenameFindsNothingOnAnUnchangedUnit(t *testing.T) {
	f := newFixture(t)
	withInstanceRecord(t, f)
	p := f.policy(t)
	v := reapVerdict(t, f, p)
	before := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil)
	tomb := renameToTombstone(t, f)
	after := p.Env.TakeSnapshot(context.Background(), v, f.instance, tomb, nil)
	if d := CompareSnapshots(before, after, nil); len(d) != 0 {
		t.Fatalf("differences across the rename of an unchanged unit: %v", d)
	}
}

func TestCompareSnapshotsNamesWhatChangedOnTheTombstone(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f fixture, tomb string)
		want   string
	}{
		{"a commit that leaves status clean", func(t *testing.T, f fixture, tomb string) {
			gitRun(t, filepath.Join(tomb, "tools", "app"), "commit", "-q", "--allow-empty", "-m", "late")
		}, "ref refs/heads/main in tools/app"},
		{"a new branch", func(t *testing.T, f fixture, tomb string) {
			gitRun(t, filepath.Join(tomb, "tools", "app"), "branch", "late")
		}, "ref refs/heads/late in tools/app (new)"},
		{"a detached HEAD", func(t *testing.T, f fixture, tomb string) {
			gitRun(t, filepath.Join(tomb, "tools", "app"), "checkout", "-q", "--detach")
		}, "worktree tools/app in tools/app"},
		{"a stash", func(t *testing.T, f fixture, tomb string) {
			clone := filepath.Join(tomb, "tools", "app")
			if err := os.WriteFile(filepath.Join(clone, "README"), []byte("stashed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitRun(t, clone, "stash", "-q")
		}, "stash stash@{0} in tools/app (new)"},
		{"an untracked file", func(t *testing.T, f fixture, tomb string) {
			if err := os.WriteFile(filepath.Join(tomb, "tools", "app", "late.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "tools/app/late.txt"},
		{"a new record under .niwa", func(t *testing.T, f fixture, tomb string) {
			if err := os.WriteFile(filepath.Join(tomb, ".niwa", "mark.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, ".niwa/mark.json (new)"},
		{"a changed record under .niwa", func(t *testing.T, f fixture, tomb string) {
			if err := os.WriteFile(filepath.Join(tomb, ".niwa", "instance.json"), []byte(`{"x":1}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}, ".niwa/instance.json"},
		{"a pid record", func(t *testing.T, f fixture, tomb string) {
			if err := os.MkdirAll(filepath.Join(tomb, ".niwa", "pids"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(tomb, ".niwa", "pids", "7.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, ".niwa/pids/7.json (new)"},
		{"a key that names a program", func(t *testing.T, f fixture, tomb string) {
			gitRun(t, filepath.Join(tomb, "tools", "app"), "config", "diff.x.textconv", "/bin/cat")
		}, "key diff.x.textconv in tools/app"},
		{"a repository swapped for another directory", func(t *testing.T, f fixture, tomb string) {
			clone := filepath.Join(tomb, "tools", "app")
			if err := os.Rename(clone, clone+".old"); err != nil {
				t.Fatal(err)
			}
			gitRun(t, filepath.Join(tomb, "tools"), "clone", "-q", f.remoteURL, "app")
		}, "tools/app: changed since the walk"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			withInstanceRecord(t, f)
			p := f.policy(t)
			v := reapVerdict(t, f, p)
			before := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil)
			if d := before.RuleDifferences(v); len(d) != 0 {
				t.Fatalf("re-read differences before the change: %v", d)
			}
			tomb := renameToTombstone(t, f)
			c.change(t, f, tomb)
			after := p.Env.TakeSnapshot(context.Background(), v, f.instance, tomb, nil)
			d := CompareSnapshots(before, after, nil)
			if !containsPart(d, c.want) {
				t.Fatalf("differences %v; want one naming %q", d, c.want)
			}
		})
	}
}

// A linked worktree inside the instance names its admin directory by
// absolute path, which the rename moves, so until worktrees are removed
// before the rename the check on the tombstone counts it as changed.
func TestTombstoneSnapshotCountsALinkedWorktreeAsChanged(t *testing.T) {
	f := newFixture(t)
	checkoutMain(t, f)
	gitRun(t, f.clone, "worktree", "add", "-q", filepath.Join(f.instance, "wt"), "-b", "wt-branch", "main")
	gitRun(t, f.clone, "push", "-q", "origin", "wt-branch")
	p := f.policy(t)
	v := Evaluate(context.Background(), f.unit(), p)
	if v.Kind != Reap {
		t.Fatalf("verdict %s, checks %+v; want reap", v.Kind, v.Checks)
	}
	before := p.Env.TakeSnapshot(context.Background(), v, f.instance, f.instance, nil)
	if d := before.RuleDifferences(v); len(d) != 0 {
		t.Fatalf("re-read differences: %v", d)
	}
	tomb := renameToTombstone(t, f)
	after := p.Env.TakeSnapshot(context.Background(), v, f.instance, tomb, nil)
	d := CompareSnapshots(before, after, nil)
	if !containsPart(d, "wt: a linked worktree names its admin directory by a path the rename moved") {
		t.Fatalf("differences %v; want the linked worktree counted as changed", d)
	}

	// A worktree the pass removed before the rename is named as lost: its
	// entries may be gone, and nothing else changed.
	after = p.Env.TakeSnapshot(context.Background(), v, f.instance, tomb, []string{"wt"})
	if d := CompareSnapshots(before, after, []string{"wt"}); containsPart(d, "wt: gone") || containsPart(d, "linked worktree") {
		t.Fatalf("differences %v; want the lost worktree's own entries allowed to be gone", d)
	}
}

func TestRecordHashesNoteLinksAndOtherOwners(t *testing.T) {
	dir := t.TempDir()
	niwa := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(niwa, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(niwa, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwa, "rec"), []byte("r"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := hashRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got[".niwa/link"] != "symlink to /etc/passwd" {
		t.Fatalf("link noted as %q", got[".niwa/link"])
	}
	if !strings.HasPrefix(got[".niwa/rec"], "sha256 ") {
		t.Fatalf("record noted as %q", got[".niwa/rec"])
	}

	orig := currentUID
	currentUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { currentUID = orig })
	if _, err := hashRecords(dir); err == nil {
		t.Fatal("hashRecords read a .niwa another user owns")
	}

	absent, err := hashRecords(t.TempDir())
	if err != nil || absent[".niwa"] != "absent" {
		t.Fatalf("hashRecords with no .niwa = %v, %v", absent, err)
	}

	linked := t.TempDir()
	if err := os.Symlink(niwa, filepath.Join(linked, ".niwa")); err != nil {
		t.Fatal(err)
	}
	currentUID = orig
	if _, err := hashRecords(linked); err == nil {
		t.Fatal("hashRecords followed a .niwa that is a symlink")
	}
}

func TestNewWithinTellsTheSameProcessAcrossTheRename(t *testing.T) {
	start := time.Unix(100, 0)
	earlier := &Listing{Procs: []Process{
		{PID: 10, Start: start, Cwd: "/elsewhere", Files: []string{"/ws/inst/a.log"}},
		{PID: 11, Start: start, Cwd: "/elsewhere"},
	}}
	later := &Listing{Procs: []Process{
		{PID: 10, Start: start, Cwd: "/elsewhere", Files: []string{"/ws/.niwa-tombstone-inst-0011aabb/a.log"}},
		{PID: 11, Start: start, Cwd: "/ws/.niwa-tombstone-inst-0011aabb"},
		{PID: 12, Start: start, Cwd: "/ws/.niwa-tombstone-inst-0011aabb/sub"},
		{PID: 10, Start: start.Add(time.Second), Cwd: "/ws/.niwa-tombstone-inst-0011aabb"},
	}}
	got, err := NewWithin(earlier, []string{"/ws/inst"}, later, "/ws/.niwa-tombstone-inst-0011aabb")
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, p := range got {
		pids = append(pids, p.PID)
	}
	// 10 held a file in the instance before and holds it in the tombstone
	// now: the same process. 11 moved its working directory in: new. 12 is
	// new. The second 10 reused the PID: new.
	if len(pids) != 3 || pids[0] != 11 || pids[1] != 12 || pids[2] != 10 {
		t.Fatalf("new processes %v; want 11, 12 and the PID-reusing 10", pids)
	}

	if _, err := NewWithin(earlier, []string{"/ws/inst"}, &Listing{Err: errors.New("lsof failed")}, "/ws/inst"); err == nil {
		t.Fatal("NewWithin accepted a failed listing")
	}
	if _, err := NewWithin(nil, []string{"/ws/inst"}, later, "/ws/inst"); err == nil {
		t.Fatal("NewWithin accepted a missing earlier listing")
	}

	// The earlier listing reported the instance through its resolved form,
	// which can't be read once the instance is renamed: naming that form too
	// keeps the same process from reading as new.
	resolved := &Listing{Procs: []Process{{PID: 20, Start: start, Cwd: "/elsewhere", Files: []string{"/private/ws/inst/a.log"}}}}
	moved := &Listing{Procs: []Process{{PID: 20, Start: start, Cwd: "/elsewhere", Files: []string{"/private/ws/.niwa-tombstone-inst-0011aabb/a.log"}}}}
	got, err = NewWithin(resolved, []string{"/ws/inst", "/private/ws/inst"}, moved, "/private/ws/.niwa-tombstone-inst-0011aabb")
	if err != nil || len(got) != 0 {
		t.Fatalf("new processes %+v (%v); want none", got, err)
	}
}

func TestReadDispatchPending(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		inst := t.TempDir()
		if err := os.MkdirAll(filepath.Join(inst, ".niwa"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(inst, DispatchPendingMarker), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return inst
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got, ok := ReadDispatchPending(write(t, ts.Format(time.RFC3339)+"\n")); !ok || !got.Equal(ts) {
		t.Fatalf("ReadDispatchPending = %v, %v; want %v", got, ok, ts)
	}
	if _, ok := ReadDispatchPending(write(t, "not a time\n")); ok {
		t.Fatal("ReadDispatchPending parsed a malformed marker")
	}
	if _, ok := ReadDispatchPending(write(t, strings.Repeat("x", maxRecordBytes+1))); ok {
		t.Fatal("ReadDispatchPending read a marker over its cap")
	}

	linked := t.TempDir()
	if err := os.MkdirAll(filepath.Join(linked, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "marker")
	if err := os.WriteFile(target, []byte(ts.Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(linked, DispatchPendingMarker)); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadDispatchPending(linked); ok {
		t.Fatal("ReadDispatchPending followed a marker that is a link")
	}

	owned := write(t, ts.Format(time.RFC3339))
	orig := currentUID
	currentUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { currentUID = orig })
	if _, ok := ReadDispatchPending(owned); ok {
		t.Fatal("ReadDispatchPending read another user's marker")
	}
}
