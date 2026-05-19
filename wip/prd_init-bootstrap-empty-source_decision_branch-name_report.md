<!-- decision:start id="init-bootstrap-empty-source/branch-name" status="assumed" -->
### Decision: Bootstrap branch name inside the worktree

**Context**

Bootstrap chains init -> create -> session create (per the lifecycle
decision T1). The session-create step provisions a git worktree on a
new branch at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. The
scaffolded `workspace.toml` lands as a commit on that branch, and the
user will eventually push it to the empty remote so the workspace
config is shared with their team.

Today's session-create hardcodes the branch name as `session/<sid>`
(`internal/mcp/handlers_session.go:227`), where `<sid>` is 8 random
hex characters. That shape is fine for worker-agent sessions because
the SID is an internal handle the user rarely sees. Bootstrap is
different: the branch will be pushed to origin, become a PR head ref
(today via manual `gh pr create`, tomorrow via a planned
`--create-pr` flag), and live in the team's git history forever.
First impressions and long-term readability both matter for this one
branch in a way they don't for ordinary sessions.

Reading the code confirmed three load-bearing facts. First, the
branch name is NOT stored in session state today — it's reconstructed
from the SID at every callsite (create, destroy, and the
worktree-attach warning that prints the exact `git push -u origin
session/<sid>` command for the user to copy). Second, session-list is
keyed by SID, not by branch name, so changing the branch shape does
not orphan the bootstrap session from `niwa go <sid>` discoverability.
Third, any option other than the zero-change baseline requires the
same small refactor: add a `BranchName` field to
`SessionLifecycleState`, populate it in create, and update destroy +
warnings to read from state. The implementation cost is therefore
constant across B2, B3, and B4 — the choice between them is purely a
UX call.

**Assumptions**

- niwa session state can be extended with an optional `BranchName`
  field without breaking back-compat for in-flight sessions. If wrong:
  the migration story gets more involved, but the failure mode (empty
  field falls back to `"session/" + sessionID`) is straightforward to
  implement.
- The "future PR-creation flag" mentioned in the constraints is a real
  near-term consideration. If wrong: the PR-title-readability driver
  weakens, but the push-command and git-history drivers still favor a
  named-prefix option.
- Bootstrap will sometimes fail and be re-run against the same target
  (network blip during clone, user typo in slug, etc.). If wrong: the
  collision-safety driver weakens and B2 / B4 become viable.
- For the empty-remote bootstrap case, the workspace name from the
  name-derivation decision (N1: GitHub repo basename) is what would
  appear in a B4-shaped branch. If wrong: B4 needs its own derivation
  rule, but the slug parser is already available.
- This decision is made in --auto mode without interactive user
  confirmation, hence the `status="assumed"` block marker. A reviewer
  may upgrade to `confirmed` after sign-off.

**Chosen: B3 — `niwa-bootstrap/<sid>`**

The bootstrap branch is named `niwa-bootstrap/<sid>`, where `<sid>` is
the same 8-hex-character session ID that names the worktree directory
and the session-state file. Concretely:

1. Session-create gains an optional `branch_name` parameter in its MCP
   request schema. When unset, today's `session/<sid>` default
   applies. When set, the supplied name is used verbatim.
2. `SessionLifecycleState` (`internal/mcp/session_lifecycle.go:30`)
   gains a `BranchName string` field. Create populates it from the
   request (or the default); destroy and the attach-warnings code
   read it instead of reconstructing `"session/" + sessionID`. Empty
   field falls back to `"session/" + sessionID` for back-compat with
   sessions written before the field existed.
3. Bootstrap invokes session-create with
   `branch_name: "niwa-bootstrap/" + sid`. The SID is whichever one
   `newSessionLifecycleID` generated for this session; the branch
   reuses it so a single identifier ties together the worktree dir,
   the session state file, and the branch ref.
4. The success message and any printed `git push` hint use the full
   branch name, so the user copies an exact command:
   `git push -u origin niwa-bootstrap/abc12345`.

The result the user sees: a worktree on a branch named
`niwa-bootstrap/abc12345`. `git status` inside the worktree shows that
branch. The push command they're prompted with names that branch. If
they open a PR (today manually, tomorrow via a niwa flag), the PR
title defaults to "niwa-bootstrap/abc12345" — the "niwa-bootstrap"
prefix carries the meaning, the SID suffix is forgettable noise.

**Rationale**

B3 wins on the heaviest driver — eternal git history quality — while
preserving the collision safety the SID-based scheme already
guarantees.

