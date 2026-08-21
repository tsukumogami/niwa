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
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/promptcapture"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	dispatchCmd.Flags().StringVar(&dispatchLabel, "label", "", "optional human-friendly alias recorded on the session mapping")
	dispatchCmd.Flags().StringVarP(&dispatchName, "name", "n", "", "optional display name for the session (sanitized into a slug; also names the niwa instance: <config>+-<id> with no name, <config>+<slug>-<id> with one -- '+' always marks the end of the config name)")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", dispatchModelFlagHelp())
	dispatchCmd.Flags().StringVar(&dispatchPermissionMode, "permission-mode", "", "permission mode to forward to the background worker; dropped for an agent that has no such flag")
	dispatchCmd.Flags().StringVar(&dispatchAgent, "agent", "", "subagent type to forward to the background worker; this selects a role within the launched agent, not which agent is launched (that is --harness). Dropped for an agent that has no such flag")
	dispatchCmd.Flags().StringVar(&dispatchHarness, harnessFlagName, "", harnessFlagUsage())
	dispatchCmd.Flags().BoolVarP(&dispatchDetach, "detach", "d", false, "put the worker out of this terminal's reach: for an agent whose runner executes the turn in the foreground, run it detached instead of here; for one that backgrounds its own session, skip the attach. Either way, print hints and return")
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
	dispatchHarness        string
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

	// dispatchRetainMarker is dropped in an instance whose worker finished and
	// produced work, but whose session could not be identified -- so there is
	// no mapping, and the reaper's backstop would otherwise judge the directory
	// an abandoned dispatch and delete it once it aged past the TTL.
	//
	// It exists because the in-process rollback is not the only thing that
	// deletes an instance. Disarming that rollback keeps the work for the
	// length of the command; the backstop runs out-of-process at the top of the
	// next create, dispatch or watch, and its eligibility signal is the
	// directory NAME. Removing the pending marker does not help -- the age
	// check falls back to the directory mtime -- so keeping the work needs a
	// signal the sweep honors rather than the absence of one.
	//
	// The contents are the reason, in the words a developer needs to decide
	// what to do with the directory. The sweep spares any instance carrying
	// this file and says so, which is the same treatment an instance whose
	// liveness cannot be read already gets.
	dispatchRetainMarker = ".niwa/dispatch-retain"

	// dispatchCaptureTimeout bounds the jobs-dir cwd-correlation poll that
	// recovers the worker's session UUID. Exhaustion is a capture failure that
	// triggers self-rollback, never a hang (DESIGN Decision 3, R20/R22).
	dispatchCaptureTimeout = 30 * time.Second

	// dispatchForegroundCaptureTimeout bounds the same poll when the launch ran
	// the turn in this terminal and has already returned. The record was
	// written while the turn ran or it was never written at all, so this is a
	// grace period for a slow filesystem rather than a wait for work in
	// progress.
	dispatchForegroundCaptureTimeout = 2 * time.Second

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

	// There is deliberately no reserve here any more. One existed because a
	// REFUSAL has to happen before provisioning while the keep-alive prepend is
	// decided after, so the only sound answer was to reserve the worst case up
	// front and charge it to the developer. Making the decision a route rather
	// than a refusal dissolves that: nothing is denied, so the decision need not
	// be early, and once it can be late it is made against the final argv string
	// in the launcher, where no estimate is needed.
)

// lookAgentBinary reports the path to a worker binary or an error if it is not
// on PATH. The name comes from the launched agent's own declaration, so this
// function knows nothing about which agent it is preflighting. It is a package
// variable so the preflight check is unit-testable without a real install
// (DESIGN Decision 9).
var lookAgentBinary = func(name string) (string, error) {
	return exec.LookPath(name)
}

// dispatchLaunchSpec resolves an agent's launch description. It is a package
// variable so a test can substitute a description for an agent niwa does not
// ship, which is the only way to tell "the dispatch reads the resolved agent's
// spec" apart from "the dispatch reads a spec". Asserting against the one real
// agent's spec proves the second and not the first, and the difference between
// them is the whole feature.
var dispatchLaunchSpec = func(ag agent.Agent) (agentplan.LaunchSpec, bool) {
	return agentplan.For(ag).LaunchSpec()
}

