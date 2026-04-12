# Design Summary: shell-navigation-protocol

## Input Context (Phase 0)
**Source:** Freeform topic (related to issue #48)
**Problem:** The stdout-as-cd protocol is fragile — any stdout output beyond
the landing path silently breaks shell navigation. Future output modes (verbose,
JSON, CI) make this worse since we can't control all subprocess output.
**Constraints:** Must be parseable by POSIX bash/zsh; must not suppress
progress output; must survive third-party subprocess stdout.

## Security Review (Phase 5)
**Outcome:** Option 2 (document considerations)
**Summary:** No design changes required. Primary surface is NIWA_RESPONSE_FILE
injection (low severity — attacker can only write a path string, not arbitrary
content) and temp file TOCTOU (theoretical, mitigated by mktemp randomness and
/tmp sticky bit). validateStdoutPath closes shell injection. Implementation
should validate NIWA_RESPONSE_FILE points into $TMPDIR or /tmp.

## Current Status
**Phase:** 6 - Final Review
**Last Updated:** 2026-04-11
