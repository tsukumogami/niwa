package removal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRecordedAcceptsARelativeCleanPath(t *testing.T) {
	instance := t.TempDir()
	writeFile(t, filepath.Join(instance, "a", "b"), "x")
	for _, p := range []string{"a/b", "a/", "a"} {
		got, err := ResolveRecorded(Base{Instance: instance}, p)
		if err != nil {
			t.Fatalf("ResolveRecorded(%q): %v", p, err)
		}
		if want := filepath.Join(instance, filepath.FromSlash(strings.TrimSuffix(p, "/"))); got != want {
			t.Errorf("ResolveRecorded(%q) = %q, want %q", p, got, want)
		}
	}
	if err := os.MkdirAll(filepath.Join(instance, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRecorded(Base{Instance: instance}, "d/"); err != nil {
		t.Fatalf("a directory with its trailing slash: %v", err)
	}
}

func TestResolveRecordedRefuses(t *testing.T) {
	instance := t.TempDir()
	writeFile(t, filepath.Join(instance, "a", "b"), "x")
	for _, p := range []string{
		"",
		"/abs",
		"../a",
		"a/../b",
		"a/..",
		"..",
		".",
		"a/./b",
		"./a",
		"a//b",
		"a/b//",
		"a\x00b",
		`a\b`,
	} {
		_, err := ResolveRecorded(Base{Instance: instance}, p)
		if err == nil {
			t.Errorf("ResolveRecorded(%q) succeeded", p)
			continue
		}
		if !strings.HasPrefix(err.Error(), fmt.Sprintf("%q: ", p)) {
			t.Errorf("ResolveRecorded(%q): error %q doesn't start with the path", p, err)
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ResolveRecorded(%q): a refused path reads as missing: %v", p, err)
		}
	}
}

func TestResolveRecordedTellsAMissingPathFromARefusedOne(t *testing.T) {
	instance := t.TempDir()
	_, err := ResolveRecorded(Base{Instance: instance}, "gone/file")
	if !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), `"gone/file": gone does not exist`) {
		t.Fatalf("missing path: %v; want not-exist naming the first missing component", err)
	}
}

func TestResolveRecordedBindsJobToTheJobDirectory(t *testing.T) {
	instance, job := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(job, "x"), "job")
	writeFile(t, filepath.Join(instance, "x"), "instance")
	got, err := ResolveRecorded(Base{Instance: instance, JobDir: job}, "job:x")
	if err != nil || got != filepath.Join(job, "x") {
		t.Fatalf("job:x = %q, %v; want the job directory's file", got, err)
	}
	// Only the job directory: job:x never falls back to the instance.
	if err := os.Remove(filepath.Join(job, "x")); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveRecorded(Base{Instance: instance, JobDir: job}, "job:x"); err == nil {
		t.Fatalf("job:x with the job's file gone = %q; want it missing, not the instance's", got)
	}
	for _, tc := range []struct {
		base Base
		p    string
	}{
		{Base{Instance: instance}, "job:x"},
		{Base{Instance: instance, JobDir: job}, "job:"},
		{Base{Instance: instance, JobDir: job}, "job:../x"},
		{Base{Instance: instance, JobDir: job}, "job:/x"},
		{Base{Instance: instance, JobDir: job}, "job:a//b"},
	} {
		if _, err := ResolveRecorded(tc.base, tc.p); err == nil || !strings.HasPrefix(err.Error(), fmt.Sprintf("%q: ", tc.p)) {
			t.Errorf("ResolveRecorded(%+v, %q): %v; want a refusal naming the path", tc.base, tc.p, err)
		}
	}
	// A path without the prefix resolves against the instance, not the job.
	got, err = ResolveRecorded(Base{Instance: instance, JobDir: job}, "x")
	if err != nil || got != filepath.Join(instance, "x") {
		t.Fatalf("x = %q, %v; want the instance's file", got, err)
	}
}

func TestResolveRecordedRefusesASymlinkInAnyComponent(t *testing.T) {
	instance := t.TempDir()
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "f"), "x")
	symlink(t, elsewhere, filepath.Join(instance, "mid"))
	symlink(t, filepath.Join(elsewhere, "f"), filepath.Join(instance, "real", "last"))
	for p, want := range map[string]string{
		"mid/f":     `"mid/f": mid is a symlink`,
		"real/last": `"real/last": real/last is a symlink`,
		"mid":       `"mid": mid is a symlink`,
	} {
		_, err := ResolveRecorded(Base{Instance: instance}, p)
		if err == nil || err.Error() != want {
			t.Errorf("ResolveRecorded(%q): %v; want %q", p, err, want)
		}
	}
	// A link at the base itself is the caller's and is followed.
	linkedBase := filepath.Join(t.TempDir(), "base")
	symlink(t, elsewhere, linkedBase)
	if _, err := ResolveRecorded(Base{Instance: linkedBase}, "f"); err != nil {
		t.Fatalf("a link at the base: %v", err)
	}
}

func TestResolveRecordedRefusesAnotherDevice(t *testing.T) {
	root, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := os.Lstat("/dev")
	if err != nil {
		t.Skipf("no /dev: %v", err)
	}
	rd, _ := deviceOf(root)
	dd, _ := deviceOf(dev)
	if rd == dd {
		t.Skip("/dev is on the root's device here")
	}
	_, err = ResolveRecorded(Base{Instance: "/"}, "dev/null")
	if err == nil || err.Error() != `"dev/null": dev is on another device` {
		t.Fatalf("ResolveRecorded across a device: %v; want a refusal naming dev", err)
	}
}