// dispatchCapture is the capture seam. Production wires it to captureSessionID;
// tests substitute a fake to return a fabricated session id without a real
// record store.
var dispatchCapture = captureSessionID

// dispatchAttach steps the terminal into a dispatched session by running the
// launched agent's own resume verb against the handle capture recovered, with
// inherited stdio. Which verb that is and what the handle means are both the
// agent's declaration; everything else here -- looking the binary up, wiring
// stdio, propagating the outcome -- is the same for whoever is launched, and is
// written once.
//
// It is a package variable so tests can assert it is or isn't called and force
// a (non-fatal) failure without a real binary. It runs ONLY as the final step,
// after the mapping is durable, so its failure never rolls back (DESIGN
// Decision 1).
var dispatchAttach = func(spec agentplan.LaunchSpec, handle, workdir string) error {
	bin, err := lookAgentBinary(spec.Binary)
	if err != nil {
		return fmt.Errorf("%s binary not found in PATH: %w", spec.Binary, err)
	}
	cmd := exec.Command(bin, append(slices.Clone(spec.ResumeArgs), handle)...)
	// The resumed session runs where it ran. An agent that narrows its own
	// session list by working directory would otherwise refuse to find a
	// session started somewhere else, and a session that reopened in the
	// terminal's directory rather than its own would be a different session in
	// every way that matters to the work in it.
	cmd.Dir = workdir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

var dispatchCmd = &cobra.Command{
	Use:   "dispatch [prompt]",
	Short: "Launch a background worker in a fresh ephemeral instance",
	Long: `dispatch creates a fresh ephemeral niwa instance, launches a background
worker rooted inside it, captures the worker's session id, and records an
ephemeral dispatch-origin mapping so the instance is reclaimed when the session
ends.

Which agent the worker runs as is resolved once, in this order: --harness, then
NIWA_DISPATCH_HARNESS, then the workspace's [workspace].default_agent, then your
own [global].default_dispatch_harness (niwa config set
default-dispatch-harness). Every instance is prepared for every agent niwa
supports whatever those say, so this picks a launch target and takes nothing
away from the other agent. Not to be confused
with --agent, which forwards a subagent type INTO whichever agent is launched.

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

--detach/-d chooses how the worker runs, not only whether a step follows it.
Without it, an agent whose runner executes the turn in the foreground runs it
here: the output is this terminal's and dispatch does not return until the turn
ends. An agent that backgrounds its own session instead has the terminal attach
to it (like docker run) if it can hand a session over mid-turn, and says so and
returns if it cannot -- the session is yours to resume once the turn ends.

With --detach the worker is put out of this terminal's reach: a foreground
runner is detached, with its output kept in the instance, and an agent that
backgrounds its own session simply skips the attach. Either way dispatch
returns after printing the launched agent's own management hints, which is the
mode for fan-out and scripting.

Any failure before the mapping is durable destroys the just-created instance,
so dispatch never leaves an unreclaimable instance DIRECTORY. One caveat, for a
worker that is still running when that happens: if the launch succeeds but
session-id capture then fails, the rollback deletes the instance directory, but
the background process keeps running -- we never captured its session id, so we
cannot stop it. That process has no mapping and stopping it is yours to do, with
the launched agent's own stop verb, once you find it in that agent's own session
list. It is mostly harmless, with one caveat: a prompt too large to pass as a
command argument is written into the instance, so a worker in that window may
find its task file already deleted and proceed on the excerpt it was given.`,
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

	// (2a) Load the host global config ONCE, best-effort, and reuse it below.
	// It carries two things this command reads: the machine-wide dispatch
	// harness (broadest rung of the agent resolution just below) and the
	// dispatch_model and remote-control defaults consumed after provisioning. A missing or
	// unreadable config degrades to "none of those set", which is today's
	// behavior for every one of them -- they are all opt-in defaults.
	gc, gcErr := config.LoadGlobalConfig()

	// (2b) Resolve which agent this dispatch launches, from --harness,
	// NIWA_DISPATCH_HARNESS, the workspace default_agent, and the host
	// default_dispatch_harness, in that order. dispatch's own --agent flag is
	// the launched agent's subagent-type passthrough, a different thing, so it
	// is deliberately not consulted here. A config that cannot be loaded resolves against the
	// sources that did load and leaves the failure to the provisioning path to
	// report -- but it says so first, because a rung that was skipped rather
	// than consulted is the one case where the resolution's answer is right
	// about the inputs it had and wrong about the ones the developer wrote.
	wsConfigPath := filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
	var wsConfig *config.WorkspaceConfig
	wsCfg, cfgErr := config.Load(wsConfigPath)
	if cfgErr == nil {
		wsConfig = wsCfg.Config
	} else if notice := unreadableAgentRungNotice(wsConfigPath, cfgErr); notice != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", notice)
	}
	var hostCfg *config.GlobalConfig
	if gcErr == nil {
		hostCfg = gc
	}
	dispatchedAgent, agErr := resolveSessionAgent(dispatchHarness, wsConfig, wsConfigPath, hostCfg)
	if agErr != nil {
		return fmt.Errorf("niwa: error: %w", agErr)
	}

	// (2c) --agent names a subagent type, not an agent to launch, and the two
	// readings are one keystroke apart. A developer who means "launch codex"
	// and types --agent codex gets a Claude worker started with a subagent type
	// no installation defines, which fails inside the worker rather than here.
	// So say it, once, before anything is provisioned or launched.
	//
	// A warning rather than a refusal: an agent name is a legitimate
	// subagent-type name -- a role called "claude" is a real thing to have --
	// and refusing would break setups that already work. The trigger is
	// deliberately narrow: the value has to parse as an agent niwa knows AND
	// disagree with the one actually being launched, so --agent reviewer says
	// nothing and --agent claude on a Claude dispatch says nothing either.
	if warning := harnessMismatchWarning(dispatchAgent, dispatchedAgent); warning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", warning)
	}

	// (2d) The gate is the declaration, not a comparison against an agent this
	// code names. Launching a background worker is a declared capability, so
	// whether niwa can launch one for this agent is a lookup, and the refusal
	// quotes the same reason the generated gap list publishes -- which is what
	// keeps the binary, the table, and the guide from telling a developer three
	// different things.
	launchDecl, err := agentplan.Lookup(agentplan.DispatchLaunch, dispatchedAgent)
	if err != nil {
		return fmt.Errorf("niwa: error: %w", err)
	}
	spec, hasSpec := dispatchLaunchSpec(dispatchedAgent)
	if launchDecl.State != agentplan.StateImplemented || !hasSpec {
		return fmt.Errorf("niwa: error: niwa dispatch cannot launch a background worker for the %q agent. %s %s, or open a session yourself and run the task there",
			dispatchedAgent, launchDecl.Reason, launchableAgentsHint())
	}

	// (3) Preflight the worker binary on PATH BEFORE creating any instance, so
	// an absent binary fails with no instance dir and no mapping on disk
	// (PRD-instance-dispatch R16, R13 -- both numbers mean something unrelated
	// in PRD-dispatch-paste-prompt, so the document is named). This ordering is
	// load-bearing rather than incidental, and it holds for whichever binary the
	// declaration named above.
	if _, err := lookAgentBinary(spec.Binary); err != nil {
		return fmt.Errorf("niwa: error: %s binary not found in PATH; install it before dispatching", spec.Binary)
	}

	// (3b) A worker is launched at the instance root, and for an agent that
	// cannot be oriented there it starts without any of what the workspace
	// delivers -- which it will not say, because it has no way to know
	// something was withheld. Documented in the guide, invisible at the moment
	// it happens, and the difference between those two is a developer reading a
	// plausible answer from an uninformed worker and never learning why.
	//
	// It prints here, after every preflight and BEFORE the prompt is captured,
	// because what it changes is the prompt. A worker with no orientation needs
	// a briefing the prompt has to carry, and a developer who reads this after
	// the launch has already written one that assumes otherwise -- and stopping
	// the result means finding the process by hand. Read first, then type, or
	// press Ctrl-C having spent nothing.
	//
	// The trigger is row 2's declaration rather than a name, so it fires for
	// exactly the agents it is true of. The sentence is niwa's own rather than
	// the declaration's reason, because what a root-launched worker loses is
	// wider than orientation and the reason speaks only to that.
	if d, err := agentplan.Lookup(agentplan.RootSessionOrientation, dispatchedAgent); err == nil && d.State != agentplan.StateImplemented {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"niwa dispatch: the worker starts at the instance root, where a %s session receives none of the workspace's orientation, skills, MCP servers or posture -- those reach a session from inside a repository. Its prompt is its whole briefing.\n",
			dispatchedAgent)
	}

	// (3c) With no positional prompt, capture one from the terminal. This sits
	// after every preflight check and before anything is created, so abandoning
	// the capture costs nothing -- there is no instance to roll back -- and a
	// wrong workspace or a missing claude fails before the developer types.
	if interactive {
		if !dispatchInteractive() {
			return fmt.Errorf("niwa: error: no prompt given and this is not an interactive terminal; pass the prompt as an argument: niwa dispatch \"<task>\"")
		}
		captured, err := dispatchPromptCapture()
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

	// (6) Create the instance through the existing provision path, passing
	// --parallel straight through as this call's clone concurrency.
	res, err := provisionInstanceFunc(cmd.Context(), workspaceRoot, cwd, namePrefix, sep, dispatchParallel)
	// Dispatch has a terminal, unlike the hook that shares this provisioner, so
	// the report goes to stderr here rather than into the worker's context.
	// Rendered before the error is returned, not after the success path begins:
	// a strict refusal fails here carrying the keys that caused it, and those
	// keys are the whole explanation of what just happened.
	fmt.Fprint(cmd.ErrOrStderr(), keyreport.RenderText(res.Keys))
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

	// (9) The host global config was loaded once at (2a), before the agent
	// resolution that also reads it. gc/gcErr below are that same result.

	// (9a) Resolve the effective main-loop model. The --model flag wins; when it
	// is unset the host [global] dispatch_model default fills in; when neither is
	// set nothing is forwarded. The chosen value (a category or a versionless
	// vendor name) is resolved against the launched agent's own vocabulary to the
	// concrete name its --model receives, and an unrecognized value is forwarded
	// as-is with a warning rather than blocking the launch (see
	// resolveDispatchModel).
	effectiveModel := dispatchModel
	if effectiveModel == "" && gcErr == nil && gc != nil {
		effectiveModel = strings.TrimSpace(gc.Global.DispatchModel)
	}
	resolvedModel, modelWarning := resolveDispatchModel(spec, effectiveModel)
	if modelWarning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", modelWarning)
	}

	// (9b) Build the pass-through argv. Flags become discrete argv elements --
	// never string-concatenated -- so a crafted value cannot inject a flag
	// (DESIGN Decision 8). The spelling of each flag is the launched agent's,
	// and an intent that agent has no flag for is dropped rather than guessed
	// at.
	passthrough := buildDispatchPassthrough(spec.Flags, slug, resolvedModel)

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
	//
	// Remote control is its own capability row, and it reaches a session as a
	// settings document the agent reads. An agent that has no such flag has
	// nowhere for the document to go, so the injection is gated on the
	// declaration rather than attempted and dropped.
	inst, _ := readInstanceSettings(instancePath)
	rcInjected := false
	rcDecl, rcErr := agentplan.Lookup(agentplan.RemoteControl, dispatchedAgent)
	rcDeliverable := rcErr == nil && rcDecl.State == agentplan.StateImplemented && spec.Flags.Settings != ""
	if gcErr == nil && rcDeliverable {
		// The eligibility check must inspect the SAME environment the worker
		// inherits -- realDispatchLaunch launches with cmd.Env = os.Environ() -- so
		// the warning describes the worker's actual auth context. Keep these two
		// env sources identical if either ever stops using os.Environ().
		inject, warning := resolveDispatchRemoteControl(gc.Global, inst, os.Environ())
		if warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", warning)
		}
		if inject {
			passthrough = append(passthrough, spec.Flags.Settings, remoteControlSettingsJSON)
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
	// promptPrefix is niwa-authored text that always rides the argv element.
	// Keeping it separate from the developer's prompt is what lets the launcher
	// spill only the latter: once the two are concatenated the distinction is
	// gone, and a spill would write niwa's arming instruction into the file
	// where the developer's text belongs -- producing a session recorded and
	// reported as kept alive that was never actually armed.
	//
	// Keep-alive is its own capability row too, and it is the row that says
	// whether keeping a dispatched session warm is a thing that exists for this
	// agent at all. Consulting the declaration is what stops the arming from
	// being attempted for an agent with no bridge to keep warm.
	kaDecl, kaErr := agentplan.Lookup(agentplan.DispatchKeepAlive, dispatchedAgent)
	keepAliveDeliverable := kaErr == nil && kaDecl.State == agentplan.StateImplemented

	promptPrefix := ""
	keepAliveArmed := false
	switch {
	case !resolveDispatchKeepAlive(dispatchKeepAlive, hostGlobal, inst):
		// Not asked for. Nothing to say and nothing to do.
	case !keepAliveDeliverable:
		// Asked for and undeliverable. Saying so is the point: a flag that is
		// silently ignored for one agent is a developer believing they armed
		// something they did not, and the declaration already carries the
		// sentence that explains why.
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: --keep-alive does not apply to the %q agent and was ignored. %s\n",
			dispatchedAgent, kaDecl.Reason)
	case !remoteControlEnabled(rcInjected, inst):
		fmt.Fprintf(cmd.ErrOrStderr(), "niwa dispatch: %s\n", keepAliveNonRCWarning)
	default:
		promptPrefix = keepAliveArmingInstruction
		keepAliveArmed = true
	}

	// (9b) The process model, resolved from both of its inputs: what this
	// agent's runner is, and whether this invocation asked to detach. Reading
	// the declaration alone could not see the flag; see RunnerKind.ModeFor.
	//
	// Without --detach, a foreground runner's turn runs here, in this terminal,
	// and this call does not return until it ends. The developer watches the
	// work; for that runner, that is what attaching would have been.
	launchMode := spec.Runner.ModeFor(dispatchDetach)

	if err := dispatchLaunch(cmd.Context(), launchRequest{
		Spec:        spec,
		Mode:        launchMode,
		InstanceDir: instancePath,
		Prefix:      promptPrefix,
		Body:        prompt,
		Passthrough: passthrough,
		Stdout:      cmd.OutOrStdout(),
		Stderr:      cmd.ErrOrStderr(),
	}); err != nil {
		return fmt.Errorf("niwa: error: launching dispatch worker: %w", err)
	}

	// (10) Capture the worker's session id AND the handle a developer reaches it
	// by, correlating the launched agent's own session records against this
	// instance directory. The id keys the durable mapping; the handle is what
	// the agent's management verbs accept, and for one agent those two are
	// different strings, which is why capture returns both rather than deriving
	// one from the other.
	//
	// On failure the deferred rollback destroys the instance DIRECTORY, but the
	// background worker launched above may still be running: capture failed, so
	// we never obtained its session id and cannot stop it. The orphaned process
	// has no mapping and is harmless, but it is not auto-killed -- the user must
	// stop it manually. The backstop reclaims the directory, not the process.
	//
	// The rollback also destroys the worker's own log, which is the only place
	// the real cause is written down. A worker that died at startup -- a flag it
	// would not parse, a configuration it would not load -- says so there, writes
	// no session record, and is reported here as a capture timeout, which
	// describes the symptom and not the cause. So the log's tail is read out
	// BEFORE returning, while the directory still exists.
	//
	// The foreground path disarms the rollback BEFORE capture instead, and the
	// difference is which of the two is the deliverable. Detached, the worker
	// started moments ago and the instance holds nothing but its own logs, so
	// destroying it costs a diagnostic and no work. Foreground, the launch
	// already waited for the turn to end and everything the worker produced is
	// inside this directory: a run that did real work and then failed to yield
	// a discoverable session record -- a refused prompt, a crash after writing
	// files, a record the scanner cannot match -- would have its output deleted
	// by a rollback armed for the case where there was none.
	if launchMode == agentplan.LaunchForeground {
		success = true
	}

	recordsRoot := recordStoreRoot(spec.Records, userHomeDir(), os.Getenv)
	// The poll's deadline depends on what it is waiting for. Detached, the
	// worker started moments ago and the record is about to appear, so the full
	// timeout is the point. Foreground, the launch already waited for the turn
	// to end -- the record either exists on the first pass or never will, and
	// waiting the full thirty seconds after the developer watched the turn
	// finish buys nothing while rescanning the whole record store roughly
	// twelve hundred times.
	captureTimeout := dispatchCaptureTimeout
	if launchMode == agentplan.LaunchForeground {
		captureTimeout = dispatchForegroundCaptureTimeout
	}
	sessionID, handle, err := dispatchCapture(spec.Records, recordsRoot, instancePath, captureTimeout, nil, 0)
	if err != nil {
		if launchMode == agentplan.LaunchForeground {
			// The turn ran here, so the developer has already seen whatever the
			// worker said; what they need is the directory and why there is no
			// session to resume.
			//
			// Keeping it means more than not deleting it now. Without a
			// mapping this directory is exactly what the reaper's backstop
			// exists to reclaim, so the retain marker is what makes "kept" true
			// past the next sweep rather than for thirty minutes.
			reason := fmt.Sprintf("a %s turn finished here but no session record was found, so niwa could not identify the session", spec.Binary)
			if mErr := writeDispatchRetainMarker(instancePath, reason); mErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"niwa: warning: could not mark %s to be kept, so a later sweep may reclaim it: %v\n", instancePath, mErr)
			}
			removeDispatchMarker(instancePath)
			fmt.Fprintf(cmd.ErrOrStderr(),
				"niwa: warning: the turn ended but no session record was found, so there is nothing to resume: %v\n", err)
			fmt.Fprintf(cmd.ErrOrStderr(),
				"niwa: the work is kept at %s -- remove it with `niwa destroy` when you are done with it\n", instancePath)
			return nil
		}
		if tail := readWorkerLogTail(instancePath, spec.Binary); tail != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: the %s worker's own output, which the rollback is about to delete:\n%s\n", spec.Binary, tail)
		}
		return fmt.Errorf("niwa: error: capturing dispatch session id: %w", err)
	}

	// (11) Write the durable ephemeral, dispatch-origin mapping keyed on the
	// session id. The mapping records which agent launched the session, because
	// every later question about it -- is it still there, how do I get back into
	// it -- is answered against that agent's own declaration, and a reader that
	// had to infer the agent from the shape of an id would be guessing.
	mapping := workspace.SessionMapping{
		SessionID:    sessionID,
		InstanceName: res.Name,
		InstancePath: instancePath,
		Agent:        string(dispatchedAgent),
		Handle:       handle,
		Ephemeral:    true,
		Origin:       "dispatch",
		Label:        dispatchLabel,
		KeepAlive:    keepAliveArmed,
		Created:      time.Now().UTC(),
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, mapping); err != nil {
		// The same hazard as the capture-failure branch, on a narrower path.
		// Foreground, the rollback was already disarmed above, so returning
		// here leaves a dispatch-named unmapped instance holding a finished
		// turn's work -- the backstop's exact target, deleted half an hour
		// later. Detached there is nothing to keep and the rollback is still
		// armed, so the deferred destroy does the right thing and this does
		// not run.
		if launchMode == agentplan.LaunchForeground {
			reason := fmt.Sprintf("a %s turn finished here but its session mapping could not be written, so niwa cannot find the session again", spec.Binary)
			if mErr := writeDispatchRetainMarker(instancePath, reason); mErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"niwa: warning: could not mark %s to be kept, so a later sweep may reclaim it: %v\n", instancePath, mErr)
			}
			removeDispatchMarker(instancePath)
			fmt.Fprintf(cmd.ErrOrStderr(),
				"niwa: the work is kept at %s -- remove it with `niwa destroy` when you are done with it\n", instancePath)
		}
		return fmt.Errorf("niwa: error: writing dispatch session mapping: %w", err)
	}

	// (12) The mapping is durable. Remove the pending-marker and disarm
	// rollback.
	removeDispatchMarker(instancePath)
	success = true

	// (13) Print the session id and the launched agent's own management hints.
	// The headline prints the session id, which is the durable mapping key a
	// developer can correlate; the hints print the handle, because that is what
	// the agent's verbs accept. The verbs come from the agent's declaration
	// rather than from a list here, so niwa never offers a command the binary
	// does not have.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dispatched session %s\n", sessionID)
	fmt.Fprintf(out, "  instance: %s\n", instancePath)

	for _, verb := range spec.HintVerbs {
		fmt.Fprintf(out, "  %s %s %s\n", spec.Binary, verb, handle)
	}

	// (14) The last step is about the session the developer ends up in, and
	// which of three things happens is decided by the process model the launch
	// actually used rather than by the flag alone.
	//
	// A foreground turn has already ended by the time this runs -- the launch
	// waited for it -- so there is nothing to attach to and nothing to
	// apologize for. What is left to say is that the turn ended, which is the
	// only thing a finished run of that kind honestly reports.
	if launchMode == agentplan.LaunchForeground {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"niwa: the turn ended. That is what the run reports -- not whether the task succeeded -- so read the work, and resume the session with the command above to carry on.\n")
		return nil
	}

	// Otherwise the worker is still going. An agent that will not hand over a
	// session in that state would answer an attach with an error from its own
	// session store, which reads like a broken dispatch rather than like a
	// thing that resolves itself a minute later. Say what is true instead: the
	// hint above is already the command to run, so this does not repeat it.
	// This holds whether or not --detach was passed, because in both cases the
	// worker is running and the session is not yet openable.
	//
	// The text is built by unopenableSessionNotice rather than written inline
	// so a test can name this exact message. Its words are not distinctive:
	// "still running" also appears in the reaper's sparing report, which
	// reaches the same stderr on the same command because the opportunistic
	// sweep runs at the top of every dispatch. A test grepping for a substring
	// could pass with this message reworded away and fail with it absent but a
	// spared instance present, so it greps for nothing and calls this instead.
	if !spec.ResumeDuringTurn {
		fmt.Fprint(cmd.ErrOrStderr(), unopenableSessionNotice(spec.Binary))
		return nil
	}

	// And for an agent that does hand one over, step the terminal into the new
	// session through its own resume verb, keyed on the handle, unless the
	// developer asked to be left where they are. A failure here is NON-fatal:
	// the session and instance survive, so degrade to a warning and never roll
	// back or delete the mapping (success is already true; DESIGN Decision 1).
	if !dispatchDetach {
		if err := dispatchAttach(spec, handle, instancePath); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: warning: could not attach to session %s: %v\n", sessionID, err)
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: the session is running; attach later with: %s %s %s\n",
				spec.Binary, strings.Join(spec.ResumeArgs, " "), handle)
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
func buildDispatchPassthrough(flags agentplan.LaunchFlags, slug, model string) []string {
	var pass []string
	// Each pair is appended only when niwa has something to say AND the agent
	// has a flag to say it with. An intent an agent has no flag for is dropped
	// rather than forwarded under a near-equivalent spelling: forwarding a flag
	// the binary does not accept fails the launch, and inventing one hands the
	// developer something they did not ask for.
	for _, pair := range []struct{ flag, value string }{
		{flags.Model, model},
		{flags.PermissionMode, dispatchPermissionMode},
		{flags.SubagentType, dispatchAgent},
		{flags.DisplayName, slug},
	} {
		if pair.flag != "" && pair.value != "" {
			pass = append(pass, pair.flag, pair.value)
		}
	}
	return pass
}

