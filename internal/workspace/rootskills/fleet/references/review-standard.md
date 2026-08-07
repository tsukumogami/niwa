# Reviewing a dispatched worker's pull request

## Do not trust the pull request body

These pull requests are written by agents and are unusually thorough. That is exactly why
the body is not evidence. A thorough body argues harder, and arguing harder produces more
claims that can be false.

In one measured batch of eight reviewed pull requests, four had a finding a human needed to
act on before merging. **Three of the four lead findings were false or unverified claims in
prose, not defects in code.** One was a citation in a design document that would go stale
the moment a sibling pull request merged. Only one was purely a code defect.

That is where the residual risk sits once the code quality is good: not in the change, in
the description of the change. And every single finding was caught by *running* something.
None came from reading.

## What to actually check

Take each load-bearing claim in the body and test it:

- **"X is only reachable via Y"** -- verify the reachability yourself. Find the callers.
  This is the single most common false claim, because it is the one hardest to check while
  writing.
- **"Behavior-preserving"** -- find the before and the after and confirm it.
- **A cited file and line** -- open it. Line numbers drift, and a citation to a line that
  moved is a citation to nothing.
- **A mutation table** -- spot-check at least one entry. Apply the defect, run the named
  test, confirm it fails, revert. Leave the tree clean.
- **"This failure is pre-existing"** or **"environmental"** -- verify against the base
  branch. Sometimes true, and it is also the easiest thing to assert without checking.
- **A claim about documentation or user-facing text** -- read the shipped text, not the
  description of it. A body can accurately describe a change that the documentation it
  edited contradicts.

## Build it and run it against current base

```bash
git fetch origin <base> <branch> --quiet
git checkout -B rev-<pr> origin/<branch>
git merge --no-edit origin/<base>     # report conflicts if any
# then the project's build, vet, format and test commands
```

Clean up your branch afterwards.

**Know your local false positives before you report one.** Shared caches between sibling
worktrees, tooling versions that differ from CI, and stale build artifacts all produce
failures that are not the pull request's. The test: does the same failure reproduce on an
untouched base branch? If it does, it is not this change's.

**Know your CI's false positives too.** A misconfigured gate that fails on skipped checks,
or a workflow red on upstream drift rather than on the change, will push every worker into
explaining a failure that is not theirs. If a body's reasoning rests on one of those, say
so -- the explanation is noise and it is sitting in the durable record.

## Context that applies to a batch

When several pull requests are in flight against the same base, tell the reviewer what has
already merged and in what order. A pull request branched before some of those may carry
analysis that is now stale. **Stale analysis is worth reporting even when the code is still
correct**, because the next reader will trust it.

## What counts as feedback worth writing

Write it up only if a human should act before merging:

- a defect;
- an unverified or false claim in the body;
- scope beyond what the work was asked to do;
- a behavior change that is not called out;
- a missing test for a stated guarantee;
- stale analysis that would mislead a future reader.

Do not write it up for style preferences, "could also do X later", praise, or restating
what the pull request already says. If it is fine, say it is fine.

## Be specific about what you checked

Lead with the most serious finding. For each one: what, where (file and line), why it
matters, and **what you verified versus what you inferred**. A reviewer who inferred
something and reported it as verified has reproduced the exact failure this standard
exists to catch.
