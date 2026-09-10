# Exploration Findings: inert-defaultmode-key

## Core Question

niwa's materializer writes `permissions.defaultMode` into every instance's
`.claude/settings.json`. Claude Code no longer honors the permissive values of
that key from project scope, so the written value does not govern the session
it appears to govern -- but niwa's own dispatch flow reads the same key back to
decide whether to forward `--permission-mode` on the worker command line. The
key is dead as a Claude Code setting and load-bearing as a niwa-internal
signal. What is the right shape for that signal, and what is the smallest
change that stops the settings document from making a claim it cannot keep?

## Round 1

### Key Insights

**The brief's central claim holds, with two corrections.** The version is
2.1.257, not the 2.1.257-vs-2.1.258 the brief and the repo's own design doc
disagree about -- `DESIGN-dispatch-permission-mode.md` says 2.1.258 in four
places and is wrong by one release. And the restriction is older than either
number for one of the two modes: `auto` stopped taking effect from project
scope at 2.1.142. (lead-claude-code-scope-rule)

**Only the permissive values are dead.** `bypassPermissions` and `auto` are
ignored from project and local scope; `default`, `acceptEdits`, and `plan`
carry no documented restriction. This matters more than it sounds, because
`niwa watch` writes `defaultMode: "default"` into the same file as the whole
mechanism of its operator-approval posture. "Stop writing
`permissions.defaultMode`" is therefore the wrong framing -- the key has a
live, legitimate resident. (lead-claude-code-scope-rule, lead-drift-and-tests)

**The graph is asymmetric, and that asymmetry is the problem.** One builder,
`buildSettingsDoc`, fans the key out to three documents: the instance-root
`settings.json`, each repo's `settings.local.json`, and the workspace-root
`settings.json`. Exactly one of the three -- the instance root -- has a niwa
reader, the `--permission-mode` derivation at `dispatch.go:550-557`. The other
two copies exist purely to speak to Claude Code, which is precisely the
audience that stopped listening. (lead-producer-consumer-graph)

**Half the accepted config vocabulary has never worked.** The mapping accepts
`"bypass"` and `"ask"`; `"ask"` produces `askPermissions`, which is not a
Claude Code permission mode at all. It is pinned by a unit test
(`materialize_test.go:434`) and enshrined in `PRD-config-distribution.md:197`.
The key was inert for `ask` long before 2.1.257 made it inert for `bypass`.
(lead-producer-consumer-graph)

**`niwa watch` is a fourth producer and a genuine reader, and its hard-deny
posture is already broken by the same change.** `containment.go:189-191` says
the session "inherits the `bypassPermissions` the dispatch applies", but watch
launches through `dispatchLaunch` with a package-level `dispatchPermissionMode`
that is always empty in a watch process, so no flag is forwarded. The stated
reasoning is wrong; the resulting session is less permissive than the comment
assumes, so the posture is not weakened, but the mechanism described does not
exist. (lead-producer-consumer-graph, lead-instance-metadata-home)

**Nothing in apply, drift, or verification depends on the key.** Drift is an
advisory whole-file hash checked before the overwrite, so niwa's own output
changing is invisible to it. There is no manifest, no marker, no schema check.
The only apply-layer cost of any option is regenerating two golden
characterization manifests -- which fires even under a comment-only
annotation. (lead-drift-and-tests)

**The `@critical` scenarios are argv-only, and that is exactly the right
regression net.** `dispatch.feature:51-88` runs the real materializer and the
real dispatch and asserts only that `--permission-mode bypassPermissions`
reaches the worker. Producer and reader can be renamed or relocated freely as
long as they move in one commit. The weak spot is the opposite of what you'd
expect: the seven unit tests in `dispatch_permissionmode_test.go` write their
own fixture bytes, so under a rename they go green while testing nothing.
(lead-drift-and-tests)

