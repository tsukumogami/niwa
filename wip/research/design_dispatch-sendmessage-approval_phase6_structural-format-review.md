# Phase 6 structural-format review: DESIGN-dispatch-sendmessage-approval

Reviewed: `docs/designs/DESIGN-dispatch-sendmessage-approval.md` (697 lines)
Against: shirabe 0.19.2-dev `skills/design/references/design-format.md`,
`references/quality/considered-options-structure.md`,
`skills/writing-style/SKILL.md` and `rules.yaml`.

Overall verdict: PASS with changes recommended. The validator is clean and
the skeleton matches the format reference exactly. The substantive problems
are one divergence between the frontmatter `decision` and the Decision
Outcome (the review-session messaging deny hook), a Context section that
re-narrates PRD requirements the reference says to cite, a few Considered
Options alternatives that the PRD had already ruled out, and Consequences
negatives that have no paired mitigation.

## 1. Section presence and order

All nine required sections are present, each exactly once, in canonical
order:

| # | Section | Line | Words |
|---|---------|------|-------|
| 1 | Status | 42 | 1 |
| 2 | Context and Problem Statement | 46 | 534 |
| 3 | Decision Drivers | 105 | 385 |
| 4 | Considered Options | 147 | 1305 |
| 5 | Decision Outcome | 339 | 412 |
| 6 | Solution Architecture | 390 | 711 |
| 7 | Implementation Approach | 479 | 302 |
| 8 | Security Considerations | 553 | 989 |
| 9 | Consequences | 652 | 361 |

The `## Status` first non-blank line is the bare word `Proposed`. No
`## Absorbed ...` sections, and none are needed (no `absorbed:` frontmatter).
Context-aware sections: none required. There's no `spawned_from:`, the design
isn't strategic-altitude, and the decision space doesn't depend on external
products beyond Claude Code itself, which Context already covers as measured
facts. The document doesn't carry an Implementation Issues table.

Result: PASS.

## 2. Frontmatter

Fields in order: `schema`, `status`, `problem`, `decision`, `rationale`,
`upstream`, `user_visible_surface`, `decision_provenance`.

- Required fields `schema`, `status`, `problem`, `decision`, and `rationale` are
  all present. `problem`, `decision`, and `rationale` are each a single
  paragraph written as a YAML literal block scalar (`|`). PASS.
- Required fields come in the canonical order, and the optional fields follow
  in the reference template's order (`upstream`, then `user_visible_surface`).
  PASS.
- `upstream: docs/prds/PRD-dispatch-sendmessage-approval.md` resolves to a file
  in this repo. It's public and isn't a `wip/` path. PASS.
- `user_visible_surface: true` is correct: the design adds a flag, a
  machine-config key, stderr lines, a `niwa list` field, and a new guide.
- `decision_provenance: inline-resolved` isn't listed in `design-format.md`'s
  frontmatter spec. It is established practice, though: shirabe's own
  designs and three current niwa designs use it, and the validator accepts it.
  There is no canonical position for it. Sibling niwa designs put it either
  right after `upstream` or last. Appending it last, after the documented
  optional fields, is a reasonable choice. No change needed. (Upstream
  observation: the format reference should document this field.)
- Frontmatter `status: Proposed` matches the body's `## Status` line. PASS.

Content consistency between the frontmatter and the body:

