---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-oss-no-infisical.md
problem: |
  niwa resolves a workspace's declared secrets before it will materialize an
  instance and treats any shortfall as fatal, so a host with no vault client
  installed cannot create one. Four independent enforcement points can stop the
  command, two different users reach two different ones, and the documented
  escape hatch covers neither. Nothing downstream can distinguish a deliberately
  empty value from an unresolved one, which is why the failure has to be a
  failure rather than a degradation.
decision: |
  Mark instead of erroring. The resolver records why a key is unresolved in a
  nilable field on the resolved value, which every existing merge and deep-copy
  path already carries for free. A caller-supplied collector accumulates those
  marks and each command surface renders one sorted report after the run
  returns, on both the success and failure paths. Unresolved keys are omitted
  from generated environment files and recorded as reader-recoverable comment
  lines. Strict mode is a workspace setting with a tri-state flag override, and
  `--allow-missing-secrets` retires to a deprecated no-op.
rationale: |
  The mark rides the existing value-merge machinery, so last-layer-wins works
  without re-implementation and the required-key check reaches its answer with
  no signature change and no vault bundle. A collector rather than a return
  value is forced by the create path deleting its instance and returning a bare
  error, which would discard the report exactly when a strict failure most needs
  it. Strict mode sits on the one configuration table a visibility overlay
  structurally cannot supply, so the contributor-protection requirement holds by
  construction rather than by enforcement.
---

## Status

Planned

Five decision questions were evaluated independently against the accepted PRD,
then cross-validated. The Implementation Approach below sequences the work.

## Context and Problem Statement

The accepted PRD (`docs/prds/PRD-oss-no-infisical.md`) requires instance
materialization to stop being gated on secret resolution, with two exceptions
retained. This design decides where each mechanism lives.

The technical problem underneath the product one is that niwa currently has no
way to represent "this key has no value, and here is why." A resolved value is
a `config.MaybeSecret`, and its documented contract is two-state: it either
carries a plain value or a secret one. A key the backend does not hold, a key
whose provider could not be contacted, and a key an author deliberately declared
empty all arrive downstream as the same zero value. Every requirement in the PRD
that asks niwa to treat those cases differently — omit one but not another,
report one but not another, fail on one but not another — needs that distinction
to exist before it can be acted on.

The second problem is delivery. The report has to reach a human on five command
surfaces and an agent on a sixth, and the surface where it matters most is the
one that currently produces no output at all: the create path deletes its
instance and returns a bare error, and the SessionStart hook returns before it
writes anything to stdout.

## Decision Drivers

- **The mark must survive configuration merging.** Values move through four
  merge functions and two deep-copy paths. A representation that does not ride
  that machinery has to re-implement last-layer-wins, and its failure mode is a
  silently wrong report rather than a crash.
- **The required-key check must reach its answer without a vault bundle.**
  `checkRequiredKeys` takes a config and a writer. Threading vault state into it
  would couple the post-merge check to the resolver.
- **The report must survive the failure path.** A strict-mode failure is when
  the user most needs every affected key enumerated, and it is the path that
  currently destroys its own instance and returns nothing.
- **A contributor's first run must not be alterable by a layer they cannot
  read.** This is a security property, not a preference.
- **Existing silent-downgrade contracts must not break.** The per-reference
  opt-out exists to be quiet, and two tests assert it.
- **The generated-file record must not corrupt its neighbours.** The reader is a
  line-based split that cannot fail, so a malformed record is silently absorbed
  rather than rejected.
- **Descriptions are author-supplied free text.** They are an injection surface,
  not a display string.

## Considered Options

### Decision 1 — Where the unresolved reason lives

**Chosen: a nilable `Unresolved *Unresolved` field on `config.MaybeSecret`.**
The resolver marks rather than erroring; report assembly and the fatality call
live post-merge beside `checkRequiredKeys`.

