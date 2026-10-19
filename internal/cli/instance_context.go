package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// instanceContextCmd is the SessionStart hook niwa writes into the instance
// root's settings, registered for the startup, resume, clear and compact
// sources so its facts come back after compaction. It prints the instance's
// layout -- what the directory is, its repos, how worktrees are named -- as
// SessionStart additionalContext. Outside an instance it prints nothing.
var instanceContextCmd = &cobra.Command{
	Use:           "context",
	Short:         "Internal: print session-start context for an instance root (SessionStart hook)",
	Hidden:        true,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// The hook payload carries nothing this needs; drain it so the
		// harness never blocks writing to a closed pipe.
		_, _ = io.Copy(io.Discard, io.LimitReader(cmd.InOrStdin(), guardStdinLimit))
		return writeInstanceContext(cmd.OutOrStdout(), workspace.HookAnchor())
	},
}

func init() {
	instanceCmd.AddCommand(instanceContextCmd)
}

// writeInstanceContext writes the SessionStart JSON for the instance at or
// above dir, or nothing when dir is in no instance.
func writeInstanceContext(w io.Writer, dir string) error {
	if dir == "" {
		return nil
	}
	instanceRoot, err := workspace.DiscoverHookInstance(dir)
	if err != nil {
		return nil
	}
	text, err := workspace.BuildRootContext(instanceRoot)
	if err != nil {
		return fmt.Errorf("niwa: error: building session context: %w", err)
	}
	var inj sessionStartInjection
	inj.HookSpecificOutput.HookEventName = hookEventSessionStart
	inj.HookSpecificOutput.AdditionalContext = text
	encoded, err := json.Marshal(inj)
	if err != nil {
		return fmt.Errorf("niwa: error: encoding session context: %w", err)
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}
