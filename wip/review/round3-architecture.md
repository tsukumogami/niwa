# Round 3 — Final Architecture + Security Review

Scope: remote-control-by-default on dispatched workers. HEAD e76800f.
Fresh skeptical pass focused on layering, the dispatch-only scoping invariant,
`--settings` argv safety, failure-mode degradation, and the step-9a
order-of-operations question.

## Verdict: zero blocking findings.

The two prior rounds' resolutions hold. The feature is structurally sound: one
shared key const in the lowest common package, one host-preference field, one
read-back projection, and exactly one injection seam. No layering inversion, no
parallel pattern, no orphaned contract field.

---

## What I verified (and why each holds)

### 1. Layering / dependency direction — clean
- `config` (low-level) gains `GlobalSettings.RemoteControlOnDispatch *bool`
  (registry.go:36) and `RemoteControlAtStartupKey` const (config.go:329). No new
  imports; nothing higher pulled in.
- `internal/workspace/materialize.go:425-431` reads `config.RemoteControlAtStartupKey`
  — workspace already depends on config. Downward.
- `internal/cli/dispatch*.go` reads `config.LoadGlobalConfig` + `readInstanceSettings`
  — cli is top-level. Downward.
- The shared key const living in `config` (not in cli or workspace) is the correct
  single-source-of-truth location: both the materializer-emit site and the
  dispatch-inject site bind to the same symbol. This is the right structure, not a
  cross-feature coupling.

### 2. Dispatch-only scoping invariant — holds
- `RemoteControlOnDispatch` is read in exactly one production site:
  `dispatch.go:230` (via `resolveDispatchRemoteControl`). Grep-confirmed: no reads
  in the apply / ephemeral / root-materializer paths.
- `remoteControlSettingsJSON` is appended in exactly one site: `dispatch.go:235`.
- The materializer (materialize.go:425) emits `remoteControlAtStartup` ONLY when a
  downstream `[claude.settings]` explicitly carries it — it never defaults it on.
  So the host default cannot leak to interactive, `niwa apply`, or ephemeral
  sessions. Invariant intact.

### 3. `--settings` argv safety — safe
- `remoteControlSettingsJSON = fmt.Sprintf("{%q:true}", config.RemoteControlAtStartupKey)`
  is built from a const, never user input → static `{"remoteControlAtStartup":true}`.
- Appended as two discrete argv elements (`"--settings"`, value) and passed through
  `buildClaudeBgArgs` which keeps every value its own slice element (no shell
  interpolation, no concatenation). A crafted prompt/flag cannot smuggle a flag.
- The inline JSON value carries no secret; argv visibility via /proc is a non-issue.

### 4. Step-9a order-of-operations vs. rollback/marker — no bad interaction
This was the specific concern. Walking it:
- 9a runs AFTER provision (step 6) and marker-write (step 8), BEFORE launch (step 9).
  `success` is still false, so the deferred rollback is armed.
- 9a performs only **reads** (`LoadGlobalConfig`, `readInstanceSettings`) plus a
  local `passthrough` append and an optional stderr warning. It does NOT touch the
  marker, the instance, or `success`, and it has **no early return**.
- Both error paths degrade, never fail: `LoadGlobalConfig` err → whole block
  skipped (no injection); `readInstanceSettings` err → `inst == nil` → treated as
  "downstream unset". Neither triggers the deferred destroy.
- Reading the materialized `settings.json` AFTER provision is precisely correct:
  the file is written during step 6, so the "downstream decided" detection
  (`inst.RemoteControlAtStartup != nil`) reflects the post-overlay-merge value.
- Single-threaded per invocation; the opportunistic reaper (step 5) ran earlier and
  keys on instance name/mtime, untouched by 9a. No race, no order hazard.

### 5. Precedence correctness (downstream "off" wins) — structurally enforced
`claude --settings` outranks project settings.json, which would otherwise let an
injected `true` override a downstream `false`. The resolver prevents that by
short-circuiting to `inject=false` whenever `inst.RemoteControlAtStartup != nil`
(true OR false). niwa injects only into the genuinely-unset case. Correct.

### 6. Env-source coupling — consistent
9a passes `os.Environ()` to the resolver; `realDispatchLaunch` (dispatch_launcher.go:40)
sets `cmd.Env = os.Environ()`. Same source, same process, no mid-function mutation,
so the API-key warning describes the worker's actual auth context. `apiKeyAuthForced`
uses exact-prefix `CutPrefix("ANTHROPIC_API_KEY=")` — no false match on
`ANTHROPIC_API_KEY_*`. Correct.

### 7. Contract / schema — no orphans
Every new field has a consumer: `RemoteControlOnDispatch` → resolver;
`instanceSettings.RemoteControlAtStartup` → resolver; `RemoteControlAtStartupKey`
→ materializer-emit, dispatch-inject, struct-tag-pin test. Docs (DESIGN/PLAN) match
the structs. No schema drift.

### 8. Security posture — no escalation
Injection is gated behind an explicit host opt-in (`remote_control_on_dispatch=true`
in the user's own config) AND dispatch scope. The injected value is static; the
warning string interpolates no env values. Enabling remote control is the user's
own recorded choice, not an implicit escalation.

---

## Non-blocking observations (no action required to ship)

- **N1 (correctness, out of architecture scope).** The whole approach is load-bearing
  on `claude --settings` accepting *inline JSON* and treating it as a highest-precedence
  *merge layer* (not a wholesale replacement of settings.json — otherwise an injected
  single key would shadow the worker's permissions/plugins/hooks). The design notes
  this was validated by the Variant-C spike. Flagging only so the spike result stays
  the cited authority; it is a tester/product concern, not a structural one.
- **N2 (UX).** The 9a warning prints to stderr before launch; if launch then fails and
  rolls back, the user saw a remote-control warning for an instance that no longer
  exists. Cosmetic.
- **N3 (degradation edge).** An unreadable/corrupt `settings.json` makes `inst == nil`
  → "downstream unset" → niwa may inject `true` even if the file intended `false`. Risk
  is minimal: the worker reads the same file, so a parse the narrow reader fails on is
  likely one claude also fails on. Documented degradation, acceptable.

---

## Summary

Layering downward-only; the inject seam and host-preference read each exist in
exactly one production site; `--settings` is const-built and passed as discrete
argv; 9a is read-only and cannot perturb the rollback/marker state machine; the
downstream-wins precedence is enforced by the `inst != nil` guard. Nothing here
will be copied into a divergent pattern, and no existing contract is broken.

BLOCKING COUNT: 0
