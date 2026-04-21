package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
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

// aWorkspaceExistsWithBody writes a workspace directory whose
// .niwa/workspace.toml contains the supplied TOML body verbatim. Used by
// scenarios that need to exercise specific config shapes (e.g., the
// deprecated [content] key vs the canonical [claude.content]). The
// Gherkin form uses a docstring:
//
//	Given a workspace "myws" exists with body:
//	  """
//	  [workspace]
//	  name = "myws"
//	  [content.workspace]
//	  source = "ws.md"
//	  """
func aWorkspaceExistsWithBody(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	wsDir := filepath.Join(s.workspaceRoot, name)
	niwaDir := filepath.Join(wsDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating workspace dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(body.Content), 0o644); err != nil {
		return ctx, fmt.Errorf("writing workspace.toml: %w", err)
	}
	return ctx, nil
}

// shellInitContains asserts that `niwa shell-init <shell>` output contains
// the given text. Used to prove that the tsuku recipe's install_shell_init
// bake (which captures this output) includes the wrapper function and the
// cobra completion function -- both required for dynamic completion to
// work OOTB after `tsuku install`.
func shellInitContains(ctx context.Context, shell, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	cmd := exec.CommandContext(ctx, s.binPath, "shell-init", shell)
	cmd.Env = s.buildEnv()
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("running shell-init %s: %w", shell, err)
	}
	if !strings.Contains(string(out), text) {
		return fmt.Errorf("shell-init %s output does not contain %q\nactual output:\n%s",
			shell, text, string(out))
	}
	return nil
}

// iSourceShellInitAndRunCompletion simulates the install.sh runtime: writes
// an env file that evals `niwa shell-init auto` (same content install.sh
// produces), spawns a fresh login bash that sources it, and runs
// `niwa __complete <args> <prefix>` inside that shell. Output is captured
// into stdout/stderr/exit for downstream assertions. This proves the
// install.sh delivery chain (rc -> env -> eval -> wrapper + completion
// registered -> __complete dispatches).
func iSourceShellInitAndRunCompletion(ctx context.Context, command, prefix string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}

	// Place the binary on PATH the same way install.sh does ($HOME/.niwa/bin).
	binDir := filepath.Join(s.homeDir, ".niwa", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir bin: %w", err)
	}
	installedBin := filepath.Join(binDir, "niwa")
	_ = os.Remove(installedBin)
	if err := os.Symlink(s.binPath, installedBin); err != nil {
		return ctx, fmt.Errorf("symlinking niwa: %w", err)
	}

	// Build the args for __complete.
	tokens := strings.Fields(command)
	args := append([]string{"__complete"}, tokens...)
	args = append(args, prefix)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}

	// The env file mirrors install.sh's ENV_FILE content.
	envFile := filepath.Join(s.homeDir, ".niwa", "env")
	envContent := fmt.Sprintf(`# niwa shell configuration
export PATH=%q:"$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "$(niwa shell-init auto 2>/dev/null)"
fi
`, binDir)
	if err := os.WriteFile(envFile, []byte(envContent), 0o644); err != nil {
		return ctx, fmt.Errorf("writing env file: %w", err)
	}

	// Source the env file in a fresh bash, then run __complete.
	script := fmt.Sprintf(`set +e
. %q
niwa %s
__rc=$?
exit $__rc
`, envFile, strings.Join(quoted, " "))

	cmd := exec.CommandContext(ctx, "bash", "-c", script)
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
			return ctx, nil
		}
		return ctx, fmt.Errorf("install-sourced completion failed: %w\nstderr: %s", err, s.stderr)
	}
	s.exitCode = 0
	return ctx, nil
}

