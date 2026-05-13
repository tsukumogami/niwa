// Package render owns the niwa surface's HTML output. Templates are
// embedded at compile time (no runtime filesystem reads) and parsed
// once at package init in templates.go. The styles.css file is also
// embedded and injected into each page via the {{.CSS}} pipeline.
//
// html/template's contextual escaping (HTML body, attribute, URL) is
// the load-bearing guarantee for NFR4 — workspace names, instance
// names, branch names, session IDs, and diff bodies all flow through
// the auto-escape path. Tests assert this by feeding a
// <script>alert(1)</script> diff and checking the rendered output
// contains the literal `&lt;script&gt;` sequence.
package render

import (
	"html/template"
	"io"
	"sort"
)

// WorkspacesData is the data contract for the top-level /workspaces/
// listing — one row per registered workspace.
type WorkspacesData struct {
	Workspaces []WorkspaceRow
	CSS        template.CSS
}

// WorkspaceRow summarises one workspace on the top-level page.
// InstanceCount is the federated instance count (workspace root +
// sibling instance directories).
type WorkspaceRow struct {
	Name          string
	InstanceCount int
}

// WorkspaceData is the data contract for /workspaces/<ws>/ — the
// listing of instances under one workspace.
type WorkspaceData struct {
	Workspace string
	Instances []InstanceRow
	CSS       template.CSS
}

// InstanceRow summarises one instance on the per-workspace page. Root
// is shown for operator orientation (the instance directory on disk).
type InstanceRow struct {
	Workspace string
	Instance  string
	Root      string
}

// IndexData is the data contract for /workspaces/<ws>/<inst>/changes/.
// Workspace + Instance head the page; Changes is the per-instance
// listing the handler scanned out of `.niwa/changes/`.
type IndexData struct {
	Workspace string
	Instance  string
	Changes   []ChangeSummary
	CSS       template.CSS
}

// ChangeSummary is one row in a per-instance index. State is one of
// the ChangeState constants from internal/mcp; the template applies a
// `cleaned` class to the <li> for state == "cleaned".
type ChangeSummary struct {
	ID        string
	State     string
	UpdatedAt string
}

// ChangeData is the data contract for the per-change page
// (/workspaces/<ws>/<inst>/changes/<id>). Every field is rendered
// through html/template's auto-escape, so callers may pass arbitrary
// user-supplied strings without manual escaping. Workspace + Instance
// drive breadcrumbs back up the URL tree.
type ChangeData struct {
	Workspace          string
	Instance           string
	ID                 string
	State              string
	OriginatingSession string
	BaseRef            string
	HeadRef            string
	Branch             string
	CreatedAt          string
	UpdatedAt          string
	// Diff is the unified-diff body. It is wrapped in <pre> so leading
	// whitespace is preserved; HTML-active characters (`<`, `>`, `&`,
	// `"`) are escaped by html/template, which is the NFR4 invariant.
	Diff string
	// CSS is the embedded stylesheet injected into the page <head>.
	// Populated by RenderChange so callers do not have to thread it.
	CSS template.CSS
}

// RenderWorkspaces writes the top-level workspace listing.
func RenderWorkspaces(w io.Writer, data WorkspacesData) error {
	data.CSS = template.CSS(stylesCSS)
	return workspacesTmpl.Execute(w, data)
}

// RenderWorkspace writes the per-workspace instance listing.
func RenderWorkspace(w io.Writer, data WorkspaceData) error {
	data.CSS = template.CSS(stylesCSS)
	return workspaceTmpl.Execute(w, data)
}

// RenderIndex writes the per-instance change index. The renderer sorts
// the supplied changes such that non-cleaned ones precede cleaned ones
// at equal timestamps; within each group it preserves the caller's
// order (handlers are expected to pre-sort by updated_at desc). This
// is the "cleaned changes appear after non-cleaned for identical
// timestamps" contract from the F5 acceptance criteria.
func RenderIndex(w io.Writer, data IndexData) error {
	data.CSS = template.CSS(stylesCSS)
	sort.SliceStable(data.Changes, func(i, j int) bool {
		ci := data.Changes[i].State == "cleaned"
		cj := data.Changes[j].State == "cleaned"
		if ci != cj {
			return !ci // non-cleaned first
		}
		return false // stable: keep caller order within each subset
	})
	return indexTmpl.Execute(w, data)
}

// RenderChange writes the per-change HTML page to w. The CSS pipeline
// is set from the embedded stylesheet on every call so the template
// author does not have to remember it.
func RenderChange(w io.Writer, data ChangeData) error {
	data.CSS = template.CSS(stylesCSS)
	return changeTmpl.Execute(w, data)
}
