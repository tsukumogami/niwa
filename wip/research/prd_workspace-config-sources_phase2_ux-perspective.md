# Phase 2 Research: UX Perspective

## Lead A: Slug delimiter

### Comparison

Two forms in play, evaluated as the developer types them:

| Dimension | `:subpath@ref` (Candidate A) | `//subpath?ref=` (Candidate B) |
|-----------|------------------------------|--------------------------------|
| Whole repo | `tsukumogami/vision` (identical) | `tsukumogami/vision` (identical) |
| Subpath only | `tsukumogami/vision:.niwa` | `tsukumogami/vision//.niwa` |
| Subpath + ref | `tsukumogami/vision:.niwa@v1` | `tsukumogami/vision//.niwa?ref=v1` |
| Characters typed (subpath+ref) | 32 | 38 |
| Shell quoting needed (bash/zsh)? | No. `:` and `@` are not glob/expansion metachars. | Yes. `?` is a glob in interactive bash unless `set -o noglob`; many users hit "no matches found" in zsh. `&` would force backgrounding but is absent from this single-param form. |
| TOML safety (unquoted)? | Safe. No reserved TOML char. | `?` is not reserved in TOML strings, but the long form invites copy/paste from URLs that include `&`, `#` (comment), or special chars. |
| Copy/paste from a browser URL bar? | Doesn't roundtrip; user must rewrite. | Looks URL-shaped but isn't a real URL (`//.niwa` is not standard URL syntax outside Terraform/go-getter). Same rewrite cost. |
| Ecosystem precedent | Renovate (`org/repo:preset`), Docker (`image:tag`), git itself (`path@ref` in some commands) | Terraform module sources, go-getter, Renovate file presets (`//path/to/file.json`) |
| Reads as "inside the previous thing"? | `:` is ambiguous (could be a key, a port, a label). Reading `vision:.niwa` requires knowing the convention. | `//` is visually heavier and more obviously a "descend into" marker. But it can also read as "URL scheme separator" by mistake. |
| Niwa idiom fit | Matches the existing CLI: `niwa init --from owner/repo`. The slug stays slug-shaped — short, no URL-isms. | Forces users to think in URL terms. The CLI today never asks for a URL-shaped argument; this would be the first. |
| Mental-model load | Low. Two punctuation marks, two roles: `:` for "where in the repo", `@` for "which version". Mirrors how users already write `image:tag`. | Higher. Three syntactic moves: `//` for descent, `?` for query mode, `=` for key/value pairs. |
| Future ambiguity risk | `:` collides with SSH URL form `git@github.com:org/repo.git` — but those URLs go in a different field (`source_url` is a slug, not a URL). | None known, but URL look-alikes invite users to paste real URLs that niwa rejects. |
| Cross-tool coherence with niwa | Low conflict — niwa already accepts `org/repo` shorthand and full URLs as disjoint syntactic worlds. | Forces a half-URL syntax that's neither a slug nor a real URL. |

### Recommendation

**Adopt `:subpath@ref` (Candidate A).**

The decisive factor is niwa's existing surface. Today every user types `niwa init --from owner/repo` — a slug, not a URL. Extending that slug to `owner/repo:subpath@ref` is one mental step (two new punctuation marks). Switching to `owner/repo//subpath?ref=v1` is a different mental model: "now I'm typing a URL-ish thing." That's a heavier ask for a feature whose dominant case (whole-repo, default branch) doesn't even need the new syntax.

Shell safety also favors `:`. The `?` glob trap in zsh is real and embarrassing — `niwa init --from foo/bar//baz?ref=v1` produces "no matches found" until the user remembers to quote. The `:`/`@` form doesn't have that hazard.

The Renovate precedent is closer to niwa's mental model than the Terraform precedent. Renovate users *already* type `github>org/repo:preset-name`; the same audience will read `tsukumogami/vision:.niwa@main` correctly on first encounter. Terraform users typing `git::https://...//subdir?ref=...` are operating in a different headspace (full URLs, `git::` prefixes) that niwa never asks for elsewhere.

The one real risk — `:` colliding with SSH URLs — doesn't materialize in practice. SSH URLs go through a separate code path (`Cloner.ResolveCloneURL` recognizes `git@host:` as a full URL). Slugs and URLs occupy disjoint syntactic niches inside `source_url`, and the slug parser can require host detection via the `.` heuristic that the explore phase already validated.

### Implications for Requirements

