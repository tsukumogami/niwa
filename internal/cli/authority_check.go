package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

func init() {
	authorityCmd.AddCommand(newAuthorityCheckCmd())
}

// newAuthorityCheckCmd builds `niwa authority check`. Each call returns a
// command with its own flag state, so a test can run it more than once.
func newAuthorityCheckCmd() *cobra.Command {
	var sessionID, dir string
	var points []string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Say whether a session is its instance's dispatched session, and what it holds",
		Long: `Say whether a session is the session its instance's dispatch launched, and
what that session holds: its grant, its level and, for each --point, who
settles that decision point. This is the one lookup a gate, a coordinator
and the session itself all make, so they read the same answer.

-C names a directory in the instance: its root, a repo inside it, or a niwa
worktree of it. The session is bound when the instance's authority record
names the dispatch that one of these ties the session to, checked in this
order ("binding" in the answer says which):

  mapping   the session mapping dispatch wrote for it, naming this instance
  lineage   before the mapping exists, the dispatch's lineage entry naming
            this instance and session
  records   before dispatch has recorded the session id, and only while its
            capture is under way, the agent's own session records, with this
            session the only one claiming the instance

The answer's state is one of:

  bound            the dispatch's own session; the grant and points apply
  record-unusable  the dispatch's own session, but its record is missing,
                   malformed or names another dispatch
  not-dispatched   any other session, including one started by hand in the
                   instance

--json prints one ` + authority.CheckSchemaV1 + ` document. Treat an authority as
held only when this exits 0 and prints a document with that schema, "state":
"bound" and the authority in its grant.

Exit codes: 0 printed an answer (any state), 1 a -C that is empty, relative,
missing or in no instance, a session id that isn't a lowercase UUID, a
session mapping that can't be read, or the watchdog, each with nothing on
stdout, 2 usage or a point name not matching ` + authority.NamePatternText + `.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("session") {
				return usageError("--session is required")
			}
			if !cmd.Flags().Changed("dir") {
				return usageError("-C is required")
			}
			for _, p := range points {
				if !authority.ValidPoint(p) {
					return usageError("invalid --point %s; a point name matches %s", strconv.Quote(p), authority.NamePatternText)
				}
			}
			return runUnderWatchdog(cmd, func(stdout, _ io.Writer) error {
				doc, err := checkSession(sessionID, dir, points)
				if err != nil {
					return err
				}
				if !jsonOut {
					fmt.Fprint(stdout, doc.Text())
					return nil
				}
				b, err := doc.MarshalJSON()
				if err != nil {
					return readError("encoding the answer: %v", err)
				}
				fmt.Fprintf(stdout, "%s\n", b)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&sessionID, "session", "", "the session id to check (a lowercase UUID)")
	cmd.Flags().StringVarP(&dir, "dir", "C", "", "an absolute directory in the instance to check the session against")
	cmd.Flags().StringArrayVar(&points, "point", nil, "a decision point to answer for a bound session (repeatable)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the answer as one "+authority.CheckSchemaV1+" JSON document")
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
	return cmd
}

// checkSession binds sessionID to the instance holding dir. It reads the
// session's own mapping and the instance's record; with no mapping, the
// lineage entries, the capture-pending file and the agent's session records
// bindBeforeMapping names; and, for a bound session with a parent, the
// parent's lineage entry. It loads no config and writes nothing. The session
// id is checked before any path is built from it.
func checkSession(sessionID, dir string, points []string) (authority.CheckDoc, error) {
	if !workspace.ValidSessionID(sessionID) {
		return authority.CheckDoc{}, readError("invalid --session %s: a session id is a lowercase UUID", strconv.Quote(sessionID))
	}
	if err := checkAnchorDir(dir); err != nil {
		return authority.CheckDoc{}, err
	}
	found, ok, err := workspace.FindInstance(dir)
	if err != nil {
		return authority.CheckDoc{}, readError("finding the instance containing %s: %v", strconv.Quote(dir), err)
	}
	if !ok {
		return authority.CheckDoc{}, readError("-C %s is in no instance of a niwa workspace", strconv.Quote(dir))
	}

	m, err := authorityReadSessionMapping(found.WorkspaceRoot, sessionID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return bindBeforeMapping(sessionID, found, points), nil
		}
		// A mapping that exists but can't be read might be a dispatch's, so
		// no state is claimed for it: the exit 1 a gate reads as a failed
		// lookup, the same answer show gives for it.
		return authority.CheckDoc{}, readError("the session mapping for --session %s can't be read", sessionID)
	}
	// Origin first: a mapping dispatch didn't write is no binding wherever it
	// points, so its reason is hook-mapping even when it names another
	// instance too.
	if m.Origin != "dispatch" {
		return authority.CheckNotDispatched(authority.ReasonHookMapping), nil
	}
	if !mappingNamesInstance(m, found.Instance) {
		return authority.CheckNotDispatched(authority.ReasonOtherInstance), nil
	}
	return bindToRecord(authority.BindingMapping, m.DispatchID, found, points), nil
}

// bindBeforeMapping answers for a session with no mapping. Dispatch writes
// the mapping only after it has captured the worker's session id, and a
// worker's first command can come before that, so two earlier pieces of
// evidence bind it, in the order dispatch writes them:
//
//   - the lineage entry, once dispatch has recorded the session id in it: an
//     entry naming this instance and this session id;
//   - before that, the agent's own session records, but only while the
//     capture-pending file says a capture is under way: exactly one record
//     claiming the instance, and it is this session's. That is the match
//     capture itself is about to make.
//
// Either one ties the session to a dispatch, so the record decides the rest
// as it does for a mapping: a missing or malformed record, or one naming
// another dispatch, is record-unusable. Anything else is not-dispatched.
func bindBeforeMapping(sessionID string, found workspace.FoundInstance, points []string) authority.CheckDoc {
	rec, present, recErr := authorityReadRecord(found.Instance)
	recordUsable := recErr == nil && present
	pending, pendingFound, pendingErr := workspace.ReadCapturePending(found.Instance)
	capturePending := pendingFound && pendingErr == nil

	// The lineage store is keyed by dispatch id, so the entries read first
	// are the ones the record and the pending file name, which is where a
	// worker's entry is. Only when neither names this session is the store
	// scanned: that finds an entry for another dispatch than the record's,
	// which reads record-other-dispatch, and keeps a dispatched session whose
	// record and mapping are both gone at record-unusable rather than
	// not-dispatched.
	var candidates []string
	if recordUsable {
		candidates = append(candidates, rec.DispatchID)
	}
	if capturePending && (!recordUsable || pending.DispatchID != rec.DispatchID) {
		candidates = append(candidates, pending.DispatchID)
	}
	instanceName := filepath.Base(found.Instance)
	names := func(e lineage.Entry) bool {
		return e.Instance == instanceName && e.SessionID == sessionID
	}
	for _, id := range candidates {
		if e, ok := readLineage(found.WorkspaceRoot, id); ok && names(e) {
			return bindToRecord(authority.BindingLineage, id, found, points)
		}
	}
	if id, ok := scanLineageFor(found.WorkspaceRoot, names); ok {
		return bindToRecord(authority.BindingLineage, id, found, points)
	}

	if capturePending && boundByRecords(sessionID, found, pending) {
		return bindToRecord(authority.BindingRecords, pending.DispatchID, found, points)
	}
	return authority.CheckNotDispatched(authority.ReasonNoMapping)
}

// boundByRecords reports whether the agent's session records bind sessionID
// to the pending capture. The window is open only while the dispatch that
// wrote the file is still running: its process is alive and started no later
// than the launch the file records, so a dispatch that timed out, refused an
// ambiguous match or was killed, or a process id reused afterwards, opens
// nothing. Once the lineage entry carries a session id the capture has its
// answer and the records are not consulted.
func boundByRecords(sessionID string, found workspace.FoundInstance, pending workspace.CapturePending) bool {
	if e, ok := readLineage(found.WorkspaceRoot, pending.DispatchID); ok && e.SessionID != "" {
		return false
	}
	if !captureProcessRunning(pending) {
		return false
	}
	ag, err := agent.ParseAgent(pending.Agent)
	if err != nil {
		return false
	}
	spec, ok := dispatchLaunchSpec(ag)
	if !ok {
		return false
	}
	root := recordStoreRoot(spec.Records, userHomeDir(), os.Getenv)
	match, ambiguous, err := matchRecordByCwd(root, spec.Records, normalizePath(found.Instance))
	return err == nil && !ambiguous && match.ID == sessionID
}

// captureProcessRunning reports whether the process the pending file names is
// alive and started no later than the file's launch time. A start that can't
// be read counts as not running.
func captureProcessRunning(p workspace.CapturePending) bool {
	if !worktree.IsProcessAlive(p.PID) {
		return false
	}
	started, err := worktree.PIDStartedAt(p.PID)
	return err == nil && !started.After(p.LaunchedAt)
}

// readLineage reads one dispatch's lineage entry. An entry that is missing,
// unreadable or in a later version is no evidence.
func readLineage(root, dispatchID string) (lineage.Entry, bool) {
	stored, found, err := workspace.ReadLineageEntry(root, dispatchID)
	if err != nil || !found || stored.Parsed.LaterVersion {
		return lineage.Entry{}, false
	}
	return stored.Parsed.Entry, true
}

// scanLineageFor returns the dispatch id of the first entry in the store that
// match accepts, in the store's file-name order.
func scanLineageFor(root string, match func(lineage.Entry) bool) (string, bool) {
	results, err := workspace.ListLineageEntries(root)
	if err != nil {
		return "", false
	}
	for _, r := range results {
		if r.Err != nil || r.Entry.Parsed.LaterVersion {
			continue
		}
		if match(r.Entry.Parsed.Entry) {
			return r.DispatchID, true
		}
	}
	return "", false
}

// mappingNamesInstance reports whether the mapping's instance_path is the
// instance, symlinks resolved. The path is same-user writable, so anything
// that isn't an absolute path to that very directory names another instance.
func mappingNamesInstance(m workspace.SessionMapping, instance string) bool {
	if m.InstancePath == "" || !filepath.IsAbs(m.InstancePath) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(m.InstancePath)
	return err == nil && resolved == instance
}

// bindToRecord answers for the dispatched session of dispatchID once some
// evidence has tied the session to it: bound when the instance's record is
// usable and names that dispatch, record-unusable otherwise.
func bindToRecord(binding authority.Binding, dispatchID string, found workspace.FoundInstance, points []string) authority.CheckDoc {
	rec, present, err := authorityReadRecord(found.Instance)
	switch {
	case err != nil:
		// Every class ReadAuthorityRecord reports (a symlink, an unreadable
		// or oversized file, bad contents) is one reason here: the document
		// keeps a two-code vocabulary for a record that exists but can't be
		// used, and each class fails the same way, holding nothing.
		return authority.CheckRecordUnusable(dispatchID, authority.ReasonRecordMalformed)
	case !present:
		return authority.CheckRecordUnusable(dispatchID, authority.ReasonRecordMissing)
	case rec.DispatchID != dispatchID:
		return authority.CheckRecordUnusable(dispatchID, authority.ReasonRecordOtherDispatch)
	}
	return authority.CheckBound(binding, rec, checkParent(found.WorkspaceRoot, rec), points)
}

// checkParent names the record's parent dispatch, with the instance its
// lineage entry records when that entry still exists and reads cleanly. A
// failed read costs only the instance name.
func checkParent(root string, rec authority.Record) *authority.CheckParent {
	if rec.ParentDispatchID == "" {
		return nil
	}
	parent := &authority.CheckParent{DispatchID: rec.ParentDispatchID}
	// A later-version entry is read for its dispatch id alone, so it has no
	// instance to give.
	entry, found, err := workspace.ReadLineageEntry(root, rec.ParentDispatchID)
	if err == nil && found && !entry.Parsed.LaterVersion {
		parent.Instance = entry.Parsed.Entry.Instance
	}
	return parent
}
