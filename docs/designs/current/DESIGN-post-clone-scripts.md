---
status: Current
problem: |
  niwa clones repos during create/apply but can't run repo-provided setup scripts
  afterward. Some repos need git hooks installed, local config generated, or dev
  environments bootstrapped. Today this requires a separate manual step or an
  external installer. niwa should support running a repo's own setup scripts as
  part of the apply pipeline.
decision: |
  Scan a configurable setup directory (default: scripts/setup/) in each repo for
  executable scripts and run them in lexical order. Workspace-level config changes
  the directory name; per-repo override changes or disables. Script stdout and
  stderr stream to niwa's output prefixed with the repo and script name, in both
  TTY and non-TTY runs, scrubbed through the apply's secret redactor. Non-zero exit
  codes produce warnings, not fatal errors, and the apply still exits 0 -- but the
  count of repos whose setup did not finish is printed as a verdict line directly
  below the created/applied summary.
rationale: |
  A directory convention is more extensible than a single file -- repos can split
  setup across multiple scripts without merge conflicts or monolithic files. The
  generic directory name (scripts/setup/) doesn't imply niwa ownership, so repos
  can use the same convention with or without niwa. Lexical ordering via numeric
  prefixes (01-git-hooks.sh, 02-build.sh) follows established Unix patterns.
---

# DESIGN: Post-Clone Scripts

## Status

Current

Amended 2026-08-08 -- see "Amendment 2026-08-08: setup-script visibility" at the end
of this document. The amendment corrects two claims in Decision 2 and Security
Considerations that the implementation never satisfied, and narrows Decision 2's
failure handling. (This section previously read "Proposed" while the frontmatter
said `status: Current`; the frontmatter was right.)

## Context and Problem Statement

niwa's apply pipeline clones repos, installs content (CLAUDE.md hierarchy), and
runs materializers (hooks, settings, env, files). But some repos need additional
setup that's specific to the repo itself -- installing git hooks via a repo-provided
script, generating local configuration files, or bootstrapping development tooling.

Today these setup steps happen outside niwa: either manually by the developer, or
through a separate installer script. This breaks the "one command to set up the
workspace" promise. niwa should be able to run a repo's own setup scripts as part
of the apply pipeline, after cloning and materialization are complete.

This is distinct from niwa's materializers, which distribute config FROM the
workspace config repo TO target repos. Post-clone scripts run code that lives
INSIDE the target repo.

## Decision Drivers

- Convention over configuration: repos that follow the convention need zero config
- Non-intrusive: the convention should use generic names, not niwa-specific ones
- Extensible: repos with multiple setup concerns shouldn't be forced into one file
- Non-destructive: a failing setup script shouldn't block the entire workspace apply
- Idempotent: scripts must be safe to re-run on every `niwa apply`
- Per-repo control: some repos may need a different setup path or no setup at all

## Considered Options

### Decision 1: Script discovery convention

How niwa discovers which scripts to run in each repo after cloning/applying.

#### Chosen: Setup directory with lexical ordering

niwa scans a directory in each repo for executable scripts and runs them in
lexical order. The default directory is `scripts/setup/`. Repos organize setup
into as many scripts as they need; each runs independently.

```
myrepo/
  scripts/
    setup/
      01-git-hooks.sh
      02-install-deps.sh
      03-generate-config.sh
```

**TOML configuration:**

```toml
[workspace]
setup_dir = "scripts/setup"    # default, can be omitted

[repos.legacy-app]
setup_dir = "setup/bootstrap"  # different directory for this repo

[repos.static-site]
setup_dir = ""                 # disable for this repo
```

**Convention details:**
- Default directory: `scripts/setup/`
- Scripts must be executable (`chmod +x`)
- Non-executable files are skipped with a warning
- Empty directory or missing directory: silently skipped
- Lexical order: `01-foo.sh` runs before `10-bar.sh` (numeric prefix convention)
- Only top-level files are scanned (no recursive descent into subdirectories)
- Empty string on per-repo override explicitly disables

**Why this approach:**
- Generic name (`scripts/setup/`) doesn't imply niwa ownership -- repos can use
  this convention with any tool or manually
- Multiple scripts compose naturally -- add a file, get a new setup step
- Lexical ordering via numeric prefixes is a well-established Unix pattern
  (`/etc/cron.d/`, `/etc/init.d/`, `run-parts`)
- Repos with a single setup step just put one script in the directory

