---
status: Accepted
decision: |
  Accept the residual risk that the host mirror keeps commits a remote
  removed, a leaked secret included, until a person deletes it, and that
  recovery by id can fetch and pin such a commit again after a purge.
  Retention and recovery by id stay as the mirror-repair design states them,
  on two conditions that land with the reference-clone work: a user guide
  carries the purge steps in the order that keeps the commit out, and the
  setting that turns borrowing off can be set per repo.
rationale: |
  Retention is the requirement the rest of the repair guarantee stands on,
  and every option that narrows it either gives up commits borrowers need or
  turns a surviving branch into an unrecoverable one. Recovery by id fetches
  only commits a borrower's own ref still reaches, which a clone that didn't
  borrow would hold in its own store anyway, so it adds no exposure the
  instance hadn't already kept. What is new is a copy outside every instance
  that never expires, and the answer to that is telling people where it is
  and how to remove it, not weakening the guarantee.
---

# DECISION: accept the mirror's retention of commits a remote removed

## Status

Accepted

## Context

The mirror-repair design (`docs/designs/DESIGN-mirror-repair.md`) gives each
repo one bare mirror on the host, under `~/.niwa/`, that instances borrow
objects from through git's alternates file. A borrower keeps none of what it
borrows, so the requirements say the mirror never discards a commit just
because no remote branch points at it any more. The design meets that by
fetching with `--no-prune`, turning off `gc` and maintenance, setting
`gc.pruneExpire=never` and keeping every reflog forever, so every commit the
mirror ever fetched stays reachable. When a mirror is lost or damaged, the
restore pins two kinds of commit under `refs/niwa/kept/<sha>`: those it
salvages from the old store, and those it fetches back from the remote by id
because a borrower's ref still needs them. The design's Security
Considerations already describe a manual purge, name the result as a residual
risk put to the owner here, and leave a per-repo opt-out to the reference-clone work's
setting that turns borrowing off.

## Problem

A commit a remote removed stays in the mirror until a person deletes the
mirror directory, and that includes a commit removed because it leaked a
secret. Two things set this apart from an ordinary clone: the copy sits
outside every instance, so destroying or reaping instances, the usual way
people clean up, leaves it in place; and nothing in it ever expires, where a
plain clone's unreachable objects eventually go once its reflogs age out.
Then, after a person purges the mirror, the next restore probes the
borrowers, and if a borrower's branch or remote-tracking ref still reaches
the removed commit and the remote still serves it by id (forges often do for
a while, from a hidden ref or an object not yet collected), niwa fetches it
back and pins it again. Someone who deleted the mirror to be rid of a secret
can find it back on disk after the next niwa command unless they clear the
borrower refs first, an order of steps the purge text has to spell out.

## Question

Should the workspace owner explicitly accept that the host mirror keeps
remote-removed commits, secrets included, until someone deletes it by hand,
and that recovery by id can bring such a commit back after a purge while a
borrower still reaches it and the remote still serves it, or should the
design change before the reference-clone work builds on it?

## Decision

The risk is accepted, and retention and recovery by id stay as designed, on
two conditions that cost the design nothing and fall in work already
planned. First, the purge steps ship in a user guide with the reference-clone work,
in the order that actually keeps the commit out: rotate the secret; in each
instance that borrows from the mirror, rewrite or delete any local branch
that reaches the commit and run `git fetch --prune`; delete the mirror's
stable path and its `.store/<repo-id>-*` directories; ask the forge to drop
the commit. Second, the reference-clone work's setting that turns borrowing
off works per repo, so a repo whose history is sensitive can stay out of the
mirror entirely.

The reasoning runs like this. Retention isn't a side effect; it's the
requirement that lets a borrower's branch on a dropped commit keep working,
and the design measured a pruning collection corrupting exactly such a
borrower. Recovery by id fetches only the first missing commit of a borrower
ref that fails the probe, never a commit no borrower reaches. A clone that
didn't borrow would hold that same commit in its own store for as long as
its ref reaches it, and the fix is the same in both cases: change the ref. So
the re-pin restores the exposure the instance already kept rather than
adding one. A leaked secret is already exposed on the forge and in every
clone fetched while it was there, so rotation is the remedy and scrubbing
local history is hygiene. What's genuinely new is a long-lived copy outside
the instances, and the cheapest honest answer to that is documenting where it
lives and how to remove it. No host mirror exists until reference clones
ship, so the guide and the per-repo setting arrive when the risk does.

