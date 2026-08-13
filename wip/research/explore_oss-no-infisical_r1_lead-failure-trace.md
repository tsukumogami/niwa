# Lead: Where exactly does the required-secret failure fire across `niwa init` -> `niwa create` -> `niwa apply` -> `niwa dispatch`, and what does a user actually see?

**Evidence class.** Everything below marked OBSERVED was produced by building
the worktree HEAD (`go build ./cmd/niwa`) and running it against an offline
`file://` bare-repo config source in a sandboxed `HOME`/`XDG_CONFIG_HOME`, with
`infisical` absent from `PATH`. Reproduction scripts:
`wip/repro.sh` (init/create/apply, both personas), `wip/repro2.sh` (dispatch +
SessionStart hook), `wip/repro3.sh` (status + flag surface). The sandbox was
under `$TMPDIR` and has been removed. Items marked RECONSTRUCTED come from
reading source only.

---

## Findings

### 1. There is exactly one call site of `checkRequiredKeys`, and it lives inside the shared pipeline

`internal/workspace/required.go:47` defines it; the only caller is
`internal/workspace/apply.go:1325`:

```go
// Post-merge required/recommended enforcement (PRD R33/R34). The
// required check is NOT downgraded by AllowMissingSecrets; ...
if err := checkRequiredKeys(effectiveCfg, a.Reporter.Writer()); err != nil {
    return nil, err
}
```

That call sits in `Applier.runPipeline` (`internal/workspace/apply.go:732`),
which is shared by `Applier.Create` (`apply.go:448`) and `Applier.Apply`
(`apply.go:546`). So the reachability question reduces to: **which commands
reach `runPipeline`?**

| Command | Reaches `runPipeline`? | Path |
|---|---|---|
| `niwa init <name> --from <src>` (clone mode) | **NO** | `runInit` `modeClone` (`internal/cli/init.go:608-703`) only materializes `.niwa/`, registers, writes state, materializes workspace root. Never constructs an Applier. |
| `niwa init --bootstrap` (remote has no `workspace.toml`) | YES | `defaultRunBootstrap` -> `createWrapper` -> `applier.Create` (`internal/cli/init.go:197`) |
| `niwa create` | YES | `internal/cli/create.go:247` -> `applier.Create` |
| `niwa apply` | YES | `internal/cli/apply.go:262` -> `applier.Apply`, per instance |
| `niwa dispatch` | YES | `internal/cli/dispatch.go:300` -> `provisionInstanceFunc` -> `realProvisionInstance` -> `applier.Create` (`internal/cli/instance_from_hook.go:417`) |
| `niwa instance from-hook` (SessionStart) | YES | `internal/cli/instance_from_hook.go:172` -> same `realProvisionInstance` -> `applier.Create` |
| `niwa worktree from-hook` (WorktreeCreate) | NO | worktree content path inherits already-materialized env by byte-copy; resolves no secrets |

**The scope assumption that `niwa init` fails outright is wrong for the clone
path.** OBSERVED, Persona A:

```
$ niwa init tsukumogami --from file:///.../dot-niwa.git --no-overlay
Initializing from: file:///.../dot-niwa.git
Workspace "tsukumogami" initialized at /.../tsukumogami from remote config.

The workspace root is ready. Create an instance to start working:

  niwa create <name>
EXIT=0
```

Init exits 0 and says nothing at all about the five required keys it just
cloned a declaration for. The wall is one command later.

### 2. `niwa init` has no `--allow-missing-secrets` flag — and does not need one

Confirmed OBSERVED: `niwa init --help | grep -c allow-missing` returns `0`.
Confirmed by source: `internal/cli/init.go` registers no such flag, and there is
no Applier on the clone path to set it on. The only writers of
`Applier.AllowMissingSecrets` in the whole tree are
`internal/cli/create.go:218` and `internal/cli/apply.go:166`.

Both flags carry identical help text (`create.go:22-24`, `apply.go:24-26`):

> `downgrade unresolved vault:// references to empty strings with stderr warnings. Does NOT override *.required misses. One-shot -- re-evaluated each invocation.`

### 3. Persona A (no vault provider anywhere) — the failure is not a vault failure at all

