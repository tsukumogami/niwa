# Research: ecosystem breadth for `.env.example`

## Ecosystem-by-ecosystem

**Node.js / JavaScript (the trigger case)**
- `.env.example` is the dominant convention. Almost every
  production-grade Node starter ships one.
- Syntax in the wild: `KEY=VALUE`, quoted strings (single or
  double), inline comments, empty placeholders. Variable
  expansion (`${FOO}`) is occasional — dotenv-expand / dotenv-flow
  users.
- Tooling: `dotenv`, `dotenv-flow`, `dotenv-safe`, Next.js built-in
  loader. Some tools enforce `.env.example` as a schema (dotenv-safe
  refuses to load `.env` if keys missing relative to the example).

**Python**
- `.env.example` appears but is not universal. Flask / FastAPI
  tutorials sometimes ship one; Django / Poetry tend to use
  `.env` + documentation in the README.
- `python-dotenv` is the common loader. It doesn't consume
  `.env.example` directly.
- Many Python projects prefer `pyproject.toml` env sections or
  settings modules over dotenv.

**Ruby (Rails)**
- `dotenv-rails` does ship a `.env.example` convention for some
  starters, but it's not as universal as Node. Rails credentials
  (the preferred modern path) uses encrypted YAML instead —
  `.env.example` tends to appear in older apps.

**Rust**
- `dotenvy` exists and is common; `.env.example` less so. Rust
  apps more often embed defaults in code or use config crates
  (`config`, `figment`).

**Go**
- `godotenv` exists but the community largely uses `os.Getenv` +
  CLI flags or structured config libs (`viper`). `.env.example`
  shows up occasionally but is nowhere near the Node norm.

**Elixir / Phoenix**
- `runtime.exs` + `config/runtime.exs` + `System.get_env` is the
  canonical path. `.env.example` is uncommon; the runtime config
  approach supersedes it.

**Monorepos (Nx, Turborepo, Bazel)**
- Per-package `.env.example` is a real pattern when the packages
  are Node/JS. The monorepo tooling itself (turbo.json,
  project.json) declares which env vars matter for build-cache
  invalidation, but doesn't consume `.env.example`.

**Dockerfile `ENV`**
- Baseline defaults inside container images. Sometimes serves the
  "defaults" role that `.env.example` plays elsewhere, but it's a
  different mechanism (baked into the image, not a separate file
  at repo root).

## Distinguishing stubs from defaults

Common patterns seen across repos:

- **Empty values** (`KEY=`): "user must supply; no safe default".
- **Placeholder text** (`KEY=changeme`, `KEY=your-api-key`): clearly
  non-functional.
- **Example-shaped values** (`KEY=sk_test_Y2FyaW5...`): sometimes a
  real test key (safe to commit), sometimes a stub.
- **Functional defaults** (`KEY=3000`, `KEY=false`, `KEY=localhost`):
  real values that work out of the box.

There's no community-wide spec. Each repo picks its own.

## Scope verdict for niwa v1

**Who ships `.env.example` today, among niwa's actual users?**
Based on the tsukumogami + codespar evidence: codespar is Node/Next.js,
codespar/codespar is Node, codespar/codespar-web is Next.js. Both
ship `.env.example` in the Node style.

**Is a Node-centric v1 reasonable?** Yes. It covers the 100% of
the trigger case and matches where the convention is strongest.
Python, Ruby, and Rust support can be added later without
rearchitecting — the core merge semantics don't change by
ecosystem. The parser's syntax coverage is what expands.

**Required syntax features for v1 to feel un-broken in Node:**
- `KEY=VALUE` basic lines.
- `# comment` full-line comments (common in `.env.example` docs).
- Double-quoted values with basic escape sequences (`\n`, `\t`,
  `\"`).
- Single-quoted values (literal, no escapes).
- `export KEY=VALUE` prefix (rare but harmless to support).

**Deferrable:**
- Variable expansion (`${FOO}`, `${FOO:-default}`) — used by
  dotenv-expand but rare in `.env.example` stubs. Add later if
  users ask.
- Multi-line quoted values (backslash continuations, triple
  quotes) — even rarer in `.env.example` files.
- Python-specific or Ruby-specific syntax quirks.

## Takeaway

v1 scope: **Node.js-style `.env.example` files, parsed with a
niwa-dedicated function covering quoted values + comments +
export prefix.** No vendored dependency. Explicit non-goal:
variable expansion, multi-line, non-Node variants. Future
versions can extend the parser when a concrete user need
appears.

This keeps the PRD tight and buys us the 100% of the codespar
use case without opening the door to Python/Rust/Elixir scope.
