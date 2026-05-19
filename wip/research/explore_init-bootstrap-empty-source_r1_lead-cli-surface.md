# Lead: What CLI surface fits the bootstrap-from-empty-remote case?

## Findings

### niwa CLI conventions

Surveying every flag declared in `internal/cli/*.go` yields a clear and
consistent idiom. Niwa is a flag-rich, mode-by-positional-args CLI built on
cobra. The relevant patterns:

1. **Modes are selected by `(args, flags)` together — never by a separate
   verb.** `internal/cli/init.go:99-131` defines an internal `initMode` enum
   (`modeScaffold`, `modeNamed`, `modeClone`) and `resolveInitMode()` picks
   the mode from whether a positional `<name>` was given, whether `--from`
   was set, and whether the registry already knows the name. The user never
   types the mode name; the verb is always `init`. The same pattern shows
   up in `apply` (instance vs workspace-wide), `create` (workspace name vs
   discover-from-cwd), and `destroy` (instance vs workspace-wide vs forced
   wipe).

2. **Symmetric `--feature` / `--no-feature` flag pairs are the niwa house
   style for tri-state behavior.** This pattern is used four times in the
   existing surface:
   - `--overlay <repo>` / `--no-overlay` (init.go:43-44)
   - `--channels` / `--no-channels` (create.go:20-21, apply.go:28-29)
   - `--pull` is implicit default; `--no-pull` disables (apply.go:22)
   - `--install-plugins` is implicit default; `--no-install-plugins`
     disables (init.go:46, create.go:22, apply.go:32)

   When both members of a pair would be set simultaneously, niwa explicitly
   rejects with "mutually exclusive" (init.go:135-137). The unset state
   means "use the default behavior, possibly informed by environment or
   global config."

3. **One-shot opt-in flags use `--allow-*` for guardrail bypasses.**
   `--allow-dirty` (apply.go:21), `--allow-missing-secrets`
   (create.go:23-25, apply.go:23-25), `--allow-plaintext-secrets`
   (create.go:26-28, apply.go:26-27). The flag-description language is
   consistent: "one-shot — re-evaluated each invocation," and "no state
   persistence."

4. **Interactive prompts are gated by `IsStdinTTY()` and degrade with
   guidance, not silent defaults.** `internal/cli/prompt.go:21-23` defines
   the primitive. The usage sites
   (destroy.go:116-127, destroy.go:242-247, destroy.go:328-330) all follow
   the same shape: if TTY, prompt; if not TTY, **refuse with an error
   suggesting the explicit flag** (`--force`, name argument, etc.). Niwa
   never silently picks a non-default behavior just because stdin happens
   to be a pipe.

5. **Side-effecting registry writes carry audible audit trails.**
   `--rebind` triggers a prominent stderr WARNING after success
   (init.go:351-359). Comment at init.go:354 spells the rationale: "an
   automated agent passing it programmatically still leaves an audit
   trail."

6. **A successful command writes a landing path so the shell wrapper
   `cd`s the user into the right directory.** init (init.go:389-393),
   create (create.go:202), destroy (destroy.go:140-144), session
   (session_lifecycle_cmd.go:114-117). This is niwa's signature UX move
   and any new bootstrap flow should honor it.

7. **Sub-commands for new "modes" are reserved for genuinely different
   verbs.** `niwa init`, `niwa create`, `niwa apply`, `niwa destroy`,
   `niwa go`, `niwa reset`, `niwa session`, `niwa surface` — each names a
   distinct lifecycle action. Variants of the same action (scaffold vs
   clone, instance vs workspace) are selected by flags + args, not by a
   new verb. There is no `niwa init-from`, no `niwa apply-instance`.

### Option assessments

For each option, the scenario is: user has just created
`dangazineu/commuter` on GitHub, an empty repo. They want
`niwa init commuter --from dangazineu/commuter` (or equivalent) to drop
them in a worktree session containing a scaffolded
`.niwa/workspace.toml` on a branch named (say) `niwa-bootstrap`.

