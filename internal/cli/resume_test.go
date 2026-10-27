package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The resume tests run `niwa resume` against a scratch workspace whose one
// repository is a local bare repository, with a stub `claude` first on PATH.
// HOME, the XDG directories and CLAUDE_CONFIG_DIR all point into the test's
// temp directory, so nothing reaches the developer's own workspace, Claude
// Code jobs directory or trust file, and Claude Code's session listing is a
// seam the tests answer.

const (
	resumeTestDispatchID = "0b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"
	resumeTestSessionID  = "11111111-2222-4333-8444-555555555555"
	resumeTestInstance   = "rs+work-0123abcd"
	resumeTestName       = "work-0123abcd"
	resumeTestPassName   = "RESUME_TEST_ANCHOR"
)

// resumeStubScript is the stub `claude`. Every call appends one line per
// argument to $RESUME_STUB_LOG, closing with "--end--", and records whether
// the session mapping already existed. A resume launch also writes the job
// record dispatch's capture reads, under $HOME/.claude/jobs.
const resumeStubScript = `#!/bin/sh
[ "$*" = --version ] && exit 1
{
  for a in "$@"; do printf 'arg:%s\n' "$a"; done
  if [ -f "$RESUME_STUB_MAPPING" ]; then echo mapping:yes; else echo mapping:no; fi
  echo "env:$RESUME_TEST_ANCHOR"
  echo --end--
} >> "$RESUME_STUB_LOG"
sid=""
prev=""
for a in "$@"; do
  [ "$prev" = --resume ] && sid="$a"
  prev="$a"
done
if [ -n "$sid" ]; then
  short=$(printf '%s' "$sid" | cut -c1-8)
  mkdir -p "$HOME/.claude/jobs/$short"
  printf '{"cwd":"%s","sessionId":"%s"}' "$(pwd -P)" "$sid" > "$HOME/.claude/jobs/$short/state.json"
fi
exit 0
`

type resumeFixture struct {
	root     string
	home     string
	trust    string
	log      string
	bareHead string
	sessions []claudeSessionRow
	listErr  error
}

