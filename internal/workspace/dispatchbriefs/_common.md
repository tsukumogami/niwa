# Common working agreement for dispatched workers

Every dispatch brief in this directory references this file. It carries the parts that do
not change between tasks, so they cannot drift between workers. Your brief carries the
parts that do: the goal, the source of truth, the workflow, and the context that source
cannot know.

Read your brief first. This file is the standing agreement underneath it. Where the two
disagree, your brief wins — it was written for your task.

niwa owns the block between the two `niwa:dispatch-brief-common` markers and rewrites it
whenever the workspace root is materialized. Anything you add outside those markers is
yours and is never touched, so put workspace-specific rules — your language's tooling,
your test constraints, your house conventions — above or below the block rather than
inside it.

## Autonomy

You are expected to finish without a human, unless your brief says otherwise. When you
hit a question:

- Take the sensible default and record it. A decision with an obvious default, the size
  of the remaining work, and your own context budget are not blockers.
- Stop and say so for a genuine blocker: an irreversible or destructive action you cannot
  confirm, an upstream artifact that must change before the work can proceed, or a
  decision you cannot settle with the evidence available. Say precisely what was
  inconclusive and what evidence would settle it.

Do not stop for approval at phase boundaries. If the human wanted to be consulted at each
step they would not have dispatched you.

**Some briefs invert this deliberately.** A brief that says to settle its framing
interactively first means it: ask, wait, and use the answer rather than resolving the
question yourself and pressing on. The framing is what is uncertain in that case, and a
confident wrong answer is more expensive than the wait.

## Your source of truth is primary, and it may be wrong

Your brief points at an issue, a document, or a described problem. Read it first and
treat it as the specification.

Then verify its load-bearing claims before building on them. A carefully written issue
can still be wrong, and the failure is quiet: you build the right fix for the wrong
problem and nothing catches it, because everything downstream agrees with the premise you
inherited. If a claim does not survive contact with the code, say so plainly in the pull
request rather than silently reinterpreting it.

## Check reachability, not just mechanism

An issue can be right about the defect and wrong about the consequence. It can name
exactly which fields some conversion drops, and name a consequence — some later path
re-runs the lossy result — that turns out to be unreachable, because an earlier
short-circuit always fires first.

Before you build on a stated consequence, confirm the path is reachable and find out who
actually reaches it. It is a cheap check and it changes what gets tested. If the stated
consequence does not hold, say so and give the one that does.

## Assert on observable behavior

Drive the real code path and assert the outcome a user would see. A test that stops one
layer short — checking that a file was written without checking whether anything reads it
— is how a bug survives review. Asserting that a validation helper returns an error is
worthless if the helper already returns "valid" for the input in question.

**Mutation-test every guard you add.** Inject one specific defect, confirm a named test
fails, revert. Record the applied defects and the observed failures in the pull request.

If a mutation does not kill a test, that is the finding, not a nuisance — it means the
test is not pinning the behavior. Strengthen it and say so. Guards can mask each other,
and the cases that break the masking are the ones worth having. If a survivor is genuinely
expected, record it as an expected survivor with the reason rather than dropping it from
the table.

## Scope discipline

Your brief names the sibling work that is out of scope, and often the specific files
other in-flight workers are touching. Do not fix those. If your work collides with one,
say so in the pull request rather than widening scope — a wide pull request is harder to
review, and a security or data-loss fix buried in a refactor is worse than one that lands
alone.

**If you find a new defect while working, file it as an issue** rather than leaving it in
the pull request body. Pull requests here are squash-merged and everything below the
`---` is deleted at merge, so a finding recorded only in a description is destroyed the
moment it lands.

## Reporting

Report what is true, not what you intended. The specific failure worth avoiding: a check
runs, it answers a slightly different question than the one you were asked, and its
result gets reported as the answer. Checking that a process started is not checking that
it was accepted. Querying a list of things you already knew about cannot discover
anything new. A check that ran an hour ago is not a current check.

So: **state the check and when it ran, or say plainly that you did not check.** "No
overlapping files as of the diff I read at 14:20" is falsifiable. "Safe to run in
parallel" is not.

When you finish, end your final message with the work-in-flight summary your brief
specifies, listing every pull request you opened, one per line with its bare URL last.
Never invent a reference. If you opened none, say so in one line.

This matters because a background worker's chat output does not reach the dashboard. That
final message is the only durable record of what you produced.
