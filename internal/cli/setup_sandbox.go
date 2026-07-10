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

Run it with sudo on a hardened Linux host: 'sudo niwa setup-sandbox'.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runSetupSandbox,
}

func init() {
	rootCmd.AddCommand(setupSandboxCmd)
}

// setupAction is the outcome of the pure decision function planSetupSandboxLinux.
type setupAction int

const (
	// actionAlreadyCapable: the sandbox can already be enforced -- no-op, exit 0.
	actionAlreadyCapable setupAction = iota
	// actionInstallDeps: bwrap/socat missing -- not this command's job; exit non-zero.
	actionInstallDeps
	// actionNeedRoot: remediation is needed but we are not root -- print the
	// exact steps and re-run instruction, change nothing, exit non-zero.
	actionNeedRoot
	// actionApplyProfile: remediation is needed and we are root -- install and
	// load the AppArmor profile, then re-probe.
	actionApplyProfile
)

// planSetupSandboxLinux is the pure decision core for the Linux path, factored
// out so the full matrix is unit-testable without root or a hardened kernel. The
// privileged side effects live only in the actionApplyProfile branch of the
// caller.
func planSetupSandboxLinux(bwrapOK, socatOK, netnsOK, isRoot bool) setupAction {
	if !bwrapOK || !socatOK {
		return actionInstallDeps
	}
	if netnsOK {
		return actionAlreadyCapable
	}
	if !isRoot {
		return actionNeedRoot
	}
	return actionApplyProfile
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

	bwrapPath, bwrapErr := exec.LookPath("bwrap")
	_, socatErr := exec.LookPath("socat")
	bwrapOK := bwrapErr == nil
	socatOK := socatErr == nil

	netnsOK := false
	if bwrapOK {
		netnsOK = probeNetnsOK(ctx, bwrapPath)
	}
	isRoot := os.Geteuid() == 0

	switch planSetupSandboxLinux(bwrapOK, socatOK, netnsOK, isRoot) {
	case actionAlreadyCapable:
		fmt.Fprintln(out, "setup-sandbox: already capable; the OS sandbox can create a network namespace here. Nothing to do.")
		return nil

	case actionInstallDeps:
		missing := make([]string, 0, 2)
		if !bwrapOK {
			missing = append(missing, "bubblewrap (bwrap)")
		}
		if !socatOK {
			missing = append(missing, "socat")
		}
		return fmt.Errorf("setup-sandbox: %s not on PATH. These are niwa's Linux runtime dependencies; install them first (a normal 'tsuku install' provides them, no sudo). setup-sandbox unlocks the kernel capability, it does not install binaries",
			strings.Join(missing, " and "))

	case actionNeedRoot:
		hardened := hardenedUsernsRestricted()
		note := "bwrap is installed but cannot create a network namespace"
		if hardened {
			note = "this host restricts unprivileged user namespaces (apparmor_restrict_unprivileged_userns=1)"
		}
		fmt.Fprintf(out, "setup-sandbox: %s.\n", note)
		fmt.Fprintf(out, "The fix is an AppArmor profile granting bwrap the userns capability. It is root-only.\n\n")
		fmt.Fprintf(out, "Re-run with elevation:\n    sudo niwa setup-sandbox\n\n")
		fmt.Fprintf(out, "It will write %s with:\n\n%s\n", apparmorProfilePath, apparmorBwrapProfile(bwrapPath))
		return fmt.Errorf("setup-sandbox: root privileges required to unlock the sandbox capability; nothing was changed")

	case actionApplyProfile:
		return applyApparmorProfile(ctx, cmd, bwrapPath)
	}
	return nil
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

// applyApparmorProfile writes and loads the AppArmor profile (root path), then
// re-probes to confirm the capability is now available. It is idempotent: an
// identical already-loaded profile re-parses harmlessly.
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

	if !probeNetnsOK(ctx, bwrapPath) {
		return fmt.Errorf("setup-sandbox: profile loaded but the sandbox still cannot create a network namespace; the host may need a reboot or use a different restriction mechanism (try setting kernel.apparmor_restrict_unprivileged_userns=0)")
	}
	fmt.Fprintln(out, "setup-sandbox: done. The OS sandbox can now create a network namespace; 'niwa watch --once' can enforce no-egress containment.")
	return nil
}
