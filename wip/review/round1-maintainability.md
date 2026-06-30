# Maintainability review — remote-control by default on dispatched workers (round 1)

Scope: `git diff main -- internal/ docs/`. Reviewed the resolver and its tests, the
dispatch wiring (step 9a), the settings-vocabulary plumbing (`buildSettingsDoc` +
`readInstanceSettings`), the host config field, the launcher env contract, and the
user-facing guide.

Overall: this is unusually well-commented code. Names are clear, the pure resolver is
a clean seam, and the default-fill semantics are explained at the point of use. One
cross-package contract is load-bearing for the feature's central guarantee yet is held
together only by prose and by two independent tests that can't catch its drift. That is
the single blocking item. The rest are nits.

---

## BLOCKING

### B1. The "downstream-off wins" guarantee rides on an untested cross-package string contract

The feature's headline promise — a downstream `remoteControlAtStartup = "false"` beats
the host default — depends on three sites agreeing on the exact JSON key spelling
`"remoteControlAtStartup"`, and they live in two different packages with nothing but
prose linking them:

- `internal/workspace/materialize.go:425,431` — `buildSettingsDoc` reads
  `cfg.Settings["remoteControlAtStartup"]` and emits `doc["remoteControlAtStartup"]`.
- `internal/cli/dispatch_plugins.go:143` — `instanceSettings.RemoteControlAtStartup`
  is read back via the struct tag `json:"remoteControlAtStartup"`.
- `internal/cli/dispatch_remotecontrol.go:13` — the injected literal
  `remoteControlSettingsJSON = {"remoteControlAtStartup":true}`.

The next developer who renames the key in one place (say, because Claude Code renames
the setting) and misses another gets a **silent** failure with the worst-possible blast
radius: `readInstanceSettings` sees the now-unmatched key as absent → resolver reads
`inst.RemoteControlAtStartup == nil` → treats downstream as "unset" → injects
`--settings`, which (per the spike's Variant C) outranks the worker's own
settings.json. The user's explicit `remoteControlAtStartup = "false"` opt-out is
silently overridden and the worker starts steerable against their wishes.

Why the tests don't catch it: the coverage is split so the two ends never meet.
`TestDispatch_RemoteControl_DownstreamOff_Wins`
(`dispatch_wiring_remotecontrol_test.go:94`) **hand-writes** the literal
`{"remoteControlAtStartup": false}` as the instance settings.json, so it exercises
read-back + resolver but never `buildSettingsDoc`.
`TestBuildSettingsDoc_RemoteControlAtStartup`
(`materialize_remotecontrol_test.go:9`) checks `buildSettingsDoc`'s own emitted literal
in isolation. Neither test runs the real path `[claude.settings]` → `buildSettingsDoc`
→ on-disk settings.json → `readInstanceSettings` → resolver, so a drift between the
emitted key and the read-back tag passes both suites green.

The prose mitigates *understanding* (the comments at `dispatch_plugins.go:141-143` and
`materialize.go:418-424` both describe the round-trip), so a careful reader won't be
confused. What's missing is a *mechanical* tie. Recommended fix, cheap and turns the
invisible contract into an enforced one:

1. Introduce one shared exported constant for the Claude key name (e.g.
   `config.RemoteControlAtStartupKey = "remoteControlAtStartup"`) and use it in
   `buildSettingsDoc`'s map lookups/emit. The struct tag and the injected JSON literal
   can't use a const directly, but referencing the same source-of-truth name in a
   doc-comment cross-link at each site narrows the surface.
2. Add one end-to-end round-trip test: set `[claude.settings].remoteControlAtStartup =
   "false"`, run `buildSettingsDoc`, write the doc to a temp instance, then
   `readInstanceSettings` it and assert `RemoteControlAtStartup` is non-nil and false.
   That single test would fail the instant the emit key and the read tag drift.

---

## NON-BLOCKING

### N1. Resolver's eligibility env and the launcher's worker env are the same value by convention only

`dispatch.go:226` passes `os.Environ()` to `resolveDispatchRemoteControl`, and
`dispatch_launcher.go:40` independently sets `cmd.Env = os.Environ()` on the worker.
The resolver's whole point is to predict whether the *worker's* environment forces
API-key auth, so its `env` argument must equal the launcher's `cmd.Env`. Today they
match because both call `os.Environ()` in separate files. If a future editor customizes
`realDispatchLaunch`'s `cmd.Env` (strips or injects vars — e.g. to scrub
`ANTHROPIC_API_KEY`), the resolver's warning would silently describe a different
environment than the worker actually gets. The resolver comment
(`dispatch_remotecontrol.go:51-52`) documents the expected shape but nothing enforces
the linkage. Consider computing the worker env once and passing the same slice to both,
or a comment at `dispatch_launcher.go:40` noting the resolver depends on this value.