*A parallel structure keyed by env var name, alongside the resolved map.*
Rejected. Four merge functions and both deep-copy paths already move
`MaybeSecret` values wholesale, so the field inherits last-layer-wins — a
personal overlay supplying what a public config could not — at no cost. A
parallel map must re-implement that merge by hand at every site, and when it
gets it wrong the result is a report that names the wrong keys rather than a
compile error.

*A typed error carried up and aggregated by the caller.* Rejected. It preserves
the shape where the resolver decides fatality, which is the thing this work is
undoing, and it detaches the reason from the value it describes — so every
consumer that needs to know whether *this* key is unresolved has to search an
error list by name, which is the parallel-map problem wearing different clothes.

The accepted cost is real: `MaybeSecret`'s two-state invariant becomes
three-state across a type used in 134 test literals, and every `Plain == ""`
check becomes a site that could mis-handle the new state. The type's contract is
also "never print me", and the new field must be printed. Mitigations are in
Consequences.

### Decision 2 — Where strict mode lives

**Chosen: `strict_secrets` as a `*bool` on the `[workspace]` table, with a
tri-state `--strict-secrets` flag, resolved by a pure precedence function.**

*A boolean on an existing env or vault table.* Rejected on the security driver.
Those types are reachable from a visibility overlay — repo overrides are copied
verbatim during merge, and the vault registry is an overlay-supplied field — so
the contributor-protection requirement would have to be enforced by a check that
someone could later forget. On `[workspace]` it holds structurally: the overlay
type has no workspace stanza and the merge never assigns one.

*A per-key severity ramp modelled on the existing `.env.example` failure
policy.* Rejected as premature. It is the natural home if per-key granularity is
ever wanted, and the PRD deliberately chose coarse first because that direction
is the cheap one to reverse.

A decode-only tombstone stanza is added to the overlay type so an author who
tries to set it there gets a deferred warning rather than silence.

### Decision 3 — The omitted-key record format

**Chosen: a per-key `# niwa: unresolved` comment line, interleaved at the key's
sorted position, with the description JSON-encoded and last.**

```
# Generated by niwa - do not edit manually
# niwa: unresolved ANTHROPIC_API_KEY required provider-unreachable "API key for Claude Code sessions"
NIWA_PROFILE=dev
```

*A structured header block listing all omitted keys together.* The closest
contender, and it has a genuine advantage: one block is easier to scan when many
keys are unresolved, and it keeps the record out of the assignment stream
entirely. Rejected because it separates a key's record from its sorted position,
so a reader scanning the file for a particular variable finds nothing where they
are looking — which is the moment the record exists to serve.

*A renamed sentinel assignment carrying the metadata as its value*, such as
`ANTHROPIC_API_KEY__NIWA_UNRESOLVED=required provider-unreachable "…"`.
Rejected on four grounds: the requirement forbids the record carrying a value;
the reader would admit the sentinel into the parsed map, polluting the promote
path; in shell format it becomes a genuinely exported variable in the
contributor's environment; and putting author-supplied text on the right of an
`=` reopens the injection surface the comment form closes.

*A commented-out assignment*, `# ANTHROPIC_API_KEY=`. The most dotenv-native
form, and trivially legible. Rejected because it carries nowhere to put the
cause, the level, or the description without inventing a sub-syntax inside the
comment, at which point it is the chosen form with worse ergonomics — and
because a reader uncommenting it produces the empty value this work removes.

Non-corruption is structural rather than argued: the reader discards `#` lines
before the `=` split runs, so no adjacent assignment can change by a byte at any
position. The description is JSON-encoded because it is author-supplied TOML
free text; a raw description containing `PATH=/tmp/evil` on its own line would
otherwise become a real map entry on read-back. Encoding makes every record
exactly one physical line and closes that.

For non-dotenv targets the record is emitted in the target's own format — a
string-valued member for JSON, a comment for shell — and is not recoverable by
the format-blind reader, which the PRD accepts.

