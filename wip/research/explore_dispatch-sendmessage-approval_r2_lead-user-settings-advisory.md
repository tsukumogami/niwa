# Lead: The user-settings half — detection and advice

Round 2. Investigated in the worktree
`public/niwa/.claude/worktrees/dispatch-sendmessage-approval`. VERIFIED means read
at the cited line, or quoted from official Claude Code documentation. INFERRED
marks a conclusion drawn from those rather than stated by them.

The short version, up front, because it is a "do not build this" recommendation:
**niwa should not detect the user's `~/.claude/settings.json` state, and should
not print anything about it on dispatch.** It should say the thing once, on the
command where the developer opts into the dispatch-side grant, and write the rest
into a guide. The reasoning is in Findings 3 through 6.

## Findings

### 1. What the user must do, exactly

VERIFIED (Claude Code docs, via r1 lead "approval-mechanism", which quoted
https://code.claude.com/docs/en/settings-reference#crosssessioninbound and
https://code.claude.com/docs/en/cross-session-messaging). The key is
`crossSessionInbound`, it is **top-level** in the settings document — not nested
under `permissions` — and it takes one of `accept`, `hold`, `refuse`. `accept`
delivers every inbound peer message with no dialog.

The file is the user-scope settings file, `~/.claude/settings.json`. The exact
minimal JSON, for a user who has no such file yet:

```json
{
  "crossSessionInbound": "accept"
}
```

For a user who already has one — which is the actual case here, since r1 verified
this user's file already sets `permissions.defaultMode` to `plan` — it is one
added key alongside what is there:

```json
{
  "permissions": {
    "defaultMode": "plan"
  },
  "crossSessionInbound": "accept"
}
```

The docs name this exact remedy and its cost in one sentence, quoted in r1:

> To let a -p worker take messages unattended, start it with crossSessionInbound
> set to accept in its --settings value. An accept in your user settings also
> works but applies to every session you run.

**Why it must be user scope and not the project file.** VERIFIED, r1 quoting
https://code.claude.com/docs/en/settings: `crossSessionInbound` is one of a small
set of keys with **inverted precedence**. A project `.claude/settings.json` or
`.claude/settings.local.json` value is "Honored over managed, `--settings`, and
user values" only when it is *stricter* on the accept → hold → refuse ladder; "a
project or local value that isn't stricter is ignored". `accept` is the loosest
rung, so an `accept` written into a project-scope file is silently discarded.
That is what rules out niwa's normal materialization path
(`internal/workspace/root_materializer.go:224` writes
`<workspaceRoot>/.claude/settings.json`) for this key, and it is why the user's
own file is the only place a *user-side* `accept` can live.

**The alternative route, and it is the one to lead with.** VERIFIED
(https://code.claude.com/docs/en/settings):

> Beyond editing a settings file, you can select the value in the `/config` row
> **Messages from your other sessions**.

VERIFIED (https://code.claude.com/docs/en/cross-session-messaging), and the second
sentence is the interesting one:

> Claude Code writes the value you select to your user settings. The row requires
> Claude Code v2.1.232 or later and doesn't appear while managed settings or the
> `--settings` flag sets the key, since a user-settings value wouldn't apply then.

So the control exists, it is labeled exactly "Messages from your other sessions",
and it writes `~/.claude/settings.json` — the same key, the same file, no JSON
editing. The guide should lead with it and keep the raw JSON as the fallback for
older builds and for scripted setup.

The clause about the row disappearing under managed settings is worth dwelling on,
because it is Claude Code doing precisely the thing Finding 4 says niwa cannot:
the product knows when a user-settings value would be inert, and it responds by
*hiding the control* rather than by telling the user to set something that would
not apply. niwa, standing outside, has neither that knowledge nor that option.

### 2. The blast radius, stated honestly

VERIFIED from the docs sentence quoted above, and it is not a footnote — it is the
whole reason this half is not niwa's to apply.

The two grants are not equivalent, and the guide must not present them as two
routes to the same place:

- **What niwa gives its workers** is per-invocation. It rides `claude --settings`
  on one launched process (`internal/cli/dispatch.go:598`), it dies with that
  process, it reaches only sessions niwa itself started, and a developer can see
  the whole of it in the argv of one command.
- **What an `accept` in `~/.claude/settings.json` gives** is every Claude Code
  session that user ever starts on that machine, forever, until they edit the file
  back. Sessions that have nothing to do with niwa. Sessions in unrelated repos.
  Sessions started months from now. And it accepts from *every* sender the
  messaging fabric can reach, not only from niwa-dispatched peers.

That last clause is the part most likely to get glossed. r1's blast-radius lead
established (VERIFIED from the `SendMessage` tool contract) that delivery is
explicitly not machine-bounded — "a name that exactly matches one live agent or
session (on this machine, on another machine, or in the cloud) delivers directly"
— and that the reachable peer set is bounded by the Claude Code identity fabric
and "by nothing niwa controls". So `accept` in user settings is not "let my niwa
workers talk to me". It is "deliver any peer message this account can address into
any session I run, unread". The same lead also established, from the tool contract
itself, that the human at the delivery dialog is *the only enforcement* against
cross-session permission laundering — the contract's instruction not to launder is
prose addressed to the model, not a check. Removing the dialog machine-wide
removes that enforcement machine-wide.

niwa already has house language for exactly this asymmetry, and it is worth
copying rather than inventing. `launchableAgentsHint` at
`internal/cli/dispatch.go:1014-1017` explains why it offers the flag before the
environment variable:

> It offers the flag first and the variable second. The developer reading this is
> retrying one command, and --harness changes that command; NIWA_DISPATCH_HARNESS
> changes every niwa command in the shell, and everything dispatched from it,
> which is a bigger thing to hand someone who asked for one dispatch.

That is the same argument one level up. A `--settings` grant changes one dispatch;
an `accept` in user settings changes every session on the machine, which is a
bigger thing to hand someone who asked for one dispatch.

### 3. What diagnostic surfaces niwa actually has

I surveyed `internal/cli/` for a home for detection. The results narrow the
options sharply.

**There is no `niwa doctor`.** VERIFIED: grepping `internal/cli/*.go` and `cmd/`
for `doctor` returns nothing. The word "preflight" appears only as in-function
commentary — the dispatch binary-on-PATH check (`dispatch.go:375-383`), the init
target-exists check (`init.go:843-854`), and the watch sandbox capability probe
(`watch.go:157`, `watch.go:946`). None of them is a standalone diagnostic command.

**`niwa status` is the diagnostic surface, and it is the wrong shape for this.**
VERIFIED, `internal/cli/status.go:38-73`. It is scoped to a workspace instance —
"When run from inside an instance, shows detailed status... from the workspace
root, shows a summary table of all instances" — and everything resembling an audit
is behind an explicit opt-in flag: `--audit-secrets`, `--audit-auth`,
`--check-vault`, `--verbose` (`status.go:17-27`). A developer's personal Claude
Code settings are not a property of any instance, so there is no row for them in
either view, and the audit flags all report on things niwa itself produced (the
secrets tables, the credential sources from the last apply, the vault refs).
Adding "your global Claude config" to an instance status table would be the first
row in it that is not about the instance.

**`niwa dispatch` does print advisory notices, and there are many.** VERIFIED,
all in `internal/cli/dispatch.go`, all on `cmd.ErrOrStderr()`, all prefixed
`niwa dispatch: `:

| Line | Notice |
|---|---|
| 325 | workspace config unreadable, so its `default_agent` was skipped (`unreadableAgentRungNotice`, `agent.go:197`) |
| 332 | the shell still sets the renamed `NIWA_AGENT` (`renamedHarnessEnvNotice`, `agent.go:172`) |
| 356 | `--agent` names a subagent type but parses as a harness name (`harnessMismatchWarning`) |
| 421 | the worker starts at the instance root and misses the repo-scoped config document |
| 526 | a model warning |
| 556 | derived `--permission-mode bypassPermissions` from the workspace posture |
| 595 | remote control wanted but `ANTHROPIC_API_KEY` forces API-key auth (`apiKeyForcedWarning`, `dispatch_remotecontrol.go:21`) |
| 651, 654 | `--keep-alive` ignored for this agent / not applicable without remote control (`keepAliveNonRCWarning`, `dispatch_keepalive.go:42`) |

Every one of these is warn-and-proceed. None fails the dispatch. So "print an
advisory on dispatch" is unambiguously precedented as a *mechanism*. The question
is whether this particular advisory earns a slot, and Finding 5 says it does not.

**Precedent for detecting a condition in the developer's own environment, outside
any workspace — the real ones.** There are four, and they are more varied than the
brief's single guess:

1. **`hintShellInit`**, `internal/cli/hint.go:12-18`. Checks an environment
   variable (`_NIWA_SHELL_INIT`) and prints a two-line hint naming the exact
   command to fix it. Called from cd-eligible commands (create, go). This is the
   purest "your machine is not set up, here is the one command" precedent, and
   note what makes it cheap: the check is a single env-var read that cannot be
   wrong, and the remedy is a niwa command.

2. **`renamedHarnessEnvNotice`**, `internal/cli/agent.go:172-181`. Reads the
   developer's shell environment and tells them a variable in their profile is no
   longer read. Its doc comment states the governing principle precisely
   (`agent.go:161-167`): the rename "is a deliberate break rather than an alias,
   and the argument for taking it is that the fix is one line in a shell profile.
   That argument only holds if the developer is told which line."

3. **`ReadDeclaredMCPNames`**, `internal/workspace/payload.go:140-183`. **This is
   the closest structural analogue in the tree.** niwa reads a configuration file
   in the developer's own home — one it does not own — to check for a collision
   with what the workspace declares. It is read-only, it "never fails an apply",
   and, most importantly for my lead, **when it cannot read or parse the file it
   reports that it could not check rather than asserting a problem**:

   > the configuration at %s could not be read (%v), so niwa could not check the
   > MCP server names it defines against the ones this workspace declares; a
   > session there merges the two definitions field by field if they share a name

   That is the house answer to "what if you can't see the config", and I use it in
   Finding 4.

4. **The Codex trust writer**, `internal/workspace/codex_trust.go`. niwa *writes*
   `~/.codex/config.toml`. This is the strongest-looking precedent for "niwa may
   edit the developer's personal agent config", and Finding 5 argues it does not
   extend to this case.

The brief's guess — "the repo-scoped-config-doc warning for one of the harnesses"
— is real and is `dispatch.go:420-424`, but it is a different kind of thing: it
describes a property of *niwa's own delivery* (what the worker did not receive),
not a condition in the developer's environment. Its long comment block
(`dispatch.go:385-419`) is worth reading anyway for a different reason: it is a
sustained argument about *where a warning should be triggered from*, and it lands
on "the gate is the fact itself" after rejecting two narrower gates, one of which
would have gone silent when an unrelated gap was closed. The comment's own verdict
— "A warning that disappears because a different gap was closed is worse than no
warning: it reads as an all-clear" — is the same family of reasoning I apply
below, pointed the other way.

### 4. How niwa would detect it, and why the detection is not sound

Reading `~/.claude/settings.json` and looking for the key is trivial — niwa
already resolves the developer's home in half a dozen places
(`internal/cli/developer_home.go:24-30`, `internal/cli/plugins.go:49`,
`internal/pluginrecord/registry.go:136`). That is not the problem. The problem is
that **the key's presence in that one file is not the question anyone cares
about**. The question is the *effective* value in the receiving session, and niwa
cannot compute it:

- **There is no supported way to read the effective value at all.** VERIFIED by a
  docs sweep of the settings reference, the CLI reference, and
  https://code.claude.com/docs/en/debug-your-config: there is no
  `claude config get`, no `--print-settings`, and nothing else that resolves
  settings across scopes. `claude doctor` "prints read-only installation and
  settings diagnostics" but that means installation health and settings-file
  *validation errors*, not per-key values. `/status` shows a `Setting sources`
  line naming which files were loaded, not what they resolved to. `/permissions`
  resolves permission rules only. So an external tool has no choice but to read
  the files itself and reimplement the ladder — which is the maintenance liability
  described in the next bullet, now confirmed as unavoidable rather than merely
  likely.
- **Managed settings can set it and niwa may not be able to read them.** r1 left
  this explicitly open ("managed settings were not inspected"). VERIFIED paths:
  `/etc/claude-code/managed-settings.json` on Linux,
  `/Library/Application Support/ClaudeCode/managed-settings.json` on macOS; their
  read permissions are not documented on code.claude.com, so niwa cannot assume
  it can read them. If an enterprise policy file already sets `accept`, a niwa
  warning saying "you need to set this" is simply false, and it is false in the
  environment where the user has the least power to act on it. Claude Code's own
  answer to this case is to hide the `/config` row entirely (Finding 1) — it has
  the information to do that and niwa does not.
- **The precedence for this key is inverted, so niwa would have to reimplement a
  non-obvious rule.** Not "last scope wins" but "strictest project value wins over
  everything, looser project values are ignored, otherwise managed/`--settings`/
  user apply". Reimplementing that from the outside, against a shipping product
  that can change it, is a maintenance liability. The tree already knows this
  hazard by name in a different context: `agent.go:104-108`-era commentary and the
  `registry.go:80-90` note both argue against per-key precedence surprises.
- **niwa does not know which session is the receiver.** The gate is on the
  receiving side (r1's central finding). The session niwa would be warning about
  is the developer's own interactive terminal — which niwa did not launch, whose
  working directory it does not know, and which therefore resolves project-scope
  settings niwa cannot see. A worker might message a session rooted anywhere.
- **The user might not want it.** A developer who has deliberately left the dialog
  in place, because they want to read what their workers say before it lands, is
  not misconfigured. There is no signal that distinguishes them from a developer
  who has not yet heard of the setting.

**The failure mode of a false positive is worse than silence.** A warning that
says "your Claude Code settings block unattended message delivery; set
`crossSessionInbound` to `accept`" and is wrong teaches a developer to loosen a
security-relevant setting machine-wide for no reason, on niwa's authority. That is
a strictly worse outcome than the message being held once and the developer
clicking approve. Compare the honest shape `ReadDeclaredMCPNames` uses: it says
"niwa could not check", never "you have a collision". A truthful version of this
warning would have to read "niwa could not determine whether your sessions accept
peer messages unattended", which is a sentence that helps nobody.

**And the noise would be substantial.** A per-dispatch notice fires on every
`niwa dispatch`, which is the command a fan-out workflow runs in a loop — the
`/dispatch` skill exists precisely to launch these repeatedly. Worse, VERIFIED at
`internal/cli/dispatch_reachability_test.go:11-17`, `niwa watch` "invokes
dispatchLaunch directly, on a cron timer, with no terminal". A warning on that
path goes into a log nobody reads, on a schedule, forever. There is no
suppression mechanism available either: the one-time-notice machinery
(`docs/guides/one-time-notices.md`) records disclosures in `InstanceState`
(`DisclosedNotices`) and fires from `runPipeline` on create/apply — it is
per-workspace-instance and apply-scoped. Dispatched instances are ephemeral and
reaped, so keying a notice to one would make it fire roughly once per dispatch
anyway. The guide is also explicit that the mechanism is for messages that "have
no actionable remediation — they're informational, not warnings"
(`one-time-notices.md:29-37`), and that warnings reflecting current state "should
appear on every run". This advisory is a warning with a remediation, so by the
guide's own rule it does not qualify for suppression and would print every time.

### 5. Advisory, stronger, or nothing — the recommendation

**Recommendation: no runtime detection, no per-dispatch warning, and niwa never
writes the user's `~/.claude/settings.json`. Say it once, on the opt-in command,
and document the rest in a guide.**

Concretely, three pieces:

1. **A guide section**, in the new guide the dispatch-side grant ships with,
   headed with what does and does not work unattended after you turn the grant on.
   This is where the JSON, the blast-radius sentence, and the `/config` route (once
   verified) live.
2. **One closing line on the opt-in command**, if a `niwa config set` subcommand
   ships for the dispatch-side key. Text in Finding 6.
3. **Nothing on the dispatch path at all.**

Here is why each of the stronger options loses.

**Why not refuse the dispatch-side grant when the user-side gap exists.** Because
the dispatch-side grant is not half-broken — it is fully working for most of the
traffic. r1 established that injecting `accept` into every dispatched worker
covers worker-to-worker messaging *and* messages from the developer's terminal
into a worker, because in both cases the worker is the receiver. Only the
worker → your-own-terminal direction is left. Refusing to deliver a real,
self-contained improvement because a *different* direction needs a step niwa
cannot take would be niwa withholding what it can do to protest what it cannot.
It also fails the detection test above: to refuse conditionally, niwa would have
to know the user-side state, which Finding 4 says it cannot determine soundly.

**Why not a `niwa config`-style command that writes the user's Claude settings
with consent.** This is the option that looks best and is worst, and the Codex
trust writer is exactly why. niwa *does* write a developer's personal agent config
today — but look at the shape of that grant, documented at
`docs/guides/codex-agent.md:426-461`:

> niwa writes exactly one thing into `~/.codex/config.toml` (or `$CODEX_HOME`, if
> you've set it): one `[projects."<path>"]` block per cloned repository, carrying
> `trust_level`. That's the whole of it.

and the constraints it binds itself with, in that same section:

> **No global keys.** Only whole `[projects."<path>"]` blocks are ever appended.
> Nothing at the top level of your config is added, removed, reordered, or
> rewritten.

> **No edits to anything niwa didn't write.** ... When niwa retracts a trust entry,
> it goes by its own record of what it wrote, never by the entry's shape — Codex
> writes an identically-shaped block when you answer its own trust prompt, and a
> shape test would delete your answer.

Every property that makes the trust write defensible is absent here.
`crossSessionInbound` is a **top-level global key** — the one category the Codex
writer explicitly refuses. It is **path-scoped to nothing**: a trust entry vouches
for one directory niwa itself cloned, whereas `accept` applies to every session and
every sender. And it is **not retractable by record**: the trust writer can safely
remove its own entries because they are keyed by path and niwa remembers which
paths it wrote (`codex_trust.go:120-128`); a single scalar key has no such
identity, so niwa could never know whether the `accept` in the file was its own or
the developer's deliberate choice, and unsetting it would be exactly the "shape
test would delete your answer" failure that section warns against.

So the correct reading of the Codex-trust precedent is that it **rules this out**
rather than licensing it. niwa's own documented line is "no global keys", and
`crossSessionInbound` is a global key.

**Why not a per-dispatch advisory.** Finding 4. It cannot be made truthful without
being useless, it cannot be suppressed under the project's own one-time-notice
rules, and it would fire on cron. The brief's own framing is right: a warning
printed on every dispatch that most users cannot act on is worse than a line in a
guide.

**Why the opt-in command is the right single moment.** It is the one point where
the developer has deliberately expressed the intent this caveat is about, niwa is
already speaking to them on stdout, no detection is required so no false positive
is possible, and it fires exactly once per machine rather than once per dispatch.
The template already exists and already does this job for a different setting:
`internal/cli/config_default_harness.go:87-88` prints the resolved path it wrote
to, then a one-line reminder of what still outranks it. r1's machine-config lead
independently identified those two closing lines as part of the template worth
copying.

There is one honest caveat on this recommendation. If the dispatch-side grant
ships *without* a `niwa config set` subcommand — which is a live possibility, since
r1 found five of nine `[global]` keys are documented hand-edits with no command —
then there is no opt-in command to hang the line on, and the recommendation
degrades to guide-only. That is still the right answer; it is just quieter. Do not
add a dispatch-path warning to compensate.

### 6. Wording, against the house style

The house style, from the notices quoted in Finding 3: lowercase after the
`niwa dispatch: ` prefix; one sentence stating what is true, one stating the
consequence, one naming the fix; no imperative "you must"; a semicolon rather than
two sentences where the causal link matters. `apiKeyForcedWarning`
(`dispatch_remotecontrol.go:21`) is the cleanest specimen —

> remote-control on dispatch is enabled, but ANTHROPIC_API_KEY is set, which
> forces API-key auth; Claude Code Remote requires a claude.ai login, so the
> worker will start without remote-control

— and `renamedHarnessEnvNotice` (`agent.go:180`) is the closest to what I need,
because it is the one that ends by naming the exact edit:

> NIWA_AGENT is set to codex but is no longer read; it was renamed to
> NIWA_DISPATCH_HARNESS. This dispatch resolved its agent from the other sources.
> Set NIWA_DISPATCH_HARNESS=codex to get what you meant.

Proposed text, as the closing line of `niwa config set <key> true`, printed to
stdout after the "written to `<path>`" line, matching
`config_default_harness.go:87-88`:

```
Dispatched workers now accept peer messages without holding them for approval.
Your own interactive sessions still hold them: that is decided by your Claude Code
user settings, which niwa does not write. To accept there too, set "Messages from
your other sessions" to accept in Claude Code's /config -- it applies to every
Claude Code session you run, not only niwa's workers.
```

Four sentences, and each earns its place: what changed, what did not, why niwa
stopped there, and the fix with its price attached. The last clause is
non-negotiable — without it the line reads as "and here is the other half", which
is the presentation Finding 2 says the guidance must not adopt.

The remedy names the `/config` row rather than the JSON because it is the shorter
instruction and because it fails gracefully: a developer on a build older than
2.1.232, or under managed settings, finds no such row and goes to the guide, which
is the correct destination for both of those cases. Naming the file in a one-line
notice would send all three populations to hand-edit JSON, and one of them would
be editing a file whose value is already overridden.

## Implications

**Build:** a guide section, and one four-sentence closing line on the opt-in
command if that command ships. Nothing else.

**Where it lives:** the guide is the new `docs/guides/<name>.md` that r1's
machine-config lead already scoped for the dispatch-side setting — this becomes a
section in it rather than its own document, because a developer reads it at the
moment they are turning the grant on. It follows the shape of
`docs/guides/remote-control-on-dispatch.md`, which has the closest structure: an
"Enable it (host level)" section with the TOML, then an **"Eligibility"** section
(lines 52-67) that says plainly what niwa's setting does *not* grant and which
gaps are surfaced by Claude Code rather than by niwa. That section is the model.
The user-settings half is this feature's eligibility gap.

The guide section should also carry the `codex-agent.md:426` move: a short "what
niwa writes and what it won't" note stating that niwa grants this per-dispatch via
`--settings` and does not touch `~/.claude/settings.json`. Saying what niwa
declines to do, and why, is established practice here and it pre-empts the obvious
"why doesn't it just set it for me" question.

**Do not build:** any read of `~/.claude/settings.json`, any per-dispatch notice,
any conditional refusal of the dispatch-side grant, and any writer for the user's
Claude settings. The first cannot be made sound, the second cannot be made quiet,
the third withholds working functionality, and the fourth crosses a line
`docs/guides/codex-agent.md:445-447` has already drawn in writing.

**Add a row** to the Contributor Guides list in `CLAUDE.md:36-47` when the guide
lands, matching the one-line-summary style of the existing entries.

## Surprises

1. **The strongest-looking precedent argues the opposite way.** The Codex trust
   writer proves niwa is willing to edit a developer's personal agent config, and
   I expected it to license a `niwa config`-writes-your-settings command. Reading
   its documented constraints (`codex-agent.md:441-455`) reverses that: "No global
   keys. Only whole `[projects."<path>"]` blocks are ever appended. Nothing at the
   top level of your config is added, removed, reordered, or rewritten."
   `crossSessionInbound` is a top-level global key. The precedent forbids it.

2. **niwa's own house style already contains the blast-radius sentence.**
   `dispatch.go:1014-1017` reasons about offering `--harness` before
   `NIWA_DISPATCH_HARNESS` on precisely the "one command versus every command in
   the shell" grounds. The argument for scoping this grant did not need inventing;
   it needed one level of generalization.

3. **`ReadDeclaredMCPNames` already answers the unreadable-managed-settings
   question.** The house posture for a config niwa cannot read is to report *that
   it could not check*, never to assert a finding (`payload.go:153-157`). Applying
   that honestly to this case produces a sentence so weak it makes the case for
   printing nothing.

4. **The one-time-notice mechanism explicitly excludes this kind of message.**
   `one-time-notices.md:29-37` restricts suppression to messages with "no
   actionable remediation" and says warnings about current state "should appear on
   every run". The obvious "print it once" escape hatch is closed by the project's
   own rule, which strengthens rather than weakens the case for not printing on
   dispatch at all.

5. **`niwa watch` reaches the dispatch launch path on a cron timer with no
   terminal** (`dispatch_reachability_test.go:11-17`). Any per-dispatch advisory
   inherits a scheduled, unattended emission path. The repo has a test whose whole
   purpose is guarding that seam against a blocking prompt; a noisy warning is the
   non-blocking version of the same mistake.

6. **Claude Code hides the `/config` row when a user-settings value would not
   apply**, and no CLI anywhere exposes the effective value of a settings key.
   Those two facts together say the product has decided this question is answerable
   only from inside a session, and has built the affordance there. An external tool
   duplicating the judgment would be guessing at something the owner of the data
   declines to expose.

7. **There is no `niwa doctor` and `niwa status` is instance-scoped.** I expected
   to find a natural diagnostic home and recommend putting the check there behind
   an opt-in flag. There isn't one, and inventing a machine-scope diagnostic
   command to house a single unsound check is not proportionate.

## Open Questions

1. ~~Does `/config` have the control?~~ **RESOLVED, verified in Finding 1.** It is
   the row "Messages from your other sessions", it writes user settings, it needs
   Claude Code 2.1.232+, and it hides itself when managed settings or `--settings`
   already set the key. One thing still worth a live check before the guide ships:
   the exact labels the row offers for its three values, since the docs name the
   underlying values (`accept` / `hold` / `refuse`) but not necessarily the words
   shown in the UI.

2. ~~Is there a way to read effective settings from outside?~~ **RESOLVED: no.**
   Verified absent from the settings reference, the CLI reference, and the
   debug-your-config page. This closes the last route to sound detection and makes
   the "do not build it" recommendation firmer than it was when I drafted it.

3. **Does the dispatch-side key ship with a `niwa config set` subcommand?** The
   whole recommendation's single line of output hangs on it. If not, it is
   guide-only, and someone will be tempted to add a dispatch warning to fill the
   silence. Decide this deliberately rather than by default.

4. **Is `permissions.defaultMode: plan` in the user's settings load-bearing for
   them?** r1 flagged that `plan` lands in the *bypassing* class when bypass
   permissions are available in that session, which would remove the mismatch
   entirely without touching `crossSessionInbound`. Unverified for this user's
   actual sessions. If it turns out their terminal already resolves to the
   bypassing class, the second half of the problem may not exist as described, and
   this whole advisory would be documenting a non-issue. Cheap to check with
   `/status` in a real terminal; worth doing before writing the guide.

5. **Should the guide recommend `accept` at all, or recommend living with the
   dialog?** I have written this as "here is the remedy and its price". An equally
   defensible guide says the dialog in your own terminal is a feature — you are
   the one session that should read what your workers send before it lands — and
   only documents the setting for people who have decided otherwise. That is a
   product call, not a research finding.

## Summary

The remedy is the `/config` row "Messages from your other sessions", or the same
thing by hand as the top-level key `"crossSessionInbound": "accept"` in the user's
own `~/.claude/settings.json` — it cannot live in any file niwa materializes,
because that key's precedence is inverted and a project-scope `accept` is silently
ignored — and it must be presented with its price attached, since it applies to
every Claude Code session the developer ever runs and to every sender the
messaging fabric can reach, not just to niwa's workers. niwa should not detect it:
there is no `niwa doctor`, `niwa status` is instance-scoped, no Claude Code command
exposes the effective value of a settings key so niwa would have to reimplement an
inverted precedence ladder against managed settings it may not be able to read, a
false "you need to fix this" would push a developer to loosen a security-relevant
setting machine-wide on niwa's authority, and the dispatch path it would print on
runs in fan-out loops and on `niwa watch`'s cron timer. Say it once instead, as a
four-sentence closing line on the command that turns the dispatch-side grant on —
the shape `config_default_harness.go:87-88` already uses — and put the JSON and the
blast radius in an "Eligibility"-style guide section modeled on
`remote-control-on-dispatch.md:52-67`; notably, niwa's own documented rule for the
one personal config it does edit is "no global keys, only whole project blocks"
(`codex-agent.md:445-447`), which forbids a `niwa config` command that would write
this key for the user.