- **Divergence (should fix).** The frontmatter `decision` ends with "`niwa
  watch` review sessions gain a hook denying cross-session messaging, so a
  contained reviewer can't hand instructions to a worker that now accepts
  them." The Decision Outcome, which the reference says "mirrors the
  frontmatter `decision` field", never mentions that hook: not in the
  `Chosen: 1 + 2 + 3 + 4` line, the Summary, or the Rationale. The hook shows
  up only in Decision Drivers (D12), Components, Phase 7, Security, and
  Consequences. The reference treats this kind of divergence as a sign that
  one of the two is stale.
- The frontmatter `rationale` says "keeps the dispatch path agent-neutral under
  its AST scan". "its" grammatically points at the capability row. Suggest
  "under the dispatch-path AST scan".

## 3. Section-altitude conformance

**Context and Problem Statement: re-narrates the PRD (should fix).** The
reference says this section should state the problem fully and then "cite
everything the upstream already says ... rather than restating it". The first
three paragraphs and the three Claude Code facts are proper problem
statement: they describe the current argv shape, why `crossSessionInbound`
needs the launch flag, the last-wins `--settings` behavior, and
`respawnFlags`. But the paragraph at lines 84-96 ("The rest of the PRD adds
surface around the launch...") paraphrases R7 through R16 one by one, about
190 words. Lines 59-65 do the same for R1 through R6. Those restatements
duplicate the PRD, and Decision Drivers already cites the R numbers where
they matter.

**Decision Drivers: PASS.** Each driver is a real constraint tied to code or
measured behavior, numbered D1 through D12, and the Considered Options
section cites them. None of them introduces a requirement. D11 ("One pull
request") is a sequencing fact rather than a force on the design space, and it
would sit more naturally in Implementation Approach. That's minor.

**Considered Options: altitude slip in Decisions 3 and 4 (should fix).** PRD
R11 already fixes the marker's name, its location ("niwa's configuration
directory"), that it's created only for a terminal, that it isn't
`config.toml`, and that it's separate from the per-instance one-time notices.
PRD R9 already fixes that the `niwa list` field is always present and not
gated on liveness. So these Decision 3 alternatives:

- "A 'seen' key in `config.toml`"
- "The per-instance one-time notices"
- "Mark it shown whenever it's printed"

and these Decision 4 alternatives:

- "Gate it on liveness like keep-alive"
- "An optional list field"

are requirements the PRD already settled, argued a second time as if they
were design options. That is PRD-altitude content in a DESIGN. See section 5
for the fix.

**Decision Outcome: PASS on altitude**, but see the divergence in section 2.

**Solution Architecture: PASS.** It names the components, interfaces, and
data flow at the right level. The full explanation string under Key
Interfaces (one line of about 200 words) is design-altitude, because R10
lists the explanation's content and leaves the wording to the design. It
does make Key Interfaces (328 words) the heaviest subsection. Consider
keeping the exact wording only in the code and the guide, and listing just
the sentence structure here (content points plus the terminal-dependent last
sentence).

**Implementation Approach: PASS, with one wording tension.** The phases list
file-level deliverables, not atomic issues, which is the right altitude.
Phase 5's list of test cases leans toward PLAN detail but is acceptable. The
intro says "none has to merge before another", while Phases 3, 4, and 5 say
"Depends on Phase N". Both are true, because the phases are commits in one
PR. Say that explicitly, for example: "Phases are commits in one pull
request; the dependencies below are commit order, not merge order."

**Security Considerations: PASS on altitude.** The "Recommended posture"
paragraph is really guide content, and it partly repeats Mitigations. It's
fine to keep, but it's the first candidate to cut if the section needs
trimming.

**Consequences: see section 5 below on mitigation pairing.**

## 4. Budget-vs-spec

`design-format.md` defines no numeric per-section budgets. The only budgets
are shape guidance in `considered-options-structure.md`: 1-3 paragraphs of
decision context, 1-2 sentences per alternative, and 2-3 paragraphs of
Summary. The ">50% over budget" rule therefore can't be applied numerically.
Measured against that shape guidance:

- Decision context paragraphs (61-98 words each) are within budget.
- Alternatives mostly run 2-4 sentences. Decision 1's "Widen the
  remote-control helper" and "A per-instance settings file passed by path"
  are 3-4 sentences each, which is over the 1-2 sentence guidance but carries
  real rejection reasoning. Not flagged.
- The Decision Outcome Summary has three paragraphs (275 words), within
  budget.
- Relative outliers are Considered Options (1305 words) and Security
  (989 words). The Considered Options length is partly the PRD-settled
  alternatives from section 3. Removing them cuts about 150 words and fixes
  the altitude issue at the same time. Security's length comes from genuine
  threat content. Not flagged.
- The Context paragraph that restates R7-R16 is the clearest case of content
  at the wrong altitude (it belongs in the PRD, which already has it).

No section is flagged as more than 50% over a documented budget.

## 5. Considered Options structure

Every decision has a context paragraph, key assumptions, a `#### Chosen:`
block, and `#### Alternatives Considered`, matching the template.

