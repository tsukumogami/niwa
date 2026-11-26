package functional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/cucumber/godog"
)

// The job-directory vet scenarios. niwa vets a session's job directory under
// ~/.claude/jobs/<short> right before every claude rm, and the pass and
// destroy vet every session they will remove before their first removal
// step. These steps build the cases a vet must keep on, against the fake
// claude the dispatch steps install (or the retask steps' copy-rule stub),
// wrapped so that:
//
//   - every call is logged, with the directory it ran from, to
//     $HOME/claude-calls;
//   - `agents` also lists the rows in $HOME/fake-claude-listed, so a session
//     whose job entry has no state record (liveness reads it as gone) can
//     still be listed;
//   - a `--bg` wake that starts a copy plants a link in the copy's job
//     directory when $HOME/plant-copy-link names a target.
//
// The outside directory each scenario points a link at lives outside the
// scenario's sandbox, and is compared file by file after the run.

// vetScenario is what one scenario remembers: the outside directory and its
// recorded contents, and the image a device scenario attached.
type vetScenario struct {
	outside  string
	recorded map[string]string
	detach   string
}

var (
	vetScenariosMu sync.Mutex
	vetScenarios   = map[*testState]*vetScenario{}
)

func vetState(ctx context.Context) (*testState, *vetScenario, error) {
	s := getState(ctx)
	if s == nil {
		return nil, nil, fmt.Errorf("no test state")
	}
	vetScenariosMu.Lock()
	defer vetScenariosMu.Unlock()
	vs, ok := vetScenarios[s]
	if !ok {
		vs = &vetScenario{}
		vetScenarios[s] = vs
		s.cleanups = append(s.cleanups, func() {
			vetScenariosMu.Lock()
			delete(vetScenarios, s)
			vetScenariosMu.Unlock()
		})
	}
	return s, vs, nil
}

const claudeWrapperScript = `#!/bin/sh
real=%q
printf '%%s|%%s\n' "$(pwd -P)" "$*" >> "$HOME/claude-calls"
listed="$HOME/fake-claude-listed"
case "$1" in
  agents)
    out=$("$real" "$@") || exit $?
    extra=""
    [ -f "$listed" ] && extra=$(paste -sd, "$listed")
    if [ -z "$extra" ]; then
      printf '%%s\n' "$out"
    elif [ "$out" = "[]" ]; then
      printf '[%%s]\n' "$extra"
    else
      printf '%%s,%%s]\n' "${out%%]}" "$extra"
    fi
    exit 0
    ;;
  rm)
    "$real" "$@"
    st=$?
    if [ "$st" -eq 0 ] && [ -f "$listed" ]; then
      grep -v "\"id\":\"$2\"" "$listed" > "$listed.next"
      mv "$listed.next" "$listed"
    fi
    exit "$st"
    ;;
  --bg)
    out=$("$real" "$@")
    st=$?
    printf '%%s\n' "$out"
    copy=$(printf '%%s' "$out" | sed -n 's/.*started a copy as \([0-9a-f]*\).*/\1/p')
    if [ -n "$copy" ] && [ -f "$HOME/plant-copy-link" ]; then
      mkdir -p "$HOME/.claude/jobs/$copy/scratch"
      ln -s "$(cat "$HOME/plant-copy-link")" "$HOME/.claude/jobs/$copy/scratch/out"
    fi
    exit "$st"
    ;;
esac
exec "$real" "$@"
`

// theFakeClaudeIsWrapped puts the wrapper first on PATH, over whichever
// fake claude the scenario installed.
func theFakeClaudeIsWrapped(ctx context.Context) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if s.pathPrefix == "" {
		return ctx, fmt.Errorf("no fake claude is installed to wrap")
	}
	real := filepath.Join(s.pathPrefix, "claude")
	dir := filepath.Join(s.sandbox, "claude-wrapper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, err
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(fmt.Sprintf(claudeWrapperScript, real)), 0o755); err != nil {
		return ctx, err
	}
	s.pathPrefix = dir
	return ctx, nil
}

func shortID(sessionID string) string {
	if len(sessionID) > 8 {
		return sessionID[:8]
	}
	return sessionID
}

func jobDirOf(s *testState, sessionID string) string {
	return filepath.Join(s.homeDir, ".claude", "jobs", shortID(sessionID))
}

