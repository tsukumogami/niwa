package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listJSON, "json", false,
		"emit a JSON array of {name, path, ephemeral} records, one per instance")
}

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace instances",
	Long: `List the instances under the current workspace root.

Run from inside a workspace (at the root or inside any instance); list
resolves the workspace root from the current directory and enumerates every
instance beneath it.

With --json, emits a JSON array of {name, path, ephemeral} records, where
ephemeral marks instances backed by an ephemeral session mapping.`,
	Args: cobra.NoArgs,
	RunE: runList,
}

func runList(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	if class.Class == workspace.CwdOutside {
		return fmt.Errorf("not inside a niwa workspace or instance")
	}

	records, err := workspace.EnumerateInstanceRecords(class.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}

	if listJSON {
		// Always emit a JSON array (never null) so consumers can iterate
		// unconditionally, even when no instances exist.
		if records == nil {
			records = []workspace.InstanceRecord{}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		if err := enc.Encode(records); err != nil {
			return fmt.Errorf("encoding list JSON: %w", err)
		}
		return nil
	}

	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No instances found.")
		return nil
	}
	for _, r := range records {
		fmt.Fprintln(cmd.OutOrStdout(), r.Name)
	}
	return nil
}
