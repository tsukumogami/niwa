# Security Analysis: .env.example Integration Design

**Feature:** `.env.example` integration into niwa's env materialization pipeline
**Design phase:** Phase 5 — Security review
**Date:** 2026-04-18

---

## Scope

This analysis covers the four standard security dimensions as they apply to
the `.env.example` integration design. The feature adds a pre-pass in
`EnvMaterializer.Materialize` that reads `.env.example` from each managed
repo's working tree and writes classified values into `.local.env` (mode
`0600`) in the same working tree. No network access is added. No new
dependencies are introduced.

---

## Dimension 1: External Artifact Handling

### Does this dimension apply?

Yes. The feature reads `.env.example` files from managed app repos. These
repos may be maintained by third parties. The files are read from the local
filesystem (not from the network at apply time), but the content is
attacker-influenced if the repo author is adversarial.

### Risks identified

**1a. Path traversal via crafted key names**

The design writes values into `.local.env` only — a fixed path,
`filepath.Join(ctx.RepoDir, ".local.env")`, with no key-derived component.
However, the parser produces a `map[string]string` where the key is taken
verbatim from the file. If an env var name can contain path components
(`../../../.bashrc`), the key is stored in that map but never used as a
filesystem path. The write operation writes the entire output map as a flat
`KEY=VALUE` file; no key is concatenated into a path.

Risk assessment: **not exploitable for path traversal in the write path**,
provided the parser correctly validates key names. The existing `parseEnvFile`
does not validate key names. The new `parseDotEnvExample` function's spec does
not mention key validation either. Env var names containing `=`, `/`, or null
bytes are syntactically impossible in standard shell semantics, but the parser
could still pass through a key like `../evil` if it does not validate. Since
this key would land in a `KEY=VALUE` line in `.local.env` and not be used as a
path, there's no direct traversal risk in the current write path. The risk
becomes relevant if any future consumer iterates keys and treats them as
filenames — that consumer would need its own validation.

Mitigation: add key validation in `parseDotEnvExample` that rejects names
containing characters outside `[A-Za-z0-9_]`. Standard env var names are
restricted to this set; rejecting others is both correct per POSIX and
defensive. Emit a per-line warning for rejected names. This is low-effort and
closes the attack surface entirely.

**1b. Binary content / oversized files**

The design reads `.env.example` with no stated size limit. A crafted file
of several hundred MB would be read entirely into memory. The PRD states
"real-world files are small (typically under 2 KB)" but does not enforce
a budget.

Risk assessment: **low likelihood in practice** — managed repos are
developer-facing; a multi-MB `.env.example` is a clear anomaly. But niwa
runs unattended in CI contexts where a bad repo update could trigger this
without a human noticing.

Mitigation: add a file-size guard before `os.ReadFile` (e.g., stat the file;
if size exceeds 512 KB, emit a warning and skip). This is consistent with R22's
"whole-file failures emit a warning" policy and has essentially zero runtime
cost for the normal case.

**1c. Symlink attacks on the `.env.example` read path**

The design reads `.env.example` at `filepath.Join(ctx.RepoDir, ".env.example")`.
If the managed repo contains a `.env.example` that is a symlink pointing outside
the repo (e.g., `ln -s /etc/passwd .env.example`), niwa would read the symlink
target.

The existing `checkContainment` function resolves symlinks via
`filepath.EvalSymlinks` and is applied consistently to source paths in the
existing read paths (`FilesMaterializer`, `HooksMaterializer`, env files).
However, the design document and the decision records do not mention applying
`checkContainment` to the `.env.example` read. Since `.env.example` is read
directly by `parseDotEnvExample` at a fixed subpath of `ctx.RepoDir`, the
read itself is not path-traversal-dangerous in structure — but a symlink
redirecting it to an arbitrary file is a concern.

Risk assessment: **moderate**. An adversarial repo maintainer could redirect
`.env.example` to `/etc/passwd`, `/proc/self/environ`, or any world-readable
file. The contents would be parsed as key-value pairs, most lines failing to
parse as valid `KEY=VALUE`, some being silently skipped. Lines that do happen
to parse could land in `.local.env`. The secret-detection logic would run on
the parsed values, potentially emitting keys from the redirected file into
apply warnings (leaking key names) or into `.local.env` (leaking values if
they look like safe vars).

