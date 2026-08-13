# Phase 5 security review — DESIGN-oss-no-infisical

Reviewed: `docs/designs/DESIGN-oss-no-infisical.md` against
`docs/prds/PRD-oss-no-infisical.md` and the five decision reports under
`wip/design_oss-no-infisical_decision_*_report.md`, grounded in the tree at
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/public/niwa/.claude/worktrees/oss-no-infisical`.

Verdict: **RISKS FOUND (DESIGN CHANGE REQUIRED)**. Two of the seven surfaces
need a change to the design text; the rest are clean or already mitigated.

---

## 1. Description injection — the mitigation holds, but it is not the whole record

### What I verified

The three writers are `internal/envformat/envformat.go`:

- `marshalDotenv` (envformat.go:52-62) — header, then `KEY=value\n`, **no
  quoting of key or value at all**.
- `marshalJSON` (envformat.go:68-91) — hand-built object, but each key and each
  value goes through `json.Marshal` individually (envformat.go:72-79), so
  escaping is correct for any byte and the object stays flat and
  string-valued.
- `marshalShell` (envformat.go:95-106) — header, then `export KEY='value'` with
  `'` → `'\''` on the **value only**; the key is written raw
  (envformat.go:100).

The reader is `parseEnvFile` (`internal/workspace/materialize.go:1437-1457`).
Confirmed line by line: `strings.TrimSpace`, then `strings.HasPrefix(line, "#")`
→ `continue` (materialize.go:1447), which fires **before** `strings.Cut(line,
"=")` (materialize.go:1450). A full-line comment therefore cannot alter any
neighbouring line's parse, at any position in the file. The design's claim that
non-corruption is structural rather than argued is correct.

Descriptions are `map[string]string` values decoded from TOML
(`internal/config/config.go:215-219`, populated by
`EnvVarsTable.UnmarshalTOML` at `internal/config/env_tables.go:33-70`). TOML
basic and multi-line basic strings admit newlines, `=`, `#`, both quote
characters, backslashes and arbitrary Unicode. Nothing validates them.

### Attacking the JSON-encoding mitigation

I could not break it for the description field:

- **Embedded newline / CR / any control byte.** RFC 8259 requires escaping of
  U+0000–U+001F, and Go's `encoding/json` does it. `\n` becomes the two bytes
  `\` `n`. The one-physical-line invariant is a property of the encoding, not of
  the input.
- **Quotes and backslashes.** Escaped. A description cannot terminate its own
  quoted field and start a second `# niwa: unresolved` record, because the
  description is specified as the **last** field of the grammar — even a forged
  trailing token lands inside the quoted region.
- **U+2028 / U+2029.** Go's `encoding/json` escapes both to ` ` / ` `
  (it does this unconditionally, for JS-safety reasons). So a Unicode line
  separator cannot produce a second physical line, and cannot produce a second
  line for any downstream JS/JSON consumer either.
