package workspace

// NoticeIDRank2TeamConfig is the DisclosedNotices key for the rank-2
// deprecation notice emitted when a workspace's team config source
// resolves to the legacy whole-repo (rank-2) layout. Once recorded
// in InstanceState.DisclosedNotices, subsequent applies suppress the
// notice for that workspace instance.
const NoticeIDRank2TeamConfig = "rank2-deprecation:team-config"

// NoticeIDRank2Overlay is the same as NoticeIDRank2TeamConfig but
// for the workspace's auto-discovered personal overlay.
const NoticeIDRank2Overlay = "rank2-deprecation:overlay"

// NoticeIDPluginInstalled records that niwa successfully auto-installed
// (or kept up-to-date) the embedded niwa Claude Code plugin during this
// apply.
const NoticeIDPluginInstalled = "plugin-installed:niwa"

// NoticeIDPluginSkipped records that niwa elected to skip auto-installing
// the embedded plugin during this apply — either because the user opted
// out via --no-install-plugins or `auto_install_plugins = false`, or
// because a filesystem error prevented the install. In both cases the
// reporter line includes a manual-install command the user can run.
const NoticeIDPluginSkipped = "plugin-install-skipped:niwa"

// EmitPluginNotice emits a notice about the embedded niwa plugin:
// either that it was installed/up-to-date or that the install was
// skipped. When the install is skipped (or failed) the manualCmd
// argument is interpolated into the reporter line so the user has
// a copy-paste command to install the plugin manually.
//
// Callers own the once-per-workspace dedup contract (same shape as
// EmitRank2Notice). reporter may be nil; in that case the function
// is a no-op.
func EmitPluginNotice(id, manualCmd string, reporter *Reporter) {
	if reporter == nil {
		return
	}
	switch id {
	case NoticeIDPluginInstalled:
		reporter.Log("note: niwa Claude Code plugin installed at ~/.claude/plugins/marketplaces/niwa/. Use /niwa:migrate-config to invoke the migration skill.")
	case NoticeIDPluginSkipped:
		reporter.Log("note: niwa Claude Code plugin install skipped. To install manually, run: %s", manualCmd)
	}
}

// EmitRank2Notice emits the PRD R10 rank-2 deprecation notice for
// the given source identifier. The notice tells the user the
// source still uses the deprecated whole-repo layout and points
// them at the /niwa:migrate-config skill for assistance.
//
// Callers own the once-per-workspace dedup contract: they check
// InstanceState.DisclosedNotices before invoking, and append the
// id to the next-saved state after this call returns. The id
// argument is what the caller will record in DisclosedNotices —
// EmitRank2Notice does not touch state itself.
//
// reporter may be nil for callers that don't have one wired yet;
// in that case the function is a no-op.
func EmitRank2Notice(id, identifier string, reporter *Reporter) {
	if reporter == nil {
		return
	}
	reporter.Log("note: source %s uses the deprecated rank-2 layout (workspace.toml at repo root). Run /niwa:migrate-config to migrate the source to the rank-1 layout (.niwa/workspace.toml).", identifier)
	_ = id // surfaced for symmetry with EmitPluginNotice; recorded by caller.
}
