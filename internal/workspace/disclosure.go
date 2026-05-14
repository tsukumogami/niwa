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

// EmitRank2Notice emits the one-time PRD R10 rank-2 deprecation
// notice for the given source identifier. The notice tells the user
// the source still uses the deprecated whole-repo layout and points
// them at the /niwa:migrate-config skill for assistance.
//
// Idempotent: when state already contains id in DisclosedNotices the
// function returns without logging or mutating state. On first call
// it both writes to reporter (a `note:` line on stderr) and appends
// id to state.DisclosedNotices so the next apply suppresses the
// repeat.
//
// state may be nil (e.g. fresh init before InstanceState is loaded);
// in that case the notice is always emitted but no DisclosedNotices
// bookkeeping happens — callers that need idempotence across runs
// must thread state through.
//
// reporter may be nil for callers that don't have one yet; in that
// case the function is a no-op (no log, no state mutation), which
// preserves the contract that EmitRank2Notice never panics on
// partially-wired call sites.
func EmitRank2Notice(state *InstanceState, id, identifier string, reporter *Reporter) {
	if reporter == nil {
		return
	}
	if noticeDisclosed(state, id) {
		return
	}
	reporter.Log("note: source %s uses the deprecated rank-2 layout (workspace.toml at repo root). Run /niwa:migrate-config to migrate the source to the rank-1 layout (.niwa/workspace.toml).", identifier)
	if state != nil {
		state.DisclosedNotices = append(state.DisclosedNotices, id)
	}
}
