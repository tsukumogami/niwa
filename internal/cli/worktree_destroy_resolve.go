package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// The ids a developer actually holds are not the ids the worktree lifecycle
// store is keyed by. A dispatched session is known by its agent session id (a
// UUID) or by the short handle that appears in its window title and in
// `niwa list`; a worktree record is keyed by eight hex characters that appear
// nowhere else. Before this resolver, `niwa worktree destroy` accepted only the
// latter, which is issue #292: the id a user pastes resolved to nothing, so the
// merged-branch and dirty-tree guards never ran and teardown fell to `niwa
// reap`, which has no such guards.
//
// Resolution reads the session mapping store once and matches the value three
// ways, then either destroys one worktree of the current instance (today's
// behavior, unchanged) or every active worktree of the instance a session
// backs.

// uuidPattern matches the canonical 8-4-4-4-12 form. A mapping whose session_id
// is not one is not a mapping niwa wrote: the single-instance layout puts
// worktree lifecycle records in the same directory, and those are keyed by
// eight hex characters. Filtering on the field rather than the filename is
// deliberate — the store returns decoded bodies and never exposes a filename.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// worktreeIDPattern is the lifecycle store's key shape.
var worktreeIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

// destroyScope says what the working directory is, which decides whether a
// value may resolve to a worktree id at all. Inside an instance both readings
// are possible and a value that is both is ambiguous; at a workspace root only
// the session reading exists, because the root has no worktrees of its own.
type destroyScope struct {
	// instanceDir is the instance the command is running inside, empty at a
	// multi-instance workspace root.
	instanceDir string
	// workspaceRoot is the root whose mapping store holds the sessions.
	workspaceRoot string
}

// destroyTarget is what a value resolved to: either one worktree of the current
// instance, or one session and the instance it backs.
type destroyTarget struct {
	worktreeID string
	mapping    *workspace.SessionMapping
}

// resolveDestroyScope classifies the working directory. A worktree and a
// single-instance root both count as being inside an instance, because in both
// the lifecycle store of that instance is the one a worktree id refers to.
func resolveDestroyScope() (destroyScope, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return destroyScope{}, fmt.Errorf("niwa: error: getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return destroyScope{}, err
	}
	switch class.Class {
	case workspace.CwdInsideWorktree, workspace.CwdInsideInstance:
		return destroyScope{instanceDir: class.InstanceDir, workspaceRoot: class.WorkspaceRoot}, nil
	case workspace.CwdAtWorkspaceRoot:
		if workspace.IsSingleInstanceLayout(class.WorkspaceRoot) {
			return destroyScope{instanceDir: class.WorkspaceRoot, workspaceRoot: class.WorkspaceRoot}, nil
		}
		return destroyScope{workspaceRoot: class.WorkspaceRoot}, nil
	default:
		return destroyScope{}, fmt.Errorf("niwa: error: not inside a niwa workspace (no .niwa/workspace.toml found walking up from %s)", cwd)
	}
}

