# Exploration Decisions: inert-defaultmode-key

Decisions taken in auto mode follow the lightweight decision protocol: frame,
gather from what the research already surfaced, decide and record. `status`
is `confirmed` where the evidence was unambiguous and `assumed` where it was
not.

## Round 1

### D1 -- Run this exploration in auto mode (tier 2, confirmed)

**Question.** The repo declares no `## Execution Mode:` header, so the skill's
default is interactive, but this session is a detached background worker
dispatched with a written brief and no author on the other end.

**Evidence.** The dispatch brief asks for the skill to run to its crystallize
step and land on a concrete entry point, and to commit and push the artifact.
Blocking on `AskUserQuestion` would stall the run indefinitely. The brief also
states its own narrowing priorities, which is what the interactive convergence
questions would otherwise elicit.

**Decision.** Auto mode, max 3 rounds, decisions recorded here. The brief's
stated priorities substitute for the author's narrowing answers.

### D2 -- "Annotate the key in place" is eliminated (tier 2, confirmed)

**Question.** One of the four candidate shapes was to keep writing
`permissions.defaultMode` and add a sibling comment marking it as a
niwa-internal signal.

**Evidence.** The published Claude Code settings schema has root
`additionalProperties: true` but `permissions.additionalProperties: false`
(lead-prior-art-inert-keys), so a sibling key inside the permissions block is
schema-illegal -- the exact placement the option needs. Separately, no project
was found that annotated a dead key it kept writing; extension namespaces
exist for live second-tool config, not tombstones. And the option does not
address the originating complaint at all: a reader still sees an
authoritative-looking permissions block that does not govern the session.

**Decision.** Eliminated. Not carried into crystallize as a live option.

### D3 -- "Move the signal into the `--settings` payload" is eliminated as the
fix, and recorded as a finding (tier 3, confirmed)

**Question.** `--settings` was measured to be the one document channel that
still honors a permissive `defaultMode`. Should the signal move there?

**Evidence.** There is exactly one `--settings` argv slot; repeated use was
measured on Claude Code 2.1.267 as last-wins, total replacement, silent, which
is worse than the two recorded declines (`ef24ee8`, `0981df7`) assumed. Remote
control occupies the slot today. `--permission-mode` already delivers this
exact signal for free and is a closed enum, so spending a contested slot on it
buys nothing (lead-settings-payload-channel).

**Decision.** Not the fix. The measurement that the channel works is recorded
as a finding for whoever later needs a permission-shaped signal that
`--permission-mode`'s closed enum cannot express -- a deny list, an allow list,
`additionalDirectories`. That is a different question and it will contend for
the slot.

### D4 -- Reframe from "stop writing the key" to "stop writing the values that
do not work" (tier 3, confirmed)

**Question.** The brief and the prior-art lead both reach for "stop writing
`permissions.defaultMode`". Is that the correct scope?

**Evidence.** Only the permissive values (`bypassPermissions`, `auto`) are
inert from project scope; `default`, `acceptEdits`, and `plan` carry no
documented restriction (lead-claude-code-scope-rule). `niwa watch` writes
`defaultMode: "default"` into the same instance-root file as the whole
mechanism of its operator-approval posture and hard-verifies it survived
(lead-drift-and-tests). The key has a live, legitimate resident.

**Decision.** The framing is narrowed. Whatever lands must not remove the key
from the document's vocabulary, only stop niwa's materializer from writing a
value there that cannot take effect.

### D5 -- The `--settings` single-slot guard is worth recommending regardless
(tier 2, assumed)

**Question.** Should a test asserting `--settings` appears at most once in the
assembled argv be part of whatever lands?

**Evidence.** The constraint is currently tribal knowledge recorded in two
commit messages. A clobbering second producer would leave no trace anywhere --
the discarded document is never even parsed (lead-settings-payload-channel).
The guard costs one test.

**Decision.** Recommended as a rider, not as the subject. Marked `assumed`
because it was not the question this exploration was asked and a reviewer may
reasonably want it filed separately.

### D6 -- A merged settings-document builder is not needed for this problem
(tier 3, confirmed)

**Question.** The brief asks whether this problem strengthens the case for a
single merged settings-document builder, since a sibling session is hitting the
same constraint from another direction.

**Evidence.** The file channel already has a builder (`buildSettingsDoc`); it
is the inline payload that is a bare `fmt.Sprintf` with one hardcoded document
(lead-settings-payload-channel). Since this problem's fix does not touch the
inline payload at all -- `--permission-mode` carries the signal -- this problem
contributes no requirement to a merged builder beyond the one-line
single-owner guard in D5.

**Decision.** Say so plainly rather than deferring: this problem does not
strengthen the case. It strengthens the case for the guard. Whether the sibling
constraint justifies the builder on its own terms is the sibling's call, and a
live measurement in this round's gaps may dissolve the constraint entirely --
if `--remote-control` composes with `--bg`, niwa can vacate the slot.

