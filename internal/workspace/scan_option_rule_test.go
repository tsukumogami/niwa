package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
	"unsafe"
)

// The scan niwa destroy runs starts git only through a command builder the
// caller hands it in a ScanOption: gitOutput, gitOutputEnv, gitNetwork (the
// ls-remote and the fetch) and the `git version` read all build their
// command with it, so no call the scan makes starts plain git with the
// inherited environment. And internal/workspace imports nothing from
// internal/removal: the builder comes in as a function, not as a type of
// removal's.
//
// The builder's exact signature isn't fixed here. The tests find it by
// reflection, as the one field of scanConfig whose type is a function
// returning *exec.Cmd, and set it through a ScanOption of their own, so this
// file builds before and after the option exists.

var envAssignment = regexp.MustCompile(`^[A-Z][A-Z0-9_]*=`)

// recordingScanBuilder returns a ScanOption that sets scanConfig's command
// builder to one that starts every git it builds through a wrapper logging
// "builder <args>", or false when scanConfig holds no builder. The builder
// it makes reads its arguments generically: a context.Context is the
// command's context, string and []string arguments are git's arguments in
// order, an absolute path to an existing directory in first place is the
// directory git runs in (-C), and NAME=value strings are environment
// entries.
func recordingScanBuilder(t *testing.T, builderGit string) (ScanOption, bool) {
	t.Helper()
	cmdType := reflect.TypeOf((*exec.Cmd)(nil))
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	cfgType := reflect.TypeOf(scanConfig{})
	for i := 0; i < cfgType.NumField(); i++ {
		ft := cfgType.Field(i).Type
		if ft.Kind() != reflect.Func || ft.NumOut() == 0 || ft.Out(0) != cmdType {
			continue
		}
		fn := reflect.MakeFunc(ft, func(in []reflect.Value) []reflect.Value {
			ctx := context.Background()
			var args, env []string
			add := func(s string) {
				if envAssignment.MatchString(s) {
					env = append(env, s)
					return
				}
				args = append(args, s)
			}
			for _, v := range in {
				switch {
				case v.Kind() == reflect.Interface && v.Type().Implements(ctxType):
					if !v.IsNil() {
						ctx = v.Interface().(context.Context)
					}
				case v.Kind() == reflect.String:
					add(v.String())
				case v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.String:
					for j := 0; j < v.Len(); j++ {
						add(v.Index(j).String())
					}
				}
			}
			if len(args) > 0 && filepath.IsAbs(args[0]) {
				if st, err := os.Stat(args[0]); err == nil && st.IsDir() {
					args = append([]string{"-C"}, args...)
				}
			}
			cmd := exec.CommandContext(ctx, builderGit, args...)
			cmd.Env = append(os.Environ(), env...)
			out := []reflect.Value{reflect.ValueOf(cmd)}
			for k := 1; k < ft.NumOut(); k++ {
				out = append(out, reflect.Zero(ft.Out(k)))
			}
			return out
		})
		field := i
		return func(c *scanConfig) {
			v := reflect.ValueOf(c).Elem().Field(field)
			reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Set(fn)
		}, true
	}
	return nil, false
}

