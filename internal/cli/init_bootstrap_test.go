package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/source"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// resetInitFlags clears every package-level init flag so cases do not
// leak state across tests. Mirrors the pattern in executeInit.
func resetInitFlags(t *testing.T) {
	t.Helper()
	prev := struct {
		from             string
		skipGlobal       bool
		overlay          string
		noOverlay        bool
		rebind           bool
		noInstallPlugins bool
		bootstrap        bool
		noBootstrap      bool
	}{
		initFrom, initSkipGlobal, initOverlay, initNoOverlay,
		initRebind, initNoInstallPlugins, initBootstrap, initNoBootstrap,
	}
	t.Cleanup(func() {
		initFrom = prev.from
		initSkipGlobal = prev.skipGlobal
		initOverlay = prev.overlay
		initNoOverlay = prev.noOverlay
		initRebind = prev.rebind
		initNoInstallPlugins = prev.noInstallPlugins
		initBootstrap = prev.bootstrap
		initNoBootstrap = prev.noBootstrap
	})
	initFrom = ""
	initSkipGlobal = false
	initOverlay = ""
	initNoOverlay = false
	initRebind = false
	initNoInstallPlugins = false
	initBootstrap = false
	initNoBootstrap = false
}

// stubMaterialize swaps materializeFromSource with a function that
// always returns the provided error. The original is restored on
// cleanup. Tests use this to inject typed errors (NoMarker, AmbiguousMarkers,
// StatusError, etc.) without setting up a real git source.
func stubMaterialize(t *testing.T, fn func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error)) {
	t.Helper()
	prev := materializeFromSource
	materializeFromSource = fn
	t.Cleanup(func() { materializeFromSource = prev })
}

// stubBootstrap replaces runBootstrap for a test. Returns a pointer
// to a bool flipped to true when the stub fires.
func stubBootstrap(t *testing.T) *bool {
	t.Helper()
	called := false
	prev := runBootstrap
	runBootstrap = func(ctx context.Context, root string, src source.Source) error {
		called = true
		return errors.New("bootstrap step=create: not implemented yet")
	}
	t.Cleanup(func() { runBootstrap = prev })
	return &called
}

// stubTTY makes IsStdinTTY return the requested value for the duration
// of the test.
func stubTTY(t *testing.T, isTTY bool) {
	t.Helper()
	prev := IsStdinTTY
	IsStdinTTY = func() bool { return isTTY }
	t.Cleanup(func() { IsStdinTTY = prev })
}

// chdirTemp creates a temp dir and chdir's into it for the test
// duration, setting XDG_CONFIG_HOME inside it so global-config writes
// stay isolated.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	return dir
}

// runInitErr drives runInit with the given args and flags state
// already set by the caller. The cobra command is reused; flag values
// are read from package-level vars (resetInitFlags handles teardown).
func runInitErr(t *testing.T, args ...string) error {
	t.Helper()
	cmd := initCmd
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		return err
	}
	return cmd.RunE(cmd, cmd.Flags().Args())
}

// TestRunInit_BootstrapMutualExclusion: PRD R25. Passing both
// --bootstrap and --no-bootstrap returns the exact error string and
// the typed InitConflictError carries ExitCode 2.
func TestRunInit_BootstrapMutualExclusion(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)

	initBootstrap = true
	initNoBootstrap = true

	err := runInitErr(t)
	if err == nil {
		t.Fatal("expected error when both --bootstrap and --no-bootstrap are set")
	}
	const want = "--bootstrap and --no-bootstrap are mutually exclusive"
	if err.Error() != want {
		t.Errorf("error text mismatch:\n  got:  %q\n  want: %q", err.Error(), want)
	}
	var ice *workspace.InitConflictError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *workspace.InitConflictError in chain, got %T: %v", err, err)
	}
	if ice.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", ice.ExitCode)
	}
}