The branch name will live in the team's git history forever. B1's
`session/<sid>` will look like an internal artifact in `git log --all`
ten years from now ("why is there a branch called session/abc12345 in
our history?"). B3's `niwa-bootstrap/abc12345` is self-documenting:
the prefix tells the future reader what produced the branch without
requiring them to know anything about niwa's session model. B2 and B4
share that prefix benefit, but they also strip away the SID, which
costs the collision safety the current scheme has for free.

Collision safety matters more than it might appear. Bootstrap is the
user's first niwa command and the most likely to fail and be retried
(slug typos, network blips during the empty-remote clone, sign-in
flows that lapse mid-command). With B2 (`niwa-bootstrap`) or B4
(`niwa-bootstrap/<repo>`), the second invocation collides on the
branch name even after the first invocation's worktree has been
cleaned up. The user then has to know to run
`git branch -D niwa-bootstrap` before retrying — a niwa-internals
detail leaking into the first-run experience, exactly the kind of
friction the PRD's "intro to niwa" framing exists to avoid. B3's SID
suffix preserves the SID's collision-impossibility guarantee.

PR-title readability is the third driver. The user told us they may
add a flag for niwa to open a PR on their behalf. The default PR
title would be the branch name. B3's "niwa-bootstrap/abc12345" reads
cleanly: the reviewer's eyes go to "niwa-bootstrap" first and
understand the PR's purpose; the SID is unobjectionable trailing
context. B2's "niwa-bootstrap" reads slightly cleaner but the
difference is marginal once collision safety is priced in.

Push-command typability is the lightest driver, and B3 pays a small
cost there: `niwa-bootstrap/abc12345` is 23 characters vs B1's
`session/abc12345` at 16. The user types this exactly once, and the
prefix `niwa-bootstrap/` is tab-completable since it's unique among
local branches in any sane workspace. The cost is real but tiny.

Finally, B3 matches niwa's existing session-id model. niwa already
uses `<repo>-<sid>` for worktree directories and `session/<sid>` for
branches today. B3's `niwa-bootstrap/<sid>` follows the same template
(prefix slash SID) — just with a more descriptive prefix for the one
session type whose branch name will become user-visible artifact. The
shape generalizes cleanly if niwa later wants to label other session
types by purpose.

**Alternatives Considered**

- **B1 (Keep `session/<sid>`)**: zero implementation cost. Rejected
  because the branch name is an internal-looking artifact that will
  outlive the session in the team's git history, makes future PR
  titles opaque, and produces a push command that requires the user
  to type "session/" — a niwa-internals concept — at first contact.
  B1 wins only on cost, and cost is the lightest of the four drivers
  for a decision this load-bearing on first-run UX and long-term git
  history.

- **B2 (`niwa-bootstrap`)**: cleanest possible branch name, best
  push-command typability, best PR title. Rejected because the
  deterministic name collides on re-run. Bootstrap is the
  niwa command most likely to be retried after a failure (slug typo,
  network blip, sign-in lapse mid-clone), and a collision requires
  the user to run `git branch -D niwa-bootstrap` manually before
  retrying. That's a niwa-internals friction at the moment the user
  has the least context. B2 would be the right choice if bootstrap
  were truly single-shot, but it isn't.

- **B4 (`niwa-bootstrap/<repo>`)**: encodes which workspace this is
  bootstrapping. Rejected for two reasons. First, the suffix is
  redundant with the workspace name — anyone reading the branch ref
  already knows which repo they're in. Second, B4 inherits B2's
  collision problem (deterministic name, fails on re-run) without
  B2's typability win, since the suffix is longer than B2's bare name
  and not appreciably shorter than B3's SID. B4 strictly loses to
  either B2 (if you accept the collision risk) or B3 (if you don't).

**Consequences**

What becomes easier:

- Bootstrap branches are self-documenting forever. A future reader of
  `git log --all` sees `niwa-bootstrap/abc12345` and understands what
  produced it without needing to know niwa's session model.
- The push command the user copies post-bootstrap names a clearly
  bootstrap-flavored branch: `git push -u origin
  niwa-bootstrap/abc12345`. The prefix anchors the user's mental
  model of what just happened.
- A future `--create-pr` flag opens PRs titled
  "niwa-bootstrap/abc12345" by default — readable in the team's PR
  list without further customization.
- Re-running bootstrap after a failure produces a new branch with a
  fresh SID. No branch-cleanup step required of the user.
- The session-create MCP API gains an optional `branch_name`
  parameter, which generalizes naturally to other future session types
  that may want named branches (e.g. an explicit `--bug-fix` session
  type could request `bugfix/<sid>`).

What becomes harder:

- `SessionLifecycleState` gains a `BranchName` field, and the three
  branch-name reconstruction sites (`handlers_session.go:227`,
  `handlers_session.go:364`, `sessionattach/worktree_warnings.go:81`)
  must read from state rather than rebuilding the `"session/" +
  sessionID` string. Each callsite gains a "if state.BranchName == ""
  fall back to 'session/' + sessionID" guard for back-compat with
  sessions written before the field existed.
- Tests that assert `expectedBranch := "session/" + sessionID`
  (`handlers_session_test.go:328`) for the bootstrap path will need
  updating; tests for non-bootstrap session-create paths stay green
  via the default-fallback.
- A trivial documentation cost: the worktree-warnings code's
  comment header (`worktree_warnings.go:19-20`) currently references
  `session/<sessionID>` as the canonical branch shape and should be
  updated to say "the branch named in session state, falling back to
  `session/<sessionID>`."

What stays the same:

- Non-bootstrap sessions continue to use `session/<sid>` by default;
  worker-agent sessions are unchanged.
- `niwa go <sid>` and session-list continue to key on SID, not on
  branch name. The bootstrap session shows up in the same listing as
  any other session.
- The worktree directory name (`<repo>-<sid>`) is unchanged.
- SID generation, collision retry, and state-file naming are all
  unchanged.

What this enables next:

- A future `--create-pr` flag can open a PR titled by the branch name
  without needing to override the title — `niwa-bootstrap/<sid>` is
  already a usable default.
- Other future session types that want descriptive branches can use
  the same `branch_name` parameter (e.g. release sessions named
  `release/<sid>`, hotfix sessions named `hotfix/<sid>`).
<!-- decision:end -->
