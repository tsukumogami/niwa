package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

// TestRootPersistentPreRun_NotShadowed asserts that running a command still
// captures NIWA_RESPONSE_FILE, for every command group that declares its own
// persistent pre-run hook.
//
// Cobra walks up the parent chain and stops at the first persistent pre-run it
// finds; EnableTraverseRunHooks would make it run all of them, and niwa does not
// set it. So a command that declares its own hook replaces the root's instead of
// adding to it, and the root's hook is what calls captureNiwaResponseFile.
//
// This is #281's second cause, and it is invisible from the outside: the command
// succeeds, prints the right worktree path, and the shell silently does not move,
// because writeLandingPath had nothing to write to. It also leaves
// NIWA_RESPONSE_FILE exported to every child process, which is the inheritance
// the root hook exists to prevent (see DESIGN-shell-navigation-protocol.md).
//
// A new command group that declares a persistent pre-run without chaining to
// rootPersistentPreRun fails here rather than in a user's shell.
func TestRootPersistentPreRun_NotShadowed(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"root level", []string{"version"}},
		{"worktree group", []string{"worktree", "list"}},
		{"session alias", []string{"session", "list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A sentinel the root hook must overwrite. Distinguishes "the hook
			// ran and saw an empty variable" from "the hook never ran".
			const sentinel = "SENTINEL-ROOT-HOOK-DID-NOT-RUN"
			niwaResponseFile = sentinel
			t.Cleanup(func() { niwaResponseFile = "" })

			responseFile := filepathJoinTemp(t)
			t.Setenv(niwaResponseFileEnv, responseFile)

			var out, errOut bytes.Buffer
			rootCmd.SetArgs(tc.args)
			rootCmd.SetOut(&out)
			rootCmd.SetErr(&errOut)
			t.Cleanup(func() {
				rootCmd.SetArgs(nil)
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
			})

			// The command itself may fail (no workspace instance here); only the
			// hook's effect matters.
			_ = rootCmd.Execute()

			if niwaResponseFile == sentinel {
				t.Fatalf("niwa %v: rootPersistentPreRun did not run, so NIWA_RESPONSE_FILE "+
					"was never captured; a persistent pre-run on this command group is "+
					"shadowing the root's without chaining to rootPersistentPreRun", tc.args)
			}
			if niwaResponseFile != responseFile {
				t.Errorf("niwa %v: captured %q, want %q", tc.args, niwaResponseFile, responseFile)
			}
			// The hook must also unset the variable so children cannot inherit
			// a writable handle on the shell's cd target.
			if v, ok := os.LookupEnv(niwaResponseFileEnv); ok {
				t.Errorf("niwa %v: %s still set to %q after the hook; child processes would inherit it",
					tc.args, niwaResponseFileEnv, v)
			}
		})
	}
}

// filepathJoinTemp returns a path inside the temp directory, which is where
// validateResponseFilePath requires the response file to live.
func filepathJoinTemp(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "response")
	if err != nil {
		t.Fatalf("creating response file: %v", err)
	}
	defer f.Close()
	return f.Name()
}

// TestRootPersistentPreRun_EveryHookChains walks the command tree and fails on
// any command other than the root that declares a persistent pre-run without
// being listed as a known chainer.
//
// The test above covers the groups that exist today by running them. This one
// covers the group somebody adds tomorrow: it does not need to know what the new
// command does, only that declaring a hook is the thing that silently drops the
// root's. Adding a command here is a deliberate act that points the author at
// rootPersistentPreRun.
func TestRootPersistentPreRun_EveryHookChains(t *testing.T) {
	// Commands whose persistent pre-run is known to call rootPersistentPreRun.
	chainsToRoot := map[string]bool{
		"worktree": true,
	}

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c != rootCmd && (c.PersistentPreRunE != nil || c.PersistentPreRun != nil) {
			if !chainsToRoot[c.Name()] {
				t.Errorf("command %q declares a persistent pre-run, which shadows the root's "+
					"and silently disables NIWA_RESPONSE_FILE capture for it and every "+
					"subcommand (#281). Call rootPersistentPreRun from it, then add %q here.",
					c.Name(), c.Name())
			}
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
}
