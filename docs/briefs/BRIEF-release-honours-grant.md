---
schema: brief/v1
status: Accepted
problem: |
  A session niwa dispatches to cut releases can be granted merging and
  closing, but not releasing: the workspace never grants `cut-release`, the
  gate doesn't see the workflow dispatch that actually cuts a release, and
  `/shirabe:release` asks a person for every cut whatever the session holds.
outcome: |
  The owner grants release authority once, through niwa, as merge and close
  are granted. A release seat holding `cut-release` cuts a release and runs
  the installs that follow it with nobody asked; every other session is asked
  before a release, however it's started, and is told which authority it lacks.
motivating_context: |
  The owner directed that release permission be granted through niwa the same
  way merge and close already are, so a release seat stops asking the owner
  for every cut. That reverses the earlier ruling that kept `cut-release` out
  of the tsukumogami workspace. The owner also ruled that an approved cut
  carries the installs that follow it on the host.
---

# BRIEF: release-honours-grant

## Status

Accepted

## Problem Statement

niwa records a named grant for every session it dispatches, and the
workspace's `gate-online` hook now decides merges, issue closes and releases
for a bound session by that grant. Merge and close work end to end: a
coordinator dispatched with the `coordinator` role holds `merge-pr` and
`close-issue` and lands its work without asking anyone. Releases don't, for
three separate reasons.

1. **Nobody can hold `cut-release` here.** The tsukumogami workspace's grant
   ceiling and its `coordinator` role name only `merge-pr` and
   `close-issue`, on an earlier ruling that a release stays a person's call.
   A dispatch that asks for `cut-release` is refused.
2. **The gate watches the wrong command.** The hook treats `gh release
   create` as a release, and that's the command a person would type. But
   `/shirabe:release` cuts by dispatching each repository's "Prepare
   Release" workflow (`gh workflow run`, or the same call through the REST
   API), and the hook has no rule for that at all. Today a workflow dispatch
   is allowed for every session, granted or not, while the draft that comes
   before it is asked about. A grant decision on `gh release create` alone
   would govern the harmless half of a cut and miss the half that publishes.
3. **The skill asks regardless.** `/shirabe:release` stops for a person to
   confirm the version and the notes on every run. A seat that the owner has
   trusted to release still waits for that go, so a granted release seat
   would still interrupt the owner once per cut.

After a cut, the host is updated to the new version: the released tool is
installed again with tsuku, the plugin is updated, and the workspace is
re-applied with niwa. Those installs wait on a go of their own today, so even
an approved cut leaves the host on the old version until someone says so.

## User Outcome

The owner, who today approves every release and every install that follows,
grants release authority once and stops being asked. A release seat
dispatched with `cut-release` runs `/shirabe:release` from version choice to
published release and then brings the host up to the new version, with no
person in the loop, and the owner reads the result afterwards.

Nothing widens for anyone else. A session dispatched without `cut-release`,
and the owner's own session that niwa never dispatched, are asked before a
release whichever way the release is started, and the gate's question says
which authority the session would have needed. For the workflow dispatch
that's new: today it goes through unasked for every session. A host without
niwa gets the same questions it gets today, plus that one.

## User Journeys

### A release seat cuts a release

A release seat dispatched with the `coordinator` role, which now holds
`cut-release`, is told to release koto. It runs `/shirabe:release`, which
reads the seat's grant from niwa, takes the recommended version and its own
notes without stopping, creates the draft and dispatches the Prepare Release
workflow; the gate allows both because the seat holds the authority. Once the
workflow finishes and the release is published, the skill runs the installs
koto's release declares on the host. The owner sees a published release and
an updated host, and was asked nothing.

### A session without the grant reaches a release

A worker dispatched with `merge-pr` only, or a session the owner started by
hand, runs `/shirabe:release` or types `gh workflow run prepare-release.yml`
directly. The skill asks for the version-and-notes go as it does today, and
the gate asks before the workflow dispatch with a reason that names what the
session holds and that it needs `cut-release`. The person answering knows
why they were asked.

### A release seat hands its seat on

A release seat holding `cut-release` dispatches its successor with the
`coordinator` role; niwa launches it, and the successor releases on its own
grant. A seat that doesn't hold `cut-release` asks for it for a child and is
refused before anything launches, so release authority can't be passed on by
a session that never had it.

### The owner rolls the change out

Before any real release depends on it, the owner (or a session working for
them) runs the whole path against a throwaway repository in a disposable
workspace whose ceiling holds `cut-release`: a granted seat releases with no
question, an ungranted one is asked and told what it lacks. Only after that
does the tsukumogami workspace's ceiling and `coordinator` role gain
`cut-release`, and the running release seat is re-dispatched to pick it up.

## Scope Boundary

**IN:**

- Adding `cut-release` to the tsukumogami workspace's grant ceiling and its
  `coordinator` role, replacing the earlier ruling, once the change has been
  shown working in a throwaway repository.
- The `gate-online` hook treating a dispatch of a repository's release
  workflow, by `gh workflow run` or by the REST API, as a release decided by
  `cut-release`, while other workflow dispatches keep today's verdict.
- `/shirabe:release` reading the session's grant through `niwa authority
  check`, so that a bound session holding `cut-release` gets no
  version-and-notes question and anyone else is asked as today, including on
  a host without niwa.
- The host installs that follow a cut (tsuku update, plugin update, niwa
  apply), covered by `cut-release` on the owner's ruling, and tied to the cut
  that preceded them.
- niwa's authority guide saying what `cut-release` covers once this lands.
- How the change is rolled out and checked live before a real release relies
  on it.

**OUT:**

- Releases started some other way: pushing a tag by hand, publishing a draft
  with `gh release edit`, or creating a release through the REST releases
  endpoint. The gate doesn't read these today and this work doesn't add them.
- Confirmations in `/shirabe:release` that aren't the go for the cut, such as
  how a security-labelled pull request is described in the notes, or a
  repository check that asks a person to confirm something specific. They
  stay a person's call.
- A separate `release` role or a narrower grant for release seats. The owner
  directed that the `coordinator` role carry it.
- Installs a session runs outside a release run. The grant ties installs to
  the cut; it doesn't make a holder free to install at any time.
- Changing niwa's grant model, the record, or `niwa authority check`. The
  authority and the lookup already exist; this work uses them.
- Stronger enforcement: the gate is text inspection and stays one.

## References

- `docs/prds/PRD-gates-honour-grant.md`, `docs/designs/DESIGN-gates-honour-grant.md`
  and `docs/plans/PLAN-gates-honour-grant.md`: the work that made the gate and
  `/coordinate` honour the grant, which this extends to releases.
- `docs/guides/authority-grants.md`: the grant, the record, `niwa authority
  check` and the contract for a gate on the binding.
