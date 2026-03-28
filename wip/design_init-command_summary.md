# Design Summary: init-command

## Input Context (Phase 0)
**Source:** Freeform topic (Roadmap Feature F3: Init command and global registry)
**Problem:** niwa has no command to create workspaces. Users must manually create .niwa/workspace.toml. No scaffolding, no remote config cloning, no registry integration.
**Constraints:** Three modes required (no-args, named, remote --from). Registry backend already implemented. .niwa/ is the config home per Decision 7.

## Current Status
**Phase:** 0 - Setup (Freeform)
**Last Updated:** 2026-03-27
