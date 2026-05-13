// surface.go wires the `niwa surface serve` command — the machine-level
// HTTP listener for browser-based change review (PRD F5 / R10). One
// process per user, federating across every niwa instance discovered
// under each workspace in ~/.config/niwa/config.toml's [registry].
//
// The command owns four pieces of substrate, all at machine scope under
// the niwa config directory (XDG_CONFIG_HOME/niwa or ~/.config/niwa):
//
//  1. `surface.lock` — PID file gating single-listener-per-machine.
//     O_CREATE|O_EXCL on first try; on EEXIST the lock contents (the
//     prior PID) are checked via mcp.IsProcessAlive. A dead holder is
//     reaped and the boot retries once; a live holder makes the boot
//     exit 1 with the R10 message.
//  2. `surface.token` — UUIDv4, mode 0o600. Absent → generated.
//     Present and `--rotate-token` → regenerated. Present and no flag →
//     left intact so restarts don't invalidate open browser tabs.
//  3. `surface.port` — the actual bound port. Written via tmp+rename so
//     a concurrent niwa_query_change URL-compose pass never sees a
//     partial file. Removed on shutdown.
//  4. The HTTP listener itself, composed via internal/web.New. A per-
//     instance mcp.AuditSink is constructed for each discovered niwa
//     instance and threaded into internal/web/gc.Run so cleanup events
//     land in the originating instance's mcp-audit.log — the audit
//     substrate stays per-instance even though the surface is unified.
//
// Discovery is boot-time only: instances are enumerated from the global
// registry plus a directory scan of each workspace root. Restart is
// required when a new instance is added or a workspace's [registry]
// entry changes. F5 deliberately scopes hot-reload out — the surface is
// a long-running daemon, and discovery races would surface as missing
// or phantom changes in the index.
//
// Shutdown discipline: signal.NotifyContext(SIGINT, SIGTERM) drives a
// 5-second http.Server.Shutdown. surface.lock and surface.port are
// always removed; surface.token persists so a restart is a no-op for
// any browser tab holding the current token.

package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/web"
	"github.com/tsukumogami/niwa/internal/web/gc"
)

// surfaceServeFlags captures the parsed --port and --rotate-token values
// from cobra. Package-level vars are the existing CLI convention (see
// session.go's sessionList* vars) and keep the test harness simple —
// tests reset them between cases via t.Cleanup.
var (
	surfaceServePort        int
	surfaceServeRotateToken bool
)

const (
	surfaceLockFileName  = "surface.lock"
	surfaceTokenFileName = "surface.token"
	surfacePortFileName  = "surface.port"

	// surfaceShutdownGrace caps http.Server.Shutdown per D4. Matches
	// mcp-serve's existing 5-second window; in-flight `GET /changes/<id>`
	// renders sit well under NFR2's 200 ms budget so the cap is generous.
	surfaceShutdownGrace = 5 * time.Second

	// GC defaults match the PRD R9 spec. F5 hardcodes them; the
	// workspace.toml `[changes]` integration arrives with a later PLAN
	// issue and is out of scope here.
	surfaceGCDefaultIntervalHours = 6
	surfaceGCDefaultAbandonDays   = 14
)

func init() {
	surfaceCmd.AddCommand(surfaceServeCmd)
	rootCmd.AddCommand(surfaceCmd)

	surfaceServeCmd.Flags().IntVar(&surfaceServePort, "port", 0,
		"TCP port to bind on 127.0.0.1 (0 = kernel-assigned ephemeral)")
	surfaceServeCmd.Flags().BoolVar(&surfaceServeRotateToken, "rotate-token", false,
		"regenerate surface.token even if it already exists; open browser tabs must reload")
}

var surfaceCmd = &cobra.Command{
	Use:   "surface",
	Short: "Manage the machine-level HTTP review surface",
	Long: `Manage the machine-level HTTP review surface.

Subcommands:
  serve    Run the listener that aggregates changes across every registered workspace`,
}

var surfaceServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the change-review HTTP listener for every registered workspace",
	Long: `Run the change-review HTTP listener on 127.0.0.1, aggregating across every
registered workspace.

Boot order matches PRD R10: load the global config, enumerate every niwa
instance under each registered workspace, acquire surface.lock with a
stale-PID reap retry, ensure surface.token (regenerate with --rotate-token),
bind 127.0.0.1 (ephemeral or --port N), write surface.port atomically,
print the URL and token-file path to stderr, run the GC sweep once
across every instance, then serve until SIGINT/SIGTERM triggers a
5-second http.Server.Shutdown. The token contents are never printed —
only the path to the token file.`,
	RunE: runSurfaceServe,
}

func runSurfaceServe(cmd *cobra.Command, _ []string) error {
	g, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("load global config: %w", err)
	}
	instances, err := config.EnumerateInstances(g)
	if err != nil {
		return fmt.Errorf("enumerate instances: %w", err)
	}
	dir, err := config.SurfaceConfigDir()
	if err != nil {
		return fmt.Errorf("resolve surface config dir: %w", err)
	}
	return surfaceServeMachine(cmd, dir, instances, surfaceServePort, surfaceServeRotateToken)
}

// surfaceServeMachine is the testable boot core. It takes an explicit
// configDir (where surface.lock/token/port live) and the discovered
// instance list so tests can drive it without a real ~/.config/niwa
// layout. The real CLI entry (runSurfaceServe) reads these from the
// global config and then delegates.
func surfaceServeMachine(
	cmd *cobra.Command,
	configDir string,
	instances []config.WorkspaceInstance,
	port int,
	rotateToken bool,
) (retErr error) {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create surface config dir: %w", err)
	}

	// Signal handling wraps cmd.Context so tests can drive shutdown by
	// cancelling the cmd's context — equivalent to SIGTERM arriving.
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	// Step 1: acquire surface.lock (single retry on stale PID).
	lockPath := filepath.Join(configDir, surfaceLockFileName)
	if err := acquireSurfaceLock(lockPath); err != nil {
		return err
	}
	defer func() { _ = os.Remove(lockPath) }()

	// Step 2: ensure surface.token (UUIDv4, 0o600).
	tokenPath, err := ensureSurfaceToken(configDir, rotateToken)
	if err != nil {
		return fmt.Errorf("ensure surface.token: %w", err)
	}

	// Step 3: bind 127.0.0.1 listener.
	srv, ln, err := web.New(ctx, web.Config{
		Port:      port,
		Instances: instances,
	})
	if err != nil {
		return fmt.Errorf("surface listener: %w", err)
	}
	actualPort, err := listenerPort(ln)
	if err != nil {
		_ = ln.Close()
		return err
	}
	portPath := filepath.Join(configDir, surfacePortFileName)
	if err := writeSurfacePort(portPath, actualPort); err != nil {
		_ = ln.Close()
		return fmt.Errorf("write surface.port: %w", err)
	}
	defer func() { _ = os.Remove(portPath) }()

	// Step 4: stderr banner. Token CONTENTS are never written — only the
	// path. The unit test asserts the token bytes never appear on stderr.
	fmt.Fprintf(cmd.ErrOrStderr(),
		"niwa surface listening on http://127.0.0.1:%d\n", actualPort)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"token stored at %s (read-only, mode 0600)\n", tokenPath)
	fmt.Fprintf(cmd.ErrOrStderr(),
		"serving %d instance(s) across %d workspace(s)\n",
		len(instances), countWorkspaces(instances))

	// Step 5: synchronous on-boot GC sweep across every instance, then a
	// shared ticker goroutine. A per-instance sink lets cleanup events
	// land in the originating instance's mcp-audit.log so audit history
	// stays cohabited with the change data.
	targets := make([]gc.Target, len(instances))
	for i, inst := range instances {
		targets[i] = gc.Target{
			InstanceRoot: inst.Root,
			Sink:         mcp.NewFileAuditSink(inst.Root),
		}
	}
	gcStop, err := gc.Run(ctx, targets, gc.Config{
		IntervalHours: surfaceGCDefaultIntervalHours,
		AbandonDays:   surfaceGCDefaultAbandonDays,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer gcStop()

	// Step 6: serve until cancellation, then 5-second Shutdown.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), surfaceShutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			retErr = fmt.Errorf("surface shutdown: %w", err)
		}
		// Drain Serve's exit so the goroutine doesn't outlive us.
		<-errCh
		return retErr
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("surface serve: %w", err)
	}
}

