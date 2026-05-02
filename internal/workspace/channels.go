package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tsukumogami/niwa/internal/config"
)

// Role name format per PRD R6: lowercase alphanumeric start, up to 32
// chars of lowercase alphanumerics and hyphens.
var roleNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// coordinatorRole is the reserved role name for the instance root session.
const coordinatorRole = "coordinator"

// instanceMCPConfigName is the basename of the project-scoped MCP config
// file Claude Code's discovery loads at the cwd directory root. It lives
// at the directory root, not under .claude/.
const instanceMCPConfigName = ".mcp.json"

// InstanceMCPConfigPath returns the absolute path to the niwa-managed
// MCP config file at instanceRoot. Three callers must agree on this
// path:
//
//   - the channels installer that writes the file (this package)
//   - the daemon's worker spawn that hands the path to claude via
//     --mcp-config (internal/cli/mesh_watch.go::spawnWorker)
//   - the functional-test coordinator launcher that does the same
//     (test/functional/mesh_steps_test.go)
//
// All three reference this helper so a future move (e.g., to
// .niwa/.mcp.json, or per-repo files keyed differently) is a one-line
// change. See issue #78 for the longer-term strategy review.
func InstanceMCPConfigPath(instanceRoot string) string {
	return filepath.Join(instanceRoot, instanceMCPConfigName)
}

// WorkerMCPConfigPath returns the path where spawnWorker writes the per-spawn
// worker MCP config. Lives inside the task directory so it is co-located with
// envelope.json and state.json.
func WorkerMCPConfigPath(instanceRoot, taskID string) string {
	return filepath.Join(instanceRoot, ".niwa", "tasks", taskID, "worker.mcp.json")
}

// WorkerMCPConfig generates the JSON content for a per-spawn worker MCP
// config. The generated file is passed to `claude -p` via --mcp-config so
// that Claude Code's env-block processing delivers the correct
// NIWA_SESSION_ROLE (the worker's actual role, not "coordinator") and
// NIWA_TASK_ID to the niwa mcp-serve subprocess.
//
// Using a per-spawn config prevents the instance-root .mcp.json's hardcoded
// NIWA_SESSION_ROLE=coordinator from overriding the worker's actual role when
// Claude Code merges the env block on top of the inherited process environment.
func WorkerMCPConfig(instanceRoot, role, taskID string) ([]byte, error) {
	cmdPath, err := os.Executable()
	if err != nil || cmdPath == "" {
		cmdPath = "niwa"
	}
	if !utf8.ValidString(cmdPath) {
		return nil, fmt.Errorf("niwa binary path is not valid UTF-8: %q", cmdPath)
	}
	if !utf8.ValidString(instanceRoot) {
		return nil, fmt.Errorf("instance root is not valid UTF-8: %q", instanceRoot)
	}
	if !utf8.ValidString(role) {
		return nil, fmt.Errorf("role is not valid UTF-8: %q", role)
	}
	if !utf8.ValidString(taskID) {
		return nil, fmt.Errorf("task ID is not valid UTF-8: %q", taskID)
	}
	cmdJSON, _ := json.Marshal(cmdPath)
	rootJSON, _ := json.Marshal(instanceRoot)
	roleJSON, _ := json.Marshal(role)
	taskIDJSON, _ := json.Marshal(taskID)
	return []byte(fmt.Sprintf(workerMCPTemplate, string(cmdJSON), string(rootJSON), string(roleJSON), string(taskIDJSON))), nil
}

// channelsMCPTemplate is the template for the instance-root `.mcp.json`.
// It registers the niwa mcp-serve command with NIWA_INSTANCE_ROOT baked in
// so Claude Code can start the MCP server without any user configuration.
// The command field is the absolute path to the provisioning niwa binary
// (resolved via os.Executable at apply time) so the MCP server always
// matches the version that provisioned the instance; this avoids PATH
// ambiguity when multiple niwa installs coexist on one machine.
const channelsMCPTemplate = `{
  "mcpServers": {
    "niwa": {
      "type": "stdio",
      "command": %s,
      "args": ["mcp-serve"],
      "env": {
        "NIWA_INSTANCE_ROOT": %s,
        "NIWA_SESSION_ROLE": "coordinator"
      }
    }
  }
}
`

// workerMCPTemplate is the per-spawn template for worker MCP config files.
// Unlike channelsMCPTemplate (which hardcodes NIWA_SESSION_ROLE=coordinator
// for the coordinator), this template takes the actual target role and task
// ID as format arguments so each worker gets the right env block values.
// Claude Code's env-block processing applies these on top of the spawned
// process's inherited environment — having the correct values here ensures
// the MCP subprocess always picks up the right role regardless of env
// inheritance semantics.
const workerMCPTemplate = `{
  "mcpServers": {
    "niwa": {
      "type": "stdio",
      "command": %s,
      "args": ["mcp-serve"],
      "env": {
        "NIWA_INSTANCE_ROOT": %s,
        "NIWA_SESSION_ROLE": %s,
        "NIWA_TASK_ID": %s
      }
    }
  }
}
`

// channelsSectionHeader is the marker used to detect an already-present
// ## Channels section in workspace-context.md.
const channelsSectionHeader = "## Channels"

