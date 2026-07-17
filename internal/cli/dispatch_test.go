package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// dispatchTestSessionID is a canonical full UUID a fake capture returns; it
// keys the durable mapping.
const dispatchTestSessionID = "abcdef12-3456-7890-abcd-ef1234567890"

// dispatchTestShortID is the SHORT session id a fake capture returns alongside
// the full UUID. It is deliberately NOT the first 8 chars of the UUID so tests
// catch any regression that conflates the two (the short id is the
// `claude attach/logs/stop` handle; the full UUID is the mapping key).
const dispatchTestShortID = "shortid1"

// setupDispatchWorkspace creates a workspace root with .niwa/workspace.toml so
// ClassifyCwd resolves it to CwdAtWorkspaceRoot. It returns the root path.
func setupDispatchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// t.TempDir can hand back a symlinked path (e.g. /var -> /private/var on
	// macOS, or a symlinked TMPDIR on Linux). Resolve it so the workspace root
	// ClassifyCwd derives matches the cwd we chdir into.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := "[workspace]\nname = \"test-ws\"\n"
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// chdir changes into dir for the duration of the test and restores the previous
// cwd on cleanup.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// dispatchFakes captures the state the dispatch seams record across a run.
type dispatchFakes struct {
	provisionCalled int
	launchCalled    int
	captureCalled   int
	attachCalled    int
	destroyCalled   int
	attachedID      string
	destroyedPath   string
	instancePath    string
}

// installDispatchFakes wires every dispatch seam to a fake and resets the
// command flags to their zero values, restoring all originals on cleanup. The
// returned struct records calls. By default: lookClaude succeeds, provision
// creates a real temp instance dir under workspaceRoot, launch succeeds,
// capture returns dispatchTestSessionID, attach succeeds.
func installDispatchFakes(t *testing.T, workspaceRoot string) *dispatchFakes {
	t.Helper()
	f := &dispatchFakes{}

	prevLook := lookClaude
	prevProvision := provisionInstanceFunc
	prevLaunch := dispatchLaunch
	prevCapture := dispatchCapture
	prevAttach := dispatchAttach
	prevDestroy := destroyInstanceFunc
	prevLabel := dispatchLabel
	prevName := dispatchName
	prevModel := dispatchModel
	prevPerm := dispatchPermissionMode
	prevAgent := dispatchAgent
	prevDetach := dispatchDetach
	prevKeepAlive := dispatchKeepAlive

	dispatchLabel = ""
	dispatchName = ""
	dispatchModel = ""
	dispatchPermissionMode = ""
	dispatchAgent = ""
	dispatchDetach = false
	dispatchKeepAlive = nil

	lookClaude = func() (string, error) { return "/usr/bin/claude", nil }

	provisionInstanceFunc = func(_ context.Context, root, _, namePrefix, sep string) (provisionResult, error) {
		f.provisionCalled++
		name := "test-ws" + sep + namePrefix
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		f.instancePath = dir
		return provisionResult{Name: name, Path: dir}, nil
	}

	dispatchLaunch = func(_ context.Context, _, _ string, _ []string, _ []string) error {
		f.launchCalled++
		return nil
	}

	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		f.captureCalled++
		return dispatchTestSessionID, dispatchTestShortID, nil
	}

	dispatchAttach = func(id string) error {
		f.attachCalled++
		f.attachedID = id
		return nil
	}

	destroyInstanceFunc = func(path string) error {
		f.destroyCalled++
		f.destroyedPath = path
		return nil
	}

	t.Cleanup(func() {
		lookClaude = prevLook
		provisionInstanceFunc = prevProvision
		dispatchLaunch = prevLaunch
		dispatchCapture = prevCapture
		dispatchAttach = prevAttach
		destroyInstanceFunc = prevDestroy
		dispatchLabel = prevLabel
		dispatchName = prevName
		dispatchModel = prevModel
		dispatchPermissionMode = prevPerm
		dispatchAgent = prevAgent
		dispatchDetach = prevDetach
		dispatchKeepAlive = prevKeepAlive
	})

	return f
}

// runDispatchCmd invokes runDispatch with the given prompt, capturing stdout
// and stderr. It uses a fresh cobra.Command so the command's output streams are
// isolated.
func runDispatchCmd(t *testing.T, prompt string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	runErr := runDispatch(cmd, []string{prompt})
	return outBuf.String(), errBuf.String(), runErr
}

