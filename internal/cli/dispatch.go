package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/promptcapture"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	dispatchCmd.Flags().StringVar(&dispatchLabel, "label", "", "optional human-friendly alias recorded on the session mapping")
	dispatchCmd.Flags().StringVarP(&dispatchName, "name", "n", "", "optional display name for the session (sanitized into a slug; also names the niwa instance: <config>+-<id> with no name, <config>+<slug>-<id> with one -- '+' always marks the end of the config name)")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "model for the worker's main chat loop: a capability category or a versionless vendor name ("+knownModelHint(agent.AgentClaude)+"); overrides the [global] dispatch_model default")
	dispatchCmd.Flags().StringVar(&dispatchPermissionMode, "permission-mode", "", "permission mode to forward to the background worker (--permission-mode)")
	dispatchCmd.Flags().StringVar(&dispatchAgent, "agent", "", "agent to forward to the background worker (--agent)")
	dispatchCmd.Flags().BoolVarP(&dispatchDetach, "detach", "d", false, "do not attach the terminal to the new session; print hints and return")
	dispatchCmd.Flags().IntVar(&dispatchParallel, "parallel", 0,
		"maximum repos to clone concurrently when provisioning the dispatch instance (>=1). Lower this on slow or flaky networks; 1 clones serially. Overrides the [global] clone_workers config. 0 (the default) uses clone_workers, else niwa's built-in default.")
	// --keep-alive is tri-state (unset / explicit true / explicit false) so it
	// can override the [global] keep_alive_on_dispatch host default in BOTH
	// directions; a plain BoolVar cannot distinguish "not given" from "false".
	// NoOptDefVal makes the bare `--keep-alive` form mean explicit true.
	dispatchCmd.Flags().Var(triBoolValue{&dispatchKeepAlive}, "keep-alive", "arm a keep-alive self-wake on the dispatched worker so its remote-control session stays reachable across long idle (only applies when remote control is on; --keep-alive=false forces it off)")
	dispatchCmd.Flags().Lookup("keep-alive").NoOptDefVal = "true"
	rootCmd.AddCommand(dispatchCmd)
}

var (
	dispatchLabel          string
	dispatchName           string
	dispatchModel          string
	dispatchPermissionMode string
	dispatchAgent          string
	dispatchDetach         bool
	dispatchParallel       int
	// dispatchKeepAlive holds the tri-state --keep-alive value: nil when the
	// flag was not given, otherwise a pointer to the explicit true/false (see
	// triBoolValue in dispatch_keepalive.go).
	dispatchKeepAlive *bool
)

// maxDispatchSlugRunes caps the sanitized --name slug so it cannot dominate the
// instance directory name (which is "<config>+<slug>-<8hex>"). 40 runes is
// generous for a human-readable label while leaving room below filesystem name
// limits for the config prefix and the "-<8hex>" signature suffix.
const maxDispatchSlugRunes = 40

