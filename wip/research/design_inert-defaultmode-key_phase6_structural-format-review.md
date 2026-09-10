# Phase 6 Structural-Format Review: DESIGN-inert-defaultmode-key

Document under review: `docs/designs/DESIGN-inert-defaultmode-key.md` (niwa, public repo)
Reference: shirabe 0.19.2-dev `skills/design/references/design-format.md`
Style source: shirabe 0.19.2-dev `skills/writing-style/rules.yaml`

## 1. Section presence and order

All nine required sections are present as level-2 headings, in canonical order:

| # | Required section | Line | Present / in order |
|---|------------------|------|--------------------|
| 1 | Status | 32 | yes |
| 2 | Context and Problem Statement | 36 | yes |
| 3 | Decision Drivers | 106 | yes |
| 4 | Considered Options | 145 | yes |
| 5 | Decision Outcome | 334 | yes |
| 6 | Solution Architecture | 395 | yes |
| 7 | Implementation Approach | 535 | yes |
| 8 | Security Considerations | 636 | yes |
| 9 | Consequences | 722 | yes |

FC04 and FC15 would pass. No extra level-2 sections sit between required ones. The first non-blank line under `## Status` is the bare word `Proposed`, so FC03's body-side shape is right.

Context-aware sections: `spawned_from` isn't set, so no Upstream Design Reference is needed. This is a tactical design with no child designs, so Required Tactical Designs doesn't apply. The decision space is internal to niwa, not a question of industry conventions, so Market Context isn't needed. Its absence is correct.

No Implementation Issues table is present, which is correct because the PLAN owns it.

## 2. Frontmatter

Declared order: `schema`, `status`, `upstream`, `problem`, `decision`, `rationale`, `decision_provenance`.

Canonical template order: `schema`, `status`, `problem`, `decision`, `rationale`, then the optional fields (`upstream`, `spawned_from`, `motivating_context`, `user_visible_surface`).

- The required fields `status`, `problem`, `decision`, `rationale` appear in canonical relative order, and `schema: design/v1` comes first. FC01 and FC02 would pass.
- `problem`, `decision`, and `rationale` each use a YAML literal block scalar (`|`) and each is one paragraph, as the reference prescribes.
- Frontmatter `status: Proposed` matches the body Status line `Proposed`, so FC03 passes.
- `upstream` comes before `problem`. The reference template puts every optional field after `rationale`, so this placement is inconsistent with it. It doesn't break anything, since validators key by name, but it departs from the documented shape.
- `upstream` is a scalar path to a public PRD in the same repo (`docs/prds/PRD-inert-defaultmode-key.md`). That's a supported shape and safe for a public repo.
- The reference's field list doesn't document `decision_provenance: inline-resolved`. Putting it last, after the required fields, fits the optional-after-required convention, but the reference gives no canonical slot to check it against. It comes from the /design workflow, so it's tolerated, just not specified here.
- `user_visible_surface` is absent. The design changes user-visible CLI behavior: the dispatch argv, the unknown-value error text, and a new stderr warning. It also edits two `docs/guides/*` files in Phase 5. Without the field, /plan falls back to scanning the body for `docs/guides/*`, which will find them. The reference still asks designs with user-facing surface to set it explicitly.
- `spawned_from` and `motivating_context` are absent. That's appropriate, because there's no parent design and the problem statement already carries the trigger (the Claude Code 2.1.257 behavior change).

Frontmatter-to-body mirroring: the `decision` field matches Decision Outcome, covering instance state, the mapping function, the explicit builder parameter, and the test split. The `rationale` field matches the rejection logic in Considered Options (tamper resistance, reuse of the pipeline-state pattern, the explicit watch value, and step ordering). No drift.

## 3. Section-altitude conformance

**Context and Problem Statement.** It states the technical problem, not a smuggled solution, and it stands alone. But it restates PRD requirements in prose instead of citing them by number. Examples: "The PRD requires that no generated document ever carries...", "A `bypass` document must carry no `permissions.defaultMode`...", "The PRD requires the decision to come from the instance's effective declared posture...", "The flag must reach every form of dispatch...", "An operator's explicit `--permission-mode` must win...". These are attributed to the PRD, so they aren't new requirements and the section hasn't climbed to PRD altitude. Still, the reference's quality guidance says to "cite everything the upstream already says -- requirements by their numbers ... rather than restating it." Decision Drivers then restates the same content with R numbers attached, so the reader reads each requirement twice. The fix is to keep the defect narrative in Context and replace the requirement sentences with R-number citations.

**Decision Drivers.** These are real forces, not filler, and most cite R numbers. They're bolded bullets rather than numbered D1, D2, and so on, and Considered Options cites R numbers rather than drivers. The reference calls numbering "often" done in one place and says "Numbered" in its quality guidance, so this is advisory. Two drivers ("Smallest diff that fits the existing grain" and "No second `--settings` producer") are design constraints with no PRD citation. That's fine for drivers, because they constrain the solution rather than add product requirements.