// listSession adds the session to what the wrapped fake lists, stopped.
func listSession(s *testState, sessionID string) error {
	row, err := json.Marshal(map[string]string{"id": shortID(sessionID), "sessionId": sessionID, "kind": "background", "state": "done"})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.homeDir, "fake-claude-listed"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\n", row)
	return err
}

// theSessionIsGoneButStillListed removes the session's state record and its
// running marker, so liveness reads the session as gone, keeps its job
// directory, and has the wrapped fake list it.
func theSessionIsGoneButStillListed(ctx context.Context, sessionID string) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	dir := jobDirOf(s, sessionID)
	for _, name := range []string{"state.json", "running"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return ctx, err
		}
	}
	return ctx, listSession(s, sessionID)
}

// theSessionIsListedWithNoJobDirectory removes the session's job directory
// and has the wrapped fake list it anyway.
func theSessionIsListedWithNoJobDirectory(ctx context.Context, sessionID string) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if err := os.RemoveAll(jobDirOf(s, sessionID)); err != nil {
		return ctx, err
	}
	return ctx, listSession(s, sessionID)
}

// anOutsideDirectoryHoldsAMarker makes a directory outside the scenario's
// sandbox with a marker file in it.
func anOutsideDirectoryHoldsAMarker(ctx context.Context) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	dir, err := os.MkdirTemp("", "niwa-vet-outside-")
	if err != nil {
		return ctx, err
	}
	s.cleanups = append(s.cleanups, func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("must stay\n"), 0o600); err != nil {
		return ctx, err
	}
	vs.outside = dir
	return ctx, nil
}

// recordOutside records the outside directory's listing, sizes and hashes.
func recordOutside(vs *vetScenario) error {
	rec, err := snapshotDir(vs.outside)
	if err != nil {
		return err
	}
	vs.recorded = rec
	return nil
}

// snapshotDir maps every entry under dir to its kind, size and hash.
func snapshotDir(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			out[rel] = fmt.Sprintf("file %d %s", info.Size(), hex.EncodeToString(sum[:]))
		case info.IsDir():
			out[rel] = "dir"
		default:
			out[rel] = info.Mode().Type().String()
		}
		return nil
	})
	return out, err
}

// theOutsideDirectoryIsUnchanged compares the outside directory with what
// was recorded and checks the marker is still there.
func theOutsideDirectoryIsUnchanged(ctx context.Context) error {
	_, vs, err := vetState(ctx)
	if err != nil {
		return err
	}
	if vs.recorded == nil {
		return fmt.Errorf("the outside directory was never recorded")
	}
	now, err := snapshotDir(vs.outside)
	if err != nil {
		return err
	}
	var diffs []string
	for k, v := range vs.recorded {
		if now[k] != v {
			diffs = append(diffs, fmt.Sprintf("%s: %q -> %q", k, v, now[k]))
		}
	}
	for k, v := range now {
		if _, ok := vs.recorded[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: new %q", k, v))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		return fmt.Errorf("the outside directory changed:\n%s", strings.Join(diffs, "\n"))
	}
	if _, err := os.Stat(filepath.Join(vs.outside, "marker")); err != nil {
		return fmt.Errorf("the marker is gone: %w", err)
	}
	return nil
}

// replaceWithLink moves the directory at p into the outside directory under
// name, puts a link to it at p, and records the outside directory.
func replaceWithLink(s *testState, vs *vetScenario, p, name string) error {
	if vs.outside == "" {
		return fmt.Errorf("no outside directory")
	}
	target := filepath.Join(vs.outside, name)
	if err := os.Rename(p, target); err != nil {
		return err
	}
	if err := os.Symlink(target, p); err != nil {
		return err
	}
	return recordOutside(vs)
}

func theJobDirectoryIsALinkOutside(ctx context.Context, sessionID string) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, replaceWithLink(s, vs, jobDirOf(s, sessionID), "job")
}

func theJobsRootIsALinkOutside(ctx context.Context) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, replaceWithLink(s, vs, filepath.Join(s.homeDir, ".claude", "jobs"), "jobs")
}

func theConfigDirectoryIsALinkOutside(ctx context.Context) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, replaceWithLink(s, vs, filepath.Join(s.homeDir, ".claude"), "config")
}