func newResumeFixture(t *testing.T, extraTOML string) *resumeFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	f := &resumeFixture{}
	f.home = canonicalTempDir(t)
	t.Setenv("HOME", f.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(f.home, "xdg-config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(f.home, "xdg-state"))
	claudeConfig := filepath.Join(f.home, "claude-config")
	if err := os.MkdirAll(claudeConfig, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", claudeConfig)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("GITHUB_TOKEN", "test-token-not-used")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	t.Setenv("NIWA_REAP_KEY", "")
	f.trust = filepath.Join(claudeConfig, ".claude.json")

	prevHome := userHomeDir
	userHomeDir = func() string { return f.home }
	prevList := listClaudeSessionsFunc
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) { return f.sessions, f.listErr }
	prevTimeout := resumeCaptureTimeout
	resumeCaptureTimeout = 2 * time.Second
	t.Cleanup(func() {
		userHomeDir = prevHome
		listClaudeSessionsFunc = prevList
		resumeCaptureTimeout = prevTimeout
	})

	// The stub claude, first on PATH.
	stubDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stubDir, "claude"), []byte(resumeStubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.log = filepath.Join(t.TempDir(), "claude.log")
	t.Setenv("RESUME_STUB_LOG", f.log)
	t.Setenv(resumeTestPassName, "anchor-from-shell")

	// One repository, served from a local bare repository.
	src := filepath.Join(canonicalTempDir(t), "app")
	scanInitGitRepoForCLITest(t, src)
	head, err := exec.Command("git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	f.bareHead = strings.TrimSpace(string(head))

	f.root = canonicalTempDir(t)
	toml := "[workspace]\nname = \"rs\"\n\n[groups.tools]\n\n[repos.app]\nurl = \"file://" + src + ".bare\"\ngroup = \"tools\"\n\n[claude]\ntrust_instances = true\n\n[session.dispatch_env]\npassthrough = [\"" + resumeTestPassName + "\"]\n" + extraTOML
	if err := os.MkdirAll(filepath.Join(f.root, config.ConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, config.ConfigDir, config.ConfigFile), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	mapping := filepath.Join(f.root, workspace.StateDir, "sessions", resumeTestSessionID+".json")
	t.Setenv("RESUME_STUB_MAPPING", mapping)
	chdir(t, f.root)
	return f
}

// record is a launch record as a dispatch into resumeTestInstance writes it.
func (f *resumeFixture) record() workspace.LaunchRecord {
	return workspace.LaunchRecord{
		DispatchID:     resumeTestDispatchID,
		SessionID:      resumeTestSessionID,
		Agent:          string(agent.AgentClaude),
		InstanceName:   resumeTestInstance,
		PermissionMode: "default",
		SessionName:    resumeTestName,
		Model:          "opus",
		Settings:       `{"enabledMcpjsonServers":["docs"],"env":{"NIWA_REAP_KEY":"` + resumeTestDispatchID + `","` + resumeTestPassName + `":"anchor-recorded"}}`,
		Env:            map[string]string{config.ReapKeyEnv: resumeTestDispatchID, resumeTestPassName: "anchor-recorded"},
		WrittenAt:      time.Now().UTC().Add(-time.Hour),
	}
}

func (f *resumeFixture) writeRecord(t *testing.T, rec workspace.LaunchRecord) {
	t.Helper()
	if err := workspace.WriteLaunchRecord(f.root, rec); err != nil {
		t.Fatal(err)
	}
}

func (f *resumeFixture) instancePath() string { return filepath.Join(f.root, resumeTestInstance) }

// writeTranscript gives the session the transcript Claude Code keeps for it.
func (f *resumeFixture) writeTranscript(t *testing.T, sessionID string) {
	t.Helper()
	p := sessionattach.TranscriptPath(f.home, f.instancePath(), sessionID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// presentInstance puts a minimal instance at the record's path.
func (f *resumeFixture) presentInstance(t *testing.T) {
	t.Helper()
	if err := writeMinimalInstanceState(f.instancePath()); err != nil {
		t.Fatal(err)
	}
}

func (f *resumeFixture) setHostMode(t *testing.T, mode string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "[global]\ndispatch_permission_mode = \"" + mode + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// calls returns the stub's recorded calls, each its argument list plus the
// mapping and env lines.
func (f *resumeFixture) calls(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	var cur []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "--end--" {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	return out
}

func runResumeCmd(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	runErr := runResume(cmd, args)
	return outBuf.String(), errBuf.String(), runErr
}

func trustHolds(t *testing.T, trustFile, path string) bool {
	t.Helper()
	data, err := os.ReadFile(trustFile)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("trust file does not parse: %v\n%s", err, data)
	}
	_, ok := doc.Projects[path]
	return ok
}

func argsOf(call []string) []string {
	var args []string
	for _, l := range call {
		if a, ok := strings.CutPrefix(l, "arg:"); ok {
			args = append(args, a)
		}
	}
	return args
}

func TestResumeRestoresDestroyedInstance(t *testing.T) {
	f := newResumeFixture(t, "")
	rec := f.record()
	f.writeRecord(t, rec)
	f.writeTranscript(t, resumeTestSessionID)

	stdout, stderr, err := runResumeCmd(t, resumeTestInstance, "-carry on")
	if err != nil {
		t.Fatalf("resume: %v\nstderr:\n%s", err, stderr)
	}

	// The directory is back at its original path, holding the repository.
	if _, err := os.Stat(filepath.Join(f.instancePath(), "tools", "app", "README.md")); err != nil {
		t.Fatalf("instance not restored at %s: %v", f.instancePath(), err)
	}
	if !trustHolds(t, f.trust, f.instancePath()) {
		t.Errorf("no Claude trust entry for %s in %s", f.instancePath(), f.trust)
	}

	calls := f.calls(t)
	var launches [][]string
	for _, c := range calls {
		if len(argsOf(c)) > 0 && argsOf(c)[0] == "--bg" {
			launches = append(launches, c)
		}
	}
	if len(launches) != 1 {
		t.Fatalf("stub claude launched %d times, want 1: %q", len(launches), calls)
	}
	want := []string{"--bg", "--resume", resumeTestSessionID,
		"--permission-mode", "default",
		"--name=" + resumeTestName,
		"--model=opus",
		"--settings", rec.Settings,
		"--", "-carry on"}
	if got := argsOf(launches[0]); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("launch argv\n got %q\nwant %q", got, want)
	}
	if !listHas(launches[0], "mapping:yes") {
		t.Errorf("the session mapping was not written before the launch: %q", launches[0])
	}
	if !listHas(launches[0], "env:anchor-recorded") {
		t.Errorf("the worker did not get the recorded environment value: %q", launches[0])
	}

	for _, w := range []string{
		"Resumed session " + resumeTestSessionID,
		"permission mode: default",
		"tools/app at " + f.bareHead,
		"unpushed work",
	} {
		if !strings.Contains(stdout, w) {
			t.Errorf("stdout lacks %q:\n%s", w, stdout)
		}
	}

	m, err := workspace.ReadSessionMapping(f.root, resumeTestSessionID)
	if err != nil {
		t.Fatalf("no session mapping: %v", err)
	}
	if m.DispatchID != resumeTestDispatchID || m.InstancePath != f.instancePath() || m.Origin != workspace.MappingOriginDispatch {
		t.Errorf("mapping = %+v", m)
	}
}

func TestResumeBySessionIDAndNameWithPresentDirectory(t *testing.T) {
	f := newResumeFixture(t, "")
	f.writeRecord(t, f.record())
	f.writeTranscript(t, resumeTestSessionID)
	f.presentInstance(t)

	for _, arg := range []string{resumeTestSessionID, resumeTestName} {
		stdout, stderr, err := runResumeCmd(t, arg)
		if err != nil {
			t.Fatalf("resume %s: %v\nstderr:\n%s", arg, err, stderr)
		}
		if strings.Contains(stdout, "Restored") {
			t.Errorf("resume %s restored a present instance:\n%s", arg, stdout)
		}
		if !strings.Contains(stdout, "Resumed session "+resumeTestSessionID) {
			t.Errorf("resume %s: stdout lacks the session id:\n%s", arg, stdout)
		}
	}
	if _, err := os.Stat(f.trust); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a trust entry was written for a present instance (%v)", err)
	}
	calls := f.calls(t)
	if len(calls) != 2 {
		t.Fatalf("stub claude called %d times, want 2: %q", len(calls), calls)
	}
	for _, c := range calls {
		args := argsOf(c)
		if len(args) < 3 || args[1] != "--resume" || args[2] != resumeTestSessionID {
			t.Errorf("call did not resume %s: %q", resumeTestSessionID, args)
		}
		if listHas(args, "--") {
			t.Errorf("a resume with no prompt passed a separator: %q", args)
		}
	}
}

func TestResumePermissionModeIsTheStricter(t *testing.T) {
	cases := []struct {
		recorded, current string
		want              string
		notice            bool
	}{
		{"bypassPermissions", "default", "default", true},
		{"acceptEdits", "plan", "plan", true},
		{"acceptEdits", "bypassPermissions", "acceptEdits", false},
		{"default", "default", "default", false},
		// No current mode at all: the downgrade spells "default" out rather
		// than leaving the mode to the agent's own settings.
		{"bypassPermissions", "", "default", true},
	}
	for _, c := range cases {
		t.Run(c.recorded+"_then_"+c.current, func(t *testing.T) {
			f := newResumeFixture(t, "")
			rec := f.record()
			rec.PermissionMode = c.recorded
			f.writeRecord(t, rec)
			f.writeTranscript(t, resumeTestSessionID)
			f.presentInstance(t)
			if c.current != "" {
				f.setHostMode(t, c.current)
			}

			stdout, stderr, err := runResumeCmd(t, resumeTestInstance)
			if err != nil {
				t.Fatalf("resume: %v\nstderr:\n%s", err, stderr)
			}
			calls := f.calls(t)
			if len(calls) != 1 {
				t.Fatalf("stub claude called %d times: %q", len(calls), calls)
			}
			args := argsOf(calls[0])
			i := indexOf(args, "--permission-mode")
			if i < 0 || i+1 >= len(args) || args[i+1] != c.want {
				t.Errorf("launched %q, want --permission-mode %s", args, c.want)
			}
			if !strings.Contains(stdout, "permission mode: "+c.want) {
				t.Errorf("stdout lacks the mode %s:\n%s", c.want, stdout)
			}
			hasNotice := strings.Contains(stderr, "instead of the recorded")
			if hasNotice != c.notice {
				t.Errorf("notice printed = %v, want %v:\n%s", hasNotice, c.notice, stderr)
			}
			if c.notice && !(strings.Contains(stderr, c.recorded) && strings.Contains(stderr, c.want)) {
				t.Errorf("notice does not name both modes:\n%s", stderr)
			}
		})
	}
}

func TestResumeRefusals(t *testing.T) {
	cases := []struct {
		name     string
		arg      string
		present  bool
		setup    func(t *testing.T, f *resumeFixture)
		want     string
		alsoWant string
	}{
		{
			name: "no launch record",
			arg:  "rs+other-89abcdef",
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
			},
			want: "no launch record",
		},
		{
			name: "two matching records",
			arg:  resumeTestName,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				other := f.record()
				other.DispatchID = "9b1c2d3e-4f50-4a6b-8c7d-8e9fa0b1c2d3"
				other.SessionID = "99999999-2222-4333-8444-555555555555"
				other.InstanceName = "rs+work-89abcdef"
				f.writeRecord(t, other)
			},
			want:     "rs+work-89abcdef",
			alsoWant: resumeTestInstance,
		},
		{
			name: "gone transcript",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
			},
			want: "transcript",
		},
		{
			name: "running session",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				f.sessions = []claudeSessionRow{{ID: "11111111", SessionID: resumeTestSessionID, PID: 4242}}
			},
			want: "still running",
		},
		{
			name: "file at the original path",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				if err := os.WriteFile(f.instancePath(), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			present: true,
			want:    "occupies",
		},
		{
			name: "directory without the instance's state",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				if err := os.MkdirAll(f.instancePath(), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			present: true,
			want:    "occupies",
		},
		{
			name: "symlink at the original path",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				target := t.TempDir()
				if err := writeMinimalInstanceState(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, f.instancePath()); err != nil {
					t.Fatal(err)
				}
			},
			present: true,
			want:    "occupies",
		},
		{
			name: "environment name no longer passed",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				rec := f.record()
				rec.Env["RESUME_DROPPED_NAME"] = "x"
				f.writeRecord(t, rec)
				f.writeTranscript(t, resumeTestSessionID)
			},
			want: "RESUME_DROPPED_NAME",
		},
		{
			name: "settings key dispatch never emits",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				rec := f.record()
				rec.Settings = `{"hooks":{"Stop":[]}}`
				f.writeRecord(t, rec)
				f.writeTranscript(t, resumeTestSessionID)
			},
			want: `"hooks"`,
		},
		{
			name: "model not starting with a letter or digit",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				rec := f.record()
				rec.Model = "--dangerously-skip-permissions"
				f.writeRecord(t, rec)
				f.writeTranscript(t, resumeTestSessionID)
			},
			want: "model",
		},
		{
			name: "session name not starting with a letter or digit",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				rec := f.record()
				rec.SessionName = "-x"
				f.writeRecord(t, rec)
				f.writeTranscript(t, resumeTestSessionID)
			},
			want: "session name",
		},
		{
			name: "recorded mode outside the order",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				rec := f.record()
				rec.PermissionMode = "dontAsk"
				f.writeRecord(t, rec)
				f.writeTranscript(t, resumeTestSessionID)
			},
			want: "dontAsk",
		},
		{
			name: "record not mode 0600",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				p := filepath.Join(workspace.LaunchDir(f.root), resumeTestDispatchID+".json")
				if err := os.Chmod(p, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "mode",
		},
		{
			name: "record that is a symlink",
			arg:  resumeTestInstance,
			setup: func(t *testing.T, f *resumeFixture) {
				f.writeRecord(t, f.record())
				f.writeTranscript(t, resumeTestSessionID)
				p := filepath.Join(workspace.LaunchDir(f.root), resumeTestDispatchID+".json")
				moved := filepath.Join(t.TempDir(), "rec.json")
				if err := os.Rename(p, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, p); err != nil {
					t.Fatal(err)
				}
			},
			want: "launch record",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newResumeFixture(t, "")
			c.setup(t, f)
			_, stderr, err := runResumeCmd(t, c.arg)
			if err == nil {
				t.Fatalf("resume succeeded; want a refusal\nstderr:\n%s", stderr)
			}
			msg := err.Error()
			if !strings.Contains(msg, `"`+c.arg+`"`) {
				t.Errorf("refusal does not name the argument %q: %v", c.arg, msg)
			}
			if !strings.Contains(msg, c.want) {
				t.Errorf("refusal lacks %q: %v", c.want, msg)
			}
			if c.alsoWant != "" && !strings.Contains(msg, c.alsoWant) {
				t.Errorf("refusal lacks %q: %v", c.alsoWant, msg)
			}
			if calls := f.calls(t); len(calls) != 0 {
				t.Errorf("stub claude was called: %q", calls)
			}
			if !c.present {
				if _, err := os.Lstat(f.instancePath()); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("something was created at %s (%v)", f.instancePath(), err)
				}
			}
			if _, err := os.Stat(f.trust); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a trust entry was written (%v)", err)
			}
			if _, err := workspace.ReadSessionMapping(f.root, resumeTestSessionID); err == nil {
				t.Errorf("a session mapping was written")
			}
		})
	}
}