// aRegisteredWorkspaceExists creates the workspace directory AND adds a
// matching entry to the scenario's sandboxed global config at
// $HOME/.config/niwa/config.toml. Completion tests rely on the registry
// being present because completeWorkspaceNames reads it via
// config.ListRegisteredWorkspaces.
func aRegisteredWorkspaceExists(ctx context.Context, name string) (context.Context, error) {
	ctx, err := aWorkspaceExists(ctx, name)
	if err != nil {
		return ctx, err
	}
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cfgDir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating config dir: %w", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.toml")
	wsRoot := filepath.Join(s.workspaceRoot, name)
	entry := fmt.Sprintf("\n[registry.%q]\nsource = %q\nroot = %q\n",
		name, filepath.Join(wsRoot, ".niwa", "workspace.toml"), wsRoot)
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return ctx, fmt.Errorf("opening global config: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(entry); err != nil {
		return ctx, fmt.Errorf("appending registry entry: %w", err)
	}
	return ctx, nil
}

// aWorkspaceInstanceExists creates a workspace instance directory at
// <workspaceRoot>/<workspaceName>/<instanceName>/ with a valid
// .niwa/instance.json state file. Optionally creates repo subdirs of the
// form <group>/<repo> under the instance. `reposSpec` is a comma-separated
// list like "group-a/api,group-a/web,group-b/sdk". Empty reposSpec skips
// repo creation.
func aWorkspaceInstanceExistsWithRepos(ctx context.Context, workspaceName, instanceName, reposSpec string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instanceRoot := filepath.Join(s.workspaceRoot, workspaceName, instanceName)
	if err := os.MkdirAll(filepath.Join(instanceRoot, ".niwa"), 0o755); err != nil {
		return ctx, fmt.Errorf("creating instance dir: %w", err)
	}
	// Minimal instance.json. instance_number is derived heuristically from
	// the tail of the instance name (e.g., "alpha" -> 1, "alpha-2" -> 2).
	instanceNumber := 1
	if idx := strings.LastIndexByte(instanceName, '-'); idx >= 0 {
		if n, err := strconv.Atoi(instanceName[idx+1:]); err == nil {
			instanceNumber = n
		}
	}
	stateJSON := fmt.Sprintf(`{"schema_version":1,"config_name":null,"instance_name":%q,"instance_number":%d,"root":%q,"created":"2024-01-01T00:00:00Z","last_applied":"2024-01-01T00:00:00Z","managed_files":[],"repos":{}}`,
		instanceName, instanceNumber, instanceRoot)
	if err := os.WriteFile(filepath.Join(instanceRoot, ".niwa", "instance.json"), []byte(stateJSON), 0o644); err != nil {
		return ctx, fmt.Errorf("writing instance.json: %w", err)
	}
	if reposSpec == "" {
		return ctx, nil
	}
	for _, spec := range strings.Split(reposSpec, ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		repoDir := filepath.Join(instanceRoot, filepath.FromSlash(spec))
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			return ctx, fmt.Errorf("creating repo dir %q: %w", spec, err)
		}
	}
	return ctx, nil
}

// iRunCompletion runs `niwa __complete <tokens...> <prefix>` and captures
// output into stdout/stderr/exitCode. Wrapping __complete in its own step
// avoids argument-quoting headaches in Gherkin: the prefix is passed as a
// separate quoted table cell so empty strings ("") round-trip correctly.
func iRunCompletion(ctx context.Context, command, prefix string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	tokens := strings.Fields(command)
	args := append([]string{"__complete"}, tokens...)
	args = append(args, prefix)

	cmd := exec.CommandContext(ctx, s.binPath, args...)
	cmd.Dir = s.homeDir
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
			return ctx, nil
		}
		return ctx, fmt.Errorf("completion command failed: %w", err)
	}
	s.exitCode = 0
	return ctx, nil
}

