// handlers.go implements the F5 hierarchical GET endpoints for the
// machine-level review surface:
//
//	GET /                                                       → 302 /workspaces/
//	GET /workspaces/                                            → list of every workspace
//	GET /workspaces/<ws>/                                       → instances under that workspace
//	GET /workspaces/<ws>/<inst>/changes/                        → changes in that instance
//	GET /workspaces/<ws>/<inst>/changes/<change-id>             → per-change render
//
// The per-change handler composes the changestore mutator path with the
// dual-target event emitter and the renderer. The state transition is
// idempotent under the per-change flock: a re-arrival on an already
// in-review change is a no-op write, but the change_engaged audit event
// still fires once per HTTP hit (R5).
//
// Per-instance audit isolation: each instance owns its own
// mcp-audit.log. The Handlers consults SinkFor (config.WorkspaceInstance
// → mcp.AuditSink) for the originating instance whenever it emits, so
// federated changes log their events alongside their data.
//
// The package depends on internal/mcp only through the exported types
// and helpers; no internal/mcp unexported symbol is touched, so the
// audit/state machinery remains the single owner of write discipline.
package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/web/render"
)

// diffPatchFileName names the per-change unified-diff snapshot the
// niwa_create_change MCP handler writes alongside state.json. Mirrors
// the constant in internal/mcp/handlers_change.go; duplicated here so a
// future rename in the mcp package does not silently retarget what the
// surface serves.
const diffPatchFileName = "diff.patch"

// uuidV4Re validates the {id} path parameter on per-change routes.
// Independent of internal/mcp's uuidV4Regex so the web package does not
// reach into mcp's unexported symbols. The shape is identical: the
// 8-4-4-4-12 UUIDv4 layout with the version nibble fixed at 4 and the
// variant nibble in [89ab].
var uuidV4Re = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// Handlers carries the dependencies for the machine-level HTTP handlers.
// Instances is the full list of (workspace, instance, root) endpoints
// the surface serves; SinkFor lets per-instance audit emission compose
// without the handlers reaching into the audit constructor directly.
type Handlers struct {
	Instances []config.WorkspaceInstance
	SinkFor   func(instanceRoot string) mcp.AuditSink
}

// RegisterRoutes wires the F5 GET routes onto mux using Go 1.22+
// method+path patterns. The `{$}` end-anchors on the index routes keep
// `/workspaces/foo/extra` from silently matching `/workspaces/{ws}/{$}`.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /workspaces/{$}", h.handleWorkspaces)
	mux.HandleFunc("GET /workspaces/{workspace}/{$}", h.handleWorkspace)
	mux.HandleFunc("GET /workspaces/{workspace}/{instance}/changes/{$}", h.handleInstanceIndex)
	mux.HandleFunc("GET /workspaces/{workspace}/{instance}/changes/{id}", h.handleChange)
}

// handleRoot redirects the bare root URL to /workspaces/. 302 (Found)
// rather than 301 (Permanent) because the surface URL space is fluid
// across F5/F10 and an aggressive browser cache of 301 would be a
// regression hazard.
func (h *Handlers) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/workspaces/", http.StatusFound)
}

