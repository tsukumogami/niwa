package cli

import (
	"os"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// configureDeveloperHome points an applier at the developer's own home
// directory, which is what lets its procedure-routed deliveries write outside
// the instance -- today, the per-directory trust entry a Codex session needs
// before it can write anything at all.
//
// The Applier leaves the field empty by default and skips those deliveries
// while it is: the unit suites build Appliers by the dozen against temp
// directories, and a default that resolved the real home would have every one
// of them edit it. Every CLI surface that constructs an Applier calls this, so
// a real `niwa create` or `niwa apply` delivers what a session needs.
//
// A home that cannot be resolved leaves the field empty rather than failing the
// command. That is the same posture the writer itself takes toward a config it
// cannot read: a machine with no resolvable home still gets its repositories
// cloned and its files materialized.
func configureDeveloperHome(applier *workspace.Applier) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	applier.DeveloperHome = home
}