// niwaMCPToolNames is the canonical list of the 11 niwa MCP tools. The
// order is stable: it is emitted both in the SKILL.md allowed-tools block
// and in the ## Channels section. Callers that change this list MUST
// update the skill body's tool references and the PRD's R10 enumeration
// in lockstep.
var niwaMCPToolNames = []string{
	"niwa_delegate",
	"niwa_query_task",
	"niwa_await_task",
	"niwa_report_progress",
	"niwa_finish_task",
	"niwa_list_outbound_tasks",
	"niwa_update_task",
	"niwa_cancel_task",
	"niwa_ask",
	"niwa_send_message",
	"niwa_check_messages",
}

// inboxSubdirs is the canonical set of per-role inbox subdirectories that
// messages transition through: the top-level inbox (queued), plus
// in-progress, cancelled, expired, and read. Every directory is created
// with mode 0700 under the role's inbox/ root.
var inboxSubdirs = []string{"in-progress", "cancelled", "expired", "read"}

// uuidV4RE matches a UUIDv4 string anywhere. Used by the migration helper
// to detect pre-1.0 .niwa/sessions/<uuid>/ subdirectories.
var uuidV4RE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// skillFrontmatterCharLimit is Claude Code's combined frontmatter cap
// (name + description + allowed-tools). buildSkillContent keeps its
// frontmatter under this limit; channels_test.go asserts it as a raw-
// byte upper bound.
const skillFrontmatterCharLimit = 1536

