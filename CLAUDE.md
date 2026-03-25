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
