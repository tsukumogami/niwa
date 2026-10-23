package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The read side's seams. Production uses the real readers and a two-second
// watchdog; tests replace them to stall a read or count the mapping reads.
var (
	authorityReadRecord         = workspace.ReadAuthorityRecord
	authorityReadSessionMapping = workspace.ReadSessionMapping
	authorityWatchdog           = 2 * time.Second
)

func init() {
	authorityCmd.AddCommand(newAuthorityShowCmd(), newAuthorityDecideCmd())
}

// dispatchSelectors are the flags that pick which dispatch show and decide
// answer for. The *Set fields say whether a flag was given at all, so an
// empty -C is told apart from no -C.
type dispatchSelectors struct {
	Session, Instance, Dir          string
	SessionSet, InstanceSet, DirSet bool
}

// dispatchSelection is what selectDispatch picked: the instance whose record
// applies, empty when none does, and the directory the no-record level is
// resolved from.
type dispatchSelection struct {
	Instance  string
	PolicyDir string
}

func usageError(format string, args ...any) error {
	return &exitCodeError{code: 2, err: fmt.Errorf(format, args...)}
}

func readError(format string, args ...any) error {
	return &exitCodeError{code: 1, err: fmt.Errorf(format, args...)}
}

// selectDispatch applies R21's order: the session --session names, else the
// instance --instance names, else the instance containing -C's directory or
// the working directory. It never reads the session id variable: a worker can
// inherit its dispatcher's (a Codex worker does), so a session that left its
// instance, or one at the workspace root, would otherwise report its
// dispatcher's wider record as its own. A directory in no instance selects no
// dispatch, which reads as the no-record answer.
//
// The -C and working-directory paths only stat paths (FindInstance), so a
// lookup that finds a record touches no config and writes nothing. --session
// and --instance find the workspace root the way every niwa command does.
func selectDispatch(sel dispatchSelectors, cwd string) (dispatchSelection, error) {
	n := 0
	for _, set := range []bool{sel.SessionSet, sel.InstanceSet, sel.DirSet} {
		if set {
			n++
		}
	}
	if n > 1 {
		return dispatchSelection{}, usageError("--session, --instance and -C each select the dispatch; pass at most one")
	}
	switch {
	case sel.SessionSet:
		return selectBySession(sel.Session, cwd)
	case sel.InstanceSet:
		return selectByInstance(sel.Instance, cwd)
	}
	anchor := cwd
	if sel.DirSet {
		if err := checkAnchorDir(sel.Dir); err != nil {
			return dispatchSelection{}, err
		}
		anchor = sel.Dir
	}
	found, ok, err := workspace.FindInstance(anchor)
	if err != nil {
		return dispatchSelection{}, readError("finding the instance containing %s: %v", strconv.Quote(anchor), err)
	}
	if !ok {
		return dispatchSelection{PolicyDir: anchor}, nil
	}
	return dispatchSelection{Instance: found.Instance, PolicyDir: found.Instance}, nil
}

// checkAnchorDir refuses an empty, relative or missing -C rather than falling
// back to the working directory: a hook that passed a bad anchor must not
// silently get the answer for wherever the model left the shell.
func checkAnchorDir(dir string) error {
	if dir == "" {
		return readError("-C is empty; pass the absolute path of a directory")
	}
	if !filepath.IsAbs(dir) {
		return readError("-C %s is not an absolute path", strconv.Quote(dir))
	}
	info, err := os.Stat(dir)
	if err != nil {
		return readError("-C %s does not exist", strconv.Quote(dir))
	}
	if !info.IsDir() {
		return readError("-C %s is not a directory", strconv.Quote(dir))
	}
	return nil
}

func discoverWorkspaceRoot(cwd string) (string, error) {
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return "", readError("not inside a niwa workspace: %v", err)
	}
	return filepath.Dir(configDir), nil
}