// loadMappingsForDestroy makes the resolver's one read of the session mapping
// store.
//
// The read is unlocked, as every reader of this store is today. That matters
// for what it can get wrong: a read taken while the workspace root's config
// directory is being swapped sees the directory missing (ListSessionMappings
// returns no mappings) or a subset of its files, because each mapping is read
// whole and a malformed one is skipped. A subset can only remove candidates, so
// the resolver can report "no match" where a match existed, and can miss an
// ambiguity it would otherwise have refused. It cannot invent a mapping, and it
// cannot name an instance no mapping named. Issue #297 replaces this one call
// with a locked read; until then the failure direction is recorded as a known
// limitation rather than papered over.
//
// Two filters run here rather than at the match site. Entries whose session_id
// is not a UUID are dropped, so the single-instance layout's lifecycle records
// -- which share this directory -- never become fake mappings. Entries whose
// handle fails watch.IsSafeHandle keep their session-id match and lose their
// handle match: the handle is matched as a string and then printed back in the
// ambiguity line, and it comes from a file any same-user process can write.
func loadMappingsForDestroy(workspaceRoot string) ([]workspace.SessionMapping, error) {
	all, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("niwa: error: reading session mappings: %w", err)
	}
	out := make([]workspace.SessionMapping, 0, len(all))
	for _, m := range all {
		if !uuidPattern.MatchString(m.SessionID) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// mappingHandle returns the handle a mapping may be matched and printed by, or
// empty when it has none or the recorded one is unsafe to put on a terminal.
func mappingHandle(m workspace.SessionMapping) string {
	if m.Handle == "" || !watch.IsSafeHandle(m.Handle) {
		return ""
	}
	return m.Handle
}

// matchMappings returns every mapping the value names. Matching is exact and
// case-sensitive in all three forms:
//
//   - the full session id;
//   - the recorded handle;
//   - the first eight characters of the session id, but only for a mapping with
//     no usable handle. A mapping that records a handle is addressed by that
//     handle, so matching its id prefix too would make one session answer to two
//     short forms and turn a handle collision into an ambiguity a user cannot
//     act on.
//
// Results are deduplicated by session id, so a mapping whose handle equals its
// own id (which is what niwa writes for a Codex session) is one match rather
// than two.
func matchMappings(ms []workspace.SessionMapping, v string) []workspace.SessionMapping {
	seen := make(map[string]bool)
	var out []workspace.SessionMapping
	add := func(m workspace.SessionMapping) {
		if seen[m.SessionID] {
			return
		}
		seen[m.SessionID] = true
		out = append(out, m)
	}
	for _, m := range ms {
		switch {
		case m.SessionID == v:
			add(m)
		case mappingHandle(m) == v && v != "":
			add(m)
		case mappingHandle(m) == "" && worktreeIDPattern.MatchString(v) && strings.HasPrefix(m.SessionID, v):
			add(m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// isWorktreeIDOf reports whether v keys an active lifecycle record of the given
// instance.
func isWorktreeIDOf(instanceDir, v string) bool {
	if instanceDir == "" || !worktreeIDPattern.MatchString(v) {
		return false
	}
	states, err := worktree.ListSessionLifecycleStates(filepath.Join(instanceDir, ".niwa", "sessions"))
	if err != nil {
		return false
	}
	for _, st := range states {
		if st.SessionID == v {
			return true
		}
	}
	return false
}

// describeMatch renders one candidate for the ambiguity line.
func describeMatch(m workspace.SessionMapping) string {
	if h := mappingHandle(m); h != "" {
		return fmt.Sprintf("session %s (%s)", m.SessionID, h)
	}
	return "session " + m.SessionID
}

// resolveDestroyTarget decides what a value refers to. It is pure: everything
// it needs is the scope, the value and the snapshot of mappings already read.
//
// Ambiguity is refused rather than guessed. Inside an instance a value can name
// both a worktree of that instance and a session, and there is no safe default
// between "remove this one worktree" and "remove every worktree of that
// session's instance" -- so the command says so and names both readings. At a
// workspace root the worktree reading does not exist, so the same value
// resolves to the session with nothing to disambiguate.
func resolveDestroyTarget(s destroyScope, v string, ms []workspace.SessionMapping) (destroyTarget, error) {
	matches := matchMappings(ms, v)
	isWorktree := isWorktreeIDOf(s.instanceDir, v)

	switch {
	case isWorktree && len(matches) == 0:
		return destroyTarget{worktreeID: v}, nil

	case isWorktree && len(matches) > 0:
		names := []string{fmt.Sprintf("worktree %s in this instance", v)}
		for _, m := range matches {
			names = append(names, describeMatch(m))
		}
		return destroyTarget{}, &sessionattach.ExitCodeError{
			Code: 4,
			Msg: fmt.Sprintf("niwa: error: %q matches %s. "+
				"Pass --by-path <worktree path> for the worktree, or the full session id for the session.",
				stripControlChars(v), strings.Join(names, ", ")),
		}

	case len(matches) == 1:
		m := matches[0]
		return destroyTarget{mapping: &m}, nil

	case len(matches) > 1:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, describeMatch(m))
		}
		return destroyTarget{}, &sessionattach.ExitCodeError{
			Code: 4,
			Msg: fmt.Sprintf("niwa: error: %q matches %s. Pass the full session id.",
				stripControlChars(v), strings.Join(names, ", ")),
		}

	default:
		return destroyTarget{}, &sessionattach.ExitCodeError{
			Code: 3,
			Msg:  fmt.Sprintf("niwa: error: no worktree or session matches %q", stripControlChars(v)),
		}
	}
}

// checkSessionInstance decides whether a matched mapping's instance may be torn
// down, and returns the directory to act on.
//
// The returned directory is the one instance enumeration produced, never the
// string the mapping recorded. That is the containment invariant: a mapping can
// name any path at all, and the only ones that survive are those the workspace
// root actually holds as instances. gone reports the exit-0 outcome, an
// instance that is simply not there any more, which a teardown after a reap
// should report rather than treat as an error.
func checkSessionInstance(workspaceRoot string, m workspace.SessionMapping, all []workspace.SessionMapping) (instanceDir string, gone bool, err error) {
	recorded := strings.TrimSpace(m.InstancePath)
	if recorded == "" {
		return "", false, fmt.Errorf("niwa: error: session %s records no instance path", m.SessionID)
	}
	cleaned := filepath.Clean(recorded)

	// Lexical rung: a direct child of the root, and not the root's own .niwa.
	parent := filepath.Dir(cleaned)
	base := filepath.Base(cleaned)
	if parent != filepath.Clean(workspaceRoot) || base == workspace.StateDir {
		return "", false, fmt.Errorf("niwa: error: session %s names %s, which is not an instance of this workspace", m.SessionID, cleaned)
	}

	// Existence rung. A missing directory is the exit-0 "already gone" case;
	// anything present but not a directory is a refusal, because treating it as
	// gone would invite a caller to reclaim a path it does not understand.
	info, statErr := os.Lstat(cleaned)
	if os.IsNotExist(statErr) {
		return "", true, nil
	}
	if statErr != nil {
		return "", false, fmt.Errorf("niwa: error: session %s: %v", m.SessionID, statErr)
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("niwa: error: session %s names %s, which is not a directory", m.SessionID, cleaned)
	}

	// Membership rung. EnumerateInstances returns lexical joins of the root it
	// was handed and resolves nothing, so both sides are resolved before they
	// are compared; otherwise a workspace root reached through a symlink -- the
	// default for anything under /tmp on macOS -- would never match.
	instances, enumErr := workspace.EnumerateInstances(workspaceRoot)
	if enumErr != nil {
		return "", false, fmt.Errorf("niwa: error: enumerating instances: %w", enumErr)
	}
	wantResolved := resolvePathForCompare(cleaned)
	matched := ""
	for _, cand := range instances {
		if resolvePathForCompare(cand) == wantResolved {
			matched = cand
			break
		}
	}
	if matched == "" {
		return "", false, fmt.Errorf("niwa: error: session %s names %s, which is not an instance of this workspace", m.SessionID, cleaned)
	}

	// Newest rung. An instance can be backed by a newer session than the one
	// named; tearing down through the older mapping would destroy worktrees the
	// current session is using.
	newest := workspace.NewestMappingPerInstance(all)
	if cur, ok := newest[cleaned]; ok && cur.SessionID != m.SessionID {
		return "", false, fmt.Errorf(
			"niwa: error: session %s no longer backs %s; session %s does. Pass that session id, or --by-path for one worktree",
			m.SessionID, cleaned, cur.SessionID)
	}

	return matched, false, nil
}

// destroySessionWorktrees tears down every active worktree of one instance.
//
// It continues past a refusal rather than stopping at the first. A session can
// back several worktrees, and a guard firing on one of them says nothing about
// the others; stopping would leave the caller to re-run the same command and
// hit the same guard. Refusals go to stderr one line each and make the command
// exit 1, so a script can tell "all gone" from "some kept" without parsing
// stdout.
//
// The instance directory is re-checked immediately before each destroy, which
// narrows the window between the membership check and the git call without
// closing it. The record's own fields are validated inside DestroySession,
// beside the git calls that consume them, which is what actually bounds where
// teardown can reach.
func destroySessionWorktrees(cmd *cobra.Command, instanceDir string, m workspace.SessionMapping, git worktree.GitInvoker) error {
	states, err := worktree.ListSessionLifecycleStates(filepath.Join(instanceDir, ".niwa", "sessions"))
	if err != nil {
		return fmt.Errorf("niwa: error: listing worktrees of %s: %w", instanceDir, err)
	}
	active := make([]worktree.SessionLifecycleState, 0, len(states))
	for _, st := range states {
		if st.Status == worktree.SessionStatusEnded || st.Status == worktree.SessionStatusAbandoned {
			continue
		}
		active = append(active, st)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].SessionID < active[j].SessionID })

	if len(active) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(),
			"session: nothing to destroy: session %s has no active niwa-managed worktree in %s; "+
				"branches outside niwa-managed worktrees were not checked\n",
			m.SessionID, instanceDir)
		return nil
	}

	refused := 0
	for _, st := range active {
		if info, statErr := os.Lstat(instanceDir); statErr != nil || !info.IsDir() {
			return fmt.Errorf("niwa: error: instance %s disappeared during teardown", instanceDir)
		}
		state, destroyErr := worktree.DestroySession(cmd.Context(), instanceDir, st.SessionID, false, git)
		if destroyErr != nil {
			refused++
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: error: %s\n", stripControlChars(destroyErr.Error()))
			continue
		}
		if state.BranchWarning != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), "warning:", stripControlChars(state.BranchWarning))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "session: destroyed %s (%s) at %s\n",
			st.SessionID, stripControlChars(st.Repo), stripControlChars(st.WorktreePath))
	}
	if refused > 0 {
		return &sessionattach.ExitCodeError{Code: 1}
	}
	return nil
}

// resolvePathForCompare resolves symlinks when it can and falls back to the
// lexical form when the path is gone, so two spellings of one directory compare
// equal.
func resolvePathForCompare(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}