// TestRunInit_HostCheckNonGitHub: PRD R9 / R21. Non-GitHub host with
// --bootstrap refuses with the exact stderr string and exit code 3
// BEFORE any materialize / clone call. The materialize stub is wired
// to fail loudly if reached.
func TestRunInit_HostCheckNonGitHub(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)

	called := false
	stubMaterialize(t, func(context.Context, source.Source, string, string, config.MarkerSet, workspace.FetchClient, *workspace.Reporter) (int, error) {
		called = true
		return 0, errors.New("materialize should not have been called")
	})

	initFrom = "gitlab.com/acme/proj"
	initBootstrap = true

	err := runInitErr(t)
	if err == nil {
		t.Fatal("expected error for non-GitHub host")
	}
	if called {
		t.Error("materializeFromSource was called; host check ran AFTER materialize")
	}
	const want = "bootstrap supports only GitHub sources in v1; got host=gitlab.com"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error missing R9 string:\n  got:  %q\n  want substring: %q", err.Error(), want)
	}
	var ice *workspace.InitConflictError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *workspace.InitConflictError, got %T", err)
	}
	if ice.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", ice.ExitCode)
	}
}

// TestRunInit_BootstrapDerivesNameFromSrcRepo: PRD R2. With --bootstrap
// + no positional + --from owner/foo, the workspace name derives from
// src.Repo so the workspace is created at <cwd>/foo/. The bootstrap
// stub fires (NoMarker error), the workspaceCreated defer reclaims the
// directory.
func TestRunInit_BootstrapDerivesNameFromSrcRepo(t *testing.T) {
	dir := chdirTemp(t)
	resetInitFlags(t)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		// Assert configDir reflects the derived name.
		wantDir := filepath.Join(dir, "foo", workspace.StateDir)
		if configDir != wantDir {
			t.Errorf("materialize configDir = %q, want %q (R2 derivation)", configDir, wantDir)
		}
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})
	bootstrapCalled := stubBootstrap(t)

	initFrom = "acme/foo"
	initBootstrap = true

	err := runInitErr(t)
	if err == nil {
		t.Fatal("expected bootstrap stub error")
	}
	if !*bootstrapCalled {
		t.Error("runBootstrap stub was not invoked")
	}
	// Defer should have cleaned up the directory.
	if _, statErr := os.Stat(filepath.Join(dir, "foo")); !os.IsNotExist(statErr) {
		t.Errorf("expected <cwd>/foo to have been removed by R7 defer; stat err: %v", statErr)
	}
}

// TestRunInit_BootstrapPositionalWins: PRD R2. When both a positional
// name AND --from are given with --bootstrap, the positional name
// wins.
func TestRunInit_BootstrapPositionalWins(t *testing.T) {
	dir := chdirTemp(t)
	resetInitFlags(t)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		wantDir := filepath.Join(dir, "bar", workspace.StateDir)
		if configDir != wantDir {
			t.Errorf("materialize configDir = %q, want %q (positional should win over R2)", configDir, wantDir)
		}
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})
	stubBootstrap(t)

	initFrom = "acme/foo"
	initBootstrap = true

	err := runInitErr(t, "bar")
	if err == nil {
		t.Fatal("expected bootstrap stub error")
	}
}

// TestRunInit_NonTTYNoFlagFailFast: PRD R13 row 6. Non-TTY + neither
// flag + NoMarker → exact fail-fast string + ExitCode 4.
func TestRunInit_NonTTYNoFlagFailFast(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)
	stubTTY(t, false)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})

	initFrom = "acme/foo"

	err := runInitErr(t, "myws")
	if err == nil {
		t.Fatal("expected fail-fast error in non-TTY no-flag path")
	}
	const want = "remote has no .niwa/workspace.toml and stdin is not a terminal; re-run with --bootstrap to scaffold"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error missing R13 non-TTY string:\n  got:  %q\n  want substring: %q", err.Error(), want)
	}
	var ice *workspace.InitConflictError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *workspace.InitConflictError, got %T", err)
	}
	if ice.ExitCode != 4 {
		t.Errorf("ExitCode = %d, want 4", ice.ExitCode)
	}
}