#### Alternatives Considered

**Single well-known file (`scripts/niwa-setup.sh`):** One file, one entry point.
Rejected because (1) the `niwa-` prefix is intrusive -- repos shouldn't need
niwa-specific files, (2) a single file doesn't compose -- repos with multiple
setup concerns end up with a monolithic script that grows over time, and (3)
multiple contributors editing the same file creates merge conflicts.

**Hybrid entry point + directory (`setup.sh` + `setup.d/`):** A single entry
point runs first, then directory scripts. Rejected because it adds complexity
without clear benefit -- if you have an entry point, you'll put everything there
and the directory becomes an afterthought. One convention is simpler than two.

**Single configurable file path:** Workspace declares a file path, niwa runs
that one file. Most flexible for per-repo override, but the same extensibility
problem as the single-file approach -- one file doesn't compose.

### Decision 2: Execution semantics

How scripts are invoked, what environment they receive, and how failures are
handled.

#### Chosen: Run from repo root, warn on failure, stop on first script error

> **Update 2026-08-08 -- output and failure reporting.** The stdout/stderr bullet
> below described behavior the implementation never had: script output was routed
> through `Reporter.Status`, which is a no-op off a TTY and transient-then-erased on
> one, so nothing a script wrote ever reached the operator in either mode. The
> sample output below was likewise aspirational. Both are corrected here to what
> the code now does. Decision 2's *failure policy* is unchanged in substance --
> stop-on-first-error within a repo, continue to the next repo, warn rather than
> abort -- but it is narrowed: the outcome is now also counted in a verdict line
> below the summary, so a failure is discoverable without reading the warning
> stream. The exit code deliberately stays 0. See the Amendment section at the end
> of this document for the reasoning and for the deferred `setup_policy` mechanism.