| Decision | Alternative | Genuine? |
|----------|-------------|----------|
| 1 | Second `--settings` element | Yes. Rejection traced to D2 and measured behavior. |
| 1 | Widen the remote-control helper | Yes. A real trade-off, D3. |
| 1 | Per-instance settings file by path | Yes. The reaped path breaks `claude respawn`, a concrete failure. |
| 1 | Write the key into instance settings | Yes. Measured: project/local values can only make it stricter (D1). |
| 2 | Gate on settings flag spelling | Yes. The strongest alternative, with a concrete failure mode. |
| 2 | Reuse the `RemoteControl` row | Borderline. One-sentence rejection, but the coupling argument holds. |
| 2 | Name the agent in the dispatch path | No. It's not viable (the AST scan fails the build), so it's filler. |
| 3 | "Seen" key in `config.toml` | Real reasoning, but R11 already rules it out. |
| 3 | Per-instance one-time notices | Real reasoning, but R11 already rules it out. |
| 3 | State or cache directory | Yes. The one live alternative. |
| 3 | Mark it shown whenever printed | Real reasoning, but R11 already rules it out. |
| 4 | Gate on liveness | R9 already rules it out. |
| 4 | Separate audit log | Yes. |
| 4 | Optional list field | R9 already rules it out; the rejection just cites R9. |

Every decision has at least one genuine, non-strawman alternative, so the
minimum bar is met. Recommendations:

- Drop "Name the agent in the dispatch path", or fold it into the Decision 2
  context as a constraint ("D4 rules out naming the agent").
- Reframe Decision 3 around the question that's actually open: which
  directory counts as "niwa's configuration directory"
  (`filepath.Dir(config.GlobalConfigPath())` versus `config.GlobalConfigDir()`
  versus a state or cache directory), and how the create is made race-safe.
  Keep the state-dir alternative and promote the `GlobalConfigDir()` point
  (now in the Chosen text) to an alternative. Replace the three
  already-settled alternatives with one sentence citing R11.
- Do the same for Decision 4. Cite R9 for the always-present, not-liveness
  shape, and add a real alternative if there is one. For example, derive the
  field at `niwa list` time from Claude Code's job record `respawnFlags`
  instead of storing it. That's rejectable because the job record is owned
  by Claude Code and outlives neither a reap nor a format change.
- Missing decision: the review-session messaging deny hook (D12, Phase 7) is
  a real design choice with no Considered Options entry. Plausible
  alternatives: widening the existing `egressDenyMatcher` to include
  `SendMessage`, which would only apply in sandbox mode; applying the deny
  only in the operator-approval posture; or deferring it to the dispatch
  containment follow-up. Add a short Decision 5, or at minimum mention the
  hook in the Decision Outcome Summary and add it to the `Chosen:` line.
  That also clears the frontmatter divergence from section 2.
- Several alternatives have the reason but not the "Rejected because"
  phrasing the template uses ("Widen the remote-control helper", "per-instance
  settings file", "flag spelling", "Reuse the RemoteControl row",
  "per-instance notices", "state or cache directory", "separate audit log").
  This is cosmetic. The reasoning is present.

## 6. Writing style

Mechanical checks:

- Banned words from the full `rules.yaml` list (organizing, verbs,
  descriptors, abstract nouns, adverb openers) have zero matches. The
  validator agrees.
- Em dashes: 0. Contractions are used throughout. Headings are sentence
  case, apart from the required section names.

Judgment-only patterns:

- Line 657, Positive consequences: "which removes the cause of both rounds of
  observed holds for the receiving side". "Both rounds" has no antecedent
  anywhere in the design (or the PRD). It points at history a public reader
  never saw. Rewrite as, for example, "which removes the class-mismatch hold
  on everything a worker receives."
- Lines 386-388, Rationale: "The marker's terminal rule trades a repeated
  explanation for agents that only ever dispatch through agents, which is
  documented, against losing the explanation unseen, which isn't
  recoverable." "Agents that only ever dispatch through agents" should be
  "developers who only dispatch through agents", and the two "which"
  clauses make the sentence hard to parse. Suggest: "The terminal rule
  accepts a repeated explanation for developers who only dispatch through
  agents, a documented cost, to avoid the explanation being consumed unseen,
  which can't be undone."
- Frontmatter `rationale` has the "its AST scan" antecedent problem noted in
  section 2.
- No low-density or empty-conclusion paragraphs. Security's opening "Everything
  below follows from that." is doing real work.

Public-repo checks: no `wip/` references (grep clean). No private repos,
internal tooling, or private skill names. The only URL is the public
`github.com/tsukumogami/niwa` guide link. PASS.

## 7. `shirabe validate`

```
shirabe validate --format json --visibility=public docs/designs/DESIGN-dispatch-sendmessage-approval.md
outcome: clean, errors: 0, notices: 0, advisory notes: none; exit 0
```

## Additional observations

**Consequences: mitigations aren't paired with every negative (should fix).**
The reference says "Mitigations are paired with each negative consequence."
There are eight Negative items and five Mitigations. These negatives have no
mitigation and no explicit "accepted" note:

- "Switching it off keeps Claude Code's default, which doesn't isolate a
  worker from same-class peers."
- "A worker steered by an injected message can dispatch more accepting
  workers, and a workspace that relocates `XDG_CONFIG_HOME` or `HOME`..."
  Security names the follow-up (rejecting those variables in session
  environment tables), but Mitigations doesn't.
- "Review sessions lose the ability to message other sessions, which nothing
  uses today." This could be marked as an accepted cost with no mitigation
  needed.

Either add a mitigation for each, or add an "Accepted without mitigation"
line naming them and why.

**Code-citation accuracy (minor, outside the format rubric).** Context line 50
says "step 9a derives `--permission-mode bypassPermissions`". In
`internal/cli/dispatch.go`, `(9a)` is model resolution and the bypass
derivation is labeled `(9a-derive)`. Use "step 9a-derive" or drop the step
number. The other citations check out: 9c is the remote-control block at about
566-601, step 11 is the mapping write at 756, `watch.go:581` and `:844` are
`dispatchLaunch` calls, `containment.go:18` is `egressDenyMatcher`, and
`list.go:117` is `annotateFromSessionMappings`. Hard-coded line numbers drift
over time, so consider citing function names only.

## Recommended edits, in priority order

1. Put the review-session messaging deny hook into the Decision Outcome
   (the `Chosen:` line and one Summary sentence), ideally backed by a short
   Decision 5 with real alternatives. This fixes the frontmatter divergence.
2. Replace the Context paragraph at lines 84-96, and the R1-R6 paraphrase at
   lines 59-65, with a short citation such as "The PRD's R1-R6 define the
   precedence and delivery requirements and R7-R16 the audit, record,
   explanation, and exclusion surface; this design meets them as follows."
3. Reframe Decisions 3 and 4 so the alternatives are ones the PRD left open.
   Cite R11 and R9 for the settled parts.
4. Pair every Consequences negative with a mitigation or an explicit
   acceptance.
5. Drop the "Name the agent in the dispatch path" alternative.
6. Fix the three prose issues: "both rounds of observed holds", the
   Rationale's "agents that only ever dispatch through agents" sentence, and
   "its AST scan" in the frontmatter rationale.
7. Clarify in Implementation Approach that the phase dependencies are commit
   order within one PR. Optionally move D11 there.
8. Minor: correct "step 9a" to "9a-derive".