// TestRunInit_NoBootstrapDecline: PRD R13 rows 2 and 5. --no-bootstrap
// set + NoMarker → fail-fast with NoMarker text + explicit-decline
// reason + ExitCode 4.
func TestRunInit_NoBootstrapDecline(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)
	stubTTY(t, true) // TTY value irrelevant; --no-bootstrap fires in both rows.

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})

	initFrom = "acme/foo"
	initNoBootstrap = true

	err := runInitErr(t, "myws")
	if err == nil {
		t.Fatal("expected fail-fast error in --no-bootstrap path")
	}
	if !strings.Contains(err.Error(), "--no-bootstrap was set") {
		t.Errorf("error missing decline reason:\n  %s", err.Error())
	}
	var ice *workspace.InitConflictError
	if !errors.As(err, &ice) {
		t.Fatalf("expected *workspace.InitConflictError, got %T", err)
	}
	if ice.ExitCode != 4 {
		t.Errorf("ExitCode = %d, want 4", ice.ExitCode)
	}
}

// TestPromptBootstrap_AcceptsYesYBareEnter exercises the three "yes"
// inputs: bare Enter (empty line), "y", "Y".
func TestPromptBootstrap_AcceptsYesYBareEnter(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"bare-enter", "\n"},
		{"lowercase-y", "y\n"},
		{"uppercase-Y", "Y\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			proceed, err := promptBootstrap(strings.NewReader(tc.input), out)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !proceed {
				t.Error("expected proceed=true for affirmative input")
			}
			if !strings.Contains(out.String(), "[Y/n]") {
				t.Errorf("prompt text missing [Y/n] capitalization:\n%s", out.String())
			}
		})
	}
}

// TestPromptBootstrap_AcceptsNoLowerUpper exercises the two "no"
// inputs.
func TestPromptBootstrap_AcceptsNoLowerUpper(t *testing.T) {
	for _, input := range []string{"n\n", "N\n"} {
		out := &bytes.Buffer{}
		proceed, err := promptBootstrap(strings.NewReader(input), out)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proceed {
			t.Errorf("expected proceed=false for input %q", input)
		}
	}
}

// TestPromptBootstrap_RepromptsOnUnknown verifies the prompt re-asks
// when the user types garbage, then accepts a follow-up Y.
func TestPromptBootstrap_RepromptsOnUnknown(t *testing.T) {
	out := &bytes.Buffer{}
	proceed, err := promptBootstrap(strings.NewReader("what?\nmaybe\ny\n"), out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !proceed {
		t.Error("expected proceed=true after re-prompt loop")
	}
	// The prompt string fired three times (two re-prompts + final).
	if got := strings.Count(out.String(), "Remote has no .niwa/workspace.toml"); got != 3 {
		t.Errorf("prompt printed %d times, want 3 (initial + two re-prompts)", got)
	}
}

// TestRunInit_TTYDeclineExits0: PRD R13 row 3 ("N" branch). TTY user
// types N → runInit returns nil (exit 0) and the workspaceCreated
// defer is disarmed so the directory survives for re-init.
func TestRunInit_TTYDeclineExits0(t *testing.T) {
	dir := chdirTemp(t)
	resetInitFlags(t)
	stubTTY(t, true)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})

	// Wire stdin to "N\n" via cmd.SetIn so promptBootstrap reads it.
	initCmd.SetIn(strings.NewReader("N\n"))
	t.Cleanup(func() { initCmd.SetIn(os.Stdin) })
	stderrBuf := &bytes.Buffer{}
	initCmd.SetErr(stderrBuf)
	t.Cleanup(func() { initCmd.SetErr(os.Stderr) })

	initFrom = "acme/foo"

	err := runInitErr(t, "myws")
	if err != nil {
		t.Fatalf("expected nil error (exit 0) on TTY decline, got: %v", err)
	}
	// Directory should NOT be cleaned up — the user may want to re-init.
	if _, statErr := os.Stat(filepath.Join(dir, "myws")); statErr != nil {
		t.Errorf("workspace dir was removed on clean decline; stat err: %v", statErr)
	}
}

