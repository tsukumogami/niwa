# Security review: worker-permissions (Phase 6)

## Review questions

### 1. Does startup-time resolution in spawnContext actually close the TOCTOU path?

Yes. The revised design correctly closes the escalation path identified in Phase 5.
Phase 5's high-severity finding was that a per-spawn read of `settings.local.json`
lets an `acceptEdits` worker overwrite that file to inject `bypassPermissions` before
the next spawn. The revised design reads the file once in `runMeshWatch` before the
event loop starts, stores the result in `spawnContext.workerPermMode`, and never
reads the file again. No worker runs before that read completes. A tampered
`settings.local.json` written by any subsequent worker is never consulted.

The Phase 5 report rated two distinct concerns under "settings.local.json tamper":
a per-spawn TOCTOU path and an alternative hash-check or epoch-pinning mitigation.
The revised design's approach (startup-time freeze) is architecturally cleaner than
either and eliminates the surface entirely rather than detecting tampering after the
fact.

One residual edge case: if `runMeshWatch` crashes and is restarted automatically
(e.g., by a process supervisor) while a tampered `settings.local.json` is in place,
the new daemon instance would read the tampered value at its startup. This is a
narrow window — requiring both a daemon restart and a race against the file system
state — and is acceptable for a v1 design. It should be noted in the Consequences
section.

### 2. Are the mitigations for bypass blast radius adequate?

Partly. The design acknowledges that bypass is all-or-nothing at the instance level
and flags "future work" for per-delegation scoping. The SIGKILL teardown ordering
bounds the exfiltration window at daemon destroy time. These are the right baselines
for a v1 design.

However, the Phase 5 concern about "orphan bypass" workers (a coordinator that no
longer needs bypass having already spawned long-running workers with bypass) is not
addressed in the revised design. The design's startup-time freeze means the daemon
locks in the permission mode at start, so a user who downgrades from bypass to ask
via `niwa apply` and `niwa daemon restart` does get the new mode — there is no
orphan-bypass problem at the instance level. Workers already spawned but still
running do retain bypass until they exit, but that is the same behavior as any other
permission mode change.

The one genuinely unaddressed blast-radius concern: all delegated subtasks in a
bypass-configured workspace session inherit bypass, even subtasks that do not require
it. The design notes this and defers it. The deferral is reasonable but the design
should state explicitly that the per-delegation override remains a planned future
mitigation so it does not get dropped from the backlog.

### 3. Residual risk from the minimal-struct parsing recommendation

The revised design adopted the minimal-struct recommendation from Phase 5. The struct
in the interface definition parses only `permissions.defaultMode` and nothing else.
This closes the in-process secret exposure path Phase 5 identified.

One residual concern: the `settingsPermissionsDoc` struct name is internal to the new
`permissions.go` file. If a future maintainer extends `WorkerPermissionMode` to return
additional fields (e.g., to read an `env` block for some future feature), they may
broaden the struct without realizing the security intent of keeping it narrow. The
implementation should include a comment on the struct explaining why it intentionally
omits the `env` key.

### 4. Attack vectors the Security Considerations section missed

**Process supervisor restart race (low severity).** If the daemon is managed by a
process supervisor that auto-restarts it on crash, a compromised worker can write
`settings.local.json`, then signal the daemon to crash (e.g., via a file that triggers
a panic), causing the supervisor to restart with the tampered file. The design's
startup-time freeze does not cover this scenario. The attack requires a restart, which
the user would notice in logs, but it is worth documenting.

**Curated Bash pattern scope understatement (acknowledged but needs clearer language).**
The design correctly states the curated list is "not a security boundary," but then
lists `Bash(go test *)` and `Bash(go build *)` as if they are benign relative to
`Bash(gh *)` and `Bash(git *)`. In practice, `go test ./...` with a compromised
`_test.go` file can execute arbitrary code (Go test files are compiled and run). A
compromised repository could include a `TestMain` that exfiltrates data. This is a
pre-existing risk for any tool that compiles and runs code, but the Security
Considerations section should be explicit that `Bash(go test *)` and `Bash(make *)`
are code-execution primitives, not command-line wrappers.

**No new vectors beyond Phase 5.** The other Phase 5 findings (prompt injection,
permission scope) are correctly represented in the revised Security Considerations
section and the mitigations documented are appropriate for v1.

## Summary and recommended changes

1. **TOCTOU: closed.** The startup-time freeze correctly eliminates the escalation
   path. No change required.

2. **Process supervisor restart race:** Add a note to the Security Considerations
   section that startup-time resolution does not cover a daemon restart triggered
   by a compromised worker. Severity: low; no design change required, documentation
   only.

3. **Minimal struct comment:** Add an explicit comment on `settingsPermissionsDoc`
   explaining that the `env` key is intentionally absent to avoid loading secret
   material.

4. **go test / make as code-execution primitives:** Revise the curated Bash fallback
   paragraph in Security Considerations to note explicitly that `Bash(go test *)` and
   `Bash(make *)` are code-execution surfaces, not command wrappers, and carry the
   same prompt-injection consequence as `gh api` or `git push`.

5. **Per-delegation bypass: document the deferral explicitly.** Add a sentence to the
   Consequences/Negative section naming the per-delegation override as planned future
   work so it is tracked, not forgotten.