// selectBySession reads the one mapping --session names. No mapping is the
// no-record answer. The mapping file is writable by any same-user process, so
// the instance_path it holds is used only when it is an instance directly
// under this workspace root; anything else is refused, and the message never
// echoes the mapping's contents.
func selectBySession(id, cwd string) (dispatchSelection, error) {
	if !workspace.ValidSessionID(id) {
		return dispatchSelection{}, readError("invalid --session %s: a session id is a lowercase UUID", strconv.Quote(id))
	}
	root, err := discoverWorkspaceRoot(cwd)
	if err != nil {
		return dispatchSelection{}, err
	}
	m, err := authorityReadSessionMapping(root, id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return dispatchSelection{PolicyDir: cwd}, nil
		}
		return dispatchSelection{}, readError("the session mapping for --session %s can't be read", id)
	}
	refuse := func() (dispatchSelection, error) {
		return dispatchSelection{}, readError("the session mapping for --session %s does not name an instance of this workspace; refusing to read a record through it", id)
	}
	if m.InstancePath == "" || !filepath.IsAbs(m.InstancePath) {
		return refuse()
	}
	path := filepath.Clean(m.InstancePath)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return dispatchSelection{}, readError("resolving the workspace root: %v", err)
	}
	if parent := filepath.Dir(path); parent != filepath.Clean(root) && parent != resolvedRoot {
		return refuse()
	}
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		// The instance the session ran in is gone (reaped or destroyed), and
		// its record went with it.
		return dispatchSelection{PolicyDir: cwd}, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return refuse()
	}
	found, ok, err := workspace.FindInstance(path)
	if err != nil || !ok || found.Instance != resolved || found.WorkspaceRoot != resolvedRoot {
		return refuse()
	}
	return dispatchSelection{Instance: found.Instance, PolicyDir: found.Instance}, nil
}

// selectByInstance finds the instance --instance names among the ones the
// workspace root enumerates, matching by name, so the flag's value is checked
// before it is ever part of a path and can't step outside the root.
func selectByInstance(name, cwd string) (dispatchSelection, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || !workspace.ValidName(name) {
		return dispatchSelection{}, readError("invalid --instance %s: pass the name of an instance in this workspace", strconv.Quote(name))
	}
	root, err := discoverWorkspaceRoot(cwd)
	if err != nil {
		return dispatchSelection{}, err
	}
	dirs, err := workspace.EnumerateInstances(root)
	if err != nil {
		return dispatchSelection{}, readError("listing the instances of the workspace at %s: %v", root, err)
	}
	for _, dir := range dirs {
		if filepath.Base(dir) != name {
			continue
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			break
		}
		found, ok, err := workspace.FindInstance(dir)
		if err != nil || !ok || found.Instance != resolved {
			break
		}
		return dispatchSelection{Instance: found.Instance, PolicyDir: found.Instance}, nil
	}
	return dispatchSelection{}, readError("no instance named %s in the workspace at %s", strconv.Quote(name), root)
}

// dispatchAnswer is what show and decide answer from: the record when one
// exists, else the level a dispatch with no authority flags would resolve to.
type dispatchAnswer struct {
	Record      authority.Record
	Found       bool
	Level       authority.Level
	LevelSource authority.Layer
}

// readSelectedDispatch selects the dispatch and reads its record. The record
// is read before any config: a record that exists answers on its own, so a
// config typo added after the dispatch doesn't hide it. Only the no-record
// answer loads and validates the authority config (R11), and a bad config
// then fails it. A record that exists but can't be used fails with its
// failure class only, never its path or contents.
func readSelectedDispatch(sel dispatchSelectors, warn io.Writer) (dispatchAnswer, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return dispatchAnswer{}, readError("getting working directory: %v", err)
	}
	s, err := selectDispatch(sel, cwd)
	if err != nil {
		return dispatchAnswer{}, err
	}
	if s.Instance != "" {
		rec, found, err := authorityReadRecord(s.Instance)
		if err != nil {
			var re *workspace.AuthorityRecordError
			if errors.As(err, &re) {
				return dispatchAnswer{}, readError("the selected dispatch's %s", re.Error())
			}
			return dispatchAnswer{}, readError("the selected dispatch's authority record cannot be read")
		}
		if found {
			return dispatchAnswer{Record: rec, Found: true}, nil
		}
	}
	p, warnings, err := loadAuthorityPolicy(s.PolicyDir)
	for _, w := range warnings {
		fmt.Fprintf(warn, "warning: %s\n", w)
	}
	if err != nil {
		return dispatchAnswer{}, err
	}
	report := authority.BuildPolicyReport(p, warnings)
	return dispatchAnswer{Level: report.NoFlagLevel, LevelSource: report.NoFlagLevelSource}, nil
}

