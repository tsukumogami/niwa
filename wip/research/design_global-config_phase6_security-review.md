# Security Review: DESIGN-global-config (Phase 6)

## Scope

Deep review of the Security Considerations section in
`docs/designs/DESIGN-global-config.md`, cross-referenced against the existing
codebase (`internal/config/`, `internal/workspace/`) to evaluate coverage,
mitigation quality, and residual risk.

This review builds on the phase-5 security assessment, which identified three
implementation-level gaps. Each gap is re-evaluated here along with additional
vectors not covered in phase 5.

---

## Question 1: Attack Vectors Not Considered

### 1.1 Env file path traversal (not mentioned in the design)

`GlobalOverride.Env.Files` accepts a list of file paths read from the cloned
global config repo. The `EnvMaterializer` calls `checkContainment(src,
ctx.ConfigDir)` (materialize.go line 412), but the design's Security
Considerations section only mentions parse-time validation for the `files`
destination map. It does not mention equivalent parse-time validation for
`Env.Files` source paths.

The workspace config has no corresponding parse-time check for `Env.Files`
either — both rely entirely on the runtime `checkContainment` guard. For
consistency with the stated design principle ("parse-time rejection produces
clearer error messages"), the global config parser should also validate
`Env.Files` entries for `..` components and absolute paths.

**Severity:** Low-to-medium. The runtime guard catches the issue, but the
design's own argument for parse-time validation applies here equally and was
not extended to env file paths.

### 1.2 CLAUDE.global.md as a prompt injection vector

The design acknowledges that `CLAUDE.global.md` is copied verbatim, and
identifies content injection as the relevant risk, equivalent to the workspace
CLAUDE.md. However, there is a meaningful asymmetry worth noting: the workspace
CLAUDE.md is team-owned and reviewed by multiple people; `CLAUDE.global.md` is
entirely user-owned and may receive less scrutiny over time (users often set
something up once and forget it).

A compromised personal GitHub account, a supply-chain event on the global config
repo (e.g., a compromised GitHub Actions workflow modifying the file), or a repo
transferred to a bad actor could silently inject prompt content into every
workspace the user applies. This is not unique to global config, but the
single-owner, set-and-forget nature of personal repos increases the practical
likelihood of undetected modification.

**Severity:** Low (same attack class as workspace CLAUDE.md). No new mitigation
is required, but the design should note the operational asymmetry: users of
global config should treat CLAUDE.global.md review as part of their normal
security hygiene, especially after `git pull` updates.

### 1.3 Race condition on `runConfigUnsetGlobal` clone directory deletion

`runConfigUnsetGlobal` performs:
1. Load config (reads `LocalPath`)
2. `os.RemoveAll(LocalPath)`
3. Clear `[global_config]` in config
4. Save config

If a concurrent `niwa apply` is running on the same machine (plausible on
multi-instance setups or CI/CD pipelines), it could be in the middle of
syncing or reading the global config clone directory when `RemoveAll` deletes
it. This produces an apply error at best (directory vanishes mid-read) and
potentially leaves the instance in an inconsistent state.

**Severity:** Low. This is a TOCTOU issue within a single-user trust boundary.
It is not exploitable by a remote attacker, but it is a operational hazard for
concurrent niwa usage. The design does not mention it.

**Mitigation:** Document the limitation. A simple advisory warning during
unset ("avoid running apply while unset is in progress") is sufficient for v1.
A lockfile mechanism would be a follow-on.

### 1.4 Workspace name collision in `GlobalConfigOverride.Workspaces`

`GlobalConfigOverride.Workspaces` is a `map[string]GlobalOverride` keyed by
workspace name. `ResolveGlobalOverride` performs a map lookup by
`ws.Workspace.Name`. Workspace names are validated by `validName` regex
(`^[a-zA-Z0-9._-]+$`) in the workspace config parser. However,
`GlobalConfigOverride` is a separate TOML struct, and the design does not
specify whether the workspace name keys in the global config are validated
against the same regex.

If an attacker can influence the global config TOML (e.g., by committing to a
compromised global config repo), they could include a workspace key whose name
collides with a legitimate workspace name in a non-obvious way (e.g., Unicode
homoglyphs, though TOML normalizes keys as UTF-8 strings). This is a narrow
vector but should be noted.

**Severity:** Very low. The attack requires controlling the global config repo,
which already allows arbitrary hook execution. The additional surface is
marginal. No new mitigation is needed beyond using the same `validName`
validation on global config workspace keys.

---

## Question 2: Are the Mitigations Sufficient for Identified Risks?

### 2.1 Arbitrary file write via global config TOML (`files` map)

The design correctly specifies parse-time validation and names the runtime
`checkContainment` as a second line of defense. The existing
`FilesMaterializer` does call `checkContainment` on both source
(materialize.go line 584) and destination (line 604) for single files, and
source (line 624) and destination (line 666) for directories. The defense-in-depth
pattern is sound.

**Gap (confirmed from phase 5, still open):** The parse-time check is specified
but not yet implemented. The design defers it to Block 1 without specifying
the exact function or test requirements. This is sufficient as a design
statement, but the implementation checklist should treat it as a blocking
requirement for Block 1.

**Assessment:** Sufficient if the parse-time check is implemented as described.
The dual-layer approach (parse + runtime) is correct.

### 2.2 Hook script source directory resolution

The design specifies: "The implementation must resolve global hook script paths
to absolute paths at merge time (inside `MergeGlobalOverride`) so that the
materializer requires no knowledge of which config directory a hook came from."

This is the correct resolution. Absolute path rewriting at merge time means the
`HooksMaterializer` source-side `checkContainment(src, ctx.ConfigDir)` check
at line 78 of materialize.go will fail for pre-resolved absolute paths if it
strictly checks containment within `ctx.ConfigDir` (the workspace config dir).

**Gap:** The design says destination containment checks remain in place, but
does not explicitly state what happens to the source-side `checkContainment`
check. If the implementation resolves to absolute paths, either:
(a) the source-side check must be skipped for pre-resolved absolute paths, or
(b) the check must be changed to validate containment within the path's own
config directory (which it cannot determine without additional metadata).

The design leaves this subtlety unresolved. A naive implementation that resolves
to absolute paths without adjusting the source-side check will produce confusing
"hook script ... is not within config directory" errors at apply time.

**Assessment:** Partially sufficient. The chosen approach (absolute path
resolution at merge time) is sound but requires a corresponding change to the
source-side containment check that the design does not specify. This is an
implementation detail that needs to be explicit in the design or deferred as a
known implementation constraint.

### 2.3 Credential exposure via `GlobalConfigOverride` TOML

The design notes that niwa does not add new transmission vectors and that error
paths must not log env var values. This is correct as stated. The existing
`EnvMaterializer` and `resolveClaudeEnvVars` do not log values. No new logging
is introduced by the proposed design.

**Assessment:** Sufficient. The constraint is correctly identified and the
existing codebase already satisfies it.

### 2.4 `config.toml` file permissions

The design specifies `0o600` for `SaveGlobalConfigTo`, which is correct. The
current implementation uses `os.Create`, which does not restrict permissions.
This is a known gap from phase 5.

**Assessment:** The mitigation is specified correctly in the design. Whether it
is implemented is an implementation concern, not a design deficiency. However,
the design section makes this sound optional ("should create") rather than
required. Given that the current `SaveGlobalConfigTo` already uses `os.Create`
(which is world-readable under many umasks), this should be stated as a
firm requirement.

### 2.5 Machine-level config tamper

The design's treatment of `os.RemoveAll` is accurate — it is within the
user's own trust boundary. No additional mitigation is needed.

**Assessment:** Sufficient.

---

## Question 3: Are Any Justifications Missing Depth?

### 3.1 "No new mitigation is needed" for arbitrary hook execution

The design states: "This risk is identical to workspace config today -- the
threat model is that the user owns and trusts their global config repo, just as
they own and trust their workspace config repo."

This justification is accurate but glosses over an important operational
difference. Workspace configs are team-owned repos, typically with branch
protections and multiple reviewers. A personal global config repo is owned and
maintained by one person, likely with no branch protections, no CI, and no
review process. The attack surface is not identical in practical terms even if
the threat model classification is the same.

The design would benefit from a sentence acknowledging this asymmetry and
advising users to enable branch protection or review settings on their global
config repo, rather than asserting the risk is "identical."

### 3.2 Sync failure abort semantics

The design says sync failure "must abort apply, same as workspace config sync
failure." The existing `SyncConfigDir` returns an error, which `runPipeline`
propagates. The design does not mention whether sync failure leaves the global
config clone directory in a partially-updated state (e.g., if `git pull` fails
mid-fetch). In the workspace config case this is also unaddressed, so the
omission is consistent with existing behavior, but it is worth noting that a
partial pull could leave the clone with an inconsistent state that persists
until the next successful sync.

**Assessment:** The justification is sufficient for v1. A follow-on could
address partial-state recovery, but it is not a blocking concern.

### 3.3 Plugin union semantics

The design explains the asymmetry between global plugin merge (union) and
repo override plugin merge (replace) and notes it must be documented to avoid
future contributor confusion. This is correct. However, the justification for
the union semantics does not address the security implication: global plugins
are user-controlled additions, and a user could inadvertently (or an attacker
could intentionally) union a malicious plugin into every workspace. The
workspace config's replace semantics give the workspace config owner a clean
way to enforce a specific plugin set; the global union semantics do not allow
that same enforcement.

The design notes this as a known limitation ("Plugin deduplication can be
added...") but does not frame the trust implication: workspace config cannot
override or remove global plugins. If a team wants to prevent users from adding
plugins via global config, they have no current mechanism to do so.

**Assessment:** The justification needs a sentence on the trust implication.
For v1, the limitation is acceptable (global config is user-owned and user
responsibility). But the design should state explicitly that workspace config
has no mechanism to block global plugin additions, so operators on shared
machines should use `--skip-global` if plugin control is required.

---

## Question 4: Residual Risk That Should Be Escalated

### 4.1 Hook script source-side containment check gap (medium, recommend escalation)

The source-side `checkContainment(src, ctx.ConfigDir)` in `HooksMaterializer`
assumes all hook script paths are relative to a single config directory. When
global hook scripts are resolved to absolute paths at merge time (as the design
specifies), either the source-side check breaks for those paths, or it must be
skipped. The design does not resolve this explicitly.

**Recommended action:** The design document should add a sentence clarifying
that absolute-path hook scripts bypass the source-side containment check and
explaining why this is acceptable (the file is already committed to a
user-trusted repo; the relevant safety property is the destination containment
check, not the source read). Without this clarification, an implementor
following the design document could either (a) omit the check silently, (b)
incorrectly add it and break global hooks, or (c) discover the gap during
review and delay implementation.

This is a design clarity issue with a direct implementation safety implication.
It warrants a design document update before Block 2 work begins.

### 4.2 No mechanism for workspace-level global config opt-out after init (low, informational)

An instance initialized without `--skip-global` cannot selectively opt out of
the global layer later without re-initialization. The design notes this as an
"intentional v1 constraint." This is acceptable, but it means that if a user's
global config repo is compromised after an instance is initialized, the only
remediation path is `niwa init --skip-global` on a new instance (existing
instances continue pulling from the compromised repo).

This is not an escalation-level risk for v1, but should be noted in the
Consequences section as an operational response consideration — compromised
global config repo requires re-initialization of all non-skip-global instances
to fully remediate.

---

## Summary Table

| Risk | Severity | Status | Action |
|------|----------|--------|--------|
| `files` destination parse-time validation missing | Medium | Open (unimplemented) | Blocking requirement for Block 1 |
| `Env.Files` parse-time validation not mentioned | Low-medium | Gap (new) | Add to parse-time validation scope |
| Hook source-side containment check after absolute path resolution | Medium | Design gap | Clarify in design before Block 2 |
| `config.toml` permissions `0o644` instead of `0o600` | Low | Open (unimplemented) | Firm requirement in SaveGlobalConfigTo |
| CLAUDE.global.md prompt injection via compromised repo | Low | Accepted (by design) | Add operational guidance |
| Plugin union allows user additions that workspace cannot block | Low | Accepted (by design) | Add trust implication note; advise --skip-global for controlled machines |
| Concurrent unset + apply race | Low | Not mentioned | Document limitation |
| No re-enable path for instances with SkipGlobal | Low | Intentional v1 constraint | Note in Consequences as remediation guidance |
| Workspace name key validation in global config | Very low | Gap (new) | Apply validName check in ParseGlobalConfigOverride |
