package cli

import (
	"context"
	"strings"
	"testing"
)

// captureLaunchPrompt overrides the launch seam to record the final prompt the
// worker is launched with (the arming instruction, when injected, is prepended
// to it) alongside the passthrough argv.
func captureLaunchPrompt(f *dispatchFakes, gotPrompt *string, gotPass *[]string) {
	dispatchLaunch = func(_ context.Context, _, prompt string, passthrough, _ []string) error {
		f.launchCalled++
		*gotPrompt = prompt
		if gotPass != nil {
			*gotPass = passthrough
		}
		return nil
	}
}

// Flag on + host remote-control on (niwa injects RC) => the fixed self-arm
// instruction is prepended to the task prompt, and the payload is exactly the
// constant plus the original prompt riding one argv element.
func TestDispatch_KeepAlive_FlagOn_RCInjected_Arms(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "") // downstream unset -> RC injected
	var prompt string
	var pass []string
	captureLaunchPrompt(f, &prompt, &pass)
	dispatchKeepAlive = kaBoolPtr(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := keepAliveArmingInstruction + "do a thing"; prompt != want {
		t.Fatalf("launched prompt = %q, want the fixed arming constant prepended to the task", prompt)
	}
	if !hasRemoteControlSettings(pass) {
		t.Fatalf("RC injection must be unaffected by keep-alive; got passthrough %v", pass)
	}
	if strings.Contains(stderr, keepAliveNonRCWarning) {
		t.Fatalf("no non-RC warning expected when RC is on, got %q", stderr)
	}
	// Argv safety: the armed prompt stays the single final argv element.
	args := buildClaudeBgArgs(prompt, pass)
	if args[len(args)-1] != keepAliveArmingInstruction+"do a thing" {
		t.Fatalf("armed prompt must ride one argv element; final element = %q", args[len(args)-1])
	}
}

// Flag on + remote-control decided DOWNSTREAM (instance settings set
// remoteControlAtStartup true, so niwa injects nothing) => still arms: the
// worker starts with RC either way.
func TestDispatch_KeepAlive_FlagOn_DownstreamRC_Arms(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "") // no host config: nothing injected
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, `{"remoteControlAtStartup": true}`)
	var prompt string
	var pass []string
	captureLaunchPrompt(f, &prompt, &pass)
	dispatchKeepAlive = kaBoolPtr(true)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(prompt, keepAliveArmingInstruction) {
		t.Fatalf("downstream RC must still arm keep-alive; prompt = %q", prompt)
	}
	if hasRemoteControlSettings(pass) {
		t.Fatalf("downstream-decided RC must not be injected; got %v", pass)
	}
}

// Flag on + NO remote control anywhere => a clear warning, no arming, and the
// dispatch still succeeds (R3: a no-op with a warning, never an error).
func TestDispatch_KeepAlive_FlagOn_NonRC_WarnsAndSkips(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var prompt string
	captureLaunchPrompt(f, &prompt, nil)
	dispatchKeepAlive = kaBoolPtr(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("keep-alive without RC must not fail the dispatch: %v", err)
	}
	if f.launchCalled != 1 {
		t.Fatalf("the worker must still launch; launchCalled = %d", f.launchCalled)
	}
	if prompt != "do a thing" {
		t.Fatalf("no arming without RC; prompt = %q, want the task unchanged", prompt)
	}
	if !strings.Contains(stderr, keepAliveNonRCWarning) {
		t.Fatalf("expected the non-RC warning on stderr, got %q", stderr)
	}
}

// Flag unset => nothing injected; the launched prompt is byte-identical to the
// task and no keep-alive warning appears, even with RC on.
func TestDispatch_KeepAlive_Unset_PromptUnchanged(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var prompt string
	captureLaunchPrompt(f, &prompt, nil)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "do a thing" {
		t.Fatalf("without --keep-alive the prompt must be byte-identical; got %q", prompt)
	}
	if strings.Contains(stderr, keepAliveNonRCWarning) {
		t.Fatalf("no keep-alive warning expected when not requested, got %q", stderr)
	}
}

// Explicit --keep-alive=false => no arming and no warning, even with RC on.
func TestDispatch_KeepAlive_FlagFalse_NoArm(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var prompt string
	captureLaunchPrompt(f, &prompt, nil)
	dispatchKeepAlive = kaBoolPtr(false)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prompt != "do a thing" {
		t.Fatalf("--keep-alive=false must not arm; prompt = %q", prompt)
	}
	if strings.Contains(stderr, keepAliveNonRCWarning) {
		t.Fatalf("an explicit off is not a non-RC condition; no warning expected, got %q", stderr)
	}
}
