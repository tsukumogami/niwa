package functional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registerReapLossScanSteps wires the steps the reap loss-scan scenarios use:
// a commit made by hand in a dispatch instance's clone, and a snapshot of the
// instance's whole tree, .git included, to show a reap that keeps it left
// every byte where it was.
func registerReapLossScanSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a commit is made on branch "([^"]*)" in repo "([^"]*)" of the dispatch instance$`, aCommitIsMadeInTheDispatchInstance)
	ctx.Step(`^the dispatch instance's contents are recorded$`, theDispatchInstancesContentsAreRecorded)
	ctx.Step(`^the dispatch instance's contents are unchanged$`, theDispatchInstancesContentsAreUnchanged)
	ctx.Step(`^the error output names the dispatch instance$`, theErrorOutputNamesTheDispatchInstance)
	ctx.Step(`^the first dispatch instance still exists$`, theFirstDispatchInstanceStillExists)
}

func theFirstDispatchInstanceStillExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.authority.firstInstance == "" {
		return fmt.Errorf("no first dispatch instance was kept")
	}
	if _, err := os.Stat(s.authority.firstInstance); err != nil {
		return fmt.Errorf("the first dispatch instance %s is gone: %w\nstderr:\n%s", s.authority.firstInstance, err, s.stderr)
	}
	return nil
}

// dispatchInstanceForStep returns the dispatch instance the scenario made.
func dispatchInstanceForStep(s *testState) (string, error) {
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return "", fmt.Errorf("no dispatch instance under %s", s.workspaceRoot)
	}
	return inst, nil
}

func aCommitIsMadeInTheDispatchInstance(ctx context.Context, branch, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	clone := filepath.Join(inst, filepath.FromSlash(repo))
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	for _, args := range [][]string{
		{"checkout", "-q", "-b", branch},
		{"commit", "-q", "--allow-empty", "-m", "work nobody pushed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = clone
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return ctx, fmt.Errorf("git %v in %s: %v\n%s", args, clone, err, out)
		}
	}
	return ctx, nil
}

// instanceTreeDigest lists every entry under dir with its mode, modification time
// and, for a file, a hash of its contents, without following links.
func instanceTreeDigest(dir string) (string, error) {
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(&b, "%s %s %d", rel, info.Mode(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			b.WriteString(" " + hex.EncodeToString(sum[:]))
		}
		b.WriteString("\n")
		return nil
	})
	return b.String(), err
}

func theDispatchInstancesContentsAreRecorded(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	digest, err := instanceTreeDigest(inst)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, instanceDigestKey{}, digest), nil
}

type instanceDigestKey struct{}

func theDispatchInstancesContentsAreUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	before, ok := ctx.Value(instanceDigestKey{}).(string)
	if !ok {
		return fmt.Errorf("the dispatch instance's contents were never recorded")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	after, err := instanceTreeDigest(inst)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("the dispatch instance changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	return nil
}

func theErrorOutputNamesTheDispatchInstance(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	want := "niwa: kept " + filepath.Base(inst) + ":"
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("error output does not contain %q:\n%s", want, s.stderr)
	}
	return nil
}
