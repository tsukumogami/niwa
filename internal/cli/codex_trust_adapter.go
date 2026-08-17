package cli

import "github.com/tsukumogami/niwa/internal/workspace"

// configureCodexTrust wires the Codex trust writer onto an Applier.
//
// The write it enables lands in the developer's own Codex config, outside any
// niwa instance, so the Applier leaves the seam nil by default: the unit
// suites build Appliers by the dozen against temp directories, and a default
// that reached the developer's home would have every one of them edit it.
// Every CLI surface that constructs an Applier calls this helper, so a real
// `niwa create` or `niwa apply` always writes the entries a Codex session
// needs to be able to write files at all.
func configureCodexTrust(applier *workspace.Applier) {
	applier.EnsureCodexTrust = workspace.EnsureCodexTrust
}
