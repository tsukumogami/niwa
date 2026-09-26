package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Every dispatch test in this file gets its permissions posture from a
// workspace.toml declaration run through a real Applier.Create, never from a
// hand-written settings document or state file. The tamper tests hand-edit the
// generated settings file only to prove dispatch ignores it, and the
// broken-state tests damage the state Create wrote only after checking what it
// recorded. TestDerivePermissionMode is a pure table over the derivation.

// derivedNoticePrefix is the stderr notice dispatch prints when it derives
// --permission-mode from the workspace's declared posture rather than an
// explicit flag.
const derivedNoticePrefix = "niwa dispatch: derived --permission-mode bypassPermissions"

// The declarations named S1-S3 in the document matrix of
// docs/prds/PRD-inert-defaultmode-key.md. S1 is the workspace declaring
// bypass, S2 the workspace declaring ask, S3 no declaration at all.
const (
	postureS1 = "\n[claude.settings]\npermissions = \"bypass\"\n"
	postureS2 = "\n[claude.settings]\npermissions = \"ask\"\n"
	postureS3 = ""
)

// declaredWorkspaceTOML is a workspace with one repo, which the provisioner
// pre-creates with a .git marker so the Cloner leaves it alone.
const declaredWorkspaceTOML = `[workspace]
name = "test-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]
`

// setupDeclaredDispatchWorkspace is setupDispatchWorkspace with a real
// workspace config: decl is appended to it, so the posture a dispatched
// instance records comes from the declaration alone.
func setupDeclaredDispatchWorkspace(t *testing.T, decl string) string {
	t.Helper()
	// Create may write per-directory trust into the developer's own agent
	// configuration; keep that inside the test.
	t.Setenv("HOME", t.TempDir())
	root := canonicalTempDir(t)
	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(declaredWorkspaceTOML+decl), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// createDeclaredInstance runs a real Applier.Create for the workspace at root,
// naming the instance name, and returns the instance path.
func createDeclaredInstance(ctx context.Context, root, name string) (string, error) {
	loaded, err := config.Load(filepath.Join(root, config.ConfigDir, config.ConfigFile))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(root, name, "default", "app", ".git"), 0o755); err != nil {
		return "", err
	}
	applier := workspace.NewApplier(&stubRepoLister{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	})
	applier.Reporter = workspace.NewReporterWithTTY(io.Discard, false)
	applier.Cloner = &workspace.Cloner{}
	return applier.Create(ctx, loaded.Config, filepath.Join(root, config.ConfigDir), root, name)
}

// provisionThroughCreate replaces the provision seam with a real Create of the
// workspace's declaration. afterCreate, when non-nil, runs on the new instance
// before dispatch sees it -- the point at which a test breaks or tampers with
// what Create wrote. Call it after installDispatchFakes, which would otherwise
// overwrite the seam; installDispatchFakes' cleanup restores the original.
func provisionThroughCreate(t *testing.T, f *dispatchFakes, afterCreate func(t *testing.T, instancePath string)) {
	t.Helper()
	provisionInstanceFunc = func(ctx context.Context, root, _, namePrefix, sep string, _ int) (provisionResult, error) {
		f.provisionCalled++
		name := "test-ws" + sep + namePrefix
		path, err := createDeclaredInstance(ctx, root, name)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		f.instancePath = path
		if afterCreate != nil {
			afterCreate(t, path)
		}
		return provisionResult{Name: name, Path: path}, nil
	}
}

// statePathOf is where Create saves an instance's state.
func statePathOf(instancePath string) string {
	return filepath.Join(instancePath, workspace.StateDir, workspace.StateFile)
}

