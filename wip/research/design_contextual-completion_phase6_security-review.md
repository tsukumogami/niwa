# Phase 6 Security Review: contextual-completion

Reviewer perspective: adversarial read of the Phase 5 analysis and the
Security Considerations section in the design doc. Goal is to surface gaps,
challenge "not applicable" calls, and decide whether the proposed mitigations
are enough to ship.

## Verdict

**Approve with minor edits.**

The design has a correct threat model (local-only subprocess, no new exec or
network surface, read-only on files the CLI already reads) and proposes a
reasonable hardening (filter `\t`, `\n`, `< 0x20` from filesystem-derived
candidates). The Phase 5 analysis is thorough on the cobra-protocol angle.

Three items are under-specified and one residual risk should be escalated.
None are blockers; all can fold into Phase 1 or a follow-up issue.

## Findings

### F1 — Sanitization filter is ASCII-only; misses Unicode spoofing bytes [should-fix]

The one-liner filter

```go
if strings.ContainsAny(name, "\t\n") || hasControlChar(name) { continue }
```

with `hasControlChar` covering `< 0x20` is sufficient to defend cobra's
`__complete` protocol (which uses literal `\t` and `\n` as delimiters) and
the test harness's `completionSuggestions` helper. That's the stated design
goal and the filter achieves it.

It does **not** defend the user's terminal against name-based spoofing from
a filesystem under partial attacker control (shared workspaces, crafted repo
clones, malicious tarballs). Codepoints that sneak past a `< 0x20` check:

- **U+0085 NEL** (0xC2 0x85 in UTF-8). C1 control, treated by some terminals
  as a line break. Passes a `< 0x20` byte-level test because no byte in its
  UTF-8 encoding is below 0x20.
- **U+2028 LINE SEPARATOR** and **U+2029 PARAGRAPH SEPARATOR**. Not
  delimiters for cobra, but some terminal emulators render them as newlines
  when the completion menu draws candidates.
- **U+200B / U+200C / U+200D** zero-width space, ZWNJ, ZWJ. A repo named
  `api\u200B-prod` displays as `api-prod` but resolves to a different path.
  Enables confusion with a legitimate sibling repo.
- **U+202A-U+202E** bidirectional override characters. Classic spoofing
  vector — a repo named `fro\u202Ednammoc` renders as `command-end` in a
  left-to-right terminal. User sees what looks like a familiar name, picks
  it, and `niwa go` resolves the real (attacker-chosen) directory.
- **U+FEFF** BOM / zero-width non-breaking space. Same confusion class.
- Trailing ASCII space or `.` — not a security bug, but renders
  ambiguously in decorated lists.

Severity is **low-to-medium**, gated on the attacker having write access
under a workspace root the victim uses. The phase-5 framing of "attacker
with workspace write access already wins" is defensible for local solo
workspaces, but shared/cloud-mount scenarios (NFS team directories, shared
container volumes, a `git clone` of an attacker-controlled repo) make
"owns the workspace" a weaker assumption than the design implies.

**Recommendation:** tighten the filter to reject names containing any
Unicode codepoint in the Cc (control), Cf (format), or Zl/Zp (line/paragraph
separator) categories, plus the BOM. Go's `unicode.IsControl` covers Cc
and most of what matters; `unicode` category tests give Cf and Zl/Zp. A
small helper is fine:

```go
func safeForCompletion(name string) bool {
    if !utf8.ValidString(name) { return false }
    for _, r := range name {
        if r == '\t' || r == '\n' { return false }
        if unicode.IsControl(r) { return false }        // Cc
        if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
            return false
        }
    }
    return true
}
```

Alternatively, apply the same rule niwa already uses for
identifier-validated workspace names (presumably `[A-Za-z0-9._-]`) to
filesystem-discovered names as well. That's stricter than the attack
surface requires but simpler to reason about and consistent with how the
registry path handles the problem.

### F2 — "External Artifact Handling: Applies No" understates one edge [nit]

Phase 5 claims "downloads nothing and executes nothing from remote
sources." That is true for the completion subprocess itself. The *shell
wrapper script* it installs (bash V2 / zsh) is generated text committed to
`$TSUKU_HOME/share/shell.d/` at `shell-init` time. If the shell wrapper
format ever changes to `eval` candidate strings (some legacy bash V1
implementations still do), the whole threat model shifts.

The design already pins bash V2 / zsh as targets and Cobra's generators as
the source, so this is fine for v1. Flag in the design doc as: "Shell
wrapper assumes non-eval'ing completion (bash V2, zsh). A future downgrade
to bash V1 must re-visit this review." One sentence, nothing more.

### F3 — DoS surface on tab press is acknowledged but unbounded [should-fix]

Phase 5 notes the symlink-to-slow-FS case under "Symlink traversal" and the
design accepts this as same-class-as-runtime-latency. Two additional DoS
angles are not covered:

- **Workspace with 10^5+ instance directories or repo entries.** `os.ReadDir`
  loads the full dirent list; `EnumerateInstances` then `os.Stat`s each.
  At 100k entries on a cold cache, every TAB press stalls multi-second.
  Not a security boundary violation, but it's a user-observable freeze in
  a config shape that a shared team workspace could plausibly produce.