Scripts are executed with:
- Working directory: the repo root
- Invocation: direct execution (script's shebang determines interpreter)
- Environment: inherits the niwa process environment
- Stdout/stderr: streamed line by line to niwa's output, prefixed
  `[<repo>/<script>]`, in both TTY and non-TTY runs. Each line has ANSI and OSC
  escapes stripped and is then scrubbed through the apply's secret redactor
- Announcement: `running setup script <repo>/<script>` is printed before each
  script executes
- Exit code 0: success
- Exit code non-zero: warning printed, **remaining scripts for that repo are
  skipped**, pipeline continues with next repo, and the repo is counted in the
  verdict line below the apply summary

```
running setup script tsuku/01-git-hooks.sh
[tsuku/01-git-hooks.sh] Installing git hooks... done.
running setup script tsuku/02-install-deps.sh
[tsuku/02-install-deps.sh] added 4 packages in 1s
running setup script koto/01-git-hooks.sh
[koto/01-git-hooks.sh] error: .githooks not found
applied ws (3 repos)
setup incomplete for 1 repo: koto
warning: setup script scripts/setup/01-git-hooks.sh failed for koto: exit status 1
```

Repos with no setup directory produce no output at all; a missing or empty
directory is still silently skipped.

**Why stop-on-error per repo:** If `01-git-hooks.sh` fails, running
`02-install-deps.sh` may not make sense (scripts often have implicit ordering).
But one repo's failure shouldn't block other repos from setting up. This gives
fail-fast within a repo and resilience across repos.

**Why shebang, not `sh -e`:** Scripts choose their own interpreter via shebang
(`#!/bin/bash`, `#!/usr/bin/env python3`). This is more flexible than forcing
`sh -e`, and aligns with how executable scripts work everywhere else.

#### Alternatives Considered

**Fatal on any failure:** Stop the entire apply pipeline if any script fails.
Rejected because it makes the pipeline fragile -- one repo's broken script
blocks all other repos.

**Continue all scripts on failure:** Run every script regardless of exit codes,
report all failures at the end. Rejected because scripts within a repo often
have ordering dependencies -- running later scripts after an earlier failure
can produce confusing results.

## Decision Outcome

Post-clone scripts use a directory-based convention: niwa scans
`scripts/setup/` (configurable) in each repo for executable scripts and runs
them in lexical order. The directory name is generic and not niwa-specific, so
repos can use the same convention independently.

Failures stop remaining scripts for that repo but don't block other repos.
The workspace-level `setup_dir` changes the default directory. Per-repo overrides
change or disable with empty string. Missing or empty directories are silently
skipped.

## Solution Architecture

### Overview

A new pipeline step runs after materializers (Step 6.5) and before managed file
tracking (Step 7). It iterates classified repos, resolves the setup directory
path, scans for executable scripts, and runs each in lexical order.

### Components

**`WorkspaceMeta.SetupDir`** -- new optional field on workspace metadata.
Defaults to `scripts/setup` when empty.

**`RepoOverride.SetupDir`** -- new optional field. When set, overrides the
workspace default for that repo. Empty string disables. Uses `*string` to
distinguish "not set" from "explicitly empty."

**`RunSetupScripts`** -- new function that scans a directory for executable
scripts and runs them in lexical order.

### Key Interfaces

```go
// In config.go
type WorkspaceMeta struct {
    // ... existing fields ...
    SetupDir string `toml:"setup_dir,omitempty"`
}

type RepoOverride struct {
    // ... existing fields ...
    SetupDir *string `toml:"setup_dir,omitempty"`
}
```

```go
// In setup.go
type SetupResult struct {
    RepoName string
    Scripts  []ScriptResult
    Skipped  bool  // directory not found
    Disabled bool  // explicitly disabled
}

type ScriptResult struct {
    Name   string
    Error  error  // nil = success
}

func RunSetupScripts(repoDir, setupDir string) *SetupResult
```

### Data Flow

```
For each classified repo:
    |
    +-- Resolve setup directory:
    |     repo override (*string set) -> use override value
    |     repo override (nil)         -> use workspace default
    |     workspace default empty     -> use "scripts/setup"
    |
    +-- Is resolved path ""?
    |     yes -> skip (disabled)
    |
    +-- Does directory exist at repoDir/setupDir?
    |     no  -> skip silently
    |
    +-- Scan for executable files (top-level only, lexical order)
    |     none found -> skip silently
    |
    +-- For each script in order:
          execute (cwd = repoDir)
          exit 0   -> continue to next script
          exit !0  -> warn, skip remaining scripts for this repo
```

## Implementation Approach

### Phase 1: Config types and resolution

- Add `SetupDir string` to `WorkspaceMeta`
- Add `SetupDir *string` to `RepoOverride`
- Add resolution function (repo override -> workspace default -> "scripts/setup")
- Tests for config parsing and resolution

### Phase 2: Script execution and pipeline integration

- Implement `RunSetupScripts` (scan dir, filter executable, run in order)
- Add Step 6.75 to apply pipeline
- Print progress lines per repo and per script
- Tests: scripts exist and succeed, directory missing (skip), script fails
  (warn + skip remaining), disabled via empty string, non-executable file
  (warn + skip), empty directory (skip)

## Security Considerations

Post-clone scripts run arbitrary code from the cloned repo. This is inherently
trusted -- the user chose to clone the repo, and the scripts are part of the
repo's codebase. niwa surfaces what it's about to run (prints each script name
before execution) so the user can verify.

The security boundary is the same as `git clone` itself: if you clone a repo, you
trust its contents. niwa doesn't elevate privileges -- scripts run as the current
user with the current environment.

Mitigations:
- Only executable files are run (non-executable are warned, not silently executed)
- No recursive descent into subdirectories (limits discovery scope)
- Script paths are validated to stay within the repo directory

### Secret exposure through printed script output

> **Update 2026-08-08.** The claim above that niwa "prints each script name before
> execution" was not true when written; it is true now (`running setup script
> <repo>/<script>`). This subsection is new, because printing a script's output is
> a genuinely new exposure path rather than a widening of an existing one -- until
> this amendment, everything a setup script wrote was discarded.

Setup scripts run after the materializer stage, with the repo root as their working
directory. That means `.env.local` (or whichever secret-output target the workspace
declares) is sitting in the script's cwd, written by niwa one pipeline step earlier.
A script that does `set -a; . ./.env.local` and then `set -x` -- an entirely ordinary
thing for a setup script to do -- will echo secrets that niwa now prints to a
terminal, a dispatch log, or a CI log.

Secrets reach setup scripts through **files only**. niwa never sets `cmd.Env` for a
setup script and exports nothing into its own process environment, so there is no
`env`-borne route to a niwa-managed secret; the residual environment exposure is
whatever the operator exported before invoking niwa.

The mitigation is to scrub every emitted line through the redactor the apply already
constructs, before the line is printed. The fragment set at the time setup scripts
run is exactly the set of values materialized into the repo working trees one step
earlier, which is the best available alignment between what the redactor knows and
what a script can read.

Redaction is a mitigation, not a control. Four classes of leak survive it, and repo
authors should not treat scrubbed output as safe to publish:

1. **Only values niwa itself resolved are scrubbed.** A script that runs a
   credential helper, reads a keychain, queries a cloud metadata endpoint, or sources
   an unmanaged `.env` and echoes the result is covered by nothing -- the redactor has
   never seen those bytes.
2. **Only verbatim re-emission is caught.** Matching is plain substring against raw
   plaintext, so base64, URL-encoding, JSON-escaping, and shell-quoting all defeat it.
   Dotenv output is unquoted, so `cat .env.local` is caught; a JSON-format
   secret-output target escapes its values and can emit a form that is not matched.
3. **Multi-line secrets are not caught at all.** Scrubbing is per line, and dotenv
   marshalling does no quoting, so a PEM or SSH private key lands in the file with
   real newlines. No single emitted line contains the whole registered value, so
   nothing is redacted. This is the sharpest limit, and it applies to precisely the
   secret type an operator would most regret leaking.
4. **Secrets shorter than the redactor's minimum fragment length are never
   redacted.** The redactor's own documentation asserts such values are rejected at
   resolution time with a hard error; the resolver states the opposite and implements
   the permissive behavior. The gap predates this amendment, but printing script
   output is what makes it reachable. Tracked separately against the secret
   subsystem; not fixed here.

A legitimate value that happens to match a registered secret renders as the
redactor's placeholder. When that happens the fix is to move the value out of the
secrets table rather than to disable scrubbing -- there is no opt-out, matching every
other place niwa scrubs subprocess output.

## Consequences

### Positive

- "One command to set up the workspace" becomes achievable
- Repos that follow the convention need zero config
- Multiple setup concerns compose naturally (one script per concern)
- Generic directory name works with or without niwa
- Idempotent execution means apply always converges

### Negative

- Lexical ordering requires discipline (numeric prefixes)
- Adding a script to the directory silently changes behavior on next apply
- No structured output from scripts -- niwa can only report pass/fail

### Mitigations

- Numeric prefix convention is well-documented and widely understood
- niwa prints each script name before execution, so changes are visible
- Per-repo disable provides an escape hatch when auto-execution is unwanted

## Amendment 2026-08-08: setup-script visibility

A repo's setup script could fail on every `niwa create` and `niwa apply` for months
without anyone finding out. Two things combined. The script's own explanation of why
it failed was discarded before it reached the terminal, contrary to what Decision 2
above already promised. And the failure surfaced only as one deferred warning line,
printed below a summary that said the apply succeeded, with the command exiting 0.

The output half was a plain implementation defect, and an unusually clear one:
`DESIGN-clone-output-ux.md`, the design that introduced the helper setup scripts run
through, specifies `reporter.Log` for setup-script output in four places and
`r.Status` in exactly one -- a Go doc stub in its Key Interfaces block. The
implementation copied that stub verbatim, comment and all, including the line
asserting that script output is silent in piped and CI contexts. No rationale for
`Status` is recorded anywhere in either design. Restoring `Log` finishes an
implementation rather than reversing a decision. That stub has been corrected in
`DESIGN-clone-output-ux.md` so the two documents stop disagreeing.

The exit-code half was a real design question, and it is answered here rather than
left in code.

### Decision A: How setup-script output reaches the operator

**Chosen: stream every line through `Reporter.Log`, prefixed `[<repo>/<script>]`,
with a durable per-script announcement.**

Nothing is buffered. `runCmdWithReporter` routes each scanned line through `Log`
instead of `Status`; `RunSetupScripts` emits `running setup script <repo>/<script>`
before each `exec.Command` and supplies the prefix. Off a TTY the rendering is
identical minus the spinner. On a TTY the spinner is torn down once per script, not
once per line, because `Log` stops it and the next `Status` call restarts it.

The prefix carries the repo name rather than the group because repo names are unique
within a workspace -- `ResolveSetupDir` looks repos up by bare name. If that ever
stops holding, the prefix becomes `[<group>/<repo>/<script>]`; the group is already
in scope at the call site.

No cap or truncation is applied. Measured by script class, a real git-hooks installer
emits 2 lines, `npm install` 4, `pip install` 20, and a deliberately verbose
`go mod download -x` 139. Because the helper always attaches an `io.Pipe`, every tool
that checks `isatty` is already in quiet mode before niwa routes a byte. If chatty
successful scripts ever become a real complaint the answer is a `--quiet`/`--verbose`
pair, which niwa does not have today -- not a return to discarding output.

**Alternatives considered.** *Buffer per script and attach the captured tail to the
returned error*, mirroring how the git helper embeds diagnostics: rejected because it
delivers less than the design already promised (successful scripts stay silent, and
the promise is unconditional), and because it holds script output -- potentially
including secrets -- in a heap buffer for the script's lifetime. *Keep `Status` for
the live TTY feel and additionally buffer and replay on failure*: rejected as the
worst of both, duplicating every line's handling and leaving two places to audit for
redaction. *Write output to a log file under the instance and print the path on
failure*: rejected because niwa has no log-file concept, and it answers "where is the
output" with a second command the operator must run.

### Decision B: How a failed setup script becomes discoverable, and what happens to the exit code

**Chosen: a counted verdict line placed directly below the summary; exit code
unchanged at 0; a `setup_policy` config key specified and deliberately deferred.**

The apply pipeline carries the outcome out as data -- a list of repos whose setup did
not finish, a sibling of the warning list it already returns -- and `Create` and
`Apply` print a counted line between the summary line and the deferred warning
block:

```
created myws (2 repos) -> /path/to/myws
setup incomplete for 1 repo: beta
warning: setup script scripts/setup/20-deps.sh failed for beta: exit status 1
```

Placement is the substance here, not the wording. The old warning was invisible
*structurally*: deferred messages print below the summary by contract, so the failure
read as trailing noise after a success verdict. Putting the counted line above the
summary -- inline in the pipeline, the way the plugin-record healing line does it --
does not fix that, because the verdict would still be the last thing printed and it
would still say `created myws (2 repos)`. And once Decision A streams up to a few
hundred lines of script output, an inline line's distance from the verdict becomes
unbounded and run-dependent, which is the exact failure mode being fixed.

**The exit code stays 0, and this is a decision rather than an omission.** `create`'s
exit code already carries a meaning that is written down and relied upon: whether a
usable instance exists. A non-zero exit for an instance that *does* exist is a lie in
the other direction, and it strips the operator of the recovery path. The shell
wrapper's `cd` into a new instance is gated on exit 0, so a fatal setup failure would
strand the operator outside the very instance they need to enter to fix the script.
`--json` emits no path. The SessionStart hook must exit 0 or it loses the
`additionalContext` injection that is its only channel to the agent. And a dispatched
worker never launches at all: dispatch returns on a provisioning error before it even
arms its rollback. niwa also already commits in writing, in
`docs/guides/workspace-config-sources.md`, that a warning-emitting apply exits 0.

**Alternatives considered.** *Non-zero exit by default*: mechanically expressible if
the signal travels on the pipeline result and becomes a verdict only after the state
write -- returning it as a pipeline error would delete the instance on create and
leave `state.json` stale on apply -- but rejected on the consequences above. It also
fails in the wrong direction in Go: it must be expressed as an error that four of
five call sites are required *not* to treat as failure, so a site that forgets the
sentinel propagates it unexamined and orphans a live instance, whereas a site that
forgets an accessor merely exits 0, which is today's behavior and loud in the
terminal. *Non-zero by default plus a run-scoped `--allow-setup-failures` flag*:
materially stronger, and it would match the three existing `--allow-*` escape hatches
on this command exactly, but the escape hatch fixes the migration problem and not the
semantic one -- the exit-code bit is already spoken for. *A `--strict-setup` flag*:
rejected on convention; niwa has no `--strict`-anything, and such a flag would be the
first whose default is the lenient side, reading backwards against its three
siblings.

**The deferred mechanism.** Where a durable posture is wanted, the shape is config,
not a flag: a `setup_policy = "warn" | "fail"` key on the workspace metadata and the
per-repo override, resolved most-specific-wins, exactly like the `.env.example`
failure policy that already ships on those same structs. It is specified here so that
adding it later is additive rather than a re-litigation. It is deferred because no
concrete scripted consumer can be named today, and because a key defaulting to `warn`
does not actually give the reporter what they asked for -- it gives them a way to ask
again, and asking requires already knowing that setup can fail silently, which is the
knowledge the counted line now supplies. If a scripted consumer does appear, a
`setup_failed` field on `niwa create --json` is probably the cheaper and more precise
first response, since it hands the caller repo names rather than a single bit.

Two gaps stay open and are stated rather than papered over. A dispatched worker still
learns nothing: provisioning wires its reporter to the dispatching process's stderr,
not the worker's context, and the `additionalContext` injection is built only on the
SessionStart hook path. And `niwa create --json` still carries no partial-failure
field. Both are the automated case, which is what the deferred half was for; neither
is made worse by this amendment.

### Decision C: Secret exposure once output is printed

**Chosen: unconditional redaction at the line choke point, paired with documented
limits.** Both halves ship; neither is sufficient alone. The mechanism and the four
surviving leak classes are in Security Considerations above.

Scrubbing happens inside the existing scanner loop, immediately after escapes are
stripped. That order is load-bearing: a colorized `set -x` trace can interleave an
escape sequence inside a token, and stripping first rejoins the fragment so the match
succeeds. Placing it there also makes this decision orthogonal to Decision A --
whatever the routing, it consumes an already-scrubbed line, and there is exactly one
line to audit when someone asks whether setup-script output is redacted.

The redactor is threaded as an explicit parameter rather than through the context.
niwa's own redactor documentation calls context attachment a deliberate, narrowly
scoped anti-pattern justified only inside the vault resolution pipeline, which this is
not; the call site already holds the pointer. And setup scripts use `exec.Command`
rather than `exec.CommandContext`, so a context parameter would advertise
cancellation the function does not honor.

**Alternatives considered.** *No redaction*: rejected -- it would make setup-script
output the only subprocess output in niwa that is not scrubbed, and would ship a
knowing new leak path in a change whose whole purpose is operator visibility. The
"it's the operator's own repo" framing does not survive the fact that niwa itself
wrote the secret file into that repo one step earlier. *Redact only on the failure
path*: rejected -- not simpler, since it means either buffering unscrubbed plaintext
or scrubbing twice, and there is no version of this where success-path output is safe
and failure-path output is not. *A config opt-out*: rejected -- no precedent, and the
problem it solves has a cheaper fix, since a value that mangles legitimate output does
not belong in a secrets table and moving it fixes the mangling everywhere.

### Solution Architecture

Four files in two packages; no new types, no new config surface, no `Reporter`
change.

- **`internal/workspace/gitutil.go`** -- `runCmdWithReporter` gains a line prefix and
  a redactor. Decisions A and C both touch this signature, so they land as one change:
  `runCmdWithReporter(r *Reporter, cmd *exec.Cmd, prefix string, red *secret.Redactor) error`.
  Inside the scanner loop the order is strip escapes, scrub, prefix, `Log`. The
  redactor is nil-tolerant so existing callers and tests can pass `nil`. The
  function's doc comment, which currently asserts that script output is silent in
  piped and CI contexts, is rewritten.
- **`internal/workspace/setup.go`** -- `RunSetupScripts` gains the redactor, emits the
  per-script announcement, and builds the prefix once per script.
- **`internal/workspace/apply.go`** -- `pipelineResult` gains a list of repos whose
  setup did not finish. Step 6.75 appends to it, counting a repo once even when
  several of its scripts errored, since a non-executable script is skipped rather than
  stopping the repo. `Create` and `Apply` each print the counted line between their
  summary line and their deferred-warning loop. The pipeline still returns nil, so the
  instance-removal path on create is never reached.
- **`internal/workspace/gitutil_test.go`** -- the test asserting all lines route
  through `Status` currently pins the defect and is rewritten.

### Consequences

**Positive.** A failed setup script now explains itself, in every run mode, and the
failure is attached to the verdict rather than trailing below it. Two false claims in
this document become true. Cross-repo resilience is untouched: every repo still gets
its turn and stop-on-first-error stays scoped within a repo.

**Negative.** Successful scripts are now audible where they were silent, which is a
visible change to apply output for anyone with chatty setup scripts. A legitimate
value matching a registered secret now renders as a placeholder, and an operator has
to recognize that as redaction rather than as the script misbehaving. The exit code
still does not distinguish a fully-provisioned instance from a partially-provisioned
one, so a scripted consumer that only checks `$?` learns nothing new.

**Mitigations.** Volume was measured rather than assumed, and the escape hatch if it
becomes a problem is a verbosity flag rather than a return to discarding output. The
redactor's placeholder is documented here. The deferred `setup_policy` and the
`--json` field are both specified, so the scripted case can be answered without
re-opening this decision.
