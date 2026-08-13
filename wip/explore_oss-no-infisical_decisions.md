# Exploration Decisions: oss-no-infisical

## Round 1

- **Both personas and both fix surfaces are in scope**: the OSS contributor with
  no overlay access and the maintainer on a host without Infisical; niwa's
  `required` contract and the tsukumogami base config are both open to change.
  Rationale: the two personas fail through different gates, so a single-surface
  fix closes neither completely.

- **Command scope covers the whole init -> create -> apply path plus
  `niwa dispatch`** and the SessionStart hook. Rationale: `init` turned out to
  succeed; the wall is at every instance-materializing command, and the
  launch-coupled paths cannot pass a CLI flag.

- **CI/automation is not treated as a distinct persona.** Rationale: the user
  did not select it; its needs are largely covered by the maintainer case.

- **Infisical as a vendor choice is out of scope.** The question is about
  backend-absence behaviour, not backend selection.

- **niwa must not name or reference an overlay in any message on the
  auto-discovery path.** GitHub returns 404 for both "private and inaccessible"
  and "does not exist", so niwa cannot distinguish them and would be asserting
  something it does not know. Naming the derived `<org>/<repo>-overlay` in a
  warning would also disclose the existence of a private repository to every
  contributor who runs the command. This reverses recommendations from two
  research leads (lead-existing-surfaces wanted the personal overlay named in the
  error; lead-static-detection wanted the silent overlay skip made visible).

- **The exception: an explicitly requested overlay should fail loudly.** When the
  user passed `--overlay` or `instance.json` already carries `overlay_url`, they
  asserted the overlay exists and are entitled to know the clone failed.
  Auto-discovery failure stays silent, or at most reports "no overlay found"
  without naming a repository.

- **Consequence for messaging**: an OSS contributor's output can only say that
  declared secrets were not resolved because no provider is configured. It cannot
  explain where the values were supposed to come from. This raises the weight of
  the tsukumogami config fix — the base config's key descriptions and README
  become the only place a contributor learns what the keys are for and that they
  are skippable.

- **Omit, never write empty.** Whatever the final severity model, an unresolved
  key must be absent from the generated `.local.env` rather than written as an
  empty value. Rationale: this preserves the defensible half of R34's intent, and
  downstream tools distinguish "unset" from "set to empty string" — the latter
  produces failures several layers away from the cause.

- **Remedy shape chosen: consumption-placed, strict-when-reachable.**
  Materialization proceeds with unresolved keys omitted, niwa reports the gap
  loudly, and the command exits 0. `required` stays fatal only when a provider is
  configured AND reachable and the key is simply absent — a genuine authoring
  error. Rationale: no comparable tool makes secret resolution a precondition for
  constructing the environment, and this keeps the maintainer's steady state
  exactly as strict as it is today while unblocking both failing personas. Cost
  accepted: it is the largest of the four candidate shapes and touches R34, its
  guide text, and `TestApplyAllowMissingSecretsDoesNotDowngradeRequired`.
  Rejected: default-softened-only (fixes the maintainer persona but not the OSS
  contributor's static contradiction, which never reaches the vault) and
  narrow-bug-fixes-only (leaves placement intact and cannot reach `dispatch` or
  the SessionStart hook, which pass no flags).

- **Unresolved keys must be annotated as comments in the generated
  `.local.env`.** Each key that was declared but could not be resolved gets a
  comment naming it and its declared description. Rationale: a terminal warning
  scrolls away, but the comment sits in the file a contributor opens when they
  wonder why a variable is missing, and it gives the omit-don't-write-empty rule
  a visible trace so "absent" does not read as "niwa forgot". Names only — never
  values, and never the reason if that reason would name an overlay.

- **A `--strict` (or similarly named) flag must enforce resolution and fail the
  command.** The permissive behaviour becomes the default; strictness becomes
  opt-in rather than removed. Rationale: anyone using today's hard gate as a
  deployment or provisioning guard needs a way to keep it, and per the prior-art
  naming discipline an escape hatch should name the exact failure it governs.
  Open: whether one flag covers all four gates or whether strictness needs to be
  per-gate, and whether it also belongs as a workspace-level setting given that
  `dispatch` and the SessionStart hook cannot pass flags.
