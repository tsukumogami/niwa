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