const (
	// dispatchPendingMarker is the file dropped inside a dispatch-created
	// instance at create time and removed only after the session mapping is
	// durably written. Its contents are an RFC3339 creation timestamp. The
	// marker is now a PRECISION aid for the reaper backstop's age check (it
	// carries the exact creation time), NOT the sole eligibility signal: the
	// backstop keys eligibility on the instance NAME (isDispatchInstanceName,
	// the purely structural "+<dash-free-slug>-<8hex>" signature) and falls back
	// to the directory mtime when the marker is absent (the SIGKILL-before-marker
	// case), so the orphan window is closed (DESIGN Decision 4).
	dispatchPendingMarker = ".niwa/dispatch-pending"

	// dispatchCaptureTimeout bounds the jobs-dir cwd-correlation poll that
	// recovers the worker's session UUID. Exhaustion is a capture failure that
	// triggers self-rollback, never a hang (DESIGN Decision 3, R20/R22).
	dispatchCaptureTimeout = 30 * time.Second

	// maxArgStringBytes is the largest byte length niwa lets a SINGLE argv
	// element reach. The prompt is handed to claude as one discrete argv element
	// (buildClaudeBgArgs), so the binding kernel limit is the per-string one --
	// not ARG_MAX, which bounds the TOTAL of argv plus envp. Leaving headroom
	// below ARG_MAX for the binary path, the flags, and the environment does
	// nothing for a single oversized string, and a single oversized string is
	// the only way a dispatch realistically hits an exec limit.
	//
	// On Linux the per-string limit is MAX_ARG_STRLEN, a fixed 32 pages (131072
	// bytes at the usual 4 KiB page size) counted INCLUSIVE of the NUL
	// terminator, so the largest argument execve accepts is one byte less.
	// Probed directly: 131071 succeeds, 131072 returns E2BIG.
	//
	// macOS imposes no per-string cap; it bounds argv plus envp together against
	// ARG_MAX (262144). We apply the Linux number on both platforms anyway. It
	// is the tighter constraint in practice, it keeps the accepted prompt size
	// identical everywhere -- so a prompt that dispatches on Linux dispatches on
	// macOS -- and it avoids modelling a total-size budget whose environment
	// term niwa does not control. The cost is that a darwin host would in
	// principle accept a somewhat larger prompt; the benefit is one number, one
	// behavior, and no platform-conditional cap to keep honest.
	maxArgStringBytes = 32*4096 - 1

	// dispatchPromptReserve is the byte allowance held back at validation time
	// for text niwa itself prepends to the prompt AFTER the check. Today that is
	// exactly keepAliveArmingInstruction, prepended in step (9d) -- long after
	// the step (1) check, and only when keep-alive resolves on, which cannot be
	// known before the instance exists. Reserving its full length up front makes
	// the single early check sound for BOTH outcomes: whatever (9d) decides, the
	// string that reaches execve still fits maxArgStringBytes, and the rejection
	// still lands before anything is provisioned.
	//
	// Anything else that starts prepending to the prompt must be added here.
	dispatchPromptReserve = len(keepAliveArmingInstruction)

	// maxPromptBytes is the largest user-supplied prompt dispatch accepts: the
	// exec ceiling minus everything niwa may later prepend. Exceeding it fails
	// clearly and early rather than letting exec reject the call with an opaque
	// E2BIG after an instance has already been provisioned (DESIGN Decision 8,
	// R43).
	maxPromptBytes = maxArgStringBytes - dispatchPromptReserve
)

// lookClaude reports the path to the claude binary or an error if it is not on
// PATH. It is a package variable so the preflight check is unit-testable
// without a real claude install (DESIGN Decision 9).
var lookClaude = func() (string, error) {
	return exec.LookPath("claude")
}

// dispatchCapture is the capture seam. Production wires it to captureSessionID;
// tests substitute a fake to return a fabricated UUID without a real jobs dir.
var dispatchCapture = captureSessionID