func TestDispatch_OutsideWorkspace_Errors(t *testing.T) {
	outside := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(outside); err == nil {
		outside = resolved
	}
	chdir(t, outside)
	f := installDispatchFakes(t, outside)

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error outside a workspace, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision must not be called outside a workspace; called %d times", f.provisionCalled)
	}
}

func TestDispatch_ClaudeNotOnPath_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	lookClaude = func() (string, error) { return "", errors.New("not found") }

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error when claude is absent, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision must not be called when claude is absent; called %d", f.provisionCalled)
	}
}

func TestDispatch_EmptyPrompt_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	_, _, err := runDispatchCmd(t, "")
	if err == nil {
		t.Fatal("expected an error for an empty prompt, got nil")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("nothing must be created for an empty prompt; provision called %d", f.provisionCalled)
	}
}

func TestDispatch_HappyPath_WritesMappingAndAttaches(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchLabel = "my-task"

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Mapping written with the expected provenance.
	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading mapping: %v", err)
	}
	if !m.Ephemeral {
		t.Error("mapping must be ephemeral")
	}
	if m.Origin != "dispatch" {
		t.Errorf("Origin = %q, want dispatch", m.Origin)
	}
	if m.Label != "my-task" {
		t.Errorf("Label = %q, want my-task", m.Label)
	}
	if m.InstancePath != f.instancePath {
		t.Errorf("InstancePath = %q, want %q", m.InstancePath, f.instancePath)
	}

	// Pending-marker removed after the durable mapping.
	if _, statErr := os.Stat(filepath.Join(f.instancePath, dispatchPendingMarker)); !os.IsNotExist(statErr) {
		t.Errorf("pending-marker should be removed on success; stat err = %v", statErr)
	}

	// The mapping stays keyed on the FULL UUID even though attach uses the short
	// id: ReadSessionMapping by the full UUID above succeeded, so this confirms
	// the key did not switch to the short id.
	if m.SessionID != dispatchTestSessionID {
		t.Errorf("mapping SessionID = %q, want the full UUID %q", m.SessionID, dispatchTestSessionID)
	}

	// Attach called exactly once with the SHORT id (claude attach is keyed on
	// the short id, not the full UUID).
	if f.attachCalled != 1 {
		t.Errorf("attach called %d times, want 1", f.attachCalled)
	}
	if f.attachedID != dispatchTestShortID {
		t.Errorf("attached id = %q, want the short id %q (not the full UUID)", f.attachedID, dispatchTestShortID)
	}
	if f.destroyCalled != 0 {
		t.Errorf("instance must not be destroyed on success; destroy called %d", f.destroyCalled)
	}
	// The headline prints the full UUID; the claude hints print the short id.
	if !bytes.Contains([]byte(stdout), []byte(dispatchTestSessionID)) {
		t.Errorf("stdout should print the full session id; got %q", stdout)
	}
	if !bytes.Contains([]byte(stdout), []byte("claude attach "+dispatchTestShortID)) {
		t.Errorf("stdout should print the management hints keyed on the short id; got %q", stdout)
	}
}

