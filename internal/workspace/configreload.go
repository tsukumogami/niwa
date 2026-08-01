package workspace

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// ReconcileAndReloadConfig refreshes the workspace-root config snapshot from
// its source and reloads it, returning the config that should drive
// materialization: the one this run just pulled, not the one that happened to
// be on disk when the command started.
//
// Every command that materializes a workspace reads its config first and hands
// that value down. EnsureConfigSnapshotWithStatus then runs again deeper in the
// stack (Applier.Apply, Applier.Create) and can swap the whole config dir for
// freshly fetched upstream content -- but nothing reloads the already-loaded
// value, so materialization keeps running off the pre-swap read, and a setting
// or env key added upstream since that read lands one run late (issues #214,
// #227). Call this before the config drives materialization, and before
// workspace-name and agent resolution too, so those read what materialization
// reads.
//
// A caller may still consume its pre-reconcile read for something that must
// precede the fetch. The apply command does: its --force source-URL-change gate
// exists to refuse before any sync touches the config dir, so it runs first, on
// the earlier read. That ordering is all it shares with this function -- the
// re-materialization that gate's contract describes is not wired here or
// anywhere on the apply path, because refreshSnapshot refetches from the source
// recorded in the provenance marker and never consults the registry's URL.
//
// Worktrees deliberately do not call this. Under the inherit model a worktree
// is a derived view of its instance, so converging one must not advance it past
// the instance it belongs to; converge the instance instead.
//
// Gated on an existing provenance marker so it touches only workspaces already
// in the snapshot model. It must not pre-empt the case-2 legacy working-tree
// conversion, whose one-time "manual edits will not persist" notice is emitted
// from the converted flag EnsureConfigSnapshotWithStatus returns to the applier
// -- if this converted the dir first, that notice would never fire. The cost is
// that the single conversion run itself materializes from the pre-conversion
// read; it self-heals on the next command, once the marker exists.
//
// Case 3 -- a workspace with a registered source but no marker -- is classified
// local-only and still never reconciles. That is issue #215, which needs its
// own change. A true local-only workspace has no source to track and is
// correctly left alone.
//
// current is returned unchanged when there is nothing to reconcile, so callers
// can report load warnings once against whichever config ends up effective. The
// config dir is derived from configPath rather than passed alongside it, so a
// caller cannot supply a mismatched pair.
func ReconcileAndReloadConfig(
	ctx context.Context,
	configPath string,
	fetcher FetchClient,
	reporter *Reporter,
	current *config.ParseResult,
) (*config.ParseResult, error) {
	configDir := filepath.Dir(configPath)
	if !provenanceMarkerExists(configDir) {
		return current, nil
	}
	if _, _, snapErr := EnsureConfigSnapshotWithStatus(
		ctx, configDir, config.TeamConfigMarkerSet(), fetcher, reporter,
	); snapErr != nil {
		return nil, fmt.Errorf("reconciling workspace-root config from source: %w", snapErr)
	}
	// Reload so the freshly-swapped workspace.toml is what drives the caller,
	// not the stale snapshot it read before the swap.
	return config.Load(configPath)
}