Mitigation: before reading `.env.example`, verify that the path is not a
symlink using `os.Lstat`. If `os.Lstat` returns `ModeSymlink`, skip the file
with a warning: "`.env.example` is a symlink; skipping for safety." An
alternative is to apply `checkContainment` after `filepath.EvalSymlinks` on
the resolved path to confirm it remains within `ctx.RepoDir`. Either approach
closes the attack vector. The symlink check is a one-liner and is consistent
with the defensive posture already established in `checkContainment`.

**1d. Null bytes and non-UTF-8 content**

The design does not mention encoding validation. A `.env.example` with null
bytes or non-UTF-8 content will be processed by Go's string handling, which
treats bytes as-is. Null bytes in key or value strings would produce a
`.local.env` with null bytes, which most consumers would misparse. This is
unlikely to be exploitable beyond causing parse confusion in downstream tools.

Risk assessment: **very low**. If the parser rejects keys that fail the
`[A-Za-z0-9_]` character class check (mitigation for 1a), this is
automatically closed for keys. For values, null bytes in `.local.env` would be
unusual but not a security issue beyond potential downstream parser confusion.

### Severity summary for Dimension 1

| Risk | Severity | Has mitigation? |
|------|----------|-----------------|
| Path traversal via key names | Low (not currently exploitable in write path) | Recommend key validation |
| Oversized files | Low | Recommend size guard |
| Symlink redirect on `.env.example` read | Moderate | Needs explicit symlink check |
| Null bytes / non-UTF-8 | Very low | Addressed by key validation |

---

## Dimension 2: Permission Scope

### Does this dimension apply?

Yes, in two respects: the permission of the output file (`.local.env`) and
the permission model for reading (`.env.example`).

### Risks identified

**2a. Output file permissions**

`.local.env` is written with `secretFileMode` (`0600`). This is correct and
consistent with the existing materializer behavior. No risk.

**2b. Directory creation permissions**

`.local.env` is written to `ctx.RepoDir`, which already exists (it's a cloned
repo). No directory creation is needed for the output file itself. No
`os.MkdirAll` is called for the env output path. No escalation risk.

**2c. Read permissions on `.env.example`**

`parseDotEnvExample` reads a file from a path the user's process can
already access (the cloned repo is under the instance root, created by the
process itself). No elevated permissions are used. Per R22, unreadable files
are warnings, not errors, so `EACCES` on `.env.example` is handled gracefully.

**2d. File descriptor leaks**

`os.ReadFile` opens, reads, and closes the file in a single call — no explicit
`os.Open`/`file.Close` pair to leak. No risk.

**2e. Privilege escalation via env injection**

Env variables materialized into `.local.env` are not evaluated or executed by
niwa itself. They are written for the user to source or for their tooling to
consume. This is the same trust model as the existing materializer: niwa
writes the file; the user decides when and how to source it. The existing
guardrail (R13, public-repo guardrail) is specifically designed to prevent
probable secrets from landing in `.local.env` from public repos.

The only new angle here is that `.env.example` values are third-party-controlled
in a way that inline `[env.vars]` is not. The classification system (entropy +
prefix blocklist) is the intended mitigation for this. Values that pass
classification and are written to `.local.env` are treated as non-secret vars
by design (they came from an example file). Users sourcing the file would
receive these values as env vars in their shell, but this is equivalent to
manually copying `.env.example` to `.env.local` — the conventional developer
workflow this feature automates.

Risk assessment: **accepted by design**. The existing pattern is to trust the
workspace maintainer's configuration; this extends that trust to the managed
app repo's example file, scoped by the classification system.

### Severity summary for Dimension 2

No unmitigated risks. File permission model is correct. No directory creation
needed. No privilege escalation path beyond the intended design.

---

## Dimension 3: Supply Chain and Dependency Trust

### Does this dimension apply?

Partially. The design adds no new Go dependencies (confirmed: stdlib only).
However, the trust model for the content being processed changes.

