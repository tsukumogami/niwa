package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// claudeSessionRow is the subset of one `claude agents --json --all` row that
// destroy reads. ID is the short id Claude Code's verbs (rm, stop, attach)
// take; SessionID is the full id a session mapping records.
//
// PID and Status are present only while the session's process is alive, so
// either one set means the session is running. State is not consulted: Claude
// Code reports "done" for a session that finished its task whether or not its
// process has exited, and a finished session can sit at done/idle with a live
// process.
//
// Cwd is the directory the session runs in. Absent decodes to "", which roots
// the session nowhere.
type claudeSessionRow struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
}

func (r claudeSessionRow) running() bool {
	return r.PID != 0 || r.Status != ""
}

// sessionsRootedAtInstance returns the rows rooted at instancePath other than
// the mapped session. A row is rooted at the instance when its cwd, made
// canonical (cleaned, symlinks resolved), equals the canonical instance path
// or lies under it at a path-separator boundary, so a session working in a
// repo or worktree inside the instance counts and "<parent>/inst-2" is not
// under "<parent>/inst". A row with an empty or relative cwd is rooted nowhere.
//
// The mapped session is excluded only by its full session id, never by path
// or short id, so a different session that happens to share a short id or a
// directory is still reported. An empty mappedSessionID excludes nothing.
func sessionsRootedAtInstance(rows []claudeSessionRow, instancePath, mappedSessionID string) []claudeSessionRow {
	if instancePath == "" {
		return nil
	}
	instance := workspace.CanonicalPath(instancePath)
	var rooted []claudeSessionRow
	for _, r := range rows {
		if mappedSessionID != "" && r.SessionID == mappedSessionID {
			continue
		}
		if r.Cwd == "" || !filepath.IsAbs(r.Cwd) {
			continue
		}
		if pathWithin(workspace.CanonicalPath(r.Cwd), instance) {
			rooted = append(rooted, r)
		}
	}
	return rooted
}

// Seams over the two claude invocations, so tests never reach a real Claude
// Code install. Production wires them to the real binary.
var (
	listClaudeSessionsFunc  = realListClaudeSessions
	removeClaudeSessionFunc = realRemoveClaudeSession
)

// claudeSessionLister lists Claude Code's background sessions.
type claudeSessionLister func(context.Context) ([]claudeSessionRow, error)

// listClaudeSessionsOnce returns a lister that asks listClaudeSessionsFunc on
// its first call and hands every later call the same answer, error included,
// so one destroy lists Claude Code's sessions at most once. The call carries
// cc, so claude runs from cc's checked empty directory.
func listClaudeSessionsOnce(cc *claudeCalls) claudeSessionLister {
	var (
		done bool
		rows []claudeSessionRow
		err  error
	)
	return func(ctx context.Context) ([]claudeSessionRow, error) {
		if !done {
			done = true
			if cc != nil {
				ctx = withClaudeCalls(ctx, cc)
			}
			rows, err = listClaudeSessionsFunc(ctx)
		}
		return rows, err
	}
}

const (
	claudeListTimeout   = 30 * time.Second
	claudeRemoveTimeout = 60 * time.Second
	// claudeVetTimeout bounds one job-directory vet. A caller's earlier
	// deadline, the pass's, bounds it further.
	claudeVetTimeout = 10 * time.Second
)

// claudeCalls is what the claude calls on the removal paths (the pass,
// destroy and retask) run with: the environment that resolves claude from
// the filtered PATH and gives the checked empty directory each call runs
// from, and the jobs root removal.ResolveJobsRoot gives, which the
// job-directory vet checks.
//
// It travels on the context, so the two claude seams keep their signatures:
// a call whose context carries none lists the old way (resume and the watch
// read the listing outside the removal paths) and removes nothing.
type claudeCalls struct {
	home     string
	jobsRoot string
	// jobsErr is why the jobs root couldn't be resolved; every vet fails
	// with it.
	jobsErr error

	// root is the workspace root newClaudeCalls was given; its environment
	// is made on the first claude call, so a command that makes none
	// creates no scratch directory.
	root    string
	envOnce sync.Once
	env     *removal.Env
	// owned is set when close should remove env's scratch directory.
	owned bool
}

type claudeCallsKey struct{}