- The PRD must commit to `[host/]owner/repo[:subpath][@ref]` as the v1 slug grammar.
- The PRD must specify that the parser rejects malformed forms early (e.g., `@ref` before `:subpath`, multiple `:` separators, multiple `@` separators) with a clear error, so users don't silently get the wrong subpath.
- Documentation and `niwa init --help` examples should always show the slug form, not URL forms, as the recommended input.
- The `niwa status` detail view should print the slug back in canonical `owner/repo:subpath@ref` form so users can copy/paste between machines.

---

## Lead B: Migration cutover ergonomics

### Option journeys

**Today's precedent (from `reset.go:84-87`):** when a destructive operation cannot proceed safely, niwa errors out with a message naming the obstacle and the remediation. There are no interactive yes/no prompts in niwa today — every "destructive" command (`destroy`, `reset`) gates behind a `--force` flag, prints the obstacle to stderr, and exits non-zero. Users either fix the obstacle (commit the dirty changes) or re-run with `--force`.

That sets the tone: niwa is non-interactive and refuses dangerous operations until the user opts in. Interactive prompts would break the existing pattern.

**Option (a): URL-change detection in `niwa apply`.**

Developer journey:
1. Maintainer pushes `.niwa/` into `tsukumogami/vision`.
2. Developer edits `~/.config/niwa/config.toml` (or runs `niwa config set global tsukumogami/vision`) to point at the brain repo.
3. Developer runs `niwa apply` from the workspace.
4. niwa loads the registry, sees `source_url` changed from `tsukumogami/dot-niwa` to `tsukumogami/vision`, and inspects `<workspace>/.niwa/`. Today it's a working tree with `.git/`. Tomorrow it should be a snapshot.
5. niwa prints:
   ```
   error: workspace config source changed
     was:  tsukumogami/dot-niwa
     now:  tsukumogami/vision (subpath: .niwa)
     The current .niwa/ on disk is a working tree from the old source.
     Replacing it will discard any uncommitted edits.
   To proceed:
     1. cd .niwa && git status   # check for uncommitted work
     2. niwa apply --force        # discard .niwa/ and re-materialize as a snapshot
   ```
6. Developer inspects, then runs `niwa apply --force`.
7. niwa atomically swaps `<workspace>/.niwa/` for the new snapshot and continues.

What the developer does: one extra command (`apply --force`) plus the inspection step. The error message is the prompt; no interactive UI needed.

**Option (b): dedicated `niwa registry migrate` command.**

Developer journey:
1. Maintainer pushes `.niwa/` into `tsukumogami/vision`.
2. Developer reads release notes, learns about `niwa registry migrate`.
3. Developer runs `niwa registry migrate --to tsukumogami/vision:.niwa`.
4. niwa detects the old source, the new source, and the working tree:
   ```
   Migration plan:
     workspace: tsukumogami
     old source: tsukumogami/dot-niwa (working tree at .niwa/)
     new source: tsukumogami/vision:.niwa (snapshot)
     uncommitted changes in old config: 0 files
   This will:
     - rewrite the registry entry
     - replace .niwa/ with a snapshot from the new source
   Proceed? [y/N]
   ```
5. Developer types `y`.
6. niwa does the swap, prints confirmation.
7. Next `niwa apply` Just Works.

What the developer does: discover a new command, run it, confirm interactively. Three new things to learn (the command exists, the flag, the confirmation pattern).

**Comparison:**

- Option (a) extends a pattern users already know (`--force` to override safety gates). Zero new commands, zero new UI patterns. The error message is the migration guide.
- Option (b) introduces interactive prompts for the first time in niwa, plus a new subcommand. It's more "guided" but also more discoverable-only-if-you-read-the-release-notes. Users who edit the registry directly (the natural workflow today) won't know to run it.
- Option (a)'s message text can be tuned to be just as informative as (b)'s prompt — the only thing (b) buys is interactivity, which contradicts niwa's existing tone.
- Option (b) adds maintenance: a new command surface, new tests, new help text, new failure modes (what if the user runs `migrate` *without* having edited the registry?).

### Recommendation

**Adopt option (a).** Detect the URL change in `niwa apply`, refuse to proceed without `--force`, print a remediation hint, and auto-handle the working-tree → snapshot conversion when `--force` is passed.

Rationale:
- Matches the existing destructive-operation pattern (`destroy --force`, `reset --force`).
- Zero new commands, zero new UI primitives.
- The error message itself is the migration guide — users who edit the registry by hand discover the path naturally on their next `apply`.
- Snapshots eliminate the silent-edit-loss concern *after* the cutover, so the only moment that needs careful UX is the cutover itself, and option (a) handles it inline.