**Considered Options.** There are four decisions, each with a Chosen option and at least one alternative whose rejection traces to concrete weaknesses: the fake provisioner reintroduces hand-written input under R17, duplicated precedence can drift, the tamper cases fail R3, a vault value has no plaintext at load time, and so on. None read as strawmen. "Change the table's values" is the thinnest rejection, since it rests on readability of a sentinel, but it gives a concrete reason.

**Decision Outcome.** It names the combined choice, mirrors the frontmatter, and gives a PLAN author enough to decompose. Its Summary, though, overlaps heavily with Solution Architecture's Components. The second and third Summary paragraphs repeat the mapping behavior, the degraded-read behavior, and the builder parameter, and the last paragraph repeats Decision 3's test list. That's acceptable altitude, just redundant.

**Solution Architecture.** The altitude is right: components with file locations, signatures under Key Interfaces, and a numbered Data Flow. It's concrete enough to sketch. One small gap: Data Flow step 1 lists the precedence as "workspace overlay, workspace, personal overlay" and leaves out `[instance.claude.settings]`, which only arrives through `MergeInstanceOverrides` in step 3. A cold reader could take step 1 as the full precedence chain.

**Implementation Approach.** It names five phases, gives explicit sequencing ("the reader stops depending on the file before the producer stops writing it"), and lists file-level deliverables. That's phase altitude, not atomic issues. Phase 3's bulleted list of six new tests, together with the similar list in Decision 3, is the closest the document gets to PLAN altitude. Each bullet is a test to write, not an issue, so it doesn't cross the line, but a PLAN author will likely turn these straight into acceptance criteria. Phase 1 says "no output changes", which isn't quite true: `.niwa/instance.json` gains `claude_permissions`. If the characterization goldens hash `instance.json`, Phase 1 changes them, which conflicts with Phase 3 being the phase that regenerates goldens. That's worth checking and stating.

**Security Considerations.** It names attack vectors (a tampered or deleted settings file, a writable state file, vault plaintext in errors, watch inheriting a derived flag, per-repo `ask` not restraining workers), the real defenses, and the residual risks: containment for dispatched workers is carried over, and the state file is trusted only right after provisioning. The altitude is appropriate.

**Consequences.** Positive, Negative, and Mitigations are all present. The five negatives pair one-to-one, in order, with the five mitigations. The negatives are honest, including the loss of file-borne bypass on Claude Code older than 2.1.257 and a corrupted `instance.json` costing a worker its bypass.

No new product requirements are introduced. Behaviors the design adds, such as the vault-safe error text and the stderr warning on an unreadable state file, are design decisions that serve cited requirements.

## 4. Budget-vs-spec

The reference documents only one budget: one paragraph each for `problem`, `decision`, `rationale`, and `motivating_context`. All three present fields fit it. It gives no word or paragraph budget for any body section, so the >50% overshoot test has nothing to measure body sections against, and no budget finding can be raised.

For information, here are approximate prose sizes: Context about 730 words, Decision Drivers about 400, Considered Options about 1,900, Decision Outcome about 620, Solution Architecture about 1,300, Implementation Approach about 900, Security Considerations about 900, Consequences about 420. The two places where length reflects content belonging elsewhere, rather than healthy detail, are the ones already noted under altitude: PRD requirements restated in Context, and the Decision Outcome Summary repeating Solution Architecture and Decision 3. Trimming both would cut roughly 300 to 400 words without losing information.

## 5. Writing style

**Banned word list.** I scanned all categories and found no hits (no tier, robust, comprehensive, leverage, facilitate, highlight, enhance, navigate, Additionally, Moreover, and so on).

**Em dash density.** No em dashes in the prose. Passes.

**Emojis / AI attribution.** None.

**Judgment-only rules:**

