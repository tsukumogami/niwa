package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(lineageCmd)
	lineageCmd.AddCommand(lineageShowCmd)
	lineageShowCmd.Flags().BoolVar(&lineageShowJSON, "json", false,
		"print the chain as one "+lineageChainSchemaV1+" JSON document")
	// A usage error in this group exits 2, apart from a missing or unreadable
	// entry's 1, as in the authority group.
	lineageCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
}

// lineageChainSchemaV1 is the schema of the document `lineage show --json`
// prints.
const lineageChainSchemaV1 = "niwa.lineage.chain/v1"

var lineageShowJSON bool

var lineageCmd = &cobra.Command{
	Use:   "lineage",
	Short: "Read the record of who dispatched each session",
	Long: `Read the dispatch lineage record.

Every niwa dispatch writes one lineage entry, keyed by its dispatch id, into
<workspace root>/.niwa-lineage/. The entry outlives the instance: reaping,
resetting or destroying the instance leaves it in place. show reads one entry
back.`,
}

var lineageShowCmd = &cobra.Command{
	Use:   "show <dispatch id>",
	Short: "Show a dispatch's lineage entry",
	Long: `Show the lineage entry of the dispatch whose id is given: its instance, agent,
origin, when it was written, the worker's session id, the parent session and
parent dispatch links with how niwa came by each, and the principal.

--json prints one ` + lineageChainSchemaV1 + ` document.

Exit codes: 0 printed (an entry of a later version included), 1 no lineage
entry, an unreadable entry or outside a workspace, 2 usage.`,
	Args: usageArgs(cobra.ExactArgs(1)),
	RunE: runLineageShow,
}

// createLineageEntryFunc writes the entry at (8a). It is a variable so a test
// can make the write fail and watch the rollback.
var createLineageEntryFunc = workspace.CreateLineageEntry

// completeLineageEntryFunc records the worker's session id in the entry
// after (10). It is a variable so a test can make the completion fail.
var completeLineageEntryFunc = workspace.CompleteLineageEntry

// dispatchLineageEntry is the entry a dispatch writes: its identity, and the
// parent links and principal (2e) resolved.
func dispatchLineageEntry(dispatchID, instance, agentName string, now time.Time, auth *dispatchAuthority) lineage.Entry {
	return lineage.Entry{
		Schema:         lineage.SchemaEntryV1,
		DispatchID:     dispatchID,
		Instance:       instance,
		Agent:          agentName,
		Origin:         lineage.OriginDispatch,
		WrittenAt:      now.UTC().Format(time.RFC3339),
		ParentSession:  auth.parentSession,
		ParentDispatch: auth.parentDispatch,
		Principal:      auth.principal,
	}
}

// lineageChainDoc is the document `lineage show --json` prints.
type lineageChainDoc struct {
	Schema               string            `json:"schema"`
	Chain                []json.RawMessage `json:"chain"`
	Stop                 string            `json:"stop"`
	Matched              int               `json:"matched"`
	Matches              []string          `json:"matches"`
	InstanceNameFallback bool              `json:"instance_name_fallback"`
}

func runLineageShow(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return &exitCodeError{code: 1, err: fmt.Errorf("getting working directory: %w", err)}
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return &exitCodeError{code: 1, err: fmt.Errorf("classifying working directory: %w", err)}
	}
	if class.WorkspaceRoot == "" {
		return &exitCodeError{code: 1, err: errors.New("not inside a niwa workspace")}
	}
	id := args[0]
	stored, found, err := workspace.ReadLineageEntry(class.WorkspaceRoot, id)
	if !found {
		// A malformed id names no file, so it matches nothing.
		return &exitCodeError{code: 1, err: errors.New("no lineage entry")}
	}
	out := cmd.OutOrStdout()
	if err != nil {
		if lineageShowJSON {
			level, _ := json.Marshal(struct {
				DispatchID string `json:"dispatch_id"`
				Error      string `json:"error"`
			}{id, err.Error()})
			if werr := writeLineageChain(out, id, level, "unreadable"); werr != nil {
				return &exitCodeError{code: 1, err: werr}
			}
			return &exitCodeError{code: 1, err: fmt.Errorf("%s unreadable: %w", id, err)}
		}
		fmt.Fprintf(out, "%s unreadable: %s\n", id, err)
		return &exitCodeError{code: 1, err: fmt.Errorf("%s unreadable: %w", id, err)}
	}

	if stored.Parsed.LaterVersion {
		if lineageShowJSON {
			level, _ := json.Marshal(struct {
				DispatchID string `json:"dispatch_id"`
				Schema     string `json:"schema"`
				Known      bool   `json:"known"`
			}{id, stored.Parsed.Entry.Schema, false})
			if err := writeLineageChain(out, id, level, "later-version"); err != nil {
				return &exitCodeError{code: 1, err: err}
			}
			return nil
		}
		fmt.Fprintf(out, "%s later version %s\n", id, lineageText(stored.Parsed.Entry.Schema))
		return nil
	}

	if lineageShowJSON {
		if err := writeLineageChain(out, id, stored.Raw, "root"); err != nil {
			return &exitCodeError{code: 1, err: err}
		}
		return nil
	}
	writeLineageEntryText(out, stored.Parsed.Entry)
	return nil
}

// writeLineageChain prints a one-level chain document. The level is printed
// as given: a readable entry exactly as stored, its members neither dropped
// nor interpreted.
func writeLineageChain(w io.Writer, id string, level []byte, stop string) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, level); err != nil {
		return fmt.Errorf("encoding the lineage chain: %w", err)
	}
	doc := lineageChainDoc{
		Schema:  lineageChainSchemaV1,
		Chain:   []json.RawMessage{compact.Bytes()},
		Stop:    stop,
		Matched: 1,
		Matches: []string{id},
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("encoding the lineage chain: %w", err)
	}
	return nil
}

// writeLineageEntryText prints one entry's fields, one per line.
func writeLineageEntryText(w io.Writer, e lineage.Entry) {
	fmt.Fprintf(w, "dispatch %s\n", e.DispatchID)
	fmt.Fprintf(w, "  instance:        %s\n", lineageText(e.Instance))
	fmt.Fprintf(w, "  agent:           %s\n", lineageText(e.Agent))
	fmt.Fprintf(w, "  origin:          %s\n", lineageText(e.Origin))
	fmt.Fprintf(w, "  written at:      %s\n", lineageText(e.WrittenAt))
	fmt.Fprintf(w, "  session:         %s\n", lineageOrNone(e.SessionID))
	fmt.Fprintf(w, "  parent session:  %s\n", lineageLinkText(e.ParentSession))
	fmt.Fprintf(w, "  parent dispatch: %s\n", lineageLinkText(e.ParentDispatch))
	fmt.Fprintf(w, "  principal:       %s (source %s, trust %s)\n",
		lineageOrNone(e.Principal.Name), lineageText(e.Principal.Source), lineageText(e.Principal.Trust))
}

func lineageLinkText(l lineage.Link) string {
	if l.ID == "" {
		return "none (source " + lineageText(l.Source) + ")"
	}
	return lineageText(l.ID) + " (source " + lineageText(l.Source) + ")"
}

func lineageOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return lineageText(s)
}

// lineageText makes a stored value safe to print on a terminal: any value
// holding something other than printable ASCII is printed quoted and
// escaped, since any same-user process can write the store.
func lineageText(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return strconv.QuoteToASCII(s)
		}
	}
	return s
}
