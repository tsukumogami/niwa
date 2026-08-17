# Verdict: FAIL

The criteria are well-shaped: they name observable commands, they mostly avoid
enumerating filenames the design hasn't chosen yet, and two of them (the
`git status` check and the sentinel-file credential check) are genuinely good
tests that would fail loudly in the broken case. The failure is concentrated in
a specific place: the criteria written to catch the *silent* modes the
exploration found are, with one exception, phrased so that the test they imply
passes in exactly the broken case they exist to prevent.

Three defects are load-bearing:

1. **AC6 (truncation) has no threshold.** "Even when the instance- and
   group-level context is large" is the entire discriminating condition, and it
   is unquantified. Codex's `project_doc_max_bytes` default is 32768, shared
   across the whole walk chain and drained root-first. A test whose "large"
   outer layers total 4KB passes whether or not the implementation raised the
   budget. This is the marquee anti-silent-failure criterion in the PRD and it
   currently cannot fail.
2. **AC2 carries an unbounded exemption.** "The existing test suite passes
   unmodified except for tests that assert the old exclusivity" lets any
   inconvenient failing test be reclassified into the exemption at review time.
   The exploration already enumerated the affected tests, so the exemption can
   be closed to a named list.
3. **R13's non-credential half has no detector at all.** Every criterion about
   the developer's Codex setup is about credentials. But the chosen design
   writes one trust entry per cloned repo into the developer's personal
   `~/.codex/config.toml`, and nothing in the criteria would detect niwa
   clobbering, reordering, or dropping the developer's own settings in that
   file, or writing a global key (`project_root_markers`,
   `project_doc_fallback_filenames`) that degrades Codex in every repository on
   the machine. That last failure is the exact one Architecture A was rejected
   for, and it is currently unenforced.

Separately, twelve of the fourteen criteria are phrased as "a Codex session
sees / receives / can do X" with no stated offline proxy. In a CI without
`codex` on PATH — which is where these will actually run — the implementer
chooses the proxy, and the proxy they will naturally reach for (does the file
exist, does it contain the string) is precisely the one that passes in the
silent-failure cases. The proxies need to be in the criteria, not left to
implementation.

---

## Per-criterion assessment

Criteria numbered in document order.

### AC1 — instance root, no agent config, sees instance-level context

**Decidable:** No, as worded. **Test:** unstated; realistically "the
instance-root context file exists and contains a marker from the workspace's
configured content." **Catches the real failure:** partially. The instance root
is not a git repository, so Codex's walk there is degenerate (cwd only) and this
criterion exercises none of the walk mechanics, none of the budget accounting,
and none of the trust path. It is a smoke test, which is fine — but it should
not be read as covering R4, because the layering claim in R4 is untested here.

Offline proxy that works: the instance-root context file exists, is non-empty
after whitespace stripping (the empty-slot trap), and contains a sentinel from
`[claude.content.workspace]`.

### AC2 — Claude side byte-for-byte identical

**Decidable:** the first clause yes with a scoping fix, the second clause no.

The first clause needs its comparison set named. After this change the instance
tree is *not* byte-identical — it gains a Codex payload. As written, a literal
reading of "the files ... a Claude session sees in a prepared instance" is
either trivially false (whole tree) or undefined (which subset?). Make it: the
set of paths niwa materializes for Claude today — the `CLAUDE.md` tree,
`.claude/`, the settings file, and the skills tree — compared by content hash
between a pre-change and post-change apply of the same workspace config.