### Risks identified

**3a. No new dependencies**

The design explicitly rules out vendoring `godotenv` or `gotenv`. The new
`parseDotEnvExample` function is ~50 LOC of stdlib Go. There is no new package
import, no new binary vendored, no new network fetch. From a traditional supply
chain perspective (compromised dependency, typosquat, transitive vulnerability),
this dimension is not applicable.

**3b. Trust boundary shift: managed app repos as input sources**

This is the substantive supply chain concern. Today, niwa's env materialization
reads from:
1. The workspace config repo (controlled by the workspace maintainer).
2. Static env files declared in `[env.files]` (also in the config repo).
3. Discovered per-repo env files (also placed by the maintainer, in the config
   dir).

With this feature, the pipeline also reads from:
4. `.env.example` files in managed app repos (potentially controlled by third
   parties).

The managed app repos are cloned from GitHub. They may be public repos with
external contributors, or private repos maintained by a different team.
A malicious commit to a managed repo could install a `.env.example` designed
to:
- Inject safe-looking env vars that override legitimate ones (blocked by R5:
  workspace wins on collision — but only for keys that workspace has declared;
  undeclared keys flow through).
- Include a probable-secret value for a known-harmless key name to attempt a
  bypass of the classification logic.
- Include a symlink (see 1c) to redirect the read to sensitive system files.

The per-repo opt-out (`read_env_example = false`) at R18 is the explicit
trust-boundary mitigation for untrusted repos, and it is highlighted in User
Story 5. The design correctly names this risk and provides an operator
control.

However, the default is `read_env_example = true` for all repos, including
newly-discovered repos that the workspace maintainer may not have audited. This
is a deliberate trade-off (PRD "Decision: feature defaults to on") that favors
developer convenience. The security implication is that a workspace maintainer
who adds a new org repo to discovery without reviewing its `.env.example` will
get its vars materialized on the next apply.

Risk assessment: **low-to-moderate, accepted by design with documented
operator mitigation**. The opt-out mechanism is the correct tool. The design
should make the trust-boundary risk explicit in operator-facing documentation
and potentially in the apply warning for undeclared keys.

**3c. Classification system as trust enforcement**

The `classifyEnvValue` function in `envclassify.go` is the gatekeeper between
managed-repo content and the local env file for undeclared keys. The
correctness of the entropy calculation and prefix lists directly affects
security. The design describes these as package-level `var` slices, which
means they are part of the binary's compiled state — not configurable at
runtime — and can only be changed by submitting a PR. This is the right model
for security-critical lists.

Shannon entropy as the sole probabilistic signal has known limitations: some
randomized-looking values will score below 3.5 bits/char (e.g., base32-encoded
tokens), and some readable strings score above (e.g., dense URLs). The chosen
threshold (3.5) is calibrated higher than truffleHog's default (3.0) to reduce
false positives on readable defaults. This trade-off accepts some false negatives
(missed secrets that look like readable text). The PRD acknowledges this in
Known Limitations.

Risk assessment: **inherent limitation of probabilistic detection, acknowledged
and documented**. Not a design flaw. The correct response for any value the user
knows is safe (or secret) is explicit declaration, which bypasses detection.

### Severity summary for Dimension 3

| Risk | Severity | Has mitigation? |
|------|----------|-----------------|
| New dependencies | N/A | No new deps |
| Trust boundary: managed repos | Low-to-moderate | Per-repo opt-out (R18); apply warnings |
| Classification correctness | Low (probabilistic limits accepted) | Entropy + prefix list + allowlist |

---

## Dimension 4: Data Exposure

### Does this dimension apply?

Yes. This is the most substantive dimension for this feature, because the
design explicitly handles values that may be secrets.

### Risks identified

**4a. Warning messages exposing value fragments**

The design emits per-key warnings for undeclared safe keys:
`warn: <repo>: undeclared key <key> read from .env.example, treating as var`

This format includes only the key name, not the value. That is correct for
safe values. The risk is in the probable-secret error path.

The design says probable-secret errors are "collected across all repos; apply
fails at the end with a summary." The PRD says "error MUST name the offending
file and line." If the error message includes the value (or even the entropy
score with a fragment), it exposes potentially secret material on stderr.