**The derivation's input is already in memory and is thrown away.**
`runDispatch` holds the raw workspace config 230 lines above the derivation,
and the fully merged, vault-resolved effective config -- the actual input the
materializer turned into `permissions.defaultMode` -- is computed inside the
same `provisionInstanceFunc` call moments earlier and discarded. Surfacing it
through `provisionResult` needs no re-resolve and follows the pattern
`pipelineResult` already uses for five other outputs. The governing design doc
weighed three options and all three read the output; its only stated reason
was an objection to re-parsing that this plumbing removes.
(lead-instance-metadata-home)

**Annotating in place is schema-illegal in the one spot it would go.** The
published Claude Code settings schema has root `additionalProperties: true`
but `permissions.additionalProperties: false`, so a sibling `_comment` next to
`defaultMode` is invalid -- a near-exact replay of the Docker Compose `x-`
under `depends_on` bug. (lead-prior-art-inert-keys)

**Every real precedent moves the signal out of the foreign document.**
Kubernetes replacing `last-applied-configuration` with server-side apply's
`managedFields`, Flux's status inventory, chezmoi's own state DB, npm's hidden
lockfile. No project was found that used an unknown-key namespace to annotate
a dead key it kept writing -- extension namespaces exist for live second-tool
config, not for tombstones. (lead-prior-art-inert-keys)

**The single-slot constraint is real and worse than recorded.** Repeated
`--settings` on Claude Code 2.1.267 was measured as last-wins, total
replacement, silent: `--settings 'GARBAGE' --settings '{}'` starts cleanly
without ever parsing the garbage. The two prior declines (`ef24ee8`,
`0981df7`) guessed at this; it is now measured.
(lead-settings-payload-channel)

**But `--settings` is the one document channel that still carries the
posture.** It was measured to honor a permissive `defaultMode` where the
project settings file does not. niwa has occupied that slot since
`b364915` for one boolean, and put a `--permission-mode` flag next to it
instead, without noticing. The dispatch-permission-mode design never
considered the channel at all -- its entire options section is about where in
the function the derivation goes. (lead-settings-payload-channel)

**niwa already ships the shape under discussion, twice.**
`keepAliveOnDispatch` is a niwa-defined key inside the same settings document,
documented as niwa-only, read back through the same `instanceSettings`
projection. `ephemeralSessionMode` goes further and demonstrates both halves at
once: the decorative copy in the settings document that nobody reads, and the
authoritative copy in `instance.json` that `session_map.go` actually consults.
(lead-producer-consumer-graph, lead-drift-and-tests,
lead-instance-metadata-home)

