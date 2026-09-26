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
		"emit a JSON array of {name, path, ephemeral, accepts_session_messages[, keep_alive][, session_name]} records, one per instance")
}

var listJSON bool

// The markers the human output appends to an instance's name. Keep-alive
// comes first when both apply, so the "<name> (keep-alive)" prefix that
// existing readers match stays unchanged.
const (
	keepAliveMarker              = " (keep-alive)"
	acceptsSessionMessagesMarker = " (accepts session messages)"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace instances",
	Long: `List the instances under the current workspace root.

Run from inside a workspace (at the root or inside any instance); list
resolves the workspace root from the current directory and enumerates every
instance beneath it.

An instance whose dispatch forwarded a session name is followed by a
"  session name: <name>" line: the name the session answers to in Agent
View and to its peers, as recorded when it was dispatched.

An instance backed by a dispatched session is followed by the command that
steps back into that session, so the handle survives the terminal that
printed it.

With --json, emits a JSON array of {name, path, ephemeral,
accepts_session_messages} records, where ephemeral marks instances backed by
an ephemeral session mapping.

accepts_session_messages is on every record. It is true when niwa launched
the instance's dispatched session set to accept messages from other sessions
without an approval prompt, and false otherwise. It is reported whether or
not that session is still running, for as long as the instance exists;
turning the machine setting off later does not change it. A true record
shows an "(accepts session messages)" marker in the human output.

An instance whose session was dispatched with keep-alive armed and is still
live additionally carries keep_alive:true (and a "(keep-alive)" marker in
the human output, placed before the "(accepts session messages)" marker when
both apply). An instance whose dispatch recorded a session name additionally
carries session_name, the same value the session name: line shows.`,
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
		line := r.Name
		if r.KeepAlive {
			line += keepAliveMarker
		}
		if r.AcceptsSessionMessages {
			line += acceptsSessionMessagesMarker
		}
		fmt.Fprintln(cmd.OutOrStdout(), line)
		// The name the session answers to, as the dispatch recorded it. It
		// was validated against the forwarded-name shape when the join filled
		// it, so what prints here is never raw mapping content.
		if r.SessionName != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  session name: %s\n", r.SessionName)
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
// It sets each record's AcceptsSessionMessages flag when any mapping pointing
// at the instance recorded the behavior, with NO liveness check. The grant was made
// when the session launched, so it is reported for as long as the instance
// exists, including after the session has finished or been deleted.
//
// It returns, keyed by instance path, the command that steps back into the
// session an instance is backed by, for the instances niwa can name one for.
// A store read failure degrades to no annotation and no commands, which leaves
// both flags false and the name empty; list must stay usable with a partially
// written store.
//
// It also fills each record's SessionName from the mapping with the latest
// Created time for that instance. The value is checked against
// dispatchSessionNamePattern before it is used, because mapping files are
// writable by any same-user process and the name reaches a terminal; a value
// that fails the check counts as absent. There is no fallback to an older
// mapping's name: the newest mapping is the session currently backing the
// instance, and an older name belongs to a session it replaced. The resume
// command deliberately keeps its own, pre-existing selection (the last
// mapping, in session-id order, that yields a non-empty resume command), so an
// instance with several mappings can show a name and a resume command from
// different ones; changing resume is out of scope for the name.
func annotateFromSessionMappings(records []workspace.InstanceRecord, workspaceRoot, jobsDir string, now time.Time) map[string]string {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil || len(mappings) == 0 {
		return nil
	}
	keptAlive := make(map[string]bool)
	accepting := make(map[string]bool)
	resume := make(map[string]string)
	// newest is the latest-Created mapping per instance path. It is chosen
	// before its name is checked: a newer mapping whose name is empty or fails
	// the check hides an older one's rather than falling back to it. Ties keep
	// the first in session-id order.
	newest := make(map[string]workspace.SessionMapping)
	for _, m := range mappings {
		if m.KeepAlive && sessionLive(jobsDir, m.SessionID, now) {
			keptAlive[m.InstancePath] = true
		}
		if m.AcceptsSessionMessages {
			accepting[m.InstancePath] = true
		}
		if cmdline := sessionResumeCommand(m); cmdline != "" {
			resume[m.InstancePath] = cmdline
		}
		if cur, ok := newest[m.InstancePath]; !ok || m.Created.After(cur.Created) {
			newest[m.InstancePath] = m
		}
	}
	for i := range records {
		if keptAlive[records[i].Path] {
			records[i].KeepAlive = true
		}
		if accepting[records[i].Path] {
			records[i].AcceptsSessionMessages = true
		}
		if m, ok := newest[records[i].Path]; ok && dispatchSessionNameRe.MatchString(m.SessionName) {
			records[i].SessionName = m.SessionName
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
