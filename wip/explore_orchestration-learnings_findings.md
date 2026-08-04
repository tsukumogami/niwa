# Exploration Findings: orchestration-learnings

Round 1. Five leads: three delegated, two run directly.

| Lead | File |
|---|---|
| niwa file/skill distribution | `wip/research/..._r1_lead-niwa-distribution.md` |
| shirabe extension layering | `wip/research/..._r1_lead-extension-layering.md` |
| chain shape vs outcomes | `wip/research/..._r1_lead-chain-shape.md` |
| harness mechanics re-verification | `wip/research/..._r1_lead-harness-reverify.md` |
| transcript-only facts | `wip/research/..._r1_lead-transcript.md` |

## The finding that reorganises everything else

**A `docs/guides/` file in the niwa repo is not reachable from an arbitrary workspace.**
niwa is a binary. A workspace clones whatever repos its config names, and most workspaces
that use `niwa dispatch` will never have the niwa source tree on disk. So a reference doc
in `docs/guides/` is loaded by a niwa contributor, or by someone who followed a link — not
by a coordinator trying to reach a stuck worker at the moment it matters.

The only content that reaches every workspace is what niwa embeds in the binary and writes
out. Today that is exactly one file: `internal/workspace/rootskills/dispatch/SKILL.md`,
`go:embed`-ed at `internal/workspace/root_materializer.go:25-26` and written to
`<workspaceRoot>/.claude/skills/dispatch/SKILL.md` by `writeRootSkills`
(`root_materializer.go:189-223`).

This splits the work by *audience* rather than by kind:

- **Re-verifiable evidence** — the commands, the observed outputs, the version stamp —
  belongs in `docs/guides/`, where a reader can check it and where it is expected to age.
- **The operational recipe** — what to run when a session will not answer — has to be
  embedded, or it is not there when it is needed.

The task brief's hypothesis (reference doc for mechanics, execution path for judgment) is
right about the split and wrong about the boundary: a slice of the mechanics is
operational and has to ship.

## Load points, and when each is read

| Path | Reader | Read when | Ships how |
|---|---|---|---|
| `<workspaceRoot>/.claude/skills/dispatch/SKILL.md` | coordinator | the user asks to dispatch work | `go:embed`, written on root-scope `niwa apply` (`cli/apply.go:237-238`) and on `niwa init` in named/clone mode (`init.go:771`, `:1004`) |
| `<workspaceRoot>/.niwa/dispatch-briefs/_common.md` | **worker** | first tool call of every dispatched task, because every brief's "Read first" names it | nothing — hand-written, one machine |
| `niwa docs/guides/*.md` | human, niwa contributor | working on niwa, or following a pointer | in the repo, listed in niwa's `CLAUDE.md` |

Two different readers for the skill and `_common.md`. Collapsing them loses a load point:
the coordinator never reads `_common.md`, and the worker never reads the dispatch skill.

**Coverage gap to accept or fix:** `MaterializeWorkspaceRoot` runs only at root-scope apply
and on named/clone init. `niwa create` and `niwa dispatch` do not re-materialize the root
(`lead-niwa-distribution` §1). A workspace whose owner only runs instance-scoped applies
never gets updated shipped content.

## `_common.md`: how it can ship and still be overrideable

**The preservation guarantee is not in the way.** `preserveDispatchBriefs`
(`snapshotwriter.go:504-524`) defends the config-snapshot *swap*; `MaterializeWorkspaceRoot`
writes at a different moment, and `niwa apply` already orders the swap first
(`cli/apply.go:177`) and the materialize second (`:238`). A file niwa writes into
`.niwa/dispatch-briefs/` survives every later swap for free.

**The author's stated model does not transfer mechanically.** The shirabe pattern is an
`@path` import line at the top of a SKILL.md, expanded by Claude Code when it loads the file
as context. `_common.md` reaches the worker as the return value of a `Read` tool call, and
the lead verified experimentally that `Read` hands `@` lines back as literal text. An `@`
line in `_common.md` would be inert.

Also worth knowing before imitating it: as deployed in this workspace the shirabe mechanism
is collapsed. niwa's `[files]` table auto-appends a `.local` infix, so everything niwa
distributes lands in `<skill>.local.md` — the slot reserved for personal overrides — while
the committed team slot `<skill>.md` sits empty in all nine managed repos. Imitate the
design, not the deployment.