// withClaudeCalls returns ctx carrying cc.
func withClaudeCalls(ctx context.Context, cc *claudeCalls) context.Context {
	return context.WithValue(ctx, claudeCallsKey{}, cc)
}

// claudeCallsFrom returns the claudeCalls ctx carries, nil when none.
func claudeCallsFrom(ctx context.Context) *claudeCalls {
	cc, _ := ctx.Value(claudeCallsKey{}).(*claudeCalls)
	return cc
}

type removalSessionKey struct{}

// withRemovalSession returns ctx naming sessionID, the full session id of
// the session the caller chose to pass to claude rm under its short id.
// realRemoveClaudeSession refuses a call whose context names none, and one
// whose fresh listing holds the short id under another full id.
func withRemovalSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, removalSessionKey{}, sessionID)
}

// removalSessionFrom returns the full session id ctx names, empty when
// none.
func removalSessionFrom(ctx context.Context) string {
	id, _ := ctx.Value(removalSessionKey{}).(string)
	return id
}

// newClaudeEnvFunc resolves the claude environment for destroy and retask.
// A variable so a test can point it at a fake claude.
var newClaudeEnvFunc = func(root, home, pathEnv string) *removal.Env {
	return removal.NewClaudeEnv(root, home, pathEnv)
}

// newClaudeCalls makes the claudeCalls for one destroy or retask in the
// workspace at root: the jobs root resolved once through
// removal.ResolveJobsRoot, and, on the first claude call, its own scratch
// directory under ~/.niwa/w-<key>/scratch/. close removes that directory.
func newClaudeCalls(root string) *claudeCalls {
	home, homeErr := os.UserHomeDir()
	cc := &claudeCalls{home: home, root: root, owned: true}
	if homeErr != nil {
		cc.jobsErr = fmt.Errorf("the home directory can't be resolved: %v", homeErr)
	} else {
		cc.jobsRoot, cc.jobsErr = removal.ResolveJobsRoot(os.LookupEnv, home)
	}
	return cc
}

// passClaudeCalls is the pass's claudeCalls: its environment and its jobs
// root, both already resolved for the pass, which closes the environment
// itself.
func passClaudeCalls(env *removal.Env, jobsRoot string) *claudeCalls {
	cc := &claudeCalls{env: env, home: userHomeDir(), jobsRoot: jobsRoot}
	cc.envOnce.Do(func() {})
	return cc
}

// environment returns the environment claude calls run with, making it on
// the first call.
func (cc *claudeCalls) environment() *removal.Env {
	cc.envOnce.Do(func() {
		cc.env = newClaudeEnvFunc(cc.root, cc.home, os.Getenv("PATH"))
	})
	return cc.env
}

// command builds one claude call through the environment.
func (cc *claudeCalls) command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	return cc.environment().ClaudeCommand(ctx, args...)
}

// close removes the scratch directory newClaudeCalls made, when a claude
// call made one.
func (cc *claudeCalls) close() {
	if cc == nil || !cc.owned {
		return
	}
	cc.envOnce.Do(func() {})
	cc.env.Close()
}

// vet runs the job-directory vet on session id's own job directory under
// its own 10-second context, cut shorter by ctx's deadline.
func (cc *claudeCalls) vet(ctx context.Context, id string) error {
	if cc.jobsErr != nil {
		return cc.jobsErr
	}
	ctx, cancel := context.WithTimeout(ctx, claudeVetTimeout)
	defer cancel()
	return removal.VetJobDir(ctx, cc.jobsRoot, cc.home, id)
}

// realListClaudeSessions reads every background session Claude Code knows
// about, finished ones included. With claudeCalls on ctx, claude runs by
// its absolute path from the filtered PATH, from the checked empty
// directory; without, as found on PATH from niwa's own directory.
func realListClaudeSessions(ctx context.Context) ([]claudeSessionRow, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeListTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if cc := claudeCallsFrom(ctx); cc != nil {
		c, err := cc.command(ctx, "agents", "--json", "--all")
		if err != nil {
			return nil, fmt.Errorf("claude agents: %w", err)
		}
		cmd = c
	} else {
		bin, err := exec.LookPath("claude")
		if err != nil {
			return nil, errors.New("claude binary not found in PATH")
		}
		cmd = exec.CommandContext(ctx, bin, "agents", "--json", "--all")
	}
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("claude agents timed out after %s", claudeListTimeout)
		}
		return nil, fmt.Errorf("claude agents: %w", err)
	}
	return decodeClaudeRows(out)
}