// theClaudeConfigDirIsALinkOutside moves ~/.claude outside, sets
// CLAUDE_CONFIG_DIR to a link to it, and leaves ~/.claude a link too, since
// the fake claude always writes under ~/.claude.
func theClaudeConfigDirIsALinkOutside(ctx context.Context) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if err := replaceWithLink(s, vs, filepath.Join(s.homeDir, ".claude"), "config"); err != nil {
		return ctx, err
	}
	link := filepath.Join(s.sandbox, "claude-config-link")
	if err := os.Symlink(filepath.Join(vs.outside, "config"), link); err != nil {
		return ctx, err
	}
	s.envOverrides["CLAUDE_CONFIG_DIR"] = link
	return ctx, nil
}

func theJobDirectoryIsGroupWritable(ctx context.Context, sessionID string) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if err := os.Chmod(jobDirOf(s, sessionID), 0o775); err != nil {
		return ctx, err
	}
	if vs.outside != "" {
		return ctx, recordOutside(vs)
	}
	return ctx, nil
}

// theJobDirectoryIsOnAnotherDevice mounts a small disk image the user can
// attach without root at the job directory's path. Where that can't be
// done the scenario is skipped with the reason printed; the vet's unit
// tests cover the case through a stat seam either way.
func theJobDirectoryIsOnAnotherDevice(ctx context.Context, sessionID string) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	hdiutil, err := exec.LookPath("hdiutil")
	if err != nil {
		fmt.Fprintf(os.Stderr, "skipped: no hdiutil on this host, so no other filesystem can be attached without root\n")
		return ctx, godog.ErrSkip
	}
	img := filepath.Join(s.sandbox, "jobdir.dmg")
	if out, err := exec.Command(hdiutil, "create", "-quiet", "-size", "2m", "-fs", "HFS+", "-volname", "niwavet", img).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "skipped: hdiutil create failed: %v %s\n", err, out)
		return ctx, godog.ErrSkip
	}
	dir := jobDirOf(s, sessionID)
	if err := os.RemoveAll(dir); err != nil {
		return ctx, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return ctx, err
	}
	if out, err := exec.Command(hdiutil, "attach", "-quiet", "-nobrowse", "-mountpoint", dir, img).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "skipped: hdiutil attach failed: %v %s\n", err, out)
		return ctx, godog.ErrSkip
	}
	vs.detach = dir
	s.cleanups = append(s.cleanups, func() { _ = exec.Command(hdiutil, "detach", "-quiet", "-force", dir).Run() })
	if err := os.Chmod(dir, 0o700); err != nil {
		return ctx, err
	}
	var a, b syscall.Stat_t
	if err := syscall.Lstat(dir, &a); err != nil {
		return ctx, err
	}
	if err := syscall.Lstat(filepath.Dir(dir), &b); err != nil {
		return ctx, err
	}
	if a.Dev == b.Dev {
		fmt.Fprintf(os.Stderr, "skipped: the attached image shares the jobs root's device\n")
		return ctx, godog.ErrSkip
	}
	return ctx, nil
}

// theJobDirectoryIsOwnedByAnotherUID gives the job directory to another uid,
// which needs root; without it the scenario is skipped with the reason
// printed, and the vet's unit tests cover the case through a stat seam.
func theJobDirectoryIsOwnedByAnotherUID(ctx context.Context, sessionID string) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "skipped: giving a directory to another uid needs root, and this run is uid %d\n", os.Geteuid())
		return ctx, godog.ErrSkip
	}
	if err := os.Chown(jobDirOf(s, sessionID), 65534, 65534); err != nil {
		fmt.Fprintf(os.Stderr, "skipped: chown to uid 65534 failed: %v\n", err)
		return ctx, godog.ErrSkip
	}
	return ctx, nil
}

// innerEntry replaces <job>/scratch/out with what make puts there and
// records the outside directory when there is one.
func innerEntry(ctx context.Context, sessionID string, make func(p string, vs *vetScenario) error) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	scratch := filepath.Join(jobDirOf(s, sessionID), "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return ctx, err
	}
	p := filepath.Join(scratch, "out")
	if err := os.RemoveAll(p); err != nil {
		return ctx, err
	}
	if err := make(p, vs); err != nil {
		return ctx, err
	}
	if vs.outside != "" {
		return ctx, recordOutside(vs)
	}
	return ctx, nil
}

