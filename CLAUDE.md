# niwa

Declarative workspace manager for AI-assisted development. Manages multi-repo
workspaces with layered Claude Code configuration (CLAUDE.md hierarchy).

## Repo Visibility: Public

## Default Scope: Tactical

## Architecture

Go CLI using cobra. Entry point at `cmd/niwa/main.go`, commands in `internal/cli/`,
version info in `internal/buildinfo/`.

## Conventions

- Go code: standard gofmt, go vet only (no external linters)
- Conventional commits
- No emojis in code or documentation
- Never add AI attribution or co-author lines to commits or PRs

## Testing

Unit tests live alongside source files (`*_test.go`). Functional
(end-to-end) tests live in `test/functional/` and run the compiled
binary via `make test-functional` or `make test-functional-critical`.

When you ship a user-facing CLI command or fix a regression in the
init → create → apply workflow, add a `@critical` Gherkin scenario in
`test/functional/features/`. See
`docs/guides/functional-testing.md` for patterns and the
`localGitServer` helper that provides offline bare-repo fakes.

## Contributor Guides

- `docs/guides/functional-testing.md` — end-to-end test patterns and the `localGitServer` helper
- `docs/guides/one-time-notices.md` — how to add informational messages that appear once per workspace instance
- `docs/guides/vault-integration.md` — vault provider architecture and acceptance coverage
- `docs/guides/workspace-config-sources.md` — config source resolution, snapshot model, and discovery conventions
