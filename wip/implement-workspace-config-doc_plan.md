# Documentation Plan: workspace-config

Generated from: docs/plans/PLAN-workspace-config.md
Issues analyzed: 8
Total entries: 6

---

## doc-1: README.md
**Section**: Status, (new section) Usage
**Prerequisite issues**: #1
**Update type**: modify
**Status**: pending
**Details**: Update the Status section to reflect that `niwa apply` is implemented. Add a Usage section after Install that covers: creating a `.niwa/workspace.toml`, basic config structure (workspace name, one source, two groups), and running `niwa apply`. Keep it minimal -- a quick-start example, not a full reference.

---

## doc-2: docs/guides/workspace-config.md
**Section**: (new file)
**Prerequisite issues**: #1, #2
**Update type**: new
**Status**: pending
**Details**: New guide covering the workspace.toml format and content hierarchy. Sections: config file location (`.niwa/`), workspace metadata, declaring sources, defining groups with visibility filters, content hierarchy (workspace/group/repo/subdir levels), template variables (`{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`), auto-discovery from `content_dir`, and the CLAUDE.md vs CLAUDE.local.md placement convention. Written for someone setting up their first niwa workspace.

---

## doc-3: docs/guides/workspace-config.md
**Section**: Sources and Groups
**Prerequisite issues**: #1, #3, #4
**Update type**: modify
**Status**: pending
**Details**: Expand the sources section to document `max_repos` threshold (default 10), per-source threshold override, explicit repo lists on sources, and multi-org support. Expand the groups section to cover explicit `repos = [...]` lists as an alternative to visibility filters, mixed filter+list groups, and classification rules (no-match warns, multi-match errors).

---

## doc-4: docs/guides/workspace-config.md
**Section**: Per-repo Overrides
**Prerequisite issues**: #1, #5
**Update type**: modify
**Status**: pending
**Details**: Add a section documenting the `[repos.<name>]` override syntax: `claude = false` to skip config generation, per-repo url/branch overrides, settings/env overlay semantics (repo wins for settings/env vars, extend for hooks), and warning behavior for unknown repo names.

---

## doc-5: README.md
**Section**: Usage
**Prerequisite issues**: #1, #7
**Update type**: modify
**Status**: pending
**Details**: Add mention of the global registry (`~/.config/niwa/config.toml`) and workspace name resolution to the Usage section. Briefly note that `niwa apply` can resolve a workspace by name through the registry, with a pointer to the full guide for details.

---

## doc-6: docs/guides/workspace-config.md
**Section**: Instance Management
**Prerequisite issues**: #1, #6, #7
**Update type**: modify
**Status**: pending
**Details**: Add a section covering multi-instance workspaces and the global registry. Document: instance state file (`.niwa/instance.json`), drift detection (hash-based warning before overwrite), instance discovery (walk-up from cwd), instance enumeration (scan sibling directories), numbering scheme, and the global config at `~/.config/niwa/config.toml` with `[global]` settings and `[registry.<name>]` entries.
