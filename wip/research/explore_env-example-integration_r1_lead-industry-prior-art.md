# Research: industry prior art on `.env.example`

## Docker Compose

- Reads `env_file:` directive (explicit path) and `environment:` block (inline vars or shell pass-through).
- Does NOT read `.env.example` — it's documentation. Missing vars under `${VAR}` interpolation warn from v2.24.0+ but otherwise pass undefined.

## Vercel / Railway / Fly.io / Heroku

- None auto-detect `.env.example` during onboarding. Variable setup is manual: form entry or paste-raw-editor.
- No stub-vs-real heuristic at the platform level; the deployer's responsibility.

## Turborepo

- `turbo.json` declares `globalEnv` / `env` for cache-hashing purposes — it cares which env vars affect build output, not values.
- Does not load `.env` files automatically; apps/frameworks load their own. `.env.example` is developer-facing documentation only.

## Nx

- Loads `.env.<task>.<config>` priority files when explicitly configured in `project.json` targets.
- No `.env.example` integration; treats `.env` as runtime input, not schema.

## dotenv CLI + direnv

- `dotenv` loads `.env` at process start; no special `.env.example` handling.
- `direnv` convention: `.envrc.sample` (committed) + `.envrc.local` (gitignored). Manual copy step; no automatic read.

## Stub-vs-default convention in `.env.example`

No consensus. Observed patterns:
- **Empty** (`KEY=`): signals "user must supply".
- **Placeholder text** (`KEY=changeme`, `KEY=your-api-key`): explicitly non-functional.
- **Example-shaped values** (`KEY=sk_test_abc`, `KEY=postgres://user:pass@localhost/db`): shows format, sometimes functional.
- **Functional defaults** (`KEY=true`, `KEY=3000`): real defaults.

Validation tools (env.dev, dotenvx) scan for "changeme"/weak patterns but there's no widely-adopted spec. The closest to a standard is: if the value is obviously a placeholder word or empty, treat as "user must supply"; otherwise it's a default. Each team picks its own conventions.

## Takeaway for niwa

Greenfield. No established tool consumes `.env.example` declaratively — it's treated as documentation everywhere I looked. That means niwa has flexibility to define its own semantics, but also no precedent to cite as "this is how it's done".

The closest conceptual match is **direnv's `.envrc.sample` / `.envrc.local` split**: a committed example with stubs, a gitignored local file with real values. niwa's opportunity is to automate that split — read the example, merge it with workspace-declared vars + vault-resolved secrets, write the combined result to a gitignored `.local.env`. That model maps cleanly to the existing niwa materialization pipeline.

## Sources

- https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/
- https://turborepo.dev/docs/crafting-your-repository/using-environment-variables
- https://nx.dev/docs/guides/tips-n-tricks/define-environment-variables
- https://env.dev/guides/dotenv/
- https://direnv.net/
