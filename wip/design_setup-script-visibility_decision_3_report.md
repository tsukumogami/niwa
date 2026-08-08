<!-- decision:start id="setup-script-output-redaction" status="assumed" -->
### Decision: How secret exposure is handled once setup-script output is printed

**Context**

Setup scripts run at Step 6.75 of `runPipeline` (`internal/workspace/apply.go:1580-1596`),
after Step 6.5 materializers have written `.env.local` and any configured secret-output
targets into each repo's working tree at mode 0600. The script's cwd is that repo root
(`setup.go:105`). Today every line a script writes goes to `Reporter.Status`, which is a no-op
off-TTY and gets erased by `stopSpinner` on-TTY, so nothing durable is written anywhere.
Printing that output is therefore a genuinely new exposure path, not a widening of an existing
one: the same bytes start landing in dispatch logs, CI logs, and terminal scrollback where they
have never been written before.

The exposure is real but narrower than "the script can see everything." Secrets reach setup
scripts through **files only**. `setup.go:104-105` never sets `cmd.Env`, so the script inherits
niwa's own process environment verbatim — and niwa exports nothing into it: there is no
`os.Setenv` anywhere in `internal/`, and materialized values reach disk through
`os.WriteFile` in `materializeEnvOutput` (`materialize.go:1329-1378`) and nowhere else. So
there is no `env | grep` route to a niwa-managed secret. What there is, instead, is
`.env.local` sitting in the script's own working directory, and `set -a; . ./.env.local`
followed by `set -x` is an entirely ordinary thing for a setup script to do. The residual
env-borne exposure is whatever the *operator* exported before invoking niwa, which the redactor
has never seen and cannot help with.

The mitigation is already built and already in scope. `secret.NewRedactor()` is constructed at
`apply.go:1105` and attached to `ctx` at `:1106`, in the same function as the Step 6.75 loop,
479 lines earlier.

**Assumptions**

- The output-routing decision keeps a single point in `runCmdWithReporter` where a raw script
  byte becomes a niwa-handled string. This holds for both live routing choices — stream through
  `Reporter.Log`, or buffer and embed in the returned error — because both consume the line
  produced by the existing scanner loop at `gitutil.go:104-107`. If a later design bypasses that
  scanner (e.g. wiring `cmd.Stdout` directly to `Reporter.Writer()`), the single insertion point
  is lost and the redaction plumbing has to move with it.
- Operators put genuine secrets in `*.secrets` tables and genuine non-secrets outside them.
  The resolver auto-wraps and registers **every** plaintext literal in a secrets table
  (`resolve.go:485-490`), so a non-secret parked there becomes a redaction fragment and will be
  replaced with `***` in ordinary log lines. If this assumption is wrong the symptom is mangled
  output, not a leak.
- No niwa-managed secret is shorter than 6 bytes in practice. See Consequences — the
  compensating control the redactor's own doc comment claims for this case does not exist.

**Chosen: Unconditional redaction at the line choke point, paired with documented limits**

Scrub every line of setup-script output through the per-apply `secret.Redactor` before that
line is printed or buffered, unconditionally, with no opt-out — and write the four classes of
leak that redaction provably cannot catch into `DESIGN-post-clone-scripts.md`'s Security
Considerations section. Both halves ship; neither is sufficient alone.

*Where the scrub goes.* Inside the existing scanner loop in `runCmdWithReporter`
(`gitutil.go:100-109`), immediately after the `stripEscapes` call that is already there:

```go
line := stripEscapes(scanner.Text())
if red != nil {
    line = red.Scrub(line)
}
```

Order is load-bearing. Stripping must come first: a `set -x` trace with colorized output can
interleave an ANSI sequence inside a token, and stripping first rejoins the fragment so the
substring match succeeds. Scrubbing first would miss it. Placing the scrub here also makes this
decision **orthogonal to the output-routing decision** — whatever routing picks, it consumes an
already-scrubbed line, and there is exactly one place to audit.

