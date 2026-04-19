# Documentation Plan: env-example-integration

Generated from: docs/plans/PLAN-env-example-integration.md
Issues analyzed: 6
Total entries: 4

---

## doc-1: README.md
**Section**: What it does
**Prerequisite issues**: 4
**Update type**: modify
**Status**: pending
**Details**: Add `.env.example` auto-discovery to the feature bullet list under "What it does". The feature reads each managed repo's `.env.example` on every apply and merges it as the lowest-priority defaults layer into `.local.env`, with per-key warnings for undeclared keys and errors for probable secrets. Also note the `read_env_example` opt-out flag exists at workspace and per-repo level so users know it's opt-out, not opt-in.

---

## doc-2: README.md
**Section**: Commands
**Prerequisite issues**: 6
**Update type**: modify
**Status**: pending
**Details**: Update the `niwa status` row in the Commands table to note that `--verbose` now includes `.env.example` as a named source alongside `[env.vars]`, vault, and overlay. Current description says "Show workspace health: repos, drift, last applied" — it should surface that `--verbose` reports per-var source attribution.

---

## doc-3: docs/designs/current/DESIGN-workspace-config.md
**Section**: Go type definitions
**Prerequisite issues**: 1
**Update type**: modify
**Status**: pending
**Details**: Add `ReadEnvExample *bool` field to the `WorkspaceMeta` and `RepoOverride` Go type definitions shown in the design doc. Include the TOML tag (`read_env_example`) and the nil-means-inherit semantics (nil = true at workspace level; nil = inherit workspace setting at repo level). This keeps the design doc's type snapshot current with the actual struct.

---

## doc-4: docs/designs/current/DESIGN-workspace-config.md
**Section**: Full workspace.toml example
**Prerequisite issues**: 1, 4
**Update type**: modify
**Status**: pending
**Details**: Add a commented `read_env_example` example to the full workspace.toml config example in the design doc. Show both the workspace-level opt-out (`[config] read_env_example = false`) and per-repo opt-out (`[repos.<name>] read_env_example = false`) so readers can see where each flag lives in real config.

---