func theSessionHasAnInnerLinkOutside(ctx context.Context, sessionID string) (context.Context, error) {
	return innerEntry(ctx, sessionID, func(p string, vs *vetScenario) error {
		if vs.outside == "" {
			return fmt.Errorf("no outside directory")
		}
		return os.Symlink(vs.outside, p)
	})
}

func theSessionHasAnInnerNamedPipe(ctx context.Context, sessionID string) (context.Context, error) {
	return innerEntry(ctx, sessionID, func(p string, _ *vetScenario) error {
		return syscall.Mkfifo(p, 0o600)
	})
}

func theSessionHasAnInnerRegularFile(ctx context.Context, sessionID string) (context.Context, error) {
	return innerEntry(ctx, sessionID, func(p string, _ *vetScenario) error {
		return os.WriteFile(p, []byte("out\n"), 0o600)
	})
}

// aCopyTheWakeStartsHoldsALinkOutside has the copy-rule stub start a copy
// on every wake and the wrapper plant a link to the outside directory in
// the copy's job directory.
func aCopyTheWakeStartsHoldsALinkOutside(ctx context.Context) (context.Context, error) {
	s, vs, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if s.retask == nil {
		return ctx, fmt.Errorf("the copy-rule claude stub is not installed")
	}
	if vs.outside == "" {
		return ctx, fmt.Errorf("no outside directory")
	}
	if err := s.retask.stub.SetKnob("ALWAYS_COPY", "1"); err != nil {
		return ctx, err
	}
	if err := os.WriteFile(filepath.Join(s.homeDir, "plant-copy-link"), []byte(vs.outside), 0o600); err != nil {
		return ctx, err
	}
	return ctx, recordOutside(vs)
}

// claudeCalls reads the wrapper's call log.
func claudeCalls(s *testState) ([][2]string, error) {
	data, err := os.ReadFile(filepath.Join(s.homeDir, "claude-calls"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][2]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		cwd, args, _ := strings.Cut(line, "|")
		out = append(out, [2]string{cwd, args})
	}
	return out, nil
}

// noClaudeCallCarriesAnOverrideFlagOrRunsInTheWorkspace checks the calls
// the wrapper logged: none carries --discard-unpushed or
// --force-remove-worktree, and every claude agents and claude rm, the calls
// the removal paths make, ran from a directory outside the workspace. Other
// calls (a dispatch's launch, retask's build check and wake) run where they
// always have and are only checked for the flags.
func noClaudeCallCarriesAnOverrideFlagOrRunsInTheWorkspace(ctx context.Context) error {
	s, _, err := vetState(ctx)
	if err != nil {
		return err
	}
	calls, err := claudeCalls(s)
	if err != nil {
		return err
	}
	ws := s.workspaceRoot
	if r, err := filepath.EvalSymlinks(ws); err == nil {
		ws = r
	}
	removal := 0
	for _, c := range calls {
		for _, flag := range []string{"--discard-unpushed", "--force-remove-worktree"} {
			if strings.Contains(c[1], flag) {
				return fmt.Errorf("claude was passed %s: %q", flag, c[1])
			}
		}
		verb, _, _ := strings.Cut(c[1], " ")
		if verb != "agents" && verb != "rm" {
			continue
		}
		removal++
		if c[0] == ws || strings.HasPrefix(c[0], ws+string(filepath.Separator)) {
			return fmt.Errorf("claude %q ran from %s, inside the workspace", c[1], c[0])
		}
	}
	if removal == 0 {
		return fmt.Errorf("the wrapper logged no claude agents or claude rm call")
	}
	return nil
}

// theClaudeCallLogIsCleared starts the wrapper's call log afresh, so the
// checks after it see only the calls of the command that follows.
func theClaudeCallLogIsCleared(ctx context.Context) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if err := os.Remove(filepath.Join(s.homeDir, "claude-calls")); err != nil && !os.IsNotExist(err) {
		return ctx, err
	}
	return ctx, nil
}

// theWrapperLoggedNoClaudeRm checks no claude rm reached the fake.
func theWrapperLoggedNoClaudeRm(ctx context.Context) error {
	s, _, err := vetState(ctx)
	if err != nil {
		return err
	}
	calls, err := claudeCalls(s)
	if err != nil {
		return err
	}
	for _, c := range calls {
		if c[1] == "rm" || strings.HasPrefix(c[1], "rm ") {
			return fmt.Errorf("claude rm ran: %q", c[1])
		}
	}
	return nil
}