func TestDispatch_Detach_SkipsAttach(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.attachCalled != 0 {
		t.Errorf("--detach must skip attach; attach called %d", f.attachCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr != nil {
		t.Errorf("mapping must still be written under --detach: %v", mErr)
	}
}

func TestDispatch_AttachFailure_NonFatal(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchAttach = func(id string) error {
		f.attachCalled++
		f.attachedID = id
		return errors.New("session already exited")
	}

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("attach failure must be non-fatal; got err %v", err)
	}
	if f.destroyCalled != 0 {
		t.Errorf("attach failure must not roll back; destroy called %d", f.destroyCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr != nil {
		t.Errorf("mapping must survive an attach failure: %v", mErr)
	}
	if _, statErr := os.Stat(filepath.Join(f.instancePath, dispatchPendingMarker)); !os.IsNotExist(statErr) {
		t.Errorf("marker must still be gone after attach failure; stat err = %v", statErr)
	}
	if !bytes.Contains([]byte(stderr), []byte("warning")) {
		t.Errorf("expected a non-fatal warning on stderr; got %q", stderr)
	}
}

func TestDispatch_Rollback_LaunchFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchLaunch = func(_ context.Context, _, _ string, _ []string, _ []string) error {
		f.launchCalled++
		return errors.New("launch boom")
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on launch failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("launch failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
	if f.destroyedPath != f.instancePath {
		t.Errorf("destroyed path = %q, want %q", f.destroyedPath, f.instancePath)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr == nil {
		t.Error("no mapping must remain after a launch-failure rollback")
	}
}

func TestDispatch_Rollback_CaptureFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		f.captureCalled++
		return "", "", errors.New("capture timeout")
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on capture failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("capture failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
	if _, mErr := workspace.ReadSessionMapping(root, dispatchTestSessionID); mErr == nil {
		t.Error("no mapping must remain after a capture-failure rollback")
	}
}

func TestDispatch_Rollback_MappingWriteFailure(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	// Force a mapping-write failure by having capture return an invalid id;
	// WriteSessionMapping rejects a non-UUID session id without writing.
	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		f.captureCalled++
		return "not-a-uuid", "shortid1", nil
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("expected an error on mapping-write failure")
	}
	if f.destroyCalled != 1 {
		t.Errorf("mapping-write failure must destroy the instance; destroy called %d", f.destroyCalled)
	}
}

func TestDispatch_SelfDispatch_ResolvesEnclosingWorkspaceRoot(t *testing.T) {
	root := setupDispatchWorkspace(t)
	// Create a real instance under the root and chdir inside it, so ClassifyCwd
	// returns CwdInsideInstance whose WorkspaceRoot is the enclosing root.
	instanceDir := makeReapInstance(t, root, "test-ws-existing")
	chdir(t, instanceDir)
	f := installDispatchFakes(t, root)

	var gotRoot string
	prev := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string) (provisionResult, error) {
		gotRoot = r
		return prev(ctx, r, cwd, namePrefix, sep)
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRoot != root {
		t.Errorf("provision invoked with root %q, want enclosing workspace root %q", gotRoot, root)
	}
	if f.provisionCalled != 1 {
		t.Errorf("provision called %d times, want 1", f.provisionCalled)
	}
}

func TestDispatch_OverLongPrompt_Errors(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	big := bytes.Repeat([]byte("a"), maxPromptBytes+1)
	_, _, err := runDispatchCmd(t, string(big))
	if err == nil {
		t.Fatal("expected an error for an over-limit prompt")
	}
	if f.provisionCalled != 0 {
		t.Errorf("nothing must be created for an over-limit prompt; provision called %d", f.provisionCalled)
	}
}

// TestDispatch_SessionStartGuard_NoOpsInsideDispatchInstance asserts that the
// existing SessionStart re-entrancy guard no-ops against a dispatch-created
// instance: a dispatch instance is a genuine, valid niwa instance (it carries
// .niwa/instance.json), so a SessionStart hook whose launch cwd is inside it
// fails guard step 3 and provisions no second instance. This proves the
// dispatch path composes with the existing hook path without the hook code
// changing (Issue 6 AC, R39/R40). It exercises the guard helper directly rather
// than running a full hook, which is the simpler equivalent check.
func TestDispatch_SessionStartGuard_NoOpsInsideDispatchInstance(t *testing.T) {
	root := setupHookWorkspace(t, true) // ephemeral-session mode on
	jobsDir := t.TempDir()

	// A dispatch-created instance is shaped exactly like makeReapInstance's
	// output: a real instance dir under the workspace root carrying
	// .niwa/instance.json. Its name follows the disp-<hex> convention.
	instanceDir := makeReapInstance(t, root, "test-ws-disp-abc12345")

	// The session is a genuine background worker (so guards 1 and 2 pass); only
	// the re-entrancy guard (3) should be what makes this a no-op.
	const sid = "aabbccdd-eeff-1122-3344-556677889900"
	writeJobState(t, jobsDir, sid, sid, bgJobTemplate)

	// At the workspace root the guard passes (a fresh dispatch would provision).
	if !sessionStartGuardPasses(root, root, sid, jobsDir) {
		t.Fatalf("guard unexpectedly failed at the workspace root; expected it to pass there")
	}

	// With the launch cwd INSIDE the dispatch instance, the re-entrancy guard
	// must make it a no-op -- no second instance is provisioned.
	if sessionStartGuardPasses(root, instanceDir, sid, jobsDir) {
		t.Fatalf("re-entrancy guard did not no-op inside a dispatch instance; it would nest a second instance")
	}
}

// TestDispatch_Concurrent_DistinctMappings runs N dispatches concurrently
// against a single workspace root with a fake provision that returns distinct
// temp instance dirs and a fake capture that returns distinct UUIDs. It asserts
// that all N produce distinct, intact ephemeral dispatch-origin mappings, none
// clobbered, and the mapping store parses cleanly afterward (R36/R37 -- the
// crypto/rand name suffix plus per-session-id mapping files mean concurrent
// dispatches do not collide).
func TestDispatch_Concurrent_DistinctMappings(t *testing.T) {
	const n = 12

	root := setupDispatchWorkspace(t)
	chdir(t, root) // cwd is constant for all goroutines; no per-goroutine chdir
	installDispatchFakes(t, root)
	dispatchDetach = true // no attach in the fan-out path

	// Override the launch seam with a goroutine-safe no-op. The default fake from
	// installDispatchFakes mutates shared dispatchFakes counters without
	// synchronization, which would be a data race under concurrent dispatch; this
	// test asserts on the durable mappings instead of those counters.
	dispatchLaunch = func(_ context.Context, _, _ string, _ []string, _ []string) error { return nil }
	destroyInstanceFunc = func(_ string) error { return nil }

	// A goroutine-safe provision: each call mints a distinct instance dir under
	// the workspace root using the unique namePrefix dispatch generated.
	var provisionCount int64
	provisionInstanceFunc = func(_ context.Context, r, _, namePrefix, sep string) (provisionResult, error) {
		atomic.AddInt64(&provisionCount, 1)
		name := "test-ws" + sep + namePrefix
		dir := filepath.Join(r, name)
		if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		return provisionResult{Name: name, Path: dir}, nil
	}

	// A goroutine-safe capture handing back a distinct valid UUID per call.
	var captureSeq int64
	dispatchCapture = func(_, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		i := atomic.AddInt64(&captureSeq, 1)
		// 12 distinct, well-formed lowercase UUIDs differing only in the final
		// hex digit (i is 1..n, single hex digit covers n <= 15). A distinct
		// short id accompanies each so the mapping key (full UUID) and the
		// user-facing handle (short id) stay separable.
		return fmt.Sprintf("00000000-0000-0000-0000-00000000000%x", i), fmt.Sprintf("short%x", i), nil
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var outBuf, errBuf bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&outBuf)
			cmd.SetErr(&errBuf)
			cmd.SetContext(context.Background())
			errs[idx] = runDispatch(cmd, []string{fmt.Sprintf("task %d", idx)})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("dispatch %d failed: %v", i, err)
		}
	}

	// The store parses, and every mapping is ephemeral + dispatch-origin.
	mappings, err := workspace.ListSessionMappings(root)
	if err != nil {
		t.Fatalf("listing session mappings after concurrent dispatch: %v", err)
	}
	if len(mappings) != n {
		t.Fatalf("got %d mappings, want %d (one per dispatch, none clobbered)", len(mappings), n)
	}

	seenSession := make(map[string]bool, n)
	seenInstance := make(map[string]bool, n)
	for _, m := range mappings {
		if !m.Ephemeral {
			t.Errorf("mapping %s not ephemeral", m.SessionID)
		}
		if m.Origin != "dispatch" {
			t.Errorf("mapping %s origin = %q, want dispatch", m.SessionID, m.Origin)
		}
		if seenSession[m.SessionID] {
			t.Errorf("duplicate session id %s", m.SessionID)
		}
		seenSession[m.SessionID] = true
		if seenInstance[m.InstancePath] {
			t.Errorf("two mappings share instance path %s (a clobber)", m.InstancePath)
		}
		seenInstance[m.InstancePath] = true
	}

	if got := atomic.LoadInt64(&provisionCount); got != n {
		t.Errorf("provision called %d times, want %d", got, n)
	}
}

func TestDispatch_PassthroughFlags_DiscreteArgv(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	// Isolate the host global config so this test does not pick up the
	// developer's real ~/.config/niwa/config.toml (e.g. a
	// remote_control_on_dispatch=true default would otherwise append a
	// --settings element and make the passthrough assertion non-hermetic).
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchModel = "sonnet"
	dispatchPermissionMode = "acceptEdits"
	dispatchAgent = "reviewer"
	dispatchDetach = true

	var gotPass []string
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string, _ []string) error {
		gotPass = passthrough
		return nil
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--model", "sonnet", "--permission-mode", "acceptEdits", "--agent", "reviewer"}
	if len(gotPass) != len(want) {
		t.Fatalf("passthrough = %v, want %v", gotPass, want)
	}
	for i := range want {
		if gotPass[i] != want[i] {
			t.Fatalf("passthrough[%d] = %q, want %q (full %v)", i, gotPass[i], want[i], gotPass)
		}
	}
}

// TestSanitizeInstanceSlug exercises the slug normalization rules: lowercasing,
// collapsing non-[a-z0-9] runs to a single underscore, trimming leading/trailing
// underscores, capping length (re-trimming an exposed trailing underscore), and
// returning "" when nothing usable remains. The separator is an underscore so a
// user-typed dash collapses to "_" and the slug stays dash-free.
func TestSanitizeInstanceSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"spaces and punctuation", "My Feature!", "my_feature"},
		{"underscores and double hyphens", "  __foo--bar__  ", "foo_bar"},
		{"sentence with mixed case", "Refactor the AuthZ layer", "refactor_the_authz_layer"},
		{"user-typed dash collapses to underscore", "auth-layer", "auth_layer"},
		{"only punctuation", "!!!", ""},
		{"empty", "", ""},
		{"non-ascii dropped", "café", "caf"},
		{"already an underscore slug", "fix_bug_123", "fix_bug_123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeInstanceSlug(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeInstanceSlug(%q) = %q, want %q", tc.in, got, tc.want)
			}
			assertSlugShape(t, got)
		})
	}
}

