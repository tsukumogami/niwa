# Security Considerations — Dispatch handle retask

## REQUIRED design changes

These are ordered by severity. R-SEC-1 and R-SEC-2 are fail-open holes the
design does not currently close; the rest harden input handling that the design
gestures at but does not pin.

1. **R-SEC-1 — Generic `niwa retask` must refuse watch-sandboxed instances (sandbox-bypass hole).**
   Watch review sessions run under a no-egress OS sandbox *because* they read
   untrusted PR diffs (`ApplyReviewSettings`, `internal/watch/containment.go`).
   The design routes watch's own continuation through the engine with
   `PreLaunch: ApplyReviewSettings(...)`, but the generic `niwa retask <target>`
   command carries **no** PreLaunch hook. A generic retask against a
   watch-staged instance relaunches `claude --bg --resume` with `cmd.Dir` = the
   instance, re-reading whatever `.claude/settings.json` is on disk *without
   re-running `ApplyReviewSettings` and without its re-verification gate*. That
   is a real containment bypass: the review worker runs under `bypassPermissions`
   in the hard-deny posture and can rewrite its own `.claude/settings.json`; a
   generic retask would then relaunch it uncontained (network egress restored,
   MCP/WebFetch un-denied, `--strict-mcp-config` dropped) with the untrusted PR
   context still in the transcript — an exfiltration path. The generic command
   cannot reconstruct the correct `ApplyReviewSettings(sandbox, ask)` arguments
   (it does not know the owner/repo/ask context), so it cannot safely re-assert
   them. **Requirement:** `niwa retask` must detect an instance that has a watch
   `StagedRecord` (or a `sandbox` stanza in its `.claude/settings.json`) and
   **refuse** with a distinct sentinel error directing the operator to let watch
   drive its continuation. Watch's own path (which supplies the PreLaunch
   re-assertion and re-verification) stays the only sanctioned way to continue a
   sandboxed review session.

2. **R-SEC-2 — `classifyWorker` must default-deny; the busy/blocked signals it keys on are not currently decoded.**
   The design classifies `busy = state running/working, active tempo, or
   in-flight tasks` and `blocked = blocked state or a pending need`, with
   "anything else is retaskable." But `jobState` (`internal/cli/job_state.go`)
   decodes only `sessionId`, `template`, and `cwd` — no `state`, `tempo`, or
   `tasks` field exists in the decoder or anywhere in the tree. As written, a
   genuinely busy worker decodes those fields as their zero value and falls
   through to **retaskable**, so retask would `claude stop` a worker mid-turn —
   a fail-**open** violation of R4/N1 that can corrupt or discard the running
   worker's in-flight work. **Requirement:** the classifier must fail closed —
   unless the decoded job state *positively* proves the worker is idle and
   retaskable, refuse (busy/unknown). The implementation must add and validate
   the specific state.json fields the busy/blocked decision reads (as part of
   the live-gate verification in Implementation Approach step 1), and treat
   absent/undecodable fields as "not provably idle → refuse," never as
   retaskable.

3. **R-SEC-3 — Every session id crossing into argv or a path must be re-validated with `ValidSessionID` at the point of use.**
   `ReadSessionMapping` validates only the id in the *filename*, never the
   `session_id` in the JSON *body*. The superseded id the engine reads from the
   mapping flows into `claude stop`, `--resume <id>`, and `claude rm <id>`
   argv. A hand-edited or corrupt mapping body could carry a value that is not a
   plain UUID (e.g. a leading-dash string that `claude rm` parses as a flag, or a
   different valid UUID naming an unrelated session). **Requirement:**
   re-validate the old id with `workspace.ValidSessionID` immediately before it
   becomes an argv element or path component, abort if it fails, and pass it as a
   discrete argv element after a `--` terminator where the subcommand supports
   one. The newly captured id is already validated inside `matchSessionByCwd`;
   keep that guard.

4. **R-SEC-4 — The lock filename component must be a validated instance name, never the raw target argv.**
   `.niwa/locks/<instance>.lock` must be built from the **resolved** instance
   name (`mapping.InstanceName`), and that name must be asserted path-safe before
   it becomes a filename: `filepath.Base(name) == name`, not `"."`/`".."`, and
   containing no path separator or NUL. Building the path from the raw
   `<target>` argument (which the user controls) would expose traversal
   (`../../etc/...`) and absolute-path escape out of `.niwa/locks/`. `isSafeHandle`
   is too strict here (instance names legitimately contain `+` and `.`), so use a
   dedicated path-component assertion rather than reusing it verbatim.

## Trust model and privilege posture (CLEAN)

- **Same-user boundary.** Every input surface retask touches — the session
  mapping store (`.niwa/sessions`), the lock dir (`.niwa/locks`), the Claude
  jobs dir (`~/.claude/jobs`), and the instance's `.claude/settings.json` — is
  owned and writable by the invoking user. Anyone who can craft a malicious
  mapping or job-state file already has the user's shell and could invoke
  `claude` directly. Retask therefore introduces **no new trust boundary versus
  `dispatch` itself**: dispatch already launches a worker with the user's full
  permissions and an arbitrary prompt. The validations above are defense against
  *corruption and mistakes* (a garbled mapping causing an unrelated `claude rm`,
  a traversal via a crafted name), not against a privilege-crossing attacker.