The second clause is the un-failable one. "Except for tests that assert the old
exclusivity" is a category the implementer defines after seeing which tests
fail. The exploration already did the enumeration: three functional scenarios in
`test/functional/features/codex-agent.feature`, two of whose assertions invert,
plus a countable set of unit tests asserting "the other file does not exist."
Name them, or cap it ("no test outside `codex-agent.feature` and
`internal/workspace/content_test.go` is modified").

Why this matters beyond bookkeeping: the dispatch-refusal scenario in that same
feature file is the only thing enforcing an Out of Scope promise (dispatch stays
Claude-only). Under the current exemption clause, deleting it is defensible.

### AC3 — three levels deep inside a cloned repo

**Decidable:** the environment half yes, the "sees" half no.

"From a plain shell with no niwa-set environment" is a good, decidable
condition, and it should be made explicit about what is scrubbed: no `NIWA_*`,
and specifically **no `CODEX_HOME`** — the whole architecture rests on not
needing it, and a test that leaks it from the developer's shell would pass while
the no-home property was broken.

The nesting depth is the right instinct but currently proves less than it looks.
Codex takes at most one file per directory walking project-root → cwd; nesting
three empty levels deep changes nothing about what is found at the repo root.
The test would pass against an implementation that only ever worked at repo
roots. To make the depth load-bearing, the fixture needs a candidate file in an
intermediate directory (a subdirectory with its own committed `AGENTS.md`), so
the criterion actually exercises per-directory first-match rather than just
"walk happened."

### AC4 — worktree sees workspace context plus worktree framing

**Decidable:** yes if operationalized as file content; the step
`the file "X" in the last worktree contains "Y"` already exists, and
`worktree.feature` gives the fixture. **Catches the real failure:** no, and this
is the criterion I am least comfortable with.

`git worktree add` creates `.git` as a *file* containing a `gitdir:` pointer,
not a directory. Whether Codex's default `.git` project-root marker matches a
file is unverified in the exploration record — every measurement quoted was
against ordinary clones. If it does not match, a worktree has no project root,
discovery collapses to cwd-only, and the outer layers vanish while the
worktree's own file is still found and read. The session looks completely
healthy. A file-content proxy ("the worktree's context file contains the repo
name and branch") passes in that broken state.

The criterion needs to assert the composition *from the outer layers* is
reachable in the worktree, distinguishably from the worktree's own file — e.g.
an instance-level sentinel that appears only in the instance-level content, and
a check that the marker-detection assumption holds for a worktree's `.git` file
(a one-line measurement against codex-cli, or a fixture assertion that whatever
marker niwa relies on is present in the worktree as a directory).

### AC5 — repo shipping its own committed context file

**Decidable:** the byte-identical half yes and it is the strongest half of the
criterion — it directly enforces R12 and it would fail loudly if niwa wrote its
context as `AGENTS.md`. Keep it exactly as is.

The "receives both" half is not decidable and, worse, its natural proxy misses
the failure. The failure mode is *ordering*, not content: if niwa's context
lands in a filename that loses first-match to the repository's committed
`AGENTS.md`, niwa's content silently never arrives while the committed file is
still byte-identical, so both a content check on niwa's file and the byte-
identity check pass. The exploration flagged exactly this (`public/shirabe`
ships its own `AGENTS.md` today, and any repo can become one at any time with no
error and no signal).

Make the ordering observable: "the single file Codex would select for that
directory contains both the workspace context and the repository's own content,
and no candidate file of higher precedence than niwa's exists in that
directory."

**Fixture feasibility:** needs new support. `aSourceRepoExists` creates an empty
bare repo. You need a source repo with a committed file. Either extend
`localGitServer` with a `RepoWithFiles(name, map[string]string)` helper (small,
and it unblocks AC3's intermediate-directory case too), or commit into the clone
after `niwa create` and re-apply — the latter is cheaper but tests a different
sequence than the one users hit.

### AC6 — end-of-content marker survives large outer layers

**Decidable:** no. **Catches the real failure:** no, as written. This is the
defect that decides the verdict.

The mechanic is precise: 32768-byte default budget, shared across the chain,
drained root-first, truncation is a raw byte cut with no marker and nothing on
stderr. "Large" must be pinned to that number or the test is a coin flip. The
criterion should state that the instance- and group-level content together
exceed the default `project_doc_max_bytes` (32768 bytes), so that under the
default budget the repository layer is provably starved.

**Offline proxy that actually decides it** — and this one is good, so the
criterion does not need a live session at all: after `niwa apply`, the budget
configured in niwa's own payload config is greater than or equal to the total
byte size of the composed chain on disk, *and* the fixture's outer layers exceed
32768 bytes. That fails the moment someone ships without raising the budget, and
it fails offline, deterministically, in CI. Pair it with the live check gated on
`codex` being on PATH (skip-not-fail, following the `claude is available`
precedent in `session_attach.feature`).

### AC7 — skill parity

**Decidable:** "under the same name" no; "a sampled skill's content matches" yes
but too weakly to matter. **Catches the real failure:** no, on two counts.

*Sampling.* The design commitment is verbatim delivery with no content
transformation. A sample of one skill passes against an implementation that
transforms skills matching some pattern — e.g. rewrites `${CLAUDE_PLUGIN_ROOT}`,
which the exploration specifically established must *not* happen because it
corrupts self-referential documentation and misses the `:-` fallback form. The
criterion should be a full tree comparison: every delivered file byte-identical
to its source, no file added, no file omitted.

*What sampling a `SKILL.md` cannot see.* The exploration's finding is that a
detached skill copy "loses its namespace and orphans its references" — every
reference points at plugin-root `references/` and `scripts/` living *above* the
skill. An implementation that copies `skills/**` and nothing else produces
skills that load, resolve by name, and have byte-identical `SKILL.md` files, and
whose every reference is a dangling path. Sampling a `SKILL.md` passes. The
criterion must assert the *plugin root* is delivered whole: `plugin.json`
present at the root of what is delivered, and sibling `references/` and
`scripts/` directories present and byte-identical where the source has them.

*Namespacing offline.* "Available under the same name" is decidable without a
session, because namespacing derives from the nearest `plugin.json` on disk:
assert the delivered path resolves (through the symlink) to a directory
containing a `plugin.json` whose declared name matches the namespace Claude
sees, with `skills/<skill>/SKILL.md` present for every skill in the Claude tree.

### AC8 — can write a file on first attempt

**Decidable:** no, and it needs the trust mechanism to become decidable.
**Catches the real failure:** only live.

The failure is precise and offline-checkable: a missing
`[projects."<absolute path>"]` entry drops the session to a read-only sandbox;
the recorded *value* is irrelevant, only presence matters. So the proxy is
"after `niwa apply`, an entry exists keyed by the exact absolute on-disk path of
every cloned repo (and every worktree)." Keying is where this fails silently — a
stale entry from a previous instance path, or a path that differs by a symlink
resolution or a trailing separator, is present-but-wrong and yields the same
read-only sandbox with a config file that looks correct to a human reader. The
criterion should say the entry's key resolves to the same path as the repo's
actual root.

### AC9 — interactive session, no trust or review prompt

**Decidable:** no offline; live only. This was still an open unknown when the
exploration handed off, and the PRD is right to carry it as a criterion.

Two things make it usable. First, a decidable observation of "no prompt": run
under a PTY with no input supplied (the `I run "X" under a pty with input "Y"`
step exists), assert the session reaches its ready state within a bounded time
and its output contains no approval/trust prompt text — a prompt manifests as a
hang. Gate on `codex` being on PATH and skip otherwise, matching how the
`claude`-dependent attach scenarios keep `@critical` CI fast and offline.

Second, an offline proxy for the *other* prompt source. The Decisions section
makes hook-absence load-bearing for R10 — "an interactive Codex session blocks on
a review prompt for a hook it cannot verify." Nothing in the criteria asserts
niwa ships no hooks. Add: after `niwa apply`, the payload contains no
`hooks.json` and no hook-state entries in the developer's config. That is fully
decidable offline, and it enforces an Out of Scope boundary that is otherwise
enforced only by nobody happening to add hooks.

### AC10 — git status clean in every cloned repository

**Decidable:** yes, existing step
(`the git status of repo "X" in instance "Y" is clean`). **Catches the real
failure:** yes. This is the best criterion in the document.

It enumerates no filenames, so it trips on any new leak automatically — which is
what makes it the detector for the highest-risk trap in the feature, the bare
`.codex` versus `.codex/` exclude pattern. The trailing-slash form matches a
directory; `<repo>/.codex` is a *symlink*, which git treats as a file, so the
wrong pattern leaves permanent dirt that `git status --porcelain` reports. It
also happens to be the general detector for R12 (a modified tracked file shows
as ` M`), which is worth noting because R12 otherwise has only the narrow
byte-identity check in AC5.

One gap: "every cloned repository" excludes worktrees, and worktrees get the
same payload under a different git plumbing path (`.git` as a file, exclude
resolution through the common dir). Extend the criterion to worktrees created by
`niwa worktree`.

The second clause, "again after a Codex session has run there," needs a live
session. It is worth keeping as a gated scenario but it should not be the only
thing standing between the exclude pattern and CI — the first clause already
carries that weight.

### AC11 — no reading or writing of credential files

**Decidable:** the write half yes, the read half no.

Sentinel files in a sandboxed home detect mutation, not access. Two fixes. For
writes, make it concrete: the credential/login files are byte-identical and
their mtimes are unchanged after `niwa create` and `niwa apply`. For reads, the
decidable form is to make the file unreadable — `chmod 000` the credential file
in the sandboxed home and assert both commands still exit 0. That fails if niwa
opens it, and it needs no tracing.

Worth noting this criterion is doing more work than it appears: the exploration
records that `forced_login_method = "api"` triggers an implicit logout that
*deletes the auth file*, so a byte-identity assertion on the auth file catches a
config-writing mistake with destructive consequences, not just an etiquette
violation.

### AC12 — legacy `default_agent` workspace applies with no migration

**Decidable:** the "succeeds with no migration step" half yes (exit 0, no
prompt, no error). The "serves both agents per the criteria above" half is
decidable if the criteria above are turned into a reusable scenario outline;
otherwise it is a pointer to nothing.

Gap: R14 has two clauses and only one is covered. "The setting retains its
launch-time meaning (which agent a niwa-launched session runs)" has no criterion
deciding it. That clause is what keeps the dispatch refusal coherent — an Out of
Scope promise — and it is currently verified only by an existing scenario that
AC2's exemption clause makes deletable.

### AC13 — never-declared workspace serves both agents

**Decidable:** yes, same shape as AC1. Sound. (Its overlap with AC1 is a
completeness/clarity question, not mine.)

### AC14 — re-apply leaves everything passing

**Decidable:** yes as a re-run of the suite. Sound as far as it goes, and the
idempotency instinct is right.

Two things it does not reach:

*Accumulation.* The specific idempotency failure for this feature is duplication
in append-shaped state: the exclude block written twice (the repo already has
the precedent assertion — `contains "# >>> niwa managed >>>" exactly once`), and
trust entries duplicated in the developer's `config.toml` on every apply, which
either produces a TOML duplicate-key parse error or unbounded growth. "All the
above criteria still passing" catches the parse error only via a live session.
Add an explicit count assertion after three applies.

*Staleness.* R3 requires materializations be left "current," which means a
changed source context propagates. Nothing tests refresh — only that a repeated
apply of *unchanged* config is harmless. A stale composed context file is a
textbook silent failure: the session starts, reads yesterday's context, and
looks perfect. Add a criterion that changes the workspace's configured content,
re-applies, and asserts the new marker is present and the old one is gone.

*Worktree apply.* R3 says "creating **or applying** a worktree." AC4 covers
create; nothing covers `niwa worktree apply` refreshing the Codex side. There is
existing precedent to build on in `worktree-env-parity.feature`.

---

## Silent-failure detection

Each mode from the exploration handoff, the criterion meant to catch it, and
whether it would.

| Silent mode | Criterion | Would it catch it? |
|---|---|---|
| Exclude pattern written `.codex/` instead of bare `.codex`, leaving a permanently dirty `git status` | AC10 | **Yes.** The strongest coverage in the document. Extend to worktrees. |
| Shared byte budget drains root-first; oversized outer layers silently truncate the repo layer with no marker and nothing on stderr | AC6 | **No.** "Large" is unquantified against a known 32768 default. Would pass in the broken case. |
| Empty or whitespace-only context file claims its directory's single slot and suppresses every remaining candidate | *none* | **No criterion exists.** See below. |
| Missing project trust entry drops the session to a read-only sandbox | AC8 | **Only live.** Needs the presence-and-key-correctness proxy stated. |
| Stale/wrong trust key (right file, wrong absolute path) | AC8 | **No.** Present-but-wrong reads as correct to a human and to a naive existence check. |
| Repo ships its own `AGENTS.md`; niwa's context loses first-match and silently never arrives | AC5 | **No.** The byte-identity half passes, the content half passes; the *ordering* is what breaks and nothing observes it. |
| Skills delivered detached from their plugin root — namespace lost, `references/` and `scripts/` orphaned | AC7 | **No.** Sampling a `SKILL.md` passes with every reference dangling. |
| Content transformation applied to some skills | AC7 | **No.** A one-skill sample does not detect a pattern-scoped rewrite. |
| Worktree `.git` is a file, not a directory; if it is not a project-root marker, discovery collapses to cwd and outer layers vanish | AC4 | **No.** A file-content proxy on the worktree's own file passes while the outer layers are gone. |
| Composed context goes stale after the source changes | *none* | **No criterion exists.** Makes R3's "current" unverifiable. |
| niwa clobbers or reorders the developer's own `~/.codex/config.toml` settings | *none* | **No criterion exists.** Makes R13 unverifiable. |
| A global key (`project_root_markers`, `project_doc_fallback_filenames`) degrades Codex in every repo on the machine | *none* | **No criterion exists.** This is the failure Architecture A was rejected for and it is unenforced. |
| Stale hook `trusted_hash` degrades in complete silence | n/a — hooks out of scope | Correctly excluded, **but** nothing asserts no hooks are shipped, and hook-absence is load-bearing for R10. |

The empty-slot case deserves its own note because it interacts with R6. If a
workspace configures no content, and niwa still writes its context file
unconditionally (as the chosen shape does), the resulting empty-or-whitespace
file claims the directory's only slot and suppresses the repository's own
committed `AGENTS.md`. The developer gets *nothing* — strictly worse than before
the feature — with no error anywhere. AC5 covers R6 only in the case where
workspace context is non-empty, so R6 is unverified in exactly the degenerate
case that produces total loss.

---

## Offline verifiability

Twelve of fourteen criteria are phrased in terms of what a Codex session "sees",
"receives", or "can do". None can be decided in CI without `codex` installed,
authenticated, and reachable — and AC9 additionally needs a paid interactive
session. Left unspecified, the proxy becomes the implementer's choice at the
moment they are most motivated to pick an easy one.

Proxies that decide the same question offline:

- **AC1, AC3, AC4** — assert on the *selected* file for the directory (the one
  Codex's first-match rule would pick), not on file existence: it exists, it is
  non-empty after whitespace stripping, it carries a distinct sentinel from each
  layer that should be present, and no higher-precedence candidate shadows it.
- **AC6** — the payload's configured budget is at least the composed chain's
  total byte size on disk, with fixture outer layers deliberately exceeding
  32768. Fully deterministic; fails exactly when the implementation forgets to
  raise the budget.
- **AC7** — resolve the delivered path through the symlink and diff the whole
  plugin tree against its source, asserting `plugin.json` at the root and every
  sibling `references/`/`scripts/` present.
- **AC8** — a trust entry exists keyed by a path that resolves to each repo's
  and worktree's actual root.
- **AC9** — no `hooks.json` and no hook-state entries anywhere in what niwa
  writes; plus the live PTY scenario gated on `codex` being on PATH, skipping
  rather than failing, following the `claude is available` precedent.
- **AC10, AC11, AC12, AC13, AC14** — already offline-decidable.

Anything that genuinely needs a live session (AC9's interactive start, AC10's
"after a Codex session has run") should be a separate gated scenario, never the
sole coverage for a mechanism, and the PRD should say so — the exploration's own
known unknown was a TUI behavior, so this is the one place where a live check
carries information nothing else does.

---

## Negative-case enforceability

| SHALL NOT | Detecting criterion | Enforceable? |
|---|---|---|
| R6 — not modify or replace the repository's file | AC5, byte-identical after apply | **Yes.** |
| R6 — not *suppress* the repository's file | AC5, "receives both" | **No.** Suppression is an ordering failure; nothing observes ordering. |
| R7 — outer layers not crowd out, truncate, or displace the innermost | AC6 | **No** as worded; unquantified threshold. |
| R11 — nothing appears in `git status`, in any state | AC10 | **Yes** for clones; **no** for worktrees. |
| R12 — not overwrite any file a repository ships | AC5 (narrow) + AC10 (general — a modified tracked file shows as ` M`) | **Yes**, and worth stating that AC10 is the general detector. |
| R13 — never read or write the developer's credentials | AC11 | **Write half yes** (make it byte-identity + mtime). **Read half no** — sentinels do not detect reads; use `chmod 000` and assert exit 0. |
| R13 — not modify the developer's Codex installation or configuration defaults | *none* | **No.** And the design deliberately writes into the developer's personal config, so this needs a bounded-diff criterion, not an untouched-file one. |
| R13 — not change how Codex behaves outside niwa-managed instances | *none* | **No.** This is the property Architecture B was chosen to preserve. |
| R1 — no per-instance configuration needed | AC1, AC13 | **Yes.** |
| R2 — Claude side unchanged | AC2 | **Weak.** Comparison set undefined, exemption clause unbounded. |
| R5 — no environment preparation, wrapper, or shell integration | AC3 | **Partly.** Make the scrubbed set explicit, especially `CODEX_HOME`. |
| R14 — setting retains its launch-time meaning | *none* | **No.** |

---

## Required changes

1. **AC6: pin the threshold.** Rewrite as: *"With instance- and group-level
   context together exceeding 32768 bytes (Codex's default
   `project_doc_max_bytes`, which is shared across the walk chain and consumed
   root-first), a marker at the end of the repository-level context is still
   delivered in full: after `niwa apply`, the budget configured in niwa's own
   payload is at least the total byte size of the composed chain on disk, and a
   Codex session started in that repository reports the end-of-content marker
   (live check, gated on `codex` availability)."*

2. **AC2: close the exemption.** Replace "except for tests that assert the old
   exclusivity" with a named set — the scenarios in
   `test/functional/features/codex-agent.feature` and the unit tests in
   `internal/workspace/content_test.go` asserting "the other file does not
   exist" — and state that no other test is modified or deleted. Also name the
   comparison set for byte-identity: the `CLAUDE.md` tree, `.claude/`, the
   settings file, and the skills tree, compared by content hash between a
   pre-change and post-change apply of the same config.

3. **AC5: make ordering observable.** Add: *"...and the file Codex selects for
   that directory is the one niwa composed; no candidate of higher precedence
   exists there."* Without this, an implementation that loses first-match to the
   repository's committed file passes every clause.

4. **AC7: replace sampling with a whole-tree assertion.** *"Every file niwa
   delivers for skills is byte-identical to its source with no file added or
   omitted; the delivered root contains `plugin.json` and every sibling
   `references/` and `scripts/` directory present in the source; and every skill
   Claude sees resolves under the same namespace."*

5. **New criterion — the developer's Codex config is changed only additively.**
   *"Given a pre-existing developer Codex config containing the developer's own
   settings, after `niwa create` and `niwa apply` that file differs from its
   prior content only by the addition of per-project entries, every one of whose
   path keys resolves inside a niwa instance; no pre-existing key is removed,
   reordered, or altered, and no global key affecting Codex outside niwa
   instances is written."* This is the only criterion that would make R13's
   non-credential clauses enforceable, and it is fully offline-decidable.

6. **New criterion — trust entries are correctly keyed and do not accumulate.**
   *"After `niwa apply`, exactly one project entry exists per cloned repository
   and per niwa-managed worktree, each keyed by a path that resolves to that
   tree's actual root; after three successive applies the count is unchanged."*
   Covers both the read-only-sandbox mode and the duplicate-key mode.

7. **New criterion — materialization is refreshed, not just idempotent.**
   *"After changing the workspace's configured context content and re-running
   `niwa apply`, the new content is present in the Codex-facing context and the
   previous content is absent."* Without it R3's "current" is unverifiable and
   stale context is undetectable.

8. **AC10: extend to worktrees.** *"...in every cloned repository and every
   niwa-managed worktree of a prepared instance."* Worktrees take a different
   git plumbing path (`.git` as a file, exclude resolution through the common
   dir) and get the same payload.

9. **AC4: distinguish the outer layers from the worktree's own file.** Assert an
   instance-level sentinel that appears *only* in instance-level content is
   present in what a worktree session receives, so a collapse to cwd-only
   discovery fails the test. This depends on an unverified assumption — that a
   worktree's `.git` *file* satisfies Codex's project-root marker check — which
   should be measured before the criterion is finalized, since if it does not
   hold the design needs a worktree-specific answer.

10. **AC11: make both halves decidable.** Writes: credential and login files are
    byte-identical *and* mtime-unchanged after `niwa create` and `niwa apply`.
    Reads: with the credential file at mode `000` in the sandboxed home, both
    commands still exit 0.

11. **AC9: add the offline half.** *"After `niwa apply`, niwa has written no
    hook definitions and no hook-state entries anywhere."* The Decisions section
    makes hook-absence load-bearing for R10; nothing currently enforces it.

12. **New criterion — R14's launch-time clause.** *"In a workspace declaring the
    per-workspace agent setting as codex, a niwa-launched session still selects
    Codex and `niwa dispatch` still refuses."* One clause of R14 is otherwise
    undecided, and it is what keeps an Out of Scope promise coherent.

13. **AC3: name what is scrubbed.** *"...from a shell with no `NIWA_*` variables
    and no `CODEX_HOME` set."* And give the fixture a candidate context file in
    an intermediate directory so the nesting depth actually exercises
    per-directory first-match.

14. **New criterion — the empty-slot trap.** *"In a workspace with no configured
    context content, a repository shipping its own committed context file still
    delivers that file's content to a session: niwa writes no empty or
    whitespace-only context file that would claim the directory's single
    slot."* This is what makes R6 verifiable in the case where violating it
    causes total loss rather than partial.

---

## Fixture feasibility

Mostly constructible with what exists. What needs new support:

- **A source repo with committed files.** `aSourceRepoExists` creates an empty
  bare repo. AC5 (repo ships its own context file) and AC3 (intermediate-
  directory candidate) both need one. Suggest a `localGitServer` helper along
  the lines of `RepoWithFiles(name, map[string]string)`; it is small and unlocks
  both.
- **Deeply nested working directories** — trivial with the file-writing steps
  that exist.
- **Worktrees** — fully covered; `worktree.feature`, `session_attach.feature`,
  and the `the file "X" in the last worktree contains "Y"` step already exist.
- **Oversized context content for AC6** — needs a way to generate >32KB of
  content; either a generated source file in the config repo fixture or a step
  that pads an existing content file. Straightforward but not currently
  available.
- **A pre-seeded developer Codex config** (required change 5) — needs a step to
  write into the sandboxed `$HOME` before `niwa create`. The sandbox already
  isolates `HOME` and `XDG_CONFIG_HOME`, so this is a small addition.
- **Byte-identity comparison of a delivered tree against its source**
  (required change 4) — needs a tree-diff step; nothing comparable exists today.
- **`codex` availability gate** — mirror `claudeIsAvailable`; skip rather than
  fail so `@critical` CI stays offline.

---

## Optional improvements

- AC1 and AC13 assert nearly the same thing. Not a testability defect, but if
  the completeness reviewer keeps both, the second could carry the harder case
  (a workspace whose config predates the agent setting entirely) to earn its
  place.
- The criteria never state which are `@critical`. Given the CI-gating
  convention, the exclude-pattern criterion (AC10) and the budget criterion
  (AC6, offline half) are the two that most deserve it: both are cheap, both are
  offline, and both guard silent modes.
- Consider stating explicitly that live-session criteria skip rather than fail
  when `codex` is absent. It is the established repo pattern, and leaving it
  implicit invites either a red CI or a quietly deleted scenario.

---

# Round 2

# Verdict: FAIL

Thirteen of my fourteen required changes landed, and landed with the property
intact rather than just the wording. The revision is materially stronger than
round one: the preamble's two conventions — that "sees"/"receives" is decided
offline against the single file first-match selects, and that live checks gate
and skip rather than fail — do in one paragraph what I had asked for criterion
by criterion, and they do it better, because they bind future criteria too.

One change landed in a form that no longer reaches the state it was written to
forbid. That is the single required change below. Everything else in this
section is verification or a non-blocking note.

## Verification of round-one required changes

| # | Change | Landed as | Verdict |
|---|---|---|---|
| 1 | AC6: pin the truncation threshold | AC7 (32768 named, "shared across the whole chain and consumed outermost-first", offline budget-covers-chain assertion + live gated check) | **Landed.** Fails correctly in both broken shapes: no declared budget, or a declared budget left at the default while the chain exceeds it. |
| 2 | AC2: close the exemption, name the comparison set | AC2 (comparison set named; modified tests closed to `codex-agent.feature`, `content_test.go`, `root_materializer_test.go`) | **Landed.** Verified on disk — see below. |
| 3 | AC5: make ordering observable | AC5 ("the single context file Codex selects for that directory", "no higher-precedence candidate shadows it") | **Landed.** The ordering failure is now the thing being asserted, not a side effect. |
| 4 | AC7: whole-tree assertion for skills | AC8 (byte-identical, no file added or omitted, plugin manifest at the delivered root, every `references/` and `scripts/` the source has) | **Landed.** The orphaned-references shape now fails. "Plugin manifest" instead of the filename is correct de-mechanising for PRD altitude and loses nothing. |
| 5 | New: developer's config changed only additively | AC14, plus R13's new "scoped, additive entries... are consistent" sentence | **Landed in full**, including the global-key clause and the outside-the-instance consequence. This was the largest hole in round one and it is now the most precisely worded criterion in the document. |
| 6 | New: trust entries correctly keyed, no accumulation | AC10 ("a present-but-miskeyed entry fails", three-applies count) | **Landed.** The present-but-wrong mode is named explicitly. |
| 7 | New: refresh, not just idempotence | AC19, plus R3's new "'Current' includes refresh" sentence | **Landed**, including the `niwa worktree apply` half I raised separately. |
| 8 | AC10: extend git-status to worktrees | AC12 ("every cloned repository and every niwa-managed worktree"), now also citing R12 | **Landed.** |
| 9 | AC4: distinguish outer layers from the worktree's own file | AC4 ("a sentinel that appears only in the instance-level content... so a collapse to current-directory-only discovery fails the check") | **Landed, unsoftened**, as instructed. See the note below on what the open measurement does and does not touch. |
| 10 | AC11: make both credential halves decidable | AC13 (byte-identical **and** mtime-unchanged; mode `000` and both commands still exit zero) | **Landed exactly.** |
| 11 | AC9: add the offline half for hook absence | AC11 ("niwa has written no hook definitions and no hook-state entries anywhere") | **Landed.** The Out of Scope hook exclusion is now enforced rather than assumed. |
| 12 | New: R14's launch-time clause | AC16 (launch-time selection **and** the dispatch refusal) | **Landed.** |
| 13 | AC3: name what is scrubbed; make the depth load-bearing | AC3 (`NIWA_*` and `CODEX_HOME` named; committed context file in an intermediate directory) | **Landed, and it discriminates correctly.** A current-directory-only implementation delivers nothing; a repo-root-only implementation delivers the outer layers but misses the intermediate file. Both fail. |
| 14 | New: the empty-slot trap | AC6, plus R6's new degenerate-case sentence | **Weakened — see Required change.** |

### AC2's named test set, verified on disk

The author's deviation is correct and my round-one naming was incomplete.
`internal/workspace/root_materializer_test.go:153-164` is a table-driven test
whose cases carry a "the other agent's file is absent" column
(`{"codex", AgentCodex, "AGENTS.md", "CLAUDE.md"}` and its inverse) — a genuine
exclusivity assertion that must invert, and it is not in `content_test.go`.
`content_test.go:183` carries the separate `assertNotExist(..., "AGENTS.md")`
form. Both belong in the named set.

Worth recording that the set is also correctly *exclusive*:
`internal/agent/agent_test.go` asserts only that the name accessor maps
`AgentCodex` to `AGENTS.md`. Per the exploration's finding that the exclusivity
lives in the callers rather than the accessors, that test stays valid unmodified
and correctly sits outside the exemption. The closed list is right in both
directions, which is what makes it usable as a contract.

## The core test, re-run on the new and rewritten criteria

Every criterion added or rewritten in this pass implies a test that fails in its
broken case, with the one exception below.

Two are worth calling out as better than what I asked for. AC7's offline half —
the declared budget covers the composed chain's byte size — now verifies R7's
*broadened* claim ("every context layer present for a location SHALL reach the
session whole"), not just the innermost-layer clause I had scoped it to, because
a budget covering the whole chain truncates no layer at all. The rewritten
requirement and the criterion moved together, which is the thing that usually
goes wrong in a nineteen-change pass and did not here. And AC10 plus AC14
together now make R13 enforceable across all three of its clauses, where round
one had a detector for only the credential clause.

## Required change

**AC6's fixture cannot produce the file it forbids.** I asked for "a workspace
with no configured context content"; the criterion narrowed it to "a workspace
that configures no **repository-level** context content."

Under the composition this feature requires, the per-repository context file
carries the instance and group layers as well — a session standing in a repo
reaches nothing above the repo root, so the outer layers have to be inlined
there. With instance or group content configured, that file is non-empty
regardless of whether repository-level content exists, so the narrowed fixture
never constructs an empty or whitespace-only file, and the criterion's own
assertion is checked against a file that was never at risk.

The criterion is not inert — it still catches one broken shape, an
implementation that writes the repo-root file from repository-level content
alone and forgets to inline the outer layers. But it stops catching the shape it
was written for: a correctly composing implementation, a workspace with nothing
to say at any layer, an unconditional write, an empty file claiming the
directory's single slot, and the repository's own committed context suppressed
entirely. That is total loss, silent, and it is the case R6's newly added
sentence now explicitly requires ("when the workspace has nothing to say at a
layer, the repository's own content still reaches the session undiminished").

It also is not an exotic fixture. A workspace declaring only
`[workspace] name = "ws"` with no content tables at all is the most common shape
in this repo's own functional suite — `git-invisibility.feature`'s Background is
exactly that. The uncovered case is the default one.

Suggested wording:

> In a workspace that configures no context content at any layer, a repository
> shipping its own committed context file still delivers that file's content to
> a session: niwa writes no empty or whitespace-only file that would claim the
> directory's single context slot and suppress the repository's own (R6).

## Non-blocking notes

Neither of these asks for a change; both are recorded because they arose from
edits made in this pass.

**AC11's gate is narrower than R10's carve-out.** R10 was rewritten to place
prompts belonging to the developer's own Codex setup — a first-run login —
outside the requirement. AC11 still reads "shows no trust, review, or approval
prompt" and gates only on `codex` being on PATH. In a sandboxed home with no
login, a login prompt would fail a criterion that R10 says is not about it. The
existing `claudeIsAvailable` helper gates on the binary *and* the credential,
which is the precedent that resolves it. I am not asking for a change because
this direction of error is loud, not silent: it produces a red or flaky
scenario, never a false pass.

**AC10 and AC18 overlap on the three-applies count** for per-project entries.
Benign — both are decidable, both fail in the same broken case, and AC18 covers
niwa-managed blocks that AC10 does not. No action.

## On the open worktree measurement

AC4 is written unsoftened, as instructed, and I am not weakening it. One
observation for the spike's readers rather than for the criteria: because the
preamble decides "sees"/"receives" offline against the file first-match selects,
AC4's offline half necessarily encodes an answer to the question the spike is
measuring — a test has to know which file Codex would pick in a worktree in
order to assert against it. If the measurement returns that a worktree's `.git`
*file* does not satisfy the project-root marker check, AC4 does not become a
worse criterion; it becomes a criterion the design cannot satisfy without a
worktree-specific answer, which is the design-altitude consequence the lead
already named. Nothing to change here either way.

---

# Round 3

# Verdict: PASS

AC6 now reads "in a workspace that configures no context content at any layer",
which puts the empty-or-whitespace-only file back within reach of the fixture
and restores the criterion's ability to fail in the total-loss case — the
correctly-composing implementation that writes the file unconditionally, claims
the directory's single slot, and silently suppresses the repository's own
committed context. That was my only outstanding round-two finding, and it is
closed. The other thirteen required changes were verified in round two and are
not re-examined here. The nineteen acceptance criteria are testable as written,
each decidable by a stated procedure, and each would fail in the case it exists
to prevent.

Carried forward, unchanged and non-blocking: AC11's live gate should check for
an authenticated Codex session rather than only `codex` on PATH, since R10 now
places the developer's own first-run login outside the requirement; and AC4's
offline procedure inherits the worktree project-root-marker assumption the
separate spike is measuring, which is a design-altitude consequence rather than
a criteria defect either way.