// dispatchAttach attaches the terminal to the given session by running
// `claude attach <id>` with inherited stdio. It is a package variable so tests
// can assert it is/isn't called and force a (non-fatal) failure without a real
// claude. It runs ONLY as the final step, after the mapping is durable, so its
// failure never rolls back (DESIGN Decision 1).
var dispatchAttach = func(id string) error {
	bin, err := lookClaude()
	if err != nil {
		return fmt.Errorf("claude binary not found in PATH: %w", err)
	}
	cmd := exec.Command(bin, "attach", id)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var dispatchCmd = &cobra.Command{
	Use:   "dispatch [prompt]",
	Short: "Launch a background Claude Code worker in a fresh ephemeral instance",
	Long: `dispatch creates a fresh ephemeral niwa instance, launches a Claude Code
background worker rooted inside it, captures the worker's session id, and
records an ephemeral dispatch-origin mapping so the instance is reclaimed when
the session ends.

The prompt is optional. With no prompt argument, dispatch opens an interactive
capture on the terminal: paste or type the task, press Enter to dispatch,
Ctrl-J for a newline, Ctrl-C to cancel. This is the way to hand a worker an
error you are looking at rather than one you can describe -- select it, run
'niwa dispatch', paste, press Enter.

The capture relies on the terminal marking where a paste begins and ends, which
current terminals do. On one that does not, a pasted line break is
indistinguishable from a typed one, so a multiline paste submits at its first
line and the rest reaches the shell; pass the prompt as an argument there.

With no prompt AND no terminal -- a script, a hook, a cron job -- dispatch fails
immediately rather than waiting on input that will never arrive.

By default the terminal then attaches to the new session (like docker run);
pass --detach/-d to skip the attach and return after printing the
attach/logs/stop hints (the mode for fan-out and scripting).

Any failure before the mapping is durable destroys the just-created instance,
so dispatch never leaves an unreclaimable instance DIRECTORY. One caveat: if the
worker launch succeeds but session-id capture then fails, the rollback deletes
the instance directory, but the detached background process keeps running -- we
never captured its session id, so we cannot stop it. That process has no mapping
and is harmless, but it is yours to 'claude stop' once you find it in 'claude
list'.`,
	Args:          cobra.MaximumNArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runDispatch,
}

// dispatchPromptCapture reads a prompt interactively from the terminal. It is a
// package variable so tests can drive the capture path without a terminal, and
// so the launcher-reachability test can stub it to fail if it is ever called
// from a path that must never be interactive.
var dispatchPromptCapture = promptcapture.Read

// dispatchInteractive reports whether an interactive capture can run. Both stdin
// and stderr must be terminals: the capture reads the first and renders to the
// second. Stdout is left out on purpose so it stays redirectable.
var dispatchInteractive = func() bool { return IsStdinTTY() && IsStderrTTY() }

func runDispatch(cmd *cobra.Command, args []string) error {
	// Arity selects the path, so no invocation can request both a positional
	// prompt and a capture. An explicit empty argument stays an error: it is
	// usually an unset variable in a script, and turning it into a capture
	// trigger would open an interactive prompt inside a cron job.
	var prompt string
	interactive := len(args) == 0
	if !interactive {
		prompt = args[0]

		// (1) Validate the prompt before touching anything.
		if err := validateDispatchPrompt(prompt); err != nil {
			return err
		}
	}

	// (2) Resolve the enclosing workspace root from cwd. Inside an instance or
	// worktree resolves to the shared workspace root (a self-dispatching worker
	// creates a sibling, never a nested instance); outside a workspace creates
	// NOTHING.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("niwa: error: getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("niwa: error: classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("niwa: error: not inside a niwa workspace; run dispatch from within a workspace")
	}
	workspaceRoot := class.WorkspaceRoot

	// (2b) niwa dispatch launches a Claude worker (it forwards Claude flags and
	// spawns the claude binary), so it refuses when the workspace's resolved
	// agent is not Claude -- otherwise the instance would be prepared for another
	// agent whose context the launched Claude worker cannot read. The resolved
	// agent comes from NIWA_AGENT and the workspace default_agent; dispatch's own
	// --agent flag is Claude's subagent passthrough (a different thing), so the
	// escape hatch from a Codex-default workspace is NIWA_AGENT=claude. A config
	// that cannot be loaded is left to the provisioning path to report.
	if wsCfg, cfgErr := config.Load(filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)); cfgErr == nil {
		resolvedAgent, agErr := resolveSessionAgent("", wsCfg.Config)
		if agErr != nil {
			return fmt.Errorf("niwa: error: %w", agErr)
		}
		if resolvedAgent != agent.AgentClaude {
			return fmt.Errorf("niwa: error: niwa dispatch launches a Claude worker; this workspace's agent is %q, which background dispatch does not support yet. Set NIWA_AGENT=claude to dispatch a Claude worker, or wait for Codex background dispatch", resolvedAgent)
		}
	}

	// (3) Preflight claude on PATH BEFORE creating any instance, so an absent
	// claude fails with no instance dir and no mapping on disk (R16, R13).
	if _, err := lookClaude(); err != nil {
		return fmt.Errorf("niwa: error: claude binary not found in PATH; install Claude Code before dispatching")
	}

	// (3b) With no positional prompt, capture one from the terminal. This sits
	// after every preflight check and before anything is created, so abandoning
	// the capture costs nothing -- there is no instance to roll back -- and a
	// wrong workspace or a missing claude fails before the developer types.
	if interactive {
		if !dispatchInteractive() {
			return fmt.Errorf("niwa: error: no prompt given and this is not an interactive terminal; pass the prompt as an argument: niwa dispatch \"<task>\"")
		}
		captured, err := dispatchPromptCapture(maxPromptBytes)
		if err != nil {
			switch {
			case errors.Is(err, promptcapture.ErrCanceled):
				return fmt.Errorf("niwa: canceled")
			case errors.Is(err, promptcapture.ErrEndOfInput):
				return fmt.Errorf("niwa: error: dispatch prompt must not be empty")
			}
			return fmt.Errorf("niwa: error: reading prompt: %w", err)
		}
		if err := validateDispatchPrompt(captured); err != nil {
			return err
		}
		prompt = captured
	}

	// (4) Generate a unique "-<8 hex>" name suffix via crypto/rand and pass it
	// as the customName branch of the existing provision path, sidestepping the
	// racy numbered scan (DESIGN Decision 2). When --name sanitizes to a usable
	// slug it is prepended, so the suffix is "<slug>-<8hex>" and the name becomes
	// "<config>+<slug>-<8hex>"; with no slug the suffix is "-<8hex>" and the name
	// is "<config>+-<8hex>". The random hex is always kept, and the mandatory
	// "-<8hex>" is the structural signature isDispatchInstanceName (and thus the
	// reaper backstop) keys on -- there is no "disp" literal.
	slug := sanitizeInstanceSlug(dispatchName)
	namePrefix, err := dispatchNameSuffix(slug)
	if err != nil {
		return fmt.Errorf("niwa: error: generating instance name: %w", err)
	}
	// "+" is the end-of-config marker for dispatch instances, present for every
	// dispatch whether or not a slug is supplied: no-name dispatch is
	// "<config>+-<8hex>", named is "<config>+<slug>-<8hex>". It marks the config
	// boundary unambiguously (config names may contain '.', '-', and '_', so none
	// of those can serve as the separator).
	const sep = "+"

	// (5) Self-bound orphans: run the opportunistic reclamation sweep the same
	// way runCreate does, before creating the new instance (R12).
	reapOpportunistically(workspaceRoot)

	// (6) Create the instance through the existing provision path. --parallel
	// rides into realProvisionInstance via provisionCloneWorkers (the provision
	// signature is fixed and shared with the hook/reap callers).
	provisionCloneWorkers = dispatchParallel
	res, err := provisionInstanceFunc(cmd.Context(), workspaceRoot, cwd, namePrefix, sep)
	if err != nil {
		return fmt.Errorf("niwa: error: provisioning dispatch instance: %w", err)
	}
	instancePath := res.Path

	// (7) Arm the deferred self-rollback IMMEDIATELY after create, before any
	// other work. ANY early return after create -- and before success is set --
	// destroys the just-created instance promptly (DESIGN Decision 4). A Go
	// defer does not run on SIGKILL; the name+TTL reaper backstop closes that
	// remaining gap (the dispatch instance NAME, created atomically by provision,
	// is the backstop's eligibility signal, so no marker is required).
	success := false
	defer func() {
		if !success {
			_ = destroyInstanceFunc(instancePath)
		}
	}()

	// (8) Drop the pending-marker carrying its own creation timestamp. This is
	// the FIRST action after arming rollback. The marker is a precision aid for
	// the backstop's age check, not its eligibility signal; the instance name
	// already makes a SIGKILL-orphaned instance reclaimable even if this write
	// never lands. A write failure rolls back via the deferred destroy above.
	if err := writeDispatchMarker(instancePath); err != nil {
		return fmt.Errorf("niwa: error: writing dispatch pending-marker: %w", err)
	}

	// (9) Load the host global config ONCE, best-effort. A missing or unreadable
	// config degrades to "no dispatch_model default" and "no remote-control
	// injection" -- neither can fail the dispatch (both features are opt-in
	// defaults, so absence just means today's behavior).
	gc, gcErr := config.LoadGlobalConfig()

	// (9a) Resolve the effective main-loop model. The --model flag wins; when it
	// is unset the host [global] dispatch_model default fills in; when neither is
	// set nothing is forwarded. The chosen value (a category or a versionless
	// vendor name) is resolved to the concrete name `claude --model` receives, and
	// an unrecognized value is forwarded as-is with a warning rather than blocking
	// the launch (see resolveDispatchModel).
	effectiveModel := dispatchModel
	if effectiveModel == "" && gcErr == nil && gc != nil {
		effectiveModel = strings.TrimSpace(gc.Global.DispatchModel)
	}
	// F2 lands the resolver as agent-aware groundwork; the dispatch launcher
	// stays Claude, so resolving under Claude preserves today's behavior exactly.
	resolvedModel, modelWarning := resolveDispatchModel(agent.AgentClaude, effectiveModel)
	if modelWarning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", modelWarning)
	}

	// (9b) Build the pass-through argv. Flags become discrete argv elements --
	// never string-concatenated -- so a crafted value cannot inject a claude flag
	// (DESIGN Decision 8).
	passthrough := buildDispatchPassthrough(slug, resolvedModel)

	// (9c) Remote-control-on-dispatch default-fill. When the host preference
	// (~/.config/niwa/config.toml [global].remote_control_on_dispatch) is on and
	// the dispatched instance left remoteControlAtStartup unset, append the
	// Claude Code Remote settings flag so the worker starts steerable. The flag
	// is two discrete argv elements (no shell interpolation). This is the only
	// dispatch-exclusive seam, so the default never leaks to interactive,
	// ephemeral, or `niwa apply` sessions. Neither read can fail the dispatch: a
	// missing/unreadable global config degrades to "no injection" (the preference
	// is treated as unset), and an unreadable instance settings file is treated as
	// "downstream unset" -- so the host default-fill still applies. Either way the
	// dispatch always launches. The global config is loaded once in step (9) and
	// reused here. The instance settings are read once too -- the keep-alive
	// resolution in (9d) consults the same projection.
	inst, _ := readInstanceSettings(instancePath)
	rcInjected := false
	if gcErr == nil {
		// The eligibility check must inspect the SAME environment the worker
		// inherits -- realDispatchLaunch launches with cmd.Env = os.Environ() -- so
		// the warning describes the worker's actual auth context. Keep these two
		// env sources identical if either ever stops using os.Environ().
		inject, warning := resolveDispatchRemoteControl(gc.Global, inst, os.Environ())
		if warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", warning)
		}
		if inject {
			passthrough = append(passthrough, "--settings", remoteControlSettingsJSON)
			rcInjected = true
		}
	}

	// (9d) Keep-alive arming. The opt-in resolves flag > downstream > host
	// default (resolveDispatchKeepAlive); an unreadable host config degrades to
	// "host default unset" through the zero GlobalSettings, so keep-alive --
	// like remote-control -- can never fail the dispatch. When it resolves on
	// AND the worker starts with remote control (either injected above or
	// decided downstream), prepend the fixed self-arm instruction to the task
	// prompt (channel B2; see dispatch_keepalive.go for why the SessionStart
	// channel does not reach a dispatched worker). The instruction rides the
	// same single argv element as the prompt, so the D8 no-shell-interpolation
	// guard is preserved, and its fixed size was already reserved by step (1):
	// maxPromptBytes is the exec ceiling minus dispatchPromptReserve, which is
	// this constant's length, so a prompt that got here can absorb the prepend
	// and still fit in one argv element. This is a reservation, not a margin:
	// grow the instruction and maxPromptBytes shrinks by the same amount.
	// Requesting keep-alive without remote control warns and arms nothing --
	// the dispatch itself always proceeds. Without the opt-in this block
	// changes nothing: the launch stays byte-identical.
	var hostGlobal config.GlobalSettings
	if gcErr == nil && gc != nil {
		hostGlobal = gc.Global
	}
	// keepAliveArmed records that the arming actually happened (resolved on AND
	// remote control on); it is what the durable mapping carries in step (11),
	// so `niwa list` reports sessions genuinely kept alive, not mere requests.
	keepAliveArmed := false
	if resolveDispatchKeepAlive(dispatchKeepAlive, hostGlobal, inst) {
		if remoteControlEnabled(rcInjected, inst) {
			prompt = keepAliveArmingInstruction + prompt
			keepAliveArmed = true
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", keepAliveNonRCWarning)
		}
	}

	if err := dispatchLaunch(cmd.Context(), instancePath, prompt, passthrough, nil); err != nil {
		return fmt.Errorf("niwa: error: launching dispatch worker: %w", err)
	}

	// (10) Capture the worker's full session UUID AND its short id by jobs-dir
	// cwd correlation. The full UUID keys the durable mapping; the short id is
	// the handle `claude attach/logs/stop` accept (those commands reject the
	// full UUID with "No job matching ...", so every user-facing claude
	// invocation below uses shortID, not sessionID).
	// On failure the deferred rollback destroys the instance DIRECTORY, but the
	// background worker launched in step (9) may still be running: capture failed,
	// so we never obtained its session id and cannot 'claude stop' it. The
	// orphaned process has no mapping and is harmless, but it is not auto-killed
	// -- the user must stop it manually. The backstop reclaims the directory, not
	// the process.
	sessionID, shortID, err := dispatchCapture(defaultJobsDir(), instancePath, dispatchCaptureTimeout, nil, 0)
	if err != nil {
		return fmt.Errorf("niwa: error: capturing dispatch session id: %w", err)
	}

	// (11) Write the durable ephemeral, dispatch-origin mapping keyed on the
	// full UUID.
	mapping := workspace.SessionMapping{
		SessionID:    sessionID,
		InstanceName: res.Name,
		InstancePath: instancePath,
		Ephemeral:    true,
		Origin:       "dispatch",
		Label:        dispatchLabel,
		KeepAlive:    keepAliveArmed,
		Created:      time.Now().UTC(),
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, mapping); err != nil {
		return fmt.Errorf("niwa: error: writing dispatch session mapping: %w", err)
	}

	// (12) The mapping is durable. Remove the pending-marker and disarm
	// rollback.
	removeDispatchMarker(instancePath)
	success = true

	// (13) Print the session id and management hints. The headline prints the
	// full UUID (it is the durable mapping key the user can correlate), but the
	// claude management hints use the SHORT id because `claude attach/logs/stop`
	// are keyed on it -- the full UUID yields "No job matching ...".
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dispatched session %s\n", sessionID)
	fmt.Fprintf(out, "  instance: %s\n", instancePath)
	fmt.Fprintf(out, "  claude attach %s\n", shortID)
	fmt.Fprintf(out, "  claude logs %s\n", shortID)
	fmt.Fprintf(out, "  claude stop %s\n", shortID)

	// (14) Unless --detach, attach the terminal to the new session as the FINAL
	// step. attach is keyed on the SHORT id, not the full UUID. An attach failure
	// is NON-fatal: the session and instance survive, so degrade to a warning and
	// never roll back or delete the mapping (success is already true; DESIGN
	// Decision 1).
	if !dispatchDetach {
		if err := dispatchAttach(shortID); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: warning: could not attach to session %s: %v\n", sessionID, err)
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: the session is running; attach later with: claude attach %s\n", shortID)
		}
	}

	return nil
}

