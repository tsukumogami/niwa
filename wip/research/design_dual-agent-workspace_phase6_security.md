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

# Round 2

# Verdict: PASS

## Required change 1 — conflicts (closed)

The rule landed with the property, not just wording. Decision 7 defines
the conflict predicate correctly (anything at either name that niwa did
not itself materialize — tracked or untracked, file, directory, or
symlink), specifies skip-modify-nothing-report-loudly, and the
git-exclude discussion now states the pattern is inert for tracked
paths, which was the half-truth I flagged. The trust-withholding does
close the impersonation path I identified: with no entry, the
repository's own `.codex/` config layer does not load trusted on niwa's
signature — the decision is pushed back to Codex's own prompt, where
the developer sees what they are trusting. The "Conflicts with
committed content" subsection, the Security bullet naming the
impersonation shape, and the honest opt-out negative in Consequences
are all present and consistent with each other and with batch 2/3
sequencing (batch 3 consumes batch 2's conflict verdicts).

One residual is prospective-only, and I judged it non-blocking: apply
"leaves existing entries untouched", so a repository that becomes
conflicted *after* its entry was written (developer removes niwa's
symlink to unblock a checkout that brings a committed `.codex/`) keeps
a standing trust entry that now vouches for repository-authored
content. Reaching it requires the developer to remove niwa's own
symlink (git refuses to overwrite the untracked link on
checkout/pull), every subsequent apply reports the conflict, and the
underlying exposure — content of a trusted repository changing over
time, branches included — is Codex's repo-granular sticky trust model,
which niwa cannot fix and the trust bullet's residual already covers.
Named below as optional.

## Required change 2 — symlink-safe inlining (closed)

The refusal rule is the right one and is stated where it binds:
lstat-not-stat, regular-file-only, generalized to "any other repository
file the composer ever reads", applied identically to worktree
checkouts, and treated as a Decision 7 conflict (no override written,
so nothing displaces native discovery; loud report). "Regular file" is
the correct predicate — it excludes symlinks, directories, and special
files in one check, with no resolution edge cases. The refusal-over-
resolve rationale is sound, and tying the never-reads-credentials claim
explicitly to this rule ("true only because of this rule") is exactly
the drift-proofing I asked for. One overstatement, non-blocking: "no
window between the check and the read" is not strictly true of
lstat-then-open — see optional below. The racing actor for that window
must already be executing in the working tree during apply, which is
outside this design's threat model.

## Required change 3 — write discipline (closed)

All three properties landed as decision text, not aspiration: atomic
same-directory temp-file + fsync + rename (interruption leaves the
prior file intact), refuse-and-report on an unparseable pre-existing
file with the file left byte-untouched, and an advisory lock across the
whole read-modify-write with a re-read under the lock. Batch 3 carries
them explicitly and the Security bullet's new *discipline* bound names
all three. The honest caveat that Codex's own concurrent writes are
outside the lock — the same exposure Codex's own sessions already carry
— is correct and correctly scoped.

## Regression check — architecture-batch text

- **Trust-key canonicalization (Decision 4):** a security improvement,
  not a regression. Resolving symlinks before keying prevents the
  silently-miskeyed read-only-sandbox failure, and the canonical path
  still resolves inside niwa-managed directories, so the scope claim
  holds.
- **Plugin install-root resolution (Decision 3):** the github-sourced
  case makes Claude Code's user-global plugin cache a symlink target
  reachable from every repository. The Security section handles this
  honestly: regeneration heals the payload and links but not the
  targets, tampering there persists until plugin reconciliation, and
  the exposure is shared with (not added to) the Claude side, which
  reads the same roots. That parity claim is correct — the symlink adds
  an address, not a capability. Missing roots are skipped-and-reported
  rather than left as silent dangling links, which is the right
  anti-silent-failure shape.
- **Worktree composition rework:** the regular-file-only rule is
  explicitly extended to worktree checkouts; no gap introduced.
- My round-one optional items also landed: stale entries are now "not
  fully inert" with removal as planned lifecycle work, and the trust
  bullet concretely names sandbox/approval settings as loadable from a
  trusted project layer — the worst case, named.

## Required changes

None.

## Optional improvements

- **Remove, don't just withhold, on a `.codex` conflict.** When apply
  detects a `.codex` conflict in a repository whose trust entry niwa
  itself wrote earlier, remove that entry (it is niwa's own write, so
  additivity is preserved) — or at minimum have the conflict report
  state that a standing trust entry still vouches for the repository's
  own content. The same report wording should cover the per-worktree
  variant: a conflicted worktree still runs trusted through the main
  repository root's entry, which cannot be withheld per-worktree.
- **Tighten the check-to-read claim.** Replace "no window between the
  check and the read" with the implementation that makes it true:
  open with O_NOFOLLOW (or open-then-fstat) rather than lstat-then-open.
  One flag, and the design's claim becomes accurate as written.

# Round 3

# Verdict: PASS

## The narrowed refusal and the displacement claim

The narrowing is sound, and the author's stronger claim is correct as
scoped. The disclosure vector I identified was niwa's read; the write
never touched it. And the displacement argument holds on the measured
facts already pinned in this document: `AGENTS.override.md` is
hardcoded first in the per-directory precedence, each directory
contributes exactly one context file, and the candidate probe is
trust-independent — so a written override means Codex never opens
`AGENTS.md` in that directory at all. Without the override, Codex's
native read opens the committed file with an ordinary follow-the-link
open and pulls the target into session context. The wide rule
therefore left the symlink sitting in the very slot Codex reads,
delivering through Codex the disclosure niwa refused to perform
itself. The narrow rule is not merely as safe — it is strictly safer,
and the round-two version was actively harmful in exactly the case it
existed for. I confirm the reversal.

Two boundary statements so the claim is not read wider than it holds,
neither blocking because both describe vanilla Codex behavior niwa
neither adds to nor can close: the displacement covers the directory
slot niwa contests (the repository root, and a worktree root). A
committed context-file symlink in a *subdirectory* is still read
natively by Codex when the working directory is at or below it, and
trust does not gate context reads, so niwa's entry does not enable it.
And in the degenerate case where every workspace layer is empty, the
never-empty rule means no override is written and the root slot falls
back to native discovery — the refusal is still loudly reported, and
the exposure is identical to the repository outside any niwa
workspace.

## O_NOFOLLOW enforcement

Implemented better than I asked: the enforcement is the open itself,
the design explicitly rejects a separate pre-check for the swap window
it would leave, and the non-symlink non-regular cases (a committed
directory is the only other shape git can produce) are still refused
after the open. The predicate — content is inlined only from a regular
file reached without following a link — survives the mechanism change
intact. One forward-looking note: the rule's extension to "any other
repository file the composer ever reads" is only fully enforced by
O_NOFOLLOW for paths whose intermediate components are not repository
content. That is true of every path the composer reads today
(`<repo>/AGENTS.md`, `<worktree>/AGENTS.md` — the parent directories
are niwa's clone, not committed content). If a future increment reads
deeper repository paths, a committed symlinked *directory* on the way
down defeats O_NOFOLLOW alone and needs openat2/RESOLVE_BENEATH-style
resolution. Not a defect now; worth remembering then.

## Full degradation on `.codex` conflict

Confirmed safe, not merely degraded. The end state of a
conflicted-`.codex` repository is byte-for-byte vanilla Codex: native
discovery of the repository's own content, Codex's own trust prompt in
the TUI, read-only sandbox under exec, and no niwa-written trust entry
in existence afterward — including removal of an entry niwa wrote
earlier, which closes the late-conflict window I named in round two as
an optional. niwa adds nothing on top of what the repository would
present to a developer who had never run niwa, and the step-back is
loudly reported at apply. The internal reasoning is also right in two
places I checked deliberately: suppressing the override along with the
link is correct because an override without the payload's budget
declaration would compose a chain into the 32768-byte default and
silently truncate the repository layer — writing it would manufacture
a silent failure; and the decoupled reverse case (override conflict
alone) keeps the trust entry safely, because the thing the entry
vouches for — the `.codex` layer — is still niwa's own payload, so the
impersonation invariant "niwa's signature vouches only for what niwa
wrote" holds in both branches. The marker-based self-recognition is
acceptable: tracked content is a conflict regardless of marker, a
forged untracked marker file at niwa's own name is simply overwritten
at the next apply with nothing committed touched, and trust removal
retracts only niwa's own keys.

## Required changes

None.

## Optional improvements

None beyond the two boundary notes above, recorded here so they are
not rediscovered: the displacement defense is per-slot, not per-tree
(subdirectory context symlinks remain Codex-native behavior), and
deeper repository reads in a future increment need stronger path
resolution than O_NOFOLLOW.
