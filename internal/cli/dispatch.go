package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	dispatchCmd.Flags().StringVar(&dispatchLabel, "label", "", "optional human-friendly alias recorded on the session mapping")
	dispatchCmd.Flags().StringVarP(&dispatchName, "name", "n", "", "optional display name for the session (sanitized into a slug; also names the niwa instance: <config>+-<id> with no name, <config>+<slug>-<id> with one -- '+' always marks the end of the config name)")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "model to forward to the background worker (--model)")
	dispatchCmd.Flags().StringVar(&dispatchPermissionMode, "permission-mode", "", "permission mode to forward to the background worker (--permission-mode)")
	dispatchCmd.Flags().StringVar(&dispatchAgent, "agent", "", "agent to forward to the background worker (--agent)")
	dispatchCmd.Flags().BoolVarP(&dispatchDetach, "detach", "d", false, "do not attach the terminal to the new session; print hints and return")
	rootCmd.AddCommand(dispatchCmd)
}

var (
	dispatchLabel          string
	dispatchName           string
	dispatchModel          string
	dispatchPermissionMode string
	dispatchAgent          string
	dispatchDetach         bool
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

	// maxPromptBytes guards against a prompt that would exceed the operating
	// system's argument-length limit. ARG_MAX is at least 4096 on every POSIX
	// platform and is typically far larger; a conservative bound below it
	// leaves room for the binary path, the flags, and the environment, and
	// fails clearly rather than letting exec truncate or reject the call with
	// an opaque error (DESIGN Decision 8, R43).
	maxPromptBytes = 128 * 1024
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
	Use:   "dispatch <prompt>",
	Short: "Launch a background Claude Code worker in a fresh ephemeral instance",
	Long: `dispatch creates a fresh ephemeral niwa instance, launches a Claude Code
background worker rooted inside it, captures the worker's session id, and
records an ephemeral dispatch-origin mapping so the instance is reclaimed when
the session ends.

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
	Args:          cobra.ExactArgs(1),
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runDispatch,
}

func runDispatch(cmd *cobra.Command, args []string) error {
	prompt := args[0]

	// (1) Validate the prompt before touching anything.
	if prompt == "" {
		return fmt.Errorf("niwa: error: dispatch prompt must not be empty")
	}
	if len(prompt) > maxPromptBytes {
		return fmt.Errorf("niwa: error: dispatch prompt is too long (%d bytes, limit %d); shorten it rather than relying on truncation", len(prompt), maxPromptBytes)
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

	// (3) Preflight claude on PATH BEFORE creating any instance, so an absent
	// claude fails with no instance dir and no mapping on disk (R16, R13).
	if _, err := lookClaude(); err != nil {
		return fmt.Errorf("niwa: error: claude binary not found in PATH; install Claude Code before dispatching")
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

	// (6) Create the instance through the existing provision path.
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

	// (9) Launch the background worker rooted in the instance. Flags become
	// discrete argv elements -- never string-concatenated -- so a crafted value
	// cannot inject a claude flag (DESIGN Decision 8).
	passthrough := buildDispatchPassthrough(slug)

	// (9a) Remote-control-on-dispatch default-fill. When the host preference
	// (~/.config/niwa/config.toml [global].remote_control_on_dispatch) is on and
	// the dispatched instance left remoteControlAtStartup unset, append the
	// Claude Code Remote settings flag so the worker starts steerable. The flag
	// is two discrete argv elements (no shell interpolation). This is the only
	// dispatch-exclusive seam, so the default never leaks to interactive,
	// ephemeral, or `niwa apply` sessions. A missing/unreadable global config or
	// instance settings file degrades to "no injection" -- never a dispatch
	// failure -- preserving today's behavior when the preference is unset.
	if gc, gcErr := config.LoadGlobalConfig(); gcErr == nil {
		inst, _ := readInstanceSettings(instancePath)
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
		}
	}

	if err := dispatchLaunch(cmd.Context(), instancePath, prompt, passthrough); err != nil {
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
func buildDispatchPassthrough(slug string) []string {
	var pass []string
	if dispatchModel != "" {
		pass = append(pass, "--model", dispatchModel)
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