### D7 -- Round 2 runs, targeting measurement gaps only (tier 2, confirmed)

**Question.** Are the round 1 findings sufficient to crystallize?

**Evidence.** The shape question has narrowed cleanly to two candidates, but
three cheap measurements could each move the answer: whether restrictive
`defaultMode` values survive from project scope (decides whether `niwa watch`'s
ask posture is a second instance of the same bug), whether `--remote-control`
composes with `--bg` (decides whether the single-slot constraint persists), and
whether anything outside this repo reads the key (bounds the blast radius).

**Decision.** One more round, scoped to measurement rather than to new
territory.

## Round 2

### D8 -- The problem is a regression, not a documentation defect (tier 3, confirmed)

**Question.** Round 1 framed this as a false signal: a document making a claim
it cannot keep. Is that the right characterisation?

**Evidence.** A project-scope `bypassPermissions` does not fall through to the
layer below. It wins the scope merge and is then downgraded to `default`,
measured on 2.1.267 across a matrix of user-scope values
(lead-restrictive-mode-probe). A developer whose own settings say `acceptEdits`
gets `default` in every niwa instance. The written value therefore changes
behavior -- in the restrictive direction -- rather than doing nothing.

**Decision.** Reframe. The misleading document is the visible half; the
suppressed user posture is the half that costs something. Any fix scoped to
honesty alone is insufficient, and this raises the priority of the work.

### D9 -- `niwa watch`'s posture is sound and stays untouched (tier 2, confirmed)

**Question.** Round 1 flagged `niwa watch` as a possible second instance of the
same bug, since it writes `defaultMode: "default"` into the same file.

**Evidence.** Restrictive values are honored from project scope, measured both
from the headless init event and behaviorally, including a project-scope
`"default"` pulling a user-scope `acceptEdits` down to prompting with the write
actually denied (lead-restrictive-mode-probe). `VerifyReviewSettings` guards
something live.

**Decision.** Watch's ask posture is correct and out of scope. Its hard-deny
posture's *comment* is still wrong -- it claims to inherit a bypass that never
arrives -- but the resulting session is more restrictive than the comment
assumes, so this is a documentation defect rather than a security one. Record
it; do not fix it here.

### D10 -- The `--settings` slot has a phantom occupant (tier 3, confirmed)

**Question.** The brief presented the single `--settings` slot as a constraint
to design around, with remote control holding it and two recorded declines
against passing the flag twice.

**Evidence.** `--remote-control` composes with `--bg`, connects for real in a
backgrounded session with no terminal attached, throws the same internal switch
`remoteControlAtStartup` throws, and adds an optional session name the boolean
setting cannot express (lead-settings-slot-probe). The repeated-flag hazard is
real and worse than recorded -- last-wins, total, silent -- but the reason niwa
is in the slot dissolves.

**Decision.** Say so plainly rather than designing around scarcity. The slot is
available to whoever needs it next, once niwa switches to the flag. That
switch is a separate piece of work this exploration does not own, but the
finding belongs in whatever artifact lands.

### D11 -- Recommend stopping the round trip, not renaming the key (tier 3, confirmed)

**Question.** Round 1 left two live candidates: rename the key into niwa's
existing `keepAliveOnDispatch` family, or stop reading the materialized output
and feed the derivation from the effective config directly.

**Evidence.** The rename is one line plus a JSON tag and has in-repo precedent
twice over, but it preserves the round trip and answers only the honesty
complaint -- which D8 established is the smaller half of the problem. The
effective, vault-resolved config is already computed inside the same
`provisionInstanceFunc` call and discarded, so surfacing it through
`provisionResult` needs no re-resolve and follows the pattern `pipelineResult`
already uses for five other outputs (lead-instance-metadata-home). Every
external precedent found resolves this class of problem by recomputing from the
generator's own inputs rather than round-tripping through a foreign schema
(lead-prior-art-inert-keys). It also disposes of the `"ask"` -> `askPermissions`
defect without a separate fix.

**Decision.** Recommend the structural fix. Note that the two are not exclusive
-- the materializer must stop writing the permissive value regardless, and that
half is the one-line change -- so a plan can sequence the write-side fix first
and the read-side fix second.

### D12 -- Crystallize rather than run a third round (tier 2, confirmed)

**Question.** The scope file allows three rounds. Is a third warranted?

**Evidence.** Every gap remaining is either not load-bearing (managed-scope
behavior, which niwa never writes), not answerable without installing old
releases (the exact version boundary, where release notes already give a
citable answer), or a mechanism question whose observable outcome is already
settled (merged-then-sanitized versus sanitized-at-parse). No remaining gap
changes the recommendation.

**Decision.** Crystallize.
