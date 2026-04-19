# Design Summary: env-example-integration

## Input Context (Phase 0)

**Source PRD:** docs/prds/PRD-env-example-integration.md
**Problem (implementation framing):** ResolveEnvVars in materialize.go has no
lowest-priority layer for .env.example; adding one requires a Node-syntax parser
rewrite, a secrets-exclusion check against the fully-merged config, and per-key
classification for undeclared keys.

## Current Status

**Phase:** 0 - Setup (PRD)
**Last Updated:** 2026-04-18