// TestRunInit_TTYAcceptDispatchesBootstrap: PRD R13 row 3 ("Y"
// branch). TTY user types Y → runBootstrap stub fires.
func TestRunInit_TTYAcceptDispatchesBootstrap(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)
	stubTTY(t, true)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		return 0, &config.NoMarkerError{Markers: config.TeamConfigMarkerSet()}
	})
	bootstrapCalled := stubBootstrap(t)

	initCmd.SetIn(strings.NewReader("Y\n"))
	t.Cleanup(func() { initCmd.SetIn(os.Stdin) })
	initCmd.SetErr(&bytes.Buffer{})
	t.Cleanup(func() { initCmd.SetErr(os.Stderr) })

	initFrom = "acme/foo"

	err := runInitErr(t, "myws")
	if err == nil {
		t.Fatal("expected bootstrap stub error")
	}
	if !*bootstrapCalled {
		t.Error("runBootstrap not invoked on TTY-Y dispatch")
	}
	if !strings.Contains(err.Error(), "bootstrap step=create") {
		t.Errorf("expected bootstrap-stub error text, got: %v", err)
	}
}

// TestRunInit_NoBootstrapBaselineNoRegression: PRD R2 negative case.
// `--from owner/foo` (no --bootstrap) with no positional must NOT
// derive a name from src.Repo. The materialize stub is invoked with
// configDir under cwd directly (modeClone no-name path).
func TestRunInit_NoBootstrapBaselineNoRegression(t *testing.T) {
	dir := chdirTemp(t)
	resetInitFlags(t)

	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		wantDir := filepath.Join(dir, workspace.StateDir)
		if configDir != wantDir {
			t.Errorf("materialize configDir = %q, want %q (no-bootstrap baseline unchanged)", configDir, wantDir)
		}
		// Return success with rank=1 so post-flight is satisfied by a
		// dummy workspace.toml we'll write here.
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return 0, err
		}
		return 1, os.WriteFile(filepath.Join(configDir, workspace.WorkspaceConfigFile), []byte("[workspace]\nname = \"derived\"\n"), 0o644)
	})

	initFrom = "acme/foo"
	// No --bootstrap.

	if err := runInitErr(t); err != nil {
		t.Fatalf("baseline init failed: %v", err)
	}
}

// TestRunInit_AmbiguousMarkersFlowsThroughClassifier verifies that
// Issue 1's classifier wiring is reachable from runInit: an
// *config.AmbiguousMarkersError surfaces via the displayConflict
// shape and carries the classifier's ExitCode=1 verbatim.
func TestRunInit_AmbiguousMarkersFlowsThroughClassifier(t *testing.T) {
	chdirTemp(t)
	resetInitFlags(t)
	stubTTY(t, true)

	markers := config.TeamConfigMarkerSet()
	stubMaterialize(t, func(ctx context.Context, src source.Source, sourceURL, configDir string, markers2 config.MarkerSet, fetcher workspace.FetchClient, reporter *workspace.Reporter) (int, error) {
		return 0, &config.AmbiguousMarkersError{Found: markers, Markers: markers}
	})

	initFrom = "acme/foo"
	initBootstrap = true // ambig precedes NoMarker so this should still surface.

	err := runInitErr(t, "myws")
	if err == nil {
		t.Fatal("expected error for ambiguous markers")
	}
	var ice *workspace.InitConflictError
	if !errors.As(err, &ice) {
		t.Fatalf("expected typed InitConflictError, got %T: %v", err, err)
	}
	if ice.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1 (classifier ambig arm)", ice.ExitCode)
	}
}