### Decision 4 — How the report is produced and delivered

**Chosen: a caller-supplied collector in a new leaf package, drained and
rendered by each command surface after the run returns.**

*Returning the report up the call stack.* Rejected on the failure-path driver:
`Create` removes its instance directory and returns a bare error, so a returned
report is discarded on exactly the strict-mode path that needs it most.

*Rendering inline through the existing reporter.* Rejected. The reporter has no
read-back and its byte stream interleaves spinner frames, so the hook's
structured output cannot be assembled from it and the prohibited-vocabulary test
cannot isolate report text.

*Deriving the report from the generated files after the fact*, by reading back
the records the materializer just wrote. Genuinely cheap — near-zero plumbing,
no new field threaded through three option structs, and the records already
carry key, level, and description. Rejected because the cause is run-scoped, not
file-scoped: whether a provider was unreachable, and whether its client was
absent, is knowledge the resolver had and the file does not. The two messages
the requirements demand cannot be reconstructed from what was written. It also
inverts on the strict path, where the failure happens before any file exists.

The collector mirrors an existing caller-supplied sink on the resolver's
options, so the shape is not new to the codebase. Sorting happens on read, which
makes the report deterministic without touching the two unsorted map walks that
feed it.

### Decision 5 — Flag retirement and precedence

**Chosen: `--allow-missing-secrets` stays registered as a deprecated no-op;
`--strict-secrets` is added to create, apply, and init through one shared
registrar; co-occurrence is rejected by the flag framework's
mutually-exclusive-group mechanism.**

*Silent accept-and-ignore.* Rejected — a user whose script passes the flag
deserves to learn it no longer does anything.

*Outright removal.* Rejected — it breaks existing scripts and CI invocations for
no benefit, since the tolerant behaviour they wanted is now the default.

**Precedence rule.** Strictness is the configuration setting's value, overridden
by `--strict-secrets` whenever the flag was explicitly present on the command
line, and tolerant when neither speaks. The arbiter is whether the flag was
changed rather than its value, so `--strict-secrets=false` genuinely
de-escalates a workspace that sets it. `--allow-missing-secrets` never
participates; its presence alongside the strict flag rejects the invocation.

## Decision Outcome

The five decisions compose into one path. The resolver stops deciding fatality
and starts marking, which gives every downstream consumer the distinction it
needs. The marks ride the existing merge machinery to the post-merge check,
where fatality is decided once, against the full picture, by a function whose
signature does not change. The same marks feed a collector that each surface
drains and renders, and feed the materializer, which omits the keys and writes
recoverable records in their place.

**Where strict mode is consulted.** Resolved strictness is carried on the
applier and read at one place: immediately beside the post-merge required-key
check, after fatality has been decided for the strict-when-reachable case. A
strict run turns every collected mark fatal there; a tolerant run turns none.
Enforcement point 4 sits outside that gate because promotion happens later,
per-repo, so the three-way promote branch gains a strict arm of its own: under
strict mode a promoted key that was omitted fails rather than being reported.
Both sites read the same resolved value, so there is one decision and two
consult points, not two decisions.

**The unsatisfiable-declaration cause has two shapes, and only one carries a
mark.** Where a `vault://` reference names a provider the merged configuration
does not declare, there is a value to mark and the resolver marks it. But where
a key is listed in a required sub-table with no entry in the values map at all,
the resolver's walker never visits it — there is nothing to hang a mark on. That
second shape is the no-provider contributor's exact case, so a report assembled
purely by scanning marks would come back empty for the very user this work
exists to serve.

The post-merge check already detects that shape, by finding a declared key with
no value. The report is therefore assembled from two sources: the marks carried
on values, and the declared-but-absent keys derived post-merge. Anything
implementing only the first half satisfies no acceptance criterion that matters.

