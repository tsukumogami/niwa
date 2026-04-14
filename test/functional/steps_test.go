package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// aCleanNiwaEnvironment is a no-op. The Before hook already sets up a fresh
// sandbox; this step exists so feature files read naturally.
func aCleanNiwaEnvironment(ctx context.Context) (context.Context, error) {
	return ctx, nil
}

// aWorkspaceExists creates a workspace directory with a minimal
// .niwa/workspace.toml at <workspaceRoot>/<name>. It does not register the
// workspace in the global config — use aRegisteredWorkspaceExists for that.
func aWorkspaceExists(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	wsDir := filepath.Join(s.workspaceRoot, name)
	niwaDir := filepath.Join(wsDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating workspace dir: %w", err)
	}
	cfg := fmt.Sprintf("[workspace]\nname = \"%s\"\n", name)
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(cfg), 0o644); err != nil {
		return ctx, fmt.Errorf("writing workspace.toml: %w", err)
	}
	return ctx, nil
}

// iSetEnv stores a per-scenario env var that will be applied to subsequent
// command invocations.
func iSetEnv(ctx context.Context, key, value string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides[key] = value
	return ctx, nil
}

// iSetEnvToTempPath sets an env var to a freshly-created path under the
// scenario's scoped TMPDIR. Used when the test needs a valid
// NIWA_RESPONSE_FILE path but doesn't care about its exact value. The
// file is created inside the per-scenario sandbox so it's automatically
// cleaned up by the next Before hook.
func iSetEnvToTempPath(ctx context.Context, key string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	f, err := os.CreateTemp(s.tmpDir, "response-*")
	if err != nil {
		return ctx, fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Truncate(path, 0)
	s.envOverrides[key] = path
	return ctx, nil
}

// buildEnv returns the environment for invoking the niwa binary. It overrides
// HOME, XDG_CONFIG_HOME, and TMPDIR to the sandbox so config, state, and
// temp files don't leak across scenarios or into the real user environment.
// Per-scenario overrides win last.
func (s *testState) buildEnv() []string {
	base := os.Environ()
	filtered := base[:0]
	for _, kv := range base {
		if strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "XDG_CONFIG_HOME=") ||
			strings.HasPrefix(kv, "TMPDIR=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	env := append(filtered,
		"HOME="+s.homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(s.homeDir, ".config"),
		"TMPDIR="+s.tmpDir,
	)
	for k, v := range s.envOverrides {
		env = append(env, k+"="+v)
	}
	return env
}

// runNiwa executes the test binary with the given args from cwd and records
// stdout/stderr/exit code in state. Replaces the literal "niwa" token at the
// start of `command` with the test binary path so scenarios read naturally.
func runNiwa(s *testState, cwd, command string) error {
	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = s.buildEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	s.stdout = stdout.String()
	s.stderr = stderr.String()
	s.shellPwd = ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("command execution failed: %w", err)
	}
	s.exitCode = 0
	return nil
}

func iRun(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, runNiwa(s, s.homeDir, command)
}

func iRunFromWorkspace(ctx context.Context, command, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, ".niwa")
	return ctx, runNiwa(s, cwd, command)
}

// iSourceWrapperAndRunFromWorkspace is the end-to-end shell-integration step.
// It writes a bash script that sources the wrapper, runs the command, then
// emits a sentinel line with the final pwd. This is the only way to verify
// that `builtin cd` actually fired in the wrapped shell — unit tests on the
// template string cannot catch a broken wrapper.
func iSourceWrapperAndRunFromWorkspace(ctx context.Context, command, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, ".niwa")
	return ctx, runWrappedShell(s, cwd, command)
}

func iSourceWrapperAndRun(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, runWrappedShell(s, s.homeDir, command)
}