### N2. Resolver truth-table comment promises a full cross product but lists a curated subset

`dispatch_remotecontrol_test.go:16` says "host {unset,false,true} × downstream
{nil,false,true} × API key {absent,present}" — reads as an exhaustive 3×3×2 = 18-cell
matrix, but the table has 10 cases. The omissions are genuinely redundant (the resolver
short-circuits: host off/unset returns early before downstream or API key matter;
downstream-decided returns early before API key matters), so the chosen cases are
representative, not lazy. But the "×" notation tells the next developer the matrix is
complete when it is curated. One line — "cases below are the representative cells; the
omitted combinations are unreachable distinctions because the resolver short-circuits on
host-off and on downstream-decided" — would stop a future editor from either trusting a
nonexistent cell or redundantly filling in all 18.

### N3. The full warning string is duplicated verbatim in the user guide and will silently go stale

`apiKeyForcedWarning` (`dispatch_remotecontrol.go:18`) is reproduced word-for-word in
`docs/guides/remote-control-on-dispatch.md:55`. The only test that asserts on it,
`TestDispatch_RemoteControl_APIKey_WarnsAndSkips`
(`dispatch_wiring_remotecontrol_test.go:147`), checks `strings.Contains(stderr,
"ANTHROPIC_API_KEY")` — a substring, not the full message. So an edit to the const
message leaves the doc quietly wrong and no test fails. Either soften the doc to
paraphrase rather than quote the exact line, or add a comment on the const noting the
guide reproduces it.

---

## Clear / no action needed (so the next reviewer doesn't re-litigate)

- **Naming distinction is well drawn.** `RemoteControlOnDispatch` (host preference,
  `registry.go:30-36`) vs `RemoteControlAtStartup` (the Claude key,
  `dispatch_plugins.go:141-143`) vs `remoteControlSettingsJSON` (the injected literal)
  are each documented at their declaration with the host-preference-vs-Claude-key
  framing spelled out. Not a source of confusion.
- **Default-fill-not-override is discoverable from the code, not just the design doc.**
  The resolver doc comment (`dispatch_remotecontrol.go:20-33`) and the step-9a comment
  (`dispatch.go:215-223`) both state the inject-only-when-downstream-unset rule and *why*
  (the `--settings` precedence trap). A reader doesn't need the design doc.
- **The quoted-string-bool requirement is documented where a user hits it.** The error
  message (`materialize.go:429`) names the accepted values, the materialize comment
  (`materialize.go:423-424`) explains why `SettingsConfig` forces quoting, and the guide
  (`remote-control-on-dispatch.md:35-36,39`) shows `remoteControlAtStartup = "false"`
  with an inline note. `TestBuildSettingsDoc_RemoteControlAtStartup_Invalid` pins the
  reject path.
- **The nil-instance contract is documented and tested.** `resolveDispatchRemoteControl`
  states nil == "downstream unset" (`dispatch_remotecontrol.go:33`) and
  `TestResolveDispatchRemoteControl_NilInstance` exercises it.
- **Test names match their assertions.** `DownstreamOff_Wins`, `HostUnset_NoChange`,
  `APIKey_WarnsAndSkips`, and the `apiKeyAuthForced` prefix-collision/empty cases all
  test what they claim.

---

BLOCKING COUNT: 1
