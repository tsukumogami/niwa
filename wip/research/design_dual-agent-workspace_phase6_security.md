# Verdict: FAIL

## Trust grant

The design states the grant honestly and better than most: the second
Security Considerations bullet says plainly that a trust entry enables
project-local config, exec policies, and (if declared) hooks to load from
repository content, including a committed `.codex/` anywhere in the tree,
and it names the bounds (Codex's project-layer denylist; hook execution
gated on hash state niwa never writes). It also records the presence-not-
value quirk of the sandbox gate, which is exactly the kind of measured
detail this section should carry. Scope is as tight as the mechanism
allows: one path-scoped entry per cloned repository, nothing global, and
worktree coverage comes free through the `.git` pointer rather than extra
entries.

Two soft spots, neither disqualifying on its own. First, "exec policies"
is the vaguest phrase in an otherwise concrete section — if the project
layer of a trusted repository can loosen approval or sandbox policy in
codex-cli 0.147.0, that is the single worst thing third-party content can
do with the grant, and the design should name it concretely rather than
gesture at it (or state that those keys are denylisted, if measured).
Second, the justification "the developer who clones a repository is
expressing the same intent the trust prompt would have asked about" is
arguable — developers clone repositories to read them, not only to trust
them — but the design accepts and states the residual risk rather than
hiding it, which is what honesty requires.

## Third-party repository content

This is where the design has a real hole. It handles the collision it
thought of (`AGENTS.md`, solved by inlining) and explicitly declares the
collision guard unnecessary — but that reasoning covers only `AGENTS.md`.
The design writes two things into every repository working tree at fixed
names, `.codex` and `AGENTS.override.md`, and never says what happens
when a repository *commits* content at either name:

- A committed `.codex/` directory at the repository root is a real Codex
  feature, and the design's own trust bullet acknowledges repositories
  can ship one. niwa's symlink write then either fails, or replaces
  committed content (violating R12), or — if niwa skips on conflict —
  the third-party repository's own `.codex/` loads as the *trusted*
  project layer while niwa's payload, budget declaration, and skills
  silently never arrive for that repository. That last shape is a
  hostile repository impersonating the payload, with niwa's trust entry
  vouching for it, and it is precisely the silent minority-case failure
  the design's own drivers exclude.