// TestResumeRecordModelShapes pins which recorded models resume accepts:
// dispatch forwards an unrecognized model as given, so a full model id must
// resume, while anything that could read as a flag or carry a space is
// refused.
func TestResumeRecordModelShapes(t *testing.T) {
	f := newResumeFixture(t, "")
	spec, _ := agentplan.For(agent.AgentClaude).LaunchSpec()
	for model, ok := range map[string]bool{
		"opus":                     true,
		"claude-opus-4-5[1m]":      true,
		"vendor/model:tag":         true,
		"model@2026-01-01":         true,
		"--dangerously-skip-perms": false,
		"[1m]":                     false,
		"opus --permission-mode x": false,
		"opus\nbypassPermissions":  false,
		strings.Repeat("a", 129):   false,
	} {
		rec := f.record()
		rec.Model = model
		err := validateLaunchRecord(rec, spec, []string{resumeTestPassName})
		if (err == nil) != ok {
			t.Errorf("model %q: err = %v, want accepted = %v", model, err, ok)
		}
	}
}

func TestResumeArgvForAnAgentWithoutResumeLaunch(t *testing.T) {
	spec := agentplan.LaunchSpec{Binary: "x"}
	if got := reentryResumeArgs(spec, resumeLaunch{SessionID: resumeTestSessionID}); got != nil {
		t.Errorf("an agent with no resume-launch arguments got %q", got)
	}
	claude, _ := agentplan.For(agent.AgentClaude).LaunchSpec()
	noFlag := claude
	noFlag.Flags.Model = ""
	if got := reentryResumeArgs(noFlag, resumeLaunch{SessionID: resumeTestSessionID, Model: "opus"}); got != nil {
		t.Errorf("a recorded model with no flag to carry it was dropped silently: %q", got)
	}
}

func listHas(list []string, s string) bool {
	return indexOf(list, s) >= 0
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