The same post-merge walk must cover the optional sub-table as well as required
and recommended. Today nothing walks optional, because nothing needed to; the
report's declared-level column does.

**Where the promote branch learns which keys are unresolved.** On the instance
path it reads the marks already present in the effective config — the entry
stays in the values map carrying its mark, and only the materialized output
omits it, so no additional field is threaded. This resolves a disagreement
between two decision reports, one of which assumed the entry would be removed
from the map entirely. On the worktree path the marks are not in memory, because
that path reads an already-materialized file rather than re-resolving; there the
unresolved set is recovered by the records-aware reader from the records written
into the clone's own environment file.

**Which failures stay fatal inside the resolver.** Marking replaces erroring
only for shortfalls. A malformed reference that fails to parse, an unsupported
reference version, a body that will not decode, and a missing required field all
stay hard errors, because they are configuration defects rather than absent
values — degrading them would make the tolerant default swallow config typos,
which is the opposite of the intent. The tolerable set is exactly: key not found
on a reachable provider, provider unreachable, client not installed, and a
reference naming a provider the merged configuration does not declare.

**Where the new types live.** `Unresolved`, its cause enum, and the level live
in `config` beside `MaybeSecret`, because the field is on that type and the
value must travel with it through merge and deep-copy. `internal/keyreport`
holds the collector and the renderers and imports `config` for those types; it
is a leaf with respect to `workspace` and `vault`, which is what the cycle
avoidance requires, but it is not stdlib-only. A marked value's `String()` and
`MarshalText()` return what a zero value returns today — the mark is not part of
the value's textual form, and making it so would put must-print data on a type
whose rendering is deliberately empty.

**Which commands honour strict mode.** Every command that provisions through the
shared provisioning path does, which includes `niwa reset`, `niwa watch --once`,
and the reap-driven re-provision, not only the five the requirements enumerate.
This is deliberate rather than incidental: strictness is a property of the
workspace, and a path that quietly ignored it would be the same class of bug as
the escape hatch that did not escape.

**Worktree re-materialization is the one exemption, and it costs nothing.**
That path resolves no secrets at all — it copies and reads already-materialized
files — so it never reaches a consult site and strictness is never threaded into
it. The exemption therefore holds by omission rather than by a check. The one
hazard is documentary: a stale comment elsewhere still describes a worktree
secret-resolution tolerance that no longer exists, and an implementer reading it
would plumb strictness into a path with nothing to be strict about. Deleting
that comment is part of the work, not tidying alongside it.

Four cross-decision dependencies are load-bearing:

- Decision 3's record must be reader-recoverable, because decision 5 found that
  omission propagates into a worktree clone's environment file and the promote
  path reads it back. A record the reader drops would turn a tolerated omission
  into a new worktree failure.
- Decision 1's reclassification of the undeclared-provider case is what lets
  decision 4 emit two different messages. Today that case is wrapped as
  key-not-found, which under the retained strict-when-reachable rule would make
  the OSS contributor's situation wrongly fatal — the precise bug this work
  exists to remove.
- Decision 3's blocker gates both: the materializer early-returns on an empty
  value map, so a repo whose keys are *all* unresolved writes no file and
  therefore no record. That is the primary user's exact case, and both guards
  must admit "records exist."
- Promotion records originate inside the per-repo clone worker pool, so several
  goroutines record into the collector concurrently. The collector guards its
  accumulation with a mutex, and sorts on read rather than on write — which is
  also what makes the ordering requirement independent of goroutine scheduling
  rather than merely of map iteration.

**The worktree recovery path is a trust boundary.** Because the worktree half
recovers its unresolved set from a file inside the clone, a repository that
writes its own environment file can move a key from the promote branch's hard
error into the tolerated branch. The effect is degradation and text injection
into a report, not privilege escalation — the repo already controls its own
contents — but the records-aware reader treats those records as untrusted input
and applies the same key-name and description constraints on read that the
writer applies on write.

