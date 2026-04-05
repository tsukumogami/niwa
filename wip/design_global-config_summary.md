# Design Summary: global-config

## Input Context (Phase 0)

**Source PRD:** docs/prds/PRD-global-config.md
**Problem (implementation framing):** The niwa apply pipeline assumes a single config source (workspace.toml); adding global config requires a new sync step, a new intermediate merge layer, a new per-instance opt-out flag in instance state, and a new `niwa config` subcommand for registration.

## Current Status

**Phase:** 0 - Setup (PRD)
**Last Updated:** 2026-04-04
