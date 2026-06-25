package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	dispatchCmd.Flags().StringVar(&dispatchLabel, "label", "", "optional human-friendly alias recorded on the session mapping")
	dispatchCmd.Flags().StringVar(&dispatchModel, "model", "", "model to forward to the background worker (--model)")
	dispatchCmd.Flags().StringVar(&dispatchPermissionMode, "permission-mode", "", "permission mode to forward to the background worker (--permission-mode)")
	dispatchCmd.Flags().StringVar(&dispatchAgent, "agent", "", "agent to forward to the background worker (--agent)")
	dispatchCmd.Flags().BoolVarP(&dispatchDetach, "detach", "d", false, "do not attach the terminal to the new session; print hints and return")
	rootCmd.AddCommand(dispatchCmd)
}

var (
	dispatchLabel          string
	dispatchModel          string
	dispatchPermissionMode string
	dispatchAgent          string
	dispatchDetach         bool
)

const (
	// dispatchPendingMarker is the file dropped inside a dispatch-created
	// instance at create time and removed only after the session mapping is
	// durably written. Its contents are an RFC3339 creation timestamp. The
	// reaper backstop (Issue 5) keys on this file plus the embedded timestamp
	// to reclaim a SIGKILL-orphaned, marked-and-unmapped instance past the
	// backstop TTL (DESIGN Decision 4).
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
so dispatch never leaves an unreclaimable orphan.`,
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
	// racy numbered scan (DESIGN Decision 2).
	namePrefix, err := dispatchNameSuffix()
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

	// (7) Drop the pending-marker carrying its own creation timestamp. The
	// reaper backstop keys on this for the SIGKILL gap.
	if err := writeDispatchMarker(instancePath); err != nil {
		// The instance exists, so arm a manual rollback for this early failure.
		_ = destroyInstanceFunc(instancePath)
		return fmt.Errorf("niwa: error: writing dispatch pending-marker: %w", err)
	}

	// (8) Arm the deferred self-rollback. ANY early return after create -- and
	// before success is set -- destroys the just-created instance (DESIGN
	// Decision 4). A Go defer does not run on SIGKILL; the marker+TTL reaper
	// backstop closes that remaining gap.
	success := false
	defer func() {
		if !success {
			_ = destroyInstanceFunc(instancePath)
		}
	}()

	// (9) Launch the background worker rooted in the instance. Flags become
	// discrete argv elements -- never string-concatenated -- so a crafted value
	// cannot inject a claude flag (DESIGN Decision 8).
	passthrough := buildDispatchPassthrough()
	if err := dispatchLaunch(cmd.Context(), instancePath, prompt, passthrough); err != nil {
		return fmt.Errorf("niwa: error: launching dispatch worker: %w", err)
	}

	// (10) Capture the worker's full session UUID by jobs-dir cwd correlation.
	sessionID, err := dispatchCapture(defaultJobsDir(), instancePath, dispatchCaptureTimeout, nil, 0)
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

	// (13) Print the session id and management hints.
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Dispatched session %s\n", sessionID)
	fmt.Fprintf(out, "  instance: %s\n", instancePath)
	fmt.Fprintf(out, "  claude attach %s\n", sessionID)
	fmt.Fprintf(out, "  claude logs %s\n", sessionID)
	fmt.Fprintf(out, "  claude stop %s\n", sessionID)

	// (14) Unless --detach, attach the terminal to the new session as the FINAL
	// step. An attach failure is NON-fatal: the session and instance survive, so
	// degrade to a warning and never roll back or delete the mapping (success is
	// already true; DESIGN Decision 1).
	if !dispatchDetach {
		if err := dispatchAttach(sessionID); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: warning: could not attach to session %s: %v\n", sessionID, err)
			fmt.Fprintf(cmd.ErrOrStderr(), "niwa: the session is running; attach later with: claude attach %s\n", sessionID)
		}
	}

	return nil
}

// dispatchNameSuffix returns a unique "disp-<8 lowercase hex>" name suffix,
// using crypto/rand for collision safety under concurrency without a lock
// (DESIGN Decision 2).
func dispatchNameSuffix() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "disp-" + hex.EncodeToString(b[:]), nil
}

// buildDispatchPassthrough turns the set pass-through flags into discrete argv
// elements (flag, value pairs). Each value stays its own element so a crafted
// value cannot smuggle in an extra claude flag (DESIGN Decision 8).
func buildDispatchPassthrough() []string {
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