// dispatchNameSuffix returns a unique name suffix ending in a mandatory "-" plus
// 8 lowercase hex digits, using crypto/rand for collision safety under
// concurrency without a lock (DESIGN Decision 2). The provision path joins this
// to the config name with "+" (the end-of-config marker for dispatch instances).
//
// With no slug the suffix is "-<8hex>", so the instance dir is "<config>+-<8hex>"
// (the "+" then "-" sit adjacent). With a slug the suffix is "<slug>-<8hex>", so
// the dir is "<config>+<slug>-<8hex>". The "+" is added by the join, NOT here.
// There is no longer a "disp" literal: the dispatch signature is now purely
// structural -- a "+", an optional dash-free slug, a "-", then exactly 8 hex --
// which isDispatchInstanceName recognizes via the regex
// "\+[a-z0-9_]*-[0-9a-f]{8}$". The mandatory "-<8hex>" is what distinguishes a
// dispatch instance from a `create --name` instance ("<config>+<slug>", no
// trailing "-<8hex>"); it relies on slugs being dash-free (sanitizeInstanceSlug)
// so the only "-" after the "+" is the one this suffix adds.
func dispatchNameSuffix(slug string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	suffix := "-" + hex.EncodeToString(b[:])
	if slug != "" {
		return slug + suffix, nil
	}
	return suffix, nil
}