// iRunCompletionFromInstance is iRunCompletion but with cwd set to a
// specific workspace instance so closures that discover the current
// instance have a realistic context.
func iRunCompletionFromInstance(ctx context.Context, command, prefix, workspaceName, instanceName string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, workspaceName, instanceName)
	tokens := strings.Fields(command)
	args := append([]string{"__complete"}, tokens...)
	args = append(args, prefix)

	cmd := exec.CommandContext(ctx, s.binPath, args...)
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
			return ctx, nil
		}
		return ctx, fmt.Errorf("completion command failed: %w", err)
	}
	s.exitCode = 0
	return ctx, nil
}

// completionSuggestions parses cobra's __complete stdout, returning candidate
// names with TAB-separated descriptions stripped. Drops the ":<directive>"
// trailer and the "Completion ended with directive:" line that cobra
// unconditionally emits.
func completionSuggestions(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if strings.HasPrefix(line, "Completion ended with directive:") {
			continue
		}
		if idx := strings.IndexByte(line, '\t'); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return out
}

// theCompletionOutputContains asserts that one of the parsed candidate names
// equals the expected text.
func theCompletionOutputContains(ctx context.Context, text string) error {
	s := getState(ctx)
	for _, line := range completionSuggestions(s.stdout) {
		if line == text {
			return nil
		}
	}
	return fmt.Errorf("expected completion candidate %q, got candidates:\n%v\nraw stdout:\n%s",
		text, completionSuggestions(s.stdout), s.stdout)
}

// theCompletionOutputDoesNotContain asserts that none of the parsed
// candidate names equals the forbidden text.
func theCompletionOutputDoesNotContain(ctx context.Context, text string) error {
	s := getState(ctx)
	for _, line := range completionSuggestions(s.stdout) {
		if line == text {
			return fmt.Errorf("expected completion not to include %q, got candidates:\n%v",
				text, completionSuggestions(s.stdout))
		}
	}
	return nil
}