#### Option A — Transparent fallback in `--from`

Command line:

```
niwa init commuter --from dangazineu/commuter
```

niwa attempts `MaterializeFromSource`, sees the remote is reachable but
has no `.niwa/`, and silently scaffolds-and-stages.

- **Pros**
  - Zero discovery cost. The flag the user already knows simply works.
  - No new flag surface to document or test.
  - Aligned with niwa's "DWIM" tone in init: the registry-aware
    `niwa init <name>` already silently switches between scaffold and
    clone based on whether the name is registered (init.go:124-128).

- **Cons**
  - Violates niwa convention #4 above. Today, the `--from` contract is
    "fetch and materialize"; making it sometimes scaffold-and-stage
    instead changes the verb's meaning depending on a remote-state
    condition the user can't see locally. Scripts that pass `--from` to
    pin a known config repo will silently start scaffolding if the
    config gets deleted upstream.
  - Surprising failure-recovery semantics: a temporary network blip or
    auth misroute could be misclassified as "remote has no config" and
    trigger a scaffold the user didn't want. The classification logic
    has to be airtight, and even then the user can't audit it.
  - Reviewers of niwa state files later won't see a clear signal that
    bootstrap, not clone, occurred. Today registry entries with a
    `source_url` are interpreted as "this came from there"; a bootstrap
    that landed-and-staged but hasn't pushed yet is a different
    relationship.

#### Option B — Explicit flag

Command line (any of):

```
niwa init commuter --from dangazineu/commuter --bootstrap
niwa init commuter --from dangazineu/commuter --create-config
niwa init commuter --from dangazineu/commuter --scaffold
```

niwa requires the flag; without it, an empty remote still fails the way
it does today.