**Four mechanisms exist** (`lead-niwa-distribution` §4). Recommended: **B, a sentinel-section
merge**, modelled on `installWorktreeContextLayer` (`worktree_content.go:696-736`) and
`stripWorktreeContextSection` (`:869-875`) — an existing, tested strip-and-reappend pattern.
niwa owns a delimited block and rewrites it every apply; anything the workspace adds outside
the sentinel survives. Write-if-absent (A) freezes every workspace at the version it first
saw and niwa could never ship a correction. Plain overwrite plus a `_common.local.md` (C) is
less Go but pushes composition onto the reader and cannot carry a fix into a file the user
has edited. Cost of B: one embedded file, a ~30-line writer beside `writeRootSkills`, two
unit tests, one `@critical` scenario. No change to `snapshotwriter.go`.

## Chain shape: what the evidence actually supports

The correlation is checkable and the answer is more interesting than "full chain for hard
things." Full detail in `lead-chain-shape` §4-§6.

**Supported:**

- A change to a **persisted schema with no version field to gate reads on** takes the full
  chain (#2468 → #2484; the design's first decision is backfill semantics, and the chosen
  marker beat the obvious format-version bump on a counted 110-golden-file cost).
- When the **mechanism already exists in-tree and the work is applying it**, take `work-on`.
  Four briefs state this property; all four merged with no escalation.
- When **exactly one question is open and everything around it is settled**, take `work-on`
  and mandate `shirabe:decision` on that named question. Two cases, both merged.

**Not supported, and this is the useful part:**

- "The full chain is what surfaces a rejected direction" is **false**. #2491 and #2503 were
  bare `work-on` and both rejected the direction their issue proposed, on evidence.
  What the full chain uniquely produces is the **durable design doc** — verified: only the
  four full-chain PRs created a `DESIGN-*.md`. So the criterion is not "will the direction
  need rethinking" but "does the reasoning need to outlive the PR", which matters because
  squash-merge deletes everything else.
- **Diff size does not predict shape** and should not be back-fitted. Full-chain and
  `work-on` diffs overlap in both lines and files.
- Brief length is confounded: the short late briefs delegate 167 lines to `_common.md` while
  the long early ones carry it inline. Any criterion phrased as "how much the coordinator
  wrote" measures the refactor, not the work.
- Shape and date are **almost perfectly confounded** — every full-chain brief is Aug 1-2,
  every Aug 3 brief is `work-on`. A backlog explanation and a "the coordinator got more
  confident" explanation fit the same data equally.

**Counter-evidence worth carrying:** the two `work-on`-plus-decision PRs got the *worse*
review verdicts, both containing a false claim a reviewer had to catch. Weak (n=2), but it
runs against the instinct that more shaping is safer. And `verify-additional-dead-schema`
was dispatched `work-on`, came out at +994/-60 across 22 files, and was right to widen —
no criterion predicted it.

## Reviews: what the four reviews establish

All four reviewed PRs were `work-on`-shaped; no full-chain PR was reviewed, so "did shaping
produce cleaner PRs" **cannot be answered** from this data and should not be claimed.

What can: **three of four lead findings were false or unverified claims in prose, not
defects in code.** One was a defect in a design doc that would go stale the moment a sibling
PR merged. Only one was purely a code defect. Every one was caught by *running* something —
building the branch, applying a mutation, driving a probe test through the real code path.

That relocates the review standard's centre of gravity. At this quality level the residual
risk is not "the code is wrong", it is "the prose about the code is wrong" — and the PR body
is the least reliable part of the PR precisely because agent-written bodies are thorough.

## The failures, sharpened

**The unverified-state pattern is not "didn't check."** In all five cases a check ran. It
was the wrong check, or it was hours stale, and its result was reported as though it
answered the question asked (`lead-transcript` §1):

- queried a fixed list of known issue numbers, which by construction cannot discover
  anything newly filed, then reported "not filed yet" — and pushed to file duplicates three
  times;
- checked *file* disjointness between #2463 and #2464, reported *parallel-safety*; the
  coupling was `os.RemoveAll` on tool directories holding user data;
- checked that a retry process launched, reported that the session was *restarted*; it had
  been refused in 3 seconds by the stale-registration guard;
- checked for absence of the string "pending", reported an agent as stalled; it had written
  194 lines including the finding that caught #2513;
- and one claim with no check at all — "I've replaced the monitor" — stated one turn after
  the intention was formed.

"Verify before you claim" would not have caught any of these; all five felt verified. The
rule with teeth is: **state the check and when it ran, or say you did not check.**
"#2463 and #2464 touch no common files (`gh pr diff`, 14:20)" is falsifiable. "#2463 is
parallel-safe" is not.