// theCompletionDescriptionMatches asserts that the candidate `name` appears
// in the output with the given TAB-separated description. Useful for
// verifying the `repo in <N>` and `workspace` decorations.
func theCompletionDescriptionMatches(ctx context.Context, name, description string) error {
	s := getState(ctx)
	want := name + "\t" + description
	for _, line := range strings.Split(s.stdout, "\n") {
		if line == want {
			return nil
		}
	}
	return fmt.Errorf("expected decorated candidate %q, stdout:\n%s", want, s.stdout)
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

// theErrorOutputDoesNotContainAnsiEscapeSequence asserts that the last
// command's stderr contains no ANSI escape sequences (byte 0x1b / ESC).
// Use this to verify that --no-progress output is plain text with no
// terminal control codes.
func theErrorOutputDoesNotContainAnsiEscapeSequence(ctx context.Context) error {
	s := getState(ctx)
	if strings.Contains(s.stderr, "\x1b") {
		return fmt.Errorf("expected stderr to contain no ANSI escape sequences (0x1b), got:\n%s", s.stderr)
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

// --- Local git server steps ---

// aLocalGitServerIsSetUp is a no-op — the localGitServer is initialized in
// the Before hook. This step exists so scenarios read naturally.
func aLocalGitServerIsSetUp(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.gitServer == nil {
		return ctx, fmt.Errorf("no git server in test state")
	}
	return ctx, nil
}

// aConfigRepoExistsWithBody creates a bare repo with a committed workspace.toml
// and stores its file:// URL in state keyed by name. Before creating the repo,
// it substitutes {repo:<name>} placeholders in the body with the stored URL for
// that repo, allowing feature files to reference dynamic file:// URLs.
func aConfigRepoExistsWithBody(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	content := body.Content
	for repoName, repoURL := range s.repoURLs {
		content = strings.ReplaceAll(content, "{repo:"+repoName+"}", repoURL)
	}
	url, err := s.gitServer.ConfigRepo(name, content)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

// anOverlayRepoExistsWithBody creates a bare repo with a committed
// workspace-overlay.toml and stores its file:// URL in state keyed by name.
func anOverlayRepoExistsWithBody(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, err := s.gitServer.OverlayRepo(name, body.Content)
	if err != nil {
		return ctx, fmt.Errorf("creating overlay repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

// aPersonalOverlayExistsWithBody writes the given TOML content to
// $XDG_CONFIG_HOME/niwa/global/niwa.toml so niwa treats it as the personal
// global overlay for the scenario. The file is written inside the sandboxed
// homeDir, so it is isolated between scenarios.
func aPersonalOverlayExistsWithBody(ctx context.Context, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa", "global")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating personal overlay dir: %w", err)
	}
	path := filepath.Join(dir, "niwa.toml")
	if err := os.WriteFile(path, []byte(body.Content), 0o644); err != nil {
		return ctx, fmt.Errorf("writing personal overlay: %w", err)
	}
	return ctx, nil
}

// aSourceRepoExists creates a bare repo and stores its file:// URL in state.
func aSourceRepoExists(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, err := s.gitServer.Repo(name)
	if err != nil {
		return ctx, fmt.Errorf("creating source repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

// iRunNiwaInitFromConfigRepo runs niwa init --from <url> from workspaceRoot so
// that the workspace root (and subsequent instance directories) land under the
// sandboxed workspaces directory rather than homeDir.
func iRunNiwaInitFromConfigRepo(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa init --from "+url)
}

// iRunNiwaInitFromConfigRepoWithOverlay runs niwa init --from <url> --overlay
// <overlay-url> from workspaceRoot.
func iRunNiwaInitFromConfigRepoWithOverlay(ctx context.Context, name, overlayName string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	overlayURL, ok := s.repoURLs[overlayName]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for overlay repo %q", overlayName)
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa init --from "+url+" --overlay "+overlayURL)
}

// theInstanceExists asserts that <workspaceRoot>/<name> is a directory.
func theInstanceExists(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, name)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("instance %q does not exist at %s: %w", name, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("expected %s to be a directory", dir)
	}
	return nil
}

// theInstanceDoesNotExist asserts that <workspaceRoot>/<name> is absent.
func theInstanceDoesNotExist(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("instance directory %q exists but should not", dir)
	}
	return nil
}

// theRepoExistsInInstance asserts that <workspaceRoot>/<instance>/<group>/<repo> is a directory.
func theRepoExistsInInstance(ctx context.Context, groupRepo, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, instance, filepath.FromSlash(groupRepo))
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("repo %q in instance %q does not exist at %s: %w", groupRepo, instance, dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("expected %s to be a directory", dir)
	}
	return nil
}

// iWriteFileToRepoInInstance writes content to a file at the given relative
// path inside a managed repo directory. The repo is identified as
// "<group>/<repo>" (forward-slash separated); the file is created relative to
// the repo root. Use this to plant files (e.g. .env.example) after
// `niwa create` has cloned the repo, without needing them committed upstream.
func iWriteFileToRepoInInstance(ctx context.Context, content, relFilePath, groupRepo, instanceName string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	repoDir := filepath.Join(s.workspaceRoot, instanceName, filepath.FromSlash(groupRepo))
	dst := filepath.Join(repoDir, filepath.FromSlash(relFilePath))
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		return ctx, fmt.Errorf("writing %s: %w", dst, err)
	}
	return ctx, nil
}
// noNiwaTempFilesRemain scans the scenario's scoped TMPDIR for wrapper
// leftovers. TMPDIR is set to s.tmpDir in buildEnv, so the wrapper's
// `mktemp` creates files there; its `rm -f` should clean them up. Any
// remaining entry in s.tmpDir after the wrapped command ran indicates a
// missing or failed cleanup in the wrapper.
// aForeignDirectoryExistsAtInstancePath creates an empty directory at
// workspaceRoot/name without a .niwa/instance.json, simulating a leftover or
// foreign directory that blocks the numbered instance slot.
func aForeignDirectoryExistsAtInstancePath(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating foreign directory %s: %w", dir, err)
	}
	return ctx, nil
}

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

// --- File assertion steps ---

// theFileExistsInInstance verifies that a file exists at relPath within the
// named instance directory.
func theFileExistsInInstance(ctx context.Context, relPath, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, instance, relPath)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("expected file %q to exist in instance %q", relPath, instance)
	} else if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	return nil
}

// theFileInInstanceContains verifies that the file at relPath within the named
// instance contains text.
func theFileInInstanceContains(ctx context.Context, relPath, instance, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, instance, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %q in instance %q: %w", relPath, instance, err)
	}
	if !strings.Contains(string(data), text) {
		return fmt.Errorf("file %q does not contain %q\ncontent:\n%s", path, text, string(data))
	}
	return nil
}