// unopenableSessionNotice is what dispatch says when the worker it just
// started belongs to an agent that will not hand over a session whose turn is
// running. It exists as a function so the message has a name: the test that
// holds it absent from the foreground path calls this rather than grepping for
// words, because the words it would grep for are not unique to it -- the
// reaper's sparing report says "still running" too, and lands on the same
// stderr on the same command.
func unopenableSessionNotice(binary string) string {
	return fmt.Sprintf(
		"niwa: %s will not open a session while its turn is still running. The worker is going; resume it with the command above once it finishes.\n",
		binary)
}

// launchableAgentsHint names the agents a refusal can point a developer at, in
// the form they would set. The names come from the declarations rather than
// from a sentence written here, because the person reading a refusal is by
// definition the person who does not already know which agent to name -- and a
// hardcoded one goes stale the moment a row flips, silently, at exactly the
// moment it is being read.
//
// It offers the flag first and the variable second. The developer reading this
// is retrying one command, and --harness changes that command; NIWA_DISPATCH_HARNESS
// changes every niwa command in the shell, and everything dispatched from it,
// which is a bigger thing to hand someone who asked for one dispatch.
//
// With no launchable agent at all there is nothing useful to suggest, so the
// hint says what is true rather than pointing at an empty set.
func launchableAgentsHint() string {
	launchable := agentplan.LaunchableAgents()
	if len(launchable) == 0 {
		return "No agent niwa knows about can be dispatched to"
	}
	if len(launchable) == 1 {
		only := string(launchable[0])
		return "Retry with --" + harnessFlagName + " " + only + ", or set NIWA_DISPATCH_HARNESS=" + only + " for the whole shell"
	}
	names := make([]string, len(launchable))
	for i, ag := range launchable {
		names[i] = string(ag)
	}
	return "Retry with --" + harnessFlagName + " set to one of " + strings.Join(names, ", ") +
		", or set NIWA_DISPATCH_HARNESS to one of them for the whole shell"
}

