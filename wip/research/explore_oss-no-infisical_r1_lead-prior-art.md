# Lead: How do comparable developer tools handle declared-but-unavailable secrets on first run, and what do they treat as fatal versus loud-but-survivable?

Round 1 research, exploration `oss-no-infisical`. All sources public and cited inline.

## Findings

### The headline correction

The framing going in was "is niwa's hard-fail an outlier?" The answer from the survey is
**no, hard-failing on a missing *required* secret is the majority behaviour** — but every peer
tool that does it differs from niwa on two axes that turn out to matter more than the fail/warn
choice itself:

1. **Where in the flow the check happens.** No surveyed tool blocks *environment construction*
   on secret resolution. They block the *process that consumes the secret*. The setup step
   (clone, install, enter shell) survives; the run step fails.
2. **Who decides that a given secret is required.** In every tool with a required/optional
   distinction, `required` is a per-secret authoring decision that the config author is
   expected to relax for development conveniences — and the tools ship affordances (`default`,
   `devDefault`, `required = false`, profiles) specifically so that a public config can declare
   a secret without making it a barrier to entry.

niwa's `--allow-missing-secrets` flag that deliberately does not downgrade `required` has no
analogue in the survey. Where tools ship an escape hatch, the escape hatch works.

---

### direnv — the sharpest "loud but survivable" model