**Delegation cuts both ways.** Three of four status agents stalled after writing a skeleton
while direct `gh` queries took under a minute and were right every time. But the fourth
finished and found the single most important fact of the session — 170 lines of uncommitted
symlink work stranded beside a green PR — because it ran a *different* check: a filesystem
sweep across worktrees. The distinguishing property is not difficulty or size. It is
**whether the answer is a lookup against a source you can query directly, or a sweep across
a surface you would not otherwise visit.**

**Uncommitted worktree state is invisible to every GitHub-side query.** PR state, checks,
reviews, commits — all showed #2513 finished and green while the fix sat uncommitted beside
it. It merged that way.

**A monitor seeded after the fact is silent and looks healthy.** Seeding from "everything
open right now" treats seven of ten in-flight PRs as pre-existing noise. Seed from an
explicit list of what you are waiting for.

## Harness mechanics: three corrections confirmed, plus four more

Verified against `claude 2.1.221` with two throwaway probe sessions
(`lead-harness-reverify`). Full commands and outputs in that file.

The three corrections the task brief named all reproduce — and the recipe is worse than the
brief thought:

1. **Resume directory.** Not the instance root, and also *not* the encoded projects
   directory (that re-encodes and fails the same way). The only reliable source is the `cwd`
   recorded inside the transcript. `claude agents` reports the *current* cwd for a live
   session and the *launch* cwd for a finished one, and the transcript may be under neither.
   Both directions were observed simultaneously on this machine.
2. **`timeout` kills mid-turn**, exit 124, `terminal_reason: aborted_streaming` — and
   **partial side effects still land**. The probe's log gained its line before the process
   died. Nothing rolls back.
3. **A SIGTERM-killed session keeps a stale registration.** The row reads
   `"state":"working"` with **no `pid` field**, the process is gone, and `--resume` is
   refused with "currently running as a background agent" in 3 seconds. A script that
   treats a fast non-zero exit as "already handled" concludes the message was delivered.

New, and one of them invalidates the old document's governing rule:

4. **`pid` absence is not a safe-to-resume signal.** The stale row above has no `pid` and
   refuses. The discriminator is `state`: `done`/`stopped` resume, `working` refuses whether
   the process exists or not.
5. **Permission mode is not inherited by a resumed turn.** A resume without the flag the
   session was launched with stalled on a prompt it could not answer — and cost eight times
   as much as the successful retry, because a resume reloads the whole context before it
   discovers it cannot act.
6. **`--bg` ignores `--session-id`** and conflicts with `--print`. A background session's
   uuid cannot be pinned; capture it at launch or lose the handle.
7. **`--from-pr` on a miss cost $0.0998** — the most expensive command in the probe run —
   to create an empty session and report success.

`--fork-session` still runs invisibly (new id, real tool calls, agent-view row count
unchanged) and `--from-pr` still fails open. Both reproduce unchanged.

## The artifact the brief nearly missed

The **work-in-flight table** is the thing the transcript shows being used most. The user
asked for it four separate times, including — of the brief for this very task — "did you
include instructions about the table we used to see all the work in flight and decide what's
next?"

Its columns were `PR | Session | Work state | CI`, and `Work state` is the one GitHub cannot
supply: "4 files edited, uncommitted", "clean, starting fresh", "committed". That column is
where #2513 would have been caught. The table also carries the queue and its coupling
reasons, which is what makes "what can run in parallel next" answerable.

Note it does **not** overlap shirabe's `/inflight`, which is deliberately scoped to the
current session's own PRs and "never issues a non-session `gh` listing." The coordinator's
table is about *other* sessions' work. Complementary, and worth saying so, so they do not
drift into each other.

## Cost, and the decision it should inform

One follow-up message to an existing worker cost **$8.83 over 40 turns**. Probe measurements
on a trivial session: $0.05 for a resume that stalled on a permission prompt, $0.0067 for
the one that did the work, $0.0998 for a `--from-pr` miss.

The trade-off was stated in the transcript and never turned into a rule. What makes waking a
session worth it is not the size of the fix — it is whether the session holds context you
would otherwise rebuild, and whether the change needs the author's judgment about their own
in-progress edits. The coordinator's own phrasing, on the stranded #2513 work: "they're the
author's uncommitted edits and I'd rather it verify its own diff than have me guess at
intent mid-edit."

## Open question for the author

Skill shape. The recommendation and its alternatives are in the convergence checkpoint.

## Decision: Crystallize
