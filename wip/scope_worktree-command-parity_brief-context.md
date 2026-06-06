# Brief context — worktree-command-parity

Requirement (from the maintainer) for the `/scope worktree-command-parity` chain.

## The trigger

The mesh removal made `niwa worktree create` a first-class command, but it only
does `git worktree add` + scaffolds `.niwa/sessions/`. It installs NO claude
context. A repo checkout gets `CLAUDE.local.md` and other claude accessories from
`niwa apply`; a worktree gets none of that (and `CLAUDE.local.md` is `.local`/
untracked, so it doesn't travel into a separate `git worktree`). So an agent
launched in a worktree is under-served vs. a normal checkout.

## The real ask: symmetric command structure

This is bigger than one missing feature. niwa has a clear lifecycle at the
**workspace-instance** level: `niwa create | apply | destroy`. The mesh removal
introduced a **worktree/repo** level: `niwa worktree create | destroy | list |
attach | detach`. The two levels should have a coherent, SYMMETRIC structure and
shared code paths — not divergent experiences for operations that are doing
similar things.

Questions the chain must answer (the maintainer is deliberately glossing over
details and expects them to be encountered and resolved during investigation):

1. **Parity for create**: `niwa worktree create` should set up the worktree with
   close to parity to what a repo gets from `niwa create`/`apply` — `CLAUDE.local.md`,
   `.claude/` accessories (settings, rules, hooks), and any content the repo level
   installs. Reuse the existing workspace content installers; do NOT fork a parallel
   code path.
2. **The missing verbs**: what is the worktree-level analog of `niwa apply`? (Re-sync
   a worktree's claude content after config changes.) Is there a worktree analog of
   other workspace verbs? What should `niwa worktree create|<?>|<?>` be? Map the
   workspace verbs to worktree verbs and identify gaps.
3. **Worktree-specific customization**: the `<purpose>` arg + branch name are natural
   inputs. Design a worktree-specific customization hook/template (e.g. a
   `CLAUDE.local.md` section describing the worktree's purpose/branch, or a
   user-providable per-worktree template/hook), distinct from repo-level content.
4. **Hooks / templates / customizations**: enumerate the customization surfaces at
   the workspace level (content sources, overlay, hooks, settings, templates) and
   define their worktree-level analogs.
5. **Shared code paths**: where workspace-level and worktree-level operations do
   similar things (content install, claude-accessory setup, destroy/cleanup), they
   should share implementation, not duplicate. Note the architecture constraint:
   `internal/worktree/` is a leaf package (imports neither `internal/mcp` — deleted —
   nor `internal/workspace`); `internal/workspace/` imports `internal/worktree/`.
   Orchestrating content install for a worktree likely belongs at the CLI layer
   (cli calls `worktree.CreateSession` then a workspace content installer against the
   worktree path), mirroring how bootstrap orchestrates — to keep the leaf a leaf.

## Scope intent

- Produce a DESIGN that defines the symmetric command surface + the content/customization
  model + the shared-code-path architecture, and a PLAN that sequences it.
- The maintainer does NOT expect full implementation in this branch — identify and
  design everything; a first implementation slice (e.g. create-parity) may follow, but
  the deliverable here is the DESIGN + PLAN.

## Grounding (investigate during DESIGN; verify against current code)

- Workspace verbs: `niwa create`, `niwa apply`, `niwa destroy` — what each does
  (materialize instance, install claude content/settings/hooks, snapshot, vault,
  registry; re-sync on apply; teardown on destroy).
- Content installers: internal/workspace/content.go (InstallWorkspaceContent,
  InstallGroupContent, InstallRepoContent -> CLAUDE.local.md), workspace_context.go
  (InstallWorkspaceContext, InstallWorkspaceRootSettings, overlay/global), apply.go
  pipeline ordering.
- Worktree verbs (post-mesh-removal): `niwa worktree create|destroy|list|attach|detach`
  in internal/cli/ (session.go, session_lifecycle_cmd.go, session_attach_register.go),
  backed by internal/worktree/ (CreateSession, DestroySession, scaffoldWorktreeNiwa).
  Worktrees land at <instance>/.niwa/worktrees/<repo>-<sid>/.

## Public-clean

niwa is a public repo; all artifacts must reference only public paths. No private
repo references.