// theFileDoesNotExistInInstance verifies that a file does not exist at relPath
// within the named instance directory.
func theFileDoesNotExistInInstance(ctx context.Context, relPath, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, instance, relPath)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("expected file %q to not exist in instance %q, but it does", relPath, instance)
	}
	return nil
}

// theFileInInstanceDoesNotContain verifies the file at relPath does not contain text.
func theFileInInstanceDoesNotContain(ctx context.Context, relPath, instance, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, instance, relPath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // absent file trivially does not contain the text
	}
	if err != nil {
		return fmt.Errorf("reading %q in instance %q: %w", relPath, instance, err)
	}
	if strings.Contains(string(data), text) {
		return fmt.Errorf("file %q should not contain %q\ncontent:\n%s", path, text, string(data))
	}
	return nil
}

// --- Claude integration steps ---

// claudeIsAvailable checks that the claude CLI and ANTHROPIC_API_KEY are
// available. Returns godog.ErrPending to skip the scenario when either is absent.
func claudeIsAvailable(ctx context.Context) (context.Context, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return ctx, godog.ErrPending
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return ctx, godog.ErrPending
	}
	return ctx, nil
}

// iRunClaudePFromInstanceRoot runs claude -p with the given prompt from the
// named instance's workspace root and records output into state.
func iRunClaudePFromInstanceRoot(ctx context.Context, instance string, prompt *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, instance)
	return ctx, runClaudeP(s, cwd, strings.TrimSpace(prompt.Content))
}

// iRunClaudePFromRepoInInstance runs claude -p with the given prompt from
// groupRepo (e.g. "tools/myapp") inside the named instance.
func iRunClaudePFromRepoInInstance(ctx context.Context, groupRepo, instance string, prompt *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, instance, groupRepo)
	return ctx, runClaudeP(s, cwd, strings.TrimSpace(prompt.Content))
}

// runClaudeP executes claude -p <prompt> in cwd with a sandboxed environment.
// stdout is stored lowercased so callers can assert "yes"/"no" without caring
// about capitalisation. Returns godog.ErrPending if claude is not on PATH.
func runClaudeP(s *testState, cwd, prompt string) error {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		return godog.ErrPending
	}
	env := s.buildEnv()
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		env = append(env, "ANTHROPIC_API_KEY="+key)
	}
	cmd := exec.Command(claudeBin, "-p", prompt)
	cmd.Dir = cwd
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	s.stdout = strings.ToLower(stdout.String())
	s.stderr = stderr.String()
	s.shellPwd = ""
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("claude -p failed: %w\nstderr: %s", runErr, s.stderr)
	}
	s.exitCode = 0
	return nil
}

// --- Mesh / session steps ---

// meshState holds per-scenario mesh state: session IDs keyed by role.
type meshState struct {
	sessionIDs   map[string]string // role → session ID
	instanceRoot string
}

type meshStateKeyType struct{}

var meshStateKey = meshStateKeyType{}

func getMeshState(ctx context.Context) *meshState {
	if ms, ok := ctx.Value(meshStateKey).(*meshState); ok {
		return ms
	}
	return nil
}

func setMeshState(ctx context.Context, ms *meshState) context.Context {
	return context.WithValue(ctx, meshStateKey, ms)
}