// TestSanitizeInstanceSlug_CapsLength verifies a very long input is capped to
// maxDispatchSlugRunes with no trailing underscore exposed by the cut.
func TestSanitizeInstanceSlug_CapsLength(t *testing.T) {
	// A run of words longer than the cap, with a hyphen-producing boundary
	// positioned so a naive cut would leave a trailing hyphen.
	long := ""
	for i := 0; i < 60; i++ {
		long += "ab "
	}
	got := sanitizeInstanceSlug(long)
	if len([]rune(got)) > maxDispatchSlugRunes {
		t.Fatalf("slug length = %d runes, want <= %d (%q)", len([]rune(got)), maxDispatchSlugRunes, got)
	}
	assertSlugShape(t, got)
}

// assertSlugShape asserts a slug only ever contains [a-z0-9_], never a dash, and
// never leads or trails with an underscore (an empty slug is allowed).
//
// The dash-free property is LOAD-BEARING for isDispatchInstanceName: the dispatch
// signature regex ("\+[a-z0-9_]*-[0-9a-f]{8}$") treats the "-" before the 8 hex
// as the sole dash after the "+". If a slug could contain a dash, that structural
// signature would be ambiguous. This helper pins the invariant.
func assertSlugShape(t *testing.T, slug string) {
	t.Helper()
	if slug == "" {
		return
	}
	// Explicit dash check first: this is the invariant isDispatchInstanceName
	// depends on, so assert it unambiguously rather than only via the charset loop.
	if strings.Contains(slug, "-") {
		t.Fatalf("slug %q must NEVER contain a dash (isDispatchInstanceName's structural signature relies on slugs being dash-free)", slug)
	}
	for _, r := range slug {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_') {
			t.Fatalf("slug %q contains illegal rune %q (only [a-z0-9_] allowed; dashes are forbidden)", slug, r)
		}
	}
	if strings.HasPrefix(slug, "_") || strings.HasSuffix(slug, "_") {
		t.Fatalf("slug %q must not lead or trail with an underscore", slug)
	}
}