// countWorkspaces returns the number of distinct workspace identifiers
// in the instance list. The serving-summary banner uses this so the
// operator can sanity-check that the registry was read as expected.
func countWorkspaces(instances []config.WorkspaceInstance) int {
	seen := make(map[string]struct{}, len(instances))
	for _, inst := range instances {
		seen[inst.Workspace] = struct{}{}
	}
	return len(seen)
}

// acquireSurfaceLock writes surface.lock with the current PID via
// O_CREATE|O_EXCL. If the lock already exists, the holder's liveness is
// checked: a dead PID (or a corrupt file) is reaped and the create
// retries once; a live PID makes the function return the documented
// "lock held by PID N" error. Exactly one retry — a second EEXIST after
// reap means a racing process won the second create, which surfaces as
// the same "lock held" error.
func acquireSurfaceLock(lockPath string) error {
	if err := tryCreateSurfaceLock(lockPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create surface.lock: %w", err)
	}

	// Read prior PID and check liveness.
	data, rerr := os.ReadFile(lockPath)
	if rerr != nil {
		// Lock vanished between OpenFile and ReadFile — race-friendly:
		// retry the create.
		if errors.Is(rerr, os.ErrNotExist) {
			return tryCreateSurfaceLock(lockPath)
		}
		return fmt.Errorf("read surface.lock: %w", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid <= 0 {
		// Corrupt lock contents: treat as stale and reap.
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reap corrupt surface.lock: %w", err)
		}
		return tryCreateSurfaceLock(lockPath)
	}
	if mcp.IsProcessAlive(pid) {
		return fmt.Errorf("surface.lock held by PID %d", pid)
	}
	// Stale PID — reap and retry once.
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reap stale surface.lock: %w", err)
	}
	if err := tryCreateSurfaceLock(lockPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("surface.lock held by PID %d", pid)
		}
		return fmt.Errorf("create surface.lock after reap: %w", err)
	}
	return nil
}

func tryCreateSurfaceLock(lockPath string) error {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		return err
	}
	return nil
}

// ensureSurfaceToken writes a fresh UUIDv4 to surface.token if the file
// is absent OR rotate is true. The write is atomic (tmp+rename) so a
// concurrent reader never sees a half-written token. Returns the
// absolute token path for the boot banner.
func ensureSurfaceToken(configDir string, rotate bool) (string, error) {
	path := filepath.Join(configDir, surfaceTokenFileName)
	if rotate {
		if err := writeSurfaceToken(path); err != nil {
			return "", err
		}
		return path, nil
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat surface.token: %w", err)
	}
	if err := writeSurfaceToken(path); err != nil {
		return "", err
	}
	return path, nil
}

func writeSurfaceToken(path string) error {
	token := mcp.NewSessionID() // UUIDv4 from crypto/rand
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("write surface.token tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename surface.token: %w", err)
	}
	return nil
}

// writeSurfacePort writes the bound port to surface.port atomically.
// The mcp-serve side composes change-review URLs by reading this file,
// so a partial write would surface as a malformed URL in agent output.
// tmp+rename guarantees readers see either the prior bytes or the new
// ones, never a torn write.
func writeSurfacePort(portPath string, port int) error {
	tmp := portPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write surface.port tmp: %w", err)
	}
	if err := os.Rename(tmp, portPath); err != nil {
		return fmt.Errorf("rename surface.port: %w", err)
	}
	return nil
}

// listenerPort extracts the TCP port from a bound net.Listener. The
// concrete type is *net.TCPListener for both ephemeral and fixed-port
// binds since web.New always passes "tcp" to net.Listen.
func listenerPort(ln net.Listener) (int, error) {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener address is not TCP: %T", ln.Addr())
	}
	return addr.Port, nil
}