// niwaInstanceRootIsSetToATempDirectory creates a temp directory and sets
// NIWA_INSTANCE_ROOT in the scenario env overrides, also storing it in
// meshState for assertions that need the path.
func niwaInstanceRootIsSetToATempDirectory(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.tmpDir, "mesh-instance")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating instance root: %w", err)
	}
	s.envOverrides["NIWA_INSTANCE_ROOT"] = dir

	ms := &meshState{
		sessionIDs:   make(map[string]string),
		instanceRoot: dir,
	}
	return setMeshState(ctx, ms), nil
}

// iRunNiwaSessionRegisterAsRole runs "niwa session register" with
// NIWA_SESSION_ROLE set to the given role. It captures the session ID
// from the output and stores it in meshState.
func iRunNiwaSessionRegisterAsRole(ctx context.Context, role string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}

	// Override NIWA_SESSION_ROLE for this invocation.
	saved := s.envOverrides["NIWA_SESSION_ROLE"]
	s.envOverrides["NIWA_SESSION_ROLE"] = role
	defer func() {
		if saved == "" {
			delete(s.envOverrides, "NIWA_SESSION_ROLE")
		} else {
			s.envOverrides["NIWA_SESSION_ROLE"] = saved
		}
	}()

	if err := runNiwa(s, s.homeDir, "niwa session register"); err != nil {
		return ctx, err
	}

	// Parse session_id from stdout: "session_id=<uuid> role=<role>"
	sessionID := ""
	for _, line := range strings.Split(s.stdout, "\n") {
		if strings.HasPrefix(line, "session_id=") {
			parts := strings.Fields(line)
			if len(parts) >= 1 {
				sessionID = strings.TrimPrefix(parts[0], "session_id=")
			}
		}
	}
	if sessionID == "" {
		return ctx, fmt.Errorf("no session_id in output: %q", s.stdout)
	}

	ms := getMeshState(ctx)
	if ms == nil {
		ms = &meshState{sessionIDs: make(map[string]string)}
		ctx = setMeshState(ctx, ms)
	}
	ms.sessionIDs[role] = sessionID
	return ctx, nil
}

// aSessionsJSONEntryExistsForRole asserts that sessions.json in the
// instance root contains an entry for the given role.
func aSessionsJSONEntryExistsForRole(ctx context.Context, role string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	ms := getMeshState(ctx)
	if ms == nil {
		return fmt.Errorf("no mesh state; call NIWA_INSTANCE_ROOT setup first")
	}
	jsonPath := filepath.Join(ms.instanceRoot, ".niwa", "sessions", "sessions.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("reading sessions.json: %w", err)
	}
	// sessions.json is written with MarshalIndent so spacing varies; match
	// both compact and pretty-printed forms.
	found := strings.Contains(string(data), `"role":"`+role+`"`) ||
		strings.Contains(string(data), `"role": "`+role+`"`)
	if !found {
		return fmt.Errorf("sessions.json does not contain role %q:\n%s", role, string(data))
	}
	return nil
}

