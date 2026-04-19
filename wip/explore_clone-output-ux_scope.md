# Explore Scope: clone-output-ux

## Visibility

Public

## Core Question

Should niwa replace its linear log-dump approach to clone/apply output with inline
status indicators that update in place, and if so, how should it handle the
tensions between progress display, error/warning visibility, and machine-readable
output? We need to understand industry patterns and the constraints of niwa's
current output architecture before committing to an approach.

## Context

Niwa's `apply` and `create` commands clone and pull many repos, printing one line
per action via raw `fmt.Fprintf(os.Stderr, ...)` calls scattered across `apply.go`,
`clone.go`, `sync.go`, and `overlaysync.go`. Git commands pipe their stderr
directly to `os.Stderr`. The overlay clone path intentionally suppresses all output
(privacy requirement). There's no `Reporter` or `Progress` abstraction — every
output call is a direct write. The user wants modern inline indicators that replace
status in place (spinner/progress bar style), but this must not obscure errors or
warnings, and there may be a need for a machine-readable fallback mode.

## In Scope

- Inline progress/status UX patterns for the `apply` and `create` output phases
- TTY detection and machine-readable mode conventions
- Go library options for terminal UI (progress bars, spinners, inline replacement)
- How to surface errors and warnings while progress display is active
- The specific output architecture in niwa and how it constrains implementation

## Out of Scope

- Overlay clone output (intentionally suppressed for privacy — do not change)
- Structured logging / log levels (this is a UX question, not observability)
- Windows terminal compatibility (not a stated requirement)
- Breaking the stderr-only output contract (errors must stay on stderr)

## Research Leads

1. **How do industry CLIs (cargo, npm, docker, gh, kubectl) handle inline progress during multi-step operations?**
   Niwa is doing something similar to `docker pull` or `cargo build` — many parallel or sequential
   operations with status. Understanding what works and what annoys users will inform the design.

2. **What TTY detection and machine-readable mode conventions are standard across CLIs?**
   The user asked whether to add a flag for machine readers. We need to know whether the convention
   is `--plain`, `--porcelain`, `NO_COLOR`, `CI` env var detection, or something else — and whether
   TTY detection alone is sufficient.

3. **What Go terminal UI libraries exist and which are appropriate for niwa's constraints?**
   bubbletea/bubbles, mpb, uiprogress, pterm, progressbar — each has different integration models.
   Niwa's scattered direct-write architecture means some libraries will be a better fit than others.

4. **How do CLIs keep errors and warnings visible when progress display is active?**
   This is the critical constraint: if a spinner or progress bar is overwriting lines, a `warning:`
   or `error:` message written to the same stream will be erased or interleaved badly. We need to
   know how tools like `cargo`, `npm`, and `docker` handle this.

5. **What is the actual output site map in niwa's codebase, and how coupled/scattered is it?**
   Before deciding on an approach, we need to know whether a thin abstraction can wrap all output
   sites without major restructuring, or whether the scatter forces a more invasive change.

6. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)
   You are a demand-validation researcher. Investigate whether evidence supports
   pursuing this topic. Report what you found. Cite only what you found in durable
   artifacts. The verdict belongs to convergence and the user.

   ## Visibility

   Public

   Respect this visibility level. Do not include private-repo content in output
   that will appear in public-repo artifacts.

   ## Six Demand-Validation Questions

   Investigate each question. For each, report what you found and assign a
   confidence level.

   Confidence vocabulary:
   - **High**: multiple independent sources confirm (distinct issue reporters,
     maintainer-assigned labels, linked merged PRs, explicit acceptance criteria
     authored by maintainers)
   - **Medium**: one source type confirms without corroboration
   - **Low**: evidence exists but is weak (single comment, proposed solution
     cited as the problem)
   - **Absent**: searched relevant sources; found nothing

   Questions:
   1. Is demand real? Look for distinct issue reporters, explicit requests,
      maintainer acknowledgment.
   2. What do people do today instead? Look for workarounds in issues, docs,
      or code comments.
   3. Who specifically asked? Cite issue numbers, comment authors, PR
      references — not paraphrases.
   4. What behavior change counts as success? Look for acceptance criteria,
      stated outcomes, measurable goals in issues or linked docs.
   5. Is it already built? Search the codebase and existing docs for prior
      implementations or partial work.
   6. Is it already planned? Check open issues, linked design docs, roadmap
      items, or project board entries.

   ## Calibration

   Produce a Calibration section that explicitly distinguishes:

   - **Demand not validated**: majority of questions returned absent or low
     confidence, with no positive rejection evidence. Flag the gap. Another
     round or user clarification may surface what the repo couldn't.
   - **Demand validated as absent**: positive evidence that demand doesn't exist
     or was evaluated and rejected. Examples: closed PRs with explicit maintainer
     rejection reasoning, design docs that de-scoped the feature, maintainer
     comments declining the request. This finding warrants a "don't pursue"
     crystallize outcome.

   Do not conflate these two states. "I found no evidence" is not the same as
   "I found evidence it was rejected."

   Write your full findings to: wip/research/explore_clone-output-ux_r1_lead-adversarial-demand.md

   Return ONLY a 3-sentence Summary to this conversation.
