package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(instanceCmd)
	instanceCmd.AddCommand(instanceFromHookCmd)
}

// instanceCmd is the parent of `niwa instance ...`. It hosts the
// workspace-root Claude Code session hook entry point (`from-hook`). It is
// deliberately DISTINCT from the per-repo worktree hook (`niwa worktree
// from-hook`, internal/cli/session_from_hook_cmd.go): that command operates at
// the worktree level on WorktreeCreate/WorktreeRemove events, while this one
// operates at the instance level on Claude SessionStart/SessionEnd events. The
// two share nothing but the `from-hook` suffix convention. See DESIGN
// Decision 2 (naming -- avoid the "session" collision).
var instanceCmd = &cobra.Command{
	Use:    "instance",
	Short:  "Instance-level operations (Claude Code session hook entry point)",
	Hidden: true,
}

// instanceFromHookCmd is the thin entry Claude Code invokes as the
// workspace-root SessionStart/SessionEnd hook command (an absolute-path
// `niwa instance from-hook`, piping the hook JSON on stdin). It reads the
// payload, validates session_id, and dispatches on hook_event_name to the
// provisioning (SessionStart) or teardown (SessionEnd) path.
var instanceFromHookCmd = &cobra.Command{
	Use:    "from-hook",
	Short:  "Internal: dispatch a Claude Code session hook (reads JSON on stdin)",
	Hidden: true,
	Long: `Internal entry point invoked directly by Claude Code's workspace-root
SessionStart/SessionEnd hooks.

Reads the Claude hook JSON payload on stdin and dispatches on
hook_event_name. Not intended for direct human use; the hook command is an
absolute-path "niwa instance from-hook" written into the workspace-root
.claude/settings.json by niwa.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runInstanceFromHook,
}

// instanceHookPayload is the subset of the Claude Code SessionStart/SessionEnd
// hook JSON this command reads. Absent fields decode to their zero value.
type instanceHookPayload struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	Source         string `json:"source"`
}

const (
	hookEventSessionStart = "SessionStart"
	hookEventSessionEnd   = "SessionEnd"

	// bgJobTemplate is the job-state `template` value Claude Code records for a
	// dispatched background worker. An interactive/foreground session carries
	// "claude". This is the confirmed coordinator-vs-worker discriminator (see
	// DESIGN Decision 3).
	bgJobTemplate = "bg"

	// sessionNamePrefixLen is how many leading hex characters of the session
	// UUID are used as the `--name` suffix for the provisioned instance. A
	// UUID prefix is filesystem-safe; 12 chars keeps collisions negligible
	// while sidestepping the NextInstanceNumber race an unnamed concurrent
	// create would hit (DESIGN Decision 5).
	sessionNamePrefixLen = 12
)

// provisionResult carries the outcome of a successful instance provision: the
// instance directory name and its absolute path. It mirrors the machine
// surface of `niwa create --json` ({name, path}), which the real provisioner
// reuses.
type provisionResult struct {
	Name string
	Path string
}

// provisionInstanceFunc provisions an ephemeral instance for the given session
// under workspaceRoot, naming it from namePrefix (the `--name` suffix) joined
// to the config name with sep. The hook path passes "-" (so the hook-created
// name stays byte-identical to before); the dispatch path passes "+" when a
// user slug is present so the config|slug boundary is unambiguous. It returns
// the created instance's name and absolute path. It is a package variable so
// tests can substitute a fake provisioner that does no clone, exercising the
// guard + mapping + injection logic in isolation.
var provisionInstanceFunc = realProvisionInstance

// destroyInstanceFunc force-destroys the instance at instancePath. It is a
// package variable so SessionEnd teardown tests can substitute a fake that
// records the call without touching the filesystem.
var destroyInstanceFunc = realDestroyInstance

func runInstanceFromHook(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return fmt.Errorf("niwa: error: reading hook payload from stdin: %w", err)
	}

	var payload instanceHookPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("niwa: error: parsing hook payload JSON: %w", err)
	}

	switch payload.HookEventName {
	case hookEventSessionStart:
		return runInstanceHookStart(cmd, payload, defaultJobsDir())
	case hookEventSessionEnd:
		return runInstanceHookEnd(cmd, payload)
	default:
		// Unknown event: no-op exit 0. Unlike the worktree create hook, neither
		// session event blocks the session, so an unrecognized event is not a
		// hard error -- it is simply not ours to handle.
		return nil
	}
}

// runInstanceHookStart handles a SessionStart hook. It applies the three-part
// guard (DESIGN Decision 3); on passing it provisions an ephemeral instance,
// writes the session->instance mapping, and emits the additionalContext
// injection JSON on stdout. If any guard fails it is a clean no-op (exit 0, no
// output), so ordinary sessions are untouched.
//
// jobsDir is injected (rather than read from the environment) so the guard's
// job-state read is unit-testable against a fixture jobs tree.
func runInstanceHookStart(cmd *cobra.Command, payload instanceHookPayload, jobsDir string) error {
	// session_id flows from untrusted hook stdin into a path component and the
	// instance name. Reject anything that is not a canonical UUID before use.
	if !workspace.ValidSessionID(payload.SessionID) {
		return nil
	}

	// The hook's cwd is the launch root (the workspace root for a dispatched
	// session). Resolve the workspace root from it; if it does not resolve to a
	// workspace, this is not a session we provision for.
	workspaceRoot, ok := resolveHookWorkspaceRoot(payload.Cwd)
	if !ok {
		return nil
	}

	// Defense in depth for the reaper backstop: a dispatched bg worker booting
	// inside an ephemeral dispatch instance that still carries the pending marker
	// but has no mapping self-heals by writing the mapping itself, so the worker
	// is never left as an unmapped orphan the backstop could reap even if its
	// dispatch parent died before mapping it. This is a terminal case (the worker
	// is already inside its instance), so on success it returns without
	// provisioning a new one.
	if maybeAdoptSelf(workspaceRoot, payload, jobsDir) {
		return nil
	}

	if !sessionStartGuardPasses(workspaceRoot, payload.Cwd, payload.SessionID, jobsDir) {
		return nil
	}

	namePrefix := payload.SessionID[:sessionNamePrefixLen]
	// The hook joins the session-hex suffix with "-" so the provisioned name
	// stays "<config>-<sessionhex>", byte-identical to the pre-"+"-separator
	// behavior. "+" is reserved for user-supplied dispatch slugs.
	res, err := provisionInstanceFunc(cmd.Context(), workspaceRoot, payload.Cwd, namePrefix, "-")
	if err != nil {
		return fmt.Errorf("niwa: error: provisioning instance for session %s: %w", payload.SessionID, err)
	}

	mapping := workspace.SessionMapping{
		SessionID:      payload.SessionID,
		InstanceName:   res.Name,
		InstancePath:   res.Path,
		TranscriptPath: payload.TranscriptPath,
		Ephemeral:      true,
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, mapping); err != nil {
		return fmt.Errorf("niwa: error: writing session mapping for %s: %w", payload.SessionID, err)
	}

	out, err := buildSessionStartInjection(res.Path)
	if err != nil {
		return fmt.Errorf("niwa: error: assembling session context: %w", err)
	}
	if _, err := cmd.OutOrStdout().Write(out); err != nil {
		return fmt.Errorf("niwa: error: writing session context: %w", err)
	}
	return nil
}

// runInstanceHookEnd handles a SessionEnd hook. It is a deliberate NO-OP: it
// never destroys an instance and never deletes a mapping (DESIGN Decision 6,
// revised -- delete-only teardown).
//
// SessionEnd is NOT a deletion signal. Claude Code fires it on idle-suspend
// (`reason: resume`), `/clear`, logout, and similar -- none of which mean the
// session was deleted from the Agent View. A session that finishes a task or
// goes idle is still listed and resumable, and tearing its instance down here
// (as the original code did) reclaimed instances while their sessions were
// still alive. Teardown therefore lives entirely in the reaper, which keys on
// the session's job entry disappearing (the proxy for an explicit delete); this
// handler does nothing.
//
// The case is left wired in runInstanceFromHook's dispatch as defense in depth:
// a workspace whose settings.json was materialized before this change still
// carries a SessionEnd hook entry until it re-applies, and this no-op guarantees
// that stale entry cannot destroy anything. The path always exits 0.
func runInstanceHookEnd(cmd *cobra.Command, payload instanceHookPayload) error {
	// Intentionally a no-op: never resolve a mapping, never destroy, never
	// delete. The reaper is the single teardown path.
	_ = cmd
	_ = payload
	return nil
}

// sessionStartGuardPasses evaluates the three-part SessionStart guard (DESIGN
// Decision 3). All three must hold for provisioning to proceed:
//
//  1. The workspace root is in ephemeral-session mode (the opt-in master
//     switch, default off).
//  2. The session is a dispatched background worker: its job state at
//     <jobsDir>/<session-id-prefix>/state.json exists, its sessionId confirms
//     the match, and its template == "bg". CLAUDE_JOB_DIR is intentionally NOT
//     consulted -- it is not reliably set.
//  3. Re-entrancy: the launch cwd does not already resolve inside a niwa
//     instance (a worker that dispatches sub-sessions must not nest).
func sessionStartGuardPasses(workspaceRoot, cwd, sessionID, jobsDir string) bool {
	// (1) Master switch.
	if !workspace.EphemeralSessionMode(workspaceRoot) {
		return false
	}

	// (2) Background-job detection.
	if !isBackgroundWorker(jobsDir, sessionID) {
		return false
	}

	// (3) Re-entrancy: already inside a genuine instance -> no-op. The
	// discovered dir must be a real instance, not the workspace root, which
	// also carries a .niwa/instance.json (the root state file holding the
	// ephemeral-mode flag). ValidateInstanceDir rejects a workspace root (it
	// also carries .niwa/workspace.toml), so it is the discriminator between
	// "inside an instance" and "at the workspace root that merely has root
	// state".
	if dir, err := workspace.DiscoverInstance(cwd); err == nil {
		if workspace.ValidateInstanceDir(dir) == nil {
			return false
		}
	}

	return true
}

// isBackgroundWorker reports whether sessionID is a dispatched background
// worker by reading its Claude Code job state. The job dir is named by the
// session-id prefix; the full sessionId inside state.json must confirm the
// match before the template is trusted. Any read/parse failure, a sessionId
// mismatch, or template != "bg" yields false (fail safe). An empty jobsDir
// (HOME unresolved) yields false.
func isBackgroundWorker(jobsDir, sessionID string) bool {
	if jobsDir == "" {
		return false
	}
	js, ok := readJobState(jobsDir, sessionID)
	if !ok {
		return false
	}
	// The dir is keyed by the session-id prefix, so confirm the full id inside
	// matches before trusting the template -- a colliding prefix must not be
	// mistaken for this session.
	if js.SessionID != "" && js.SessionID != sessionID {
		return false
	}
	return js.Template == bgJobTemplate
}

// maybeAdoptSelf handles the adopt-self case of the SessionStart guard: a
// dispatched background worker booting INSIDE an ephemeral dispatch instance that
// still carries the pending marker but has no mapping writes the mapping itself.
// It returns true when it took ownership of this hook invocation (the worker is
// already inside its instance, so the caller must NOT provision a new one),
// whether or not the mapping write ultimately succeeded; it returns false when
// this is not the adopt-self shape, so the caller falls through to the ordinary
// three-part guard.
//
// This is the defense-in-depth complement to the reaper backstop's own liveness
// check: it makes the worker's own boot a mapping source, so the data-loss path
// (a dispatch parent that dies before writing the mapping, leaving a live but
// unmapped instance) is closed even without the parent surviving. It deliberately
// mirrors the ordinary guard's gates -- ephemeral mode on, a confirmed bg worker
// -- so an ordinary session is never touched, and it relaxes only guard #3
// (re-entrancy) for exactly this case: the instance must be a dispatch instance
// with an unresolved pending marker and no mapping, not any nested/dev instance.
func maybeAdoptSelf(workspaceRoot string, payload instanceHookPayload, jobsDir string) bool {
	// Honor the master switch: outside ephemeral-session mode the hook is inert.
	if !workspace.EphemeralSessionMode(workspaceRoot) {
		return false
	}

	// Only a dispatched background worker self-heals.
	if !isBackgroundWorker(jobsDir, payload.SessionID) {
		return false
	}

	// The worker must be booting inside a genuine instance (the condition that
	// makes guard #3 no-op for a dispatched worker). A workspace-root or
	// non-instance cwd is not an adopt-self case.
	instanceDir, err := workspace.DiscoverInstance(payload.Cwd)
	if err != nil {
		return false
	}
	if workspace.ValidateInstanceDir(instanceDir) != nil {
		return false
	}

	// The instance must be a dispatch instance carrying an unresolved pending
	// marker: dispatch drops the marker at create and removes it only after the
	// mapping is durably written, so marker-present + unmapped is precisely the
	// "parent died before mapping" shape. A non-dispatch instance, or a dispatch
	// instance whose marker was already cleaned up, is not healed here.
	if !isDispatchInstanceName(filepath.Base(instanceDir)) {
		return false
	}
	if _, err := os.Stat(filepath.Join(instanceDir, dispatchPendingMarker)); err != nil {
		return false
	}

	// If a mapping already exists for this session (the parent mapped it, or a
	// prior hook run did), there is nothing to heal -- fall through to the guard,
	// which no-ops on re-entrancy.
	if _, err := workspace.ReadSessionMapping(workspaceRoot, payload.SessionID); err == nil {
		return false
	}

	// Write the missing mapping. This is the terminal action for this worker's
	// SessionStart: it is already inside its instance, so we never provision a
	// nested one regardless of the write outcome.
	mapping := workspace.SessionMapping{
		SessionID:      payload.SessionID,
		InstanceName:   filepath.Base(instanceDir),
		InstancePath:   instanceDir,
		TranscriptPath: payload.TranscriptPath,
		Ephemeral:      true,
		Origin:         selfHealedOrigin,
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, mapping); err != nil {
		// Best effort: a failed self-heal must not break the session. The reaper
		// backstop's own liveness check remains the primary protection.
		fmt.Fprintf(os.Stderr, "niwa: warning: self-healing session mapping for %s: %v\n", payload.SessionID, err)
	}
	return true
}

// sessionStartInjection is the SessionStart hook output shape Claude Code
// consumes: hookSpecificOutput.additionalContext is injected into the
// session's context.
type sessionStartInjection struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// buildSessionStartInjection assembles the SessionStart hook JSON carrying the
// additionalContext payload: the instance path, the instance's CLAUDE.md
// content (so the agent operates under the instance's guidance without a
// re-root), and an explicit instruction to cd into the instance before any
// work (DESIGN Decision 4). A missing instance CLAUDE.md is tolerated (the
// path + cd instruction still inject); only the instance path is load-bearing.
func buildSessionStartInjection(instancePath string) ([]byte, error) {
	claudeMD := ""
	if data, err := os.ReadFile(filepath.Join(instancePath, "CLAUDE.md")); err == nil {
		claudeMD = string(data)
	}

	var b []byte
	b = append(b, "A dedicated niwa instance has been provisioned for this session at:\n\n  "...)
	b = append(b, instancePath...)
	b = append(b, "\n\nBefore doing any work, run this first so all tools operate inside the instance:\n\n  cd "...)
	b = append(b, instancePath...)
	b = append(b, "\n\n"...)
	if claudeMD != "" {
		b = append(b, "The instance's CLAUDE.md follows; treat it as the authoritative guidance for this session:\n\n"...)
		b = append(b, claudeMD...)
	}

	var inj sessionStartInjection
	inj.HookSpecificOutput.HookEventName = hookEventSessionStart
	inj.HookSpecificOutput.AdditionalContext = string(b)

	encoded, err := json.Marshal(inj)
	if err != nil {
		return nil, fmt.Errorf("marshaling session start injection: %w", err)
	}
	return append(encoded, '\n'), nil
}

// resolveHookWorkspaceRoot resolves the workspace root from the hook's
// reported cwd (the launch root). It returns ok=false when cwd is empty or
// does not classify to a workspace root, so the caller no-ops rather than
// guessing a root.
func resolveHookWorkspaceRoot(cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return "", false
	}
	if class.WorkspaceRoot == "" {
		return "", false
	}
	return class.WorkspaceRoot, true
}

// realProvisionInstance is the production provisioner: it reuses the same
// applier.Create path `niwa create --json --name <prefix>` drives, cloning an
// instance under workspaceRoot named <config><sep><prefix>. sep is "-" for the
// hook path and "+" for a user-supplied dispatch slug. It is wired into
// provisionInstanceFunc; tests override that variable to avoid a real clone.
func realProvisionInstance(ctx context.Context, workspaceRoot, cwd, namePrefix, sep string) (provisionResult, error) {
	configPath, configDir, err := config.Discover(cwd)
	if err != nil {
		return provisionResult{}, fmt.Errorf("discovering workspace config from %q: %w", cwd, err)
	}

	result, err := config.Load(configPath)
	if err != nil {
		return provisionResult{}, err
	}
	cfg := result.Config

	configName, err := resolveEffectiveWorkspaceName(workspaceRoot, cfg)
	if err != nil {
		return provisionResult{}, err
	}

	instanceName, err := computeInstanceName(configName, namePrefix, sep, workspaceRoot)
	if err != nil {
		return provisionResult{}, err
	}

	token := resolveGitHubToken()
	gh := github.NewAPIClient(token)

	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporter(os.Stderr)
	configurePluginAutoInstall(applier, false)

	if globalCfg, gErr := config.LoadGlobalConfig(); gErr == nil {
		if gDir, gErr := config.GlobalConfigDir(); gErr == nil {
			applier.GlobalConfigDir = gDir
		}
		if entry := globalCfg.LookupWorkspace(configName); entry != nil {
			applier.ConfigSourceURL = entry.SourceURL
		}
	}

	instancePath, err := applier.Create(ctx, cfg, configDir, workspaceRoot, instanceName)
	if err != nil {
		return provisionResult{}, err
	}

	return provisionResult{Name: instanceName, Path: instancePath}, nil
}

// realDestroyInstance is the production teardown: it force-destroys the
// instance directory, equivalent to `niwa destroy --force <instance>`. It
// validates the directory is a destroyable instance (not a workspace root)
// first. It is wired into destroyInstanceFunc; tests override that variable.
func realDestroyInstance(instancePath string) error {
	if err := workspace.ValidateInstanceDir(instancePath); err != nil {
		return err
	}
	return workspace.DestroyInstance(instancePath)
}
