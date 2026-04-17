# Design Summary: vault-multi-org-auth

## Input Context (Phase 0)

**Source:** Freeform topic (from /explore vault-multi-org-auth)
**Problem:** The Infisical backend relies on one CLI session, but users
working across multiple Infisical orgs need per-provider auth. The
fix must be zero-ceremony for single-org users and opt-in for multi-org.
**Constraints:** No new Go deps (R20), no credentials in any repo,
threat model accepts same-user processes reading local files at 0o600.

**Execution mode:** auto
**Visibility:** Public
**Scope:** Tactical

## Current Status

**Phase:** 0 - Setup (Freeform) complete
**Last Updated:** 2026-04-17
