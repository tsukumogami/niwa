# Security Review: setup-script-visibility

Reviewed: `docs/designs/current/DESIGN-post-clone-scripts.md` (Amendment 2026-08-08 and the
new "Secret exposure through printed script output" subsection),
`wip/design_setup-script-visibility_decision_1_report.md`,
`wip/design_setup-script-visibility_decision_3_report.md`, against the code on branch
`fix/setup-script-visibility`.

Every claim below is grounded in code read in this worktree, or in a measurement run against a
verbatim replica of the code path (the two replicas are noted where used).

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes. This is where the change carries real risk.

#### What does not change

The execution surface is untouched. `RunSetupScripts` (`internal/workspace/setup.go:46-119`)
already scans, filters on the executable bit, sorts lexically, and runs each script with
`exec.Command(scriptPath)` and `cmd.Dir = repoDir` (`setup.go:104-105`); Step 6.75 already
iterates classified repos and calls it (`internal/workspace/apply.go:1580-1596`). The amendment
adds a `prefix string` and a `*secret.Redactor` to two signatures and changes one routing call.
Nothing new is executed, nothing new is discovered, and no invocation semantics change. The
"arbitrary code from a cloned repo" exposure is entirely pre-existing.

What is new is that attacker-influenced bytes now reach durable output. Precisely how new is
worth stating, because the design's framing ("until this amendment, everything a setup script
wrote was discarded") is right off a TTY and slightly overstated on one: `Reporter.Status`
stores the line in `spinMsg` (`reporter.go:62-77`) and `doTick` writes it to the terminal inside
`\r\033[K%s %s` (`reporter.go:111`), so today, on a TTY, roughly one arbitrary line per script
already renders transiently. And niwa already routes remote-influenced content into *durable*
output elsewhere: `runGitWithReporter` sends every git line matching `fatal:`/`error:`/`warning:`
through `r.Warn` (`gitutil.go:66-70`) with exactly the same `stripEscapes` as its only defence.
So the class is not new to niwa. What is new is the volume and the control: every line, of fully
attacker-chosen content, in both TTY modes, into terminal scrollback, CI logs, and dispatch logs.

#### Finding 1 (medium): `stripEscapes` strips two forms and lets everything else through

`stripEscapes` (`gitutil.go:12-23`) is exactly two regexes:

```go
var csiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)   // ESC [ digits/semicolons letter
var oscPattern = regexp.MustCompile(`\x1b\][^\x07]*\x07`)      // ESC ] ... BEL
```

Measured against a verbatim replica of that function (results reproduced from a standalone run;
`clean` means no control byte survived):

| input | verdict |
|---|---|
| `\x1b[31mred\x1b[0m` (SGR colour) | clean |
| `\x1b[2K`, `\x1b[1A` (erase line, cursor up) | clean |
| `\x1b]0;title\x07` (OSC, BEL-terminated) | clean |
| `\x1b[?25l` (private-parameter CSI) | **survives** — `?` is not in `[0-9;]` |
| `\x1b[>0c`, `\x1b[0 q` (private / intermediate CSI) | **survives** |
| `\x1b]0;title\x1b\\` (OSC, **ST**-terminated) | **survives** |
| `\x1b]8;;http://evil\x1b\\click\x1b]8;;\x1b\\` (OSC 8 hyperlink) | **survives** |
| `\x1b]52;c;<base64>\x1b\\` (OSC 52 clipboard) | **survives** |
| `\x1bc` (RIS, full terminal reset) | **survives** |
| `\x1b7` / `\x1b8` (save/restore cursor) | **survives** |
| `\x1b(0` (DEC line-drawing charset) | **survives** |
| `\x1bP…\x1b\\` (DCS), `\x1b_G…\x1b\\` (APC/Kitty graphics), `\x1b^…` (PM) | **survives** |
| trailing `\x1b[` or lone `\x1b` at end of line | **survives** |
| `\r` (embedded carriage return) | **survives** |
| `\b`, `\x07`, `\x00`, `\x0b`, `\x0c` | **survives** |

Two of these are directly weaponisable against the amendment's own output contract.