The PRD should also commit to a one-line "what to inspect" hint in the error message (e.g., "cd .niwa && git status") so users know exactly which command to run before passing `--force`. This avoids the trap where `--force` exists but the user has no easy way to check what they're about to lose.

### Implications for Requirements

- `niwa apply` must detect a registry URL/subpath change before invoking the materializer.
- The detection compares the new `(host, owner, repo, subpath, ref)` tuple against the existing on-disk `.niwa/` (via the new provenance marker if present, or by detecting `.git/` presence as the legacy proxy).
- When a change is detected and `.niwa/` is the legacy working-tree form, `niwa apply` errors with a structured message naming both URLs and the inspection command, then refuses to proceed without `--force`.
- When `--force` is passed (or `.niwa/` is already a snapshot), niwa does the atomic snapshot swap.
- No new subcommand. No interactive prompts.
- An acceptance criterion: `niwa apply` after a registry URL change produces a non-zero exit, prints the structured message, and leaves `.niwa/` untouched.

---

## Lead C: Default-branch ref resolution timing

### Comparison

| Dimension | Pin at init time | Re-resolve every apply (today's behavior) |
|-----------|------------------|---------------------------------------------|
| Stability of registry view | High. `niwa status` shows a fixed ref. | Low. `niwa status` shows whatever default branch resolves to right now. |
| Behavior when remote renames default | User keeps tracking the old branch (which probably still exists or was deleted). Either silent staleness or hard failure on next fetch. | If the local branch was set at clone time, today's `git pull --ff-only origin` follows the local branch — so a remote rename causes silent staleness too, until the local branch is forcibly re-set. Niwa has no logic to detect or handle this. |
| Reproducibility across machines | High. Two developers run `niwa init`; both record `main`; both see the same workspace tomorrow if `main` exists. Without snapshot commits in state, this is moot. With state.config_source.resolved_commit, even more reproducible. | Lower. Two developers initing on different days at different times can get different commits without realizing. |
| User mental model | "I pinned `main`. If maintainers rename it, I have to opt in to the new ref." Matches Cargo, Nix lock semantics. | "I pointed niwa at `org/repo`. It tracks whatever the default is." Matches casual `git clone` behavior. |
| Discoverability of opt-in to HEAD-tracking | Awkward. User must know to type `niwa init owner/repo` and *then* edit the registry to remove the pin, or pass a flag. | Default. The common case is the implicit case. |
| First-use surprise for new users | A user who types `niwa init owner/repo`, sees `Source: owner/repo @ main` in status, and assumes "main" is metadata is correct. But a user who later expects "track the latest" is surprised. | A user who types `niwa init owner/repo`, sees `Source: owner/repo` in status, and assumes "track the default" is correct. Aligns with `git clone` mental model. |
| Behavior when user is offline | Init must hit the network to resolve the default. | Init can be offline (no resolution); apply hits the network. |
| Failure mode if remote becomes unreachable mid-life | Apply uses the pinned ref (still requires fetch). | Apply tries to fetch latest; same failure mode. |

**Status output sketch — pin at init time:**

```
Source: tsukumogami/vision @ main
        commit: 9f8e7d6c (fetched 2h ago)
```

The `main` here is what was resolved at `init`. Stable across days.

**Status output sketch — re-resolve every apply:**

```
Source: tsukumogami/vision (default branch)
        commit: 9f8e7d6c (fetched 2h ago)
```

The "default branch" label is honest — niwa doesn't know which branch will be checked next time. The commit is what was fetched on the last apply.

**Subtlety: most users today don't type `@ref`.** It doesn't exist as a feature yet. Adding refs without a clear default makes them feel "advanced." The design needs a sensible no-`@ref` behavior that doesn't make users think about ref resolution at all.

**Subtlety: today's `git pull --ff-only origin` (from `configsync.go:42`) does NOT follow remote default-branch renames.** It pulls into the local branch (set at clone time). So today's behavior is "implicit pin to whatever the default was at clone time, with silent staleness if the remote renames." That's worse than either explicit option, because the user thinks they're tracking HEAD when they're really tracking a stale branch reference.

### Recommendation

**Re-resolve every apply, but record the resolved commit oid in state — and surface the resolution explicitly in `niwa status`.**

Rationale:
- Matches the dominant user mental model ("I pointed at the repo, niwa keeps it current").
- Matches `git clone` semantics, which most niwa users have internalized.
- Doesn't require users to learn refs as a first-class concept — refs become an opt-in advanced feature for users who pin to tags or commits.
- The explore phase identified that today's behavior already has a silent-staleness problem (local branch tracks the original default name). The fix isn't pinning at init time — it's *re-resolving* the default branch every apply, which the new tarball-based fetcher does naturally (the GitHub API's tarball endpoint resolves `HEAD` server-side).
- Recording `resolved_commit` in `state.config_source` gives reproducibility-conscious users a hook (they can inspect the commit, share it, etc.) without forcing the rest of the population to think about refs.
- `niwa status` should explicitly say "(default branch)" when no ref was specified, so users see the moving-target nature without being surprised by it.

