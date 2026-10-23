package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(lineageCmd)
	lineageCmd.AddCommand(newLineageShowCmd(), newLineageListCmd())
	// A usage error in this group exits 2, apart from a missing or unreadable
	// entry's 1, as in the authority group.
	lineageCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
}

// The schemas of the documents `lineage show --json` and `lineage list
// --json` print.
const (
	lineageChainSchemaV1 = "niwa.lineage.chain/v1"
	lineageListSchemaV1  = "niwa.lineage.list/v1"
)

// lineageNotOnRecord is the error a chain level carries for a parent
// dispatch id with no entry.
const lineageNotOnRecord = "not on record"

var lineageCmd = &cobra.Command{
	Use:   "lineage",
	Short: "Read the record of who dispatched each session",
	Long: `Read the dispatch lineage record.

Every niwa dispatch writes one lineage entry, keyed by its dispatch id, into
<workspace root>/.niwa-lineage/. The entry outlives the instance: reaping,
resetting or destroying the instance leaves it in place. show walks one
entry's chain of parent dispatches; list prints every entry.

Only "observed" is observed: in a parent session source, a parent dispatch
source and a principal's trust, every other value, including "mapped" and
values this niwa doesn't know, prints followed by "(not observed)". A link of
"none" means there is no parent and prints alone.`,
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

// lineageSelectors are show's selectors. The *Set fields say whether a flag
// was given at all, so an empty -C is told apart from no -C.
type lineageSelectors struct {
	Session, Instance, Dir          string
	SessionSet, InstanceSet, DirSet bool
}

func newLineageShowCmd() *cobra.Command {
	var sel lineageSelectors
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show [<dispatch id>]",
		Short: "Show a dispatch's lineage chain",
		Long: `Show a dispatch's lineage entry and then each ancestor reached through its
parent dispatch ids, whatever their source, nearest first: for each level the
instance, agent, origin, when it was written, the worker's session id, the
parent session and parent dispatch links with how niwa came by each, and the
principal. A level whose parent session id is set prints "directed by <id>".

The entry is selected by one of: a dispatch id as the argument; --session,
the entries whose worker session id it is; --instance, the entries for that
instance name; otherwise the instance containing -C's directory or the
working directory, by its authority record's dispatch id, or, with no record,
by its instance name ("selected by instance name"). When several entries
match, the chain starts from the latest written, ties going to the lower
dispatch id, and the other matches are listed.

The walk stops at an entry with no parent ("root"), a parent with no entry
("<id> not on record"), an entry that can't be read ("<id> unreadable:
<reason>"), one of a later version ("<id> later version <schema>"), an id
already printed ("stopped: cycle at <id>") or 64 levels ("stopped: 64
levels").

--json prints one ` + lineageChainSchemaV1 + ` document, every value as stored.

Exit codes: 0 printed (a selected entry of a later version included), 1 no
lineage entry, an unreadable selected entry, a bad -C, an unreadable store or
outside a workspace, 2 usage.`,
		Args: usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			sel.SessionSet = cmd.Flags().Changed("session")
			sel.InstanceSet = cmd.Flags().Changed("instance")
			sel.DirSet = cmd.Flags().Changed("dir")
			return runLineageShow(cmd.OutOrStdout(), args, sel, jsonOut)
		},
	}
	cmd.Flags().StringVar(&sel.Session, "session", "", "start from the entries whose worker session id is this")
	cmd.Flags().StringVar(&sel.Instance, "instance", "", "start from the entries for the instance with this name")
	cmd.Flags().StringVarP(&sel.Dir, "dir", "C", "", "find the instance from this absolute directory instead of the working directory")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the chain as one "+lineageChainSchemaV1+" JSON document")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
	return cmd
}

func newLineageListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every lineage entry in the workspace",
		Long: `List every lineage entry in the workspace, latest written first, ties broken
by dispatch id, one line each: the dispatch id, the worker session id, the
parent session with its source, and the principal. An entry that can't be
read lists as its dispatch id and error, and the rest still list.

--json prints one ` + lineageListSchemaV1 + ` document, every value as stored.

Exit codes: 0 listed (nothing, with no store), 1 the store can't be read or
outside a workspace, 2 usage.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLineageList(cmd.OutOrStdout(), jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the entries as one "+lineageListSchemaV1+" JSON document")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
	return cmd
}

// lineageRoot finds the workspace root containing dir.
func lineageRoot(dir string) (string, error) {
	class, err := workspace.ClassifyCwd(dir)
	if err != nil {
		return "", readError("classifying %s: %v", strconv.Quote(dir), err)
	}
	if class.WorkspaceRoot == "" {
		return "", readError("not inside a niwa workspace: %s", strconv.Quote(dir))
	}
	return class.WorkspaceRoot, nil
}

// lineageSelection is what a selector matched: the matching entries, latest
// first, and whether they came from the instance-name fallback.
type lineageSelection struct {
	matches  []workspace.LineageListResult
	fallback bool
}

var errNoLineageEntry = readError("no lineage entry")

// selectLineage applies R13: one selector, or the instance containing the
// anchor directory.
func selectLineage(root, anchor string, args []string, sel lineageSelectors) (lineageSelection, error) {
	switch {
	case len(args) == 1:
		stored, found, err := workspace.ReadLineageEntry(root, args[0])
		if !found {
			// A malformed id names no file, so it matches nothing.
			return lineageSelection{}, errNoLineageEntry
		}
		return lineageSelection{matches: []workspace.LineageListResult{{DispatchID: args[0], Entry: stored, Err: err}}}, nil
	case sel.SessionSet:
		if !authority.ValidSessionID(sel.Session) {
			return lineageSelection{}, readError("invalid --session %s: pass a session id", strconv.Quote(sel.Session))
		}
		return matchLineage(root, false, func(e lineage.Entry) bool { return e.SessionID == sel.Session })
	case sel.InstanceSet:
		return matchLineage(root, false, func(e lineage.Entry) bool { return e.Instance == sel.Instance })
	}
	found, ok, err := workspace.FindInstance(anchor)
	if err != nil {
		return lineageSelection{}, readError("finding the instance containing %s: %v", strconv.Quote(anchor), err)
	}
	if !ok {
		return lineageSelection{}, errNoLineageEntry
	}
	rec, recFound, recErr := authorityReadRecord(found.Instance)
	if recErr == nil && recFound {
		return selectLineage(root, anchor, []string{rec.DispatchID}, lineageSelectors{})
	}
	// No record, or one that can't be read: a reused instance name can
	// match an older dispatch, so the fallback says so.
	name := filepath.Base(found.Instance)
	return matchLineage(root, true, func(e lineage.Entry) bool { return e.Instance == name })
}

// matchLineage lists the store and keeps the readable v1 entries match
// accepts, latest first.
func matchLineage(root string, fallback bool, match func(lineage.Entry) bool) (lineageSelection, error) {
	all, err := workspace.ListLineageEntries(root)
	if err != nil {
		return lineageSelection{}, readError("%v", err)
	}
	var out []workspace.LineageListResult
	for _, r := range all {
		if r.Err == nil && !r.Entry.Parsed.LaterVersion && match(r.Entry.Parsed.Entry) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return lineageSelection{}, errNoLineageEntry
	}
	sortLineageLatestFirst(out)
	return lineageSelection{matches: out, fallback: fallback}, nil
}

// sortLineageLatestFirst orders results by written_at, latest first, ties
// to the lower dispatch id; a result with no readable written_at sorts after
// every one that has it.
func sortLineageLatestFirst(rs []workspace.LineageListResult) {
	at := func(r workspace.LineageListResult) (time.Time, bool) {
		if r.Err != nil || r.Entry.Parsed.LaterVersion {
			return time.Time{}, false
		}
		t, err := time.Parse(time.RFC3339, r.Entry.Parsed.Entry.WrittenAt)
		return t, err == nil
	}
	sort.SliceStable(rs, func(i, j int) bool {
		ti, oki := at(rs[i])
		tj, okj := at(rs[j])
		switch {
		case oki != okj:
			return oki
		case oki && !ti.Equal(tj):
			return ti.After(tj)
		}
		return rs[i].DispatchID < rs[j].DispatchID
	})
}

func runLineageShow(w io.Writer, args []string, sel lineageSelectors, jsonOut bool) error {
	n := len(args)
	for _, set := range []bool{sel.SessionSet, sel.InstanceSet, sel.DirSet} {
		if set {
			n++
		}
	}
	if n > 1 {
		return usageError("a dispatch id, --session, --instance and -C each select the entry; pass at most one")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return readError("getting working directory: %v", err)
	}
	anchor := cwd
	if sel.DirSet {
		if err := checkAnchorDir(sel.Dir); err != nil {
			return err
		}
		anchor = sel.Dir
	}
	root, err := lineageRoot(anchor)
	if err != nil {
		return err
	}
	s, err := selectLineage(root, anchor, args, sel)
	if err != nil {
		return err
	}

	selected := s.matches[0]
	var chain lineage.Chain
	switch {
	case selected.Err != nil:
		chain = lineage.Chain{Stop: lineage.StopUnreadable, Levels: []lineage.Level{{DispatchID: selected.DispatchID, Lookup: lineage.Lookup{Err: selected.Err}}}}
	case selected.Entry.Parsed.LaterVersion:
		chain = lineage.Chain{Stop: lineage.StopLaterVersion, Levels: []lineage.Level{{DispatchID: selected.DispatchID, Lookup: lookupFromStored(selected.Entry)}}}
	default:
		chain = lineage.Walk(selected.Entry.Parsed.Entry, func(id string) lineage.Lookup {
			stored, found, err := workspace.ReadLineageEntry(root, id)
			switch {
			case err != nil:
				return lineage.Lookup{Err: err}
			case !found:
				return lineage.Lookup{}
			}
			return lookupFromStored(stored)
		})
		chain.Levels[0].Raw = selected.Entry.Raw
	}

	ids := make([]string, len(s.matches))
	for i, m := range s.matches {
		ids[i] = m.DispatchID
	}
	if jsonOut {
		if err := writeLineageChainJSON(w, chain, ids, s.fallback); err != nil {
			return readError("%v", err)
		}
	} else {
		writeLineageChainText(w, chain, ids, s.fallback)
	}
	if selected.Err != nil {
		return readError("%s unreadable: %v", selected.DispatchID, selected.Err)
	}
	return nil
}

func lookupFromStored(stored workspace.StoredLineageEntry) lineage.Lookup {
	if stored.Parsed.LaterVersion {
		return lineage.Lookup{LaterVersion: stored.Parsed.Entry.Schema}
	}
	e := stored.Parsed.Entry
	return lineage.Lookup{Entry: &e, Raw: stored.Raw}
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

// lineageListDoc is the document `lineage list --json` prints.
type lineageListDoc struct {
	Schema  string            `json:"schema"`
	Entries []json.RawMessage `json:"entries"`
}

// lineageLevelJSON is one level's object: a readable entry exactly as
// stored, its members neither dropped nor interpreted; a later version's id
// and schema with "known": false; or an id and an error.
func lineageLevelJSON(id string, l lineage.Lookup) (json.RawMessage, error) {
	var b []byte
	var err error
	switch {
	case l.Err != nil:
		b, err = json.Marshal(struct {
			DispatchID string `json:"dispatch_id"`
			Error      string `json:"error"`
		}{id, l.Err.Error()})
	case l.LaterVersion != "":
		b, err = json.Marshal(struct {
			DispatchID string `json:"dispatch_id"`
			Schema     string `json:"schema"`
			Known      bool   `json:"known"`
		}{id, l.LaterVersion, false})
	case l.Entry == nil:
		b, err = json.Marshal(struct {
			DispatchID string `json:"dispatch_id"`
			Error      string `json:"error"`
		}{id, lineageNotOnRecord})
	default:
		b = l.Raw
	}
	if err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, b); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func writeLineageJSON(w io.Writer, doc any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func writeLineageChainJSON(w io.Writer, c lineage.Chain, matches []string, fallback bool) error {
	doc := lineageChainDoc{
		Schema:               lineageChainSchemaV1,
		Chain:                []json.RawMessage{},
		Stop:                 c.Stop,
		Matched:              len(matches),
		Matches:              matches,
		InstanceNameFallback: fallback,
	}
	for _, l := range c.Levels {
		b, err := lineageLevelJSON(l.DispatchID, l.Lookup)
		if err != nil {
			return fmt.Errorf("encoding the lineage chain: %w", err)
		}
		doc.Chain = append(doc.Chain, b)
	}
	if err := writeLineageJSON(w, doc); err != nil {
		return fmt.Errorf("encoding the lineage chain: %w", err)
	}
	return nil
}

func writeLineageChainText(w io.Writer, c lineage.Chain, matches []string, fallback bool) {
	if len(matches) > 1 {
		fmt.Fprintf(w, "%d entries matched; showing the latest\n", len(matches))
		for _, id := range matches[1:] {
			fmt.Fprintf(w, "  also matched: %s\n", id)
		}
	}
	if fallback {
		fmt.Fprintln(w, "selected by instance name")
	}
	for _, l := range c.Levels {
		switch {
		case l.Err != nil:
			fmt.Fprintf(w, "%s unreadable: %s\n", l.DispatchID, l.Err)
		case l.LaterVersion != "":
			fmt.Fprintf(w, "%s later version %s\n", l.DispatchID, lineageText(l.LaterVersion))
		case l.Entry == nil:
			fmt.Fprintf(w, "%s %s\n", l.DispatchID, lineageNotOnRecord)
		default:
			writeLineageEntryText(w, *l.Entry)
		}
	}
	switch c.Stop {
	case lineage.StopCycle:
		fmt.Fprintf(w, "stopped: cycle at %s\n", c.CycleID)
	case lineage.StopDepth:
		fmt.Fprintf(w, "stopped: %d levels\n", lineage.MaxWalkLevels)
	}
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
	fmt.Fprintf(w, "  principal:       %s\n", lineagePrincipalText(e.Principal))
	if e.ParentSession.ID != "" {
		fmt.Fprintf(w, "  directed by %s\n", lineageText(e.ParentSession.ID))
	}
}

// lineageSourceText prints a link source or a trust value, followed by "(not
// observed)" unless it is observed (R18).
func lineageSourceText(v string) string {
	if lineage.IsObserved(v) {
		return lineageText(v)
	}
	return lineageText(v) + " (not observed)"
}

// lineageLinkText prints a link: its id and source, or "none" alone when the
// link's source says there is no parent.
func lineageLinkText(l lineage.Link) string {
	if l.Source == lineage.SourceNone {
		if l.ID == "" {
			return "none"
		}
		return lineageText(l.ID) + " none"
	}
	s := lineageOrNone(l.ID) + " " + lineageSourceText(l.Source)
	if l.MappedSessionID != "" {
		s += ", mapped session " + lineageText(l.MappedSessionID)
	}
	return s
}

// lineagePrincipalText prints the principal: its name, its source as a
// provenance label with no annotation, and its trust.
func lineagePrincipalText(p lineage.Principal) string {
	return lineageOrNone(p.Name) + ", source " + lineageText(p.Source) + ", trust " + lineageSourceText(p.Trust)
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

func runLineageList(w io.Writer, jsonOut bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return readError("getting working directory: %v", err)
	}
	root, err := lineageRoot(cwd)
	if err != nil {
		return err
	}
	rs, err := workspace.ListLineageEntries(root)
	if err != nil {
		return readError("%v", err)
	}
	sortLineageLatestFirst(rs)
	if jsonOut {
		doc := lineageListDoc{Schema: lineageListSchemaV1, Entries: []json.RawMessage{}}
		for _, r := range rs {
			l := lineage.Lookup{Err: r.Err}
			if r.Err == nil {
				l = lookupFromStored(r.Entry)
			}
			b, err := lineageLevelJSON(r.DispatchID, l)
			if err != nil {
				return readError("encoding the lineage list: %v", err)
			}
			doc.Entries = append(doc.Entries, b)
		}
		if err := writeLineageJSON(w, doc); err != nil {
			return readError("encoding the lineage list: %v", err)
		}
		return nil
	}
	for _, r := range rs {
		switch {
		case r.Err != nil:
			fmt.Fprintf(w, "%s  unreadable: %s\n", r.DispatchID, r.Err)
		case r.Entry.Parsed.LaterVersion:
			fmt.Fprintf(w, "%s  later version %s\n", r.DispatchID, lineageText(r.Entry.Parsed.Entry.Schema))
		default:
			e := r.Entry.Parsed.Entry
			fmt.Fprintln(w, strings.Join([]string{
				e.DispatchID,
				"session " + lineageOrNone(e.SessionID),
				"parent session " + lineageLinkText(e.ParentSession),
				"principal " + lineageOrNone(e.Principal.Name),
			}, "  "))
		}
	}
	return nil
}