- **Pros**
  - Matches the `--allow-*` idiom (convention #3): one-shot opt-in for
    a behavior the user is explicitly authorizing.
  - Discoverable in `--help` and reads as intent in scripts/CI.
  - Composes cleanly with `--no-overlay`, `--overlay`, `--skip-global`,
    `--rebind` — all niwa flags layer onto a single `init` invocation.
  - Trivially testable: presence/absence is a deterministic switch.
  - The error path stays informative: if a user runs without the flag
    and hits an empty remote, niwa can print the exact remediation
    command with `--bootstrap` appended.

- **Cons**
  - One more flag to learn. Mitigated by the fact that this command is
    rare (workspace setup happens once per project).
  - Naming: `--bootstrap` is overloaded (vault bootstrap, daemon
    bootstrap), `--create-config` is clear but long, `--scaffold` is
    accurate but doesn't convey "after detecting empty remote."

#### Option C — New mode (subcommand or new flag-as-mode)

Command line (either):

```
niwa adopt commuter --from dangazineu/commuter
niwa init commuter --adopt dangazineu/commuter
```

Niwa introduces a new lifecycle verb (or a mode-selector flag) that
exists specifically to bootstrap from an empty remote.

- **Pros**
  - Clean separation: `init` clones existing config, `adopt` claims an
    empty remote and stages a scaffold. Discoverable as a top-level
    verb in `niwa --help`.
  - Allows distinct semantics (e.g., `adopt` could refuse a non-empty
    remote, where `init --from` would happily clone it) without
    overloading.

- **Cons**
  - Command sprawl (convention #7). Niwa does not split modes into
    separate verbs today, and a new top-level verb signals a major
    lifecycle action when this is really a one-time bootstrap variant
    of init.
  - A flag-as-mode (`--adopt <repo>`) is essentially `--from <repo>`
    with one bit of metadata attached. That's a clearer fit for option
    B (a boolean modifier on `--from`).
  - Existing users have to learn that `init --from <empty-repo>` is
    "wrong" and `adopt <empty-repo>` is "right" — niwa today has no
    convention pointing them at the second verb.

#### Option D — Prompt

Command line:

```
niwa init commuter --from dangazineu/commuter
```

niwa detects the empty remote and asks:

```
Remote dangazineu/commuter has no .niwa/workspace.toml.
Scaffold a minimal config and stage it on a branch? [Y/n]
```

- **Pros**
  - Lowest friction interactively. The user doesn't need to know the
    flag exists; niwa surfaces the option at the moment they need it.
  - Matches convention #4 (prompt-when-TTY) directly.
  - Educational: the prompt itself documents what's about to happen.

- **Cons**
  - Useless or actively harmful in non-interactive contexts (CI, agent
    workflows, scripted setup). niwa's convention #4 (destroy.go:117,
    242, 329) is "refuse with guidance when stdin is not TTY" — a
    prompt-only path means non-interactive callers get the same hard
    failure they get today, with no flag to escape it.
  - Two-step UX: prompt fires after the materialize attempt, which
    means the user has already paid the network round trip. Latency is
    fine for interactive use but feels heavy as the primary signal.

#### Option E — Hybrid (prompt + flag for non-interactive)

Command line — interactive case:

```
niwa init commuter --from dangazineu/commuter
# prompts: Remote has no config — scaffold one? [Y/n]
```

Command line — non-interactive (CI, agent):

```
niwa init commuter --from dangazineu/commuter --bootstrap
# proceeds without prompting

niwa init commuter --from dangazineu/commuter --no-bootstrap
# fails fast even if TTY (suppresses the prompt)
```

- **Pros**
  - Covers both audiences. Interactive users get the lowest-friction
    UX (prompt); non-interactive users get a deterministic flag.
  - Mirrors the existing `--feature` / `--no-feature` pair pattern
    (convention #2), so `--bootstrap` / `--no-bootstrap` reads as a
    native niwa flag pair.
  - Degrades safely: when stdin is not a TTY and neither flag is
    given, niwa refuses with a remediation hint pointing at
    `--bootstrap` — the same pattern destroy.go follows when the
    confirmation prompt can't fire.
  - Scriptable and discoverable: CI snippets paste cleanly with
    `--bootstrap`; interactive use needs no learning curve.

- **Cons**
  - Three flag states to test (set, no-set, unset) and three contexts
    (TTY-prompt-yes, TTY-prompt-no, non-TTY-refuse). Test matrix is
    larger.
  - Naming overload: `--bootstrap` is reused in niwa today
    informally (the stderr "Bootstrap with: infisical login" note at
    init.go:507-511). Disambiguation in docs is needed; a different
    name (`--scaffold-config`, `--init-empty`) is an option.

### Prior art comparison

#### `cargo new` vs `cargo init` (Rust)

- **User runs**: `cargo new my-pkg` (creates new dir) vs `cargo init`
  (adopts current dir).
- **Empty/missing case**: `cargo init` is designed for the existing-dir
  case: it writes a Cargo.toml and uses any existing `src/*.rs` files,
  or creates a sample if none exist.
- **Prompt?** No. Both commands are non-interactive.
- **Flag?** No flag toggles between them — they are separate
  subcommands. Selector is verb-level.
- **Source**: [cargo init — The Cargo Book](https://doc.rust-lang.org/cargo/commands/cargo-init.html);
  [cargo new vs cargo init — Rust Forum](https://users.rust-lang.org/t/cargo-new-vs-cargo-init/40794).

Takeaway: Cargo's split corresponds to the "new dir vs adopt existing
dir" axis, **not** to "remote has config vs remote is empty." For niwa,
the parallel split would be `niwa init` (scaffold local) vs `niwa init
--from` (clone remote) — which we already have. The empty-remote case
is a *third* state Cargo doesn't have an analog for. Cargo's lesson is
that verb-splits work when the split is along a hard semantic boundary;
ours isn't.

#### `gh repo create` (GitHub CLI)

- **User runs**: `gh repo create my-repo --clone` or
  `gh repo create my-repo --template org/template --clone`.
- **Empty/missing case**: With `--template`, gh always uses the template
  remotely. With `--clone` and an empty new repo, the local clone is
  created with a `.git` directory but no content checked out (see
  cli/cli#2290, cli/cli#5142). With `--template --clone` together, the
  clone step is known to fail with "couldn't find remote ref" when the
  template hasn't replicated yet.
- **Prompt?** Yes — `gh repo create` (with no args) opens an interactive
  questionnaire. Passing args/flags makes it non-interactive.
- **Flag?** `--clone` is opt-in. `--template` is a separate flag.
- **Source**: [gh repo create manual](https://cli.github.com/manual/gh_repo_create);
  [Issue #2290](https://github.com/cli/cli/issues/2290);
  [Issue #5142](https://github.com/cli/cli/issues/5142).

Takeaway: gh treats "create" and "scaffold-from-template" as distinct
flag-modifiers on a single verb. It already demonstrates the pitfalls
of stitching multiple optional behaviors onto one command without
explicit flags: silent edge cases (empty clone) and race conditions
(template replication). This argues against Option A (transparent
fallback) for niwa — leaving classification to runtime introspection
breeds silent corner cases.

#### `terraform init`

- **User runs**: `terraform init` in a working directory containing
  `.tf` files that may declare a remote backend.
- **Empty/missing case**: If the backend (e.g. S3 state bucket) doesn't
  exist yet, `terraform init` fails. Terraform does **not**
  auto-create the backend. The community pattern is a separate
  "tfstate-backend" module that creates the bucket first, then a real
  module that uses it.
- **Prompt?** No bootstrap prompt; terraform fails loud with an error
  pointing at backend config.
- **Flag?** `-backend=false` skips backend init, `-backend-config=...`
  for partial configs, `-reconfigure` to ignore existing setup. None
  bootstrap the backend.
- **Source**: [terraform init command](https://developer.hashicorp.com/terraform/cli/commands/init);
  [The Terraform Bootstrap Problem](https://burakdede.com/blog/the-terraform-bootstrap-problem-how-to-create-your-state-backend-without-going-insane/).

Takeaway: Terraform's choice is the most conservative — explicitly
refuse to auto-bootstrap, document the workaround, accept the user
friction. The newer ecosystem (atmos, Terragrunt's
`--backend-bootstrap`) layers explicit opt-in flags on top. The lesson:
when the user community asks for auto-bootstrap, the convention is to
add a **named flag** rather than a transparent fallback.

#### `git init` vs `git clone`

- **User runs**: `git init` (no remote) or `git init` inside an
  existing dir, or `git clone <url>` against a remote.
- **Empty/missing case**: `git init` on a fresh dir scaffolds `.git/`
  and stops. `git clone` against an empty remote succeeds with a
  warning ("warning: You appear to have cloned an empty repository.")
  and leaves the local clone with the remote configured.
- **Prompt?** Never.
- **Flag?** No flag bridges the two. They are separate verbs.
- **Source**: git documentation; widely known behavior.

Takeaway: git's two-verb model is the original of the
"create from nothing vs clone existing" split. For niwa, the
analog is `niwa init` (scaffold) vs `niwa init --from` (clone) — which
matches. Our empty-remote case lives in between: clone succeeds at the
git layer, but materialize fails at the niwa-config layer. Git has no
equivalent; its analog for "clone an empty repo and stage a commit"
would be: `git clone`, `git add`, `git commit`, `git push` — four
explicit user actions, no auto-magic.

#### `npm init` and `npm init -y`

- **User runs**: `npm init` (interactive Q&A) or `npm init -y` (accept
  defaults silently).
- **Empty/missing case**: `npm init` always writes a new `package.json`
  if none exists. With `-y`, no prompts; defaults like `name` (from
  folder), `version: 1.0.0`, `license: ISC` fill in.
- **Prompt?** Yes by default; `-y` suppresses.
- **Flag?** `-y` / `--yes`.
- **Source**: [npm-init docs](https://docs.npmjs.com/cli/v11/commands/npm-init/).

Takeaway: npm's pattern is the clearest precedent for **Option E**
(hybrid). Prompt-by-default interactively, `--yes` (or in our case
`--bootstrap`) for non-interactive consent. The flag's existence is
discoverable in `--help`; the prompt is the on-ramp for first-time
users.

#### `helm create`

- **User runs**: `helm create my-chart`.
- **Empty/missing case**: Always scaffolds. Creates the dir if missing,
  overwrites conflicting files in place if present, leaves other files
  alone.
- **Prompt?** No.
- **Flag?** `--starter` to use a starter template instead of the
  built-in one.
- **Source**: [helm create](https://helm.sh/docs/helm/helm_create/).

Takeaway: helm uses a dedicated verb (`create`, distinct from `install`
or `init`). The verb is unambiguous; no prompt or flag needed because
the whole verb's purpose is scaffolding. This is the analog of Option
C (`niwa adopt`) — and underscores why C is overkill for niwa: helm's
scaffold and install/upgrade are genuinely different lifecycle stages,
whereas niwa's bootstrap is one branch inside the same `init`
lifecycle stage.

## Recommendation

**Option E (hybrid): prompt by default, `--bootstrap` to force in
non-interactive contexts, `--no-bootstrap` to suppress the prompt and
fail fast.**

Rationale:

1. **Fits convention #2** — niwa already has four `--feature` /
   `--no-feature` flag pairs in this exact role. Adding
   `--bootstrap` / `--no-bootstrap` follows muscle memory.

2. **Fits convention #4** — interactive prompt with TTY gating is how
   destroy.go handles its three confirmation cases. Reusing the same
   `IsStdinTTY()` primitive (prompt.go:21-23) means the non-TTY
   behavior — refuse-with-guidance pointing at `--bootstrap` — is
   already a niwa-native pattern users have seen.

3. **Fits convention #3** — `--bootstrap` reads as a one-shot opt-in
   modifier like `--allow-dirty`, `--rebind`. The user is explicitly
   authorizing a side-effecting setup action; the flag leaves an
   audit trail in shell history and CI logs.

4. **Avoids the failure modes of A and C.** Transparent fallback (A)
   makes `--from` polymorphic in a way that breaks scripts and
   confuses operators reading logs after the fact. A new verb (C) is
   command sprawl for what is really a one-time variant of the
   existing init flow — and adding a verb tax for a once-per-project
   action is unfair to discoverability of the rest of the surface.

5. **Prior-art alignment.** The npm `init` / `init -y` model is the
   closest analog to the user's actual scenario (interactive setup
   that also needs to work in scripts), and Terragrunt's recent
   addition of `--backend-bootstrap` confirms the pattern transfers
   to "init-time auto-create the substrate."

6. **Recovery path stays clear.** Without `--bootstrap` in a non-TTY
   context, niwa returns the same materialize-failure today produces,
   plus an explicit hint: "Remote has no .niwa/. Re-run with
   `--bootstrap` to scaffold a config and stage it on a branch." This
   preserves the existing fail-fast semantics for scripts that
   shouldn't be silently scaffolding.

### Naming

`--bootstrap` is the recommended name despite a soft overload with
the vault-bootstrap stderr pointer (init.go:507). Reasons:

- It's the right concept word: the user is bootstrapping the
  config-side of the workspace.
- `--scaffold` is accurate but already implies the local-scaffold
  modes (`modeScaffold`, `modeNamed`).
- `--create-config` is unambiguous but long for everyday use.
- The vault-bootstrap pointer is a noun-phrase in stderr prose, not
  a flag name, so the collision is verbal not structural.

If reviewers object to the overload, `--init-empty` is a clean
runner-up — it telegraphs the precise condition the flag handles.

## Sketch — recommended command

### Interactive case (TTY, no `--bootstrap` / `--no-bootstrap`)

```
$ niwa init commuter --from dangazineu/commuter
Initializing from: https://github.com/dangazineu/commuter.git
Remote dangazineu/commuter has no .niwa/workspace.toml.
Scaffold a minimal config and stage it on a branch in a worktree
session? The remote is not pushed — you inspect and push when ready.
[Y/n] y
Scaffolded .niwa/workspace.toml.
Staged on branch: niwa-bootstrap
Worktree: /home/user/workspaces/commuter/.niwa/worktrees/bootstrap-<id>
Next steps:
  1. cd into the worktree (the shell wrapper handles this)
  2. Edit .niwa/workspace.toml
  3. git push --set-upstream origin niwa-bootstrap
  4. Run `niwa apply`
```

Stdout exit 0. The shell wrapper drops the user into the worktree dir
via `writeLandingPath()` — same mechanism `niwa session create` uses
(session_lifecycle_cmd.go:114-117).

### Non-interactive case (CI/agent, `--bootstrap` set)

```
$ niwa init commuter --from dangazineu/commuter --bootstrap
Initializing from: https://github.com/dangazineu/commuter.git
Remote has no .niwa/workspace.toml — scaffolding (--bootstrap).
Staged on branch: niwa-bootstrap
Worktree: /workspaces/commuter/.niwa/worktrees/bootstrap-<id>
```

Stdout exit 0. Branch name and worktree path are also written to
stderr in case stdout is captured.

### Non-interactive case (no flag, no TTY) — fail fast

```
$ niwa init commuter --from dangazineu/commuter < /dev/null
Initializing from: https://github.com/dangazineu/commuter.git
Error: remote dangazineu/commuter has no .niwa/workspace.toml and stdin is not a terminal.
  Re-run with --bootstrap to scaffold a minimal config and stage it on a branch,
  or with --no-bootstrap to keep the original "fail when empty" behavior explicit.
```

Stderr exit 1. Matches the destroy.go non-TTY-refuse pattern verbatim
(see destroy.go:117, 242, 329).

### Suppress prompt explicitly (`--no-bootstrap`, TTY)

```
$ niwa init commuter --from dangazineu/commuter --no-bootstrap
Initializing from: https://github.com/dangazineu/commuter.git
Error: remote dangazineu/commuter has no .niwa/workspace.toml.
  --no-bootstrap was set; refusing to scaffold. Re-run with --bootstrap
  to opt in.
```

Stderr exit 1.

## Implications

1. **Flag mutual-exclusion needs adding to runInit**, matching the
   `--overlay` / `--no-overlay` precedent at init.go:135-137. A user
   passing both `--bootstrap` and `--no-bootstrap` should fail with
   the same error wording.

2. **State capture**: when bootstrap runs, the InstanceState
   (workspace.SaveState) needs a marker that this workspace was
   bootstrapped not cloned. The registry entry's `source_url` already
   records the URL; we likely need a new field like
   `BootstrapBranch` so subsequent applies know the remote may not
   yet contain the config.

3. **Audit-trail messaging**: bootstrap is side-effecting against the
   user's local branch state (creates a branch). Following the
   `--rebind` precedent (init.go:351-359), the success message
   should be prominent on stderr, not just stdout. The exact wording
   should mention the branch name and worktree path on separate
   lines.

4. **Plugin install behavior**: rank-2 deprecation notices and the
   plugin auto-install path (init.go:281-283) need to be skipped on
   bootstrap — there is no source config to inspect rank against. A
   bootstrapped config is unambiguously the modern shape niwa expects.

5. **Test surface**: the bootstrap path needs both unit tests (mode
   selection, flag mutual exclusion, prompt gating) and a `@critical`
   functional test using the `localGitServer` helper to simulate the
   "empty remote" case (see `docs/guides/functional-testing.md`,
   referenced in niwa/CLAUDE.md).

## Surprises

- **niwa already has a quiet form of Option A in another place.**
  init.go:124-128 shows that when the user runs `niwa init <name>`
  (no `--from`), niwa silently switches between scaffold and clone
  based on whether `<name>` is registered with a SourceURL. This
  could be cited as precedent for Option A — but the difference is
  that the switch is driven by *local registry state* the user
  controls, not by *remote state* niwa probes at runtime. The
  asymmetry is real and the precedent does not transfer.

- **`gh repo create --template --clone` has documented bugs going
  back years** that look strikingly similar to what Option A would
  expose for niwa: timing-sensitive failures, silently-empty local
  state, "did this work?" ambiguity. This is the strongest single
  reason to avoid transparent fallback.

- **Terragrunt's `--backend-bootstrap` defaults to false**, even
  though the obvious convenience would be to default true. They
  chose explicit opt-in for exactly the audit-trail and
  least-surprise reasons. This is a direct precedent for naming our
  flag `--bootstrap` and defaulting it off (off = prompt or fail).

## Open Questions

1. **Branch name**: `niwa-bootstrap` is a placeholder. Should the
   branch name be configurable via a flag (`--bootstrap-branch
   <name>`)? Or fixed by convention? Suggest: fixed default + a
   future flag if demand surfaces.

2. **What does the scaffolded `.niwa/workspace.toml` contain?**
   Covered by lead-minimal-scaffold separately, but the CLI surface
   should *not* gain a flag for "minimal vs maximal scaffold"
   variants — keep `--bootstrap` boolean and let scaffold contents
   be a separate decision.

3. **Should `--bootstrap` work without `--from`?** No. Bootstrap only
   makes sense when there's a remote to push the scaffolded config
   to eventually. Without `--from`, `niwa init <name>` already
   scaffolds locally and the user can wire up the remote later. The
   flag should error if used without `--from`.

4. **Should `--bootstrap` also work when the remote is 404 (does not
   exist), not just empty?** Probably no for v1 — distinguish
   "remote exists but empty" (auto-bootstrap fits) from "remote
   doesn't exist" (the user probably typo'd the slug or hasn't
   created the repo yet). Lead-other-failures should land that
   decision.

5. **Interaction with `--overlay` / `--no-overlay`**: if the user
   passes `--from <empty-remote> --bootstrap --overlay <other-repo>`,
   should niwa try to clone the overlay (whose remote may also be
   empty) and recursively bootstrap? Suggest: no — bootstrap applies
   only to the primary `--from` source for v1; overlay-bootstrap can
   be a follow-up.

## Summary

niwa's CLI convention strongly favors a `--feature` / `--no-feature`
flag pair gated by `IsStdinTTY()` for one-shot, side-effecting,
auditable actions — exactly the shape `--bootstrap` / `--no-bootstrap`
would take. Option E (prompt by default, `--bootstrap` for
non-interactive, `--no-bootstrap` to suppress) matches four existing
niwa flag pairs, mirrors npm's `init -y` and Terragrunt's
`--backend-bootstrap` precedents, and avoids the silent-classification
hazards documented in `gh repo create --template --clone`. The
recommended invocation is `niwa init commuter --from
dangazineu/commuter --bootstrap` (non-interactive) or the same
without the flag in a TTY, with the shell wrapper landing the user
inside the worktree session containing the scaffolded config on a
`niwa-bootstrap` branch.

Sources:
- [cargo init — The Cargo Book](https://doc.rust-lang.org/cargo/commands/cargo-init.html)
- [cargo new vs cargo init — Rust Forum](https://users.rust-lang.org/t/cargo-new-vs-cargo-init/40794)
- [gh repo create manual](https://cli.github.com/manual/gh_repo_create)
- [gh repo create --template creates empty repository locally — Issue #2290](https://github.com/cli/cli/issues/2290)
- [gh repo create --clone arbitrarily fails — Issue #5142](https://github.com/cli/cli/issues/5142)
- [terraform init command reference](https://developer.hashicorp.com/terraform/cli/commands/init)
- [The Terraform Bootstrap Problem — Burak Dede](https://burakdede.com/blog/the-terraform-bootstrap-problem-how-to-create-your-state-backend-without-going-insane/)
- [Automatic Backend Provisioning — atmos](https://atmos.tools/changelog/automatic-backend-provisioning)
- [npm-init docs](https://docs.npmjs.com/cli/v11/commands/npm-init/)
- [helm create](https://helm.sh/docs/helm/helm_create/)
