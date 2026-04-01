# Design Summary: pull-managed-repos

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** niwa has no mechanism to keep managed repos current after initial
workspace creation. Repos are skipped on apply, leaving clones at their original
commit. Users must manually pull each repo or recreate the workspace.
**Constraints:** Non-destructive by default, backward-compatible, low friction.
Git strategy is fetch + ff-only. Pull only clean repos on default branch.

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-01
