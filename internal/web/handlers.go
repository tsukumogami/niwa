// handlers.go implements the three F5 GET endpoints for the per-instance
// review surface:
//
//	GET /            → 302 /changes/
//	GET /changes/    → index of pending + in-review + cleaned changes
//	GET /changes/<id> → per-change page, advances pending → in-review
//
// The per-change handler composes the changestore mutator path (#3) with
// the dual-target event emitter (#4) and the renderer (#7). The state
// transition is idempotent under the per-change flock: a re-arrival on
// an already in-review change is a no-op write, but the change_engaged
// audit event still fires once per HTTP hit (R5).
//
// The package depends on internal/mcp only through the exported types
// (AuditSink, ChangeState, ChangeMutator) and the public helpers (Read,
// UpdateChangeState, ChangesDir, ChangeDir, AppendChangeEvent). No
// internal/mcp unexported symbol is touched, so the audit/state machinery
// remains the single owner of write discipline.
package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/web/render"
)

// diffPatchFileName names the per-change unified-diff snapshot the
// niwa_create_change MCP handler writes alongside state.json. Mirrors
// the constant in internal/mcp/handlers_change.go; duplicated here so a
// future rename in the mcp package does not silently retarget what the
// surface serves.
const diffPatchFileName = "diff.patch"

// uuidV4Re validates the {id} path parameter on GET /changes/<id>.
// Independent of internal/mcp's uuidV4Regex so the web package does not
// reach into mcp's unexported symbols. The shape is identical: the
// 8-4-4-4-12 UUIDv4 layout with the version nibble fixed at 4 and the
// variant nibble in [89ab].
var uuidV4Re = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// Handlers carries the dependencies for the per-instance HTTP handlers.
// InstanceRoot is the absolute path to the niwa instance whose
// `.niwa/changes/` directory is served. Sink receives the
// review_surface_opened and change_engaged audit events; a nil Sink
// elides audit emission (the per-change transitions.log writes still
// run via AppendChangeEvent).
type Handlers struct {
	InstanceRoot string
	Sink         mcp.AuditSink
}

// RegisterRoutes wires the three F5 GET routes onto mux using Go 1.22+
// method+path patterns. The patterns use `{$}` end-anchors on the index
// routes so `/changes/extra` does not silently match `/changes/{$}`.
func (h *Handlers) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.handleRoot)
	mux.HandleFunc("GET /changes/{$}", h.handleIndex)
	mux.HandleFunc("GET /changes/{id}", h.handleChange)
}

// handleRoot redirects the bare root URL to /changes/. 302 (Found)
// rather than 301 (Permanent) because the surface URL space is fluid
// across F5/F10 and an aggressive browser cache of 301 would be a
// regression hazard.
func (h *Handlers) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/changes/", http.StatusFound)
}

// handleIndex lists pending + in-review + cleaned changes and emits one
// review_surface_opened audit event per HTTP hit. The renderer
// (RenderIndex) sorts cleaned to the end of the list with a stable
// secondary order, so the handler only has to sort by UpdatedAt desc.
//
// The verdict-cast state is reserved for F10 and never written by F5
// code; if present on disk (from a future writer), it is excluded from
// the index here because the F5 index acceptance criteria scope
// excludes it.
func (h *Handlers) handleIndex(w http.ResponseWriter, r *http.Request) {
	summaries := h.scanIndex()

	// Best-effort audit emit. R4/D5: a failed audit emit does not fail
	// the request.
	_ = mcp.AppendChangeEvent(h.InstanceRoot, h.Sink, mcp.ChangeEvent{
		Kind:    mcp.ChangeEventSurfaceOpened,
		Payload: map[string]any{},
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderIndex(w, render.IndexData{Changes: summaries})
}

// handleChange serves the per-change page. Pipeline (PRD R6, R5):
//
//  1. UUIDv4-validate the path param; 404 on malformed.
//  2. Read state via mcp.Read; 404 on miss.
//  3. If state == pending, advance to in-review via UpdateChangeState
//     (idempotent under the per-change flock — concurrent first hits
//     produce one write).
//  4. Read diff.patch.
//  5. Emit change_engaged via AppendChangeEvent (per HTTP hit; emitted
//     unconditionally of the state transition, so two hits → two
//     events).
//  6. Render via RenderChange.
//
// The post-update re-read picks up the new state and the UpdatedAt
// stamp from UpdateChangeState so the rendered page reflects the
// freshly-recorded transition. A failed re-read falls back to the
// pre-update state — the change_engaged event still fires.
func (h *Handlers) handleChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidV4Re.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	st, err := mcp.Read(h.InstanceRoot, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if st.State == mcp.ChangeStatePending {
		_ = mcp.UpdateChangeState(h.InstanceRoot, id,
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
		// and UpdatedAt. A failure here is non-fatal: we still have
		// the pre-update state and can render that.
		if updated, rerr := mcp.Read(h.InstanceRoot, id); rerr == nil {
			st = updated
		}
	}

	dir, derr := mcp.ChangeDir(h.InstanceRoot, id)
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

	_ = mcp.AppendChangeEvent(h.InstanceRoot, h.Sink, mcp.ChangeEvent{
		Kind:     mcp.ChangeEventEngaged,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id},
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = render.RenderChange(w, render.ChangeData{
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

// scanIndex enumerates `.niwa/changes/` and returns one summary per
// pending / in-review / cleaned change, sorted by UpdatedAt desc. The
// renderer applies a stable secondary sort that moves cleaned entries
// to the end of the list.
//
// Corrupt or unreadable change directories are skipped silently — a
// partial index is preferred over a 500 that hides every healthy
// change. Non-directory entries (e.g. `.session-<sid>.create.lock`
// from handlers_change.go's per-session create lock) are filtered out
// because they fail the IsDir check.
func (h *Handlers) scanIndex() []render.ChangeSummary {
	changesDir := mcp.ChangesDir(h.InstanceRoot)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		return nil
	}
	out := make([]render.ChangeSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := mcp.Read(h.InstanceRoot, e.Name())
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