The opt-in path for "I want a stable pin" is `niwa init owner/repo@v1.0` — explicit, discoverable through `--help`, and meaningful to users who already think in terms of release tags.

### Implications for Requirements

- The PRD must specify that ref-less slugs resolve the default branch on every `niwa apply` (not at `init`).
- The PRD must specify that the resolved commit oid is recorded in `state.config_source.resolved_commit` after each apply.
- `niwa status` detail view must distinguish "pinned ref" from "default branch (auto-resolved)" in its output.
- The PRD must commit to a behavior when the remote default branch is renamed: niwa re-resolves at each apply, so the rename is followed transparently, with the new commit recorded in state. No special handling needed.
- The PRD should note that explicit refs (`@v1.0`, `@some-branch`, `@<sha>`) skip default-branch resolution and use the literal ref.
- An acceptance criterion: `niwa apply` against a slug with no ref records the latest commit on the remote default branch in `instance.json`, and `niwa status` shows that commit alongside an explicit "(default branch)" annotation.

---

## Lead D: vault_scope = "@source" shorthand

### Recommendation

**Defer to a v1.1 follow-up.** Do not ship the `@source` shorthand in v1.

Justification:
- The current `vault_scope` resolution code (`internal/config/config.go:128-132`, `internal/workspace/override.go:266-295`) treats `vault_scope` as an opaque string label. Adding `@source` expansion means the resolver must know about source identity at scope-resolution time, threading the parsed source tuple through the override merger and the personal overlay matcher. Not free, not large, but real.
- The use case (one brain repo, many subpath workspaces, all sharing one personal-overlay scope) is real but has a workable manual answer today: the workspace author writes `vault_scope = "vision"` (or whatever stable label) explicitly in each workspace.toml. That's one line per subpath, copy-paste obvious.
- The risk of shipping `@source` early is binding niwa to a particular expansion scheme (`owner-repo`? `owner-repo-subpath`? `host-owner-repo`?) that turns out wrong once real monorepo-of-teams setups exist. Picking the wrong scheme creates a back-compat tail.
- The risk of *not* shipping `@source` early is small: workspace authors write one extra line, and the explicit form is self-documenting (a future reader knows exactly which scope the personal overlay needs to define).
- Subpath sourcing itself is the v1 ergonomic win. Layering `@source` on top inflates the v1 surface without proportional benefit.

**When to revisit:** After v1 ships and three or more real monorepo-of-teams setups exist in the wild, examine the explicit `vault_scope` strings they chose. If a clear pattern emerges (e.g., everyone uses `<owner>-<repo>`), ship `@source` as exactly that pattern. If patterns diverge, the manual form was the right answer all along.

The PRD should explicitly mark `@source` as out-of-scope-for-v1 with a note that it's a candidate for follow-up if usage data supports it.

---

## Lead E: User-story narratives

### Story 1: First-time subpath adoption

A developer working at an org with a brain repo (`tsukumogami/vision`) wants to create a new workspace. They run `niwa init --from tsukumogami/vision:.niwa my-workspace`. niwa resolves the slug, fetches the `.niwa/` subpath from the brain repo's default branch as a snapshot, materializes it at `<cwd>/my-workspace/.niwa/` (a pure file tree, no `.git/`), and registers `my-workspace` in `~/.config/niwa/config.toml` with the parsed source tuple. They run `niwa create` and `niwa apply`, which clones the workspace's repos and weaves the brain repo's CLAUDE.md content into the workspace overlay. The `.niwa/` directory feels like a downloaded artifact, not a checkout — the developer never thinks to edit it in place, because there's no `.git/` to suggest they could.