The public base config declares required keys with **no value entries at all** —
no `vault://` ref, no plaintext. `collectMissing` (`required.go:111-128`) looks
up `t.Values[key]`; the key is simply absent, so it is reported. The vault
resolver is never involved. That means `--allow-missing-secrets` is *structurally*
irrelevant here: there is nothing for it to downgrade.

OBSERVED, verbatim, `niwa create tsukumogami` (stderr; exit 1):

```
warning: recommended env key "BRAVE_API_KEY" not supplied: Brave search API key - resolved by overlay vault (scope env.secrets)
warning: recommended env key "TAVILY_API_KEY" not supplied: Tavily search API key - resolved by overlay vault (scope env.secrets)
required env keys not supplied:
  [env.secrets] ANTHROPIC_API_KEY: Anthropic API key for Claude - resolved by overlay vault
  [env.secrets] GH_TOKEN: GitHub PAT with repo scope - supplied via personal overlay
  [repos.niwa.env.secrets] INFISICAL_CLIENT_ID: Infisical machine identity client ID - resolved by overlay vault
  [repos.niwa.env.secrets] INFISICAL_CLIENT_SECRET: Infisical machine identity client secret - resolved by overlay vault
  [repos.niwa.env.secrets] INFISICAL_TEST_PROJECT_ID: Infisical test project ID - resolved by overlay vault
declare each key under the matching table with a value, supply it via the personal overlay, or remove it from the required sub-table
```

Adding `--allow-missing-secrets` produces byte-identical output (modulo the
nondeterministic ordering of the two `recommended` warning lines) and the same
exit 1. OBSERVED.

The remediation sentence (`required.go:100-101`) offers three routes, and **all
three are unavailable to an OSS contributor**: they cannot edit the upstream
public config to add values, they have no personal overlay that supplies them
(and no documentation pointer to create one at this moment), and they cannot
remove the required sub-table. The message is accurate and useless.

Note also the `[repos.niwa.env.secrets]` scope: `checkRequiredKeys` iterates
`cfg.Repos` (`required.go:63-71`), which is the *config override map*, not the
set of repos actually cloned into the instance. In the OBSERVED run the fixture
had zero repos in scope and the three `INFISICAL_*` keys still fired.

### 4. Persona B (vault declared, backend absent) — the failure fires EARLIER than `checkRequiredKeys`, and `--allow-missing-secrets` does not help

This is the largest correction to the preliminary read. The scope note said
"the resolver softens the unreachable vault lookup to an empty value and the
required check then fails on that emptiness." That is **not** what happens when
the Infisical CLI is missing.

`internal/vault/resolve/resolve.go:526-555` branches on the error class:

- `vault.ErrKeyNotFound` + `opts.AllowMissing` -> downgrade to empty + warning (line 531-536).
- `vault.ErrProviderUnreachable` -> **hard error, unconditionally.** `AllowMissing` is not consulted at all (line 550-555).

A missing `infisical` binary surfaces as `ErrProviderUnreachable`
(`internal/vault/infisical/infisical.go:245`, `:297`), so resolution aborts in
`ResolveAndMergeEffectiveConfig` (`effective_config.go:74-81`) and
`runPipeline` returns at `apply.go:1316-1318` — **before** line 1325.

OBSERVED, `niwa create vaultws` with no `infisical` on `PATH` (exit 1):

```
vault: env.secrets.ANTHROPIC_API_KEY: provider "(anonymous)" unreachable while resolving key "ANTHROPIC_API_KEY": infisical: running export: vault: provider unreachable: exec: "infisical": executable file not found in $PATH
```

OBSERVED, same command with `--allow-missing-secrets` (exit 1):

```
vault: env.secrets.GH_TOKEN: provider "(anonymous)" unreachable while resolving key "GH_TOKEN": infisical: running export: vault: provider unreachable: exec: "infisical": executable file not found in $PATH
```

Two things worth flagging. First, the flag genuinely does nothing for this
failure mode — not because of R34 (required precedence) but because R10's
downgrade was only ever wired to key-not-found. Second, **which key is named is
nondeterministic** (`ANTHROPIC_API_KEY` on one run, `GH_TOKEN` on the next):
the walker iterates a map and returns on the first failure, so the user is told
about one arbitrary secret out of N and gets no inventory of what else is
broken.

Unlike Persona A, Persona B does at least get a pointer at init time —
`emitVaultBootstrapPointer` (`internal/cli/init.go:939`) fires because the
config declares `[vault.provider]`. OBSERVED:

```
note: this workspace declares a vault (kind: infisical). Bootstrap with:
  `infisical login`
Then run `niwa apply`.
```

Persona A gets nothing equivalent, because the public base config has no
`[vault.provider]` block — the pointer is keyed on provider declaration, not on
required-key declaration.

### 5. Per-command failure surface

Cobra adds no prefix: `rootCmd` sets `SilenceErrors: true` / `SilenceUsage: true`
(`internal/cli/root.go:42-43`) and `Execute()` does
`fmt.Fprintln(os.Stderr, err)` + `os.Exit(1)` (`root.go:95-96`). So the user
sees the raw error string, and the `niwa: error: ` prefix appears only where an
individual command baked it into its `fmt.Errorf`.

| Command | Reaches check | `--allow-missing-secrets` | stderr shape | exit |
|---|---|---|---|---|
| `init` (clone) | no | not registered | success message | 0 |
| `create` | yes | registered, ineffective | bare `required env keys not supplied:` block | 1 |
| `apply` | yes | registered, ineffective | error printed **twice** (see below) | 1 |
| `dispatch` | yes | **not registered, never set** | `niwa: error: provisioning dispatch instance: <block>` | 1 |
| `instance from-hook` | yes | **not registered, never set** | `niwa: error: provisioning instance for session <uuid>: <block>` | 1 |

`niwa apply` prints the same multi-line block twice — once from the per-instance
loop (`internal/cli/apply.go:267`, `"error: applying to %s: %v\n"`) and again
from `combineInstanceErrors` (`apply.go:432`, `"apply failed for %s: %w"`) via
`Execute()`. OBSERVED. Note also the loop's prefix is lowercase `error: ` with
no `niwa: `, inconsistent with dispatch and the hooks.

OBSERVED, `niwa dispatch "do a thing" --detach` in the Persona A workspace:

```
warning: recommended env key "TAVILY_API_KEY" not supplied: ... (scope env.secrets)
warning: recommended env key "BRAVE_API_KEY" not supplied: ... (scope env.secrets)
niwa: error: provisioning dispatch instance: required env keys not supplied:
  [env.secrets] ANTHROPIC_API_KEY: ...
  ...
declare each key under the matching table with a value, supply it via the personal overlay, or remove it from the required sub-table
EXIT=1
```

OBSERVED, SessionStart hook with the guard satisfied (ephemeral mode on, fake
`bg` job state, cwd outside an instance):

```
warning: recommended env key "TAVILY_API_KEY" not supplied: ... (scope env.secrets)
warning: recommended env key "BRAVE_API_KEY" not supplied: ... (scope env.secrets)
niwa: error: provisioning instance for session 11111111-2222-3333-4444-555555555555: required env keys not supplied:
  ...
EXIT=1
```

With ephemeral mode off, the hook is a clean silent no-op, exit 0. OBSERVED.

The hook path is the worst-behaved of the five. `instance_from_hook.go:174`
returns the error, which exits 1 through `root.go:96`. The hook command is
`exec`-ed by `guardedNiwaHookCommand` (`internal/workspace/materialize.go:354-358`)
with no `|| true`, so the non-zero status is the hook's status. Crucially, the
success-path JSON (`sessionStartInjection`, `instance_from_hook.go:287-292`) is
never emitted — so the background worker starts anyway, at the workspace root,
with no instance and no `additionalContext` telling it where to `cd`. The
failure is loud to a terminal nobody is reading and invisible to the agent that
needs to know. I found no comment or doc in the repo stating how Claude Code
treats a non-zero SessionStart exit.

### 6. Partial state left behind

OBSERVED after a failed `niwa create`:

- **Instance directory: cleaned.** `Applier.Create` removes it on every failure
  path (`internal/workspace/apply.go:401, 420, 431, 458` — `_ = os.RemoveAll(instanceRoot)`).
  The sandbox listing showed no orphan instance dir.
- **Workspace root: retained.** `.niwa/workspace.toml`, `.niwa/instance.json`,
  `.niwa/.niwa-snapshot.toml`, `.claude/settings.json`, `.claude/skills/dispatch/`,
  and `CLAUDE.md` all survive — they were written by the successful `init`.
- **Global registry entry: retained.** `~/.config/niwa/config.toml` keeps
  `[registry.tsukumogami]` with `root` and `source_url`. OBSERVED.