// InstallChannelInfrastructure provisions the per-role mesh filesystem
// layout for a channeled workspace. It is a no-op when channels are not
// enabled. When enabled it:
//
//  1. Runs the pre-1.0 migration helper: removes .niwa/sessions/<uuid>/
//     directories and warns once to stderr; preserves sessions.json.
//  2. Enumerates roles from workspace topology (coordinator + one per
//     cloned repo) and [channels.mesh.roles] overrides; validates
//     collision, reserved-name, and format constraints.
//  3. Creates .niwa/roles/<role>/inbox/{in-progress,cancelled,expired,read}/
//     for every role, plus .niwa/tasks/, .niwa/daemon.pid, .niwa/daemon.log.
//  4. Writes `<instanceRoot>/.mcp.json` (the project-scoped MCP config
//     Claude Code reads when launched at the instance root, per the
//     PRD's headline scenario). NIWA_INSTANCE_ROOT is baked into the
//     env block so the MCP server resolves the right workspace. No
//     per-repo mirror is written — see the rationale at the call site
//     and issue #78 for the trade-off matrix.
//  5. Installs the niwa-mesh SKILL.md at instance-root and per-repo
//     .claude/skills/niwa-mesh/ with byte-compare idempotency — writes only
//     when the on-disk bytes differ from the installer's output, emits a
//     single-line stderr drift warning on overwrite.
//  6. Writes the minimal ## Channels section into workspace-context.md.
//
// Every installer-written path is appended to *writtenFiles so that the
// apply pipeline can track the file in InstanceState.ManagedFiles,
// including workspace-context.md (the installer owns only the ## Channels
// section but still tracks the file for destroy-time cleanup). Runtime
// artifacts (.niwa/tasks/<id>/*, .niwa/roles/*/inbox/<id>.json) are NOT
// tracked — the daemon and MCP handlers write those at runtime and manage
// their own lifecycle.
//
// The signature is preserved from the prior implementation so the call
// site in Applier.runPipeline (step 4.75) is unchanged.
func InstallChannelInfrastructure(cfg *config.WorkspaceConfig, instanceRoot string, writtenFiles *[]string) error {
	if !cfg.Channels.IsEnabled() {
		return nil
	}

	niwaDir := filepath.Join(instanceRoot, ".niwa")

	// Step 1: Pre-1.0 migration helper. Runs before we enumerate roles so
	// that a pre-1.0 workspace is cleaned even if enumeration would fail
	// (e.g., an orphan repo directory survived a prior manual edit).
	if err := migratePre1Layout(niwaDir); err != nil {
		return fmt.Errorf("migrating pre-1.0 mesh layout: %w", err)
	}

	// Step 2: Enumerate and validate roles.
	roles, err := enumerateRoles(cfg, instanceRoot)
	if err != nil {
		return err
	}

	// Step 3: Directory scaffolding. Every directory gets mode 0700
	// independent of umask; mkdirAllMode does the chmod ladder.
	if err := mkdirAllMode(niwaDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa dir: %w", err)
	}
	tasksDir := filepath.Join(niwaDir, "tasks")
	if err := mkdirAllMode(tasksDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa/tasks: %w", err)
	}
	rolesRoot := filepath.Join(niwaDir, "roles")
	if err := mkdirAllMode(rolesRoot, 0o700); err != nil {
		return fmt.Errorf("creating .niwa/roles: %w", err)
	}

	// .niwa/sessions/sessions.json is the coordinator session registry
	// (per PRD R39, R40 and AC-P5). Workers are not registered, only
	// coordinators. Provision the directory and an empty registry file
	// here so `niwa session list` finds a well-formed file from apply
	// time, not a lazy-create on first `niwa session register`. Create
	// only if absent — re-apply must NOT overwrite a populated registry
	// (that would wipe live session entries).
	sessionsDir := filepath.Join(niwaDir, "sessions")
	if err := mkdirAllMode(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa/sessions: %w", err)
	}
	sessionsPath := filepath.Join(sessionsDir, "sessions.json")
	if _, err := os.Stat(sessionsPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("checking sessions.json: %w", err)
		}
		if err := os.WriteFile(sessionsPath, []byte("{\"sessions\":[]}\n"), 0o600); err != nil {
			return fmt.Errorf("writing sessions.json: %w", err)
		}
	}
	*writtenFiles = append(*writtenFiles, sessionsPath)

	// Create per-role inbox trees. Role enumeration is stable-sorted by
	// enumerateRoles; walking the sorted slice keeps directory creation
	// order deterministic for easier test assertions.
	for _, r := range roles {
		inboxDir := filepath.Join(rolesRoot, r.name, "inbox")
		if err := mkdirAllMode(inboxDir, 0o700); err != nil {
			return fmt.Errorf("creating inbox for role %q: %w", r.name, err)
		}
		for _, sub := range inboxSubdirs {
			p := filepath.Join(inboxDir, sub)
			if err := mkdirAllMode(p, 0o700); err != nil {
				return fmt.Errorf("creating %s for role %q: %w", sub, r.name, err)
			}
		}
	}

	// daemon.pid / daemon.log placeholders. daemon.pid is created empty;
	// the daemon overwrites it atomically when it starts. daemon.log is
	// created empty; the daemon opens it with O_APPEND. Both are tracked
	// as ManagedFiles so niwa destroy cleans them up.
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	if err := ensureEmptyFile(pidPath, 0o600); err != nil {
		return fmt.Errorf("creating daemon.pid: %w", err)
	}
	*writtenFiles = append(*writtenFiles, pidPath)

	logPath := filepath.Join(niwaDir, "daemon.log")
	if err := ensureEmptyFile(logPath, 0o600); err != nil {
		return fmt.Errorf("creating daemon.log: %w", err)
	}
	*writtenFiles = append(*writtenFiles, logPath)

	// Step 4: project-scoped MCP config at the instance root. Claude
	// Code's discovery loads `<cwd>/.mcp.json` only, no parent walk-up,
	// so the instance root is the cwd where the headline scenario "open
	// Claude here and delegate" finds niwa tools. The file lives at the
	// directory root, not under `.claude/`. Per-repo writes are
	// deliberately omitted to avoid colliding with projects that ship
	// their own `.mcp.json`; see issue #78. Workers don't depend on
	// discovery — `mesh_watch.go::spawnWorker` passes `--mcp-config` to
	// this same file with `--strict-mcp-config`.
	mcpContent, err := buildMCPContent(instanceRoot)
	if err != nil {
		return fmt.Errorf("building .mcp.json content: %w", err)
	}
	instanceMCPPath := InstanceMCPConfigPath(instanceRoot)
	if err := writeIdempotent(instanceMCPPath, mcpContent, 0o600, os.Stderr); err != nil {
		return fmt.Errorf("writing instance .mcp.json: %w", err)
	}
	*writtenFiles = append(*writtenFiles, instanceMCPPath)

	// Step 5: niwa-mesh SKILL.md at instance-root and per-repo. Content
	// is identical across paths (flat uniform skill, Decision 5).
	skillContent := buildSkillContent()
	instanceSkill := filepath.Join(instanceRoot, ".claude", "skills", "niwa-mesh", "SKILL.md")
	if err := writeIdempotent(instanceSkill, skillContent, 0o600, os.Stderr); err != nil {
		return fmt.Errorf("writing instance SKILL.md: %w", err)
	}
	*writtenFiles = append(*writtenFiles, instanceSkill)

	for _, r := range roles {
		if r.name == coordinatorRole {
			continue
		}
		if r.repoPath == "" {
			continue
		}
		repoSkill := filepath.Join(r.repoPath, ".claude", "skills", "niwa-mesh", "SKILL.md")
		if err := writeIdempotent(repoSkill, skillContent, 0o600, os.Stderr); err != nil {
			return fmt.Errorf("writing %s: %w", repoSkill, err)
		}
		*writtenFiles = append(*writtenFiles, repoSkill)
	}

	// Step 6: Hook scripts on disk. HooksMaterializer reads Scripts as
	// file paths in Step 6.5 of runPipeline. injectChannelHooks (called
	// in Step 0 of runPipeline) has already recorded these paths in
	// cfg.Claude.Hooks; we just need the files to exist.
	hooksDir := filepath.Join(niwaDir, "hooks")
	if err := mkdirAllMode(hooksDir, 0o700); err != nil {
		return fmt.Errorf("creating hooks dir: %w", err)
	}
	// Hook source scripts live under .niwa/ and therefore follow R48's
	// file-mode rule (0600). HooksMaterializer (step 6.5) reads these
	// bytes and writes them to .claude/hooks/<event>/ with mode 0755
	// where Claude Code actually invokes them; the source files never
	// need the execute bit themselves.
	sessionStartPath := filepath.Join(hooksDir, "mesh-session-start.sh")
	if err := writeIdempotent(sessionStartPath, []byte("#!/bin/sh\nniwa session register\n"), 0o600, os.Stderr); err != nil {
		return fmt.Errorf("writing mesh-session-start.sh: %w", err)
	}
	*writtenFiles = append(*writtenFiles, sessionStartPath)

	userPromptPath := filepath.Join(hooksDir, "mesh-user-prompt-submit.sh")
	if err := writeIdempotent(userPromptPath, []byte("#!/bin/sh\nniwa session register --check-only\n"), 0o600, os.Stderr); err != nil {
		return fmt.Errorf("writing mesh-user-prompt-submit.sh: %w", err)
	}
	*writtenFiles = append(*writtenFiles, userPromptPath)

	// Stop hook: reset the stall watchdog at every turn boundary. The absolute
	// binary path is resolved at apply time so the hook works even when niwa
	// is not on PATH inside Claude Code's hook execution environment.
	niwaBin, exErr := os.Executable()
	if exErr != nil || niwaBin == "" {
		niwaBin = "niwa"
	}
	stopHooksDir := filepath.Join(hooksDir, "stop")
	if err := mkdirAllMode(stopHooksDir, 0o700); err != nil {
		return fmt.Errorf("creating hooks/stop dir: %w", err)
	}
	stopScriptPath := filepath.Join(stopHooksDir, "report-progress.sh")
	stopScriptContent := []byte("#!/bin/sh\n" + niwaBin + " mesh report-progress\n")
	if err := writeIdempotent(stopScriptPath, stopScriptContent, 0o600, os.Stderr); err != nil {
		return fmt.Errorf("writing report-progress.sh: %w", err)
	}
	*writtenFiles = append(*writtenFiles, stopScriptPath)

	// Step 7: workspace-context.md ## Channels section. The coordinator
	// is the only reader (workers read the task envelope, not this file)
	// so Role is hardcoded. See Decision 5 / PRD R12.
	ctxPath := filepath.Join(instanceRoot, workspaceContextFile)
	if err := writeChannelsSection(ctxPath, instanceRoot); err != nil {
		return fmt.Errorf("writing channels section: %w", err)
	}
	*writtenFiles = append(*writtenFiles, ctxPath)

	return nil
}

