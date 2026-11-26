package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerReapPathClassSteps wires the steps the path-class scenarios use:
// files and directories written into a dispatch instance, git run in one of
// its clones, a tracked file rewritten so only its content changes, and a
// background process whose working directory sits in the instance.
func registerReapPathClassSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a file "([^"]*)" is written in the dispatch instance with content "([^"]*)"$`, aFileIsWrittenInTheDispatchInstance)
	ctx.Step(`^an empty directory "([^"]*)" is made in the dispatch instance$`, anEmptyDirectoryIsMadeInTheDispatchInstance)
	ctx.Step(`^git "([^"]*)" is run in repo "([^"]*)" of the dispatch instance$`, gitIsRunInTheDispatchInstance)
	ctx.Step(`^the directory "([^"]*)" in the dispatch instance is made unreadable$`, theDirectoryIsMadeUnreadable)
	ctx.Step(`^the directory "([^"]*)" in the dispatch instance is made readable again$`, theDirectoryIsMadeReadable)
	ctx.Step(`^a file "([^"]*)" is written under the home directory with content "([^"]*)"$`, aFileIsWrittenUnderTheHome)
	ctx.Step(`^a tracked file "([^"]*)" in repo "([^"]*)" of the dispatch instance is pushed, then rewritten keeping its size and modification time$`, theTrackedFileIsRewritten)
	ctx.Step(`^a background process is started in the dispatch instance$`, aBackgroundProcessIsStarted)
	ctx.Step(`^the background process is stopped$`, theBackgroundProcessIsStopped)
	ctx.Step(`^the error output names the background process$`, theErrorOutputNamesTheBackgroundProcess)
}

type backgroundProcessKey struct{}

func instancePathFor(ctx context.Context, rel string) (string, error) {
	s := getState(ctx)
	if s == nil {
		return "", fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(inst, filepath.FromSlash(rel)), nil
}

func aFileIsWrittenInTheDispatchInstance(ctx context.Context, rel, content string) error {
	p, err := instancePathFor(ctx, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.ReplaceAll(content, `\n`, "\n")), 0o644)
}

func anEmptyDirectoryIsMadeInTheDispatchInstance(ctx context.Context, rel string) error {
	p, err := instancePathFor(ctx, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(p, 0o755)
}

func gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out), nil
}

func gitIsRunInTheDispatchInstance(ctx context.Context, args, repo string) error {
	clone, err := instancePathFor(ctx, repo)
	if err != nil {
		return err
	}
	_, err = gitIn(clone, strings.Fields(args)...)
	return err
}

func theDirectoryIsMadeUnreadable(ctx context.Context, rel string) error {
	p, err := instancePathFor(ctx, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(p, "inside.txt"), []byte("x"), 0o644); err != nil {
		return err
	}
	return os.Chmod(p, 0)
}

func theDirectoryIsMadeReadable(ctx context.Context, rel string) error {
	p, err := instancePathFor(ctx, rel)
	if err != nil {
		return err
	}
	return os.Chmod(p, 0o755)
}

func aFileIsWrittenUnderTheHome(ctx context.Context, rel, content string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	p := filepath.Join(s.homeDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.ReplaceAll(content, `\n`, "\n")), 0o644)
}

// theTrackedFileIsRewritten commits and pushes a file, then changes its
// content without changing its size or modification time, so only a git
// that compares content, or trusts the inode change time, sees it.
func theTrackedFileIsRewritten(ctx context.Context, name, repo string) error {
	clone, err := instancePathFor(ctx, repo)
	if err != nil {
		return err
	}
	p := filepath.Join(clone, filepath.FromSlash(name))
	if err := os.WriteFile(p, []byte("aaaaaaaa\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-q", "-m", "add " + name}, {"push", "-q", "origin", "HEAD"}} {
		if _, err := gitIn(clone, args...); err != nil {
			return err
		}
	}
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("tracked file %s is empty", name)
	}
	changed := []byte(strings.Repeat("z", len(data)))
	if string(changed) == string(data) {
		changed = []byte(strings.Repeat("y", len(data)))
	}
	if err := os.WriteFile(p, changed, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(p, info.ModTime(), info.ModTime())
}

func aBackgroundProcessIsStarted(ctx context.Context) (context.Context, error) {
	inst, err := instancePathFor(ctx, ".")
	if err != nil {
		return ctx, err
	}
	cmd := exec.Command("sleep", "60")
	cmd.Dir = inst
	if err := cmd.Start(); err != nil {
		return ctx, err
	}
	go func() { _ = cmd.Wait() }()
	// Give the process a moment to settle in its working directory.
	time.Sleep(100 * time.Millisecond)
	return context.WithValue(ctx, backgroundProcessKey{}, cmd.Process), nil
}

func theBackgroundProcessIsStopped(ctx context.Context) error {
	p, ok := ctx.Value(backgroundProcessKey{}).(*os.Process)
	if !ok {
		return fmt.Errorf("no background process was started")
	}
	return p.Kill()
}

func theErrorOutputNamesTheBackgroundProcess(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	p, ok := ctx.Value(backgroundProcessKey{}).(*os.Process)
	if !ok {
		return fmt.Errorf("no background process was started")
	}
	want := fmt.Sprintf("pid %d", p.Pid)
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("error output does not contain %q:\n%s", want, s.stderr)
	}
	return nil
}
