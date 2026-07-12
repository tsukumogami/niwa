<!-- decision:start id="niwa-onboard-command-surface-exit-codes" status="assumed" -->
### Decision: Command surface, flag surface, and exit-code vocabulary for `niwa onboard`

**Context**

`niwa onboard` must cover two setups (team, individual) through one wizard
(PRD R1), with setup detection that is always confirmable and overridable
(R2), a topology choice that is also confirmable and overridable (R3), a
non-TTY fail-fast precondition (R18/AC-30), and five distinct terminal
outcomes that scripts can branch on (R16/AC-26: success, wizard-end
verification failure, authentication failure, operator decline/abort,
storage-write failure). Two superseded designs — provider-auth-provision
(tsukumogami/niwa#194) and vault-doctor (tsukumogami/niwa#199) — each chose
their own exit-code scheme (0/3/4/5 and 0/1/2 respectively), but neither
ships as a standalone command going forward (PRD D8, Out of Scope); their
vocabularies are historical record, not a constraint to reconcile literally.
The repo's existing convention (`internal/cli/root.go`'s `Execute()`) is a
per-command-family `errors.As` dispatch: `*sessionattach.ExitCodeError` and
`*workspace.InitConflictError` each carry their own small integers with
meaning scoped to the commands that construct them — there is no
cross-command global exit-code registry today, and code 1 is the one value
with actual cross-command meaning (the generic unclassified-error fallback).

**Assumptions**

- A flag-conflict usage error (e.g. `--team` and `--individual` both given)
  does not need a dedicated exit code, by analogy to the codebase's
  `--overlay`/`--no-overlay` precedent (plain error, generic exit 1) rather
  than `--bootstrap`/`--no-bootstrap` (PRD-mandated dedicated code 2). If
  this is wrong, the DESIGN doc adds a seventh/eighth code for it — a
  mechanical addition, not a redesign.
- The five R16/AC-26 outcomes plus the R18/AC-30 non-interactive precondition
  are the complete set of outcomes needing dedicated codes for v1; no other
  wizard-internal condition (e.g., a mid-wizard Ctrl-C) needs its own code
  beyond folding into decline/abort, matching AC-26's explicit "abandoning a
  guided dashboard step folds into operator decline/abort" instruction.
- `errors.As`-based dispatch means two different typed-error families can
  reuse the same integer without runtime ambiguity, so "not colliding with
  codes already used by other niwa commands" is satisfied by giving
  `niwa onboard` its own typed error family with a self-consistent
  vocabulary — not by hunting for globally-unclaimed integers across every
  existing command.

**Chosen: Single command, boolean override-flag pairs, fresh sequential exit-code vocabulary**

One cobra command, `internal/cli/onboard.go`, registered exactly like every
other command (package-level `var onboardCmd`, flags bound in `init()`,
`rootCmd.AddCommand(onboardCmd)`). No subcommands.

*Flags:*
- `--team` / `--individual` — mutually exclusive booleans overriding R2's
  inferred setup, mirroring `init.go`'s `--bootstrap`/`--no-bootstrap` shape
  exactly (a plain hand-written `if teamFlag && individualFlag { return err
  }` check, not cobra's `MarkFlagsMutuallyExclusive`).
- `--same-login` / `--split-login` — mutually exclusive booleans overriding
  R3's inferred topology, meaningful only within the individual path;
  combining either with `--team` is the same class of plain-error usage
  conflict as above.
- `--json` — single `BoolVar`, following `list.go`'s
  `json.NewEncoder(cmd.OutOrStdout())` pattern. Its terminal-outcome envelope
  is `{"status": <vocabulary string tied 1:1 to the exit code>, "setup":
  "team"|"individual", "exit_code": <int>, "detail": "<non-secret
  message>"}` plus setup-specific non-secret identifiers (identity id,
  `client_id`, client-secret id, store target, kind/project) per the R17/AC-27
  secret-hygiene surface — never a secret value, at any exit path. Full field
  enumeration per setup is DESIGN-level detail; this decision fixes only the
  envelope and the exit-code/status coupling.
- `--no-progress` is inherited from the root command; no onboard-specific
  progress flag is needed.

*Non-TTY behavior (R18/AC-30):* when stdin is not a TTY and neither the
setup override nor (when relevant) the topology override supplies the needed
input, the command fails fast with a fixed diagnostic before any state
changes — same shape as `init.go`'s `handleNoMarkerR13` non-TTY path.

*Exit-code vocabulary* — a new `onboard.ExitCodeError{Code int, Msg string}`
type (same two-field shape as `sessionattach.ExitCodeError`), with a third
`errors.As` arm added to `root.go`'s `Execute()`:

| Code | Outcome | Requirement |
|---|---|---|
| 0 | success | — |
| 1 | *(reserved — generic/unclassified error, unchanged repo-wide meaning; not assigned to any onboard-specific outcome)* | — |
| 2 | non-interactive precondition fail-fast | R18/AC-30 |
| 3 | operator decline/abort mid-wizard | R2/AC-4, AC-32 |
| 4 | authentication failure | R9/AC-14 |
| 5 | storage-write failure | R8 step 4/AC-34 |
| 6 | wizard-end verification failure | R11/AC-18b |

Ordering follows the wizard's own pipeline sequence (precondition check →
confirm/decline → mint+authenticate → store → verify), which is a more
useful mnemonic than the PRD's prose-listing order.

