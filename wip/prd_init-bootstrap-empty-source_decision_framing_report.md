<!-- decision:start id="init-bootstrap-lifecycle-scope" status="assumed" -->
### Decision: Lifecycle scope of `niwa init <name> --from <empty-remote> --bootstrap`

**Context**

niwa's workspace lifecycle is `init -> create -> apply`, and only
`niwa session create` produces a worktree at the standard location
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. The user has set two
firm targets for `--bootstrap`: the worktree must land at that standard
location (the W1 "in-place at workspace root" alternative is explicitly
rejected), and the command is the user's first hands-on exposure to niwa
for this workspace — friction and surprise count heavily against any
shape that requires the user to chase the worktree across multiple
invocations.

There is no code path that creates a worktree without going through
`niwa create` and channels-enabled infrastructure. Session create gates
on `instance.json`, `.niwa/roles/<repo>/`, and the cloned repo `.git`.
Any option that lands the user in the standard worktree location on
success must therefore run all three lifecycle steps. The four options
under consideration partition the lifecycle differently: T1 runs all three
as one atomic command; T2 runs only init and prints next-step
instructions; T3 runs only init and defers everything else; T4 runs init
and create but stops short of session create.

Today's init aggressively removes the workspace directory on any
scaffolding failure (`workspaceCreated` defer at
`internal/cli/init.go:215-226`). A multi-step chain needs a per-step
rollback contract so a late-stage failure does not destructively wipe
earlier successful steps. The existing shell-wrapper landing-path
protocol (`internal/cli/landing.go`) is already used by
`niwa session create` to navigate the shell into the new worktree on
success — so a chained command inherits that UX without new plumbing.

**Assumptions**

- The user invoking `--bootstrap` for the first time wants the fewest
  manual steps consistent with seeing what niwa is doing. If wrong, T1 is
  still the best UX, just by a smaller margin.
- Refactoring init's aggressive cleanup into a per-step rollback contract
  is acceptable. If wrong, T1 and T4 become harder to ship safely, but
  the refactor is local and well-scoped.
- `--bootstrap` implies `--channels` for the chained create unless
  `--no-channels` is passed explicitly. If wrong, every chained option
  needs an explicit channels gate, which is straightforward to add.
- The empty-remote case is the only `--bootstrap` shape v1 targets. If
  wrong, T1's chain still works for any source.
- A future "open Claude on my behalf" or "create a PR for me" flag will
  layer on top of session create, not replace it. If wrong, T1 may need
  re-shaping but no earlier than that future flag's design.

**Chosen: T1 (Turnkey: init -> create -> session-create)**

`niwa init <name> --from <empty-remote> --bootstrap` executes the full
lifecycle as a single atomic command:

1. Run init: validate the slug, scaffold `<workspaceRoot>/.niwa/`,
   shallow-clone the empty source into the niwa state directory, write
   `workspace.toml`.
2. Run create with channels implicitly enabled (overridable by an
   explicit `--no-channels`): mkdir `<instanceRoot>/`, run the source
   pipeline, write `instance.json`, install `.niwa/roles/<repo>/` and
   `.mcp.json`.
