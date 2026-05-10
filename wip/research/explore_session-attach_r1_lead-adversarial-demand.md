# Lead: Is there evidence of real demand for this, and what do users do today instead?

## Findings

### 1. Is demand real?

**Confidence: Low**

The only durable artifact requesting this capability is issue #117 itself, authored 2026-05-10 by dangazineu with no comments, no reactions visible in the JSON, and no other commenters. The issue is well-formed and labelled `enhancement` + `needs-prd` (a maintainer label), which is the only signal of acknowledgement — and the author and the maintainer applying the label are the same person.

I found no other issues, PRs, design docs, or PRD content that independently asks for a human-driven attach/takeover primitive. Searches for "attach", "takeover", "human-in-the-loop", "pair-debug", "interactive session", "step into" returned only:
- One incidental hit in `docs/prds/PRD-cross-session-communication.md` mentioning "Human-in-the-loop approval before spawning workers" (unrelated to attach — that's about pre-spawn approval gating).
- A handful of unrelated mentions of "attach" referring to attaching closures, tokens, or config to objects.

No second voice, no maintainer-vs-reporter distinction, no upvotes/comments. This is a single-author proposal.

### 2. What do people do today instead?

**Confidence: High** (the issue body itself enumerates the workarounds, and the alternatives it cites are real surfaces in the codebase / docs)

The issue's own framing names today's options precisely:
1. **Let the worker complete on its own** — accept whatever the worker does, even if the trajectory is wrong.
2. **`niwa_send_message`** — documented in `docs/guides/cross-session-communication.md:187` and `docs/prds/PRD-cross-session-communication.md:451-452`. Per the PRD, this is explicitly "one-way", "the sender does not wait for a reply". The issue calls this "one-way and unacknowledged".
3. **Destroy the session and restart** — `niwa session destroy` exists (recent commits 24b5b67 and e154049 reworked it). This loses worktree state and conversation history.

Adjacent mechanisms exist but don't solve the same problem:
- `niwa_ask` exists for synchronous question/answer (`docs/prds/PRD-cross-session-communication.md`), but it's an MCP-driven coordinator tool, not a human-takeover primitive.
- Issue #114 (`niwa_redelegate`) proposes re-firing tasks without rewriting bodies — also a programmatic path, not an interactive takeover.
- Issue #92 (`niwa_ask` to live coordinator routes wrongly) confirms that the existing inter-session-communication surface has gaps even for programmatic callers.

The user can in principle find the worker's `claude_conversation_id` in session metadata (`docs/guides/sessions.md:85`) and run `claude --resume <id>` manually in the worktree, but that bypasses any locking against the mesh and is undocumented as a recovery path.

### 3. Who specifically asked?

**Confidence: Low**

- **Issue #117** — author: dangazineu (Dan Gazineu, project owner per recent commits). Single-author proposal, zero comments.
- The "Related" section cross-references #108, #109, #111, #112 — all authored by dangazineu within the same session-lifecycle problem cluster (2026-05-09 / 2026-05-10).
- No PR references. No external contributor voices. No comments from other maintainers.

I did not find any second author, comment, or upstream user request that asks for the same capability.

### 4. What behavior change counts as success?

**Confidence: Medium** (the issue itself states acceptance criteria, but no independent corroboration)

The issue body provides a clear "Acceptance Shape" section the PRD would refine:
- `niwa session list` discovery scoped to current workspace instance.
- `niwa session attach <session_id>` that locks, launches `claude --resume`, releases on exit.
- `--force` flag for SIGTERM-on-running-worker.
- New session state visible via `niwa_list_sessions` distinguishing the attach lock from `active`/`ended`/`abandoned`.
- niwa-mesh skill documentation describing the human-in-the-loop pattern.

It also provides three locked-in behavior contracts (concurrent-worker waits by default; discovery is per-workspace; mesh queue invisible inside the lock).

These are author-stated criteria, not reviewer-validated or maintainer-jury'd. Confidence is Medium because the criteria are specific and testable, but they originate from the same person who proposed the feature.

### 5. Is it already built?

**Confidence: High (absent — it is not built)**

`internal/cli/session.go` defines only `niwa session list` (lifecycle-filtered with `--repo`/`--status`, otherwise deprecated alias for `niwa mesh list`). There are sibling files `session_lifecycle_cmd.go`, `session_register.go`, `session_test.go`, `session_register_test.go` — none mention attach or takeover.

`grep -rn "attach\|resume" internal/cli/session*.go` returned no relevant hits. No `attach.go`, no `session_attach.go`, no `takeover.go`. No partial scaffolding.

The infrastructure that *would* be needed exists in pieces:
- `claude_conversation_id` is persisted on sessions (per `docs/guides/sessions.md:85`) for `--resume`, used today for sequential workers within the same session (`docs/prds/PRD-mesh-session-lifecycle.md:300`).
- Worktree paths are tracked in session metadata.
- A daemon owns each session's worker spawn lifecycle.

But there is no lock primitive against mesh re-claim, no human-launch wrapper, and no `niwa session attach` cobra command.

### 6. Is it already planned?

**Confidence: Low (planned only as far as #117 itself; not on a converged roadmap)**

- No `docs/roadmaps/` directory exists in niwa. (`docs/` has `designs/`, `guides/`, `prds/` but no `roadmaps/`.)
- No design doc in `docs/designs/current/` covers attach. The closest is `DESIGN-mesh-session-lifecycle.md`, which does not mention attach/takeover/resume-by-human.
- No PRD in `docs/prds/` covers it. `PRD-mesh-session-lifecycle.md` covers create/destroy/list and the worker-`--resume` continuity model but not human attach.
- The issue references "ROADMAP-koto-observability F6 (hosted relay)" as a downstream consumer, but I could not find a ROADMAP-koto-observability file in the niwa repo (this likely lives in the koto repo, which is out of scope for this investigation).

Issue #117 is labelled `needs-prd`, which is the project's signal that the work is queued for requirements definition but not yet committed. The label itself is the only "plan".

## Implications

For the crystallize phase:
- The proposal is well-articulated but the demand signal is single-author. The exploration cannot lean on "users are asking for this" — it has to lean on the merit of the problem statement: today's recovery options for a misbehaving worker are genuinely poor (let it ride / fire-and-forget message / destroy-and-restart), and that is verifiable in the docs.
- If a PRD proceeds, the open questions enumerated in the issue are the right scope. None of them are answered elsewhere in the repo.
- The dependency on a transcript-persistence path that `claude --resume` can hit given only a session ID is a real engineering risk the PRD should investigate first; the existing `claude_conversation_id` mechanism is used for worker-to-worker continuity, not for human-resume-of-worker-transcript, and the equivalence has not been validated in any artifact.
- Adjacent issues (#108, #109, #111, #112) tell a consistent story: the mesh's session lifecycle has multiple gaps that this proposal partially addresses by giving the human a direct intervention surface. Pursuing #117 may pressure-test those other gaps.

## Surprises

- Zero comments on a feature issue this detailed. Either the issue was just filed (filing date 2026-05-10, today is 2026-05-09 per the prompt — actually filed today/yesterday) and hasn't had review time, or the project doesn't have many active contributors beyond the author. The repo's recent commit history is dominated by a single author, suggesting the latter.
- The issue mentions "ROADMAP-koto-observability F6" but no such roadmap is in the niwa repo. The author is reasoning across multiple repos that I cannot inspect in this scope.
- The author themselves wrote a "Why `needs-prd` not `needs-design`" justification — unusual self-awareness that the proposal isn't yet implementation-ready, which is a positive signal about the proposal's quality but doesn't add demand evidence.

## Open Questions

- Is there a koto repo issue or design doc that requests the equivalent capability from the relay/observability side? The cross-reference suggests yes; I cannot verify.
- Has the author actually hit this in real workspace operation (i.e., has there been a destroyed session that should have been attached instead)? The issue is framed as a need but not anchored to a specific failed run.
- Are there Telegram, Slack, or external user reports asking for this? Not visible in the repo.
- Does Claude Code's `--resume` actually work given only a session ID stored elsewhere, with full tool-call replay? The PRD scoping question; would also be a demand-validation artifact if there's a public Anthropic/Claude Code change-log or example of this pattern.

## Calibration

**Demand not validated** — not "demand validated as absent".

Reasoning:
- The signal for demand is one well-formed issue authored by the project owner, with no second voice, no comments, no upvotes, no PRs, no other reporters. That is Low/Absent on questions 1 and 3.
- I found **no positive rejection evidence**. There is no closed PR with a maintainer "we won't do this" comment, no design doc that scoped the feature out, no PRD that evaluated and dropped it. The absence is uniform across the repo's durable artifacts.
- Workarounds (Question 2) are well-evidenced and confirm the *problem* is real — the existing recovery options are limited — but the absence of independent voices asking for *this specific solution* keeps demand for the proposed solution uncorroborated.
- The proposal is in a state where another round of validation, or direct user clarification (the author themselves: "have you actually hit this? on what session? what did you do instead?"), would likely surface concrete instances. The repo alone cannot.

This is the "another round may surface what the repo couldn't" state, not the "evaluated and rejected" state.

## Summary

Demand is not validated: issue #117 is a well-articulated, single-author proposal with zero comments and no corroborating issues, PRs, or design docs requesting the capability. The problem it addresses is real and visible in the codebase — today's recovery options are limited to letting workers complete, sending one-way `niwa_send_message`, or destroying and restarting sessions — but no second voice asks for this specific attach-and-takeover solution. The biggest open question is whether the author has personally hit this in practice or other users have reported it outside the repo (Telegram, koto roadmap, etc.); the niwa repo alone shows no rejection evidence either, so this is "demand not yet validated" rather than "demand validated as absent".