- **Prompt injection is in-scope-by-design, not a new risk.** Retask delivers
  arbitrary instructions to a worker running with the user's permissions — this
  is the feature. The one place it *is* a boundary is the watch-sandbox case,
  which R-SEC-1 closes: there, the worker's context is untrusted (a PR diff) and
  the containment posture must survive the relaunch.
- **No new privileges (confirms N4).** Retask needs no root and no managed
  settings. Its writes are confined to the workspace (`.niwa/sessions`,
  `.niwa/locks`, and — only on watch's own path — the instance
  `.claude/settings.json`); its reads add only the jobs dir already consumed by
  dispatch/reap; its external effects are `claude stop|--resume|rm` run as the
  user. The only privileged operation in the codebase (`setup-sandbox`'s sudo)
  is unrelated and untouched.

## Threat surfaces mapped to components

### Untrusted-input flows

- **Prompt argument (`retask.go` → `dispatchLaunch`).** Mitigated by reuse of
  the existing D8 guard: `buildClaudeBgArgs` places the prompt as a single,
  final, non-shell-interpolated argv element, so `$(...)`, quotes, and newlines
  arrive byte-identical (PRD R1, acceptance criterion "delivered without shell
  interpretation"). **Requirement (confirming, not new):** retask must reuse
  `dispatchLaunch`/`buildClaudeBgArgs` and re-apply dispatch's empty-prompt and
  `maxPromptBytes` guards — it must never assemble a shell string. The
  `--resume` flag and its UUID must likewise be two discrete argv elements, with
  the UUID validated per R-SEC-3.
- **Target argument (`retask.go` resolution).** Travels into (a) a mapping
  lookup key and (b) the lock filename. (a) is safe — resolution reads the
  mapping index and short-id prefixes, and an unknown/ambiguous target is a
  `target-unknown` sentinel. (b) is R-SEC-4: the lock path must derive from the
  resolved instance name, not the raw argv.
- **Session ids from mappings / job files.** Covered by R-SEC-3 (mapping body
  ids) and the existing `matchSessionByCwd` validation (captured ids).

### Lock-file surface (`.niwa/locks/<instance>.lock`)

- **Traversal:** closed by R-SEC-4 (validated name component).
- **Symlink:** the attach-lock pattern (`os.OpenFile(path, O_CREATE|O_RDWR,
  0600)` + non-blocking `flock`, `sessionattach/attach.go`) is the right model —
  no `O_TRUNC`, so even a pre-planted symlink at the lock path is opened without
  truncating its target, and the same-user model means a symlink attacker
  already owns the account. Reuse that exact open+flock shape.
- **Cleanup / staleness:** `flock` is kernel-scoped and self-releases on process
  death, so a crashed retask leaves no logically-held lock and needs no
  staleness protocol (the design's stated reason for rejecting `O_EXCL` lock
  files — endorsed). The empty lock file lingering on disk is harmless and
  reused next invocation.

### Concurrency: retask vs retask, retask vs reap (N2)

- The per-instance non-blocking `flock` makes two concurrent retasks
  mutually exclusive — the loser gets `EWOULDBLOCK` → `conflict` sentinel, fail
  closed. Endorsed.
- The reaper taking the **same** trylock and skipping a held instance protects
  the stop-to-capture window where liveness legitimately reads dead. This is
  load-bearing and must cover `reapOpportunistically` (called at the start of
  every dispatch/create), not just on-demand `reap`.
- **Defense in depth already present:** even if the reap-join collision
  preference (design's `live-mapping-wins` fix) picks the wrong mapping during
  the two-mappings-one-path window, `instanceHasLiveJob` (`job_state.go`) spares
  any instance a live job's cwd resolves inside — so the survivor's instance
  cannot be reaped out from under it. Keep this guard; the trylock plus the
  collision fix plus this cwd guard form three independent layers against the
  reaped-instance-with-live-session state N2 forbids.

### Superseded-session removal (`claude rm`)

- Ordered after the rebind (write-new → delete-old → `claude rm old`), so a
  removal failure is fail-safe: the mapping already names the survivor and the
  lingering old entry is harmless. Governed by R-SEC-3 (validate the id before
  exec) so a crafted mapping cannot direct `claude rm` at an unrelated session or
  smuggle a flag. Note (correctness, not security): a *failed* `claude rm`
  re-introduces exactly the #211 residual-ambiguity the exclude-known capture
  exists to prevent, and after rebind the old id is no longer "known" to the next
  retask — worth a reap-backed sweep or a logged warning, but not a security
  defect.

## Residual risks accepted

- A user who hand-corrupts their own mapping store can still cause retask to
  operate on the wrong (but user-owned) session; the R-SEC-3 validation reduces
  this to "a valid-UUID mapping for a session the same user controls," which is
  within the same-user model.
- Fork-under-the-hood rotates the underlying session id (PRD Known Limitations);
  anything holding the raw id across a retask is stale. This is a documented
  functional limitation, not a security exposure — the niwa handle is the stable
  identity.