// decodeClaudeRows decodes claude agents --json --all output strictly: one
// JSON array of objects, no null row, nothing after it, and every field
// niwa reads of its expected type. Anything else is an error, which no
// caller reads as an empty listing.
func decodeClaudeRows(out []byte) ([]claudeSessionRow, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var raw []json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading claude agents output: %w", err)
	}
	if raw == nil {
		return nil, errors.New("reading claude agents output: not a list")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("reading claude agents output: more after the list")
	}
	rows := make([]claudeSessionRow, 0, len(raw))
	for i, r := range raw {
		if t := bytes.TrimSpace(r); len(t) == 0 || t[0] != '{' {
			return nil, fmt.Errorf("reading claude agents output: row %d is not an object", i)
		}
		var row claudeSessionRow
		if err := json.Unmarshal(r, &row); err != nil {
			return nil, fmt.Errorf("reading claude agents output: row %d: %w", i, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// realRemoveClaudeSession runs `claude rm <id>` and returns its combined
// output. It is the only place niwa issues claude rm, and it runs only with
// claudeCalls on ctx. Right before the call it lists Claude Code's sessions
// again and refuses a session that now reads running, one the listing no
// longer holds or holds more than once under id, one it holds under a full
// session id other than the one ctx names (withRemovalSession), a call
// whose ctx names none, and any listing that can't be read, since an
// earlier listing can be stale by then (destroy's waits on its
// confirmation prompt). It then vets the session's own job
// directory, and on a failure makes no call and returns the failing path as
// the reason. claude runs by its absolute path from the filtered PATH, from
// the checked empty directory. It never passes --discard-unpushed or
// --force-remove-worktree: when Claude Code refuses to delete a session's
// worktree, that refusal stands and is reported, rather than niwa
// overriding it. A call that exits non-zero or times out returns an error
// with Claude Code's message, and the caller keeps what it would have
// removed.
func realRemoveClaudeSession(ctx context.Context, id string) (string, error) {
	cc := claudeCallsFrom(ctx)
	if cc == nil {
		return "", errors.New("no checked directory to run claude rm from")
	}
	if !watch.IsSafeHandle(id) {
		return "", fmt.Errorf("claude reported an unusable id %q", id)
	}
	if cc.jobsErr != nil {
		return "", cc.jobsErr
	}
	row, err := stoppedSessionRow(ctx, id)
	if err != nil {
		return "", err
	}
	// The job directory is found by the full session id, the id the rule's
	// walk uses, not by the short id claude rm takes.
	if err := cc.vet(ctx, row.SessionID); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, claudeRemoveTimeout)
	defer cancel()
	cmd, err := cc.command(ctx, "rm", id)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := firstLine(string(out), err)
		if ctx.Err() != nil {
			msg = fmt.Sprintf("timed out after %s", claudeRemoveTimeout)
		}
		return string(out), fmt.Errorf("claude rm %s: %s", id, msg)
	}
	return string(out), nil
}

// stoppedSessionRow lists Claude Code's sessions through ctx's claudeCalls
// and returns the one row listed under id, refusing when ctx names no full
// session id, the listing can't be read, holds no row or more than one
// under id, the row reads running, or the row's full session id isn't the
// one ctx names.
func stoppedSessionRow(ctx context.Context, id string) (claudeSessionRow, error) {
	want := removalSessionFrom(ctx)
	if want == "" {
		return claudeSessionRow{}, fmt.Errorf("Claude session %s: no full session id was given for claude rm", id)
	}
	rows, err := listClaudeSessionsFunc(ctx)
	if err != nil {
		return claudeSessionRow{}, fmt.Errorf("Claude session %s: Claude Code's sessions could not be listed right before claude rm: %v", id, err)
	}
	var found []claudeSessionRow
	for _, r := range rows {
		if r.ID == id {
			found = append(found, r)
		}
	}
	switch {
	case len(found) == 0:
		return claudeSessionRow{}, fmt.Errorf("Claude Code no longer lists session %s right before claude rm", id)
	case len(found) > 1:
		return claudeSessionRow{}, fmt.Errorf("Claude Code lists more than one session under %s right before claude rm", id)
	case found[0].running():
		return claudeSessionRow{}, fmt.Errorf("Claude session %s is still running right before claude rm", id)
	case found[0].SessionID != want:
		return claudeSessionRow{}, fmt.Errorf("Claude Code lists session %s under full id %s right before claude rm, not %s", id, found[0].SessionID, want)
	}
	return found[0], nil
}

// listedRemovals is the sessions among owners that a removal will pass to
// claude rm: each one rows lists, by its full session id. A listed session
// that is running, or that Claude Code lists under an id niwa won't pass to
// a command, is returned in refused with why, and is passed to no claude
// rm. A session absent from the listing is in neither.
func listedRemovals(owners []workspace.SessionMapping, rows []claudeSessionRow) (remove []claudeSessionRow, refused []string) {
	byID := make(map[string]claudeSessionRow, len(rows))
	for _, r := range rows {
		if r.SessionID != "" {
			byID[r.SessionID] = r
		}
	}
	seen := map[string]bool{}
	for _, m := range owners {
		row, listed := byID[m.SessionID]
		if !listed || seen[m.SessionID] {
			continue
		}
		seen[m.SessionID] = true
		switch {
		case !watch.IsSafeHandle(row.ID):
			refused = append(refused, fmt.Sprintf("Claude session %s: claude reported an unusable id %q", m.SessionID, row.ID))
		case row.running():
			refused = append(refused, fmt.Sprintf("Claude session %s is still running", row.ID))
		default:
			remove = append(remove, row)
		}
	}
	return remove, refused
}

// vetBeforeRemoval is the job-directory vet of every session a removal will
// pass to claude rm, run before its first removal step. It returns the
// first failure, naming the session and the path. Each job directory is
// found by the full session id, as the rule's walk finds it.
func vetBeforeRemoval(ctx context.Context, cc *claudeCalls, sessions []claudeSessionRow) error {
	for _, s := range sessions {
		if err := cc.vet(ctx, s.SessionID); err != nil {
			return fmt.Errorf("the job directory of Claude session %s failed its check: %w", s.ID, err)
		}
	}
	return nil
}

// vetDestroySessions is destroy's vet before its first removal step: every
// mapped Claude session of instanceDirs that the listing holds and that
// removeOwningSessionsListed will pass to claude rm has its job directory
// vetted. A listing or a session store that can't be read passes no session
// to claude rm, so it vets none; removeOwningSessionsListed reports it.
func vetDestroySessions(ctx context.Context, cc *claudeCalls, workspaceRoot string, instanceDirs []string, list claudeSessionLister) error {
	owners := owningClaudeMappings(workspace.NewReporter(io.Discard), workspaceRoot, instanceDirs)
	if len(owners) == 0 {
		return nil
	}
	rows, err := list(ctx)
	if err != nil {
		return nil
	}
	remove, _ := listedRemovals(owners, rows)
	return vetBeforeRemoval(ctx, cc, remove)
}

// removeOwningSessions removes the Claude Code background sessions that own
// the given instances, so destroying an instance doesn't leave its session
// listed in the agents view against a directory that no longer exists.
//
// An instance's owners are the sessions the workspace's session store maps to
// it (written by the SessionStart hook and by dispatch). Every mapping that
// names the instance counts, not only the newest: each dispatch into an
// instance writes one, and an older session is just as stale a row once the
// directory is gone. A session started by hand inside an instance has no
// mapping and is never touched.
//
// This lives here rather than in workspace.DestroyInstance because it needs
// the workspace root, for the session store, and because the reaper calls
// DestroyInstance too: the reaper only reclaims an instance once its session
// is already gone, so it has nothing for this step to do.
//
// For each owner:
//   - gone from `claude agents` already: the workspace's on-reap command runs
//     for it, and its mapping is deleted once that succeeds;
//   - still running (a live process): it is kept, with its mapping, and the
//     line says how to remove it once it stops. destroy never stops a
//     session, and runs no command for one it kept;
//   - otherwise: `claude rm <id>` removes it, then the on-reap command runs
//     and the mapping is deleted once that succeeds. Claude Code keeps the
//     transcript, so the line names the resume command. A failed removal runs
//     no command and keeps the mapping.
//
// A failed or timed-out on-reap run keeps the mapping, and the next reap that
// finds the session gone runs the command again (see on_reap.go).
//
// Every failure is reported and swallowed, the same contract as destroy's
// plugin-record and trust cleanup: by the time this runs the user has asked
// for the instance to go and niwa's unpushed-work check has passed, so a
// leftover session never blocks the teardown. Callers run it before the
// instance directory is removed, while Claude Code can still inspect any
// worktree the session made inside it.
func removeOwningSessions(reporter *workspace.Reporter, workspaceRoot string, instanceDirs []string) {
	removeOwningSessionsListed(context.Background(), reporter, workspaceRoot, instanceDirs, listClaudeSessionsFunc)
}

// removeOwningSessionsListed is removeOwningSessions reading the sessions
// through list, so a destroy whose preflight already listed them doesn't ask
// Claude Code twice. Each claude rm gets ctx, which carries the claudeCalls
// it runs with; a failure, the job-directory vet's included, is reported
// with its reason scrubbed and keeps the session and its mapping.
func removeOwningSessionsListed(ctx context.Context, reporter *workspace.Reporter, workspaceRoot string, instanceDirs []string, list claudeSessionLister) {
	owners := owningClaudeMappings(reporter, workspaceRoot, instanceDirs)
	if len(owners) == 0 {
		return
	}

	rows, err := list(ctx)
	if err != nil {
		reporter.Warn("could not check the Claude sessions these instances own (%v); left them in place", err)
		return
	}
	byID := make(map[string]claudeSessionRow, len(rows))
	for _, r := range rows {
		if r.SessionID != "" {
			byID[r.SessionID] = r
		}
	}

	// The on-reap command runs from the workspace root, which outlives the
	// instance directory, after each removal that succeeded and for each
	// session already gone. The mapping is deleted only once it succeeds; a
	// failed run keeps it for the next reap. destroy itself never retries one.
	onReap := newOnReapStep(workspaceRoot, reporter.Writer())
	for _, m := range owners {
		row, listed := byID[m.SessionID]
		if !listed {
			// Already deleted from the agents view; only the mapping is left.
			onReap.settle(m)
			continue
		}
		// The id comes from another program's output and becomes an argv
		// element, so a value that could read as a flag is refused.
		if !watch.IsSafeHandle(row.ID) {
			reporter.Warn("could not remove Claude session %s: claude reported an unusable id %q", m.SessionID, row.ID)
			continue
		}
		if row.running() {
			reporter.Log("kept Claude session %s: it is still running; once it stops (claude stop %s), remove it with claude rm %s", row.ID, row.ID, row.ID)
			continue
		}
		out, err := removeClaudeSessionFunc(withRemovalSession(ctx, row.SessionID), row.ID)
		if err != nil {
			reporter.Warn("could not remove Claude session %s: %s; remove it with claude rm %s", row.ID, removal.ScrubEvidence(firstLine(out, err), 400), row.ID)
			continue
		}
		reporter.Log("removed Claude session %s; its transcript is kept: claude --resume %s", row.ID, m.SessionID)
		onReap.settle(m)
	}
}

// owningClaudeMappings returns the Claude session mappings that name one of
// instanceDirs. Mappings for another agent are left out: only Claude Code has
// the background-session row this removes.
func owningClaudeMappings(reporter *workspace.Reporter, workspaceRoot string, instanceDirs []string) []workspace.SessionMapping {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		reporter.Warn("could not read the session store (%v); left any Claude sessions these instances own in place", err)
		return nil
	}
	return claudeOwners(mappings, instanceDirs)
}

// claudeOwners returns the Claude session mappings among mappings that name
// one of instanceDirs.
func claudeOwners(mappings []workspace.SessionMapping, instanceDirs []string) []workspace.SessionMapping {
	dirs := make(map[string]bool, len(instanceDirs))
	for _, d := range instanceDirs {
		dirs[filepath.Clean(d)] = true
	}
	var owners []workspace.SessionMapping
	for _, m := range mappings {
		if !dirs[filepath.Clean(m.InstancePath)] {
			continue
		}
		if ag, err := agent.ParseAgent(m.Agent); err != nil || ag != agent.AgentClaude {
			continue
		}
		owners = append(owners, m)
	}
	return owners
}

// firstLine returns the first non-empty line of a command's output, falling
// back to the error when the command printed nothing.
func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
