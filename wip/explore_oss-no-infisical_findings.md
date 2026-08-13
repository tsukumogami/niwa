# Exploration Findings: oss-no-infisical

## Core Question

A niwa workspace whose declared secrets are all developer conveniences becomes
unusable when the vault backend is absent or unauthenticated. We need to figure
out where responsibility sits between niwa's `required` secret contract and a
workspace config's declaration of it, and what "loud but non-fatal" should
concretely look like along the init -> create -> apply -> dispatch path.

## Round 1

### Key Insights

- **`niwa init` succeeds for both personas.** The wall is `niwa create`,
  `niwa dispatch`, and the SessionStart hook, all of which reach the single
  `checkRequiredKeys` call site at `internal/workspace/apply.go:1325`. Init exits
  0 with a "workspace is ready" message, leaves the workspace root and registry
  entry intact, and creates no instance — so the user holds a well-formed,
  permanently unusable workspace that `niwa status` reports as healthy.
  (lead-failure-trace)

- **There are four independent hard-fail gates, not one.** Parse-time
  (`vault://` ref with no declared provider, `validate_vault_refs.go:234-239`);
  resolve-time (`ErrProviderUnreachable`, `internal/vault/resolve/resolve.go:550`);
  post-merge required-miss (`checkRequiredKeys`, governed by PRD R33/R34); and
  materialize-time `[claude.env] promote` of an absent key
  (`materialize.go:960-962`). Only the third is what R33/R34 governs. Any "loud
  but non-fatal" story has to name which gates it changes.
  (lead-required-semantics, lead-static-detection, lead-secret-necessity)

- **The two personas do not share a code path.** The OSS contributor never
  touches the vault at all: the overlay clone fails, no `vault://` ref exists, and
  the failure is a purely static declaration-without-supply contradiction that
  surfaces ~250 lines later in a function with zero vault awareness. The
  maintainer on a fresh host dies earlier, inside the resolver, and never reaches
  `checkRequiredKeys`. A fix aimed at one does nothing for the other.
  (lead-provider-absence-vs-unreachable, lead-failure-trace)

