package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/workspace"
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
worktree of it. The session is bound when the session mapping dispatch wrote
for it names this instance and the instance's authority record names the
same dispatch. The answer's state is one of:

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

// checkSession binds sessionID to the instance holding dir. It reads only
// the session's own mapping, the instance's record and, for a bound session
// with a parent, the parent's lineage entry; it loads no config and writes
// nothing. The session id is checked before any path is built from it.
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
			return authority.CheckNotDispatched(authority.ReasonNoMapping), nil
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
