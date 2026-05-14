// Package web hosts the machine-level HTTP listener that renders niwa
// changes for browser-based review. The listener binds 127.0.0.1 only
// (NFR4: no network exposure) and aggregates across every niwa instance
// discovered under each workspace registered in the user's global
// config. Routing is hierarchical:
//
//	/                                                       → 302 /workspaces/
//	/workspaces/                                            → index of every workspace
//	/workspaces/<workspace>/                                → instances under that workspace
//	/workspaces/<workspace>/<instance>/changes/             → changes in that instance
//	/workspaces/<workspace>/<instance>/changes/<change-id>  → per-change render
//
// The composeChangeURL path in internal/mcp reads the same registry +
// machine-level surface.port file to produce a URL with the same shape
// so agent-emitted URLs resolve against this listener verbatim.
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

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/mcp"
)

// Config carries the listener-construction inputs. Instances enumerates
// every (workspace, instance, root) endpoint the server serves; the
// list comes from config.EnumerateInstances at boot. Port=0 binds an
// ephemeral port. ListenAddr defaults to 127.0.0.1; callers do not
// override this in production — the field is present so tests can bind
// to 127.0.0.1:0 explicitly. SinkFor is a per-instance audit-sink
// factory; when nil, NewFileAuditSink is used. The factory hook lets
// tests inject a recording sink without touching the real audit path.
type Config struct {
	Instances  []config.WorkspaceInstance
	Port       int
	ListenAddr string
	SinkFor    func(instanceRoot string) mcp.AuditSink
}

// New constructs the machine-level HTTP server. The listener is bound
// inside New so the caller learns the actual port (matters when
// cfg.Port == 0). The caller is responsible for serving on the bound
// listener via srv.Serve(ln) — New returns the configured server, the
// bound listener (so the caller can read its address), and any error.
//
// Routing: routes are wired through Handlers.RegisterRoutes from
// handlers.go. The Bearer-auth middleware wraps zero routes at F5;
// F10's mutation API composes on the same contract.
func New(_ context.Context, cfg Config) (*http.Server, net.Listener, error) {
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1"
	}
	listenSpec := net.JoinHostPort(addr, strconv.Itoa(cfg.Port))

	ln, err := net.Listen("tcp", listenSpec)
	if err != nil {
		return nil, nil, fmt.Errorf("bind %s: %w", listenSpec, err)
	}

	sinkFor := cfg.SinkFor
	if sinkFor == nil {
		sinkFor = func(root string) mcp.AuditSink {
			return mcp.NewFileAuditSink(root)
		}
	}

	mux := http.NewServeMux()
	h := &Handlers{
		Instances: cfg.Instances,
		SinkFor:   sinkFor,
	}
	h.RegisterRoutes(mux)

	// Middleware order: CORS strip outermost so a cross-origin request
	// fails before any handler logic runs. The Bearer-auth middleware is
	// compiled and ready but not applied at F5 — the contract is locked
	// here so F10 composes on it without inventing a new auth surface.
	handler := corsStrip(mux)

	srv := &http.Server{Handler: handler}
	return srv, ln, nil
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