3. Run session create against the freshly-cloned repo with a
   `--bootstrap`-supplied purpose (or a sensible default like "bootstrap"):
   provision `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, commit the
   initial scaffold on the session branch, and write the landing-path
   file so the shell wrapper navigates the user into the worktree.

The output stream narrates each phase by name so the user learns niwa's
lifecycle even though they only typed one command. On success the user
is sitting inside the worktree and the success message lists the two
useful directories: the worktree (just landed) and the workspace
instance root (one `cd` away). On failure the chain rolls back per step:
init failure removes the workspace root (today's behavior); create
failure leaves init's scaffold but removes the instance directory and
prints the partial state; session-create failure leaves init and create
intact, surfaces the error, and points the user at
`niwa session create <repo> <purpose>` to finish manually.

**Rationale**

T1 is the only option that satisfies all of the user-stated UX targets
at once. The user said: "I wanted the worktree to live in the standard
niwa worktrees location" and "User gets to see where the worktrees are
located and has the option to open a Claude instance directly there, or
in the root of the workspace instance." For the user to "see where the
worktrees are located," a worktree must physically exist on disk and the
command must print or land in it. T2, T3, and T4 all return before any
worktree exists, so the user cannot exercise that option until they run
more commands. Only T1 produces a worktree as part of `--bootstrap`.

The user also called this command an "intro to niwa's capabilities."
That is a learning argument, and the natural intuition is that more
commands means more learning. But the lifecycle a user needs to learn is
init -> create -> session-create, and T1 narrates exactly that sequence
in a single command's output. The user reads the same step names whether
they type one command or three; the difference is whether the steps
happen in sequence automatically or with the user as the scheduler.
Auto-scheduling is the better intro because it shows the user the
intended path; manual scheduling forces the user to figure out the
sequence themselves at the moment they have the least context.

The implementation cost — per-step rollback plus implicit channels-on
under `--bootstrap` — is moderate, local, and pays off for every future
chained command niwa adds (notably the future PR-creation flag the user
mentioned, which layers on top of session create). T4 has the same
implicit-channels cost and half the rollback cost, but it does not land
the user in a worktree, which is the load-bearing UX target.

**Alternatives Considered**

- **T2 (init scaffolds + create+session prompt)**: Runs only init, then
  prints instructions for the next two commands. Rejected because the
  worktree does not exist when `--bootstrap` returns, so the user cannot
  "see where the worktrees are located" or open Claude in one without
  running more commands. Splitting the visible payoff across three
  invocations hurts the "intro to niwa" framing more than it helps.

- **T3 (init-only with deferred-worktree promise)**: Runs only init and
  narrates what future commands will produce. Rejected for the same UX
  reason as T2, more sharply: T3 produces no worktree and no instance
  either, so the user has the least to look at after `--bootstrap` of
  any option. The lowest implementation cost is not enough to offset
  failing the user's stated worktree-visibility goal outright.

- **T4 (init + create, no session-create)**: Chains init and create but
  stops at the natural lifecycle boundary. Rejected because the user
  still has to run `niwa session create` to land in a worktree, so the
  command does not satisfy the "user lands in the worktree" UX target.
  T4 pays most of T1's implementation cost (per-step rollback, implicit
  channels) without delivering T1's payoff. T4 is the second-best
  fallback if T1's session-create chaining proves prohibitively risky to
  ship in v1; in that case the bootstrap output should narrate the final
  `niwa session create` step explicitly and offer it as a copy-paste
  command.

**Consequences**

What gets easier:

- The first-run experience is one command. The user runs
  `niwa init <name> --from <empty-remote> --bootstrap` and ends up inside
  a worktree with the scaffold ready to commit, with no follow-up.
- Future flags that depend on a session branch (PR creation, "open
  Claude in this worktree", etc.) layer naturally onto the end of T1's
  chain. They do not need a new orchestration shape.
- The existing shell-wrapper landing-path mechanism is reused, so no new
  CLI plumbing is needed to land the user in the worktree.
- `--bootstrap` becomes a discoverable lifecycle teaching tool: its
  output stream lists init, create, and session-create by name, in the
  same order the standalone commands run, so the user maps the chain to
  the manual lifecycle without prose documentation.

What gets harder:

- Init's existing aggressive-cleanup defer
  (`internal/cli/init.go:215-226`) must be refactored into a per-step
  rollback contract. Init failure still removes the workspace root;
  create failure must leave init's scaffold intact but remove the
  instance directory; session-create failure must leave both intact and
  surface a "run `niwa session create` to finish" hint with the exact
  command. This is a local refactor but it must land before T1 can ship.
- `--bootstrap` implies `--channels` for the chained create, which is a
  policy that must be documented and tested. `--no-channels` must still
  override (and in that case the chain stops after create, since session
  create cannot proceed without roles installed — the failure-recovery
  message becomes the user's escape hatch).
- The PRD must specify a default `purpose` value for the chained session
  create (e.g. "bootstrap") or require the user to pass one via a
  `--purpose` flag on `--bootstrap`. The PRD owns this sub-decision; the
  chosen shape does not constrain it.
- Failure modes during create or session create are now reachable from a
  command that succeeded its first step, so user-facing error messages
  must clearly communicate which step failed and what state remains on
  disk. This is a documentation and UX cost, not an architectural one.
<!-- decision:end -->
