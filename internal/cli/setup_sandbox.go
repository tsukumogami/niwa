package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// setup-sandbox is the ONE opt-in privileged step (design Decision 8B, PRD R19).
// It unlocks the OS-sandbox capability on a hardened Linux host -- the case where
// bwrap is installed but the kernel creates the unprivileged user namespace
// without the capabilities bwrap needs to configure the network namespace (e.g.
// Ubuntu 24.04's apparmor_restrict_unprivileged_userns=1), so bwrap refuses to
// start. It is never invoked per dispatch. On macOS and permissive Linux it is a
// no-op that reports "already capable". It does NOT install binaries -- bubblewrap
// and socat are niwa's Linux runtime dependencies (tsuku install), not this
// command's job.
var setupSandboxCmd = &cobra.Command{
	Use:   "setup-sandbox",
	Short: "Unlock the OS-sandbox capability on a hardened Linux host (one-time, privileged)",
	Long: `setup-sandbox unlocks the Claude Code OS sandbox on a hardened Linux host.

On such hosts (e.g. Ubuntu 24.04 with apparmor_restrict_unprivileged_userns=1)
the unprivileged user namespace is created without the capabilities bwrap needs
to configure the network namespace, so bwrap refuses to start and 'niwa watch
--once' cannot enforce no-egress containment. The fix -- an AppArmor profile that
grants the bwrap binary the userns capability -- is root-only.

This command is the single opt-in privileged step. It is run once, never per
dispatch. On macOS (built-in Seatbelt) and on permissive Linux it reports
"already capable" and changes nothing. It does not install bubblewrap or socat;
those are niwa's Linux runtime dependencies, provided by a normal 'tsuku install'
with no sudo.

Run it WITHOUT sudo: 'niwa setup-sandbox'. niwa probes the sandbox as your
unprivileged user (the only accurate measurement) and elevates only the root-only
profile install itself, prompting for your password when a terminal is available.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runSetupSandbox,
}

// setupSandboxApplyProfile marks the elevated child re-exec: the parent probes
// the sandbox as the unprivileged user, then re-runs itself under sudo with this
// flag so only the AppArmor install runs as root.
var setupSandboxApplyProfile bool

// setupSandboxBwrapPath carries the bwrap path the unprivileged parent resolved,
// so the elevated child installs the profile for the same binary the parent
// probed (rather than re-resolving PATH under sudo, which may differ).
var setupSandboxBwrapPath string

func init() {
	rootCmd.AddCommand(setupSandboxCmd)
	setupSandboxCmd.Flags().BoolVar(&setupSandboxApplyProfile, "apply-profile", false, "internal: run the elevated AppArmor install step (set by the sudo re-exec)")
	setupSandboxCmd.Flags().StringVar(&setupSandboxBwrapPath, "bwrap-path", "", "internal: bwrap path resolved by the unprivileged parent")
	_ = setupSandboxCmd.Flags().MarkHidden("apply-profile")
	_ = setupSandboxCmd.Flags().MarkHidden("bwrap-path")
}

const apparmorProfilePath = "/etc/apparmor.d/niwa-bwrap"

// apparmorBwrapProfile returns the AppArmor profile that grants the bwrap binary
// at bwrapPath the userns capability, which is the least-privilege unlock on a
// hardened host (a per-binary grant, not the global sysctl hammer that would
// re-enable unprivileged user namespaces for every binary).
func apparmorBwrapProfile(bwrapPath string) string {
	return fmt.Sprintf(`# Managed by 'niwa setup-sandbox'. Grants bwrap the userns capability on a
# hardened kernel (apparmor_restrict_unprivileged_userns=1) so the Claude Code
# OS sandbox can create its network namespace. Least-privilege: scoped to this
# one binary, rather than relaxing the kernel restriction for every binary.
abi <abi/4.0>,
include <tunables/global>

profile niwa-bwrap %s flags=(unconfined) {
  userns,
  include if exists <local/niwa-bwrap>
}
`, bwrapPath)
}

func runSetupSandbox(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	out := cmd.OutOrStdout()

	switch runtime.GOOS {
	case "darwin":
		// Seatbelt (sandbox-exec) is built in and needs no unlock. If the probe
		// still fails, there is nothing this command can install.
		if err := sandboxCapabilityCheck(ctx); err == nil {
			fmt.Fprintln(out, "setup-sandbox: already capable (macOS Seatbelt); nothing to do.")
			return nil
		}
		return fmt.Errorf("setup-sandbox: the macOS sandbox tool 'sandbox-exec' is unavailable and cannot be installed by niwa; verify the base system")
	case "linux":
		return runSetupSandboxLinux(ctx, cmd)
	default:
		return fmt.Errorf("setup-sandbox: the OS sandbox is unavailable on GOOS=%s (Linux and macOS are supported); nothing to unlock", runtime.GOOS)
	}
}

func runSetupSandboxLinux(ctx context.Context, cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	// Elevated-child branch: the parent already probed as the unprivileged user
	// and re-execed us under sudo. We only install the profile -- never probe
	// netns here, because root can create a netns even on a restricted host and
	// would falsely report "capable".
	if setupSandboxApplyProfile {
		if err := validateApplyProfileArgs(os.Geteuid() == 0, setupSandboxBwrapPath); err != nil {
			return err
		}
		return applyApparmorProfile(ctx, cmd, setupSandboxBwrapPath)
	}

	bwrapPath, bwrapErr := exec.LookPath("bwrap")
	_, socatErr := exec.LookPath("socat")
	bwrapOK := bwrapErr == nil
	socatOK := socatErr == nil

	if !bwrapOK || !socatOK {
		missing := make([]string, 0, 2)
		if !bwrapOK {
			missing = append(missing, "bubblewrap (bwrap)")
		}
		if !socatOK {
			missing = append(missing, "socat")
		}
		return fmt.Errorf("setup-sandbox: %s not on PATH. These are niwa's Linux runtime dependencies; install them first (a normal 'tsuku install' provides them, no sudo). setup-sandbox unlocks the kernel capability, it does not install binaries",
			strings.Join(missing, " and "))
	}

	// Running under sudo directly defeats the point: root probes netns
	// successfully even on a restricted host, so the capability check is
	// meaningless. Send the user back to the unprivileged invocation, which
	// probes correctly and elevates only the install.
	if os.Geteuid() == 0 {
		fmt.Fprintln(out, "setup-sandbox: don't run this with sudo. niwa probes the sandbox as your")
		fmt.Fprintln(out, "unprivileged user and elevates only the step that needs root; running the")
		fmt.Fprintln(out, "whole command as root makes the capability check falsely pass.")
		fmt.Fprintln(out, "Re-run without sudo:\n    niwa setup-sandbox")
		return fmt.Errorf("setup-sandbox: run without sudo; niwa elevates the privileged step itself")
	}

	// Probe as the unprivileged user -- the only measurement that reflects how
	// the Claude Code sandbox actually runs.
	if probeNetnsOK(ctx, bwrapPath) {
		fmt.Fprintln(out, "setup-sandbox: already capable; the OS sandbox can create a network namespace here. Nothing to do.")
		return nil
	}

	// Remediation is needed. Show exactly what will run as root before elevating.
	note := "bwrap is installed but cannot create a network namespace"
	if hardenedUsernsRestricted() {
		note = "this host restricts unprivileged user namespaces (apparmor_restrict_unprivileged_userns=1)"
	}
	fmt.Fprintf(out, "setup-sandbox: %s.\n", note)
	fmt.Fprintf(out, "The fix is an AppArmor profile granting bwrap the userns capability. It is root-only.\n\n")
	fmt.Fprintf(out, "It will write %s with:\n\n%s\n", apparmorProfilePath, apparmorBwrapProfile(bwrapPath))

	sudoPath, sudoErr := exec.LookPath("sudo")
	if sudoErr == nil && stdinIsTTY() {
		fmt.Fprintln(out, "Elevating with sudo (you may be prompted for your password)...")
		self, err := os.Executable()
		if err != nil {
			return fmt.Errorf("setup-sandbox: cannot locate the niwa executable to elevate: %w", err)
		}
		elevate := exec.CommandContext(ctx, sudoPath, self, "setup-sandbox", "--apply-profile", "--bwrap-path", bwrapPath)
		elevate.Stdin = os.Stdin
		elevate.Stdout = out
		elevate.Stderr = cmd.ErrOrStderr()
		if err := elevate.Run(); err != nil {
			return fmt.Errorf("setup-sandbox: the elevated install step failed: %w", err)
		}
		// Confirm as the unprivileged parent -- the child never probed.
		if probeNetnsOK(ctx, bwrapPath) {
			fmt.Fprintln(out, "setup-sandbox: done. The OS sandbox can now create a network namespace; 'niwa watch --once' can enforce no-egress containment.")
			return nil
		}
		return fmt.Errorf("setup-sandbox: the AppArmor profile was installed but the sandbox still cannot create a network namespace; the host may need a reboot, or the Claude Code sandbox uses a different bwrap than %s", bwrapPath)
	}

	// No sudo, or no TTY to prompt on: instruct the correct root invocation that
	// skips the misleading root-side probe.
	fmt.Fprintf(out, "Re-run with elevation:\n    sudo niwa setup-sandbox --apply-profile --bwrap-path %s\n", bwrapPath)
	return fmt.Errorf("setup-sandbox: root privileges required to unlock the sandbox capability; nothing was changed")
}

// validateApplyProfileArgs guards the elevated-child branch: the install must run
// as root, and the parent-resolved bwrap path must name an existing file. It is a
// pure function so the validation matrix is unit-testable without root or sudo.
func validateApplyProfileArgs(isRoot bool, bwrapPath string) error {
	if !isRoot {
		return fmt.Errorf("setup-sandbox: --apply-profile must run as root")
	}
	if bwrapPath == "" {
		return fmt.Errorf("setup-sandbox: --apply-profile requires --bwrap-path")
	}
	fi, err := os.Stat(bwrapPath)
	if err != nil || fi.IsDir() {
		return fmt.Errorf("setup-sandbox: --bwrap-path %q does not name an existing file", bwrapPath)
	}
	return nil
}

// stdinIsTTY reports whether stdin is a terminal, so we only auto-elevate (which
// may prompt for a password) when there is a human to answer the prompt.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// probeNetnsOK reports whether bwrap can actually create a network-isolated
// namespace here. It mirrors the watch preflight's functional probe.
func probeNetnsOK(ctx context.Context, bwrapPath string) bool {
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(pctx, bwrapPath, "--ro-bind", "/", "/", "--unshare-net", "--die-with-parent", "true")
	return cmd.Run() == nil
}

// hardenedUsernsRestricted reports whether the kernel restricts unprivileged
// user namespaces via the AppArmor sysctl (the Ubuntu 24.04 default). It is
// informational -- the remediation is the same AppArmor profile regardless -- so
// a read failure is treated as "not known to be restricted".
func hardenedUsernsRestricted() bool {
	data, err := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == "1"
}

// applyApparmorProfile writes and loads the AppArmor profile (root path). It runs
// in the elevated child, so it only reports what it wrote -- the unprivileged
// parent owns the netns confirmation, since root can create a namespace even on a
// restricted host and would falsely pass. It is idempotent: an identical
// already-loaded profile re-parses harmlessly.
func applyApparmorProfile(ctx context.Context, cmd *cobra.Command, bwrapPath string) error {
	out := cmd.OutOrStdout()
	profile := apparmorBwrapProfile(bwrapPath)

	if existing, err := os.ReadFile(apparmorProfilePath); err == nil && string(existing) == profile {
		fmt.Fprintf(out, "setup-sandbox: %s already up to date; reloading.\n", apparmorProfilePath)
	} else {
		if err := os.WriteFile(apparmorProfilePath, []byte(profile), 0o644); err != nil {
			return fmt.Errorf("setup-sandbox: writing %s: %w", apparmorProfilePath, err)
		}
		fmt.Fprintf(out, "setup-sandbox: wrote %s.\n", apparmorProfilePath)
	}

	if _, err := exec.LookPath("apparmor_parser"); err != nil {
		return fmt.Errorf("setup-sandbox: apparmor_parser not on PATH; cannot load the profile at %s (is AppArmor installed?)", apparmorProfilePath)
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	load := exec.CommandContext(pctx, "apparmor_parser", "-r", apparmorProfilePath)
	load.Stdout = out
	load.Stderr = cmd.ErrOrStderr()
	if err := load.Run(); err != nil {
		return fmt.Errorf("setup-sandbox: loading the AppArmor profile failed: %w", err)
	}

	fmt.Fprintf(out, "setup-sandbox: loaded the AppArmor profile for %s.\n", bwrapPath)
	return nil
}