*The plumbing, concretely.* Pass the `*secret.Redactor` explicitly. Three edits:

```go
// internal/workspace/gitutil.go:93
-func runCmdWithReporter(r *Reporter, cmd *exec.Cmd) error
+func runCmdWithReporter(r *Reporter, cmd *exec.Cmd, red *secret.Redactor) error

// internal/workspace/setup.go:46
-func RunSetupScripts(repoDir, setupDir string, r *Reporter) *SetupResult
+func RunSetupScripts(repoDir, setupDir string, r *Reporter, red *secret.Redactor) *SetupResult

// internal/workspace/apply.go:1584
-result := RunSetupScripts(repoDir, setupDir, a.Reporter)
+result := RunSetupScripts(repoDir, setupDir, a.Reporter, redactor)
```

`RunSetupScripts` forwards `red` to `runCmdWithReporter` at `setup.go:107`. `redactor` is the
existing local from `apply.go:1105` — no new construction, no new context threading, and
`apply.go` already imports `internal/secret`. `setup.go` and `gitutil.go` each gain that import.

Call sites to update: one production caller each, plus seven test callers — `gitutil_test.go:157`
and `:183` for `runCmdWithReporter`, and `setup_test.go` lines 60, 68, 77, 95, 133, 160, 187 for
`RunSetupScripts`. All seven pass `nil`, which the nil guard makes legal. The guard is required,
not defensive habit: `Scrub` has a pointer receiver that locks `r.mu` on entry
(`redactor.go:87`), so calling it on a nil `*Redactor` panics.

*Why an explicit parameter rather than `ctx`.* Two reasons, both concrete. First,
`redactor.go:118-131` states plainly that attaching the Redactor to `context.Context` is "a mild
anti-pattern" accepted deliberately and narrowly, "in exchange for letting Wrap/Errorf pick up
the active Redactor without threading it through every function signature **in the vault
resolution pipeline**." `internal/workspace` is not that pipeline, and the call site already
holds the concrete pointer — threading `ctx` here to re-extract a value we are already holding
adopts the anti-pattern in exactly the place its own justification does not reach. Second,
`RunSetupScripts` uses `exec.Command`, not `exec.CommandContext`: it honors no cancellation. A
`ctx` parameter would advertise semantics the function does not have, and a reader would
reasonably expect Ctrl-C to kill a hung setup script. If a later change wants
`exec.CommandContext`, add `ctx` then, for the reason that actually motivates it.

**Rationale**

*The redactor is populated when Step 6.75 runs — traced, not assumed.* This was the sub-question
that could have made the whole option decorative, so here is the trace.
`registerOnRedactor` (`resolve.go:571-577`) has exactly two call sites, both on the **success**
path of `resolveOne`: `:485-490` registers every plaintext literal auto-wrapped from a
`*.secrets` table, and `:519-522` registers every successfully resolved `vault://` reference.
Both run under the `ctx` that carries the redactor. Inside `runPipeline` the order is: redactor
constructed and attached at `apply.go:1105-1106` → `ResolveAndMergeEffectiveConfig(ctx, ...)` at
`:1263-1270`, which calls `resolve.ResolveWorkspace` and `ResolveGlobalOverride`
(`effective_config.go:74`, `:92`) and populates the fragment set → Step 6.5 materializers at
`:1499` write those same values to disk → Step 6.75 at `:1580`. **The fragment set at Step 6.75
is exactly the set of values written into the repo working trees one step earlier.** That is the
best possible alignment between what the redactor knows and what a script can read.

Two caveats, neither fatal. The Step 0.6 personal-overlay pre-pass calls
`resolve.ResolveWorkspace` at `apply.go:1026`, *before* the redactor exists, so those values go
unregistered — but they resolve the overlay's own vault bundle and are not materialized into
repo working trees, so they fall outside this exposure surface. And a workspace with no vault
config and no secrets tables leaves the set empty, in which case `Scrub` short-circuits at
`redactor.go:88-91` and returns the input unchanged. Redaction is a no-op precisely when there
is nothing to leak, which is the correct degenerate behavior, not a defect.