- **Invalid UTF-8.** Replaced with U+FFFD by `encoding/json`. Lossy but safe.
- **Very long values.** Produces one very long comment line. `parseEnvFile`
  reads the whole file with `os.ReadFile` and splits on `\n`, so there is no
  line-length limit to overflow. Memory exposure is the same order as the
  existing terminal report. Decision 3 already records this as an open risk and
  correctly declines to truncate (truncation would break R3's exact recovery).

### Shell-format inertness

Genuinely inert, **conditional on the one-line invariant**. `#` as the first
non-blank character of a line begins a POSIX comment that runs to the next
newline, so a record in `.env.sh` is skipped by `source`. It cannot become a
command, cannot be an assignment, and the surrounding `export K='v'` lines are
untouched. This holds only because the record is one physical line — see
finding 2, which breaks exactly that premise.

Note also that `marshalShell`'s value quoting (`'` → `'\''`) is correct, and is
unchanged by this work.

### Where the mitigation is incomplete

The design's Security Considerations say: *"JSON-encoding the description
confines every record to one physical line and is the mitigation."* That
sentence is only true of the description. The record grammar
(`# niwa: unresolved <KEY> <LEVEL> <REASON> <json-description>`) also
interpolates `<KEY>` raw, and `<KEY>` is not constrained anywhere — see
finding 2. `LEVEL` and `REASON` are closed vocabularies chosen by niwa, so they
are safe.

Severity: **Medium**, and it is finding 2 that carries the weight.

### A second, unnamed description surface

The design and decision 4 route the *same* author-supplied description to two
new rendering surfaces where no encoding is applied:

- the terminal report (`Report.RenderTerminal()`, decision 4 §Rendering), and
- the SessionStart hook's `additionalContext` (`Report.RenderContext()`), which
  is injected verbatim into an agent's session context
  (`internal/cli/instance_from_hook.go:284-326`).

Raw interpolation of a description into stderr is pre-existing — `required.go`
already does it at the required-key error (`internal/workspace/required.go:99`)
and in `warnRecommended` (`required.go:152-155`). So terminal-escape injection
(ESC sequences to clear the screen, rewrite earlier lines, set the title, or
emit OSC 8 hyperlinks) is not created by this work, but the report widens it:
today only `required` and `recommended` misses print, and only on paths that
were about to fail; after this change every unresolved key at every level
prints on every successful run.

The agent-context path is genuinely new. Precedent exists — the hook already
injects the instance's whole `CLAUDE.md` (`instance_from_hook.go:301-315`) —
so the marginal prompt-injection exposure is small and comes from the same
trust domain (repos the user chose to clone). Rated **Low**, with a cheap
mitigation worth taking.

---

## 2. Key-name injection — unconstrained, and it breaks the one-line invariant

### Verified: nothing constrains an env var key name

- `EnvVarsTable.UnmarshalTOML` (`internal/config/env_tables.go:33-70`) accepts
  any TOML key that is not one of the three reserved sub-table names, and
  stores it verbatim into `Values`. `coerceDescriptionMap` does the same for
  the `required` / `recommended` / `optional` description maps.
- `validate` (`internal/config/config.go:535-595`) validates the workspace
  name, source orgs, group names, repo override names and content source
  paths. It does **not** touch env var key names.
- The only key-charset regex in the tree is `envKeyRe =
  ^[A-Za-z0-9_]+$` at `internal/workspace/env_example.go:20`, which applies to
  `.env.example` parsing only.

I confirmed this empirically by decoding a hostile config through
`BurntSushi/toml` into `config.WorkspaceConfig` (temporary test, since removed):

```
VALUE    key="# niwa: unresolved SPOOF required provider-unreachable \"x\""  plain="v"
VALUE    key="EVIL\nPATH=/tmp/evil"                                          plain="vault://p/K"
VALUE    key="A B=C"                                                         plain="plainval"
REQUIRED key="EVIL\nPATH=/tmp/evil"                                          desc="desc"
```

TOML quoted keys follow basic-string rules, so `\n`, `=`, `#`, spaces and
quotes are all legal inside a key name, in both `[env.secrets]` and the
`[env.secrets.required]` description table.

### Consequences for the record

A key named `EVIL\nPATH=/tmp/evil` that is unresolved produces:

```
# niwa: unresolved EVIL
PATH=/tmp/evil required provider-unreachable "..."
```

- **dotenv**: the second physical line has an `=`, so `parseEnvFile` turns it
  into a real map entry (`materialize.go:1450-1454`) that
  `readCloneEnvOutput` (`internal/workspace/worktree_content.go:334-366`)
  returns to the settings materializer. This is precisely the injection the
  design says JSON-encoding closes.
- **shell**: the second physical line is no longer inside a comment and is
  **executed** when the file is sourced. That is command injection into a
  sourced file, which is strictly worse than the dotenv case.
- **json**: safe — `json.Marshal` on the member name handles it
  (`envformat.go:72-75`).
- A key named `# niwa: unresolved SPOOF ...` forges a record directly, with no
  newline required, for the dotenv recovery path.
- A key containing a space (`A B=C`) desynchronizes the reader's
  `SplitN(rest, " ", 4)`, so recovery attributes the wrong tokens to level and
  reason.

### How much is new

Partly pre-existing: `marshalDotenv` and `marshalShell` already write key names
raw (envformat.go:56, :100), so a *resolved* key with a hostile name already
corrupts the generated file today. What this work changes is that keys which
can **never** resolve now reach the file too — and the primary user of this
feature is someone materializing a workspace config from a repo they do not
control, with no vault, where every declared key takes the record path. The
design also explicitly asserts a one-physical-line invariant that this defeats,
so the assertion needs to become true rather than aspirational.

Severity: **Medium**. Design change required.

---

## 3. Disclosure — no leak found in the other output paths

R8 forbids naming a repository niwa has not successfully read, because a remote
returns the same not-found response for private-and-inaccessible and
does-not-exist. I checked every output path the design introduces:

- **The no-provider rendering** (decision 4, "Rendered example — no provider
  configured"). Carries a count, a cause sentence, and per-key
  `level / key / description`. No repository, no remedy. Correct.
- **The provider-unreachable rendering.** Names the provider *kind*, which R9
  mandates and R20 exempts. The kind can only be non-empty when a
  `[vault.provider]` block was present in a config niwa successfully parsed —
  either the base workspace config or an overlay clone that was fetched and
  read (`internal/workspace/apply.go:1027-1059`). Nothing is asserted about a
  layer that failed to fetch.
- **The `Entry.Scope` field** (decision 4) is `repos.<name>.env.secrets` for
  per-repo declarations. Repo names in scope strings come from the merged
  config; overlay-supplied repos only enter via `MergeWorkspaceOverlay`
  (`internal/workspace/override.go:753-780`) after the overlay clone was read.
  The proposed renderings do not print Scope, so no repo name reaches the
  terminal today; if a future change renders it, it still names only
  successfully-read configuration. No R8 violation either way.
- **The file records.** Carry key, level, cause, description. No repository, no
  host state, no path.
- **The strict-mode overlay tombstone warning** (decision 2, Option A). Fires
  only inside `validateOverlay` (`internal/config/overlay.go:101`), which runs
  only after `ParseOverlay` (`overlay.go:82-99`) successfully read
  `workspace-overlay.toml`. A contributor with no overlay never sees it, so it
  cannot disclose that an overlay exists. Correct by construction.
- **The hook's structured output** (`buildSessionStartInjection`,
  `internal/cli/instance_from_hook.go:299-326`). Carries the same report text.
  Same content, same guarantees.

One residual, and it is not niwa's to fix: an author-supplied *description* can
itself name a private repo or an invisible layer ("ask the platform team for
access to acme/secrets-overlay"). R8 constrains what niwa asserts; it cannot
constrain what a config author wrote. The PRD already records the inverse of
this in Known Limitations ("requirement descriptions become load-bearing").
Worth one sentence in the design so the obligation is explicit.

Severity: **None** for the design's own output. **Informational** for the
author-description channel.

---

## 4. Secret leakage — the claim holds

The design asserts "no secret value enters any new surface". Verified against
the mechanism:

- **The `Unresolved` struct** (design Components; decision 1
  §Option A) carries cause, declared level, declared description, and provider
  kind. There is no value field and no reference to `secret.Value`.
- **The collector** (`keyreport.Entry`, decision 4) is
  `{Cause, Level, Scope, Key, Description, ProviderKind}` — same shape, no
  value.
- **The record writers** emit key, level, reason, description. R3's
  "SHALL NOT carry a value" is structural in Option A: there is no field to
  put one in.
- **The renderer** builds from `Entry` fields only. Decision 4's own open risk
  ("R20's list will rot") already warns against interpolating a provider error
  string; that warning should be carried into the design, because a wrapped
  `%v` from a vault backend is the one realistic way a value fragment could
  reach the report.

I looked specifically for the cross-layer paths the task named:

- **A key that resolves on one layer and is marked on another.** All four merge
  functions assign whole `MaybeSecret` values — `merged.Env.Secrets.Values[k] =
  v` at `internal/workspace/override.go:640-642` and the parallel sites at
  `:117/:128/:147/:156` and `:569-642` — never field-wise. So a value can never
  end up with both a populated `Secret` and a non-nil `Unresolved`, and a
  record can never be written for a value that carries bytes. Verified by
  reading the merge bodies, not inferred.
  - Note the *behavioural* consequence in the other direction:
    `MergeGlobalOverride` lets the personal overlay win per key
    (`override.go:640-642`, gated only by `team_only`). If the team layer
    resolves `K` and the personal overlay declares the same `K` against an
    unreachable personal bundle, the marked-empty overlay value **overwrites**
    the resolved team value, and `K` is omitted rather than written. Today that
    is a hard error; after this change it is a silent omission plus a report
    line. Not a leak, but it deserves a sentence in Consequences.
- **The deep-copy path.** `cloneEnvVarsTable`
  (`internal/vault/resolve/deepcopy.go`) uses `maps.Copy` over
  `map[string]MaybeSecret`, which copies the struct by value including the
  `*Unresolved` pointer. Copies share the pointee. Safe only while `Unresolved`
  is immutable after construction — decision 1 open risk 3 says so; the design
  should state it as a rule (no setters, constructed in exactly one place) so
  it is not rediscovered.
- **`maybeSecretString`** (`internal/workspace/materialize.go:38`) reaches
  through `UnsafeReveal`. The design's "omit marked keys" must happen **before**
  that call, in both `ResolveEnvVars` and `resolveClaudeEnvVars`. Since a
  marked value is always zero-valued, the worst case is writing `""` rather
  than leaking — but decision 1's suggestion to make `maybeSecretString`
  assert-or-guard rather than silently return `""` is the right call and should
  survive into the design.

One residual, correctly identified by decision 3 and worth carrying into the
design because the design currently omits it: **a secret value containing a
newline can forge a record**, because `marshalDotenv` writes values raw
(`envformat.go:52-62`). Blast radius is a misleading report entry — a forged
record cannot set a value or hide a real one — and the same value already
corrupts the file today. Pre-existing; note it, do not fix it here.

Severity: **None** for the leakage claim itself; **Low** for the residuals.

---

## 5. Strict mode as a security control — verified, holds structurally

The design places `strict_secrets` on `[workspace]` because a visibility
overlay structurally cannot supply that table. I verified this independently
rather than taking decision 2's word:

- **`config.WorkspaceOverlay`** (`internal/config/overlay.go:18-26`) has
  exactly seven fields: `Sources`, `Groups`, `Repos`, `Claude`, `Env`, `Files`,
  `Vault`. There is no `Workspace` field, and its doc comment says so
  explicitly ("lacks workspace metadata fields", overlay.go:16-17). A
  `[workspace]` stanza in `workspace-overlay.toml` has nowhere to decode.
- **`MergeWorkspaceOverlay`** (`internal/workspace/override.go:708`) opens with
  `merged := *ws` (override.go:710) and then rebuilds only Claude, Env, Files,
  Sources, Groups, Repos. `grep` for `merged.Workspace`, `merged.Vault`,
  `merged.Instance`, `merged.Root` across `override.go` returns nothing.
- **Repo-wide**: `grep -rn "\.Workspace ="` over all non-test Go returns **no
  matches at all**. `WorkspaceMeta` appears in only five non-test places
  (`internal/config/config.go:240, 275, 276, 387, 395`), all declarations and
  doc comments. The field is populated by TOML decode and never assigned
  afterwards — not by `MergeWorkspaceOverlay`, not by `MergeGlobalOverride`,
  not by `MergeOverrides`, not by `MergeInstanceOverrides`.

So R13's "does not take effect" is a structural property of the type graph, not
a check that can be forgotten. The design's claim is held, not asserted. This
is the strongest part of the design.

Two caveats to record:

- **The tombstone is load-bearing and looks like dead code.** A decode-only
  `Workspace *OverlayWorkspaceStanza` field on `WorkspaceOverlay`, existing only
  to be refused, is exactly the kind of thing a future cleanup deletes — or
  worse, that someone "fixes" by wiring it to a real `WorkspaceMeta` for an
  unrelated reason, silently reopening R13. Decision 2 open risk 5 proposes
  pinning it with a test in the spirit of `TestShadowHasNoSecretValueField`.
  The design should name that test as part of the work, not leave it in a wip
  report.
- **The host/global rung.** The design defers it (the resolver takes the
  parameter but v1 leaves it unpopulated). That is the right call, and the rung
  itself would be acceptable if added later: `GlobalSettings` lives in
  `~/.config/niwa/config.toml`, is host-local, and is readable and editable by
  the person running the command — it is not a layer a contributor cannot see,
  which is the property R13 protects. The rung that would **not** be acceptable
  is `GlobalOverride` (the personal-overlay repo snapshot), and the design
  correctly does not use it. Worth one explicit sentence so a later
  implementer does not pick the wrong "global".
- **`?required=false` is outside strict mode by construction.** R2a keeps the
  per-reference opt-out unmarked (`resolve.go:529` untouched), so an opt-out
  key is never a shortfall and strict mode has no claim on it. This is
  deliberate and correct, but it means the fatality rule in `checkRequiredKeys`
  must be stated over the *(value empty, cause)* pair, not over the cause
  alone. Today `collectMissing` (`internal/workspace/required.go:107-127`) fires
  on **any** empty required value, including the opt-out downgrade, and the
  doc comment at `required.go:34-46` says that is intentional. The design's
  phrasing — "a required key whose cause is a reachable provider not holding it
  stays fatal; every other cause does not" — reads as though a required key
  with an *empty value and no cause* becomes non-fatal. If implemented that
  way, adding `?required=false` to a reference becomes a way to switch off the
  required-key check, and (because `MergeGlobalOverride` lets the personal
  overlay win per key, `override.go:640-642`) a personal overlay could do it to
  a team-declared key that is not in `team_only`. Same trust domain as the
  operator, so severity is **Low**, but the design should pin the rule so the
  implementer does not accidentally loosen it.

Severity: **None** for the placement itself. **Low** for the tombstone
durability and the fatality-rule phrasing.

---

## 6. Omission versus blanking — no inverse risk found in this repo

The design argues omission is safer than an empty value. I looked for the
inverse: a consumer that treats an absent variable as more permissive than an
empty one.

- **`os.LookupEnv` appears zero times** in `internal/` and `cmd/` (non-test).
  There is no presence-gated behaviour anywhere in niwa: every environment
  check is a `Getenv`-and-compare, for which absent and empty are identical.
- **The only membership test on a parsed env map** is the promote lookup:
  `val, found := resolvedEnv[key]` at
  `internal/workspace/materialize.go:958-962`. There, absence is *stricter*
  than empty — absence is today a hard error, while an empty string is promoted
  silently into `settings.local.json`. The PRD's own reasoning (R11 exists
  because omission starts firing a check that empty-string slipped past)
  matches the code exactly.
- **`.env.example` policy** (`internal/workspace/env_example.go`) evaluates
  declared-but-unsupplied keys through a warn/fail ladder. Omission can only
  move a key from "supplied empty" to "not supplied", which is the noisier
  direction, not the more permissive one.
- **Generated env files are consumed by the user's tools, not by niwa**, apart
  from the `readCloneEnvOutput` path above. For those tools omission is the
  conventional way to say "unset", and the PRD's R2 rationale is standard
  practice.

I found no consumer where absence is more permissive. The design's claim holds.

Severity: **None**.

---

## 7. The three-way promote branch — not a bypass, but the discriminator is
   file-supplied on the worktree path

The branch (decision 5 §5) is:

1. key in `resolvedEnv` → promote;
2. key absent **and** in the unresolved set → omit from generated settings,
   report, continue;
3. key absent **and** not in the set → keep today's hard error
   (`materialize.go:961`).

Case 3 cannot be reached *more* often than today, so the typo protection cannot
be weakened by the branch itself. The question is whether an attacker can move
a key from case 3 into case 2.

- **On the instance path** the unresolved set comes from in-memory marks
  produced by the resolver in the same process. Nothing external can add to it.
  Clean.
- **On the worktree path** the set is recovered by `readCloneEnvOutput`
  (`internal/workspace/worktree_content.go:334-366`) from the *clone's*
  generated env file — a file that lives inside a cloned repository's working
  tree (`safeTargetPath(cloneRepoDir, tgt.Path)`, worktree_content.go:344). Any
  party who can write that path in the clone — a committed `.local.env`, a repo
  the user does not fully trust, a setup script — can plant a record and move a
  key into case 2. Effect: a promoted key that should have failed loudly is
  instead silently omitted from `settings.local.json`, plus an attacker-chosen
  key name and description appear in the operator's report.

That is a degradation and a text-injection channel, not privilege escalation:
the record carries no value, cannot create an assignment (the reader drops `#`
lines at materialize.go:1447), and cannot overwrite a real key. Decision 3's
grammar already validates the recovered key against
`^[A-Za-z0-9_]+$` and a closed level/reason vocabulary, which blocks the worst
of it; what remains unvalidated is the JSON-decoded description, which can
contain terminal escapes after decoding.

Two things follow. First, the design should state explicitly that on the
worktree path the unresolved set is **file-derived and therefore only as
trustworthy as the clone**, rather than leaving that implicit. Second,
sanitizing control characters at render time (see finding 1) covers the
injection half.

Note the availability tail as well: a key name that is legal in TOML but not in
the record grammar (a space, a `#`) writes a record the reader then refuses to
recover, which drops the key back into case 3 and turns a tolerated omission
into a worktree failure. Constraining key names at the *write* side (finding 2)
closes both ends of this.

Severity: **Low-Medium**.

---

## Required design changes

1. **Constrain the key name in the record writer, and correct the design's
   mitigation sentence.** The record's one-physical-line invariant must be
   stated over the whole record, not just the description. Concretely: the
   record writer validates the key against the existing
   `^[A-Za-z0-9_]+$` charset (`internal/workspace/env_example.go:20`) and,
   for a key that fails, either JSON-encodes it in the same way as the
   description or emits no record for that key. The Security Considerations
   paragraph should say "JSON-encoding the description, and constraining the
   key name, confine every record to one physical line", because as written it
   claims a property the record does not have. Add a test with a key containing
   a newline, a `=`, a space and a leading `# niwa:` for all three formats.

2. **Sanitize control characters in both report renderings.** `RenderTerminal`
   and `RenderContext` interpolate author-supplied descriptions raw. Strip or
   escape C0 (including ESC and CR), C1, and U+2028/U+2029 before rendering,
   and note in the design that the hook's `additionalContext` carries
   author-supplied text into an agent's context. This is cheap, it also
   improves the pre-existing `required.go:99` and `required.go:152` sites, and
   without it the byte-identical-output acceptance criterion is testable while
   the terminal is still spoofable.

## Recommended, not required

3. Pin the fatality rule as *(value empty, cause)*, so a required key with an
   empty value and no cause stays fatal and `?required=false` cannot become an
   off-switch for the required-key check.
4. State that the worktree path's unresolved set is recovered from a file
   inside the clone, and is therefore only as trustworthy as that clone.
5. State that `Unresolved` is immutable after construction (shared by pointer
   across `maps.Copy` deep copies), with no setters and one construction site.
6. Name the tombstone regression test in the design (assert
   `MergeWorkspaceOverlay` leaves `merged.Workspace` equal to the base), so the
   structural R13 guarantee is protected against a future cleanup.
7. Record in Consequences that a personal overlay declaring a key the team
   layer already resolved will now omit it rather than fail, because
   `MergeGlobalOverride` lets the overlay win per key
   (`internal/workspace/override.go:640-642`).
8. Carry decision 4's warning into the design: the renderer must build text from
   structured `Entry` fields only and never interpolate a provider error string,
   or the R20 vocabulary list is enforced against a template while vendor text
   flows around it.