// sanitizeInstanceSlug normalizes a raw --name value into a filesystem- and
// flag-safe slug: lowercase, every run of characters outside [a-z0-9] collapsed
// to a single underscore, leading/trailing underscores trimmed, and the result
// capped to maxDispatchSlugRunes (re-trimming a trailing underscore the cap may
// expose). It returns "" when nothing usable remains, signaling the caller to
// fall back to the slug-less behavior. The result is guaranteed to contain only
// [a-z0-9_] and to neither lead nor trail with an underscore.
//
// The word separator is an UNDERSCORE, never a dash: even a user-typed dash
// (e.g. "auth-layer") collapses to "_" ("auth_layer"). This dash-free invariant
// is load-bearing: the dispatch instance name is "<config>+<slug>-<8hex>", and
// isDispatchInstanceName keys on the "-" immediately before the 8 hex digits
// being the SOLE dash after the "+". If a slug could contain a dash, that
// structural signature would be ambiguous (and a `create --name` instance could
// masquerade as a dispatch one). TestSanitizeInstanceSlug pins this invariant.
//
// It is shared by `niwa dispatch` (which embeds the slug in the ephemeral
// instance name) and `niwa create` (which uses it as the --name suffix), so both
// commands normalize a custom name identically.
func sanitizeInstanceSlug(raw string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSep = false
			continue
		}
		if !prevSep {
			b.WriteByte('_')
			prevSep = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	if r := []rune(slug); len(r) > maxDispatchSlugRunes {
		slug = strings.TrimRight(string(r[:maxDispatchSlugRunes]), "_")
	}
	return slug
}