// userHomeDir returns the developer's home directory, or "" when it cannot be
// resolved. Every caller here treats "" as "no session records", which is the
// same fail-safe the jobs-directory resolution has always taken: without a home
// there is no evidence either way, and a reader with no evidence must not
// answer.
// It is a package variable so a test can point every record-store lookup at a
// fixture home, which is what keeps the reaper's guards from reading -- and
// being slowed by -- whatever the developer running the suite happens to have
// on disk.
var userHomeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
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

// writeDispatchRetainMarker records that this instance holds work niwa could
// not attach a session to, so the reaper's backstop spares it instead of
// judging it an abandoned dispatch. The reason is written into the file because
// the developer meeting this directory later needs to know why it is here, and
// the sweep reads it back to say so rather than reporting a bare path.
func writeDispatchRetainMarker(instancePath, reason string) error {
	path := filepath.Join(instancePath, dispatchRetainMarker)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(reason+"\n"), 0o600)
}

// dispatchRetainReason returns the recorded reason an instance is being kept,
// and whether the marker is present at all. An unreadable marker still counts
// as present: the file existing is the retain signal, and a sweep that deleted
// a directory because it could not read the note explaining why to keep it
// would be the worst possible reading of it.
func dispatchRetainReason(instancePath string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(instancePath, dispatchRetainMarker))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		return "", true
	}
	return strings.TrimSpace(string(data)), true
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
	// No size check. A prompt too large to travel as one argv element is not
	// refused: the launcher writes it to a file inside the instance and hands
	// the worker a pointer instead. Nothing about a prompt's size is the
	// developer's problem any more, so nothing here has an opinion about it.
	return nil
}