## Solution Architecture

### Components

**`config.MaybeSecret` gains `Unresolved *Unresolved`.** The `Unresolved` struct
carries the cause (unsatisfiable declaration, provider unreachable, client not
installed, required-key shortfall), the declared requirement level, the declared
description, and the provider kind where one is configured. Nil means resolved.
The per-reference opt-out never sets it, which is how the silent-downgrade
contract survives untouched.

**`internal/keyreport`** is a new package holding the collector and the
renderers. It imports `config` for the mark types and nothing else from this
codebase, so it is a leaf with respect to `workspace` and `vault` — which is
what lets both use it without a cycle — but it is not stdlib-only. `Report()`
sorts by cause, then scope, then key, and accumulation is mutex-guarded because
per-repo materializers record into it concurrently.

**The resolver** marks instead of returning an error for the tolerable causes,
and gains a sentinel distinguishing "client binary not installed" from other
unreachability, which the two existing wrap sites currently collapse.

**`checkRequiredKeys`** reads the new field. Fatality is a field comparison: a
required key whose cause is a reachable provider not holding it stays fatal;
every other cause does not.

**The materializer** omits marked keys, writes records in their place, and its
promote path becomes three-way — promote, or omit-and-report when the key is in
the unresolved set, or keep today's hard error when it is neither, which
preserves the typo protection the PRD wanted.

**The reader** splits into a records-aware form and a wrapper preserving today's
signature, so the worktree path can consume records while existing callers are
untouched.

### Data flow

Resolution marks a value. Merge carries the mark. The post-merge check reads it
and decides fatality once. The collector accumulates every mark regardless of
fatality. Materialization omits and records. Each command surface drains the
collector and renders, after the run returns, on both paths. The hook renders
into its structured output and exits 0, because that is the only channel that
reaches the agent.

## Implementation Approach

Two pairs must land together. Landing either half alone leaves the suite red.

1. **The mark, and fatality moving.** Add the field and its struct; update the
   deep-copy paths; add the client-not-installed sentinel; reclassify the
   undeclared-provider case; add the double-resolution guard; teach the
   required-key check to read the mark; stop the resolver erroring on tolerable
   causes; delete the `AllowMissingSecrets` plumbing *including its two
   assignments in the CLI layer*, which cannot be left behind or the package
   will not compile.

   This step changes behaviour from its first commit — reclassifying the
   undeclared-provider case alters an existing resolver test's expected sentinel
   and changes the silent-downgrade path for an optional reference naming an
   unknown provider. The earlier claim that it is inert was wrong.

2. **The collector.** Add the package; thread it through the resolver options,
   effective-config options, and the applier; guard accumulation with a mutex;
   render on each surface, on both the success and failure paths.

3. **Files and promotion.** Omission, the record writers for all three formats,
   the records-aware reader, the two empty-map guards, and the three-way promote
   branch across the instance and worktree halves. These are one unit:
   introducing omission is exactly what starts firing the promote error, because
   the lookup only fails once the key leaves the map, and the worktree path
   breaks in the same commit. The promote fix is not a follow-up.

4. **Strict mode.** The setting, the flag registrar, the precedence function,
   the overlay tombstone, the consult site beside the required-key check, the
   strict arm on the promote branch, and wiring at the unattended provisioning
   site. Also delete the stale comment describing a worktree secret-resolution
   tolerance that no longer exists, since leaving it would push an implementer
   to thread strictness into a path that resolves no secrets at all.

5. **Flag retirement and documentation.** Deprecation marking, the
   mutually-exclusive group, and correcting every description including the
   sentinel doc comment.

Step 4 depends on both 2 and 3: its strict arm attaches to the promote branch
that step 3 creates, and its post-merge consult site enumerates the marks step 2
collects. Step 5 is independent of everything after step 1.

Step 1 must also update the test files referencing the deleted plumbing fields,
which is a compile dependency rather than a behavioural one.

