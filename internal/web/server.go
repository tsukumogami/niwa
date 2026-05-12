// Package web hosts the per-instance HTTP listener that renders niwa
// changes for browser-based review. The listener binds 127.0.0.1 only
// (NFR4: no network exposure), composes the changestore reads from
// internal/mcp with the templates from internal/web/render, and is
// wired into the niwa lifecycle by the `niwa surface serve` CLI command.
//
// Issue #6 shipped the boot scaffold (server.go, auth.go); issue #8
// replaced the 501 stubs with the real handlers (see handlers.go) so
// the three GET routes now compose the changestore reads and
// dual-target event emitter end-to-end.
//
// Security boundaries: the Bearer-token middleware is compiled and
// available but applied to zero routes at F5; F10's mutation API
// composes on this same contract so the auth surface is locked early.
// The CORS-strip middleware rejects cross-origin requests so a hostile
// page cannot read change diffs via fetch().
package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// Config carries the listener-construction inputs. InstanceRoot is the
// absolute path to the niwa instance whose .niwa/changes/ directory the
// server serves. Port=0 binds an ephemeral port. ListenAddr defaults to
// 127.0.0.1; callers do not override this in production — the field is
// present so tests can bind to 127.0.0.1:0 explicitly. AuditSink is the
// destination for review_surface_opened and change_engaged events; a
// nil sink elides audit emission (the per-change transitions.log writes
// still run via AppendChangeEvent).
type Config struct {
	InstanceRoot string
	Port         int
	ListenAddr   string
	AuditSink    mcp.AuditSink
}

// GCStop is the function returned by New for the GC ticker shutdown. At
// F5 the placeholder returned by New does nothing; issue #9's gc.Run
// integration replaces it with the real ticker-stop closure.
type GCStop func()

// New constructs the per-instance HTTP server. The listener is bound
// inside New so the caller learns the actual port (matters when
// cfg.Port == 0). The caller is responsible for serving on the bound
// listener via srv.Serve(ln) — New returns the configured server, the
// bound listener (so the caller can read its address), the GC stop
// function (placeholder at F5), and any error.
//
// Routing: the three GET routes are wired through Handlers.RegisterRoutes
// from handlers.go (issue #8). The Bearer-auth middleware wraps zero
// routes at F5; F10's mutation API composes on the same contract.
func New(ctx context.Context, cfg Config) (*http.Server, net.Listener, GCStop, error) {
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1"
	}
	listenSpec := net.JoinHostPort(addr, strconv.Itoa(cfg.Port))

	ln, err := net.Listen("tcp", listenSpec)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("bind %s: %w", listenSpec, err)
	}

	mux := http.NewServeMux()
	h := &Handlers{InstanceRoot: cfg.InstanceRoot, Sink: cfg.AuditSink}
	h.RegisterRoutes(mux)

	// Middleware order: CORS strip outermost so a cross-origin request
	// fails before any handler logic runs. The Bearer-auth middleware is
	// compiled and ready but not applied at F5 — the contract is locked
	// here so F10 composes on it without inventing a new auth surface.
	handler := corsStrip(mux)

	srv := &http.Server{Handler: handler}
	stop := GCStop(func() {})
	_ = ctx // reserved for future integration with the GC ticker
	return srv, ln, stop, nil
}

// corsStrip rejects requests with a non-empty Origin header and emits
// no Access-Control-Allow-* headers in any response. The same-origin
// browser path passes through unchanged; cross-origin fetches receive
// 403 from this layer before the handler runs, and even if the response
// were returned, the absence of CORS headers would cause the browser to
// decline rendering it. Belt-and-braces per NFR4.
func corsStrip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			http.Error(w, "cross-origin requests are not permitted", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