- **`--allow-missing-secrets` is inert in both scenarios.** It only downgrades
  `ErrKeyNotFound`, never `ErrProviderUnreachable` (`resolve.go:531` vs `:550`),
  so it does nothing for a missing Infisical CLI; and the OSS contributor has no
  `vault://` ref for it to act on. Its help text ("downgrade unresolved
  `vault://` references"), its R9 remediation text, and the doc comment on
  `vault.ErrProviderUnreachable` all read as though it covers this. Three doc
  comments in the codebase are stale on this point.
  (lead-required-semantics, lead-provider-absence-vs-unreachable, lead-existing-surfaces)

- **Not one of the eight declared secrets is read by niwa's clone or materialize
  path.** `git clone` runs unadorned; the GitHub API client takes its token from
  the ambient environment through `resolveGitHubToken()`, a mechanism
  `checkRequiredKeys` cannot see. The required list can therefore legitimately be
  empty. The `INFISICAL_*` trio serves only an integration suite that already
  skips cleanly, and two of the three are read by nothing in the repository at
  all — only by CI from Actions secrets. (lead-secret-necessity)

- **Hard-failing on a missing required secret is the industry norm; making
  resolution a precondition for *constructing* the environment is not.**
  secretspec defaults `required = true`, Docker Compose defaults `required: true`,
  envalid exits 1. But direnv leaves you a working shell, sops-nix defers to
  activation, Terraform blocks `plan` not `init`, and devenv recommends resolving
  per-process via `secretspec run -- cmd` rather than at shell entry. niwa's
  defect is placement, plus an escape hatch that does not escape. (lead-prior-art)

- **The overlay clone failure is completely silent** (`apply.go:990-995`). The
  error that eventually fires mentions neither the overlay, nor Infisical, nor
  the fact that a config layer went missing. (lead-provider-absence-vs-unreachable,
  lead-static-detection)

- **The escape hatch is unreachable by construction on the launch-coupled paths.**
  `dispatch` and the SessionStart hook funnel through `realProvisionInstance`
  (`instance_from_hook.go:351-423`), which never sets `AllowMissingSecrets`. Even
  a correctly-working flag could not be passed by a background worker. Whatever
  "loud but non-fatal" means, it has to be reachable without a CLI flag.
  (lead-failure-trace)

- **The contradiction is statically computable.** `config.Parse` already runs the
  mirror-image check, and the apply-time call site holds every vault bundle it
  would need to split "no mechanism exists" from "mechanism exists but the value
  is missing" — it just passes `checkRequiredKeys` only the config. For the OSS
  case no vault call happens at all, so the failure is already purely static,
  merely late and badly worded. (lead-static-detection)

- **A ready-made template for a severity ramp already exists** in the
  `.env.example` failure-policy ladder (`internal/config/env_example_policy.go`):
  four-level precedence including a global rung, plus a one-shot
  downgrade-with-audit-line flag. (lead-existing-surfaces)

- **Roughly 70% of the fix is plumbing and wording**, 30% is a genuine product
  decision: nothing today can downgrade a required miss, by R34's explicit
  design, so the stated goal requires amending R34,
  `docs/guides/vault-integration.md:178`, and
  `TestApplyAllowMissingSecretsDoesNotDowngradeRequired`. (lead-existing-surfaces)

### Tensions

- **The PRD argues both sides in writing.**
  `docs/prds/PRD-workspace-visibility-overlay.md:462` says requirement tables are
  "documentation for manual secret setup"; line 316 of the same document says a
  missing one aborts apply. The project has not decided whether a required miss
  with no vault backend is a config-authoring error or a case niwa owes a
  degraded mode.

- **Authoring-side expressiveness vs. a flag.** Peers converge on the authoring
  side (`required = false`, secretspec profiles, envalid `devDefault`) so a public
  config is usable as published. But the launch-coupled paths cannot pass a flag,
  and relying on the author assumes the author anticipated the contributor. These
  pull in opposite directions on where the fix belongs.

- **The required check is already wrong in the other direction.** `[env.files]`,
  auto-discovered env files, and the whole materialization layer supply values
  *after* `checkRequiredKeys` runs. So the check can produce false positives
  today, contradicting `PRD-workspace-visibility-overlay.md:316`. Fixing the
  placement may matter more than fixing the severity.

- **`recommended` may not survive scrutiny.** The prior-art survey found binary
  required/optional everywhere. niwa's three-level model may need its own
  justification, or may collapse to `required` + `optional` with a warning flag.

- **The documented quick-start has never satisfied the config it ships with.**
  `public/dot-niwa/README.md` says `GITHUB_TOKEN=$(gh auth token) niwa create`,
  which sets an ambient var `checkRequiredKeys` cannot see. The base config's own
  quick-start is inert with respect to its own required declaration.

### Gaps

- Claude Code's actual non-zero SessionStart semantics are not documented in this
  repo, so what the hook should do on partial provisioning failure is undecided.
- `devenv shell` behaviour on an unresolvable required secret is undocumented —
  the closest analogue to `niwa init` in the whole survey.
- Whether unauthenticated `ListRepos` returns enough for tsukumogami was not
  verified end-to-end (six public repos vs `DefaultMaxRepos = 10`), nor how a
  403 rate-limit is reported.
- `infisical run`'s actual output and exit code when unauthenticated were not
  established empirically.

### Incidental findings worth separate issues

These are real but orthogonal to the core question; they should not be folded
into this exploration's artifact.

1. **`ANTHROPIC_API_KEY` being set suppresses remote-control-on-dispatch.**
   `dispatch_remotecontrol.go:53-64` treats a non-empty key as proof that Claude
   Code Remote is impossible and skips injecting the settings flag. Since the
   private overlay vault-binds this key, the maintainer's own workspace is likely
   running with that feature silently off. Unverified end-to-end.
2. **`[claude.env] promote` writes a live GitHub PAT in cleartext** into the
   workspace-root `.claude/settings.json`. The research agent reported that file
   as mode 0644 against 0600 `.local.env` files; direct verification on this host
   showed `-rw-------` (0600), consistent with the other secret files. The
   cleartext-at-rest-in-one-more-place point stands; the world-readable claim does
   not.
3. **The per-repo required check is not scoped to cloned repos.**
   `required.go:65` iterates `cfg.Repos` wholesale, so
   `[repos.<name>.env.secrets.required]` fires even for a repo excluded from the
   current apply.
4. **`niwa apply` prints the same multi-line error twice** with two different
   prefixes.
5. **Nondeterministic reporting.** Persona B's error names an arbitrary failing
   key (map iteration order), and `warnRecommended` emits in nondeterministic
   order while the required block is explicitly sorted.
6. **No diagnostic command answers "what does this workspace need and what do I
   have."** `niwa status --audit-auth` needs an instance that `create` deletes on
   the failure path; `--audit-secrets` reads only `.Values` and never the
   requirement tables, printing "No *.secrets entries found." for a
   declaration-only config.

### Decisions

- Both personas are in scope, both fix surfaces (niwa mechanism and tsukumogami
  config) are in play, and the command scope covers the whole
  init -> create -> apply path plus `niwa dispatch`.
- CI/automation was explicitly not selected as a distinct persona.
- Infisical as a vendor choice is out of scope.

### User Focus

Pending — narrowing question posed at end of round 1.

## Round 2

### Key Insights

- **A non-zero exit does not suppress a SessionStart hook's output.** Verified
  empirically against Claude Code 2.1.231 on exit 0, 1, and 2:
  `hookSpecificOutput.additionalContext` reaches the model in every case, stderr
  reaches it in none, and even `continue: false` fails to stop the session. So
  "fail the session" is not an implementable option, and niwa's current
  exit-1-with-no-JSON is the single shape that tells the worker nothing at all.
  (lead-sessionstart-semantics)

- **The hook-path remedy is an emit, not an exit-code change.** Always write the
  JSON, exit 0, and render the unresolved-key inventory into the
  `additionalContext` string itself. `Reporter.Warn` output is a black hole on
  this path even when provisioning fully succeeds, so the warning has to travel
  inside the context payload. `niwa dispatch` differs usefully: it provisions
  before launching, so it never strands a worker, and its notice can ride the
  existing niwa-authored prompt prefix. (lead-sessionstart-semantics)

- **Strictness should be a config action, not primarily a flag — which dissolves
  round 1's reachability problem.** A `config.Action` (`"warn"` / `"fail"`) on
  `[workspace]` and `[global]` is already loadable by `realProvisionInstance`,
  the very function round 1 identified as unable to receive a CLI flag. Better,
  `WorkspaceOverlay` has no `Workspace` field, so a private overlay layer
  *structurally cannot* change the strictness a public contributor sees. That
  safety property falls out of the existing schema rather than needing design.
  (lead-annotation-and-strict)

- **The annotation cannot key off emptiness; the resolver needs an explicit
  unresolved marker.** A deliberate `FOO = ""` and a downgraded vault miss
  produce byte-identical zero `MaybeSecret` values. Carrying an explicit
  `UnresolvedReason` also lets the required check implement strict-when-reachable
  without plumbing vault bundles into it. (lead-annotation-and-strict)

- **The `.local.env` annotation is load-bearing, not cosmetic.** Omitting a key
  instead of writing `KEY=` converts a silent empty promote into a hard worktree
  failure via `readCloneEnvOutput`, so the `# niwa: unset <KEY>` comment must be
  machine-read on the way back in. This is gate 4's real cost.
  (lead-annotation-and-strict)

- **The env writer makes the annotation cheap.** It is a 106-line leaf package
  with a single call site, and the file is already fully rewritten and key-sorted
  on every apply — so idempotency, ordering, and clean removal once a key
  resolves all come free. (lead-annotation-and-strict)

- **Two of the three incidental findings were refuted on verification.**
  (lead-incidental-verify)
  - *Remote-control suppression: REFUTED.* The API-key gate is real and behaves
    as described, but it is deliberate — specified in the PRD, design doc, and
    user guide, covered by three tests including an end-to-end stderr assertion,
    and it prints its own warning at `dispatch.go:376-379`. It is inactive on the
    maintainer's host on two independent grounds: `remote_control_on_dispatch` is
    not set at all, and the vault-bound `ANTHROPIC_API_KEY` lands only in
    unsourced `.local.env` files, so it is never in the process environment.
  - *`GH_TOKEN` promotion: REFUTED.* niwa itself reads `GH_TOKEN`
    (`internal/cli/token.go:16`); the promoted value is the actual delivery path
    into sessions rather than redundancy (no shell profile exports it, yet the
    session value hash-matches `settings.local.json`); every settings file is
    0600 by code with the 0644 case explicitly fixed at `materialize.go:25-27`;
    the true workspace-root settings carries no `env` block at all; and the
    promoted fine-grained PAT is *narrower* than the classic full-`repo` keyring
    token that removal would fall back to. The round-1 credential-exposure claim
    does not survive. Only the known `promote` intolerance
    (`materialize.go:958-963`) remains a live concern.
  - *Unauthenticated repo listing: CONFIRMED, with a sharper finding.* A live
    unauthenticated call returned exactly the six public repos, well under
    `DefaultMaxRepos = 10`. But a 403 collapses to `GitHub API returned status
    403 for org "tsukumogami"` with no rate-limit wording, no typed error, and no
    test coverage — in a package where `GetRepo` and the tarball fetchers all do
    better. And the org now holds exactly 10 repos against a threshold of 10, so
    the next repo created breaks `apply` for every maintainer.

### Tensions

- **Gate 4 pulls against the omit-don't-write-empty rule.** Omission is right for
  downstream consumers but breaks `readCloneEnvOutput`, which parses these files
  back. Resolved only by making the comment machine-readable — which means the
  annotation format becomes a compatibility surface, not just human text.

- **Round 1's flag framing was wrong, and the correction is load-bearing.** The
  "escape hatch unreachable from dispatch and the hook" finding drove the
  rejection of flag-shaped remedies. A config action reaches those paths, so the
  argument for the largest remedy shape rests on placement and prior art, not on
  reachability.

### Gaps

- What a *total* provisioning failure should say and do. A stranded worker lands
  at a workspace root containing no repos of its own but other sessions'
  instances plus a CLAUDE.md explaining how to enumerate them.
- Whether an `ANTHROPIC_API_KEY` genuinely precludes Claude Code Remote. This
  repo cannot settle it and no spike ever tested it; the gate rests on an
  untested premise. Low stakes given the gate is documented and warns.

### Decisions

See `wip/explore_oss-no-infisical_decisions.md`. Round 2 added: the remedy shape
(consumption-placed, strict-when-reachable), the `.local.env` comment annotation
requirement, and the `--strict` enforcement switch.

### User Focus

Confirmed the consumption-placed remedy. Added two requirements: annotate
unresolved keys as comments in the generated `.local.env` as a second loud-but-
non-fatal channel, and provide a `--strict`-style switch that enforces resolution
and fails the command.

## Accumulated Understanding

The reported symptom is misattributed: `niwa init` works. What fails is every
subsequent operation that materializes an instance, and the failure arrives
through four separate gates that a single fix cannot close. Two of those gates
are reached by two different users for two different reasons, and the one
documented escape hatch is inert against both while claiming otherwise in its own
help text.

Underneath the mechanics sits an unresolved product question the project has
already argued both sides of in its own PRD: whether a public base config
declaring required keys that only a private overlay can satisfy is a config
authoring error, or a case niwa owes a degraded mode. The evidence tilts toward
"both, at different layers" — the tsukumogami config is genuinely miscategorized
(no declared secret is load-bearing for cloning or materializing, so the required
list can be empty and the `INFISICAL_*` keys do not belong in a public config at
all), *and* niwa's placement of the check is out of step with every comparable
tool, none of which makes secret resolution a precondition for constructing the
environment.

Round 2 settled the shape. The remedy is consumption-placed and
strict-when-reachable: materialization proceeds with unresolved keys omitted from
the generated `.local.env` rather than written empty, annotated there as comments
naming the key and its declared description, reported loudly on the terminal, and
exiting 0. `required` stays fatal only when a provider is configured and reachable
and the key is simply absent — so the maintainer's steady state is exactly as
strict as it is today. Strictness is restored on demand through a `config.Action`
(`"warn"` / `"fail"`) on `[workspace]` and `[global]`, with a CLI flag as the
interactive front door.

Three mechanisms make it work. The resolver must carry an explicit
`UnresolvedReason`, because a downgraded miss and a deliberate empty string are
byte-identical today; that marker also lets the required check implement
strict-when-reachable without plumbing vault bundles into it. The `.local.env`
comment must be machine-readable, because `readCloneEnvOutput` parses these files
back and a bare omission would convert a silent empty promote into a hard
worktree failure. And on the SessionStart path the fix is an emit rather than an
exit-code change: `additionalContext` reaches the model regardless of exit code
while stderr never does, so the inventory has to travel inside the context
payload.

The config-action placement also dissolves round 1's reachability objection —
`realProvisionInstance` already loads both rungs — and yields a safety property
for free: `WorkspaceOverlay` has no `Workspace` field, so a private overlay
cannot change the strictness a public contributor experiences.

Separately, the tsukumogami base config is genuinely miscategorized. No declared
secret is load-bearing for cloning or materializing, so the required list can be
empty, and the `INFISICAL_*` keys — two of which nothing in the repository reads —
do not belong in a public config at all. Because niwa cannot explain where the
values were supposed to come from without disclosing a private repository's
existence, the base config's own key descriptions and README carry the entire
explanatory burden for a contributor.