// TestDispatch_Name_SlugInInstanceAndSession verifies that --name "My Thing"
// (1) produces an instance name that contains the underscore slug AND still ends
// with the structural "-<8hex>" signature isDispatchInstanceName recognizes (the
// end-anchored regex is unaffected by underscores inside the slug), and (2)
// forwards "--name my_thing" to the launched worker.
func TestDispatch_Name_SlugInInstanceAndSession(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchName = "My Thing"

	var gotName string
	prevProvision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string) (provisionResult, error) {
		res, err := prevProvision(ctx, r, cwd, namePrefix, sep)
		gotName = res.Name
		return res, err
	}

	var gotPass []string
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string, _ []string) error {
		f.launchCalled++
		gotPass = passthrough
		return nil
	}
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(gotName, "my_thing") {
		t.Errorf("instance name %q should contain the slug %q", gotName, "my_thing")
	}
	// The config name and slug are joined with "+", and the suffix ends in the
	// mandatory "-<8hex>", so the full shape is "<config>+<slug>-<8hex>".
	if !regexp.MustCompile(`^test-ws\+my_thing-[0-9a-f]{8}$`).MatchString(gotName) {
		t.Errorf("instance name %q should be <config>+my_thing-<8hex>", gotName)
	}
	if !isDispatchInstanceName(gotName) {
		t.Errorf("instance name %q must still match isDispatchInstanceName (preserve the +<slug>-<8hex> signature)", gotName)
	}
	if !dispatchInstanceNameRe.MatchString(gotName) {
		t.Errorf("instance name %q must still end with -<8hex> after a +slug", gotName)
	}
	// The dispatch signature is purely structural (regex "\+[a-z0-9_]*-[0-9a-f]{8}$"):
	// a "+", an optional dash-free slug, a "-", then exactly 8 hex. It matches both
	// the no-name "<config>+-<8hex>" and the named "<config>+<slug>-<8hex>", and
	// excludes hook-, create-, and developer-shaped names. The create-with-a-
	// hex-shaped-slug case ("tsuku+deadbeef") is the critical FALSE: it proves a
	// named-create cannot masquerade as dispatch, because its slug is dash-free so
	// there is no "-" before the hex.
	matches := map[string]bool{
		"tsuku+-deadbeef":         true,  // no-name dispatch (<config>+-<8hex>)
		"tsuku+my_thing-4e33acfa": true,  // named dispatch (<config>+<slug>-<8hex>)
		"tsuku":                   false, // create first instance
		"tsuku-2":                 false, // developer numbered instance
		"tsuku+my_feature":        false, // create --name (<config>+<slug>, no -<8hex>)
		"tsuku+deadbeef":          false, // create --name with a HEX-SHAPED slug (no "-" before hex)
		"tsuku-9333f04fbe4e":      false, // hook 12-hex (<config>-<sessionhex>, no "+")
	}
	for name, want := range matches {
		if got := isDispatchInstanceName(name); got != want {
			t.Errorf("isDispatchInstanceName(%q) = %v, want %v", name, got, want)
		}
	}

	if !passthroughHasNameSlug(gotPass, "my_thing") {
		t.Errorf("launcher passthrough %v should contain \"--name my_thing\"", gotPass)
	}
}

