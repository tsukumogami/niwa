# Lead: Does the interactive Codex TUI start clean in a niwa-prepared directory?

Everything below was exercised against codex-cli 0.147.0 at
`/home/dgazineu/.tsuku/tools/current/codex`, driven through a pty with
`pyte` screen emulation. Harness and scenario scripts:

- `/home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/tui.py` (copy of the sibling spike's `tui3.py`)
- `/home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/setup.sh`
- `/home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/hooks_setup.py`

The synthetic tree mirrors the chosen architecture exactly: an instance root
`inst/` holding the `.codex/` payload, a git repo at `inst/public/repo` whose
`.codex` is a relative symlink to `../../.codex`, and a deep subdirectory
`inst/public/repo/a/b/c`. Each scenario used its own isolated `CODEX_HOME`
under the scratch tree. The host's real `~/.codex` was never written to
(`config.toml` mtime unchanged at 20:59, well before the 23:14 experiments);
no `codex login`, no token refresh, no credential printed.

Authentication was avoided the way the sibling spikes did it — a `fake` model
provider pointing at `http://127.0.0.1:9/v1` with `env_key = "OPENAI_API_KEY"`.
Because the provider is custom, `requires_openai_auth` is false and
`should_show_login_screen` returns early
(`codex-rs/tui/src/lib.rs:1979-1987`), so nothing but the trust and hook gates
can interrupt startup. Every TUI was SIGKILLed by the harness at a fixed
deadline; `pgrep` afterwards showed no surviving codex process.

## Findings

### 1. Baseline: no `projects.*` entry — the TUI blocks on a trust prompt

**Verified by experiment.** With `CODEX_HOME` containing only the fake-provider
config and no `[projects.…]` table, starting `codex` in the repo root never
reaches a composer. The captured first screen is the ASCII Codex logo followed
by:

```
  Welcome to Codex, OpenAI's command-line coding agent
> You are in /home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/inst/public/repo
  Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of
  prompt injection. Trusting the directory allows project-local config, hooks, and exec policies to load.
› 1. Yes, continue
  2. No, quit
  Press enter to continue
```

It sits there indefinitely — the 18-second capture window expired with that
screen still up. This is the onboarding trust step
(`codex-rs/tui/src/onboarding/trust_directory.rs`), selected by
`should_show_trust_screen`, which is one line
(`codex-rs/tui/src/lib.rs:1963-1965`):

```rust
fn should_show_trust_screen(config: &Config) -> bool {
    config.active_project.trust_level.is_none()
}
```

Note the prompt's own wording — "Trusting the directory allows project-local
config, hooks, and exec policies to load" — which is the user-facing statement
of the same coupling the sibling spikes found in the loader.

I re-ran this scenario after adding `hooks.json` to the payload and got the
identical trust screen, so the trust gate is what a developer hits first. I did
not drive past it to see whether the hook review screen then follows.

### 2. The niwa configuration: `trust_level = "trusted"` on the repo root — clean start, no question

**Verified by experiment. This is the decisive result: the goal is met.** With
a single added stanza in the user config —

```toml
[projects."/home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/inst/public/repo"]
trust_level = "trusted"
```

— plain `codex` in the repo root lands directly on a live composer. Nothing is
asked. The captured screen:

```
╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.147.0)                           │
│                                                      │
│ model:     gpt-5-PROJECT-LAYER   /model to change    │
│ directory: ~/.claude/jobs/7838923c/tmp/…/public/repo │
╰──────────────────────────────────────────────────────╯
  Tip: Our most capable model yet. GPT-5.6 Sol can tackle complex code changes, dig into research, produce
  polished documents, and take on your most ambitious work. Sol is highly capable at lower reasoning efforts—
  try starting lower, then turn it up for harder jobs.
› Use /skills to list available skills
  gpt-5-PROJECT-LAYER default · ~/.claude/jobs/7838923c/tmp/tuispike/inst/public/repo
```

Two things in that header are worth reading carefully. `model:
gpt-5-PROJECT-LAYER` is the value written only in `inst/.codex/config.toml` —
so the project config layer resolved through the `.codex` symlink and won over
the user config, in the interactive path, with no prompt. And the placeholder
line reads "Use /skills to list available skills", meaning the skills subsystem
came up.

The session is genuinely usable, not merely painted. Typing `/skills` into the
composer produced the live slash popup:

```
› /skills
  /skills  use skills to improve how Codex performs specific tasks
```

and submitting a real turn (`hi`) drove the model call all the way to the dead
provider:

```
■ stream disconnected before completion: error sending request for url (http://127.0.0.1:9/v1/responses)
```

That failure is the intended one — it proves everything up to and including the
outbound request happened without a gate.

### 3. Hooks: a correct hash is silent; a stale hash is a hard modal, not a silent skip

**Verified by experiment, with one correction to the assumed key format.**

I added a `SessionStart` hook to `inst/.codex/hooks.json` and wrote a
`[hooks.state]` entry keyed on the *instance* path
(`inst/.codex/hooks.json:session_start:0:0`) with a hash computed by the
sibling spike's `codexhash.py`. That did **not** satisfy the gate — the TUI
stopped on:

```
  Hooks need review
  1 hook is new or changed.
  Hooks can run outside the sandbox after you trust them.
› 1. Review hooks
  2. Trust all and continue
  3. Continue without trusting (hooks won't run)
  Press enter to confirm or esc to go back
```

To get ground truth rather than guess, I ran a throwaway `CODEX_HOME` with no
`hooks.state` at all, drove the review screen with keystrokes `2` then Enter
("Trust all and continue"), and read back what Codex itself wrote:

```toml
[hooks.state."/home/dgazineu/.claude/jobs/7838923c/tmp/tuispike/inst/public/repo/.codex/hooks.json:session_start:0:0"]
trusted_hash = "sha256:e3c66dbc6b9113344a37d6a9fb44869b51022de9a0d1928d5b1202e0e03620f7"
```

The hash is byte-identical to the one `codexhash.py` computed — **the hashing
algorithm the sibling spike reverse-engineered is correct**. The only
difference was the key: Codex uses the **per-repo symlinked path**
(`inst/public/repo/.codex/hooks.json`), not the resolved instance path. It does
not canonicalize the symlink.

With that key in place:

- **Correct hash → no review screen.** Startup went straight to the composer
  (same clean header as question 2). Submitting one turn produced
  `TUI-HOOK-FIRED` in the hook log, so the hook really ran, unprompted. Worth
  noting for anyone testing this: the `SessionStart` hook does **not** fire at
  the first frame — a 30-second idle TUI left the log absent. It fires when the
  first turn is submitted.
- **Wrong hash → the modal above, every time.** With the right key but a
  `sha256:0000…` hash, the TUI blocks on "Hooks need review / 1 hook is new or
  changed" and the hook log stays empty.

So the behaviour diverges sharply from `exec`. Under `exec` a bad hash is a
completely silent skip; under the TUI it is a blocking three-way modal that a
developer must answer before reaching a prompt. The gate is one line
(`codex-rs/tui/src/startup_hooks_review.rs:259-261`):

```rust
fn review_is_needed(bypass_hook_trust: bool, entry: &HooksListEntry) -> bool {
    !bypass_hook_trust && review_needed_count(entry) > 0
}
```

`review_needed_count` filters `entry.hooks` by `hook_needs_review`, so a single
stale hook among many is enough to interrupt. There is no per-repo config
escape hatch here — `bypass_hook_trust` comes from the `--bypass-hook-trust`
CLI flag or `--psp` (`codex-rs/tui/src/lib.rs:946`, `:1079`), which a developer
typing plain `codex` will not be passing. **(Read from the implementation, not
tested: I did not verify that the flag suppresses the screen.)**

### 4. The trust entry belongs on the repo root, and it holds from any depth

**Verified by experiment.** Starting the TUI from
`inst/public/repo/a/b/c` — three levels below the repo root — with the trust
entry keyed only on `inst/public/repo`:

```
╭─────────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.147.0)                              │
│                                                         │
│ model:     gpt-5-PROJECT-LAYER   /model to change       │
│ directory: ~/.claude/jobs/7838923c/tmp/tuispike/…/a/b/c │
╰─────────────────────────────────────────────────────────╯
  …
  gpt-5-PROJECT-LAYER default · ~/.claude/jobs/7838923c/tmp/tuispike/inst/public/repo/a/b/c
```

No trust prompt, and `gpt-5-PROJECT-LAYER` again confirms the project config
layer resolved from three levels down. Repeating this with the hook payload in
place (correct key on the repo path) also started clean and fired the hook on
the first turn — the hook trust key does not need a per-subdirectory variant
either, because discovery walks up to the same `repo/.codex/hooks.json`
regardless of where the developer is sitting.

This is consistent with the git-repo-root rule the sibling spike read out of
`get_active_project`: cwd is `a/b/c`, the git root is `repo`, and the entry on
`repo` matches. It follows that an entry on the *instance* root alone would not
help a developer inside a repo — but I did not test that case, since the design
already writes one entry per repo.

## Implications

The "run plain `codex` and it just works" goal is met, and it is met by exactly
the configuration the design already calls for: one
`[projects."<repo root>"] trust_level = "trusted"` entry per cloned repo. With
that entry, the interactive TUI starts on a live composer with the project
config layer, skills, and instruction context loaded, from the repo root or any
subdirectory. No known limitation needs to be written up for the trust gate.

Hooks are where the design needs to say something it did not previously need to
say. The evidence gathered under `exec` — that a wrong or missing hash causes a
silent skip — does not transfer to the interactive path. Interactively, the same
condition is a blocking modal reading "Hooks need review". That converts a
tolerable silent degradation into a hard stop for the primary use case, so hook
hash correctness is not a nice-to-have for the TUI; it is load-bearing. Any
niwa operation that rewrites `hooks.json` without recomputing the entry — a
payload update, a plugin version bump — leaves every developer in the workspace
facing a modal on their next `codex` start.

The key format correction matters concretely for implementation. Codex records
hook trust under the **path it discovered**, which for the symlink architecture
is `<repo>/.codex/hooks.json`, not the instance's real path. niwa must write
one `[hooks.state."<repo>/.codex/hooks.json:<event>:<group>:<index>"]` entry
per cloned repo, exactly parallel to the per-repo `[projects.…]` trust entries
— the same payload file, keyed N times, once per symlink pointing at it. Both
lists grow with the repo count and both must be rewritten when repos are added.

The one piece of good news buried in the hook result: the hash algorithm the
sibling spike derived is exactly right. Codex's own "Trust all and continue"
wrote a hash identical to the computed one, which is about as strong a
confirmation as this can get without reading the hashing source.

## Surprises

Codex does not canonicalize the symlink when keying hook trust. The project
config layer, the skills, and the instruction context all resolve *through*
`repo/.codex -> ../../.codex` to the same physical payload, but the trust key
records the symlink path. So one physical `hooks.json` shared by five repos
needs five identical `hooks.state` entries differing only in their path prefix.
It works, but it is a shape worth writing down before someone "simplifies" it
to one entry.

The `SessionStart` hook does not run at TUI startup despite the name. A
30-second idle session left the hook log empty; the hook fired only after the
first turn was submitted. Anyone verifying hook delivery interactively will
conclude hooks are broken if they only look at the first screen.

Driving the hook detail view by arrow keys was unreliable — five `Down`
presses from the events list landed on `Stop` rather than `SessionStart`, so
the list is not a flat one-row-per-event selection. Reading the key out of the
config Codex wrote itself proved both faster and more trustworthy than
navigating to it.

## Open Questions

I did not verify that `--bypass-hook-trust` actually suppresses the review
screen; I only read the flag's plumbing at `codex-rs/tui/src/lib.rs:946` and
`:1079`. It is almost certainly irrelevant to the design anyway, since the goal
is plain `codex` with no flags.

I did not drive past the trust prompt in the untrusted baseline, so I cannot
say from experiment whether the hook review screen follows the trust screen or
is skipped once the directory is trusted in-session. The source suggests they
are separate sequential gates, but that is inference.

I did not test the negative case for question 4 — a trust entry on the
*instance* root only, with cwd inside a repo. The sibling spike's reading of
`get_active_project` predicts it would fail, and the design writes per-repo
entries regardless, so exercising it would only confirm a path niwa will not
take. Someone wanting that confirmation needs one more run with the
`[projects.…]` key pointed at `inst/` instead of `inst/public/repo`.

Finally, everything here used a synthetic single-repo tree. Whether a
multi-repo instance with a dozen `[projects.…]` and a dozen `[hooks.state.…]`
entries in one user config behaves identically is untested, though nothing in
the observed mechanism suggests it would not.

## Summary

The interactive TUI starts completely clean — live composer, project config
layer loaded, skills available, no question asked — given exactly the
configuration niwa plans to write, and it does so from the repo root or any
subdirectory beneath it, so the "run plain codex and it just works" goal is met
for the primary use case. The one thing the design must change is hooks: unlike
`exec`, where a wrong or missing hook hash is a silent skip, the TUI blocks on
a "Hooks need review" modal, and the trust key must be written per repo against
the symlinked path `<repo>/.codex/hooks.json` rather than the instance's real
path — Codex does not canonicalize the symlink. The biggest untested question is
whether this holds at scale in a real multi-repo instance with a dozen trust
and hook-state entries in one user config, since every experiment here used a
synthetic single-repo tree.