- **Oversized `config.toml`.** `LoadGlobalConfig` reads and unmarshals the
  whole file. A multi-megabyte `$XDG_CONFIG_HOME/niwa/config.toml` slows
  every completion. Attacker scenario requires writing to the user's
  config dir, which Phase 5 rightly rules as "already owns the box," but
  a buggy niwa write in a past version could leave a bloated file behind.

**Recommendation:** add one sentence to Security Considerations
acknowledging "enumeration is unbounded; O(N) in directory size" and
either (a) punt to a future cap (follow-up issue) or (b) cap at, say,
10k entries and return truncated. Option (a) is fine for v1; the
important thing is the limit being a documented known gap, not an
unstated assumption.

### F4 — Sudo / root-invocation path deserves one explicit line [nit]

A user running `sudo niwa <TAB>` will cause the shell to call
`niwa __complete` either as the original user (most shells) or root
(depending on `sudo`'s env preservation and the shell's completion
integration). In the root-invocation case, the subprocess reads
`$XDG_CONFIG_HOME/niwa/config.toml` from whoever `HOME` resolves to,
which under `sudo -E` or a root rc file can be `/root/.config/niwa/...`.

This is not an attack surface niwa introduces — it's a general property
of shell completion under sudo. But because completion "reads the user's
config" is a phase-5 load-bearing claim, the design should clarify that
"user" means "the UID the subprocess runs as, which under `sudo` may
differ from the interactive user." One-liner addition.

## Answers to the posed questions

### 1. Attack vectors not considered

- **Malformed TOML:** Phase 5 covers this under "Tab-induced side effects"
  (swallowed error, empty candidate list). Adequate.
- **Large-file DoS:** not covered. See F3.
- **Environment variable injection:** `XDG_CONFIG_HOME` is trusted to be
  under the user's control; if an attacker controls the user's env, niwa
  is not in the trust boundary. Adequate framing.
- **PATH manipulation:** the shell wrapper calls `niwa __complete`. If an
  attacker has prepended a malicious `niwa` to PATH, they have already
  compromised every niwa invocation, not just completion. Correctly out
  of scope.
- **Race conditions with config writes:** covered under TOCTOU. Adequate.
- **Attacker-controlled workspace directories:** partially covered. The
  symlink section addresses filesystem traversal and latency; the
  sanitization section addresses the cobra protocol. Unicode spoofing
  (F1) is the gap.
- **Completion running with sudo'd privileges:** not mentioned. See F4.
- **CI / bot auto-TAB:** an automated agent that emits literal TAB bytes
  into an interactive shell will trigger completion. Since the subprocess
  is read-only, the worst case is leaking workspace/instance names to the
  agent's log. Same data exposure class as running `niwa list` in the
  automated shell, which is user-controlled. No new mitigation needed.
- **Shared-terminal (screen/tmux) visibility:** Phase 5 acknowledges
  ("screen-shared terminal pressing TAB surfaces workspace names").
  Consistent with the `niwa list` baseline. Adequate.

### 2. Sanitization sufficiency

**No** for spoofing-class defense. **Yes** for protocol-parsing defense.

The `\t`/`\n`/`< 0x20` filter is exactly what cobra's protocol needs — it
prevents phantom candidates and keeps the test harness parseable. It does
not handle UTF-8 surrogates (Go strings are already validated for UTF-8 at
read boundaries; a non-UTF-8 filename would still pass the byte test), bidi
overrides (U+202A-U+202E), zero-width joiners (U+200B-U+200D), NEL
(U+0085), or line/paragraph separators (U+2028, U+2029).

See F1 for the proposed widening.

### 3. "Not applicable" justifications

- **External Artifact Handling — Applies No:** correct for the completion
  subprocess; nit per F2 on the shell wrapper edge.
- **Permission Scope — Applies Yes, low severity:** correct. The subprocess
  needs exactly the reads the interactive CLI needs; no widening.
- **Supply Chain — Applies Yes, low severity:** correct. No new modules;
  cobra's completion infra is the same one hundreds of CLIs ship.
- **Data Exposure — Applies Yes, very low severity:** correct framing. The
  screen-shared-terminal case is acknowledged and bounded by the
  `niwa list` baseline.

All four dimension calls hold up.

### 4. Residual risks to escalate

1. **Unicode spoofing in completion candidates (F1)** — should be fixed in
   Phase 1 alongside the existing ASCII filter. Low-cost to widen.
2. **Unbounded enumeration on pathological workspaces (F3)** — file a
   follow-up issue for "cap enumeration at N entries, emit a
   `ShellCompDirectiveKeepOrder` truncation marker, and document the
   cap." Not a v1 blocker.
3. **Sudo invocation semantics (F4)** — one-sentence doc clarification in
   the design; no code change. Not a v1 blocker.
4. **Destructive-command UX (Decision 3)** — correctly classed as non-
   security by Phase 5. No action. Flagging only so reviewers don't
   re-litigate it later.

## Summary

Ship the design with F1 folded into the Phase 1 sanitization filter (widen
beyond ASCII control chars to cover Cf/Zl/Zp categories and the BOM). F2
and F4 are one-line doc tweaks. F3 is a known-gap acknowledgement plus a
tracked follow-up issue. The core claim — that contextual completion adds
no new execution, network, or privilege surface — holds under adversarial
review.