// dispatchInstanceNameRe matches a dispatch instance's base directory name by
// its purely STRUCTURAL signature -- there is no "disp" literal. The shape is:
// a "+" (the end-of-config marker), then an optional dash-free slug
// ("[a-z0-9_]*"), then a mandatory "-", then exactly 8 lowercase hex digits at
// the end. So it matches both "<config>+-<8hex>" (no-name dispatch; the slug is
// empty, the "+" and "-" sit adjacent) and "<config>+<slug>-<8hex>" (named
// dispatch). A developer instance ("<config>", "<config>-2"), a hook-created
// instance ("<config>-<sessionhex>", no "+"), and a create instance
// ("<config>+<slug>", no trailing "-<8hex>" -- including a hex-shaped slug like
// "<config>+deadbeef", which has no "-" before the hex) never match.
var dispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)

// isDispatchInstanceName reports whether name is a dispatch-created instance's
// base directory name. The dispatch backstop uses this as its eligibility
// signal: because provisionInstanceFunc creates the directory (and thus this
// name) atomically, a dispatch instance is recognizable the instant it exists,
// closing the SIGKILL-before-marker orphan window that a marker-file-only gate
// left open.
//
// This predicate relies on two invariants, both pinned by tests below:
//
//	(a) slugs are dash-free (sanitizeInstanceSlug collapses every dash to "_"),
//	    so the "-" immediately before the 8 hex is the ONLY dash after the "+";
//	(b) `create --name` appends no trailing "-<8hex>" (its instance is just
//	    "<config>+<slug>"), so a named-create can never present this structure.
//
// If either invariant changes, this predicate must change too.
func isDispatchInstanceName(name string) bool {
	return dispatchInstanceNameRe.MatchString(name)
}

