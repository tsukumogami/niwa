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


## Round 2

Round 2 was scoped to measurement rather than new territory: three questions
round 1 had inferred rather than tested.

### Key Insights

**Only `bypassPermissions` and `auto` are dead. Everything else works.**
Measured on Claude Code 2.1.267, project-scope `permissions.defaultMode` is
honored for `default`, `acceptEdits`, `plan`, and `dontAsk` -- confirmed both
from the headless init event's `permissionMode` field and behaviorally,
including the decisive case where a project-scope `"default"` pulled a
user-scope `acceptEdits` back down to prompting and the write was actually
denied. (lead-restrictive-mode-probe)

**So `niwa watch`'s operator-approval posture is sound, and the fix narrows.**
`ApplyReviewSettings` writes `defaultMode: "default"` so a PreToolUse hook's
`ask` decision is honored rather than silently allowed, and
`VerifyReviewSettings` hard-checks it. That value governs the session for real.
The key has a legitimate resident; removing it wholesale would break a posture
that currently works. The framing is therefore "stop writing the values that
cannot work", not "stop writing the key". (lead-restrictive-mode-probe,
correcting round 1's open question)

**The inert value is not merely inert -- it is actively harmful.** This is the
round's most consequential finding and it inverts a round 1 assumption. A
project-scope `bypassPermissions` does not fall through to the next layer down.
It wins the scope merge and is *then* downgraded to `default`, so a developer
whose own user settings say `acceptEdits` or `plan` gets `default` instead.
Writing `bypassPermissions` into an instance leaves the session **more
restrictive than writing nothing at all**, by destroying whatever the
operator's personal settings would have contributed. That is a real,
user-visible behavior change, not a documentation problem.
(lead-restrictive-mode-probe)

**"Is this permissive" is not the criterion that selected the blocked set.**
`dontAsk` is honored from project scope despite reading as permissive and
sitting next to `auto` in the flag's choice list. Whatever rule picked the
blocked pair, it is not the one round 1 inferred.
(lead-restrictive-mode-probe)

**niwa can vacate the `--settings` slot entirely.** `--remote-control` composes
with `--bg`: a backgrounded session with no terminal attached connected for
real, it throws the same internal switch `remoteControlAtStartup` throws, and
its optional name is expressiveness the boolean setting does not have. The slot
is not "already taken and therefore contested" -- it is taken by something that
has a first-class flag. (lead-settings-slot-probe)

**`--settings` merges rather than replaces.** It is a distinct settings source
(`flagSettings`) sitting between managed policy and user settings, composing
the way the layered system composes anything else: arrays concatenate, objects
merge key-wise, scalar collisions go to the higher-precedence source. The worry
that a merged builder might silently nuke a repo's settings does not
materialize. The correction to that mental model is that merge is per-key --
several keys have their own composition rules, and several are only read from a
subset of sources. (lead-settings-slot-probe)

**A dedicated flag silently beats the settings document for the same concept.**
`--permission-mode` overrode `permissions.defaultMode` with no warning. The
rule for any future builder is own both channels or own neither: writing a mode
into the document while something else passes the flag produces a value that
never takes effect and never complains. (lead-settings-slot-probe)

**The producer/consumer graph is closed inside niwa.** Nothing in koto,
shirabe, tsuku, the workspace config repos, or the private repos reads
`permissions.defaultMode`, parses a materialized settings document, or acts on
the value -- verified across code, hook scripts, skill and command markdown, CI
config, and prose. A rename or relocation breaks nothing externally.
(lead-external-readers)

**But the input surface is not closed, and it is a separate decision.**
`[claude.settings] permissions = "bypass"` in the public workspace config is
the live declaration for a real workspace, reproduced verbatim in a draft
headed for a public channel, and a legacy installer in another repo
independently writes the same dead key with the same rationale. Changing the
TOML surface is a compatibility break; changing the JSON key it materializes
into is not. (lead-external-readers)

**One relocation option is fenced off from outside the repo.** A sibling public
repo's acceptance criteria state that the committed `.claude/settings.json`
carries no `permissions` key. Whatever shape wins must not land there.
(lead-external-readers)

### Tensions

**The round 1 "smallest change" framing no longer fits the problem.** Round 1
weighed a one-line rename against a larger structural fix on grounds of size.
The clobbering finding changes what is being fixed: this is not a misleading
document, it is a live regression that makes dispatched sessions more
restrictive than doing nothing would. A fix scoped to honesty leaves the
regression in place.

**What to say about the merged builder cuts both ways.** The single-slot
constraint is real -- a second `--settings` silently discards the first -- but
niwa's occupancy of the slot is an artifact of using a setting where a flag
exists. That weakens the urgency for a merged builder while strengthening the
case that whoever wants the slot next can simply have it.