The existing `offendingKeys` function in `guardrail/githubpublic.go` is
explicitly designed to include only key names, not values (with a comment
citing PRD R22: "diagnostics never contain secret bytes"). The design does not
explicitly state the same constraint applies to the new classification error
messages.

Risk assessment: **moderate risk if not explicitly implemented**. The design
inherits the R22 requirement but does not restate it for the new code paths
in `envclassify.go` or the per-repo guardrail error path. Implementation of
`classifyEnvValue` could inadvertently include the value (e.g., for debugging)
in warning strings returned alongside the `isSafe bool`.

Mitigation: explicitly require in the implementation spec that:
1. The `reason string` returned by `classifyEnvValue` MUST NOT include the
   value, any fragment of the value, or the entropy score applied to the value.
2. Error messages for probable-secret failures MUST include the repo name,
   file, line number, key name, and reason (entropy/prefix), but NOT the value.
3. Test assertions verify that neither the value text nor any substring
   appears in the captured stderr output.

**4b. High-entropy values in plain text on stderr via per-line parse warnings**

R7 states: "emit a warning naming the line and the unsupported construct."
If the warning includes the full line text (e.g., to help the user identify
which line is problematic), it would expose the raw `.env.example` content —
including any values — on stderr. This is inconsistent with R22.

Risk assessment: **low but worth addressing**. `.env.example` values are
intended to be non-secret stubs, but some repos may have committed actual
secrets accidentally. Echoing raw file lines to stderr broadens the exposure.

Mitigation: per-line parse warnings should include the file path, line number,
and a description of the syntax problem, but NOT the raw line content. Example:
`warning: <file>:<line>: unmatched quote in value; skipping line.`

**4c. Public-repo guardrail logic (R13) correctness**

R13 requires: when a managed repo's git remote is public GitHub AND its
`.env.example` has an undeclared probable-secret value, fail apply.

The existing guardrail in `apply.go` calls `enumerateGitHubRemotes` against
`configDir` (the workspace config repo). R13 requires a separate call against
`ctx.RepoDir` (each managed repo), inside the materializer loop. The design
(Decision 2, step 6) explicitly describes this as a "per-repo public-remote
guardrail against probable-secret keys."

Two questions arise:

First: the check runs after classification. A key that passes classification
(below entropy threshold, no blocklist match) is materialized without the
per-repo guardrail firing. Only probable-secret keys trigger R13. This means a
low-entropy value in a public repo's `.env.example` — even one the workspace
maintainer hasn't declared — lands in `.local.env` regardless of the repo's
visibility. This is the correct behavior per the design (the value is classified
as safe), but implementers should confirm the intent is understood: "public
repo" is not itself a reason to block materialization; the combination of
"public repo" + "probable secret" is.

Second: `enumerateGitHubRemotes` calls `git -C <dir> remote -v` as a
subprocess. When `ctx.RepoDir` is the path to a managed repo, this is safe —
the repo is already cloned (step 3 of the pipeline runs before step 6.5).
However, if `ctx.RepoDir` does not have a git working tree for any reason
(e.g., a partially failed clone), `git remote -v` will fail gracefully and
`enumerateGitHubRemotes` will return `haveGit=false`. Per the existing
`CheckGitHubPublicRemoteSecrets` design, `haveGit=false` causes the guardrail
to emit a warning and skip. This means a partially-cloned public repo would
not be guarded — but it also wouldn't have a `.env.example` to read, so the
pre-pass would also find nothing. The failure modes cancel.

Risk assessment: **low**. The guardrail logic is correctly scoped. The partial-
clone edge case is benign. The public-vs-private distinction is correctly applied
to the managed repo, not the config repo.

**4d. `.local.env` written to `ctx.RepoDir` (managed repo working tree)**

The design writes `.local.env` into the managed app repo's working tree. The
PRD and design explicitly state this is the intended behavior (the file is
`.gitignore`'d by the managed repo or covered by `*.local*` patterns niwa
installs). However, the design also notes "niwa never writes to managed app
repos" as a principle. Writing `.local.env` into the managed repo's directory
is technically writing into the managed repo's working tree — it is not writing
into the managed repo's git history.

