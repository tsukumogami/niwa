package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countTerminalEntries returns how many entries in env name SetupTerminalEnv.
func countTerminalEntries(env []string) int {
	n := 0
	for _, kv := range env {
		if kv == SetupTerminalEnv || strings.HasPrefix(kv, SetupTerminalEnv+"=") {
			n++
		}
	}
	return n
}

func TestSetupTerminalEnv_PresentOnlyWhenSignalled(t *testing.T) {
	if got := setupTerminalEnv(false); len(got) != 0 {
		t.Errorf("setupTerminalEnv(false) = %v, want nothing", got)
	}
	got := setupTerminalEnv(true)
	if len(got) != 1 || got[0] != "NIWA_SETUP_TERMINAL=1" {
		t.Errorf("setupTerminalEnv(true) = %v, want [NIWA_SETUP_TERMINAL=1]", got)
	}
}

func TestCloneAndWorktreeSetupEnv_CarryTheSignalOnlyWhenAsked(t *testing.T) {
	cases := []struct {
		name     string
		env      []string
		wantSeen int
	}{
		{"clone without terminal", cloneSetupEnv("/root", false), 0},
		{"clone with terminal", cloneSetupEnv("/root", true), 1},
		{"worktree without terminal", worktreeSetupEnv("/root", "/wt", "app", "p", "b", false), 0},
		{"worktree with terminal", worktreeSetupEnv("/root", "/wt", "app", "p", "b", true), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countTerminalEntries(tc.env); got != tc.wantSeen {
				t.Errorf("%d %s entries in %v, want %d", got, SetupTerminalEnv, tc.env, tc.wantSeen)
			}
			// The anchor is unaffected by the flag.
			var sawAnchor bool
			for _, kv := range tc.env {
				if kv == "NIWA_INSTANCE_ROOT=/root" {
					sawAnchor = true
				}
			}
			if !sawAnchor {
				t.Errorf("NIWA_INSTANCE_ROOT missing from %v", tc.env)
			}
		})
	}
}

func TestSetupScriptEnv_StripsInheritedTerminalSignal(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"NIWA_SETUP_TERMINAL=1",
		"HOME=/home/x",
		"NIWA_SETUP_TERMINAL=",
		"NIWA_SETUP_TERMINAL",
		"NIWA_SETUP_TERMINAL_OTHER=keep",
	}

	got := setupScriptEnv(base, nil)
	if n := countTerminalEntries(got); n != 0 {
		t.Errorf("inherited %s survived: %v", SetupTerminalEnv, got)
	}
	for _, keep := range []string{"PATH=/usr/bin", "HOME=/home/x", "NIWA_SETUP_TERMINAL_OTHER=keep"} {
		var found bool
		for _, kv := range got {
			if kv == keep {
				found = true
			}
		}
		if !found {
			t.Errorf("unrelated entry %q was dropped: %v", keep, got)
		}
	}

	// When the caller signals, exactly one entry -- its own -- reaches the script.
	got = setupScriptEnv(base, setupTerminalEnv(true))
	if n := countTerminalEntries(got); n != 1 || got[len(got)-1] != "NIWA_SETUP_TERMINAL=1" {
		t.Errorf("want exactly the caller's signal, got %v", got)
	}
}

// TestRunSetupScripts_TerminalSignalReachesScript asserts against what a real
// script sees, with an inherited value planted in the parent environment, so
// the filter is proven on the path that runs scripts rather than only on the
// helper.
func TestRunSetupScripts_TerminalSignalReachesScript(t *testing.T) {
	t.Setenv(SetupTerminalEnv, "1")

	cases := []struct {
		name  string
		extra []string
		want  string
	}{
		{"no extra env", nil, "T=unset"},
		{"clone env without terminal", cloneSetupEnv("/root", false), "T=unset"},
		{"clone env with terminal", cloneSetupEnv("/root", true), "T=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := t.TempDir()
			writeWorktreeSetupScript(t, repoDir, "01-env.sh",
				"#!/bin/sh\necho \"T=${NIWA_SETUP_TERMINAL-unset}\" > ./term.txt\n")

			var buf strings.Builder
			result := RunSetupScripts(repoDir, "scripts/setup", NewReporter(&buf), nil, tc.extra...)
			for _, s := range result.Scripts {
				if s.Error != nil {
					t.Fatalf("setup script %s failed: %v", s.Name, s.Error)
				}
			}
			raw, err := os.ReadFile(filepath.Join(repoDir, "term.txt"))
			if err != nil {
				t.Fatalf("script did not run: %v", err)
			}
			if got := strings.TrimSpace(string(raw)); got != tc.want {
				t.Errorf("script saw %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunSetupScripts_OutputStillScannedWithTerminal pins that the terminal
// signal changes nothing about output handling: stdout and stderr still arrive
// line-prefixed through the reporter.
func TestRunSetupScripts_OutputStillScannedWithTerminal(t *testing.T) {
	repoDir := t.TempDir()
	writeWorktreeSetupScript(t, repoDir, "01-say.sh",
		"#!/bin/sh\necho out-line\necho err-line >&2\n")

	var buf strings.Builder
	RunSetupScripts(repoDir, "scripts/setup", NewReporter(&buf), nil, cloneSetupEnv("/root", true)...)

	out := buf.String()
	prefix := "[" + filepath.Base(repoDir) + "/01-say.sh] "
	for _, want := range []string{prefix + "out-line", prefix + "err-line"} {
		if !strings.Contains(out, want) {
			t.Errorf("reporter output missing %q; got:\n%s", want, out)
		}
	}
}

// TestApplyToWorktree_ForwardsSetupTerminal covers the worktree surface: the
// option reaches the script, and its absence leaves the variable unset even
// when the parent environment carries one.
func TestApplyToWorktree_ForwardsSetupTerminal(t *testing.T) {
	t.Setenv(SetupTerminalEnv, "1")
	for _, tc := range []struct {
		terminal bool
		want     string
	}{
		{false, "T=unset"},
		{true, "T=1"},
	} {
		cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
		optIn(cfg, "app")
		writeWorktreeSetupScript(t, worktreePath, "01-env.sh",
			"#!/bin/sh\necho \"T=${NIWA_SETUP_TERMINAL-unset}\" > ./term.txt\n")

		if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app",
			"purpose", "branch", WorktreeApplyOptions{SetupTerminal: tc.terminal}); err != nil {
			t.Fatalf("ApplyToWorktree: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(worktreePath, "term.txt"))
		if err != nil {
			t.Fatalf("setup script did not run: %v", err)
		}
		if got := strings.TrimSpace(string(raw)); got != tc.want {
			t.Errorf("SetupTerminal=%v: script saw %q, want %q", tc.terminal, got, tc.want)
		}
	}
}