## Security Considerations

**The whole record must be one physical line, not just its description.**
Records are written into generated environment files that niwa itself reads
back, and a record broken across two lines makes the second line a real
assignment in dotenv format and an executed command in shell format. Two
author-supplied fields enter a record, and the first draft of this design
mitigated only one of them.

Descriptions are TOML free text and are JSON-encoded, which holds under
adversarial review: newlines, quotes, backslashes, the unicode line and
paragraph separators, invalid UTF-8, and length all fail to escape the encoding.

**Key names are equally author-supplied and are not constrained anywhere.** The
environment-table decoder stores any TOML key verbatim and validation never
inspects it, so a configuration can declare a key containing a newline, an `=`,
a space, or a leading `# niwa:`. The record writer therefore validates the key
against the same conservative pattern the `.env.example` writer already uses,
and encodes or skips a key that fails rather than interpolating it. The
one-physical-line invariant is asserted over the entire record.

**Report renderers strip control characters.** Descriptions reach a terminal and,
on the hook path, an agent's context window. Both renderers strip C0 and C1
control characters and the unicode line and paragraph separators before
emitting. The hook case deserves naming plainly: the structured output carries
author-supplied text from a workspace configuration into an agent's context, so
it is an instruction-injection surface as well as a display one.

**The report must not disclose what niwa cannot verify.** A remote returns the
same not-found response for a private repository the caller may not read and one
that does not exist. The no-provider rendering therefore carries no remedy
sentence and names no repository — there is nothing niwa can safely suggest. The
prohibited-vocabulary check enforces the vocabulary half; the absence of a
repository name in that rendering is a separate assertion.

**Strict mode is a security-relevant setting.** Placing it on the one table a
visibility overlay cannot supply means a contributor's first-run experience
cannot be altered by a layer they cannot read. This holds structurally rather
than by check, which is the stronger form. The tombstone stanza exists so an
attempt is visible rather than silent.

**No secret value enters any new surface.** Marks carry key names, declared
levels, declared descriptions, and provider kinds. The collector holds no
values, the records carry none, and the rendered report has none to leak. The
existing redaction machinery is unchanged and unrelied-upon here, because
nothing in this path ever holds a value to redact.

**Omission is the safer default than blanking.** Writing an empty value can
cause a downstream consumer to treat an unconfigured credential as a configured
empty one. Absence produces a clearer failure closer to the cause.

## Consequences

### Positive

- The four enforcement points stop being four independent walls; fatality is
  decided once, post-merge, against the whole picture.
- The report survives the failure path, which is where it was previously absent.
- Determinism arrives by sorting on read, without touching the unsorted walks.
- Two pre-existing defects are fixed in passing: the duplicated apply error and
  the nondeterministic warning order.
- The contributor-protection property holds by construction.

### Negative, with mitigations

- **`MaybeSecret` becomes three-state.** Its documented contract is that exactly
  one of the plain and secret fields is populated, plus defined zero-value
  semantics; the redaction promise lives specifically on its string and
  text-marshalling methods. Mitigation: the field is nilable so the zero value
  behaves as today; those two methods return the zero rendering on a marked
  value, so the redaction promise is untouched; the existing constructors remain
  the only writers; and the type's tests are extended rather than replaced.
- **Fourteen existing tests change.** Mitigation: they are inventoried with
  file and line in the decision reports, and the two silent-downgrade tests were
  verified assertion by assertion to survive unchanged.
- **A workspace author enabling strict mode restores the wall for every
  contributor**, and the unattended paths have no flag to escape it. Accepted:
  unlike an overlay, the setting is in a file the contributor can read, and the
  PRD's coarse-first decision is reversible toward granularity.
- **The description scan test could force rewriting shipped documents** if it
  swept the docs tree. Mitigation: it excludes published PRDs and designs, which
  legitimately quote the old behaviour when describing what changed.