// TestDispatch_NoName_NoSlugNoNameFlag verifies that without --name the instance
// name carries no slug (it is exactly "<config>+-<8hex>") and no "--name" is
// forwarded to the worker -- the original behavior, unchanged.
func TestDispatch_NoName_NoSlugNoNameFlag(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	var gotName string
	prevProvision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string) (provisionResult, error) {
		res, err := prevProvision(ctx, r, cwd, namePrefix, sep)
		gotName = res.Name
		return res, err
	}

	var gotPass []string
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string, _ []string) error {
		f.launchCalled++
		gotPass = passthrough
		return nil
	}
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With config "test-ws" and no slug, the shape is "test-ws+-<8hex>" -- "+" is
	// the end-of-config marker, immediately followed by the suffix's "-<8hex>".
	if !regexp.MustCompile(`^test-ws\+-[0-9a-f]{8}$`).MatchString(gotName) {
		t.Errorf("instance name %q should be exactly <config>+-<8hex> with no slug", gotName)
	}
	for i, a := range gotPass {
		if a == "--name" {
			t.Errorf("no --name must be forwarded without the flag; passthrough[%d] = %q (full %v)", i, a, gotPass)
		}
	}
}

// TestDispatch_NameSanitizesEmpty_FallsBack verifies that --name "!!!" (which
// sanitizes to "") falls back to the slug-less behavior: no slug in the instance
// name and no "--name" forwarded.
func TestDispatch_NameSanitizesEmpty_FallsBack(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchName = "!!!"

	var gotName string
	prevProvision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string) (provisionResult, error) {
		res, err := prevProvision(ctx, r, cwd, namePrefix, sep)
		gotName = res.Name
		return res, err
	}

	var gotPass []string
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string, _ []string) error {
		f.launchCalled++
		gotPass = passthrough
		return nil
	}
	dispatchDetach = true

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !regexp.MustCompile(`^test-ws\+-[0-9a-f]{8}$`).MatchString(gotName) {
		t.Errorf("an empty-sanitizing --name must fall back; name %q should be <config>+-<8hex>", gotName)
	}
	for i, a := range gotPass {
		if a == "--name" {
			t.Errorf("an empty-sanitizing --name must forward no --name; passthrough[%d] = %q (full %v)", i, a, gotPass)
		}
	}
}

// passthroughHasNameSlug reports whether pass contains the discrete pair
// "--name" immediately followed by slug.
func passthroughHasNameSlug(pass []string, slug string) bool {
	for i := 0; i+1 < len(pass); i++ {
		if pass[i] == "--name" && pass[i+1] == slug {
			return true
		}
	}
	return false
}