// iSourceNoisyWrapperAndRunFromWorkspace is the regression test for the
// feature's primary motivation: under the old stdout-capture protocol, any
// subprocess that wrote to stdout broke navigation. We simulate that by
// placing a "niwa" shell script on PATH that emits stdout noise before
// exec'ing the real binary. Inside the wrapper function, `command niwa
// "$@"` picks up this script. With the temp-file protocol, the landing
// path arrives via NIWA_RESPONSE_FILE, so stdout noise is harmless.
func iSourceNoisyWrapperAndRunFromWorkspace(ctx context.Context, command, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	// The noisy script writes the kind of output that used to corrupt
	// navigation: git clone progress, verbose log lines.
	noisyDir := filepath.Join(s.homeDir, "noisy-bin")
	if err := os.MkdirAll(noisyDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir noisy-bin: %w", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "Cloning into 'fake-repo'..."
echo "remote: Enumerating objects: 1234, done."
exec %q "$@"
`, s.binPath)
	noisyPath := filepath.Join(noisyDir, "niwa")
	if err := os.WriteFile(noisyPath, []byte(script), 0o755); err != nil {
		return ctx, fmt.Errorf("writing noisy niwa script: %w", err)
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, ".niwa")
	return ctx, runWrappedShellWithPATH(s, cwd, command, noisyDir)
}

// runWrappedShell invokes bash, sources the niwa wrapper, runs the given
// command, then prints `__NIWA_SHELL_PWD=<pwd>` so we can read the final
// directory out-of-band from the command's own output. Any error from the
// command propagates through the shell's exit code.
func runWrappedShell(s *testState, cwd, command string) error {
	// The test binary lives outside $PATH, but the wrapper calls bare `niwa`
	// (which resolves to the wrapper function on the first hop and `command
	// niwa "$@"` on the second — that inner call needs `niwa` on PATH).
	// Symlink our test binary into a scenario-local bin dir and prepend it.
	linkDir := filepath.Join(s.homeDir, "bin-link")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return fmt.Errorf("mkdir bin-link: %w", err)
	}
	link := filepath.Join(linkDir, "niwa")
	_ = os.Remove(link)
	if err := os.Symlink(s.binPath, link); err != nil {
		return fmt.Errorf("symlinking test binary: %w", err)
	}
	return runWrappedShellWithPATH(s, cwd, command, linkDir)
}

// runWrappedShellWithPATH is the shared shell-invocation worker. pathPrefix
// is prepended to $PATH before the wrapper is sourced; runWrappedShell uses
// a symlink directory, while the noisy-wrapper scenario passes a directory
// containing a script that adds stdout noise before exec'ing the real niwa.
func runWrappedShellWithPATH(s *testState, cwd, command, pathPrefix string) error {
	s.shellStartPwd = cwd
	// Source the wrapper via the real binary's absolute path — this ensures
	// `shell-init` output isn't polluted by any niwa stand-in on PATH (e.g.,
	// the noisy-wrapper scenario). AFTER the wrapper function is loaded,
	// prepend pathPrefix so subsequent bare `niwa` calls through the wrapper
	// hit whatever pathPrefix provides.
	script := fmt.Sprintf(`set +e
eval "$(%q shell-init bash)"
export PATH=%q:"$PATH"
cd %q
%s
__rc=$?
printf '__NIWA_SHELL_PWD=%%s\n' "$(pwd)" >&2
exit $__rc
`, s.binPath, pathPrefix, cwd, command)

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = s.buildEnv()
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	s.stdout = stdout.String()
	// Strip the sentinel from stderr before exposing it to step assertions.
	stderrStr := stderr.String()
	if idx := strings.Index(stderrStr, "__NIWA_SHELL_PWD="); idx >= 0 {
		tail := stderrStr[idx+len("__NIWA_SHELL_PWD="):]
		nl := strings.IndexByte(tail, '\n')
		if nl < 0 {
			nl = len(tail)
		}
		s.shellPwd = strings.TrimSpace(tail[:nl])
		s.stderr = stderrStr[:idx] + stderrStr[idx+len("__NIWA_SHELL_PWD=")+nl:]
	} else {
		s.stderr = stderrStr
		s.shellPwd = ""
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("wrapped shell failed: %w\nstderr: %s", err, s.stderr)
	}
	s.exitCode = 0
	return nil
}

// --- Assertions ---

func theExitCodeIs(ctx context.Context, expected int) error {
	s := getState(ctx)
	if s.exitCode != expected {
		return fmt.Errorf("expected exit code %d, got %d\nstdout: %s\nstderr: %s",
			expected, s.exitCode, s.stdout, s.stderr)
	}
	return nil
}

func theExitCodeIsNot(ctx context.Context, notExpected int) error {
	s := getState(ctx)
	if s.exitCode == notExpected {
		return fmt.Errorf("expected exit code to not be %d\nstdout: %s\nstderr: %s",
			notExpected, s.stdout, s.stderr)
	}
	return nil
}