- A committed `AGENTS.override.md` gets clobbered by niwa's
  unconditional write. "A name repositories do not commit" is an
  assumption about third-party behavior, not a guarantee; the design
  used exactly this reasoning to reject Option 2B ("any repository can
  become one at any time with a single commit").
- In both cases the git-exclude mitigation is inert: exclude patterns
  suppress *untracked* files only. If the name is tracked, niwa's write
  shows as a modification or deletion in `git status`, violating R11/R12
  in a way the bare-pattern discussion never contemplates.

A hostile actor is not even required — a careless upstream adopting
Codex's own project-layer convention is enough. The design must define
the conflict behavior; it currently doesn't know these cases exist.

## Writes to the developer's configuration

Scope and additivity are well specified (byte-preservation of everything
else, no global keys, idempotent upsert, no accumulation — all pinned by
PRD criteria). Cleanup is addressed honestly, though "an entry whose path
no longer exists grants nothing" overstates: a stale entry trusts
whatever lands at that path later. Within niwa-managed paths that is
low-consequence, but "clutter, not capability" should be softened to
"trusts future content at a niwa-shaped path", and cleanup on instance
destruction should stay on the roadmap.

The failure modes are the gap. The design (and the PRD behind it) is
silent on all three of:

- **Concurrency.** Two instances applying at once both read-modify-write
  the developer's single `config.toml`. Without serialization or a
  compare-and-retry, one instance's entries are silently dropped — or
  the file is interleaved into invalid TOML. The design already supports
  multiple instances per workspace, so this is not hypothetical.
- **Interruption.** Nothing specifies atomic replacement (write-temp,
  fsync, rename). A crash mid-write leaves the developer's Codex config
  truncated — the one file the design says it must treat most carefully.
- **Malformed pre-existing file.** If the developer's config doesn't
  parse, what does niwa do? Refuse and report, leaving the file
  untouched, is the only acceptable answer, and the design should say
  so; a parse-and-rewrite pipeline that "repairs" what it misparses is
  how additivity guarantees get violated in practice.

Batch 3 exists precisely to give this write "its own review focus"; these
three behaviors are what that focus is for, and they're unstated.

## Credentials

Internally consistent: Decision 6, the security section's never-read/
never-write claim, and batch 3's tolerate-unreadable-credential-file all
line up, and the PRD pins byte-identity of the credential files. The
reasoning for declining the API key is sound and well evidenced — inert
under a working login, a silent metered fallback under a broken one, and
`forced_login_method` measured to fail open (`chatgpt`) or destroy the
credential file (`api`). Leaving the broken-login failure loud is the
safer posture for an unattended preparation tool. One caveat: the
symlink finding below can make niwa read credential *content*
transitively, which would falsify the never-reads claim — fix that and
this section is clean.

## Symlink hazards

One serious, one moderate:

- **Inlining follows symlinks (serious).** Decision 2A has niwa read a
  repository's committed `AGENTS.md` and inline its content into
  `AGENTS.override.md`. Git faithfully reproduces committed symlinks. A
  hostile repository committing `AGENTS.md -> /home/<user>/.codex/auth.json`
  (or any guessable sensitive path) gets the target file's contents
  read by niwa and written into the instruction context of every Codex
  session in that repository — automatic disclosure into agent-visible
  context, and a direct contradiction of "the credential and login
  files are never read." The actor is a repository author, the path is
  one committed symlink, and the payload is niwa's own composition
  step. The design never considers it. Composition must refuse a
  symlinked `AGENTS.md` (or verify the resolved target stays inside the
  repository working tree) — same discipline for any other repo file
  the composer ever reads.
- **The committed-`.codex` conflict** doubles as the link-replacement
  hazard (covered above): the design considers a session rewriting the
  link mid-session (acknowledged, bounded) but not the repository
  arriving with the name already occupied.

The in-session write-through-symlink exposure is acknowledged and fairly
bounded, with one refinement: "regenerated on every apply" covers the
payload and its symlinks, but tampering the *targets* (the shared plugin
install roots) persists across applies unless plugin content is also
reconciled — and those roots are shared by every instance and both
agents, so the blast radius is workspace-wide, not per-instance.

## Residual risk honesty

Genuinely good faith: the section names stale trust entries, the
third-party voice in session config, and mid-session skill tampering,
and the Consequences section admits the upstream-invariant fragility.
This is not a design hiding behind the word "trust". But the named
residuals are the ones the authors mitigated or measured; the unnamed
ones — name-collision impersonation, symlinked-`AGENTS.md` disclosure,
and the concurrent/interrupted/malformed write modes on the developer's
file — are the harder ones, and they're absent because they weren't
seen, not because they were weighed.

## Required changes

1. **Define conflict behavior for committed `.codex` and
   `AGENTS.override.md`.** When a repository already contains either
   name (tracked file, directory, or symlink), niwa must not overwrite
   or delete committed content (R12) and must not silently let the
   repository's own `.codex/` stand in for the payload under a
   niwa-written trust entry. Detect the conflict, skip the write, and
   surface it loudly (the design's own anti-silent-failure driver
   demands a signal). Also correct the git-exclude discussion: exclude
   patterns are inert for tracked paths, so the conflict case needs its
   own handling, not a pattern.
2. **Make committed-`AGENTS.md` inlining symlink-safe.** Refuse to
   inline (or even read) a committed `AGENTS.md` that is a symlink, or
   verify the resolved path stays inside the repository working tree.
   State the rule in the design; it protects the never-reads-credentials
   claim as much as R12.
3. **Specify the write discipline for the developer's Codex config.**
   Atomic replacement (temp file + rename), refuse-and-report on an
   unparseable pre-existing file (never rewrite what didn't parse), and
   a serialization or retry story for concurrent applies from multiple
   instances. These belong in Decision 4 / batch 3 and in the Security
   Considerations bullet that already covers scope and additivity.

## Optional improvements

- Name concretely whether a trusted repository's project layer can set
  approval/sandbox policy in 0.147.0, instead of "exec policies"; if it
  can, say that is the worst case of the grant, and if denylisted, say
  that too.
- Soften "an entry whose path no longer exists grants nothing" to
  acknowledge it trusts future content at that path, and keep trust-entry
  removal in the instance-destruction lifecycle as planned work.
- Note that apply-time regeneration does not heal tampered plugin
  install roots (the symlink targets), which are shared across instances
  and both agents; plugin reconciliation is the healing mechanism there.