**Rationale**

R1 already forecloses the subcommand alternative as a matter of requirements,
not preference — "the command surface MUST NOT split into separate per-role
commands." A single command is also the only shape that satisfies R2's actual
mechanism: the wizard, not the operator's command choice, must present the
inferred setup for confirmation before any state changes. Subcommands turn
that confirmation into "guess which subcommand to type," reintroducing the
exact discovery burden the feature exists to remove.

The boolean-pair flag shape (`--team`/`--individual`, `--same-login`/
`--split-login`) is chosen over an enum flag because it matches the nearest
existing precedent for this exact shape of choice — `init.go`'s
`--bootstrap`/`--no-bootstrap`, explicitly called out in the codebase-seams
research as "the closest existing precedent for a multi-step wizard" — rather
than the enum-flag precedent (`--store=file|vault`), which belongs to a
design that isn't shipping as a standalone CLI surface. Both of onboard's
overridable choices are inherently two-way, so a boolean pair costs no more
validation code than an enum allow-list while reading more consistently
alongside `init`.

The exit-code table is fresh and sequential rather than inherited from either
prior design because neither prior ships as a standalone command (D8), and
because the wizard's outcome set is genuinely different from either prior's
(a multi-step sequence, not a single REST call or a single read-only check).
Reserving 1 as the untyped fallback (not assigning it to any specific onboard
outcome) avoids the one real ambiguity risk in this codebase: code 1 already
means "something unclassified went wrong" everywhere, and giving it a
specific onboard meaning would blur that signal for a caller trying to
distinguish "expected failure class" from "unexpected bug." Everything else
in the codebase's exit-code usage is already command-scoped rather than
globally unique, so a fresh, internally consistent 2-6 range for `onboard`
does not create any new ambiguity — a caller only ever holds one command's
exit code at a time.

**Alternatives Considered**

- **Two subcommands (`onboard team` / `onboard individual`)**: rejected
  because PRD R1 explicitly forbids a per-role command split, and
  independently because it would push R2's setup-inference decision back
  onto the operator's command choice rather than the wizard's own
  confirmable branch, undermining US-1/US-2's stated goal of removing that
  discovery burden. Also would duplicate the non-TTY-fail-fast and `--json`
  envelope logic across two command definitions.
- **Single enum flag (`--setup=team|individual`, `--topology=same-login|split-login`)
  instead of boolean pairs**: rejected in favor of the boolean pair because
  the nearer, more directly-cited precedent in this codebase
  (`--bootstrap`/`--no-bootstrap`) is a boolean pair, and both of onboard's
  choices are inherently binary, so the enum's generality buys nothing here.
- **Inheriting one of the two priors' exit-code schemes wholesale (0/3/4/5 or
  0/1/2)**: rejected because neither prior ships as a standalone command
  (D8), the wizard's five-plus-one outcome set doesn't map 1:1 onto either
  scheme, and literal reuse of either scheme's numbers for different meanings
  (e.g. provision's "4" means target-env unreadable; onboard's would mean
  authentication failure) would be actively confusing to anyone who worked on
  the superseded designs, with no offsetting benefit since neither is a live
  surface to stay compatible with.
- **Giving the `--team`/`--individual` (and topology) mutual-exclusion
  conflict its own dedicated exit code**: considered and rejected in favor of
  a plain error (generic exit 1), matching the codebase's `--overlay`/
  `--no-overlay` precedent — a dedicated code is only warranted when a
  requirement explicitly demands one (as R25 did for `--bootstrap`/
  `--no-bootstrap`), and no onboard requirement makes that demand.

**Consequences**

- `internal/cli/onboard.go` is a single new file following the established
  package-level-var/`init()`/`AddCommand` shape; no changes to `root.go`
  beyond adding one `errors.As` arm for `*onboard.ExitCodeError`.
- The DESIGN doc still owns: the exact wizard state machine and how it maps
  internal states to these flags/codes, the full `--json` per-setup field
  list, the resume/idempotence bookkeeping (R15/R20/R21), and the
  block-insertion mechanics for config authoring (R12) — none of that is
  fixed by this decision.
- Functional tests (R19) can assert exit codes numerically per the table
  above without ambiguity against any other command, since dispatch is by
  concrete Go type, not by raw integer.
- If a future requirement adds a sixth onboard outcome, it takes the next
  free integer (7); the reserved-1 rule and the pipeline-ordering mnemonic
  both remain intact.
<!-- decision:end -->