This is the existing behavior for the env materializer (today, `.local.env` is
already written to each managed repo's directory). The new feature doesn't
change this model; it changes what content flows into that file. No new risk
compared to the existing implementation.

**4e. `EnvExampleVars` on `MaterializeContext` persists across materializer calls**

The design adds `EnvExampleVars map[string]string` and
`EnvExampleSources []SourceEntry` to `MaterializeContext`. Per the existing
pattern, `MaterializeContext` is constructed per-repo in `apply.go`'s
materializer loop (a fresh `mctx` is created for each `cr` in `classified`).
The nil-init design means there's no cross-repo contamination — each repo gets
its own `mctx` with its own `EnvExampleVars`.

Risk assessment: **none**. The per-repo context construction already prevents
cross-repo data leakage. The new fields follow the existing convention.

**4f. Secret-table exclusion check timing**

R4a requires the exclusion check run against the "fully-merged config (base
workspace + overlay + global override applied)." The design places the pre-pass
in `EnvMaterializer.Materialize`, which receives `ctx.Effective` — the
post-merge, post-resolve effective config. This is the correct timing. Values
that a personal overlay declares as `[env.secrets]` will be in `ctx.Effective`
and correctly excluded.

One subtle concern: the design checks `ctx.Effective.Env.Secrets.Values` and
`ctx.Effective.Claude.Env.Secrets.Values` to build the exclusion set. The PRD
(R4a) also enumerates `[env.secrets.required]`, `[env.secrets.recommended]`,
`[env.secrets.optional]`, and their per-repo equivalents. The design decision
references "any `[env.secrets.*]` table" without specifying how sub-tables are
accessed. If the exclusion set is built only from the flat `.Values` map and
not from sub-tables (required, recommended, optional) explicitly, a key
declared only in `[env.secrets.required]` but not in the flat `[env.secrets]`
could slip through the exclusion check and receive a stub value from
`.env.example`.

Risk assessment: **moderate if not explicitly implemented correctly**. The
PRD is clear about the intent. The implementation spec must explicitly enumerate
all sub-table paths when building the exclusion set. This is a correctness issue
with direct security implications: a missing required secret silently satisfied
by a stub value defeats the `.required` contract.

Mitigation: the exclusion-set construction logic should mirror `offendingKeys`
in scope — walk the same paths it does (workspace-level secrets, per-repo
secrets, instance override secrets, and all sub-tables). A shared helper or a
documented contract referencing the same walk would make this explicit.

### Severity summary for Dimension 4

| Risk | Severity | Has mitigation? |
|------|----------|-----------------|
| Value fragments in warning/error messages | Moderate | Needs explicit R22 enforcement |
| Raw line content in parse-error warnings | Low | Recommend line-content exclusion |
| Public-repo guardrail correctness (R13) | Low (design is correct) | No gaps found |
| Cross-repo data leakage via mctx | None | Per-repo mctx construction |
| Sub-table exclusion check completeness | Moderate | Needs explicit implementation spec |
| `.local.env` in managed repo working tree | None (existing behavior) | N/A |

---

## Overall Assessment

No single finding in this analysis represents a critical or blocking flaw. The
design's foundations — existing `checkContainment` with symlink resolution,
mode-`0600` output, per-repo `mctx` isolation, and opt-out mechanisms — are
sound. The design inherits the security posture of the existing materializer
pipeline.

Three findings warrant explicit action before or during implementation:

1. **Symlink check on `.env.example` read (Dimension 1c, moderate):** The read
   path does not apply `checkContainment` or an `Lstat` symlink check. Add an
   explicit check before `parseDotEnvExample` reads the file.

2. **R22 enforcement in classification error messages (Dimension 4a, moderate):**
   The `classifyEnvValue` return value and the per-repo guardrail error path
   must not include value text. This needs to be stated explicitly in the
   implementation spec and verified in tests.

