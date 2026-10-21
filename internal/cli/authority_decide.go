package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Exit codes `niwa authority decide` promises, beside 0 (the session settles
// the point). Configuration errors and "not inside a workspace" exit 1 through
// Execute's default path.
const (
	authorityDecideExitUsage    = 2
	authorityDecideExitNotYours = 3
)

// authorityExitError carries `niwa authority decide`'s exit code to Execute,
// the same shape as sessionattach.ExitCodeError. An empty msg prints nothing:
// exit 3 is an answer, not a failure, so the five lines on stdout are all the
// caller sees.
type authorityExitError struct {
	code int
	msg  string
}

func (e *authorityExitError) Error() string { return e.msg }

// authorityUsageError is a usage error: exit 2 with the message on stderr.
func authorityUsageError(format string, args ...any) error {
	return &authorityExitError{code: authorityDecideExitUsage, msg: fmt.Sprintf(format, args...)}
}

var authorityDecideCmd = newAuthorityDecideCmd()

// newAuthorityDecideCmd builds the decide command. It is a constructor, not a
// package-level literal, so each test gets a command whose --session flag
// starts unset.
func newAuthorityDecideCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "decide <point> [--session <id>]",
		Short: "Answer who settles a decision point for the calling session",
		Long: `Answer who settles one decision point for the calling session, from the
authority level its dispatch resolved.

It prints five lines:

  point=<point>
  level=<autonomous|supervised|strict>
  decider=<session|coordinator|person>
  author_exclusion=<on|off>
  source=<flag|machine|workspace|default>[ (no dispatch record)]

The level comes from the first of these that exists: the session mapping for
--session, or for $CLAUDE_CODE_SESSION_ID when the flag is absent; the
authority record of the instance containing the working directory; and the
level a dispatch with no --authority flag would get, marked
"(no dispatch record)".

A point name is a lowercase letter followed by up to 63 lowercase letters,
digits or dashes.

Exit codes:
  0  the session settles the point itself
  3  someone else does (the coordinator or a person)
  2  usage error: a missing or malformed point name or session id
  1  outside a workspace, or a configuration or record that is invalid

Treat any exit other than 0 as "not yours to settle". The answer is advisory:
it reads local files only and does not authenticate the caller.`,
		Args: cobra.ArbitraryArgs,
		RunE: runAuthorityDecide,
	}
	cmd.Flags().String("session", "",
		"session id whose dispatch record to read (default: $CLAUDE_CODE_SESSION_ID)")
	// A flag cobra can't parse is a usage error, so it exits 2 like a bad
	// point name rather than taking Execute's default 1.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return authorityUsageError("niwa authority decide: %v", err)
	})
	return cmd
}

func runAuthorityDecide(cmd *cobra.Command, args []string) error {
	// Usage comes first, before the working directory or any file is looked
	// at, so a malformed call gets the same answer everywhere.
	if len(args) != 1 {
		return authorityUsageError("niwa authority decide: expected exactly one decision point name, got %d", len(args))
	}
	point := args[0]
	if !authority.ValidPoint(point) {
		return authorityUsageError("niwa authority decide: invalid decision point name %q; a name is a lowercase letter followed by up to 63 lowercase letters, digits or dashes", point)
	}
	sessionID := os.Getenv("CLAUDE_CODE_SESSION_ID")
	if f := cmd.Flags().Lookup("session"); f != nil && f.Changed {
		sessionID = f.Value.String()
		if !workspace.ValidSessionID(sessionID) {
			return authorityUsageError("niwa authority decide: invalid --session %q; a session id is a lowercase UUID", sessionID)
		}
	} else if !workspace.ValidSessionID(sessionID) {
		// The environment is the agent's, not the caller's spelling: an
		// unset or foreign-shaped value means niwa has no mapping for it.
		sessionID = ""
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("not inside a niwa workspace; run niwa authority decide from within a workspace")
	}

	// Both configs load even when a record will answer: a broken setting is
	// reported the same way whichever record exists.
	in, err := loadAuthorityConfig(class.WorkspaceRoot)
	if err != nil {
		return err
	}

	resolved, noRecord, err := lookupDecideLevel(class, sessionID, in)
	if err != nil {
		return err
	}
	ans := authority.Decide(point, resolved, authority.NoDeclarations)
	ans.NoRecord = noRecord
	writeAuthorityDecide(cmd.OutOrStdout(), ans)

	if ans.Decider != authority.DeciderSession {
		return &authorityExitError{code: authorityDecideExitNotYours}
	}
	return nil
}

// lookupDecideLevel finds the calling session's level: its session mapping
// when sessionID names one that carries a level, then the authority record of
// the instance containing the working directory, then the level a dispatch
// with no flag would resolve to (noRecord true). A record that exists but
// holds anything other than a valid level and source is an error, never a
// reason to fall through: falling through would answer from a wider level
// than the dispatch recorded.
func lookupDecideLevel(class workspace.CwdClassification, sessionID string, in authority.Inputs) (authority.Resolved, bool, error) {
	if sessionID != "" {
		m, err := workspace.ReadSessionMapping(class.WorkspaceRoot, sessionID)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// No mapping for this id yet; the instance record answers.
		case err != nil:
			return authority.Resolved{}, false, err
		case m.AuthorityLevel != "" || m.AuthoritySource != "":
			r, err := resolvedFromRecord(workspace.AuthorityRecord{Level: m.AuthorityLevel, Source: m.AuthoritySource})
			if err != nil {
				return authority.Resolved{}, false, fmt.Errorf("session mapping %s: %w", sessionID, err)
			}
			return r, false, nil
		}
	}

	if class.InstanceDir != "" {
		rec, err := workspace.ReadAuthorityRecord(class.InstanceDir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// An instance niwa didn't dispatch, or one dispatched before
			// the record existed.
		case err != nil:
			return authority.Resolved{}, false, err
		default:
			r, err := resolvedFromRecord(rec)
			if err != nil {
				return authority.Resolved{}, false, err
			}
			return r, false, nil
		}
	}

	return authority.Ceiling(in), true, nil
}

// resolvedFromRecord turns a recorded level and source into a Resolved,
// refusing any value Validate rejects.
func resolvedFromRecord(rec workspace.AuthorityRecord) (authority.Resolved, error) {
	if err := rec.Validate(); err != nil {
		return authority.Resolved{}, err
	}
	l, err := authority.Parse(rec.Level)
	if err != nil {
		return authority.Resolved{}, err
	}
	return authority.Resolved{Level: *l, Source: authority.Source(rec.Source)}, nil
}

// writeAuthorityDecide prints the five lines `niwa authority decide`
// promises, in their fixed order.
func writeAuthorityDecide(w io.Writer, a authority.Answer) {
	exclusion := "off"
	if a.AuthorExclusion {
		exclusion = "on"
	}
	source := string(a.Source)
	if a.NoRecord {
		source += " (no dispatch record)"
	}
	fmt.Fprintf(w, "point=%s\n", a.Point)
	fmt.Fprintf(w, "level=%s\n", a.Level)
	fmt.Fprintf(w, "decider=%s\n", a.Decider)
	fmt.Fprintf(w, "author_exclusion=%s\n", exclusion)
	fmt.Fprintf(w, "source=%s\n", source)
}