// requireRecordedPosture asserts the state Create saved records want.
func requireRecordedPosture(t *testing.T, instancePath, want string) {
	t.Helper()
	state, err := workspace.LoadState(instancePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.ClaudePermissions != want {
		t.Fatalf("recorded claude_permissions = %q, want %q", state.ClaudePermissions, want)
	}
}

// derivedArgv rebuilds, test-side, the steps runDispatch takes to turn an
// instance's state into argv -- state load, derivePermissionMode,
// buildDispatchPassthrough -- for a Claude launch with no explicit flag. It
// shows what a fixture's state derives to. It is not runDispatch: that
// runDispatch forwards a derived flag is pinned by
// TestDispatch_PermissionMode_S1_Derived.
func derivedArgv(t *testing.T, instancePath string) []string {
	t.Helper()
	state, err := workspace.LoadState(instancePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	flags := claudeLaunchSpec().Flags
	mode, _ := derivePermissionMode("", state.ClaudePermissions, flags)
	return buildDispatchPassthrough(flags, "", "", mode)
}

// permissionModeValues returns the value following each --permission-mode in
// argv, so a test can require exactly one and name it.
func permissionModeValues(argv []string) []string {
	var vals []string
	for i, a := range argv {
		if a == "--permission-mode" && i+1 < len(argv) {
			vals = append(vals, argv[i+1])
		}
	}
	return vals
}

func requirePermissionMode(t *testing.T, argv []string, want string) {
	t.Helper()
	if got := permissionModeValues(argv); !slices.Equal(got, []string{want}) {
		t.Fatalf("--permission-mode values = %v, want exactly [%s] (argv %v)", got, want, argv)
	}
}

func requireNoPermissionMode(t *testing.T, argv []string) {
	t.Helper()
	if slices.Contains(argv, "--permission-mode") || slices.Contains(argv, "bypassPermissions") {
		t.Fatalf("expected no --permission-mode, got %v", argv)
	}
}

func TestDerivePermissionMode(t *testing.T) {
	claude := claudeLaunchSpec().Flags
	codexSpec, ok := agentplan.For(agent.AgentCodex).LaunchSpec()
	if !ok {
		t.Fatal("no Codex launch spec")
	}
	codex := codexSpec.Flags
	if codex.PermissionMode == "--permission-mode" {
		t.Fatal("the Codex case needs an agent whose permission flag is not --permission-mode")
	}
	cases := []struct {
		name        string
		explicit    string
		recorded    string
		flags       agentplan.LaunchFlags
		wantMode    string
		wantDerived bool
	}{
		{"explicit wins over bypass", "acceptEdits", "bypass", claude, "acceptEdits", false},
		{"explicit with nothing recorded", "acceptEdits", "", claude, "acceptEdits", false},
		{"bypass with Claude flags", "", "bypass", claude, "bypassPermissions", true},
		{"ask", "", "ask", claude, "", false},
		{"nothing recorded", "", "", claude, "", false},
		{"unrecognized recorded value", "", "bypassPermissions", claude, "", false},
		{"bypass with Codex flags", "", "bypass", codex, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, derived := derivePermissionMode(tc.explicit, tc.recorded, tc.flags)
			if mode != tc.wantMode || derived != tc.wantDerived {
				t.Fatalf("derivePermissionMode(%q, %q) = (%q, %v), want (%q, %v)",
					tc.explicit, tc.recorded, mode, derived, tc.wantMode, tc.wantDerived)
			}
		})
	}
}

// TestDispatch_PermissionMode_S1_Derived: a workspace declaring bypass, with no
// explicit flag, launches its worker with --permission-mode bypassPermissions
// and says so on stderr.
func TestDispatch_PermissionMode_S1_Derived(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireRecordedPosture(t, f.instancePath, "bypass")
	requirePermissionMode(t, pass, "bypassPermissions")
	if !strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("expected the derived notice on stderr, got %q", stderr)
	}
}

// TestDispatch_PermissionMode_S1_ExplicitFlagWins: an explicit
// --permission-mode on a bypass workspace is the only one on the argv.
func TestDispatch_PermissionMode_S1_ExplicitFlagWins(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchPermissionMode = "acceptEdits"
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requirePermissionMode(t, pass, "acceptEdits")
	if slices.Contains(pass, "bypassPermissions") {
		t.Fatalf("an explicit flag must be the only permission mode; got %v", pass)
	}
	if strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("the derived notice must not fire for an explicit flag; got %q", stderr)
	}
}

// TestDispatch_PermissionMode_S2S3_ForwardNothing: ask and undeclared forward
// no --permission-mode and print no notice.
func TestDispatch_PermissionMode_S2S3_ForwardNothing(t *testing.T) {
	for _, tc := range []struct {
		name, decl, recorded string
	}{
		{"S2 ask", postureS2, "ask"},
		{"S3 undeclared", postureS3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDeclaredDispatchWorkspace(t, tc.decl)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			provisionThroughCreate(t, f, nil)
			var pass []string
			captureLaunchPassthrough(f, &pass)

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			requireRecordedPosture(t, f.instancePath, tc.recorded)
			requireNoPermissionMode(t, pass)
			if strings.Contains(stderr, derivedNoticePrefix) {
				t.Fatalf("the derived notice must not fire; got %q", stderr)
			}
		})
	}
}

// TestDispatch_PermissionMode_Codex_S1_NeverDerived: a Codex worker on a bypass
// workspace gets nothing through its own permission flag.
func TestDispatch_PermissionMode_Codex_S1_NeverDerived(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchHarness = "codex"
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requireRecordedPosture(t, f.instancePath, "bypass")
	requireNoPermissionMode(t, pass)
	if slices.Contains(pass, "--sandbox") {
		t.Fatalf("Codex's --sandbox flag must not be touched by this derivation; got %v", pass)
	}
	if strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("the derived notice must not fire for Codex; got %q", stderr)
	}
}