**`WorkerPermissionMode` is confirmed dead.** Zero non-test callers; its
mesh-daemon caller was deleted in `69b735f` (PR #151); the design doc already
says so and then the code was left in place with a 64-line table test.
(lead-producer-consumer-graph, lead-drift-and-tests,
lead-instance-metadata-home -- three independent leads found this)

### Tensions

**Rename inside the settings document vs move out of it entirely.** The
in-repo precedent (`keepAliveOnDispatch`) says a niwa-owned key in
`settings.json` is fine and costs one materializer line plus one struct field.
The external precedent says every project that faced this moved the signal out
of the foreign document and recomputed it from its own inputs. These point
different ways, and the tiebreaker is probably that the external precedents
concern *state* while niwa's question concerns a *derived value whose source is
still in memory* -- which is an argument for neither storing nor renaming, but
for not round-tripping at all.

**"Smallest change" vs "right shape".** The smallest honest change is one line
in `buildSettingsDoc` plus a JSON tag: rename the key into the niwa-owned
family. The right shape, on the prior-art reading, is to stop the round trip
and read the effective config the provision call already computed. The second
is larger but deletes a file read instead of adding a key.

**The `--settings` channel is available but contested.** It is the only
document channel that carries the posture, and it is occupied by one hardcoded
`fmt.Sprintf`. Spending it on a signal `--permission-mode` already ships for
free would be a poor trade -- but the finding that it *works* is new
information the repo's two prior declines did not have, and it changes what a
future permission-shaped need (a deny list, `additionalDirectories`) can do.

**Four committed docs are already false, independent of any change.** Most
sharply `docs/guides/ephemeral-session-instances.md:327-334`, which promises a
root-level bypass posture applies to every root session, and
`DESIGN-mcp-root-instance-distribution.md:222-231`, whose "no new code needed"
decision rests entirely on the expired behavior. Fixing the key without fixing
these leaves the false premise intact in the place a reader is most likely to
hit it.

### Gaps

- Whether project-scope `defaultMode` is honored for *restrictive* values was
  inferred from the absence of a documented restriction, not measured.
  `niwa watch`'s entire operator-approval posture rides on `"default"` being
  honored from that file.
- Whether Claude Code's `--remote-control` flag composes with `--bg`. If it
  does, niwa's sole occupant of the `--settings` slot can vacate it and the
  single-slot constraint dissolves. One command answers this.
- Whether a `--settings` document merges with the project settings layer or
  replaces it. A builder that assumes merge and gets replacement is a new class
  of bug.
- Whether anything outside the niwa repo reads `permissions.defaultMode` out of
  a niwa-materialized settings file. Only the niwa tree was searched; the
  workspace also holds `shirabe`, `koto`, and the plugin source.
- Whether Claude Code warns interactively on an unknown key inside
  `permissions`, and whether that warning drops the entry or the whole file.
  A headless probe surfaced nothing, and `claude doctor` reported nothing.

### Decisions

Recorded in `wip/explore_inert-defaultmode-key_decisions.md`.

### User Focus

No interactive author. Running in auto mode per the scope file; the narrowing
that would normally come from the author is taken from the dispatch brief's
own stated priorities: verify the claims, decide the shape, name the smallest
change, and say what the `--settings` constraint means for a merged builder.

## Accumulated Understanding

The brief was right about the shape of the problem and understated its extent.
The materialized `permissions.defaultMode` is not one inert key with one
internal reader; it is one builder writing to three documents, two of which
have no reader at all, with a second subsystem (`niwa watch`) writing a fifth
value into the same file for a different purpose, and half the accepted input
vocabulary mapping to a value Claude Code has never recognized. Three separate
leads independently confirmed that `WorkerPermissionMode` is dead code the
design doc already declared dead and then left in place.

The blast radius of changing it is small and well-fenced. Nothing in apply,
drift, or verification cares. The `@critical` scenarios assert the derived
argv rather than the file, which makes them the right net for a rename or a
relocation, provided producer and reader move together. The real hazard is the
unit tests, which fabricate their own fixtures and would go quietly green.

Four shapes were on the table and the field has narrowed to two. Annotating in
place is out: it is schema-illegal in the only place it would go, and no
project has been found that did it. Moving into the `--settings` payload is
feasible and was measured to work, but it spends a contested single slot on a
signal `--permission-mode` already carries for free -- so it is the right
answer to a different question, and the finding that the channel works is worth
recording for whoever asks that question later. What remains is renaming the
key into niwa's existing `keepAliveOnDispatch` family, which is one line and
matches in-repo precedent, versus stopping the round trip entirely by reading
the effective config the provisioning call already computed and discards, which
is larger but deletes a read rather than adding a key and matches every
external precedent found.

The strongest single finding is that the round trip is unnecessary on its own
terms: the derivation reads a materialized output to recover an input that is
still in memory a few hundred lines up the same function. Whichever shape wins,
that is the fact a design has to answer to.

What is still open is mostly measurement, and cheap: whether restrictive
`defaultMode` values survive from project scope (which decides whether
`niwa watch`'s ask posture is live or is a second instance of the same bug),
whether `--remote-control` composes with `--bg` (which decides whether the
single-slot constraint persists at all), and whether anything outside this repo
reads the key.