// roleEntry pairs a validated role name with the cloned repo directory
// that owns its inbox, or "" for coordinator (inbox lives at the instance
// root).
type roleEntry struct {
	name     string
	repoPath string
}

// enumerateRoles derives the complete set of roles for this workspace,
// validates formatting and uniqueness, and returns the list sorted by
// role name for deterministic downstream processing.
//
// The enumeration rules are:
//
//  1. coordinator is always present (reserved for the instance root).
//  2. Every immediate subdirectory of every group directory under
//     instanceRoot contributes a role whose name is the repo's directory
//     basename (topology-derived). These are the cloned repos at the
//     time channels are being installed.
//  3. Explicit [channels.mesh.roles] entries override the topology for a
//     given role name. The value is treated as either an absolute path
//     (rare) or a workspace-relative repo directory (common).
//
// Validation rules:
//
//   - Role name must match ^[a-z0-9][a-z0-9-]{0,31}$ (PRD R6).
//   - "coordinator" as an explicit [channels.mesh.roles] entry targeting
//     anything other than the instance root is rejected (AC-R3).
//   - Two topology-derived repos with the same basename collide and fail
//     apply (AC-R2). Users resolve via explicit entries.
func enumerateRoles(cfg *config.WorkspaceConfig, instanceRoot string) ([]roleEntry, error) {
	// Collect explicit overrides first so collision checks below know to
	// skip the topology path for any name the user pinned manually.
	explicit := map[string]string{}
	if cfg.Channels.Mesh != nil {
		for k, v := range cfg.Channels.Mesh.Roles {
			explicit[k] = v
		}
	}

	// Validate the coordinator override first: mapping "coordinator" to
	// any non-empty path is a reserved-name error because coordinator is
	// definitionally the instance root.
	if v, ok := explicit[coordinatorRole]; ok {
		if v != "" && v != "." {
			return nil, fmt.Errorf(
				"role %q is reserved for the instance root; "+
					"remove the [channels.mesh.roles.%s] entry or leave its path empty",
				coordinatorRole, coordinatorRole,
			)
		}
	}

	// Validate explicit names' formats up front so bad names surface with
	// a specific configuration error instead of an opaque filesystem error.
	for name := range explicit {
		if name == coordinatorRole {
			continue
		}
		if !roleNameRE.MatchString(name) {
			return nil, fmt.Errorf(
				"role name %q in [channels.mesh.roles] must match ^[a-z0-9][a-z0-9-]{0,31}$",
				name,
			)
		}
	}

	// Enumerate topology-derived roles in a single walk of the instance
	// root. We need two things at once: the absolute on-disk path for
	// each repo basename (for per-repo SKILL.md mirroring) and the
	// occurrence count (for collision detection per AC-R2). A missing
	// instance root is possible on a create path before clones have
	// finished; treat it as zero repos so coordinator-only workspaces
	// install cleanly.
	topologyPaths := map[string]string{}
	repoOccurrences := map[string]int{}
	entries, err := os.ReadDir(instanceRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("enumerating repos for role derivation: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || isHiddenOrSkip(name) {
			continue
		}
		groupDir := filepath.Join(instanceRoot, name)
		repoEntries, rerr := os.ReadDir(groupDir)
		if rerr != nil {
			continue
		}
		for _, repo := range repoEntries {
			repoName := repo.Name()
			if !repo.IsDir() || isHiddenOrSkip(repoName) {
				continue
			}
			// When the same basename appears in two groups, the second
			// iteration's path wins for topologyPaths. That ordering only
			// matters in the explicit-override case below; without an
			// override, the duplicate is detected via repoOccurrences and
			// rejected as a hard error (AC-R2).
			topologyPaths[repoName] = filepath.Join(groupDir, repoName)
			repoOccurrences[repoName]++
		}
	}

	// Collision detection: the PRD defines a collision as two
	// topology-derived repos sharing a basename AND the user did NOT
	// provide an explicit [channels.mesh.roles] entry that disambiguates.
	// A repeat basename without an explicit entry is a hard failure (AC-R2).
	for repoName, count := range repoOccurrences {
		if count <= 1 {
			continue
		}
		if _, pinned := explicit[repoName]; pinned {
			continue
		}
		return nil, fmt.Errorf(
			"role name %q is derived from multiple repo basenames; "+
				"add an explicit [channels.mesh.roles] entry to disambiguate",
			repoName,
		)
	}

	// Validate topology names too so a repo whose basename isn't a valid
	// role name fails loudly. We don't rewrite the basename silently.
	for name := range topologyPaths {
		if !roleNameRE.MatchString(name) {
			if _, pinned := explicit[name]; pinned {
				continue
			}
			return nil, fmt.Errorf(
				"repo basename %q cannot be used as a role name; "+
					"it must match ^[a-z0-9][a-z0-9-]{0,31}$. Add an "+
					"explicit [channels.mesh.roles] entry to map it to a valid role name",
				name,
			)
		}
	}

	// Build the final role set. Explicit entries override topology-
	// derived names. A role present in explicit but not in topology is
	// treated as a virtual peer (no repo dir to mirror to).
	final := map[string]string{
		coordinatorRole: "",
	}
	for name, path := range topologyPaths {
		final[name] = path
	}
	for name, v := range explicit {
		if name == coordinatorRole {
			continue
		}
		// Resolve explicit value as workspace-relative path when it's
		// non-empty and not already absolute.
		resolved := ""
		if v != "" {
			if filepath.IsAbs(v) {
				resolved = v
			} else {
				resolved = filepath.Join(instanceRoot, v)
			}
		} else if existing, ok := final[name]; ok {
			// Bare explicit entry with empty value: keep the topology
			// path if one exists; otherwise leave as a virtual peer.
			resolved = existing
		}
		final[name] = resolved
	}

	result := make([]roleEntry, 0, len(final))
	for name, path := range final {
		result = append(result, roleEntry{name: name, repoPath: path})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

// migratePre1Layout detects and removes pre-1.0 .niwa/sessions/<uuid>/
// subdirectories when the new .niwa/roles/ layout is absent. It emits a
// single stderr warning on the first observed upgrade; sessions.json is
// preserved so the coordinator registry survives the schema break.
//
// The detection is conservative: the helper runs only when both
// conditions hold (old uuid dirs present AND roles/ absent). A subsequent
// apply on the new layout is a no-op because roles/ exists.
func migratePre1Layout(niwaDir string) error {
	sessionsDir := filepath.Join(niwaDir, "sessions")
	rolesDir := filepath.Join(niwaDir, "roles")

	// Short-circuit if new layout already exists.
	if _, err := os.Stat(rolesDir); err == nil {
		return nil
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var uuidDirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if uuidV4RE.MatchString(e.Name()) {
			uuidDirs = append(uuidDirs, e.Name())
		}
	}
	if len(uuidDirs) == 0 {
		return nil
	}

	// One-shot warning. Content per Decision 7 step 2 of the design doc.
	fmt.Fprintf(os.Stderr,
		"niwa: upgrading mesh layout. Discarding %d session directories from the previous mesh version; "+
			"any in-flight conversations are abandoned. Run 'niwa destroy && niwa create --channels' for a fresh start. "+
			"See docs/guides/cross-session-communication.md for details.\n",
		len(uuidDirs),
	)

	for _, name := range uuidDirs {
		p := filepath.Join(sessionsDir, name)
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("removing pre-1.0 session dir %s: %w", p, err)
		}
	}
	return nil
}

// buildMCPContent returns the bytes for `<instance>/.mcp.json`. It
// errors when the niwa binary path or the instance root contain
// invalid UTF-8 — extremely rare on Linux but reachable on
// filesystems with mojibake-encoded paths. json.Marshal of a string
// silently coerces invalid bytes to U+FFFD, so without this guard
// the file would land on disk with a path Claude Code would later
// fail to launch with a confusing "no such file" — checking up front
// produces a clear apply-time error instead.
//
// The command field resolves to the absolute path of the provisioning
// niwa binary (os.Executable) so Claude Code's MCP subprocess launcher
// does not depend on PATH. The NIWA_INSTANCE_ROOT env entry is
// JSON-marshaled so instance paths with spaces, quotes, or other
// special characters are preserved correctly.
func buildMCPContent(instanceRoot string) ([]byte, error) {
	cmdPath, err := os.Executable()
	if err != nil || cmdPath == "" {
		// Defensive default: fall back to PATH resolution when
		// os.Executable() returns empty (very rare on POSIX). Workers
		// spawned via this fallback rely on `niwa` being on PATH at
		// claude-launch time. Prefer the absolute path when available.
		cmdPath = "niwa"
	}
	if !utf8.ValidString(cmdPath) {
		return nil, fmt.Errorf("niwa binary path is not valid UTF-8: %q", cmdPath)
	}
	if !utf8.ValidString(instanceRoot) {
		return nil, fmt.Errorf("instance root is not valid UTF-8: %q", instanceRoot)
	}
	cmdJSON, _ := json.Marshal(cmdPath)
	rootJSON, _ := json.Marshal(instanceRoot)
	return []byte(fmt.Sprintf(channelsMCPTemplate, string(cmdJSON), string(rootJSON))), nil
}

// buildSkillContent returns the canonical niwa-mesh SKILL.md content. The
// six body sections required by PRD R10 are emitted in stable order with
// substantive content per section; Decision 5 of the design doc is the
// source of truth for what each section describes.
//
// The frontmatter description is intentionally under ~800 chars so that
// name + description + allowed-tools fit comfortably within Claude Code's
// skillFrontmatterCharLimit combined frontmatter cap.
func buildSkillContent() []byte {
	var b bytes.Buffer
	b.WriteString("---\n")
	b.WriteString("name: niwa-mesh\n")
	b.WriteString("description: >-\n")
	b.WriteString("  Delegate tasks across niwa workspace roles. Use when the user asks to\n")
	b.WriteString("  dispatch work to another agent, check task status, receive progress,\n")
	b.WriteString("  report completion, or exchange peer messages. Tasks are first-class\n")
	b.WriteString("  objects with a queued/running/terminal lifecycle owned by the niwa\n")
	b.WriteString("  daemon. This skill describes the default behavior for every\n")
	b.WriteString("  participant: how to delegate synchronously vs asynchronously, how to\n")
	b.WriteString("  report progress during long-running work, how to complete or abandon\n")
	b.WriteString("  cleanly, which message vocabulary to use, how to ask peers for\n")
	b.WriteString("  clarification, and common patterns such as fan-out/collect or\n")
	b.WriteString("  worker-asks-coordinator. Invoke niwa MCP tools rather than writing\n")
	b.WriteString("  directly to the filesystem; the tool surface enforces authorization\n")
	b.WriteString("  and keeps the task state machine consistent across restarts.\n")
	b.WriteString("allowed-tools:\n")
	for _, name := range niwaMCPToolNames {
		fmt.Fprintf(&b, "  - %s\n", name)
	}
	b.WriteString("---\n\n")

	b.WriteString("# niwa-mesh\n\n")
	b.WriteString("Behavioral defaults for agents participating in the niwa mesh.\n\n")

	b.WriteString("## Delegation (sync vs async)\n\n")
	b.WriteString("Use `niwa_delegate` to hand work to another role. Pass `mode=\"sync\"`\n")
	b.WriteString("when you need the result inline and are willing to block until the\n")
	b.WriteString("worker finishes, is abandoned, or is cancelled; the call returns a\n")
	b.WriteString("`{status, ...}` envelope. Pass `mode=\"async\"` to return immediately\n")
	b.WriteString("with a `{task_id}` you can later hand to `niwa_query_task` or\n")
	b.WriteString("`niwa_await_task`. Prefer async when you plan to fan out to multiple\n")
	b.WriteString("roles in parallel or when the caller can make progress while the\n")
	b.WriteString("worker runs. The body you pass is the delegation payload; keep it\n")
	b.WriteString("self-contained because the worker reads the body via\n")
	b.WriteString("`niwa_check_messages` as its first action and does not have access to\n")
	b.WriteString("your surrounding conversation.\n\n")

	b.WriteString("## Reporting Progress\n\n")
	b.WriteString("Call `niwa_report_progress` every 3-5 minutes of wall-clock work, or\n")
	b.WriteString("every ~20 tool calls, whichever arrives sooner. The `summary` field is\n")
	b.WriteString("truncated to 200 characters and appears in `niwa task list`; the\n")
	b.WriteString("optional `body` carries structured detail (file paths touched, counts,\n")
	b.WriteString("sub-task IDs) and flows to any delegator waiting on\n")
	b.WriteString("`niwa_await_task`. Progress calls reset the stalled-progress watchdog,\n")
	b.WriteString("so regular reporting prevents SIGTERM escalation during long runs.\n\n")

	b.WriteString("## Completion Contract\n\n")
	b.WriteString("Every worker MUST call `niwa_finish_task` before exiting. Use\n")
	b.WriteString("`outcome=\"completed\"` with a `result` object when the task succeeded,\n")
	b.WriteString("or `outcome=\"abandoned\"` with a `reason` string when you cannot make\n")
	b.WriteString("further progress (missing preconditions, repeated tool failures,\n")
	b.WriteString("policy refusal). Exiting the process without calling `niwa_finish_task`\n")
	b.WriteString("is classified as an unexpected exit and consumes a retry slot; after\n")
	b.WriteString("the cap is reached the daemon transitions the task to `abandoned`\n")
	b.WriteString("with `reason: \"retry_cap_exceeded\"`. A second finish call on a\n")
	b.WriteString("terminal task returns `TASK_ALREADY_TERMINAL`.\n\n")

	b.WriteString("## Message Vocabulary\n\n")
	b.WriteString("Message `type` values follow the format\n")
	b.WriteString("`^[a-z][a-z0-9]*(\\.[a-z][a-z0-9]*)*$`. The daemon and MCP handlers\n")
	b.WriteString("emit: `task.progress`, `task.completed`, `task.abandoned`,\n")
	b.WriteString("`task.cancelled`. Agent-level peer exchanges use `question.ask`,\n")
	b.WriteString("`question.answer`, and `status.update`. Define new domain-specific\n")
	b.WriteString("types with a clear namespace prefix (`deploy.requested`,\n")
	b.WriteString("`review.completed`) and stay within the format regex. Unknown types on\n")
	b.WriteString("`niwa_send_message` return `BAD_TYPE`; unknown target roles return\n")
	b.WriteString("`UNKNOWN_ROLE`.\n\n")

	b.WriteString("## Peer Interaction\n\n")
	b.WriteString("Use `niwa_ask` when you need a synchronous reply from a peer: the\n")
	b.WriteString("call blocks until the peer responds or the timeout elapses (default\n")
	b.WriteString("600 seconds). If the target role has a live coordinator session,\n")
	b.WriteString("niwa routes the question directly to that session; otherwise it\n")
	b.WriteString("creates a task with `body.kind=\"ask\"` so the daemon can spawn a\n")
	b.WriteString("worker. A blocking coordinator receives questions via\n")
	b.WriteString("`niwa_await_task` returning `status:\"question_pending\"` with\n")
	b.WriteString("`ask_task_id` and `body`; a polling coordinator receives them via\n")
	b.WriteString("`niwa_check_messages` as `type==\"task.ask\"` messages. In both\n")
	b.WriteString("cases, answer by calling `niwa_finish_task(task_id=ask_task_id,\n")
	b.WriteString("outcome=\"completed\", result=...)`. Use `niwa_send_message` for\n")
	b.WriteString("one-way notifications where you do not need a reply. Inbox\n")
	b.WriteString("retrieval is via `niwa_check_messages`, which returns unread\n")
	b.WriteString("messages and moves them into `inbox/read/` atomically; expired\n")
	b.WriteString("messages are swept into `inbox/expired/` first and never\n")
	b.WriteString("returned.\n\n")

	b.WriteString("## Common Patterns\n\n")
	b.WriteString("Coordinator fan-out: call `niwa_delegate(mode=\"async\")` once per\n")
	b.WriteString("target role, collect the returned task IDs, then loop over them with\n")
	b.WriteString("`niwa_await_task` to gather results in completion order. For mixed\n")
	b.WriteString("workloads, use `niwa_list_outbound_tasks` to rediscover in-flight\n")
	b.WriteString("work after a restart. Worker asks coordinator: inside a running task,\n")
	b.WriteString("call `niwa_ask(to=\"coordinator\", body=...)` with a tight timeout\n")
	b.WriteString("(60-120s) when you need clarification; fall back to\n")
	b.WriteString("`niwa_finish_task(outcome=\"abandoned\", reason=\"blocked: <detail>\")`\n")
	b.WriteString("if the ask times out. Cancel/update: while a task is still queued,\n")
	b.WriteString("the delegator can call `niwa_update_task` (returns `updated` if the\n")
	b.WriteString("task is still queued, `too_late` once it is running) or\n")
	b.WriteString("`niwa_cancel_task` (atomic rename into `inbox/cancelled/`).\n")
	b.WriteString("Long-running tasks: `niwa_await_task` defaults to `timeout_seconds=600`\n")
	b.WriteString("(10 minutes). For tasks expected to take longer, pass an explicit\n")
	b.WriteString("`timeout_seconds` of `(estimated_minutes * 60 + buffer)` so the call\n")
	b.WriteString("doesn't return `{\"status\":\"timeout\"}` while the worker is still\n")
	b.WriteString("running. Re-await loop: on `status:\"timeout\"`, re-call\n")
	b.WriteString("`niwa_await_task(task_id=...)` instead of giving up — the worker\n")
	b.WriteString("is still progressing and the next call resumes the wait.\n")
	b.WriteString("Coordinator question-handling loop: while blocking on\n")
	b.WriteString("`niwa_await_task`, a `status:\"question_pending\"` result means a\n")
	b.WriteString("worker has a question. Answer with `niwa_finish_task(task_id=\n")
	b.WriteString("ask_task_id, outcome=\"completed\", result=...)`, then re-call\n")
	b.WriteString("`niwa_await_task(task_id=<original_task_id>)`. Repeat until the\n")
	b.WriteString("result is terminal or a timeout:\n")
	b.WriteString("  result = niwa_await_task(task_id=...)\n")
	b.WriteString("  while result.status == \"question_pending\":\n")
	b.WriteString("    niwa_finish_task(task_id=result.ask_task_id, outcome=\"completed\", result=...)\n")
	b.WriteString("    result = niwa_await_task(task_id=<same task_id>)\n")

	return b.Bytes()
}

// writeChannelsSection writes the minimal `## Channels` section into the
// workspace-context.md at ctxPath. If the file already has a ## Channels
// section, it is replaced wholesale (so edits to the format propagate on
// reapply without duplicate sections). If the file has no such section,
// the new section is appended.
func writeChannelsSection(ctxPath, instanceRoot string) error {
	existing, err := os.ReadFile(ctxPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content := string(existing)

	// Build the canonical section.
	var sb strings.Builder
	sb.WriteString(channelsSectionHeader + "\n\n")
	sb.WriteString("- Role: coordinator\n")
	fmt.Fprintf(&sb, "- NIWA_INSTANCE_ROOT: %s\n", instanceRoot)
	sb.WriteString("- Tools:\n")
	for _, t := range niwaMCPToolNames {
		fmt.Fprintf(&sb, "  - %s\n", t)
	}
	sb.WriteString("\nSee the `/niwa-mesh` skill for usage patterns.\n")
	newSection := sb.String()

	// Replace an existing section when present; otherwise append.
	// The section is delimited by the next `## ` line at the same level
	// or end of file.
	idx := strings.Index(content, channelsSectionHeader)
	if idx == -1 {
		trimmed := strings.TrimRight(content, "\n")
		var out string
		if trimmed == "" {
			out = newSection
		} else {
			out = trimmed + "\n\n" + newSection
		}
		return os.WriteFile(ctxPath, []byte(out), 0o600)
	}

	// Find the end of the Channels section: the next line starting with
	// `## ` at or after idx+len(header), or EOF.
	tailStart := idx + len(channelsSectionHeader)
	end := len(content)
	// Search line by line for the next heading.
	rest := content[tailStart:]
	for offset := 0; offset < len(rest); {
		nl := strings.IndexByte(rest[offset:], '\n')
		if nl == -1 {
			break
		}
		lineStart := offset + nl + 1
		if lineStart >= len(rest) {
			break
		}
		// Check if the next line starts a new ## heading.
		remaining := rest[lineStart:]
		if strings.HasPrefix(remaining, "## ") || strings.HasPrefix(remaining, "# ") {
			end = tailStart + lineStart
			break
		}
		offset = lineStart
	}
	out := content[:idx] + newSection
	if end < len(content) {
		// Preserve following sections; ensure a blank line separates.
		trailing := content[end:]
		out = strings.TrimRight(out, "\n") + "\n\n" + trailing
	}
	return os.WriteFile(ctxPath, []byte(out), 0o600)
}

// writeIdempotent writes data to path with the given mode, using
// byte-compare idempotency:
//
//   - If the on-disk content matches data byte-for-byte, skip the write
//     entirely (mtime stable, no spurious git churn).
//   - If the file exists and content differs, emit a single-line stderr
//     drift warning identifying the path, then overwrite.
//   - If the file does not exist, write it silently (first materialization
//     is not a drift event).
//
// Parent directories are created with mode 0700. The file is chmoded
// explicitly to the requested mode so that umask cannot widen the
// permissions.
func writeIdempotent(path string, data []byte, mode os.FileMode, driftOut *os.File) error {
	dir := filepath.Dir(path)
	if err := mkdirAllMode(dir, 0o700); err != nil {
		return err
	}

	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, data) {
			// Ensure mode is correct even when content matches; a
			// previous run under a broken umask could have left 0644.
			// Chmod is a no-op on matching permissions.
			return os.Chmod(path, mode)
		}
		// Drift: emit one warning line to driftOut.
		if driftOut != nil {
			fmt.Fprintf(driftOut, "niwa: drift detected in managed file %s; overwriting\n", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// tmp-then-rename keeps readers from seeing a partially-written file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// ensureEmptyFile creates path as an empty file with mode when absent.
// When present, the file is left untouched (so the daemon's PID file does
// not get wiped on re-apply). mode is only applied when creating.
func ensureEmptyFile(path string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := mkdirAllMode(dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Chmod(path, mode)
}

// isHiddenOrSkip reports whether a directory basename should be skipped
// during topology enumeration: reserved control directories (.niwa,
// .claude) and any dotfile. Shared by enumerateRoles so the top-level
// and second-level filters stay in sync.
func isHiddenOrSkip(name string) bool {
	if name == StateDir || name == ".claude" {
		return true
	}
	return len(name) > 0 && name[0] == '.'
}

// mkdirAllMode is os.MkdirAll with a Chmod ladder so that every created
// directory ends up at mode independent of umask. Existing directories
// are left at their current mode — we only set mode on directories this
// call actually creates.
func mkdirAllMode(path string, mode os.FileMode) error {
	// Fast path: already exists.
	if fi, err := os.Stat(path); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", path)
		}
		return nil
	}
	// Recursively create parent first.
	parent := filepath.Dir(path)
	if parent != path {
		if err := mkdirAllMode(parent, mode); err != nil {
			return err
		}
	}
	if err := os.Mkdir(path, mode); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return os.Chmod(path, mode)
}

// injectChannelHooks inserts SessionStart and UserPromptSubmit hook
// entries into cfg.Claude.Hooks when the workspace has channel config.
// Hook entries are prepended so they run before any user-defined hooks
// for the same event. This mutates cfg in place and is called at the top
// of runPipeline before any per-repo processing.
//
// HooksMaterializer reads Scripts as file paths and copies them with
// os.ReadFile, so these must point to real files on disk.
// InstallChannelInfrastructure writes the scripts; the call order in
// runPipeline (injectChannelHooks in Step 0 → InstallChannelInfrastructure
// in Step 4.75 → HooksMaterializer in Step 6.5) guarantees the files
// exist by the time the materializer tries to read them.
func injectChannelHooks(cfg *config.WorkspaceConfig, instanceRoot string) {
	if !cfg.Channels.IsEnabled() {
		return
	}

	if cfg.Claude.Hooks == nil {
		cfg.Claude.Hooks = make(config.HooksConfig)
	}

	hooksDir := filepath.Join(instanceRoot, ".niwa", "hooks")
	sessionStartScript := filepath.Join(hooksDir, "mesh-session-start.sh")
	userPromptScript := filepath.Join(hooksDir, "mesh-user-prompt-submit.sh")
	stopScript := filepath.Join(hooksDir, "stop", "report-progress.sh")

	sessionStartEntry := config.HookEntry{Scripts: []string{sessionStartScript}}
	userPromptEntry := config.HookEntry{Scripts: []string{userPromptScript}}
	stopEntry := config.HookEntry{Scripts: []string{stopScript}}

	cfg.Claude.Hooks["session_start"] = prependHookEntry(cfg.Claude.Hooks["session_start"], sessionStartEntry)
	cfg.Claude.Hooks["user_prompt_submit"] = prependHookEntry(cfg.Claude.Hooks["user_prompt_submit"], userPromptEntry)
	cfg.Claude.Hooks["stop"] = prependHookEntry(cfg.Claude.Hooks["stop"], stopEntry)
}

// prependHookEntry returns a new slice with entry prepended before existing.
func prependHookEntry(existing []config.HookEntry, entry config.HookEntry) []config.HookEntry {
	result := make([]config.HookEntry, 0, len(existing)+1)
	result = append(result, entry)
	result = append(result, existing...)
	return result
}
