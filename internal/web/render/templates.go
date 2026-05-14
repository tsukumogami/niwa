package render

import (
	"embed"
	"html/template"
)

// templateFS holds the two HTML templates (change.tmpl, index.tmpl)
// at compile time. stylesCSS holds the stylesheet bytes as a string so
// it can flow directly into html/template's template.CSS pipeline.
//
// The //go:embed directives must sit on top-level var declarations in
// the same package as the embed import; templates.go centralises both.
//
//go:embed templates/*.tmpl
var templateFS embed.FS

//go:embed styles.css
var stylesCSS string

// Templates are parsed once at package init. A parse failure panics at
// process start so a misshapen template is a deploy-time error, not a
// request-time error.
var (
	workspacesTmpl = template.Must(template.ParseFS(templateFS, "templates/workspaces.tmpl"))
	workspaceTmpl  = template.Must(template.ParseFS(templateFS, "templates/workspace.tmpl"))
	indexTmpl      = template.Must(template.ParseFS(templateFS, "templates/index.tmpl"))
	changeTmpl     = template.Must(template.ParseFS(templateFS, "templates/change.tmpl"))
)

// CSS exposes the embedded stylesheet so callers can serve it directly
// (e.g. for inspection from tests) without round-tripping through a
// template.
func CSS() string { return stylesCSS }