// TestDispatch_PermissionMode_S1_RemoteControlAndKeepAlive: the derived flag
// rides alongside the remote-control settings pair, and keep-alive still arms
// the way it does without a derived flag.
func TestDispatch_PermissionMode_S1_RemoteControlAndKeepAlive(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	var prompt string
	var pass []string
	captureLaunchPrompt(f, &prompt, &pass)
	dispatchKeepAlive = kaBoolPtr(true)

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requirePermissionMode(t, pass, "bypassPermissions")
	if !hasRemoteControlSettings(pass) {
		t.Fatalf("remote-control default-fill must still inject; got %v", pass)
	}
	if want := keepAliveArmingInstruction + "do a thing"; prompt != want {
		t.Fatalf("keep-alive must still arm with remote control on; launched prompt = %q", prompt)
	}
}

// TestDispatch_PermissionMode_BrokenState_WithholdsFlag breaks a bypass
// instance's state file three ways. Before each break it checks that the
// instance recorded bypass and that its state derives the flag, so an argv
// without the flag afterwards comes from the break, not from a fixture that
// never asked for bypass. That runDispatch forwards a derived flag at all is
// pinned by TestDispatch_PermissionMode_S1_Derived.
func TestDispatch_PermissionMode_BrokenState_WithholdsFlag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		asRoot bool // whether the break still breaks when running as root
		brk    func(t *testing.T, statePath string)
	}{
		{"missing", true, func(t *testing.T, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable", false, func(t *testing.T, p string) {
			if err := os.Chmod(p, 0o000); err != nil {
				t.Fatal(err)
			}
		}},
		{"invalid json", true, func(t *testing.T, p string) {
			if err := os.WriteFile(p, []byte("{ not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.asRoot && os.Geteuid() == 0 {
				t.Skip("root reads a 0000 file")
			}
			root := setupDeclaredDispatchWorkspace(t, postureS1)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			provisionThroughCreate(t, f, func(t *testing.T, instancePath string) {
				requireRecordedPosture(t, instancePath, "bypass")
				requirePermissionMode(t, derivedArgv(t, instancePath), "bypassPermissions")
				tc.brk(t, statePathOf(instancePath))
			})
			var pass []string
			captureLaunchPassthrough(f, &pass)

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("a broken state file must not fail dispatch: %v", err)
			}
			requireNoPermissionMode(t, pass)
			if !strings.Contains(stderr, "warning") || !strings.Contains(stderr, statePathOf(f.instancePath)) {
				t.Fatalf("expected a warning naming %s, got %q", statePathOf(f.instancePath), stderr)
			}
		})
	}
}