## Alternatives considered

Each option below is weighed against what it costs the design's guarantee
that every borrower's refs read to the root again after a restore, with the
one stated exception of commits the remote has truly destroyed.

### Accept as stated

The owner signs off on the Security Considerations text unchanged. It's the
least work, and the design already names the purge and the forge request.
It costs the guarantee nothing. It falls short because the purge steps live
only in a design document few users will read, so the step that stops the
re-pin, clearing borrower refs before deleting the mirror, never reaches the
people who need it. Accepting the risk without fixing that leaves the one
real trap undocumented for users.

### Accept with a documented purge procedure (chosen)

As above: accept, with the ordered purge steps in a user guide and a
per-repo off setting, both delivered with the reference-clone work. It costs
the guarantee nothing; retention and recovery by id run exactly as designed.
The cost is a guide section and a requirement on a setting that work already
owns. The limit is that it relies on a person reading the guide when a
secret leaks.

### Accept with a purge command

A niwa command removes one repo's mirror, first listing every borrower ref
that still reaches a named commit and refusing or warning while one does.
This closes the re-pin trap mechanically and spares people from finding the
workspace-keyed path under `~/.niwa/` by hand. It costs the guarantee nothing
directly: a purged mirror is rebuilt by the next command like a deleted one,
and a borrower on commits the remote no longer serves lands in the existing
stated limit. The costs are new command surface, a borrower scan that today
runs only after a restore, and a command that deletes the mirror sitting
close to the by-hand repair the requirements rule out, the same worry that
kept a hidden mirror command out of the tests. It's a sound follow-up if the
guide proves not to be enough, and it isn't needed to accept the risk.

### Expire kept objects nobody reaches

After some number of days, expire reflog entries, refs for branches the
remote deleted and `refs/niwa/kept/` pins, and let a collection drop what
nothing reaches. This bounds how long a removed secret sits on the host with
nobody acting, which no other option does. It costs the guarantee the most:
it breaks the requirement that the mirror never discards a commit because the
remote dropped it, and a borrower whose branch sits on an expired commit
breaks immediately, not only after a restore. Doing it safely means probing
every borrower before each collection and keeping whatever any of them
reaches, which turns the post-restore scan into routine work and still loses
a commit a borrower dropped and later needs from its own reflog. It also
gains little for the secret case, because a commit a borrower's live branch
reaches is exactly what the probe would keep. It would need the requirements
reopened.

### Skip recovery by id for commits the remote no longer advertises

Fetch by id only commits reachable from a ref the remote lists, so a removed
commit can't come back through niwa once the remote's branches stop reaching
it. In practice this collapses into the plain stated limit the design
already rejected, since a restore's ordinary fetch already brings every
advertised ref. It costs the guarantee real cases: branches built on a
force-pushed branch's earlier tip, on a deleted branch the forge hasn't
collected yet, or on commits only a hidden ref reaches go from surviving a
restore to unrecoverable. It can't tell a commit removed for a secret from
one removed because a pull request merged, so it gives up the everyday case
to cover the rare one. And it doesn't remove the exposure: the borrower's
ref still names the commit, and the person still has to change that ref.

### A per-repo opt-out now

Add the setting that keeps a repo out of the mirror in this feature rather
than in the reference-clone work. It costs the guarantee nothing, since an
opted-out repo keeps a full clone and isn't a borrower. But this feature
builds no borrowing create, so the setting would have nothing to switch
until reference clones exist, and it would split one setting across two
features. The decision gets the same protection by asking that the
reference-clone work's off setting be per repo.

## Consequences

The design's Security Considerations stand as written, and the
reference-clone work picks up two items: a per-repo off setting and a user
guide section with the ordered purge steps. Nothing in the mirror-repair
implementation changes. A secret that reaches the mirror stays on the host
until a person follows those steps, and the guide is the only thing that
tells them to.

If the guide proves not to be enough, the purge command described above is
the follow-up that keeps the guarantee whole. Expiry or narrower recovery by
id would each move borrowers from surviving a restore into the
unrecoverable limit, and expiry would need the requirements reopened first.

## References

- `docs/designs/DESIGN-mirror-repair.md`: Decision 2 (retention), Decision 3
  (pushed commits the remote deleted), Security Considerations, Consequences
- `docs/prds/PRD-mirror-repair.md`
