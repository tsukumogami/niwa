package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listJSON, "json", false,
		"emit a JSON array of {name, path, ephemeral[, keep_alive]} records, one per instance")
}

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace instances",
	Long: `List the instances under the current workspace root.

Run from inside a workspace (at the root or inside any instance); list
resolves the workspace root from the current directory and enumerates every
instance beneath it.

An instance backed by a dispatched session is followed by the command that
steps back into that session, so the handle survives the terminal that
printed it.

With --json, emits a JSON array of {name, path, ephemeral} records, where
ephemeral marks instances backed by an ephemeral session mapping. An
instance whose session was dispatched with keep-alive armed and is still
live additionally carries keep_alive:true (and a "(keep-alive)" marker in
the human output).`,
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
	resume := annotateFromSessionMappings(records, class.WorkspaceRoot, defaultJobsDir(), time.Now())

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
		if r.KeepAlive {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (keep-alive)\n", r.Name)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), r.Name)
		}
		// A dispatched session's handle is printed once, by the dispatch that
		// created it, and then lives in scrollback. For an agent that will not
		// hand over a session mid-turn the terminal never attached in the first
		// place, so resuming later is the only way the session is ever reached
		// -- and the developer has to still have the terminal that started it.
		// This is the second place to look, and it is the one they already run
		// to see what is here.
		if cmdline := resume[r.Path]; cmdline != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  resume: %s\n", cmdline)
		}
	}
	return nil
}

// annotateFromSessionMappings joins the instance records against the
// workspace's session mapping store, which is opened once and answers two
// questions at the same time.
//
// It fills each record's KeepAlive flag from the Claude Code job-entry liveness
// signal: an instance is reported kept-alive when some mapping points at it
// with KeepAlive recorded AND that mapping's session is still live (its job
// entry exists, the same rule the reaper uses). A kept-alive session that has
// since been deleted reports nothing -- its self-wake died with the session,
// so the report reflects sessions being kept alive NOW, not past opt-ins.
//
// It returns, keyed by instance path, the command that steps back into the
// session an instance is backed by, for the instances niwa can name one for.
// A store read failure degrades to no annotation and no commands; list must
// stay usable with a partially written store.
func annotateFromSessionMappings(records []workspace.InstanceRecord, workspaceRoot, jobsDir string, now time.Time) map[string]string {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil || len(mappings) == 0 {
		return nil
	}
	keptAlive := make(map[string]bool)
	resume := make(map[string]string)
	for _, m := range mappings {
		if m.KeepAlive && sessionLive(jobsDir, m.SessionID, now) {
			keptAlive[m.InstancePath] = true
		}
		if cmdline := sessionResumeCommand(m); cmdline != "" {
			resume[m.InstancePath] = cmdline
		}
	}
	for i := range records {
		if keptAlive[records[i].Path] {
			records[i].KeepAlive = true
		}
	}
	return resume
}

// sessionResumeCommand returns the command that steps back into the session m
// records, or "" when niwa cannot name one it is sure works.
//
// Every part of it comes from the recorded agent's own launch declaration --
// the binary, the resume verb, and what counts as a handle -- so no agent name
// and no verb is written here. An agent niwa cannot launch, or one that
// declares no way back into a session, yields nothing rather than a guess.
func sessionResumeCommand(m workspace.SessionMapping) string {
	ag, err := agent.ParseAgent(m.Agent)
	if err != nil {
		return ""
	}
	spec, ok := dispatchLaunchSpec(ag)
	if !ok {
		return ""
	}
	handle := m.Handle
	if handle == "" {
		// A mapping written before the handle was recorded. The session id
		// stands in only where the declaration says the two are the same
		// string; for an agent whose verbs take something else, niwa does not
		// have the handle and says nothing rather than printing a command that
		// fails at the binary.
		if spec.Records.Handle != agentplan.HandleSessionID {
			return ""
		}
		handle = m.SessionID
	}
	// The instance directory is what the grant names, and the mapping already
	// records it. A mapping written without one yields a command with no grant
	// rather than one naming nothing -- inherited from the constructor, not
	// re-checked here.
	return reentryCommand(spec, handle, m.InstancePath)
}