// TestDispatch_PermissionMode_BrokenState_ExplicitFlagStillWins: on a broken
// bypass instance an explicit flag is still forwarded, and is still the only
// one.
func TestDispatch_PermissionMode_BrokenState_ExplicitFlagStillWins(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, func(t *testing.T, instancePath string) {
		requireRecordedPosture(t, instancePath, "bypass")
		if err := os.WriteFile(statePathOf(instancePath), []byte("{ not json"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	dispatchPermissionMode = "acceptEdits"
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	requirePermissionMode(t, pass, "acceptEdits")
	// The explicit flag set the mode, so the unreadable posture changed nothing
	// and there is nothing to warn about.
	if strings.Contains(stderr, statePathOf(f.instancePath)) {
		t.Fatalf("no posture warning expected alongside an explicit flag; got %q", stderr)
	}
}

// TestDispatch_PermissionMode_IgnoresGeneratedSettings is the tamper test: the
// generated .claude/settings.json can neither grant the flag nor withhold it.
func TestDispatch_PermissionMode_IgnoresGeneratedSettings(t *testing.T) {
	settingsOf := func(instancePath string) string {
		return filepath.Join(instancePath, ".claude", "settings.json")
	}

	t.Run("S3 with a hand-written bypass settings file", func(t *testing.T) {
		root := setupDeclaredDispatchWorkspace(t, postureS3)
		chdir(t, root)
		setHostConfig(t, "")
		f := installDispatchFakes(t, root)
		provisionThroughCreate(t, f, func(t *testing.T, instancePath string) {
			p := settingsOf(instancePath)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(`{"permissions":{"defaultMode":"bypassPermissions"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		var pass []string
		captureLaunchPassthrough(f, &pass)

		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requireNoPermissionMode(t, pass)
	})

	t.Run("S1 with the settings file deleted", func(t *testing.T) {
		root := setupDeclaredDispatchWorkspace(t, postureS1)
		chdir(t, root)
		setHostConfig(t, "")
		f := installDispatchFakes(t, root)
		provisionThroughCreate(t, f, func(t *testing.T, instancePath string) {
			if err := os.Remove(settingsOf(instancePath)); err != nil {
				t.Fatalf("removing the generated settings file: %v", err)
			}
		})
		var pass []string
		captureLaunchPassthrough(f, &pass)

		if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		requirePermissionMode(t, pass, "bypassPermissions")
	})
}

// TestWatchLaunches_CarryNoPermissionMode builds both `niwa watch` launches
// through the helpers the watch sites call, for a real bypass instance. A watch
// site that started deriving a mode from the instance's recorded posture would
// forward bypassPermissions here.
func TestWatchLaunches_CarryNoPermissionMode(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	setHostConfig(t, "")
	instancePath, err := createDeclaredInstance(context.Background(), root, "test-ws+watch-o-r-1-0000abcd")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	requireRecordedPosture(t, instancePath, "bypass")

	for _, sandbox := range []bool{false, true} {
		fresh := watchReviewLaunch(instancePath, "watch-o-r-1", "review", sandbox)
		resume := watchResumeLaunch(instancePath, "handle1", dispatchTestSessionID, "again", sandbox)
		for name, req := range map[string]launchRequest{"fresh review": fresh, "continuation": resume} {
			if req.InstanceDir != instancePath {
				t.Fatalf("%s: InstanceDir = %q, want %q", name, req.InstanceDir, instancePath)
			}
			requireNoPermissionMode(t, req.Passthrough)
		}
		if !containsSubsequence(resume.Passthrough, []string{"--resume", dispatchTestSessionID}) {
			t.Fatalf("continuation lost --resume: %v", resume.Passthrough)
		}
	}
}

// Re-entry under a bypass workspace: every way back into a dispatched Claude
// session is `claude attach <handle>` and nothing else. A change that appended
// a permission flag to re-entry would fail these.

// dispatchedHandle returns the handle recorded for the one dispatched session.
func dispatchedHandle(t *testing.T, root string) workspace.SessionMapping {
	t.Helper()
	mappings, err := workspace.ListSessionMappings(root)
	if err != nil {
		t.Fatalf("ListSessionMappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected one session mapping, got %d", len(mappings))
	}
	if mappings[0].Handle == "" {
		t.Fatal("the session mapping recorded no handle")
	}
	return mappings[0]
}

func TestReentryUnderBypass_FinalAttach(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	realAttach := dispatchAttach
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchAttach = realAttach
	dispatchDetach = false

	// A stub claude that records the argv the real dispatchAttach runs it with.
	binDir := t.TempDir()
	argvFile := filepath.Join(binDir, "argv")
	script := "#!/bin/sh\n: > " + argvFile + "\nfor a in \"$@\"; do printf '%s\\0' \"$a\" >> " + argvFile + "; done\n"
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	lookAgentBinary = func(name string) (string, error) { return filepath.Join(binDir, name), nil }

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	m := dispatchedHandle(t, root)
	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("the final attach never ran the binary: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	if want := []string{"attach", m.Handle}; !slices.Equal(got, want) {
		t.Fatalf("final attach argv = %#v, want %#v", got, want)
	}
}

func TestReentryUnderBypass_PrintedHint(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchDetach = true

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	want := "  claude attach " + dispatchedHandle(t, root).Handle
	if !slices.Contains(strings.Split(stdout, "\n"), want) {
		t.Fatalf("printed hints lack the exact line %q:\n%s", want, stdout)
	}
}

func TestReentryUnderBypass_AttachFailureFallback(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchDetach = false
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		return io.ErrClosedPipe
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	want := "attach later with: claude attach " + dispatchedHandle(t, root).Handle + "\n"
	if !strings.Contains(stderr, want) {
		t.Fatalf("the attach-failure fallback lacks %q:\n%s", want, stderr)
	}
}

func TestReentryUnderBypass_ListResume(t *testing.T) {
	root := setupDeclaredDispatchWorkspace(t, postureS1)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionThroughCreate(t, f, nil)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	m := dispatchedHandle(t, root)
	if got, want := sessionResumeCommand(m), "claude attach "+m.Handle; got != want {
		t.Fatalf("niwa list resume column = %q, want %q", got, want)
	}
}

// containsSubsequence reports whether want appears as a contiguous
// subsequence anywhere in got.
func containsSubsequence(got, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(got); i++ {
		if slices.Equal(got[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