// theInboxDirectoryExistsForRole asserts that the inbox directory for
// the given role's session exists.
func theInboxDirectoryExistsForRole(ctx context.Context, role string) error {
	ms := getMeshState(ctx)
	if ms == nil {
		return fmt.Errorf("no mesh state")
	}
	sessionID, ok := ms.sessionIDs[role]
	if !ok {
		return fmt.Errorf("no session ID recorded for role %q", role)
	}
	inboxDir := filepath.Join(ms.instanceRoot, ".niwa", "sessions", sessionID, "inbox")
	info, err := os.Stat(inboxDir)
	if err != nil {
		return fmt.Errorf("inbox directory for role %q (session %s) does not exist: %w", role, sessionID, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("expected %s to be a directory", inboxDir)
	}
	return nil
}

// callMCPTool calls a single MCP tool on the niwa mcp-serve command by
// piping a JSON-RPC initialize + tools/call sequence to stdin and
// capturing stdout. Returns the raw JSON-RPC response bytes.
func callMCPTool(s *testState, sessionID, sessionRole, toolName, argsJSON string) (string, int, error) {
	instanceRoot := s.envOverrides["NIWA_INSTANCE_ROOT"]
	if instanceRoot == "" {
		return "", 0, fmt.Errorf("NIWA_INSTANCE_ROOT not set")
	}
	sessionsDir := instanceRoot + "/.niwa/sessions"
	inboxDir := ""
	if sessionID != "" {
		inboxDir = sessionsDir + "/" + sessionID + "/inbox"
	}

	// Build the JSON-RPC sequence:
	// 1. initialize request
	// 2. notifications/initialized notification
	// 3. tools/call request
	// We use id=1 for initialize and id=2 for tools/call.
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + toolName + `","arguments":` + argsJSON + `}}` + "\n"

	env := s.buildEnv()
	// Override for this specific session.
	envMap := make(map[string]string)
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx >= 0 {
			envMap[kv[:idx]] = kv[idx+1:]
		}
	}
	envMap["NIWA_INSTANCE_ROOT"] = instanceRoot
	envMap["NIWA_SESSION_ID"] = sessionID
	envMap["NIWA_SESSION_ROLE"] = sessionRole
	envMap["NIWA_INBOX_DIR"] = inboxDir

	var envSlice []string
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	cmd := exec.Command(s.binPath, "mcp-serve")
	cmd.Env = envSlice
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			_ = exitCode
		} else {
			return "", 0, fmt.Errorf("mcp-serve failed: %w\nstderr: %s", err, stderr.String())
		}
	}
	return stdout.String(), 0, nil
}

// theWorkerSessionSendsAMessageToWithBody sends a typed message from the
// worker session to the coordinator session using the MCP niwa_send_message tool.
func theWorkerSessionSendsAMessageToWithBody(ctx context.Context, msgType, targetRole, body string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	ms := getMeshState(ctx)
	if ms == nil {
		return ctx, fmt.Errorf("no mesh state")
	}
	workerSessionID := ms.sessionIDs["worker"]
	argsJSON := `{"to":"` + targetRole + `","type":"` + msgType + `","body":{"text":"` + body + `"}}`
	out, _, err := callMCPTool(s, workerSessionID, "worker", "niwa_send_message", argsJSON)
	if err != nil {
		return ctx, err
	}
	s.stdout = out
	s.exitCode = 0
	return ctx, nil
}

// theCoordinatorInboxContainsNMessages asserts that the coordinator's
// inbox directory contains exactly n message files (excluding subdirectories).
func theCoordinatorInboxContainsNMessages(ctx context.Context, n int) error {
	ms := getMeshState(ctx)
	if ms == nil {
		return fmt.Errorf("no mesh state")
	}
	sessionID, ok := ms.sessionIDs["coordinator"]
	if !ok {
		return fmt.Errorf("no session ID for coordinator")
	}
	inboxDir := filepath.Join(ms.instanceRoot, ".niwa", "sessions", sessionID, "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		return fmt.Errorf("reading coordinator inbox: %w", err)
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	if count != n {
		return fmt.Errorf("coordinator inbox contains %d message(s), want %d", count, n)
	}
	return nil
}

// theCoordinatorSessionChecksMessages calls niwa_check_messages as the
// coordinator session and records the output.
func theCoordinatorSessionChecksMessages(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	ms := getMeshState(ctx)
	if ms == nil {
		return ctx, fmt.Errorf("no mesh state")
	}
	coordinatorSessionID := ms.sessionIDs["coordinator"]
	out, _, err := callMCPTool(s, coordinatorSessionID, "coordinator", "niwa_check_messages", "{}")
	if err != nil {
		return ctx, err
	}
	s.stdout = out
	s.exitCode = 0
	return ctx, nil
}