### Story 2: Migrating from standalone dot-niwa

A developer has an existing workspace pointing at `tsukumogami/dot-niwa`. The maintainer announces that the config has moved into the brain repo at `tsukumogami/vision:.niwa`. The developer runs `niwa config set global tsukumogami/vision` (convention discovery resolves the subpath to `.niwa` automatically) and then `niwa apply`. niwa detects the source URL changed, prints "workspace config source changed: was tsukumogami/dot-niwa, now tsukumogami/vision (subpath: .niwa). The current .niwa/ is a working tree from the old source. To proceed: 1. cd .niwa && git status to check for uncommitted work, 2. niwa apply --force to discard and re-materialize as a snapshot." The developer inspects, sees no pending edits, runs `niwa apply --force`, and niwa atomically swaps `.niwa/` for the new snapshot. From this point on, edits to `.niwa/` are never silently lost because there's no working tree to edit into.

### Story 3: Brain-repo maintainer publishing

A maintainer of `tsukumogami/vision` decides to host the workspace config inside the brain repo instead of a standalone `dot-niwa` repo. They `git mv` the dot-niwa contents into `vision/.niwa/`, drop the standalone repo's `README.md` and `LICENSE` (or fold useful bits into vision's existing equivalents), commit, and push. They post a one-line announcement: "the workspace config now lives at tsukumogami/vision:.niwa — run `niwa config set global tsukumogami/vision` to switch." Consumers who follow the instructions hit the migration flow from Story 2; consumers who don't see their existing workspaces continue working against the old repo until they switch (the standalone dot-niwa is left in place for graceful overlap). The maintainer never has to coordinate a synchronized cutover, because each consumer's switch is independent.

### Story 4: Apply after brain-repo force-push

A developer's workspace points at `tsukumogami/vision:.niwa`. The brain-repo maintainer force-pushes the default branch to clean up history. The developer runs `niwa apply`. niwa fetches a fresh tarball from the GitHub API for the (now-rewritten) default branch, computes the new resolved commit, and compares it to the previous `state.config_source.resolved_commit`. The commit oid differs, so niwa atomically swaps `<workspace>/.niwa/` for the new snapshot — no merge conflicts, no fast-forward errors, no manual reconciliation. `niwa status` afterward shows the new commit and the latest fetched-at timestamp; the developer didn't have to do anything, and issue #72 is invisible to them.

---

## Open Questions

1. **Confirmation prompt for `--force` on URL change.** Option (a) in Lead B keeps niwa non-interactive. Should the PRD allow an *optional* `--yes` flag for users who want to script the migration without `--force`-implying-discard semantics? (My read: no — `--force` is the existing pattern and adding a second flag splits the surface. But worth flagging.)

2. **Status output format for a freshly-pinned ref vs default branch.** The detail view sketch in Lead C distinguishes them, but the summary table view (`niwa status` without flags) has no source column today. Should the summary view gain one, or stay narrow? (My lean: stay narrow; surface source identity only in detail view.)

3. **Behavior when a slug has no ref AND the default branch can't be resolved (network down, no cached oid).** The PRD must commit to whether `niwa apply` errors out or proceeds with the previously-resolved commit. (My lean: proceed with cached commit, print a stale-data warning. But this needs a deliberate decision.)

4. **Slug parser strictness.** Lead A recommends rejecting `@ref` before `:subpath` and other malformed orderings. The exact list of rejected forms needs PRD-level enumeration so it becomes testable.

5. **Migration of existing registry entries.** When a user upgrades to the new niwa, their existing `source_url = "tsukumogami/dot-niwa"` entries continue to mean "whole repo, default branch" — but should niwa proactively populate the parsed mirror fields (`source_host`, etc.) on next read, or wait for the user to re-init? Lazy population on next save is the cleanest answer; the PRD should state this explicitly.

---

## Summary

**Recommend `:subpath@ref` slug delimiter (Candidate A), URL-change detection in `niwa apply` with `--force` opt-in (Option a), re-resolve default branch every apply with explicit "(default branch)" annotation in status, and defer `vault_scope = "@source"` to v1.1.** The biggest trade-off is in Lead C: pinning at init time would be more reproducible across machines but contradicts the dominant `git clone` mental model and adds friction to the most common case (no explicit ref). The biggest open question is Lead C's offline-network-down behavior — niwa needs a deliberate fallback policy when the default branch can't be re-resolved, and the PRD should pin it down rather than leave it to design.
