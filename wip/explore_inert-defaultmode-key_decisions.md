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
