# Documentation Plan: workspace-visibility-overlay

Generated from: docs/plans/PLAN-workspace-visibility-overlay.md
Issues analyzed: 4
Total entries: 4

---

## doc-1: README.md
**Section**: Commands
**Prerequisite issues**: Issue 1
**Update type**: modify
**Status**: updated
**Details**: Update the `niwa init` row in the commands table to show the new `--overlay <repo>` and `--no-overlay` flags. Add a brief note that the two flags are mutually exclusive.

---

## doc-2: README.md
**Section**: Shared workspace configs
**Prerequisite issues**: Issue 1, Issue 3
**Update type**: modify
**Status**: updated
**Details**: Add a subsection explaining workspace overlay convention discovery: when `niwa init` is run against a shared config, niwa automatically looks for a `<repo>-overlay` companion repo. Explain `--overlay <repo>` for explicit overlay, `--no-overlay` to opt out, and that `niwa apply` syncs the overlay on subsequent runs. Keep the overlay's private nature implicit — don't frame it as a "private overlay" in public docs.

---

## doc-3: README.md
**Section**: What it does
**Prerequisite issues**: Issue 1, Issue 3, Issue 4
**Update type**: modify
**Status**: updated
**Details**: Add a bullet to the feature list for overlay support: auto-discovered companion repos that layer additional repos, groups, and context onto the base workspace config. Also note that `niwa apply` keeps the overlay clone in sync automatically.

---

## doc-4: README.md
**Section**: Configure (or a new "Overlay content" subsection near step 4)
**Prerequisite issues**: Issue 4
**Update type**: modify
**Status**: updated
**Details**: Document that `CLAUDE.local.md` files can receive overlay content appended by the apply pipeline (from the overlay's content entries), and that `CLAUDE.overlay.md` is installed at the workspace root when the overlay provides one. Describe the `@CLAUDE.overlay.md` import injection and where it appears in the `CLAUDE.md` import order (`@workspace-context.md` → `@CLAUDE.overlay.md` → `@CLAUDE.global.md`).

---