- *Vague attribution without citation.* "From Claude Code 2.1.257, `bypassPermissions` no longer takes effect from a project or local settings file, and `auto` stopped at 2.1.142. Measured on 2.1.267, the ignored value wins the settings merge and is downgraded to `default`..." (Context, lines 51-55). These are claims about an external product's behavior with no source: no changelog reference, no PRD section, and no statement of who measured it or how. The whole design rests on these facts, so they need a citation, at least to the PRD section that records the measurement.
- *This/that/these without antecedent.* I checked every sentence-initial "That", "This", and "These" ("That's the hand-written-input pattern R17 forbids", "That's acceptable here for three reasons", "That includes the personal overlay", "That value is passed", "That file is now valid", "That includes the containment settings"). Each has a clear antecedent in the preceding sentence. No findings.
- *Undefined shorthand (related to antecedent clarity).* `S1-S9` first appears in Decision 3 and is never defined in this document. A cold reader can't tell that it means the PRD's posture-source scenarios. "step 9a", "(9c)", and "(9d)" in Solution Architecture point at step numbers inside `runDispatch` comments that the document doesn't explain either.
- *Empty conclusions / low information density.* The Rationale's opening sentence ("The four choices share one principle...") adds a unifying principle, so it isn't empty. The same-process trust statement, though, appears three times almost word for word: Decision Outcome ("The derivation is valid only for an instance the calling process just provisioned"), Solution Architecture ("The recorded value is trusted only for an instance the calling process just provisioned. A future caller..."), and Security Considerations ("The recorded value is trusted only for an instance the calling process just provisioned. Any future feature..."). Stating it once in Security, which is where the trust argument lives, and referring back to it elsewhere would remove the repetition.
- *Forced rule of three.* "three coupled technical defects" (three genuine paragraphs), "three moving parts and one deletion" (a genuine count), "three reasons" (three genuine bullets). No findings.
- *Synonym cycling.* "posture" (niwa vocabulary) and "mode" (Claude Code vocabulary) are kept deliberately distinct and used consistently. "settings document" and "settings file" alternate for the same concept, but the effect is mild. No finding.
- *Landscape used figuratively; from X to Y.* No occurrences.

**Public-repo check.** I found no private repo names (vision, tools, coding-tools, dot-niwa-overlay), no private paths, and no issue numbers from private repos. "Workspace overlay" and "personal overlay" are niwa product concepts, not references to the private overlay repo. There are no path-shaped `wip/` references in the document.

## Verdict

PASS

## Findings

1. **minor: frontmatter `upstream` placed before the required fields.** The reference template puts optional fields after `rationale`. Fix: move `upstream: docs/prds/PRD-inert-defaultmode-key.md` to follow `rationale`, and keep `decision_provenance` last.
2. **minor: `user_visible_surface` not set.** The design changes CLI-visible behavior (dispatch argv, error text, stderr warning) and edits `docs/guides/ephemeral-session-instances.md` and `docs/guides/file-distribution.md`. Fix: add `user_visible_surface: true` so /plan doesn't have to rely on its body-scan fallback.
3. **minor: `decision_provenance` isn't a documented field.** This format reference doesn't list it, so its placement can't be checked against a canonical order. Fix: none needed in the document. Note for shirabe that the format reference should document the field if /design emits it.
4. **minor: Context and Problem Statement restates PRD requirements in prose.** The sentences starting "The PRD requires..." and the "must" sentences in the first two defect paragraphs repeat R3, R1/R2/R11, and R4-R9, which Decision Drivers then cites by number. Fix: keep the defect narrative and replace the requirement sentences with short R-number citations, for example "the PRD's R4-R9 set the per-location expected values".
5. **minor: uncited claims about external behavior.** The Claude Code version claims (2.1.257, 2.1.142) and the "Measured on 2.1.267" downgrade observation carry no citation. Fix: cite the PRD section that records the measurement, or the Claude Code release notes, right next to the claim.
6. **minor: `S1-S9` and `step 9a` / `9c` / `9d` are undefined.** Fix: at first use, say that S1-S9 are the PRD's posture-source scenarios (with a pointer to the PRD section), and describe steps 9a, 9c, and 9d by what they do rather than by comment number.
7. **minor: the same-process trust statement is repeated three times** (Decision Outcome, Solution Architecture, Security Considerations). Fix: keep the full statement in Security Considerations, and in the other two places keep only the code-comment requirement plus a back-reference.
8. **minor: Decision Outcome Summary duplicates Solution Architecture and Decision 3.** Fix: cut the Summary to the combined decision and its key properties (roughly its first paragraph plus one sentence each on the mapping, the derivation, and the builder parameter), and leave the mechanics to Components and the test list to Decision 3.
9. **minor: Phase 1 says "no output changes", but `instance.json` gains a field.** If the characterization goldens cover `instance.json`, Phase 1 changes them before Phase 3's regeneration step. Fix: say whether the goldens hash `instance.json`. If they do, move golden regeneration for that file into Phase 1 or reword the claim as "no settings-document output changes".
10. **minor: Data Flow step 1 gives an incomplete precedence chain.** It lists workspace overlay, workspace, and personal overlay, but not `[instance.claude.settings]`. Fix: add "then instance overrides via `MergeInstanceOverrides`" to step 1, or say that step 3 applies them.
11. **minor (advisory): Decision Drivers aren't numbered.** The quality guidance prefers D1, D2, and so on for cross-referencing. Fix: optional. Number the drivers if Considered Options is going to cite them, or leave them as is, since the rejections already cite R numbers.
