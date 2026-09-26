---
schema: brief/v1
status: Done
problem: |
  niwa expands resolved secrets into a single hardcoded per-repo file,
  .local.env. That name is not a convention in most stacks, so the file
  a tool actually reads (.env, .env.local, secrets.json) still has to be
  produced by hand, defeating the point of having niwa materialize it.
outcome: |
  An operator declares, per repo, which file(s) niwa expands secrets into
  and in which format, niwa writes exactly those, and each file stays
  invisible to the repo's git status regardless of its name. Repos that
  declare nothing keep getting .local.env, unchanged.
motivating_context: |
  Raised after shipping configurable .env.example failure policy (#155)
  and self-guaranteed git invisibility for managed repos (#158): with
  invisibility no longer tied to the .local name convention, the output
  filename is free to become a per-stack choice.
---

# Brief: Configurable secret-output targets

## Status

Done

Framing settled before drafting via a brainstorming pass. UX decisions
(default posture, configurability granularity, precedence, format
selection) are settled in this brief. The downstream PRD owns the
requirements articulation; the only items deferred to it are naming and
config-nesting mechanism, recorded under Open Questions.

## Problem Statement

niwa resolves a repo's secrets (from `vault://` references, inline
`[env.vars]`/`[env.secrets]`, and the `.env.example` pre-pass) and
expands them into one file per repo. That file's path is hardcoded to
`.local.env`.

`.local.env` is a niwa-specific name. Almost no language or framework
reads it. Node and Next.js read `.env.local`; python-dotenv, Django, and
Rails read `.env`; some tools want a structured `secrets.json`. So an
operator who points niwa at a real project still has to copy or rename
`.local.env` into whatever the stack expects, by hand, on every repo.
The materialization step that was supposed to remove manual secret
handling stops one step short of the file the application loads.

The hardcoded name is also a single shape: `KEY=value` dotenv text. A
consumer that wants its secrets as JSON or as a sourceable shell script
has no path at all, regardless of filename.

The gap is that the *destination* of secret expansion -- both its path
and its serialization -- is fixed in code, while the thing that decides
the right destination (the repo's language and tooling) varies per repo.

## User Outcome

An operator managing a multi-repo workspace declares, on a per-repo
basis, which file or files niwa should expand that repo's secrets into,
and the serialization each should use. On the next `niwa apply` (or
worktree apply), niwa writes exactly those files with the repo's
resolved secrets, and each written file is invisible to that repo's git
status, so a real secret never becomes committable no matter what the
operator named it.

The operator does not configure anything they do not care about: a repo
that declares no target keeps receiving `.local.env` in dotenv form,
byte-for-byte as before. The choice is opt-in and inherited -- set a
workspace-wide default once and let individual repos override it where
their stack differs.

## User Journeys

### Polyglot operator gives each repo its native filename

An operator runs a workspace with a Next.js front end and a Python
service. Today both get `.local.env` and neither framework reads it. The
operator sets the front-end repo's secret-output target to `.env.local`
and the service repo's to `.env`. On the next apply, each repo receives
the file its framework already loads, with no manual rename, and the
applications pick up their secrets with no extra wiring.

### Tool consumer asks for a structured format

An operator has a repo whose sidecar tool reads a JSON secrets file
rather than dotenv. The operator sets that repo's target to
`secrets.json`. niwa infers JSON from the extension and writes a flat
JSON object of the resolved secrets. The tool reads it directly; the
operator never hand-translates dotenv into JSON.

### Operator sets a workspace-wide default, overrides one repo

An operator whose repos are mostly Node sets a single workspace-level
default of `.env.local` so every repo gets the Node-conventional name
without per-repo configuration. One repo is a shell-tooling project that
wants a sourceable file; the operator overrides just that repo with a
`.sh` target, and the more specific repo setting wins over the workspace
default.

### Custom name stays git-invisible

An operator points a repo's target at `.env` -- a name git does not
ignore by default. After apply, the operator runs `git status` inside
that repo and sees a clean tree: the resolved-secret file does not
appear as untracked, because niwa recorded ignore coverage for the
chosen name. The operator never risks committing a real secret, and
never edits the repo's committed `.gitignore` to get that guarantee.

## Scope Boundary

### In

- Per-repo declaration of one or more secret-output target files,
  inherited through a repo -> workspace -> personal/global precedence
  with `.local.env` as the default when nothing is declared (no behavior
  change for repos that declare nothing).
- A small set of output serializations -- dotenv (the current shape),
  JSON, and sourceable shell-export -- selected per target by inferring
  from the file extension, with an explicit per-target override for the
  ambiguous case.
- Preservation of the existing git-invisibility guarantee for every
  chosen name: a custom target is recorded as niwa-managed ignore
  coverage so resolved secrets never become committable, with the same
  fail-closed posture niwa already applies to managed-repo invisibility.
- The same behavior on both materialization paths (instance apply and
  worktree apply), so a target declared once behaves identically wherever
  niwa writes the repo.

### Out

- Additional serializations beyond the three named (for example YAML).
  Deferred until a concrete consumer needs one; the format mechanism is
  built to admit a fourth without redesign, but no fourth ships here.
- Free-form template rendering (supplying a template file niwa fills in).
  A different, larger feature; the target here is named files in known
  formats, not arbitrary output shapes.
- Per-variable routing of different secrets to different files. Every
  resolved secret for a repo goes to all of that repo's declared targets;
  splitting individual variables across files is out.
- Changing *which* values get resolved or how they are sourced. The
  existing `[env.vars]`/`[env.secrets]` resolution and the `.env.example`
  pre-pass are untouched; this feature changes only the destination of
  the already-resolved set.

## References

- `docs/briefs/BRIEF-env-example-failure-policy.md` -- prior brief in the
  same env-handling area; precedent for the repo -> workspace ->
  global precedence cascade reused here.
- `docs/briefs/BRIEF-repo-git-invisibility.md` -- frames the managed-repo
  git-invisibility guarantee this feature extends to custom names.
