package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/removal"
)

func TestPass_PrintsALivenessKeepThatNamesAProcess(t *testing.T) {
	f := newPassFixture(t)
	prev := listProcessesFunc
	listProcessesFunc = func(context.Context, *removal.Env) *removal.Listing {
		return &removal.Listing{Procs: []removal.Process{{PID: 777, Cwd: f.clone}}}
	}
	t.Cleanup(func() { listProcessesFunc = prev })

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(res.Kept) != 1 || res.Kept[0].Check.Name != removal.CheckLiveness {
		t.Fatalf("result %+v; want the unit kept by liveness", res)
	}
	if out := f.stderr.String(); !strings.Contains(out, "liveness check") || !strings.Contains(out, "pid 777") {
		t.Fatalf("output %q; want the liveness keep and the process named", out)
	}
}

func TestPass_KeepsEveryUnitWhenTheProcessListingFails(t *testing.T) {
	f := newPassFixture(t)
	prev := listProcessesFunc
	listProcessesFunc = func(context.Context, *removal.Env) *removal.Listing {
		return &removal.Listing{Err: errors.New("lsof failed")}
	}
	t.Cleanup(func() { listProcessesFunc = prev })

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(*f.destroyed) != 0 {
		t.Fatalf("reaped %d; want nothing removed", res.Reaped)
	}
}

func TestPass_InvalidRemovalSettingsRemoveNothing(t *testing.T) {
	for name, extra := range map[string]string{
		"build_trees": "build_trees = [\"target/*\"]\n",
		"disposable":  "\n[session.removal]\ndisposable = [{ pattern = \"*.orig\" }]\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newPassFixture(t)
			cfgPath := filepath.Join(f.root, config.ConfigDir, config.ConfigFile)
			body, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, cfgPath, string(body)+extra)
			// The unit holds nothing unique: only the settings stop it.
			res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if res.Reaped != 0 || len(*f.destroyed) != 0 {
				t.Fatalf("reaped %d, destroyed %v; want nothing removed", res.Reaped, *f.destroyed)
			}
			if out := f.stderr.String(); !strings.Contains(out, "removed nothing") {
				t.Fatalf("output %q; want the refusal named", out)
			}
		})
	}
}

func TestPass_HonorsDeclaredBuildTreesAndWorkspacePatterns(t *testing.T) {
	f := newPassFixture(t)
	cfgPath := filepath.Join(f.root, config.ConfigDir, config.ConfigFile)
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, cfgPath, string(body)+"build_trees = [\"dist/\"]\n\n[session.removal]\ndisposable = [{ pattern = \"*.orig\", review = \"https://example.com/pr/1\" }]\n")
	writeTestFile(t, filepath.Join(f.clone, "dist", "bundle.js"), "x")
	writeTestFile(t, filepath.Join(f.inst, "merge.orig"), "x")

	n, err := reapWorkspace(f.root, f.jobsDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reaped %d; want the unit removed\n%s", n, f.stderr.String())
	}
}

func TestJobDirFor(t *testing.T) {
	jobs := t.TempDir()
	id := "abcdef12-3456-7890-abcd-ef1234567890"
	if got := jobDirFor(jobs, id); got != "" {
		t.Fatalf("jobDirFor with no directory = %q", got)
	}
	if err := os.Mkdir(filepath.Join(jobs, "abcdef12"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := jobDirFor(jobs, id); got != filepath.Join(jobs, "abcdef12") {
		t.Fatalf("jobDirFor = %q; want the prefix directory", got)
	}
	if got := jobDirFor(jobs, "../x"); got != "" {
		t.Fatalf("jobDirFor with a path in the id = %q", got)
	}
}