*The cost is negligible and measured.* Benchmarked against a verbatim replica of `Scrub` (same
copy-under-lock, same `sort.SliceStable` longest-first, same `ReplaceAll` loop) on a 118-byte
log line: 14 ns/line with zero fragments and zero allocations; 1.1 µs with 4 fragments; 3.2 µs
with 16; 11.6 µs with 64. Set against the volumes measured during exploration — 2 lines for a
real git-hooks installer, 4 for `npm install`, 20 for `pip install`, 139 for a verbose
`go mod download -x` — a 139-line script with 16 registered secrets costs **0.44 ms total**. A
pathological 10,000-line `set -x` script with 64 fragments costs 116 ms. Both vanish beside the
process spawn and the git clones that precede them. Performance is not an argument against
anything here.

*The false-positive risk is real, bounded, and already accepted elsewhere.* Matching is plain
substring, longest-first, no word boundaries (`redactor.go:83-110`). The `minFragmentLen = 6`
floor kills the worst class outright — a port number or a version string is too short to
register — but a value like `myapp_staging` sitting in an `[env.secrets]` table will be
registered and will turn every mention of that database name in script output into `***`. That
is the realistic failure mode, and it is operator-caused: the fix is to move a non-secret out of
a secrets table, one line of config, which also stops it mangling error messages everywhere
else. The damage when it does happen is cosmetic and local — one token in one log line. It never
changes an exit status and never changes what reaches disk. And niwa already accepts exactly
this risk on paths with *higher* debugging stakes: `vault/scrub.go:42` scrubs vault subprocess
output unconditionally, `infisical/auth.go:115-116` scrubs auth output, and
`secret.Error.Error()` (`error.go:27-36`) scrubs every error message in the vault pipeline. A
false positive today can already mangle the message an operator needs to diagnose a failed
apply. Extending the same unconditional policy to setup-script output is strictly less
consequential than what already ships.

*A doc-only mitigation is necessary but not sufficient.* The argument for it is real: a setup
script is arbitrary trusted code from a repo the operator chose to clone, no different in
principle from a Makefile, and niwa does not audit Makefiles. But the hazard here is not malice,
it is accident — and the accident is one **niwa manufactures**. niwa writes `.env.local` into
the repo one pipeline step before running the script, and niwa is the party deciding to start
printing. A script author who sources that file and runs with `set -x` has done nothing unusual;
niwa is what converts it into a durable log entry. When the mitigation costs a nil-check and one
function call on machinery already constructed in the same function, "we warned them" is not a
proportionate response. It also misses the specific case the whole change exists for: the
failure path is where `set -x` traces and half-expanded error messages land, and that is exactly
the output #239 wants surfaced. The warning still ships — Consequences lists four leak classes
no scrubbing catches, and those need prose — but it ships alongside the code, not instead of it.

**Alternatives Considered**

- **No redaction — print raw.** Rejected. It would make setup-script output the only subprocess
  output in niwa that is *not* scrubbed, contradicting three existing unconditional scrub sites,
  and it would ship a knowing new leak path in a change whose entire purpose is improving
  operator visibility. The "it's the operator's own repo" framing is answerable but does not
  survive the fact that niwa itself materialized the file one step earlier.

- **Redact only on the failure path.** Rejected on both correctness and simplicity. It is not
  simpler: the scrub belongs at the line-production choke point either way, so making it
  conditional means either buffering unscrubbed plaintext (holding secrets in a heap buffer for
  the script's lifetime and requiring a scrub at every replay site, one of which will eventually
  be missed) or scrubbing twice. And it misreads where the exposure is. The exploration already
  chose to stream success-path output, so a script that succeeds while echoing `.env.local`
  leaks with nothing gained. Even under a buffer-and-replay routing choice, `set -x` echoes
  accumulate in the buffer on the success path. There is no version of this where success-path
  output is safe and failure-path output is not.