So the user is left in a well-formed workspace that cannot produce a single
instance. `niwa status` at the root reports success with empty fields (OBSERVED:
`Instance:`/`Config:` blank, exit 0) — it does not surface the blocked state at
all.

`niwa dispatch` additionally arms a rollback (`internal/cli/dispatch.go:312-317`,
`defer` -> `destroyInstanceFunc`), but on this failure path `Create` already
removed the dir, so it is a no-op. The SessionStart hook arms **no** rollback;
it relies entirely on `Create`'s internal cleanup, and a failure *after* Create
(e.g. `WriteSessionMapping` at `instance_from_hook.go:184`) would leave the
instance for `niwa reap`.

### 7. What the PRDs say, versus what ships

`docs/prds/PRD-vault-integration.md:877-885` (R34) is unambiguous that required
misses are hard errors regardless of flags, and the implementation honors that
exactly. No bug there.

But `docs/prds/PRD-workspace-visibility-overlay.md:462` says, of a base config
that declares requirements without a provider:

> Teams without an overlay still get the required/recommended/optional declarations as documentation for manual secret setup.

That is the Persona A scenario verbatim, and it is not what ships. Those
declarations are not documentation — they are a hard gate that makes the
workspace unusable, and the same PRD's R24 (line 316) is what actually landed:
"`niwa apply` aborts with a non-zero exit code naming the missing var." The two
sentences in the same document describe incompatible behaviors. This is the
sharpest available evidence that the responsibility question in the exploration
scope is a genuine unresolved design question inside niwa, not merely a
misconfiguration in `tsukumogami/dot-niwa`.

### 8. There is already a loud-but-non-fatal precedent in the same file

`internal/workspace/apply.go:1191-1205` handles an unreachable *personal* vault
provider during credential-pool construction by emitting one aggregated warning
per provider and continuing:

```
personal vault provider (anonymous) unreachable; falling back to local-file and cli-session credentials.
```

That is PRD R13.1. It is the shape "loud but non-fatal" would take, implemented
and shipping, roughly 130 lines above the hard `checkRequiredKeys` gate. Any
design that softens the required path can point at this as the in-repo
precedent for tone, aggregation (one line per provider, not per key), and
placement (`a.Reporter.Warn`, deferred to below the summary line).

---

## Implications

1. **The remediation target is `create`/`dispatch`/the SessionStart hook, not
   `init`.** Framing this as "init fails" points a fix at the wrong command.
   Init succeeds and hands the user a workspace that is guaranteed to fail on
   first use. If anything, init is where a *preflight* belongs — it has the
   parsed config in hand at `internal/cli/init.go:707` and already emits a
   vault pointer at line 720; extending that to "this workspace declares N
   required secrets you cannot currently satisfy" is a small, contained change
   that converts a delayed hard wall into an up-front warning.

2. **Two distinct failure modes need two distinct answers.** Persona A is a
   *declaration-without-supply* problem that never touches the vault; Persona B
   is a *backend-unreachable* problem that never reaches the required check.
   A fix aimed at one does nothing for the other. In particular, "make
   `--allow-missing-secrets` also cover required keys" would fix neither: it is
   not consulted on Persona B's code path, and Persona A has no vault ref for
   it to act on.

3. **`--allow-missing-secrets` not covering `ErrProviderUnreachable` looks like
   the more defensible bug to fix first.** R10 says the flag exists for "debug
   and CI fallback cases." A CI runner without the vault CLI installed is
   exactly that case, and the flag silently does nothing there. Widening the
   flag to cover unreachable providers (still emitting the per-key warning) is
   a narrow change at `resolve.go:550` that does not touch R34's required
   contract.

4. **Launch-coupled paths have no escape hatch by construction.** `dispatch`
   and the SessionStart hook funnel through `realProvisionInstance`
   (`instance_from_hook.go:351-423`), which never sets `AllowMissingSecrets`.
   Even if the flag worked, a background worker could not pass it. Whatever
   "loud but non-fatal" means, it has to be reachable without a CLI flag —
   which pushes toward a config-level declaration (a per-key or per-workspace
   severity) or a default-softened behavior, not a new flag.