// buildDispatchPassthrough turns the set pass-through flags into discrete argv
// elements (flag, value pairs). Each value stays its own element so a crafted
// value cannot smuggle in an extra claude flag (DESIGN Decision 8).
//
// A non-empty slug (the sanitized --name) is forwarded to the worker as
// "--name <slug>" so the launched claude session carries the same display name
// embedded in the instance directory. An empty slug forwards nothing, preserving
// the original slug-less behavior.
//
// model is the already-resolved main-loop model (see resolveDispatchModel): a
// concrete versionless name, forwarded as "--model <model>", or "" to forward
// nothing. Resolution happens in the caller so this stays a pure argv builder.
func buildDispatchPassthrough(slug, model string) []string {
	var pass []string
	if model != "" {
		pass = append(pass, "--model", model)
	}
	if dispatchPermissionMode != "" {
		pass = append(pass, "--permission-mode", dispatchPermissionMode)
	}
	if dispatchAgent != "" {
		pass = append(pass, "--agent", dispatchAgent)
	}
	if slug != "" {
		pass = append(pass, "--name", slug)
	}
	return pass
}

// writeDispatchMarker writes the pending-marker file containing an RFC3339
// creation timestamp inside the instance. The parent .niwa directory already
// exists in a provisioned instance, but MkdirAll keeps this robust against a
// fake provisioner that only creates the instance dir.
func writeDispatchMarker(instancePath string) error {
	marker := filepath.Join(instancePath, dispatchPendingMarker)
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	return os.WriteFile(marker, []byte(ts+"\n"), 0o600)
}

