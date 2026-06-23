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

// jobState is the subset of ~/.claude/jobs/<id>/state.json this command reads
// for the coordinator-vs-worker guard. The dir name is the session-id prefix;
// the full SessionID inside confirms the match.
type jobState struct {
	SessionID string `json:"sessionId"`
	Template  string `json:"template"`
}

// provisionResult carries the outcome of a successful instance provision: the
// instance directory name and its absolute path. It mirrors the machine
// surface of `niwa create --json` ({name, path}), which the real provisioner
// reuses.
type provisionResult struct {
	Name string
	Path string
}

// provisionInstanceFunc provisions an ephemeral instance for the given session
// under workspaceRoot, naming it from namePrefix (the `--name` suffix). It
// returns the created instance's name and absolute path. It is a package
// variable so tests can substitute a fake provisioner that does no clone,
// exercising the guard + mapping + injection logic in isolation.
var provisionInstanceFunc = realProvisionInstance

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

// defaultJobsDir returns the Claude Code jobs directory (~/.claude/jobs). A
// failure to resolve the home directory yields an empty string, which the
// guard treats as "no job state" (no-op), so a missing HOME never aborts.
func defaultJobsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "jobs")
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

	if !sessionStartGuardPasses(workspaceRoot, payload.Cwd, payload.SessionID, jobsDir) {
		return nil
	}

	namePrefix := payload.SessionID[:sessionNamePrefixLen]
	res, err := provisionInstanceFunc(cmd.Context(), workspaceRoot, payload.Cwd, namePrefix)
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

// runInstanceHookEnd handles a SessionEnd hook. Teardown is added in the next
// issue (DESIGN Decision 6); for now it is a clean no-op so the SessionEnd
// branch is wired but inert.
func runInstanceHookEnd(_ *cobra.Command, _ instanceHookPayload) error {
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

// readJobState locates the job-state file for sessionID under jobsDir and
// decodes it. The job dir name is the session-id prefix, so it first tries an
// exact match on the full id, then falls back to scanning for a directory
// whose name is a prefix of sessionID (the empirically observed layout). It
// returns ok=false on any miss or decode failure.
func readJobState(jobsDir, sessionID string) (jobState, bool) {
	// Fast path: a directory named by the full session id.
	if js, ok := decodeJobState(filepath.Join(jobsDir, sessionID, "state.json")); ok {
		return js, true
	}

	// Fall back to scanning for a job dir whose name is a prefix of the
	// session id (the observed layout uses a leading slice of the UUID).
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return jobState{}, false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "" || len(name) > len(sessionID) {
			continue
		}
		if sessionID[:len(name)] != name {
			continue
		}
		if js, ok := decodeJobState(filepath.Join(jobsDir, name, "state.json")); ok {
			return js, true
		}
	}
	return jobState{}, false
}

// decodeJobState reads and decodes a single job-state file. ok=false on any
// read or parse failure.
func decodeJobState(path string) (jobState, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return jobState{}, false
	}
	var js jobState
	if err := json.Unmarshal(data, &js); err != nil {
		return jobState{}, false
	}
	return js, true
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
// instance under workspaceRoot named <config>-<prefix>. It is wired into
// provisionInstanceFunc; tests override that variable to avoid a real clone.
func realProvisionInstance(ctx context.Context, workspaceRoot, cwd, namePrefix string) (provisionResult, error) {
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

	instanceName, err := computeInstanceName(configName, namePrefix, workspaceRoot)
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
