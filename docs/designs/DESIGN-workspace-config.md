# DESIGN: Workspace config format

## Status

Proposed

## Context and Problem Statement

niwa needs a declarative configuration format that expresses the workspace structure currently wired by an imperative 700-line bash installer. The installer performs 27 operations across repo cloning, CLAUDE.md hierarchy generation, per-repo hooks and settings distribution, environment file merging, and plugin registration. All of this is hardcoded for a single organization.

The config must generalize these operations into a TOML schema that any developer can use to define a multi-repo workspace with layered AI context. It must support multi-instance workspaces from the same definition (e.g., tsuku/, tsuku-2/), template variable substitution in content files, and per-host overrides for channel and bot configuration.

## Decision Drivers

- **Parseable by Go TOML libraries**: schema must work with BurntSushi/toml or pelletier/go-toml
- **Content by reference, not inline**: CLAUDE.md content lives in separate files, config points to them
- **Multi-instance required**: same config, multiple workspace instances, isolated state
- **Three-level hierarchy**: workspace > group > repo context inheritance for CLAUDE.md
- **Phased delivery**: v0.1 covers core lifecycle (repos, groups, content), later phases add hooks, env, channels
- **Convention over configuration**: sensible defaults reduce boilerplate for common cases
- **Prior art alignment**: TOML matches tsuku recipe format; patterns from Google repo tool and Nx inform the design