// removeDispatchMarker removes the pending-marker once the mapping is durable.
// A removal failure is non-fatal: the marker only matters to the reaper
// backstop, which also requires the mapping to be ABSENT, so a stale marker
// beside a written mapping is never acted on.
func removeDispatchMarker(instancePath string) {
	_ = os.Remove(filepath.Join(instancePath, dispatchPendingMarker))
}

// validateDispatchPrompt applies the same rules to a captured prompt as to a
// positional one, so the two paths cannot drift into quoting different limits
// or offering different advice.
//
// The whitespace check exists because the capture can produce a buffer that is
// non-empty but carries nothing: the positional path's own empty check compares
// against the empty string and would let it through.
func validateDispatchPrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("niwa: error: dispatch prompt must not be empty")
	}
	// The limit already excludes dispatchPromptReserve, so a prompt that passes
	// here still fits after step (9d) may prepend the keep-alive instruction --
	// there is one check, it is early, and it covers the final string.
	if len(prompt) > maxPromptBytes {
		return fmt.Errorf("niwa: error: dispatch prompt is too long (%d bytes, limit %d); it is passed to claude as a single exec argument, so it cannot be truncated or split. Write the detail to a file and reference its path from a shorter prompt", len(prompt), maxPromptBytes)
	}
	return nil
}