### Gaps

- The version boundary was not bisected. 2.1.257 and 2.1.142 come from release
  notes; only 2.1.267 was measured. The repo's own design doc says 2.1.258 in
  four places and is wrong on the release-note reading either way.
- Whether managed or enterprise scope behaves like project scope was untestable
  on this account. Not load-bearing: niwa writes instance-local files.
- The mechanism behind the downgrade -- merged then sanitized, versus sanitized
  at parse time -- was not distinguished. The observable outcome is identical.

### Decisions

Recorded in `wip/explore_inert-defaultmode-key_decisions.md` (D8-D11).

### User Focus

No interactive author; auto mode per the scope file. The brief's stated
priorities continue to stand in for the author's narrowing.

## Decision: Crystallize

## Accumulated Understanding

The brief asked what shape a niwa-internal signal should take. Two rounds of
research turned that into a different and larger question, because the key the
signal rides on is not merely inert.

**What is actually true.** Claude Code stopped honoring `bypassPermissions`
from project and local scope at 2.1.257, and `auto` at 2.1.142; `default`,
`acceptEdits`, `plan`, and `dontAsk` are all still honored from those scopes,
measured on 2.1.267. The ignored values do not fall through to the layer below
-- they win the scope merge and are then downgraded to `default`. So the
`bypassPermissions` niwa writes into every instance does not just fail to grant
bypass; it suppresses whatever posture the developer's own user settings would
have contributed, leaving the session more restrictive than if niwa had written
nothing. The false signal a reader sees in the document is the visible half of
a live regression.

**What niwa does today.** One builder, `buildSettingsDoc`, fans the key out to
three documents -- the instance-root `settings.json`, each repo's
`settings.local.json`, and the workspace-root `settings.json`. Exactly one has
a niwa reader: the `--permission-mode` derivation, which reads the materialized
output to recover an input the same function computed and discarded a few
hundred lines earlier. The other two copies exist only to speak to Claude Code,
which is the audience that stopped listening. Half the accepted input
vocabulary has never worked at all -- `"ask"` maps to `askPermissions`, which
is not a Claude Code mode -- and that has been pinned by a unit test and
enshrined in a PRD since before the deprecation. A second subsystem,
`niwa watch`, writes a fifth value into the same file for a genuinely
functioning purpose. And `WorkerPermissionMode` is dead code that a shipped
design doc already declared dead and then left in place with its test.

**What it would cost to change.** Little, and the fence posts are known.
Nothing in apply, drift, or verification depends on the key -- drift is an
advisory whole-file hash checked before the overwrite. Nothing outside the niwa
repo reads it. The two `@critical` scenarios assert the derived argv rather
than the file, which makes them the right regression net for a rename or a
relocation provided producer and reader move together; the hazard is the unit
tests, which fabricate their own fixtures and would go quietly green. Two
golden manifests need regenerating under any option. One option is fenced off
from outside the repo: the signal must not land in the committed per-repo
`settings.json`.

**Where the shape question landed.** Four candidates went in and one survives.
Annotating in place is schema-illegal in the only spot it would go, since the
published schema makes the `permissions` object `additionalProperties: false`,
and no project was found that annotated a dead key it kept writing. Moving into
the `--settings` payload works -- measured -- but spends a slot on a signal
`--permission-mode` already carries for free. Renaming into niwa's existing
`keepAliveOnDispatch` family is one line and matches in-repo precedent twice
over, but it preserves the round trip and answers only the honesty complaint,
not the regression. What is left is to stop writing the values that cannot
work, and to feed the derivation from the effective config the provisioning
call already computes and throws away -- which deletes a file read rather than
adding a key, matches every external precedent found, and disposes of the
`askPermissions` nonsense in the same motion.

**What this says about the merged settings-document builder.** The brief asked
whether this problem strengthens that case, and the answer is no, in a way that
should be useful to the sibling session. The single-slot constraint is real --
a repeated `--settings` silently discards the first document without parsing it
-- but niwa is holding the slot for remote control, and `--remote-control`
composes with `--bg` and does the same job better. The slot has a phantom
occupant; whoever wants it next can have a clean one. What is worth adding
regardless is a single-owner guard, so the constraint stops being tribal
knowledge, and the rule that a dedicated flag silently beats the settings
document for the same concept, so a future builder owns both channels for a
given key or neither.

**What is left open, and it is not the shape.** The work spans a materializer
change, a dispatch-input change, a dead-code deletion, and a set of committed
documents asserting a mechanism that no longer exists -- four inside niwa and
three outside it. The requirements for that set are not written down anywhere,
and the ordering matters, because producer and reader have to move in one
commit for the `@critical` scenarios to hold. That is a feature to be worked
out, not a decision to be recorded.