// runUnderWatchdog runs work with its output buffered and gives up after
// authorityWatchdog. Nothing reaches stdout until work returns successfully,
// so a stalled filesystem, a timeout or any error leaves stdout empty, and a
// caller reading the output never sees half a document. The watchdog is
// armed before work makes its first filesystem call. A stalled read is left
// behind in its goroutine; the process exits right after.
func runUnderWatchdog(cmd *cobra.Command, work func(stdout, stderr io.Writer) error) error {
	type result struct {
		out, errOut bytes.Buffer
		err         error
	}
	done := make(chan *result, 1)
	timer := time.NewTimer(authorityWatchdog)
	defer timer.Stop()
	go func() {
		r := &result{}
		r.err = work(&r.out, &r.errOut)
		done <- r
	}()
	select {
	case r := <-done:
		cmd.ErrOrStderr().Write(r.errOut.Bytes())
		if r.err != nil {
			return r.err
		}
		cmd.OutOrStdout().Write(r.out.Bytes())
		return nil
	case <-timer.C:
		return readError("gave up after %s reading the dispatch's authority record", authorityWatchdog)
	}
}

const selectorHelp = `The dispatch is selected in this order: the session --session names, the
instance --instance names, otherwise the instance containing -C's directory or
the working directory. The session id variable is never read. A directory in
no instance, or an instance with no record, gives the no-record answer.`

func addSelectorFlags(cmd *cobra.Command, sel *dispatchSelectors, jsonOut *bool, jsonHelp string) {
	cmd.Flags().StringVar(&sel.Session, "session", "", "read the dispatch of this session id (its session mapping names the instance)")
	cmd.Flags().StringVar(&sel.Instance, "instance", "", "read the dispatch of the instance with this name")
	cmd.Flags().StringVarP(&sel.Dir, "dir", "C", "", "find the instance from this absolute directory instead of the working directory")
	cmd.Flags().BoolVar(jsonOut, "json", false, jsonHelp)
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &exitCodeError{code: 2, err: err}
	})
}

func (sel *dispatchSelectors) markSet(cmd *cobra.Command) {
	sel.SessionSet = cmd.Flags().Changed("session")
	sel.InstanceSet = cmd.Flags().Changed("instance")
	sel.DirSet = cmd.Flags().Changed("dir")
}

// newAuthorityShowCmd builds `niwa authority show`. Each call returns a
// command with its own flag state, so a test can run it more than once.
func newAuthorityShowCmd() *cobra.Command {
	var sel dispatchSelectors
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the authority a dispatch was granted",
		Long: `Show the authority a dispatch was granted: its level and where it came from,
the ceilings recorded with it, its roles, its grant with each authority's
sources, its dispatch id and its parent links.

` + selectorHelp + `

With no record, the plain output starts with "dispatch record: none" and shows
the level a dispatch with no authority flags would resolve to; --json prints a
` + authority.SchemaV1 + ` document with "record": false and an empty
grant. Treat an authority as held only when --json exits 0 and prints a
document with that schema, "record": true, and the authority in its grant.

Exit codes: 0 printed (a record or none), 1 a bad or unreadable record, a
missing instance, a bad -C, a config error, outside a workspace, or the
watchdog, 2 usage.`,
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			sel.markSet(cmd)
			return runUnderWatchdog(cmd, func(stdout, stderr io.Writer) error {
				ans, err := readSelectedDispatch(sel, stderr)
				if err != nil {
					return err
				}
				return printShow(stdout, ans, jsonOut)
			})
		},
	}
	addSelectorFlags(cmd, &sel, &jsonOut, "print the record as one "+authority.SchemaV1+" JSON document")
	return cmd
}