**Carriage return forges niwa's lines.** `bufio.Scanner`'s `ScanLines` drops only a single `\r`
immediately preceding the `\n`; an embedded CR survives into `scanner.Text()` (measured:
`"mid\rline\r\n"` yields the token `"mid\rline"`). `stripEscapes` does not touch it, and
`Reporter.Log` writes the line through verbatim (`reporter.go:141`, line passed as an argument,
so no format-string issue). The bytes written are therefore:

```
"[koto/01-x.sh] junk\rsetup incomplete for 0 repos"
```

On a terminal — and in any log viewer that honours CR, which includes `cat` and the common CI
web viewers — the CR returns the cursor to column 0 and the forged text (longer than the
15-byte prefix) overwrites the attribution entirely. The rendered line is
`setup incomplete for 0 repos`. The same trick forges `applied ws (3 repos)`. `\b` gives a
slower version of the same erasure. Decision 1's Consequences names the prefix as the answer to
output forgery ("a script echoing `applied ws (5 repos)` renders as `[repo/01-x.sh] applied ws
(5 repos)`"); that holds only for content with no control bytes in it, which is not something
the current stripper guarantees. This matters more under the amendment than it would have
before, because the amendment introduces a machine-readable-looking verdict line whose whole job
is to be trusted.

**Incomplete stripping is also a redaction bypass.** This is the finding I would rank highest in
this dimension, because it makes Decision C's mitigation defeatable rather than merely limited.
The design's stated reason for ordering strip-then-scrub is exactly right — "a colorized `set -x`
trace can interleave an escape sequence inside a token, and stripping first rejoins the fragment
so the match succeeds" — but that reasoning only extends as far as the stripper does. A script
that emits a secret with any *unstripped* sequence interleaved inside it defeats the substring
match while the terminal still renders the secret contiguously:

- `abc\x1b[?25ldef` — redactor sees 13 bytes that match no fragment; terminal renders `abcdef`.
- `abcX\bdef` — same result.
- `abc\x1b7def`, `abc\x1b(Bdef`, `abc\x1b]0;\x1b\\def` — same.

That is deliberate-evasion-shaped, and a malicious repo is already running arbitrary code, so it
is not the primary threat. It matters because it converts the stripper's completeness from a
cosmetic property into a correctness property of the security control this design ships, and
because the accidental version is reachable too: private-parameter CSI (`\x1b[?…`) is emitted by
ordinary progress-rendering tools, and a `set -x` trace under such a tool can interleave one
inside a value.

**Mitigation (recommended, small, lands in the function the change already touches).** After the
two existing regex passes, remove residual control bytes: every C0 control except `\t` (i.e.
`\x00-\x08`, `\x0b`, `\x0c`, `\x0e-\x1f`, plus `\x7f`), which covers `\r`, `\b`, BEL, NUL, and
any leftover `\x1b` from a form neither regex matched. One extra `ReplaceAllString` on a
precompiled pattern. `\n` cannot appear (the scanner split on it). This closes the forgery
vector, closes the render-vs-match divergence above, and makes the "strip then scrub" ordering
argument actually true. It also has a pleasant side effect on the pre-existing
`runGitWithReporter` path if applied inside `stripEscapes` rather than at the setup-script call
site — which is the right place, since git diagnostics are remote-influenced too.

#### Finding 2 (medium-low): the prefix and the announcement carry unsanitised filenames

The design routes only script *output* through `stripEscapes` and the redactor. The announcement
`running setup script <repo>/<script>` and the per-line prefix `[<repo>/<script>] ` are built
from `entry.Name()` (`setup.go:70-76`), which is a POSIX filename: any byte except `/` and NUL.
A repo can ship `scripts/setup/<CR>applied ws (3 repos)` (executable, any content) and forge a
niwa line *before its script runs*, and again on every output line, with no stripper in the
path at all. Today that filename reaches output only in the failure `DeferWarn`
(`apply.go:1592-1593`); the amendment prints it unconditionally, once per script plus once per
emitted line.

**Mitigation:** sanitize the script name (same control-byte filter) once, where the prefix is
built in `RunSetupScripts`, before it is used for either the announcement or the prefix.

#### Finding 3 (medium, correctness of the feature itself): a script can silently suppress all of its own output

Pre-existing, but the amendment is what makes it matter. `bufio.NewScanner(pr)`
(`gitutil.go:103`) uses the default 64 KB `MaxScanTokenSize` and `scanner.Err()` is never
checked. Measured against a verbatim replica of the `runCmdWithReporter` scanner loop:

```
normal                   lines=2  runErr=<nil>            scanErr=<nil>
60KB line then normal    lines=2  runErr=<nil>            scanErr=<nil>
100KB line then normal   lines=0  runErr=signal: broken pipe  scanErr=bufio.Scanner: token too long
normal, 100KB, normal    lines=1  runErr=signal: broken pipe  scanErr=bufio.Scanner: token too long
```

One line over 64 KB kills the scanner; `pr.Close()` then SIGPIPEs the script, so a script that
would have exited 0 is killed. Under the amendment the operator gets: no output from the point
of the long line onward, the repo counted in `setup incomplete for N repos`, and a warning
reading `signal: broken pipe` that points nowhere near the cause. A script that prints a base64
blob or a one-line JSON dump hits this by accident; a hostile one hits it on purpose to
suppress the very explanation this change exists to surface. Decision 1's sub-answer 4
recommended `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` plus a `scanner.Err()` check
routed through `Warn`; the Amendment's Solution Architecture does not carry that recommendation
forward. It should either adopt it or say explicitly that it is out of scope and why.

#### Finding 4 (low): unbounded durable volume

`Log` writes straight to the reporter's writer with no cap. Off a TTY these bytes previously
went nowhere; they now accumulate in dispatch and CI logs at whatever rate a script produces
them. A runaway script consumes log storage where it previously consumed nothing. It cannot
hang the apply any harder than it already can (`cmd.Run()` waits either way). Worth one clause
in the Consequences "Negative" paragraph alongside the noise point; not worth a cap, for the
reasons Decision 1 gives.

### Permission Scope

**Applies:** No.

Concretely, nothing in the change touches a privilege boundary:

- Invocation is unchanged: `exec.Command(scriptPath)` with `cmd.Dir = repoDir` and nothing else
  set (`setup.go:104-105`). `cmd.Env` stays nil, so the child inherits `os.Environ()` exactly as
  before; `cmd.SysProcAttr` is not set, so no credential or namespace change.
- The two new parameters are a `string` and a `*secret.Redactor`. Neither reaches the child.
- No file is created, opened, or chmod'd by the change. Secret-file mode is unchanged
  (`secretFileMode = 0o600`, `materialize.go:27`, used at `:1365`).
- No network call is added.
- The pipeline still returns `nil` after Step 6.75 (`apply.go:1596`), so the instance-lifecycle
  paths (create rollback, state write) are unchanged.

Two pre-existing facts are worth keeping in view, neither introduced here:

1. The design's Mitigations bullet "Script paths are validated to stay within the repo
   directory" is not implemented. `RunSetupScripts` reads the directory, filters `entry.IsDir()`,
   and `os.Stat`s the path (`setup.go:70-102`) — `os.Stat` follows symlinks, so
   `scripts/setup/01-x -> /usr/bin/whatever` is executed, and the resolved `setupDir` from config
   is `filepath.Join`ed without a containment check (unlike `safeTargetPath`, used for env
   outputs at `materialize.go:1337`). Both are inside the "you cloned it, you trust it" boundary
   the design draws, so this is a documentation-accuracy issue rather than a vulnerability — but
   the amendment is explicitly in the business of correcting untrue claims in this document, and
   this is a third one sitting four lines above the new subsection.
2. Scripts inherit whatever the operator exported. The design says this and it is correct.

### Supply Chain or Dependency Trust

**Applies:** No.

No change to provenance or verification. Scripts still come from the already-cloned working
tree; discovery, ordering, and the executable-bit filter are untouched. No new module
dependency: `internal/secret` is in-tree and already imported by `apply.go`, and the redactor
instance passed to `RunSetupScripts` is the existing per-apply local from `apply.go:1105`, not a
new construction. `go.mod` does not change. Nothing about clone, fetch, or overlay sync is in
scope.

### Data Exposure

**Applies:** Yes. The written analysis is largely correct; one verified claim is wrong, and
there is a fifth leak class.

#### Verified claims

- **"niwa never sets `cmd.Env` for setup scripts and exports nothing into its own environment"
  — confirmed.** `setup.go:104-105` sets only `cmd.Dir`. There is no `os.Setenv` anywhere in
  production code: the only hit across `internal/` and `cmd/` is
  `internal/plugin/installer_test.go:22`. Machine-identity tokens are injected into config
  structures, not the environment (`injectProviderTokens`, `apply.go:1003`). Materialized values
  reach disk through `os.WriteFile` (`materialize.go:1365`). So the "files only" framing holds.
- **Redactor population trace — confirmed.** `registerOnRedactor` has exactly two call sites,
  both on the success path of `resolveOne`: `internal/vault/resolve/resolve.go:489` (auto-wrapped
  `*.secrets` plaintext) and `:521` (resolved `vault://` reference), and it no-ops when the ctx
  carries no redactor (`:571-577`). There is exactly one `WithRedactor` call in the whole tree:
  `apply.go:1106`.
- **Leak class 4 — confirmed, including the contradiction.** `minFragmentLen = 6` and `Register`
  silently drops shorter fragments (`internal/secret/redactor.go:17, 49-51`), while its own
  comment at `:11-16` asserts such secrets "must be rejected at resolution time with a hard
  error"; `resolve.go:564-570` says the opposite in as many words and implements the permissive
  behaviour. Correctly described as pre-existing and correctly scoped out.
- Classes 1, 2, and 3 are accurate as written. Class 2's dotenv-vs-JSON contrast is right
  (`envformat.go:52-62` writes unquoted; `marshalJSON` at `:67-91` escapes with `encoding/json`).

#### Correction: classes 2 and 3 omit the `shell` format

`envformat.Marshal` supports three formats, not two (`envformat.go:35-47`). `marshalShell`
(`:93-105`) emits `export KEY='value'` with `'` escaped as `'\''`. So a secret containing a
single quote is written in a form that is not verbatim (class 2 applies, and its "shell-quoting"
mention covers it generically but the design's prose names only JSON concretely), and a
multi-line secret in shell format keeps real newlines inside the single quotes (class 3 applies
identically to dotenv). One clause each.

#### Fifth class, and the significant one: workspace-overlay secrets are never registered

The design states: "The fragment set at the time setup scripts run is exactly the set of values
materialized into the repo working trees one step earlier." That is false for any workspace that
uses a workspace overlay, and the decision report's dismissal of the pre-pass caveat ("they
resolve the overlay's own vault bundle and are not materialized into repo working trees") does
not survive reading the merge.

Traced:

1. Step 0.6 resolves the overlay's env — top-level and per-repo — at `apply.go:1026-1030`, via
   `resolve.ResolveWorkspace(ctx, tmpCfg, …)` against the overlay's own bundle.
2. That is 79 lines *before* the redactor exists and is attached (`apply.go:1105-1106`), which is
   the only attach site in the tree. So `registerOnRedactor` returns early at
   `resolve.go:572-575` and **none of those values are registered.**
3. The resolved values are written back onto the overlay (`apply.go:1032-1033`) and merged into
   the config that drives everything downstream: `MergeWorkspaceOverlay` (`apply.go:1035`,
   `internal/workspace/override.go:708`) copies env at `:713` and merges per-repo env values at
   `:762-770`, with the comment "overlay supplies vault-resolved secrets/vars for keys not
   present in the base."
4. The later resolution pass that *does* run under the redactor —
   `ResolveAndMergeEffectiveConfig` at `apply.go:1263` — cannot rescue them, because `resolveOne`
   early-returns for anything already resolved: `if ms.IsSecret() { return ms, nil }`
   (`resolve.go:473-475`).
5. Those values then flow into the env-output materializer like any other and land in the repo
   working tree at `materialize.go:1354-1366` — the same `.env.local` sitting in the setup
   script's cwd that this whole subsection is about.

Net effect: for a workspace that keeps its secrets in the workspace overlay, unconditional
redaction of setup-script output is a **complete no-op for exactly those secrets**. Nothing in
the output or in the design tells the operator that. This is not a limit of substring matching
like classes 1-4 — it is a gap in what the redactor was told, and it is invisible.

The personal/global overlay is fine: `ResolveGlobalOverride` runs inside
`ResolveAndMergeEffectiveConfig` (`effective_config.go:92-98`), i.e. after the attach.

**Fix, and it is two lines:** move `redactor := secret.NewRedactor()` /
`ctx = secret.WithRedactor(ctx, redactor)` from `apply.go:1105-1106` to the top of `runPipeline`
(`apply.go:702`), above Step 0.6. Attaching a value to a context earlier is purely additive —
the earliest `ctx` uses before the current attach are config-snapshot sync (`:806`), credential
provider open (`:858`), clone/sync (`:905`, `:941`), and the overlay bundle build (`:1011`), all
of which only gain scrubbing on their error paths (`vault/scrub.go:42`,
`infisical/auth.go:46,115`, `secret/error.go:165` all read the redactor from ctx and no-op when
it is absent). No signature changes, no new construction. Severity without the fix: high for
affected workspaces, since it silently voids the mitigation for the secret set most likely to
exist.

#### Sixth (minor) caveat worth folding into class 1: the redactor knows this run, not the disk

Redaction covers values *this* apply resolved. A secret-output file left by an earlier apply —
after a rotation, or after a run with `--allow-missing-secrets` where this run resolved
nothing — still sits in the script's cwd with plaintext the current redactor has never seen.
Class 1's wording ("only values niwa itself resolved are scrubbed") reads as covering it but
does not, since niwa did resolve them, in another process. One clause fixes it.

#### The verdict line and the announcement line

Asked specifically: neither introduces a new *confidentiality* class.

- Both are niwa-constructed from the repo name, the resolved `setupDir`, and the script
  filename, and neither passes through the redactor. None of those three is a niwa-managed
  secret value, and all three already appear in the pre-existing failure `DeferWarn`
  (`apply.go:1592-1593`); repo names appear throughout apply output.
- The one genuinely new disclosure is an inventory one: the announcement names every setup
  script on every *successful* run, in contexts (dispatch logs, CI) where previously nothing
  appeared. Negligible on its own.
- The real problem with these two lines is integrity, not confidentiality — see Finding 2. They
  are the only attacker-influenceable output in the change that no sanitizer touches.

### Exit-Code Semantics

**Applies:** Yes, but narrowly, and the answer is not "make it non-zero."

The change does not alter the exit code — it stays 0, as it is today. So there is no regression
here; the question is whether the amendment should have fixed a pre-existing fail-open, and
whether the counted line closes it.

The concrete security scenario is real and worth naming in the design, because it is the one
that would justify pulling the deferred work forward. Setup scripts are a natural home for
*security controls*: git hook installation (the design's own running example is
`01-git-hooks.sh`), pre-commit secret scanning, commit-signing config, dependency pinning. If
that script fails, the instance is provisioned without the control, the pipeline prints a
success summary, and `niwa dispatch` proceeds to launch an autonomous agent into that repo. The
amendment's counted line does not reach that agent: as the design states, provisioning wires its
reporter to the dispatching process's stderr, and `additionalContext` injection exists only on
the SessionStart hook path. So for the automated consumer, the fail-open is exactly as it was:
a missing pre-commit secret scanner, and an agent then committing.

I do not recommend changing the exit code, and the design's reasoning survives scrutiny: a
non-zero exit for an instance that exists strands the operator outside it, and on the dispatch
path it would replace "worker runs without a hook" with "worker never launches," which is a
functional regression rather than a security improvement. The proportionate answers are the two
already-specified deferred items — a `setup_failed` field on `niwa create --json`, and carrying
the verdict into the worker's injected context. What the design should add is one sentence
saying *why* those matter, in security terms, so a future reader knows the deferred work has a
security motivation and not only an ergonomic one.

## Recommended Outcome

**1. Findings need design changes.** Two, both small and both in code the change already touches;
everything else is prose.

**Change A — complete the escape/control stripping, and apply it to script names.**
`stripEscapes` (`gitutil.go:12-23`) removes CSI-with-numeric-parameters and OSC-terminated-by-BEL
and nothing else; measured survivors include `\r`, `\b`, BEL, NUL, private-parameter CSI
(`\x1b[?25l`), ST-terminated OSC (including OSC 8 and OSC 52), `\x1bc` (full terminal reset),
DCS/APC/PM, and any lone or unterminated ESC. Two consequences make this a design-level change
rather than an implementation detail: an embedded `\r` overwrites the `[<repo>/<script>] ` prefix
and renders as a forged niwa line (`setup incomplete for 0 repos`, `applied ws (3 repos)`) in
both run modes, defeating the exact control Decision 1 names as its answer to output forgery;
and an unstripped sequence interleaved inside a secret defeats the redactor's substring match
while the terminal still renders the secret contiguously, which makes Decision C's mitigation
defeatable rather than merely limited. Add a third pass inside `stripEscapes` removing all C0
controls except `\t` (plus `\x7f`), and sanitize the script filename the same way in
`RunSetupScripts` before it is used for the announcement or the prefix — filenames are
repo-controlled and currently reach output with no sanitizer at all. The ordering in the design's
Solution Architecture becomes: strip escapes **and control bytes**, scrub, prefix, `Log`.

**Change B — fix the redactor coverage gap, or stop claiming coverage the code does not have.**
The design asserts the fragment set at Step 6.75 is "exactly the set of values materialized into
the repo working trees one step earlier." It is not, for any workspace using a workspace overlay:
Step 0.6 resolves the overlay's env at `apply.go:1026` under a ctx that has no redactor (the only
attach is at `apply.go:1106`, 79 lines later), those values merge into the effective config
(`override.go:713, 762-770`), the later pass skips them because `resolveOne` early-returns on
`ms.IsSecret()` (`resolve.go:473-475`), and they are materialized into the script's cwd
(`materialize.go:1365`). Preferred fix: move the two redactor lines to the top of `runPipeline`
— purely additive, no signature change. If that is judged out of scope, the design must list this
as a fifth leak class and drop the "exactly the set" sentence, but the fix is cheaper than the
prose and the claim is load-bearing.

**Additionally, considerations worth documenting.** Exact prose to add:

To Security Considerations, appended to leak class 1:

> Redaction also covers only *this* apply's resolved values, not what is on disk. A secret-output
> file left by an earlier apply — after a rotation, or after a run with `--allow-missing-secrets`
> in which nothing resolved — still sits in the script's working directory with plaintext the
> current redactor has never seen.

To leak class 2, after the JSON sentence:

> The `shell` output format single-quote-escapes values (`'` becomes `'\''`), so a secret
> containing a quote is likewise emitted in a form the redactor does not match.

To leak class 3, after the dotenv sentence:

> The `shell` format has the same property: single-quoted values keep their real newlines.

To the Amendment's Solution Architecture, under the `gitutil.go` bullet:

> The scanner keeps `bufio.Scanner`'s default 64 KB token limit and does not check
> `scanner.Err()`. A single line longer than that ends the scan, closes the pipe, SIGPIPEs the
> script, and loses every line from that point on — so a script that prints one long base64 blob
> is reported as `signal: broken pipe` with no output at all, which is the failure mode this
> amendment exists to remove. Raising the buffer to 1 MB and surfacing `scanner.Err()` through
> `Warn` is carried from the output-routing decision and lands with this change.

To the Consequences "Negative" paragraph:

> Script output is now durable and uncapped, so a runaway script consumes dispatch- and CI-log
> storage where it previously consumed none.

To Decision B's closing paragraph on the two gaps that stay open:

> The security shape of that gap is worth naming: setup scripts are a natural home for security
> controls — git hooks, pre-commit secret scanning, commit-signing config — and a control that
> fails to install is invisible to an automated consumer, which then proceeds against a repo that
> silently lacks it. This is unchanged from today rather than made worse, and it is the concrete
> case that would justify pulling the deferred `setup_failed` field and worker-context injection
> forward.

Finally, the Mitigations bullet in Security Considerations claiming "Script paths are validated
to stay within the repo directory" is a third untrue claim in this document: `RunSetupScripts`
performs no containment check and `os.Stat` follows symlinks (`setup.go:70-102`), and the
resolved `setupDir` is `filepath.Join`ed without the `safeTargetPath` treatment env outputs get
(`materialize.go:1337`). Both sit inside the trust boundary the design draws, so this is an
accuracy fix, not a vulnerability — but the amendment is already correcting two such claims and
this one is four lines above the new subsection.