direnv's stdlib splits every loader into a strict and a permissive variant, distinguished by an
`_if_exists` suffix. From the stdlib source
(<https://github.com/direnv/direnv/blob/master/stdlib.sh>):

```sh
dotenv() {
  ...
  if ! [[ -f $path ]]; then
    log_error ".env at $path not found"
    return 1
  fi
}

dotenv_if_exists() {
  ...
  if ! [[ -f $path ]]; then
    return
  fi
}
```

Same for `source_env` / `source_env_if_exists` / `source_up_if_exists` ("If one is not found,
nothing happens" — <https://direnv.net/man/direnv-stdlib.1.html>).

Most relevant to niwa: `env_vars_required` exists as a *separate, explicit assertion*, and its
own docstring names exactly the public-config-plus-private-file pattern niwa is in:

```sh
# Usage: env_vars_required <varname> [<varname> ...]
#
# Logs error for every variable not present in the environment or having an empty value.
# Typically this is used in combination with source_env and source_env_if_exists.
#
# Example:
#
#     # expect .envrc.private to provide tokens
#     source_env .envrc.private
#     # check presence of tokens
#     env_vars_required GITHUB_TOKEN OTHER_TOKEN
```

Two things follow. First, requiredness is asserted by a *call you write*, not inferred from the
declaration — declaring where a value comes from and asserting that it must exist are separate
acts. Second, when `env_vars_required` returns 1 the `.envrc` fails, direnv logs the error, and
**the environment is not loaded — but your shell still works and you are still in the
directory**. The failure is scoped to the environment layer, not to the ability to be there at
all. That is the structural shape of "loud but not fatal."

Source: <https://github.com/direnv/direnv/blob/master/stdlib.sh>,
<https://direnv.net/man/direnv-stdlib.1.html>

---

### SecretSpec / devenv — the closest prior art to niwa's problem, by a wide margin

SecretSpec (cachix, used by devenv) exists to solve precisely niwa's stated pain: a committed,
public declaration of *what* secrets a project needs, decoupled from *where* any individual
developer gets them.

The announcement post states the three-way split explicitly:

> "WHAT - Which secrets does your application need? (DATABASE_URL, API_KEY) HOW - Requirements
> (required vs optional, defaults, validation, environment) WHERE - Where are these secrets
> stored?"

— <https://devenv.sh/blog/2025/07/21/announcing-secretspec-declarative-secrets-management/>

And the follow-up post gives the rationale for committing declarations while excluding values:

> "Configuration describes behavior. It belongs in git, code review, bug reports, and developer
> machines. A secret grants authority. It needs restricted access and independent rotation."
> ... "Putting both in one file couples different lifecycles and audiences."

— <https://secretspec.dev/blog/secrets-dont-belong-in-config/>

Schema, from the configuration reference (<https://secretspec.dev/reference/configuration/>):

| Field | Type | Notes |
|---|---|---|
| `description` | string | human-readable purpose |
| `required` | bool or table | **defaults to `true`**; table form supports `at_least_one`/`exactly_one` presence groups |
| `default` | string | fallback value; **cannot coexist with `required = true`** |
| `providers` | array[string] | provider aliases forming a **fallback chain** |
| `generate` | bool/table | auto-create missing values |
| `prompt` | bool | prompt the operator during `run` if missing (0.19+) |

Example declaration:

```toml
[profiles.default]
DATABASE_URL = { description = "PostgreSQL connection string", required = true }
REDIS_URL    = { description = "Redis connection string", required = false }
```

Behaviour:

- `required` defaults to `true`, and `check`, `export`, and `run` **exit non-zero when a
  required secret is missing, "so they work as a CI gate."**
- Optional secrets that are absent do not block execution; in the Rust SDK they surface as
  `Option<String>` — "enabling type-safe handling of missing values without runtime failures."
- `secretspec check` is a *separate verification command* with `--json` and `--explain`
  resolution reports that show status without exposing values, e.g.
  `DATABASE_URL ok source keyring://` / `STRIPE_KEY MISSING required`.
- Profiles let the same secret be `default = "postgresql://localhost/dev"` in development and
  required-from-a-managed-provider in production.
- `providers = ["vault", "keyring", "env"]` is a per-secret ordered fallback chain — a provider
  being unavailable is not the same event as a value being absent.

Crucially, devenv's own integration guidance is to **not resolve secrets at shell-entry time at
all**:

> "Load secrets at runtime and expose them only to the processes that need them" via
> `secretspec run -- npm start` — because it "Keeps secrets out of your shell environment" and
> "Reduces exposure of sensitive data."

— <https://devenv.sh/integrations/secretspec/>

So the tool whose problem statement most exactly matches niwa's also (a) hard-fails on required,
(b) defaults `required` to true, and (c) sidesteps niwa's actual pain by never putting the
resolution on the environment-creation path.

*Ambiguity noted:* the devenv integration docs do not state what happens on `devenv shell` entry
when a required secretspec secret is unresolvable. Undocumented; would need testing.

---

### Doppler — the richest vocabulary for degraded mode

Doppler is the only surveyed tool with a fully worked-out offline story, and its flag naming is
the most transferable artefact in this survey. From `pkg/cmd/run.go`
(<https://github.com/DopplerHQ/cli/blob/master/pkg/cmd/run.go>), verbatim help strings:

- `--fallback` — "path to the fallback file. encrypted secrets are written to this file after
  each successful fetch. secrets will be read from this file if subsequent connections are
  unsuccessful."
- `--fallback-only` — "read all secrets directly from the fallback file, without contacting
  Doppler. secrets will not be updated. (implies --fallback-readonly and --no-liveness-ping)"
- `--offline` — registered as a literal **alias for `--fallback-only`**
- `--fallback-readonly` — "disable modifying the fallback file. secrets can still be read from
  the file."
- `--no-fallback` — "disable reading and writing the fallback file (implies --no-cache)"
- `--no-exit-on-write-failure` — "do not exit if unable to write the fallback file"
- `--no-exit-on-missing-only-secrets` — "do not exit on missing secrets via --only-secrets"

Docs confirm the automatic behaviour: on `doppler run` "the CLI automatically creates a fallback
file containing an encrypted snapshot of the current secrets in JSON format," and "The CLI will
fetch the latest version of the secrets if the Doppler API is reachable and a valid token is
available." (<https://docs.doppler.com/docs/automatic-fallbacks>)

Three patterns worth stealing:

1. **`--offline` as an alias for the mechanism flag.** The mechanism is "read from the cached
   snapshot"; `--offline` is the word users reach for. Doppler ships both and makes them the
   same flag.
2. **`--no-exit-on-<specific-failure>` naming.** Each escape hatch names the exact failure it
   downgrades. There is no blanket "allow missing" flag with carve-outs — which is precisely the
   shape of niwa's `--allow-missing-secrets` problem.
3. **Backend-unreachable and value-missing are different events with different handling.** The
   fallback file addresses the first; `--no-exit-on-missing-only-secrets` addresses the second.

---

### dotenvx — default is warn, `--strict` opts *into* fatal

dotenvx inverts niwa's default. Missing files produce a diagnostic and execution continues;
`--strict` is what makes it fatal:

> `--strict` causes dotenvx to "Exit with code 1 if any errors are encountered - like a missing
> .env file or decryption failure."

— <https://dotenvx.com/docs/cli/run-strict/>

Without `--strict`, validation errors are reported without stopping the command. It also ships
per-error-code suppression: `dotenvx -f .env.missing --ignore=MISSING_ENV_FILE run -- yourcommand`
(<https://github.com/dotenvx/dotenvx/issues/484>). The error codes are named and stable
(`MISSING_ENV_FILE`), which makes the escape hatch granular without being a blanket bypass.

---

### Docker Compose — exact vocabulary match for the schema change niwa needs

Compose's `env_file` long syntax carries a `required` boolean, introduced in Compose 2.20.0:

```yaml
env_file:
  - path: ./default.env
    required: true # default
  - path: ./override.env
    required: false
```

With `required: false`, "Compose silently ignores the entry" when the file is absent; with
`required: true` (the default) the missing file is an error and validation fails.
(<https://docs.docker.com/reference/compose-file/services/#env_file>)

This is the same three-word vocabulary niwa already has (`required` / `recommended` /
`optional`) — confirming the naming is idiomatic, and that the check is a **config-parse-time**
concern in Compose's model.

---

### 1Password `op run` — hard fail, no documented degraded mode

`op run` documents only `--env-file`, `--no-masking`, and `--environment` (beta). **The docs do
not specify what happens when a secret reference cannot be resolved**, and no flag exists to
continue on failure or skip missing secrets.
(<https://www.1password.dev/cli/reference/commands/run/>)

Observed behaviour reported by users is an error and non-zero exit (e.g. `[ERROR] Missing
credentials DB_USER and DB_PASSWORD`), but this is not documented behaviour and should be
treated as ambiguous.

---

### Infisical — hard fail, cache-based offline story, historically fragile

The `infisical run` reference documents `--watch`, `--project-config-dir`, `--command`,
`--projectId`, `--token`, `--expand`, `--include-imports`, `--env`, `--secret-overriding`,
`--tags`, `--path`. It documents **no offline mode, no fallback, and no behaviour when
unauthenticated**. (<https://infisical.com/docs/cli/commands/run>)

The offline story is an implicit keychain cache rather than a declared mode, and it has been
fragile: issue #1639 ("Offline functionality not working as expected", opened 2024-04-01,
closed via PR #1757) reported that after a successful online run, disabling wifi and re-running
`infisical run` failed, because the CLI had stopped saving keys to local storage.
(<https://github.com/Infisical/infisical/issues/1639>)

Related friction: `infisical run` began requiring `.infisical.json` to exist even when
authenticating purely via `--token` between 0.36.11 and 0.36.17
(<https://github.com/Infisical/infisical/issues/3249>), and users have reported "must be logged
in" errors despite passing a token (<https://github.com/Infisical/infisical/issues/2286>).

For niwa this matters directly: the current provider does not offer a documented "run degraded"
contract, so any degraded mode niwa wants must be implemented in niwa's own resolution layer,
above the provider — it cannot be delegated to the Infisical CLI.

---

### mise — capability present, failure semantics undocumented

mise declares env files via `env._.file` (dotenv/JSON/YAML/TOML), `env._.path`, and
`env._.source`, with `redact` for sensitive values.
(<https://mise.jdx.dev/environments/>) Secrets support is via fnox (recommended), sops
(experimental), and age (experimental).
(<https://mise.jdx.dev/environments/secrets/>)

**The mise documentation does not state what happens when a declared `env._.file` is missing,
nor what happens when sops/age decryption fails.** Search surfaced a `require_env_file` boolean
setting described as erroring when a configured env file is missing, defaulting to `false` —
i.e. permissive by default — but this was not confirmed against a primary doc page and should
be verified before being cited. Treat mise's semantics as **unverified**.

---

### Validation-layer tools — required/optional with an explicit dev carve-out

**envalid** (<https://github.com/af/envalid>): by default, if any required env var is missing or
invalid, envalid "will log a message and call `process.exit(1)`." Providing a `default`
"effectively makes the env var optional." Most relevant: **`devDefault`** — "a fallback value to
use only when NODE_ENV is not 'production', which is handy for env vars that are required for
production environments but optional for development and testing." That is a first-class
encoding of exactly niwa's situation: same declaration, different requiredness by context.

**t3-env** (<https://env.t3.gg/docs/customization>): ships `skipValidation`, documented as an
opt-out "during linting or if you're building with Docker and not all environment variables are
present when building the image," with the idiom `skipValidation: !!process.env.SKIP_ENV_VALIDATION`.
The docs are explicit that skipping "is not encouraged and will lead to your types and runtime
values being out of sync" — the escape hatch exists, is named, and is documented as a
known-lossy mode rather than hidden.

**dotenv-linter** (<https://github.com/dotenv-linter/dotenv-linter>): `dotenv-linter diff .env
.env.example` compares files and reports missing keys. This is the `.env.example` convention's
enforcement mechanism — a *separate lint step*, not a gate on the app starting.

---

### Contrast cases where hard failure is correct

**Terraform**: a variable without a `default` is required; Terraform "prompts the user to assign
a value before it generates a plan." Adding `default` makes it optional.
(<https://developer.hashicorp.com/terraform/language/values/variables>) Note the shape: the
missing value blocks *plan generation* — the operation that would mutate infrastructure — and
the tool's first move is to *ask*, not to abort.

**Pulumi**: `config.require()` raises "Missing required configuration variable" when the value
is unset for the stack. Requiredness is asserted at the *access site* in program code, not in a
manifest — closer to direnv's `env_vars_required` than to a declaration-driven gate.
(<https://www.pulumi.com/answers/fixing-missing-required-configuration-variable/>)

**GitHub Actions**: the opposite extreme. A missing or misnamed secret silently resolves to an
empty string; there is no built-in required-secret assertion, and the documented workaround is a
manual guard (`if: secrets.MY_SECRET`, or `[ -z "${{ secrets.X }}" ]`).
(<https://github.com/orgs/community/discussions/61551>,
<https://github.com/orgs/community/discussions/50912>) This is the failure mode that justifies
required-checks existing at all — silent empty-string substitution is worse than either warning
or failing.

**sops-nix**: enforces a hard separation between phases. "It is not possible to use secrets at
evaluation time of nix code because sops-nix decrypts secrets only in the activation phase."
Evaluation validates configuration; a missing age key fails at *activation*, not at eval.
(<https://github.com/Mic92/sops-nix>) The phase split is the same principle as everything above:
the structural step succeeds without the secret; the step that needs the plaintext is the one
that fails.

---

## Implications

**1. The check is in the wrong place, more than it is the wrong severity.**
Not one surveyed tool makes secret resolution a precondition for *constructing* the environment.
direnv gives you a working shell with a failed `.envrc`. sops-nix evaluates the config fine and
only fails at activation. devenv's own recommendation is to keep secrets out of the shell
entirely and resolve them per-process via `secretspec run -- cmd`. Terraform blocks `plan`, not
`init`. If niwa moves secret resolution off `niwa init` and onto the operations that actually
consume the values (or onto an explicit `niwa env` / materialization step), the required/optional
severity question mostly stops mattering — a contributor can create the workspace, clone the
repos, and get a loud report of what they cannot resolve.

**2. There is a strong convergent vocabulary, and niwa should adopt rather than invent.**
Ranked by fit:
- `required = true|false` per declaration — Docker Compose `env_file`, secretspec. niwa's
  existing three-way `required`/`recommended`/`optional` is already idiomatic; the gap is that
  `required` is unnegotiable rather than a default an author can relax.
- `_if_exists` suffix — direnv. Good for file/source-shaped things, less so for a TOML schema.
- `--offline` as an alias for the mechanism flag — Doppler.
- `--strict` as opt-in fatal, with permissive default — dotenvx. The inverse of niwa's default.
- `--no-exit-on-<specific-failure>` — Doppler. The most important naming lesson: **an escape
  hatch should name the exact failure it downgrades.** `--allow-missing-secrets` that does not
  allow missing required secrets is exactly the flag Doppler's naming convention prevents.
- `skipValidation` / `SKIP_ENV_VALIDATION` — t3-env, documented as knowingly lossy.
- `devDefault` — envalid. Same declaration, requiredness varies by context.

**3. Provider-unavailable and value-missing should be separate events.**
Doppler distinguishes API-unreachable (→ fallback file, transparent) from secret-missing (→
`--no-exit-on-missing-only-secrets`). secretspec's per-secret `providers = [...]` chain treats a
provider being unusable as a reason to try the next one, not as a failure. niwa currently
collapses "no vault provider configured / not authenticated" into "required secret unresolvable,"
which is what turns a public config into an unusable one for a contributor. Separating them
gives the honest message: *the vault backend is not available; N required secrets could not be
resolved; here is what will not work.*

**4. The public-declaration / private-resolution split is a named, solved pattern.**
secretspec's what/how/where decomposition and its blog framing ("Configuration describes
behavior. It belongs in git... A secret grants authority.") is the exact architecture niwa is
groping toward with public workspace config + private overlay. Worth citing directly in whatever
design doc comes out of this — including the constraint that `default` and `required = true`
cannot coexist, which is a clean way of saying "if you gave it a fallback, it was never
required."

**5. Profiles are how peers express "required in CI, optional for a drive-by contributor."**
secretspec profiles and envalid's `devDefault` both encode context-dependent requiredness without
duplicating declarations. If niwa wants a maintainer's workspace to hard-fail while a
contributor's degrades, a profile/context axis is the established shape — not a global flag.

---

## Surprises

- **Hard-failing on a missing required secret is the norm, not the outlier.** secretspec defaults
  `required = true` and exits non-zero from `check`, `export`, and `run`. envalid calls
  `process.exit(1)`. Docker Compose defaults `required: true`. The premise that niwa is unusual
  in *what* it does is wrong; it is unusual in *when* it does it and in shipping an escape hatch
  that does not escape.
- **devenv, whose problem statement matches niwa's most closely, recommends not putting secrets
  in the environment at all.** The guidance is `secretspec run -- npm start`, explicitly to keep
  secrets out of the shell. That is a stronger structural claim than "warn instead of fail."
- **Doppler registers `--offline` as a literal alias for `--fallback-only`** rather than as a
  separate mode. Small detail, good instinct: the mechanism name and the user-intent name both
  work.
- **`op run` and `infisical run` document no failure semantics whatsoever** for unresolvable
  references. The most widely used secret-injection CLIs simply do not specify this. niwa cannot
  lean on provider behaviour here.
- **direnv's `env_vars_required` docstring literally describes niwa's topology** — public
  `.envrc` sourcing a private `.envrc.private`, then asserting the tokens exist. It arrives at
  the same answer this exploration is reaching for: declaring the source and asserting
  requiredness are separate acts, and a failed assertion degrades the environment layer without
  taking the shell with it.
- **`.env.example` has no runtime enforcement anywhere.** Its enforcement is a lint step
  (`dotenv-linter diff`) or an app-boot validator (envalid, t3-env). The convention's whole point
  is that the example file is committed and harmless, and the check is deliberately elsewhere.

---

## Open Questions

1. **mise's actual behaviour on a missing `env._.file` or failed sops/age decryption is
   undocumented.** A `require_env_file` setting defaulting to `false` surfaced in search but was
   not confirmed against a primary doc page. Needs verification against the mise source before
   citation.
2. **What does `infisical run` actually print and exit with when unauthenticated?** Undocumented.
   This is the concrete behaviour niwa's provider layer wraps today, so it should be established
   empirically rather than assumed.
3. **What does `devenv shell` do when a required secretspec secret is unresolvable?** The
   integration docs are silent. This is the single closest analogue to `niwa init` in the whole
   survey and its answer would be decisive.
4. **Does `op run` distinguish "not signed in" from "reference not found"?** Undocumented; would
   determine whether the provider-unavailable / value-missing split has precedent in the
   1Password model too.
5. **Does niwa's `recommended` level have a peer analogue?** The survey found binary
   required/optional everywhere (Compose, secretspec, envalid, Terraform). A three-level model
   may need its own justification, or may collapse to `required` + `default`/`optional` with a
   warning-on-absent flag.
6. **Should the escape hatch be a flag, a profile, or a config default?** Doppler and dotenvx
   use flags; secretspec and envalid use per-declaration/profile authoring. Flags are
   discoverable but blunt; authoring is precise but requires the config author to have
   anticipated the contributor. The contributor-cloning-a-public-config case argues for the
   authoring side (so the public config is usable as published), with a flag as backstop.

---

## Summary

Hard-failing on a missing *required* secret is the norm, not the outlier — secretspec defaults
`required = true`, Docker Compose defaults `required: true`, and envalid exits 1 — but no
surveyed tool makes secret resolution a precondition for *constructing* the environment: direnv
leaves you a working shell, sops-nix defers to activation, Terraform blocks `plan` not `init`,
and devenv explicitly recommends resolving per-process via `secretspec run -- cmd` rather than at
shell entry. The implication is that niwa's real defect is placement plus an escape hatch that
does not escape: peers converge on `required = false` / `default` at the declaration site,
`--offline` and `--fallback-only` for a degraded backend, and Doppler's `--no-exit-on-<specific-failure>`
naming discipline in which a flag names the exact failure it downgrades — which is precisely what
`--allow-missing-secrets`-that-excludes-required violates. The biggest open question is what
`devenv shell` actually does when a required secretspec secret is unresolvable, since that is the
closest analogue to `niwa init` in the entire survey and its answer would be decisive.
