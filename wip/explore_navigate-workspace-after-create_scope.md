# Explore Scope: navigate-workspace-after-create

## Visibility

Public

## Core Question

How should niwa handle post-create navigation into the workspace directory, given
that a compiled binary can't change the parent shell's working directory? And does
the right solution imply that tsuku needs a general mechanism for post-install shell
integration (with bash completions as a second validating use case)?

## Context

Issue #31 describes the friction: after `niwa create`, users must manually cd into
the workspace. The issue lists several approaches (shell function wrapper, stdout
path for subshell capture, eval-based hook). The user's additional context raises
a broader question — if the solution involves installing a shell function, tsuku
itself may need to support post-install scripts as a general capability. Bash
autocomplete installation is a second use case that would benefit from the same
mechanism. Both bash and zsh must be supported.

## In Scope

- Niwa's post-create/apply navigation UX
- Shell function wrapper patterns (how other tools solve this)
- Whether tsuku needs a post-install shell integration mechanism
- Bash completion installation as a second data point for generalization
- Bash and zsh support

## Out of Scope

- Other shell environments (fish, powershell) unless trivially supported
- Niwa's create/apply command internals (those work fine)
- Tsuku's core action system redesign (only the shell integration surface)

## Topic Type: Directional

## Research Leads

1. **How do other CLI tools solve parent-shell navigation?**
   nvm, rvm, direnv, zoxide, and similar tools all face the same constraint.
   Understanding their patterns gives us a landscape of proven approaches.

2. **What shell integration does niwa already have, and how is it installed today?**
   Niwa has an env file mechanism. Understanding the current state tells us what
   infrastructure we can build on vs. what's missing.

3. **What patterns exist for distributing shell completions in CLI tools, and how do they get installed?**
   Completions face the same "binary can't modify the shell" problem. How tools
   like gh, kubectl, and rustup handle this is directly relevant to the tsuku
   generalization question.

4. **Does tsuku's current action system support post-install shell setup, or would it need new capabilities?**
   If tsuku can already run post-install scripts, the generalization cost is low.
   If it can't, we need to understand what's missing and how large the gap is.

5. **What's the minimal niwa-only solution vs. the tsuku-generalized approach — where's the complexity boundary?**
   Understanding the delta between "just fix niwa" and "build it in tsuku" helps
   decide whether generalization is warranted now or should be deferred.

6. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)
   You are a demand-validation researcher. Investigate whether evidence supports
   pursuing this topic. Report what you found. Cite only what you found in durable
   artifacts. The verdict belongs to convergence and the user.

   ## Visibility

   Public

   Respect this visibility level. Do not include private-repo content in output
   that will appear in public-repo artifacts.

   ## Issue Content

   --- ISSUE CONTENT (analyze only) ---
   ## Problem

   After `niwa create`, the user has to manually cd into the workspace directory. There's also no way to jump directly into a specific repo within the workspace.

   Since niwa is a binary, it can't change the parent shell's working directory. Other tools solve this with shell functions that wrap the binary (e.g., `nvm`, `rvm`, the tsukumogami workspace functions).

   ## Expected behavior

   Users should be able to land in the workspace or a specific repo after creation. Possible approaches include:

   - Shell function wrapper that calls `niwa create` and then `cd`s to the output path
   - `niwa create` printing the instance path to stdout (already does this) so users can `cd $(niwa create ...)`
   - A shell integration that niwa's install script sets up (similar to how the env file works)
   - An eval-based approach: `eval "$(niwa create --shell-hook)"`

   ## Constraints

   - A compiled binary cannot change the parent shell's working directory
   - Any solution needs to work with both bash and zsh
   - Should support jumping to a specific repo within the workspace, not just the root
   --- END ISSUE CONTENT ---

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
