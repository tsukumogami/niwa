# Explore Scope: env-example-integration

## Topic

Should niwa natively understand and build on top of the `.env.example`
convention for env vars declared in managed app repos?

## Problem Statement

Managed app repos (e.g., codespar/codespar, codespar/codespar-web) ship
`.env.example` files in the repo root. These declare which env vars the
app needs, provide sensible defaults for non-sensitive fields, and
stub placeholders for secrets. They are the de facto industry standard
for onboarding docs in web app repos.

niwa's workspace config currently duplicates every such default into
`[repos.<name>.env.vars]` in the dot-niwa workspace.toml. This creates
drift risk: the app repo and the dot-niwa are two sources of truth
for the same values, maintained by different people in different PR
streams. When the app repo adds a new var or changes a default, the
dot-niwa does not notice.

The question: should niwa read the app repo's `.env.example` as the
source of truth for these defaults, and if so, what is the right
boundary between "defaults from the app repo" and "overrides from the
workspace config"?

## In Scope

- niwa's existing env primitives (`[env.vars]`, `[env.secrets]`,
  `[env.secrets.required|recommended|optional]`, `[env].files`) and
  the materialization pipeline (`internal/workspace/materialize.go`).
- Reading `.env.example` files from managed app repos (the repos
  niwa clones, not the dot-niwa config repo).
- Interaction with vault resolution and the public-repo guardrail.
- The security boundary: how niwa distinguishes plausible-looking
  stub values in `.env.example` from real secrets.
- API shape candidates (convention-based auto-discovery, explicit
  declaration, a new primitive, or no new primitive at all).

## Out of Scope

- Framework-specific env file variants (`.env.local`, `.env.development`,
  Laravel `.env`, Next.js loader behavior).
- Writing to managed app repos. niwa stays declarative and
  read-only on app repos — it only reads their `.env.example` files.
- Changing the `.env.example` format itself; it's a community convention.
- Issue #61 fixes for static env files in the dot-niwa repo — related
  but separate scope.
- Issue #62 (vault URIs in recommended/optional) — related but separate.

## Constraints

- niwa is declarative. Any integration must respect the app repo as
  the source of truth for `.env.example` content.
- PR #63 (folder-path vault URIs) just merged. New capability can
  assume that vault resolution is in good shape.
- Issue #61 documents that the existing `[env].files` code path is
  a degraded subset (no vault, no guardrail, no tier metadata, no
  secret wrapping). Any new integration should either reuse and fix
  that path, or build a better one.
- Public repo (niwa). All artifacts stay free of private references.

## Research Leads (Round 1)

### Lead A: niwa env-loading internals

Map the current state. What code loads `[env].files` today? How does
the materializer merge inline `[env.vars]` entries with values read
from files? What does per-repo env configuration look like under
`[repos.<name>.env.*]`? Where does the guardrail walk, and what
does it intentionally miss (per issue #61)? What's the shape of a
hypothetical new "read a file from inside a managed repo after it's
cloned" code path — which pipeline stage owns it?

Output: a map of the relevant functions and types, plus a short list
of the integration points any new `.env.example` reader would touch.

### Lead B: Industry prior art on `.env.example`

Survey how other declarative tooling handles app-repo env files:
- Docker Compose (`env_file:` directive, `environment:` merge rules)
- Vercel / Railway / Fly.io (do they read `.env.example` to prompt
  users? how do they separate stubs from real secrets?)
- Turborepo (`turbo.json` env declarations vs `.env.local`)
- Nx (`project.json` env handling)
- dotenv / direnv (how they treat `.env.example` vs `.env`)

Output: a short note on each, with two bullets — what they read and
how they treat stubs. Flag any convention niwa could match directly.

### Lead C: `.env.example` parsing + security hazards

Two sub-questions:
1. Parsing: what does `.env.example` syntax actually include in the
   wild? Multiline values, quoted strings, variable expansion
   (`${FOO}`), export prefixes, comments, empty values. What does a
   lenient parser need to handle? Are there Go libraries worth
   depending on (godotenv, joho/godotenv, subosito/gotenv), or should
   niwa parse with a small dedicated function?
2. Security: how do teams indicate "this is a stub, not a real value"
   in `.env.example`? Is there a convention (`sk_test_...`, empty
   values, `<placeholder>`)? What should niwa do if a value in
   `.env.example` looks like a real secret (e.g., the codespar
   example ships `pk_test_Y2FyaW5...` which is a real test key)?
   How does this interact with the public-repo guardrail that
   rejects plaintext values in `[env.secrets]`?

Output: parsing requirements the integration must handle; a
recommended rule for how niwa separates "stub value → keep as
plaintext default" from "looks like a real secret → require
`[env.secrets.required]` declaration instead".

## Recommendation Heuristic for Artifact Type

This is a public-repo tactical-scope exploration. Likely landing
points after research:

- **Design doc** if the integration needs new materialization rules,
  new merge semantics, or interaction with the vault pipeline.
- **Just-do-it / small PR** if an existing primitive (e.g., extending
  `[env].files` to accept post-clone repo-relative paths) covers it
  cleanly with one or two test cases.
- **Decision record only** if the answer is "document the pattern;
  no niwa change."
- **PRD** is unlikely — this is an internal API/ergonomics question,
  not a product-level requirements question.

Phase 4 crystallize picks among these based on the research findings.
