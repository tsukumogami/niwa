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

The most defensible narrow fix — widening the resolver's unreachable-provider
branch to honour the existing flag — is a small change with clear precedent, but
it cannot stand alone: it does nothing for the OSS contributor, and the
launch-coupled `dispatch` and SessionStart paths cannot pass a flag at all. That
pushes the durable answer toward a config-level or default-level severity
mechanism rather than a new CLI switch, with the `.env.example` failure-policy
ladder as the in-repo template for how such a mechanism is already shaped.