- **Config opt-out.** Rejected on three counts. It has no precedent and inverts the house
  pattern: niwa's secret scrubbing is unconditional everywhere it exists, with no opt-out
  anywhere, and this would make setup-script output the one place an operator can switch
  redaction off. The problem it solves already has a cheaper and better fix — if redaction
  mangles legitimate output, the value does not belong in a `*.secrets` table, and moving it
  fixes the mangling everywhere rather than in one subsystem. And the payoff is asymmetric in
  the wrong direction: niwa's four escape hatches (`--allow-dirty`, `--allow-empty`,
  `--allow-missing-secrets`, `--allow-plaintext-secrets`) all unblock an apply that would
  otherwise fail, whereas a redaction opt-out unblocks nothing — the apply succeeds either way.
  Switching it on costs a credential in a dispatch log; switching it off buys a tidier log line.

- **Doc-only mitigation.** Rejected *as a standalone answer*, adopted as a required companion.
  See Rationale. Redaction cannot catch four leak classes, and those need prose; but prose alone
  leaves the common accident — sourcing the file niwa just wrote and tracing execution —
  completely uncovered, for the sake of avoiding a two-parameter change.

**Consequences**

Two function signatures change and nine call sites are touched, seven of them tests that pass
`nil`. `internal/workspace/setup.go` and `gitutil.go` each gain an `internal/secret` import.
No new types, no new config surface, no `Reporter` change, no secret-subsystem change. The
`nil`-tolerant parameter keeps every existing test compiling with a one-token edit and keeps
`RunSetupScripts` usable from any future caller that has no redactor.

Because the scrub sits at the single point where a script byte becomes a niwa-handled string,
this decision composes with either output-routing outcome without rework, and there is exactly
one line to audit when someone asks "is setup-script output redacted?"

What becomes harder: a script whose legitimate output happens to contain a registered value now
renders `***` there, and the operator has to recognize that as redaction rather than as the
script misbehaving. Naming the placeholder in the docs mitigates this; there is no code fix that
does not reintroduce the opt-out.

**The design's Security Considerations section must state four limits, because redaction is a
mitigation and not a control:**

1. **Only values niwa itself resolved are scrubbed.** A script that runs `gh auth token`, reads
   a keychain, queries a cloud metadata endpoint, or sources an unmanaged `.env` and echoes the
   result is covered by nothing — the redactor has never seen those bytes. The same applies to
   anything the operator exported into their own environment before invoking niwa.
2. **Only verbatim re-emission is caught.** Matching is plain substring against raw plaintext,
   so base64, URL-encoding, JSON-escaping, and shell-quoting all defeat it. Concretely:
   `marshalDotenv` (`envformat.go:52-62`) writes values with no quoting, so `cat .env.local` on
   the default target *is* caught — but `marshalJSON` (`:76`) escapes values, so a script
   echoing a JSON-format secret-output target can emit a form the redactor will not match.
3. **Multi-line secrets are not caught at all.** Scrubbing is per-line. Because dotenv
   marshalling does no quoting, a PEM private key or SSH key lands in `.env.local` with real
   newlines; a script that `cat`s it emits it across many lines, no single line contains the
   whole registered fragment, and nothing is redacted. This is the sharpest limit and it applies
   to precisely the secret type an operator would most regret leaking.
4. **Secrets shorter than 6 bytes are never redacted, and the documented compensating control
   for this does not exist.** `redactor.go:11-16` asserts that such secrets "must be rejected at
   resolution time with a hard error." `resolve.go:564-570` says the opposite in as many words —
   "We do NOT error on that case ... the only downside is that logs may contain the fragment
   verbatim" — and a grep for any resolve-time minimum-length check finds none. The two comments
   contradict each other and the permissive one is the implemented behavior. This gap predates
   this change, but printing script output is what makes it reachable, so the design should
   record it (and it is worth a follow-up issue against the secret subsystem, out of scope here).
<!-- decision:end -->
