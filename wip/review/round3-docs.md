# Round 3 docs review — remote-control by default on dispatched workers

HEAD e76800f. Reviewed: BRIEF, PRD, DESIGN, PLAN, guide, SPIKE.

## Writing-style validator (shirabe v0.13.0, `validate --format human --visibility ""`)

| Doc | Result |
|-----|--------|
| BRIEF-remote-control-by-default.md | All checks passed |
| PRD-remote-control-by-default.md | All checks passed |
| DESIGN-remote-control-by-default.md | All checks passed |
| PLAN-remote-control-by-default.md | 1 notice FC14 (see below), 0 errors |
| guides/remote-control-on-dispatch.md | All checks passed |
| SPIKE-remote-control-by-default.md | All checks passed |

**No FC10 (banned word) notices on any doc.** A manual grep for the full banned
list (tier/robust/comprehensive/holistic/leverage/utilize/facilitate/delve/
seamless/etc.) and for overused phrases (in order to / prior to / it's worth
noting / due to the fact) also returned nothing across all six docs.

## Findings

### NON-BLOCKING — PLAN FC14 notice (docs/plans/PLAN-remote-control-by-default.md:1)
Validator: `execution_mode is 'single-pr' but '## Dependency Graph' is populated`.
Not blocking: the PLAN is deleted at finalization under the single-pr shirabe
contract, so the notice never reaches the merged tree. Per the review brief, the
PLAN's removal itself is expected and not flagged. Mentioned only for completeness.

### NON-BLOCKING — em-dash density (docs/designs/current/DESIGN-remote-control-by-default.md)
21 ` -- ` em-dashes in the DESIGN (guide 5, BRIEF 10, PRD 7). The writing-style
guide says use them sparingly, but this is established house style across the
whole docs/ tree and the validator does not flag it. No action needed.

## Accuracy checks (docs vs. implemented code on this branch) — all pass

- Host field name/TOML key: `RemoteControlOnDispatch *bool`,
  `remote_control_on_dispatch` — matches guide, PRD R1, DESIGN
  (internal/config/registry.go:36).
- Config path `~/.config/niwa/config.toml` with `$XDG_CONFIG_HOME` fallback —
  matches code (internal/config/registry.go:152-160).
- Downstream override `[claude.settings].remoteControlAtStartup` materializes and
  is read back — matches (internal/workspace/materialize.go:425,
  internal/cli/dispatch_plugins.go:143).
- Guide's "values in [claude.settings] are written as quoted strings" + the
  `remoteControlAtStartup = "false"` example — accurate: the value is a
  MaybeSecret string parsed to bool, error message `want "true" or "false"`
  (internal/workspace/materialize.go:429).
- Injected flag `{"remoteControlAtStartup":true}` as two discrete argv elements —
  matches (internal/cli/dispatch.go:235, dispatch_remotecontrol.go:16).
- The eligibility warning the guide quotes is byte-for-byte the printed output:
  the const (dispatch_remotecontrol.go:20) plus the `niwa dispatch: ` prefix added
  at the print site (dispatch.go:232). The guide block includes that exact prefix.
- Guide's note that setting the key to `"true"` "applies wherever you set it, not
  just to dispatch" is correct: the materialized settings.json is loaded by all
  session types, so an explicit downstream value is not dispatch-scoped — distinct
  from niwa's dispatch-only default. Useful, non-obvious caveat, stated accurately.

## Clarity / usefulness

The guide is self-contained and genuinely useful: enable (host), turn off
(workspace/instance), eligibility. The one real trap — unquoted bool in `[global]`
vs. quoted string in `[claude.settings]` — is called out inline, so a reader
copy-pasting won't get a config error. No contradictions found across BRIEF / PRD /
DESIGN / SPIKE (the spike's Variant A + Variant C findings line up with every
downstream "default-fill, not override" claim).

## Verdict

No banned words, no factual errors, no confusing passages that block. The single
FC14 notice is on a file that is removed at finalization.

BLOCKING COUNT: 0
