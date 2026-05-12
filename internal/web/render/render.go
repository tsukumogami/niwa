// Package render owns the niwa surface's HTML output. Templates are
// embedded at compile time (no runtime filesystem reads) and parsed
// once at package init in templates.go. The styles.css file is also
// embedded and injected into each page via the {{.CSS}} pipeline.
//
// html/template's contextual escaping (HTML body, attribute, URL) is
// the load-bearing guarantee for NFR4 — branch names, session IDs, and
// diff bodies all flow through the auto-escape path. Tests assert this
// by feeding a <script>alert(1)</script> diff and checking the rendered
// output contains the literal `&lt;script&gt;` sequence.
package render

import (
	"html/template"
	"io"
	"sort"
)

// ChangeData is the data contract for the per-change page. Every field
// is rendered through html/template's auto-escape, so callers may pass
// arbitrary user-supplied strings without manual escaping.
type ChangeData struct {
	ID                  string
	State               string
	OriginatingSessions []string
	BaseRef             string
	HeadRef             string
	Branch              string
	CreatedAt           string
	UpdatedAt           string
	// Diff is the unified-diff body. It is wrapped in <pre> so leading
	// whitespace is preserved; HTML-active characters (`<`, `>`, `&`,
	// `"`) are escaped by html/template, which is the NFR4 invariant.
	Diff string
	// CSS is the embedded stylesheet injected into the page <head>.
	// Populated by RenderChange so callers do not have to thread it.
	CSS template.CSS
}

// IndexData is the data contract for the listing page.
type IndexData struct {
	Changes []ChangeSummary
	CSS     template.CSS
}

// ChangeSummary is one row in the index. State is one of the
// ChangeState constants from internal/mcp; the template applies a
// `cleaned` class to the <li> for state == "cleaned".
type ChangeSummary struct {
	ID        string
	State     string
	UpdatedAt string
}

// RenderChange writes the per-change HTML page to w. The CSS pipeline
// is set from the embedded stylesheet on every call so the template
// author does not have to remember it.
func RenderChange(w io.Writer, data ChangeData) error {
	data.CSS = template.CSS(stylesCSS)
	return changeTmpl.Execute(w, data)
}

// RenderIndex writes the index page to w. The renderer sorts the
// supplied changes such that non-cleaned ones precede cleaned ones at
// equal timestamps; within each group it preserves the caller's order
// (handlers are expected to pre-sort by updated_at desc). This is the
// "cleaned changes appear after non-cleaned for identical timestamps"
// contract from the F5 acceptance criteria.
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
