# Security Review

**Verdict:** PASS

PASS with notes: the design's injection, capture, and destroy-blast-radius reasoning all hold against the verified code, but four minor hardening items are worth recording.

## Issues Found
1. Pass-through flag forwarding (`--model`/`--permission-mode`/`--agent`/`--label`): NOTE / low — The reused `sessionattach` exec pattern builds argv directly with no shell (`exec.CommandContext`, verified in `internal/cli/sessionattach/supervise.go:44-52`), so a crafted `--label "x --dangerously-skip-permissions"` lands as a single argv token, not two flags. Flag injection is structurally prevented as long as the implementation appends each pass-through value as its own discrete argv element (or after a `--` terminator) and never string-concatenates user input into a flag. The design should state this argv-boundary requirement explicitly so the implementer cannot regress it.

2. cwd-correlation trust on `state.json`: NOTE / low — `~/.claude/jobs/*/state.json` is writable by any local process, so the `cwd == instanceDir` key is only trustworthy because `instanceDir` is a freshly-created unguessable `disp-<8 hex>` path that a planted file cannot pre-name. That reasoning is sound for the capture path. Suggest the design note that capture must read `sessionId` ONLY from the state file whose `cwd` matches, and must reject/timeout (not pick arbitrarily) if two files claim the same cwd, so a racing planted file cannot substitute a foreign UUID. `ValidSessionID` before path/key use matches existing hook practice and is sufficient.

3. Symlink canonicalization in the destroy path: NOTE / low — The design correctly normalizes both sides of the cwd comparison with `EvalSymlinks`+`Clean`, but the existing `workspace.DestroyInstance` (`internal/workspace/destroy.go:162-180`) validates and `os.RemoveAll`s by raw path with no canonicalization and a TOCTOU window. The dispatch design inherits this property unchanged and does not widen it (rollback destroys only the command's own created path; the backstop is gated on an in-instance marker). Pre-existing, not introduced here — flag for the implementer, not a blocker.

4. `disp-<8 hex>` naming: NOTE / informational — Not a security boundary, as the design states. 32 bits is collision-safe for the concurrency scope and guess-resistance is not load-bearing (the marker+mapping+TTL gate, not the name, is the destroy boundary). Acceptable.

Confirmed clean: no shell path anywhere (D8 holds); reaper never reaps a live mapped session (verified `sessionLive` gate in `internal/cli/reap.go` / `job_state.go`); backstop branch is gated mapping-absent AND marker-present AND age>TTL and cannot reach a developer instance (no marker) or a young in-flight instance; `Origin` is informational and reaper ignores it (eligibility unchanged); no new credential/network surface beyond `niwa create`'s existing `claude.env` materialization; no private references in the doc (only allowed `tsukumogami/niwa#171`/`#172`).

## Summary
The injection-safety claim is verified against the reused argv-only exec pattern, the destroy blast radius is correctly bounded to command-created or marker-gated instances, and the reaper's live-session protection is preserved. The cwd-correlation trust on attacker-writable `state.json` is acceptable only because the instance dir is an unguessable freshly-created path, and the four notes (explicit argv-boundary requirement, duplicate-cwd rejection on capture, the inherited non-canonical destroy path, and naming) are hardening suggestions rather than design defects. No material vulnerability requires a design change.
