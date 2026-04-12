# Design Summary: shell-navigation-protocol

## Input Context (Phase 0)
**Source:** Freeform topic (related to issue #48)
**Problem:** The stdout-as-cd protocol is fragile — any stdout output beyond
the landing path silently breaks shell navigation. Future output modes (verbose,
JSON, CI) make this worse since we can't control all subprocess output.
**Constraints:** Must be parseable by POSIX bash/zsh; must not suppress
progress output; must survive third-party subprocess stdout.

## Current Status
**Phase:** 1 - Decision Decomposition
**Last Updated:** 2026-04-11
