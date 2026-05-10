package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// CLI mirror of the niwa_redelegate MCP tool added in Issue 7. Closes
// the operator-facing recovery gap surfaced by the UX research: previously
// the only way to redelegate was to launch a Claude session and call the
// MCP tool from there.

var (
	taskRedelegateTo            string
	taskRedelegateSessionID     string
	taskRedelegateReadOnly      bool
	taskRedelegateReadOnlySet   bool // tracks whether --read-only was explicitly passed
	taskRedelegateMode          string
	taskRedelegateExpiresAt     string
	taskRedelegateBodyOverrides string
)

var taskRedelegateCmd = &cobra.Command{
	Use:   "redelegate <source-task-id>",
	Short: "Re-fire a previously-delegated task body without rewriting it",
	Long: `Re-fire a previously-delegated task body.

Source state may be any of queued/running/completed/abandoned/cancelled.
The source task's state is unchanged — active sources keep running, the
new task runs independently. The new envelope carries
` + "`redelegated_from: <source>`" + ` for the audit chain.

When the source task's envelope.json is missing (the rare taskstore_lost
recreate-stub case), pass --body-overrides to supply the new body
explicitly.

Example:
  niwa task redelegate ab12cd34 --to web --read-only
  niwa task redelegate ab12cd34 --body-overrides '{"kind":"retry"}'
  niwa task redelegate ab12cd34 --body-overrides @body.json --session-id ffeeddcc`,
	Args: cobra.ExactArgs(1),
	RunE: runTaskRedelegate,
	PreRun: func(cmd *cobra.Command, args []string) {
		taskRedelegateReadOnlySet = cmd.Flags().Changed("read-only")
	},
}

func init() {
	taskCmd.AddCommand(taskRedelegateCmd)
	taskRedelegateCmd.Flags().StringVar(&taskRedelegateTo, "to", "",
		"override target role; defaults to source.to.role")
	taskRedelegateCmd.Flags().StringVar(&taskRedelegateSessionID, "session-id", "",
		"override session (8 lowercase hex chars); defaults to source.session_id")
	taskRedelegateCmd.Flags().BoolVar(&taskRedelegateReadOnly, "read-only", false,
		"override routing to the main clone; defaults to source.read_only")
	taskRedelegateCmd.Flags().StringVar(&taskRedelegateMode, "mode", "async",
		"\"async\" (default) or \"sync\"")
	taskRedelegateCmd.Flags().StringVar(&taskRedelegateExpiresAt, "expires-at", "",
		"optional RFC3339 expiry deadline; not propagated from source")
	taskRedelegateCmd.Flags().StringVar(&taskRedelegateBodyOverrides, "body-overrides", "",
		"shallow-merge into source.body; pass JSON inline or @<path> to read from file")
}

func runTaskRedelegate(cmd *cobra.Command, args []string) error {
	sourceTaskID := args[0]
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	overrides, err := loadBodyOverrides(taskRedelegateBodyOverrides)
	if err != nil {
		return fmt.Errorf("--body-overrides: %w", err)
	}

	srv := mcp.New("coordinator", instanceRoot)
	srv.SetDaemonFuncs(makeDaemonStarter(), workspace.TerminateDaemon)

	req := map[string]any{
		"source_task_id": sourceTaskID,
	}
	if taskRedelegateTo != "" {
		req["to"] = taskRedelegateTo
	}
	if taskRedelegateSessionID != "" {
		req["session_id"] = taskRedelegateSessionID
	}
	if taskRedelegateReadOnlySet {
		req["read_only"] = taskRedelegateReadOnly
	}
	if overrides != nil {
		req["body_overrides"] = overrides
	}
	req["mode"] = taskRedelegateMode
	if taskRedelegateExpiresAt != "" {
		req["expires_at"] = taskRedelegateExpiresAt
	}
	reqBytes, _ := json.Marshal(req)

	result := srv.RedelegateDirect(reqBytes)
	if result.IsError {
		return renderMCPError(result.Content[0].Text)
	}

	// Pretty-print key fields on stdout.
	var resp map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		// Pass through the raw response if it's not parseable JSON.
		fmt.Fprintln(cmd.OutOrStdout(), result.Content[0].Text)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "task_id: %v\n", resp["task_id"])
	fmt.Fprintf(cmd.OutOrStdout(), "redelegated_from: %v\n", resp["redelegated_from"])
	fmt.Fprintf(cmd.OutOrStdout(), "source_state_at_fork: %v\n", resp["source_state_at_fork"])
	if state, ok := resp["state"]; ok {
		// Sync mode terminal-state response.
		fmt.Fprintf(cmd.OutOrStdout(), "state: %v\n", state)
	}
	if forkLabel := resp["source_state_at_fork"]; forkLabel == "queued" || forkLabel == "running" {
		fmt.Fprintln(cmd.OutOrStdout(),
			"note: forked an active source — both the source and the new task may run to completion in parallel.")
	}
	return nil
}

// loadBodyOverrides parses the --body-overrides flag value. Supports:
//   - "" (nil — no overrides)
//   - inline JSON object
//   - @<path> reads the file at path and parses it as JSON
func loadBodyOverrides(raw string) (map[string]json.RawMessage, error) {
	if raw == "" {
		return nil, nil
	}
	var data []byte
	if strings.HasPrefix(raw, "@") {
		path := raw[1:]
		fileData, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading file %q: %w", path, err)
		}
		data = fileData
	} else {
		data = []byte(raw)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return out, nil
}