3. **Sub-table completeness of secrets exclusion set (Dimension 4f, moderate):**
   The exclusion set must walk all `[env.secrets.*]` sub-tables (required,
   recommended, optional) at workspace, per-repo, and instance layers — not
   only the flat `.Values` map. Failure here would allow stub values to satisfy
   required-secret contracts.

Two additional findings are low-severity and should be documented as
implementation guidelines:

4. **Key name validation in parser (Dimension 1a, low):** `parseDotEnvExample`
   should validate key names against `[A-Za-z0-9_]` and warn on invalid names.

5. **File size guard (Dimension 1b, low):** A size guard before `os.ReadFile`
   (e.g., 512 KB) prevents unexpected memory allocation from crafted inputs.

The trust-boundary shift in Dimension 3 is an inherent consequence of the
feature's design goal. The per-repo opt-out mechanism is the appropriate
mitigation; it should be prominently documented for workspace maintainers
who manage repos with external contributors.

---

## Recommended Outcome

**OPTION 1 — Design changes needed** (three items), combined with
**OPTION 2 — Document considerations** (for the trust-boundary and probabilistic
detection limitations).

The three moderate findings (symlink check, R22 in error messages, sub-table
exclusion) are concrete enough to state as implementation requirements. They do
not require a redesign — they are pre-existing patterns in the codebase
(`checkContainment`, the `offendingKeys` value-exclusion convention) that the
implementation must explicitly apply to the new code paths.

---

## Draft Security Considerations Section (for design doc)

```markdown
## Security Considerations

### Input validation

`parseDotEnvExample` reads files from managed app repos, which may be
controlled by third parties. Three defenses apply before values reach the
classification or write paths:

- **Symlink check.** Before reading `.env.example`, niwa calls `os.Lstat` to
  detect whether the path is a symlink. If it is, niwa skips the file with a
  warning and treats the repo as having no `.env.example`. This prevents a
  crafted symlink from redirecting the read to sensitive system files.
- **Key name validation.** `parseDotEnvExample` rejects keys whose names
  contain characters outside `[A-Za-z0-9_]`. Invalid key names emit a per-line
  warning and are not included in the output map.
- **File size guard.** Files larger than 512 KB are skipped with a warning.
  Real-world `.env.example` files are under 2 KB; an oversized file is treated
  as a whole-file failure per R22.

### No value text in diagnostic output

All warning and error messages produced by the classification system, the
per-repo public-remote guardrail, and per-line parse errors MUST include only
the file path, line number, key name, and a reason description. No value text,
value fragment, or entropy score applied to a value may appear in any diagnostic
output. This extends the existing PRD R22 requirement to all new code paths in
`envclassify.go` and the per-repo guardrail.

### Secrets exclusion set

The set of keys excluded from `.env.example` materialization is built from all
`[env.secrets.*]` sub-tables at every config layer: workspace-level, per-repo,
and instance overrides, including the `required`, `recommended`, and `optional`
sub-tables. Flat `[env.secrets]` values are included. The exclusion walk mirrors
the scope of `offendingKeys` in `internal/guardrail/githubpublic.go`. A key
absent from any secrets table but present in `.env.example` may receive the
`.env.example` value as an implicit var, subject to classification.

### Trust boundary

`.env.example` files originate from managed app repos, which may have external
contributors not under the workspace maintainer's control. Workspace maintainers
who manage third-party repos or repos with untrusted contributors should set
`[repos.<n>] read_env_example = false` for those repos. The per-repo opt-out
disables `.env.example` discovery entirely for the named repo; workspace intent
(inline `[env.vars]` and `[env.secrets]`) remains the sole env source.

The default of `read_env_example = true` applies to all repos including
newly-discovered ones. Workspace maintainers adding a new repo to org-level
auto-discovery should review that repo's `.env.example` — or explicitly opt it
out — before the next `niwa apply`.

### Classification limitations

The entropy-based secret detector (3.5 bits/char threshold) has known
false-negative cases: structured secrets such as base32-encoded tokens or JWT
payloads may score below the threshold and be treated as safe values. The
known-prefix blocklist catches common vendor token patterns. For any key the
workspace maintainer knows is sensitive, declaring it explicitly as
`[env.secrets]` bypasses probabilistic detection entirely and is the recommended
path.
```
