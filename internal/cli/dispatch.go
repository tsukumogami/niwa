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
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	dispatchCmd.Flags().StringVar(&dispatchLabel, "label", "", "optional human-friendly alias recorded on the session mapping")
	dispatchCmd.Flags().StringVarP(&dispatchName, "name", "n", "", "optional display name for the session (sanitized into a slug; also names the niwa instance)")
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
// instance directory name (which is "<config>-<slug>-disp-<8hex>"). 40 runes is
// generous for a human-readable label while leaving room below filesystem name
// limits for the config prefix and the "-disp-<8hex>" signature suffix.
const maxDispatchSlugRunes = 40

const (
	// dispatchNameSegment is the segment embedded in every dispatch-created
	// instance name, between the config name and the random hex suffix:
	// "<config>-disp-<8hex>". It is the SOLE eligibility signal the reaper
	// backstop keys on, because the directory name is created ATOMICALLY by
	// provisionInstanceFunc -- there is no window (as there is with a marker
	// file written after create) in which a SIGKILL can leave a dispatch
	// instance both unmapped and unmarked. Both the dispatch naming
	// (dispatchNameSuffix) and the backstop predicate (isDispatchInstanceName)
	// derive from this const so they cannot drift.
	dispatchNameSegment = "disp-"

	// dispatchPendingMarker is the file dropped inside a dispatch-created
	// instance at create time and removed only after the session mapping is
	// durably written. Its contents are an RFC3339 creation timestamp. The
	// marker is now a PRECISION aid for the reaper backstop's age check (it
	// carries the exact creation time), NOT the sole eligibility signal: the
	// backstop keys eligibility on the instance NAME (dispatchNameSegment) and
	// falls back to the directory mtime when the marker is absent (the
	// SIGKILL-before-marker case), so the orphan window is closed (DESIGN
	// Decision 4).
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

	// (4) Generate a unique disp-<8 hex> name suffix via crypto/rand and pass it
	// as the customName branch of the existing provision path, sidestepping the
	// racy numbered scan (DESIGN Decision 2). When --name sanitizes to a usable
	// slug it is inserted BEFORE the "-disp-<8hex>" segment, so the name becomes
	// "<config>-<slug>-disp-<8hex>": the slug is additive and never replaces the
	// random hex, so the end-anchored isDispatchInstanceName signature (and thus
	// the reaper backstop) keeps matching.
	slug := sanitizeInstanceSlug(dispatchName)
	namePrefix, err := dispatchNameSuffix(slug)
	if err != nil {
		return fmt.Errorf("niwa: error: generating instance name: %w", err)
	}

	// (5) Self-bound orphans: run the opportunistic reclamation sweep the same
	// way runCreate does, before creating the new instance (R12).
	reapOpportunistically(workspaceRoot)

	// (6) Create the instance through the existing provision path.
	res, err := provisionInstanceFunc(cmd.Context(), workspaceRoot, cwd, namePrefix)
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

// dispatchNameSuffix returns a unique name suffix anchored on a
// "disp-<8 lowercase hex>" segment, using crypto/rand for collision safety under
// concurrency without a lock (DESIGN Decision 2). The provision path appends this
// to the config name, so the resulting instance directory is
// "<config>-disp-<8hex>" -- the shape isDispatchInstanceName recognizes.
//
// When slug is non-empty it is inserted BEFORE the "disp-<8hex>" segment, giving
// "<slug>-disp-<8hex>" (instance dir "<config>-<slug>-disp-<8hex>"). The slug is
// additive: the random hex is always kept (uniqueness + signature) and the
// "-disp-<8hex>" suffix stays end-anchored, so isDispatchInstanceName still
// matches and the reaper backstop is unaffected.
func dispatchNameSuffix(slug string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	suffix := dispatchNameSegment + hex.EncodeToString(b[:])
	if slug != "" {
		return slug + "-" + suffix, nil
	}
	return suffix, nil
}

// sanitizeInstanceSlug normalizes a raw --name value into a filesystem- and
// flag-safe slug: lowercase, every run of characters outside [a-z0-9] collapsed
// to a single hyphen, leading/trailing hyphens trimmed, and the result capped to
// maxDispatchSlugRunes (re-trimming a trailing hyphen the cap may expose). It
// returns "" when nothing usable remains, signaling the caller to fall back to
// the slug-less behavior. The result is guaranteed to contain only [a-z0-9-] and
// to neither lead nor trail with a hyphen.
//
// It is shared by `niwa dispatch` (which embeds the slug in the ephemeral
// instance name) and `niwa create` (which uses it as the --name suffix), so both
// commands normalize a custom name identically.
func sanitizeInstanceSlug(raw string) string {
	var b strings.Builder
	prevHyphen := false
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if r := []rune(slug); len(r) > maxDispatchSlugRunes {
		slug = strings.TrimRight(string(r[:maxDispatchSlugRunes]), "-")
	}
	return slug
}

// dispatchInstanceNameRe matches a dispatch instance's base directory name: a
// config name, then the "-disp-" segment, then exactly 8 lowercase hex digits
// at the end. Anchoring on the 8-hex suffix mirrors dispatchNameSuffix exactly
// so a developer instance ("<config>", "<config>-2") or a hook-created instance
// ("<config>-<sessionhex>", no "-disp-" segment) never matches.
var dispatchInstanceNameRe = regexp.MustCompile(`-` + dispatchNameSegment + `[0-9a-f]{8}$`)

// isDispatchInstanceName reports whether name is a dispatch-created instance's
// base directory name. The dispatch backstop uses this as its eligibility
// signal: because provisionInstanceFunc creates the directory (and thus this
// name) atomically, a dispatch instance is recognizable the instant it exists,
// closing the SIGKILL-before-marker orphan window that a marker-file-only gate
// left open.
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
