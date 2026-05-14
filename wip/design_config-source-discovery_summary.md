# Design Summary: config-source-discovery

## Input Context (Phase 0)
**Source PRD:** docs/prds/PRD-config-source-discovery.md
**Problem (implementation framing):** Close the R5 gap by adding a
streaming probe pass to the existing GitHub tarball + non-GitHub
shallow-clone fetch paths, override the upstream R35 overlay-slug
derivation to anchor on the source repo name, wire a rank-2
deprecation notice through `DisclosedNotices`, and expose a niwa
entry point the shirabe migration skill can call instead of
re-implementing the probe.

## Current Status
**Phase:** 0 - Setup (PRD)
**Last Updated:** 2026-05-14