func theOutputContains(ctx context.Context, text string) error {
	s := getState(ctx)
	if !strings.Contains(s.stdout, text) {
		return fmt.Errorf("expected stdout to contain %q, got:\n%s", text, s.stdout)
	}
	return nil
}

func theOutputDoesNotContain(ctx context.Context, text string) error {
	s := getState(ctx)
	if strings.Contains(s.stdout, text) {
		return fmt.Errorf("expected stdout not to contain %q, got:\n%s", text, s.stdout)
	}
	return nil
}

func theOutputEquals(ctx context.Context, text string) error {
	s := getState(ctx)
	got := strings.TrimRight(s.stdout, "\n")
	if got != text {
		return fmt.Errorf("expected stdout to equal %q, got %q", text, got)
	}
	return nil
}

func theOutputIsEmpty(ctx context.Context) error {
	s := getState(ctx)
	if len(s.stdout) != 0 {
		return fmt.Errorf("expected stdout to be empty, got:\n%s", s.stdout)
	}
	return nil
}

func theErrorOutputContains(ctx context.Context, text string) error {
	s := getState(ctx)
	if !strings.Contains(s.stderr, text) {
		return fmt.Errorf("expected stderr to contain %q, got:\n%s", text, s.stderr)
	}
	return nil
}

func theErrorOutputDoesNotContain(ctx context.Context, text string) error {
	s := getState(ctx)
	if strings.Contains(s.stderr, text) {
		return fmt.Errorf("expected stderr not to contain %q, got:\n%s", text, s.stderr)
	}
	return nil
}

// theResponseFileContainsWorkspace reads NIWA_RESPONSE_FILE from envOverrides
// and asserts its content is the absolute path to the named workspace (with
// a trailing newline — that's the format writeLandingPath produces).
func theResponseFileContainsWorkspace(ctx context.Context, workspace string) error {
	s := getState(ctx)
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return fmt.Errorf("NIWA_RESPONSE_FILE not set in this scenario")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading response file %s: %w", path, err)
	}
	want := filepath.Join(s.workspaceRoot, workspace) + "\n"
	if string(data) != want {
		return fmt.Errorf("response file content mismatch:\n  want: %q\n  got:  %q", want, string(data))
	}
	return nil
}

func theResponseFileDoesNotExist(ctx context.Context) error {
	s := getState(ctx)
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		data, _ := os.ReadFile(path)
		return fmt.Errorf("expected response file %s to not exist, but it contains: %q", path, string(data))
	}
	return nil
}

func theWrappedShellEndedInWorkspace(ctx context.Context, workspace string) error {
	s := getState(ctx)
	want := filepath.Join(s.workspaceRoot, workspace)
	if s.shellPwd != want {
		return fmt.Errorf("wrapped shell ended in %q, want %q", s.shellPwd, want)
	}
	return nil
}

func theWrappedShellDidNotChangeDirectory(ctx context.Context) error {
	s := getState(ctx)
	if s.shellPwd == "" {
		return fmt.Errorf("no wrapped shell recorded — did the scenario run a wrapped command?")
	}
	// Compare against the cwd the wrapped shell started in. /tmp symlinks
	// to /private/tmp on macOS — resolve both sides before comparing.
	got, _ := filepath.EvalSymlinks(s.shellPwd)
	want, _ := filepath.EvalSymlinks(s.shellStartPwd)
	if got == "" {
		got = s.shellPwd
	}
	if want == "" {
		want = s.shellStartPwd
	}
	if got != want {
		return fmt.Errorf("wrapped shell changed directory to %q; expected to stay in %q", s.shellPwd, s.shellStartPwd)
	}
	return nil
}

// noNiwaTempFilesRemain scans the scenario's scoped TMPDIR for wrapper
// leftovers. TMPDIR is set to s.tmpDir in buildEnv, so the wrapper's
// `mktemp` creates files there; its `rm -f` should clean them up. Any
// remaining entry in s.tmpDir after the wrapped command ran indicates a
// missing or failed cleanup in the wrapper.
func noNiwaTempFilesRemain(ctx context.Context) error {
	s := getState(ctx)
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return fmt.Errorf("scanning %s: %w", s.tmpDir, err)
	}
	if len(entries) == 0 {
		return nil
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return fmt.Errorf("expected %s to be empty after wrapped run; found %d leftover(s): %v", s.tmpDir, len(entries), names)
}