func printShow(w io.Writer, ans dispatchAnswer, jsonOut bool) error {
	if !jsonOut {
		if ans.Found {
			fmt.Fprint(w, ans.Record.Text())
		} else {
			fmt.Fprint(w, authority.NoRecordText(ans.Level, ans.LevelSource))
		}
		return nil
	}
	var b []byte
	var err error
	if ans.Found {
		b, err = ans.Record.MarshalJSON()
	} else {
		b, err = authority.NoRecordJSON(ans.Level, ans.LevelSource)
	}
	if err != nil {
		return readError("encoding the record: %v", err)
	}
	fmt.Fprintf(w, "%s\n", b)
	return nil
}

// newAuthorityDecideCmd builds `niwa authority decide <point>`.
func newAuthorityDecideCmd() *cobra.Command {
	var sel dispatchSelectors
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "decide <point>",
		Short: "Say who settles a decision point for a dispatch",
		Long: `Say who settles a decision point for a dispatch, one value per line:
point=, level=, decider=, author_exclusion=, source= and record=present or
record=none. The answer comes from the dispatch's level; it never reads the
grant. With no record it comes from the level a dispatch with no authority
flags would resolve to, and source= carries " (no dispatch record)".

A caller settles the point itself only when this exits 0 and prints exactly
decider=session.

` + selectorHelp + `

Exit codes: 0 printed an answer, 1 as for show, 2 usage or a point name not
matching ` + authority.NamePatternText + ` (checked first).`,
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			point := args[0]
			// Checked before any selector or filesystem work, so a typo in
			// the point is a usage error whatever else is wrong.
			if !authority.ValidPoint(point) {
				return usageError("invalid decision point %s; a point name matches %s", strconv.Quote(point), authority.NamePatternText)
			}
			sel.markSet(cmd)
			return runUnderWatchdog(cmd, func(stdout, stderr io.Writer) error {
				ans, err := readSelectedDispatch(sel, stderr)
				if err != nil {
					return err
				}
				return printDecide(stdout, point, ans, jsonOut)
			})
		},
	}
	addSelectorFlags(cmd, &sel, &jsonOut, "print the answer as one JSON object with the same six fields")
	return cmd
}

type decideDoc struct {
	Point           string `json:"point"`
	Level           string `json:"level"`
	Decider         string `json:"decider"`
	AuthorExclusion string `json:"author_exclusion"`
	Source          string `json:"source"`
	Record          string `json:"record"`
}

func printDecide(w io.Writer, point string, ans dispatchAnswer, jsonOut bool) error {
	var a authority.Answer
	record := "none"
	if ans.Found {
		a = authority.Decide(point, ans.Record.Level, ans.Record.LevelSource, false, authority.NoDeclarations)
		record = "present"
	} else {
		a = authority.Decide(point, ans.Level, ans.LevelSource, true, authority.NoDeclarations)
	}
	exclusion := "off"
	if a.AuthorExclusion {
		exclusion = "on"
	}
	doc := decideDoc{
		Point:           a.Point,
		Level:           a.Level.String(),
		Decider:         a.Decider.String(),
		AuthorExclusion: exclusion,
		Source:          a.Source,
		Record:          record,
	}
	if jsonOut {
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return readError("encoding the answer: %v", err)
		}
		fmt.Fprintf(w, "%s\n", b)
		return nil
	}
	fmt.Fprintf(w, "point=%s\nlevel=%s\ndecider=%s\nauthor_exclusion=%s\nsource=%s\nrecord=%s\n",
		doc.Point, doc.Level, doc.Decider, doc.AuthorExclusion, doc.Source, doc.Record)
	return nil
}

// listAuthority is an instance's authority object in `niwa list --json`: the
// same document `niwa authority show --json` prints for it, nothing for an
// instance with no record, and an object holding only an error naming the
// failure class (never the path or the file's contents) for a record that
// can't be used, so one bad record doesn't hide the other instances.
func listAuthority(instancePath string) json.RawMessage {
	rec, found, err := authorityReadRecord(instancePath)
	if err == nil && !found {
		return nil
	}
	if err == nil {
		b, encErr := rec.MarshalJSON()
		if encErr == nil {
			return b
		}
		err = encErr
	}
	class := "authority record cannot be read"
	var re *workspace.AuthorityRecordError
	if errors.As(err, &re) {
		class = re.Error()
	}
	b, _ := json.Marshal(map[string]string{"error": class})
	return b
}