// hookEvents are the Claude Code hook events a marker command is declared
// for.
var hookEvents = []string{
	"PreToolUse", "PostToolUse", "Notification", "UserPromptSubmit", "Stop",
	"SubagentStop", "PreCompact", "SessionStart", "SessionEnd",
}

// theInstanceDeclaresAMarkerHookOnEveryEvent writes .claude/settings.json and
// settings.local.json in the dispatch instance, each declaring every hook
// event with a command that writes a marker file under $HOME.
func theInstanceDeclaresAMarkerHookOnEveryEvent(ctx context.Context) (context.Context, error) {
	s, _, err := vetState(ctx)
	if err != nil {
		return ctx, err
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	dir := filepath.Join(s.lastDispatchInstancePath, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, err
	}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		hooks := map[string]any{}
		for _, ev := range hookEvents {
			cmd := fmt.Sprintf("touch %s", filepath.Join(s.homeDir, "hook-marker-"+name+"-"+ev))
			hooks[ev] = []any{map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": cmd}}}}
		}
		b, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
		if err != nil {
			return ctx, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func noHookMarkerWasWritten(ctx context.Context) error {
	s, _, err := vetState(ctx)
	if err != nil {
		return err
	}
	found, err := filepath.Glob(filepath.Join(s.homeDir, "hook-marker-*"))
	if err != nil {
		return err
	}
	if len(found) > 0 {
		return fmt.Errorf("hook markers written: %q", found)
	}
	return nil
}

// registerSessionVetSteps wires the job-directory vet steps. Called from
// initializeScenario.
func registerSessionVetSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the fake claude is wrapped$`, theFakeClaudeIsWrapped)
	ctx.Step(`^the session "([^"]*)" is gone but still listed$`, theSessionIsGoneButStillListed)
	ctx.Step(`^the session "([^"]*)" is listed with no job directory$`, theSessionIsListedWithNoJobDirectory)
	ctx.Step(`^an outside directory holds a marker$`, anOutsideDirectoryHoldsAMarker)
	ctx.Step(`^the job directory of session "([^"]*)" is a link to the outside directory$`, theJobDirectoryIsALinkOutside)
	ctx.Step(`^the jobs root is a link to the outside directory$`, theJobsRootIsALinkOutside)
	ctx.Step(`^the config directory is a link to the outside directory$`, theConfigDirectoryIsALinkOutside)
	ctx.Step(`^CLAUDE_CONFIG_DIR is a link to the outside directory$`, theClaudeConfigDirIsALinkOutside)
	ctx.Step(`^the job directory of session "([^"]*)" is group writable$`, theJobDirectoryIsGroupWritable)
	ctx.Step(`^the job directory of session "([^"]*)" is on another device$`, theJobDirectoryIsOnAnotherDevice)
	ctx.Step(`^the job directory of session "([^"]*)" is owned by another uid$`, theJobDirectoryIsOwnedByAnotherUID)
	ctx.Step(`^the session "([^"]*)" has an inner link to the outside directory$`, theSessionHasAnInnerLinkOutside)
	ctx.Step(`^the session "([^"]*)" has an inner named pipe$`, theSessionHasAnInnerNamedPipe)
	ctx.Step(`^the session "([^"]*)" has an inner regular file$`, theSessionHasAnInnerRegularFile)
	ctx.Step(`^a copy the wake starts holds a link to the outside directory$`, aCopyTheWakeStartsHoldsALinkOutside)
	ctx.Step(`^the outside directory is unchanged$`, theOutsideDirectoryIsUnchanged)
	ctx.Step(`^no claude call carries an override flag or runs in the workspace$`, noClaudeCallCarriesAnOverrideFlagOrRunsInTheWorkspace)
	ctx.Step(`^the wrapped fake claude ran no claude rm$`, theWrapperLoggedNoClaudeRm)
	ctx.Step(`^the claude call log is cleared$`, theClaudeCallLogIsCleared)
	ctx.Step(`^the dispatch instance declares a marker hook on every event$`, theInstanceDeclaresAMarkerHookOnEveryEvent)
	ctx.Step(`^no hook marker was written$`, noHookMarkerWasWritten)
}