// handleWorkspaces lists every workspace served by this surface. Each
// row links into `/workspaces/<ws>/`. Sorted by workspace name (the
// natural order out of EnumerateInstances).
func (h *Handlers) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	seen := make(map[string]int, len(h.Instances))
	order := make([]string, 0, len(h.Instances))
	for _, inst := range h.Instances {
		if _, ok := seen[inst.Workspace]; !ok {
			order = append(order, inst.Workspace)
		}
		seen[inst.Workspace]++
	}
	rows := make([]render.WorkspaceRow, 0, len(order))
	for _, name := range order {
		rows = append(rows, render.WorkspaceRow{
			Name:          name,
			InstanceCount: seen[name],
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderWorkspaces(w, render.WorkspacesData{Workspaces: rows})
}

// handleWorkspace lists the instances under one workspace. 404 if the
// workspace name does not appear in the federated instance list.
func (h *Handlers) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	wsName := r.PathValue("workspace")
	rows := make([]render.InstanceRow, 0)
	for _, inst := range h.Instances {
		if inst.Workspace == wsName {
			rows = append(rows, render.InstanceRow{
				Workspace: inst.Workspace,
				Instance:  inst.Instance,
				Root:      inst.Root,
			})
		}
	}
	if len(rows) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderWorkspace(w, render.WorkspaceData{
		Workspace: wsName,
		Instances: rows,
	})
}

// handleInstanceIndex lists pending + in-review + cleaned changes for
// one (workspace, instance) and emits one review_surface_opened audit
// event per HTTP hit. The event lands in the originating instance's
// mcp-audit.log via the SinkFor factory.
//
// The verdict-cast state is reserved for F10 and never written by F5
// code; if present on disk (from a future writer), it is excluded from
// the index here because the F5 index acceptance criteria scope
// excludes it.
func (h *Handlers) handleInstanceIndex(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.lookupInstance(r.PathValue("workspace"), r.PathValue("instance"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	summaries := h.scanInstanceIndex(inst.Root)

	// Best-effort audit emit. R4/D5: a failed audit emit does not fail
	// the request.
	_ = mcp.AppendChangeEvent(inst.Root, h.sinkFor(inst.Root), mcp.ChangeEvent{
		Kind:    mcp.ChangeEventSurfaceOpened,
		Payload: map[string]any{},
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderIndex(w, render.IndexData{
		Workspace: inst.Workspace,
		Instance:  inst.Instance,
		Changes:   summaries,
	})
}

// handleChange serves the per-change page. Pipeline (PRD R6, R5):
//
//  1. Look up the instance from the {workspace} / {instance} path
//     params; 404 if not registered.
//  2. UUIDv4-validate the {id} path param; 404 on malformed.
//  3. Read state via mcp.Read; 404 on miss.
//  4. If state == pending, advance to in-review via UpdateChangeState
//     (idempotent under the per-change flock — concurrent first hits
//     produce one write).
//  5. Read diff.patch.
//  6. Emit change_engaged via AppendChangeEvent (per HTTP hit; emitted
//     unconditionally of the state transition, so two hits → two
//     events).
//  7. Render via RenderChange.
//
// The post-update re-read picks up the new state and the UpdatedAt
// stamp from UpdateChangeState so the rendered page reflects the
// freshly-recorded transition. A failed re-read falls back to the
// pre-update state — the change_engaged event still fires.
func (h *Handlers) handleChange(w http.ResponseWriter, r *http.Request) {
	inst, ok := h.lookupInstance(r.PathValue("workspace"), r.PathValue("instance"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if !uuidV4Re.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	st, err := mcp.Read(inst.Root, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if st.State == mcp.ChangeStatePending {
		_ = mcp.UpdateChangeState(inst.Root, id,
			func(cur *mcp.ChangeState) (*mcp.ChangeState, error) {
				if cur.State != mcp.ChangeStatePending {
					// Idempotent: a second hit raced inside the lock,
					// the first already advanced — no-op write.
					return nil, nil
				}
				next := *cur
				next.State = mcp.ChangeStateInReview
				return &next, nil
			})
		// Re-read so the rendered page shows the post-transition state
		// and UpdatedAt. A failure here is non-fatal: we still have the
		// pre-update state and can render that.
		if updated, rerr := mcp.Read(inst.Root, id); rerr == nil {
			st = updated
		}
	}

	dir, derr := mcp.ChangeDir(inst.Root, id)
	var diffBytes []byte
	if derr == nil {
		name := st.DiffPath
		if name == "" {
			name = diffPatchFileName
		}
		// If DiffPath is absolute or escapes the change dir, fall back
		// to the conventional filename inside the change dir. This is
		// defence-in-depth: a corrupt state.json must not let an
		// HTTP GET read an arbitrary file.
		if filepath.IsAbs(name) || filepath.Dir(name) != "." {
			name = diffPatchFileName
		}
		path := filepath.Join(dir, name)
		data, rerr := os.ReadFile(path)
		switch {
		case rerr == nil:
			diffBytes = data
		case errors.Is(rerr, os.ErrNotExist):
			// Missing diff.patch is non-fatal — the renderer shows the
			// "No changes." body for an empty Diff field.
		default:
			// Other IO errors (permission, IO failure): same fallback,
			// the operator sees a page with no diff body rather than a
			// 500.
		}
	}

	_ = mcp.AppendChangeEvent(inst.Root, h.sinkFor(inst.Root), mcp.ChangeEvent{
		Kind:     mcp.ChangeEventEngaged,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id},
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderChange(w, render.ChangeData{
		Workspace:          inst.Workspace,
		Instance:           inst.Instance,
		ID:                 st.ID,
		State:              st.State,
		OriginatingSession: st.OriginatingSession,
		BaseRef:            st.BaseRef,
		HeadRef:            st.HeadRef,
		Branch:             st.Branch,
		CreatedAt:          st.CreatedAt,
		UpdatedAt:          st.UpdatedAt,
		Diff:               string(diffBytes),
	})
}

// lookupInstance returns the WorkspaceInstance matching the URL path
// parameters. Linear scan is fine for the workspace counts niwa targets
// (low tens); switching to a map keyed by `workspace+"/"+instance`
// would optimise a non-bottleneck.
func (h *Handlers) lookupInstance(workspace, instance string) (config.WorkspaceInstance, bool) {
	for _, inst := range h.Instances {
		if inst.Workspace == workspace && inst.Instance == instance {
			return inst, true
		}
	}
	return config.WorkspaceInstance{}, false
}

// sinkFor returns the audit sink for instanceRoot, using the factory
// supplied in Config or a nil sink as the fallback. A nil sink elides
// audit emission cleanly because AppendChangeEvent is nil-safe on the
// sink side.
func (h *Handlers) sinkFor(instanceRoot string) mcp.AuditSink {
	if h.SinkFor == nil {
		return nil
	}
	return h.SinkFor(instanceRoot)
}

// scanInstanceIndex enumerates an instance's `.niwa/changes/` and
// returns one summary per pending / in-review / cleaned change, sorted
// by UpdatedAt desc. The renderer applies a stable secondary sort that
// moves cleaned entries to the end of the list.
//
// Corrupt or unreadable change directories are skipped silently — a
// partial index is preferred over a 500 that hides every healthy
// change. Non-directory entries (e.g. `.session-<sid>.create.lock`
// from handlers_change.go's per-session create lock) are filtered out
// because they fail the IsDir check.
func (h *Handlers) scanInstanceIndex(instanceRoot string) []render.ChangeSummary {
	changesDir := mcp.ChangesDir(instanceRoot)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		return nil
	}
	out := make([]render.ChangeSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := mcp.Read(instanceRoot, e.Name())
		if err != nil {
			continue
		}
		switch st.State {
		case mcp.ChangeStatePending,
			mcp.ChangeStateInReview,
			mcp.ChangeStateCleaned:
			out = append(out, render.ChangeSummary{
				ID:        st.ID,
				State:     st.State,
				UpdatedAt: st.UpdatedAt,
			})
		default:
			// verdict-cast (F10) and any future state: excluded from
			// the F5 index per the acceptance criteria.
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}