5. **The hook's failure is silent where it matters.** Exiting 1 with no JSON
   means the worker boots uninstrumented at the workspace root and never learns
   why. Any softening design should make the hook emit its
   `additionalContext` block *even on partial provisioning failure*, or at
   minimum emit a degraded-mode context string, rather than nothing.

---

## Surprises

- **`niwa init` succeeds.** Exit 0, cheerful "workspace is ready" message, for
  both personas. The premise in the exploration scope is incorrect.
- **`--allow-missing-secrets` is inert against a missing vault CLI.** Not
  because of R34, but because `AllowMissing` is only checked on the
  `ErrKeyNotFound` branch (`resolve.go:531`) and never on the
  `ErrProviderUnreachable` branch (`resolve.go:550`). The flag's own help text
  ("downgrade unresolved `vault://` references") reads as though it covers this.
- **Persona B's error names an arbitrary key.** Map iteration order decides
  which of the failing secrets is reported; the run is not reproducible line
  for line. Users get one symptom out of N with no inventory.
- **`[repos.<name>.env.secrets.required]` fires for repos that are not in the
  instance.** `checkRequiredKeys` walks `cfg.Repos` (the override map), not the
  cloned repo set. The three `INFISICAL_*` keys blocked a create in a fixture
  with zero repos.
- **`niwa apply` prints the same multi-line error twice**, with two different
  prefixes (`error: applying to ...` then `apply failed for ...`).
- **`niwa status` reports nothing wrong** in a workspace that cannot create an
  instance, and `niwa status check-vault` is not a subcommand — it parses
  `check-vault` as an instance name and answers `instance "check-vault" not
  found: no instances exist in workspace` (exit 1). There is no diagnostic
  command that tells a user "here is what this workspace needs and what you
  have." OBSERVED.
- **The recommended-key warnings are emitted in nondeterministic order** across
  runs (map iteration in `warnRecommended`, `required.go:147-157`), unlike the
  required block which is explicitly sorted (`required.go:88-93`).

---

## Open Questions

1. Who owns the fix — niwa's contract or the workspace config? A workspace
   author *can* express "developer convenience" today by moving these keys from
   `required` to `recommended`. Should niwa's answer be "the public config is
   miscategorized," or does niwa owe a degraded mode regardless? The PRD
   contradiction (visibility-overlay line 462 vs line 316) means the project has
   already argued both sides in writing.
2. Should the softened behavior be keyed on *why* the value is absent? A
   plausible split: "no provider declared for this key anywhere" (Persona A —
   arguably a config-authoring error worth warning loudly about) versus
   "provider declared but unreachable right now" (Persona B — a transient host
   condition that clearly should degrade). These have different remediations and
   maybe different severities.
3. What should the SessionStart hook do on partial provisioning failure —
   fail the session, boot at the workspace root silently (today), or boot with a
   degraded-mode `additionalContext`? This needs a decision on Claude Code's
   actual non-zero SessionStart semantics, which is not documented in this repo.
4. Is widening `--allow-missing-secrets` to cover `ErrProviderUnreachable` a
   separate bug fix that should land independently of the larger design
   question? It is a two-line change and is arguably what R10 already promised.
5. Should `niwa init` gain a required-secret preflight, and should there be a
   `niwa doctor`-style command that answers "what does this workspace need and
   what can I supply?" without attempting a create?
6. Does the exclusion of the `[repos.<name>]` scope for repos not present in
   the instance count as a bug worth fixing on its own?

---

## Summary

`niwa init` succeeds for both personas and the wall is at `niwa create` /
`dispatch` / the SessionStart hook, all of which reach the single
`checkRequiredKeys` call site at `internal/workspace/apply.go:1325`; only
`create` and `apply` expose `--allow-missing-secrets`, and it is ineffective in
both persona scenarios — for Persona A because there is no `vault://` ref to
downgrade, and for Persona B because the resolver hard-fails on
`ErrProviderUnreachable` at `internal/vault/resolve/resolve.go:550` without ever
consulting the flag. Every failure leaves the workspace root and its registry
entry intact but no instance, so the user holds a well-formed, permanently
unusable workspace that `niwa status` reports as healthy. The biggest open
question is ownership: the same PRD promises both that requirement tables are
"documentation for manual secret setup" (visibility-overlay line 462) and that a
missing one aborts apply (line 316), so the project has not yet decided whether
a required miss without any vault backend is a config-authoring error or a case
niwa owes a degraded mode.