// scanGitWrappers puts a git on PATH that logs "direct <args>" for a call
// the builder didn't start, and returns the builder's own git, which logs
// "builder <args>" and marks its process so the git on PATH, should git
// itself start it, logs nothing. Both run the real git. It returns the
// builder's git and the log.
func scanGitWrappers(t *testing.T) (builderGit, log string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	if real, err = filepath.EvalSymlinks(real); err != nil {
		t.Fatal(err)
	}
	pathDir := t.TempDir()
	builderDir := t.TempDir()
	log = filepath.Join(t.TempDir(), "git-calls.log")
	direct := fmt.Sprintf(`#!/bin/sh
if [ -z "$NIWA_SCAN_TEST_VIA_BUILDER" ]; then
	printf 'direct %%s\n' "$*" >> '%s'
fi
exec '%s' "$@"
`, log, real)
	via := fmt.Sprintf(`#!/bin/sh
printf 'builder %%s\n' "$*" >> '%s'
NIWA_SCAN_TEST_VIA_BUILDER=1
export NIWA_SCAN_TEST_VIA_BUILDER
exec '%s' "$@"
`, log, real)
	if err := os.WriteFile(filepath.Join(pathDir, "git"), []byte(direct), 0o755); err != nil {
		t.Fatal(err)
	}
	builderGit = filepath.Join(builderDir, "git-via-builder")
	if err := os.WriteFile(builderGit, []byte(via), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return builderGit, log
}

// builderScanInstance is an instance holding a pushed repository at
// alpha/app with an unpushed local branch, feature, whose file isn't on
// main: the scan lists and fetches the remote (gitNetwork), and checks
// whether the branch landed by content (git version, then gitOutputEnv's
// rev-list).
func builderScanInstance(t *testing.T) string {
	t.Helper()
	instanceDir, repo := setupInstanceWithRepo(t, "alpha", "app")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("only here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "feature.txt")
	gitRun(t, repo, "commit", "-q", "-m", "feature")
	gitRun(t, repo, "checkout", "-q", "main")
	return instanceDir
}

func TestScanOptionRule_EveryGitCallTheScanMakesGoesThroughTheBuilder(t *testing.T) {
	scans := []struct {
		name string
		run  func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error)
	}{
		{"ScanInstance", func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error) {
			s, err := ScanInstance(instanceDir, opts...)
			return []InstanceScan{s}, err
		}},
		{"ScanInstancesParallel", func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error) {
			return ScanInstancesParallel(filepath.Dir(instanceDir), []string{instanceDir}, 0, opts...)
		}},
	}
	for _, s := range scans {
		t.Run(s.name, func(t *testing.T) {
			instanceDir := builderScanInstance(t)
			builderGit, log := scanGitWrappers(t)
			opt, ok := recordingScanBuilder(t, builderGit)
			if !ok {
				t.Fatal("ScanOption carries no command builder: scanConfig has no field holding a function that returns *exec.Cmd, so the scan starts plain git")
			}

			results, err := s.run(instanceDir, WithNetworkTimeout(10*time.Second), opt)
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			for _, r := range results {
				for _, repo := range r.Repos {
					for _, l := range repo.Losses {
						if l.Kind == LossScanError {
							t.Errorf("a git call the scan built through the builder failed: %s: %s", repo.Name, l.Detail)
						}
					}
				}
				if !r.HasLoss() {
					t.Errorf("the scan found no loss; want the unpushed branch feature listed: %+v", r)
				}
			}

			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatalf("the git wrappers logged nothing: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var builder []string
			for _, line := range lines {
				if strings.HasPrefix(line, "direct ") {
					t.Errorf("the scan started git without the builder: git %s", strings.TrimPrefix(line, "direct "))
				}
				if strings.HasPrefix(line, "builder ") {
					builder = append(builder, strings.TrimPrefix(line, "builder "))
				}
			}
			// One call of each helper the scan uses, each through the
			// builder: gitOutput's status, gitNetwork's ls-remote and fetch,
			// gitOutputEnv's rev-list.
			for _, sub := range []string{"status", "ls-remote", "fetch", "rev-list"} {
				found := false
				for _, b := range builder {
					found = found || containsField(b, sub)
				}
				if !found {
					t.Errorf("no git %s went through the builder; calls through it:\n%s", sub, strings.Join(builder, "\n"))
				}
			}
		})
	}
}

// A scan handed no builder starts no git at all, from PATH or anywhere
// else: every call it would make is refused before it starts, so every
// repository in the instance is listed as a scan error, and nothing is
// passed as clean.
func TestScanOptionRule_AScanGivenNoBuilderStartsNoGit(t *testing.T) {
	scans := []struct {
		name string
		run  func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error)
	}{
		{"ScanInstance", func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error) {
			s, err := ScanInstance(instanceDir, opts...)
			return []InstanceScan{s}, err
		}},
		{"ScanInstancesParallel", func(instanceDir string, opts ...ScanOption) ([]InstanceScan, error) {
			return ScanInstancesParallel(filepath.Dir(instanceDir), []string{instanceDir}, 0, opts...)
		}},
	}
	for _, s := range scans {
		t.Run(s.name, func(t *testing.T) {
			instanceDir := builderScanInstance(t)
			scanInitGitRepo(t, filepath.Join(instanceDir, "beta", "lib"))
			_, log := scanGitWrappers(t)

			results, err := s.run(instanceDir, WithNetworkTimeout(10*time.Second))
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}

			raw, err := os.ReadFile(log)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if line != "" {
					t.Errorf("a scan given no builder started git: %s", line)
				}
			}

			for _, r := range results {
				want := map[string]bool{"app": false, filepath.Join("beta", "lib"): false}
				for _, repo := range r.Repos {
					if _, ok := want[repo.Name]; !ok {
						t.Errorf("the scan listed an unexpected repository %q: %+v", repo.Name, repo)
						continue
					}
					for _, l := range repo.Losses {
						if l.Kind == LossScanError {
							want[repo.Name] = true
						}
					}
				}
				for name, listed := range want {
					if !listed {
						t.Errorf("%s isn't listed as a scan error; a scan with no builder must list every repository: %+v", name, r.Repos)
					}
				}
			}
		})
	}
}

func containsField(line, word string) bool {
	for _, f := range strings.Fields(line) {
		if f == word {
			return true
		}
	}
	return false
}

// A guard: the builder comes in as a function, so internal/workspace, and
// everything it imports, takes nothing from internal/removal.
func TestScanOptionRule_WorkspaceImportsNothingFromRemoval(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	out, err := exec.Command(goBin, "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	deps := strings.Fields(string(out))
	if len(deps) == 0 {
		t.Fatal("go list -deps listed nothing")
	}
	for _, d := range deps {
		if strings.HasSuffix(d, "/internal/removal") || strings.Contains(d, "/internal/removal/") {
			t.Errorf("internal/workspace depends on %s", d)
		}
	}
}
