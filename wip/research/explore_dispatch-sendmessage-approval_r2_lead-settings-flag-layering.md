# Lead: Does --settings layer or replace, and what does a repeated --settings do?

## Findings

Both questions were settled by direct measurement on this machine against Claude Code
2.1.267, not by reading documentation. Documentation is cited only as corroboration.

### Method

A throwaway project directory was created outside any repository, with a project-scope
settings file carrying four distinguishable contributions: an `env` block with two
variables, a `permissions.allow` entry, a `permissions.deny` entry, and a `SessionStart`
hook pointing at a small script. The script appends a label plus every `NIWA_*` variable
it can see to a log file. Because Claude Code applies a settings `env` block to its own
process environment, and hooks inherit that environment, the hook log is a direct readout
of the merged, effective settings document as the running session sees it. No cooperation
from the model is required, and no tool permissions are involved.

Each run was a short non-interactive print-mode session executed in that directory,
varying only the `--settings` flag. The log was truncated before every run.

One confound is worth stating up front: the scratch directory was never trusted, so the
project file's `permissions.allow` entry was refused with a startup warning about the
workspace not having been trusted. That warning turned out to be useful rather than
merely obstructive; see the permissions result below. Notably the project file's `hooks`
and `env` blocks were applied anyway in the untrusted directory. Only `permissions.allow`
was withheld.

### Result 1: `--settings` LAYERS. It does not replace. (VERIFIED)

Baseline, no `--settings` flag: the project hook fired and both project env variables
were present.

    HOOK_FIRED=projhook
      [projhook] NIWA_BOTH=from-project
      [projhook] NIWA_PROJ_ONLY=projvalue

With a flag supplying an env block of its own, setting one new variable and one that
collides with the project file:

    HOOK_FIRED=projhook
      [projhook] NIWA_BOTH=from-cli
      [projhook] NIWA_CLI_ONLY=clivalue
      [projhook] NIWA_PROJ_ONLY=projvalue

Three separate things are visible in that one result.

Top-level layering. The project's `hooks` key was untouched by a command-line document
that did not mention hooks, and `projhook` still fired. Keys omitted from the
command-line document keep their file-based values, exactly as documented.

The merge is DEEP, not shallow. `NIWA_PROJ_ONLY` survived even though the command-line
document supplied its own `env` object. A shallow merge would have replaced the whole
`env` object and that variable would have vanished. It did not. Nested objects are merged
key by key.

Conflicts resolve to the command line. `NIWA_BOTH` came out as `from-cli`, matching the
documented precedence where command-line arguments sit above project settings.

This answers the question the exploration flagged as the larger blast radius: "keys you
omit" does not mean top-level keys only. It reaches into nested objects.

### Result 2: nested `hooks` arrays UNION across scopes (VERIFIED)

With a command-line document defining its own SessionStart hook, both hooks ran:

    HOOK_FIRED=projhook
    HOOK_FIRED=clihook

So `hooks` behaves like the documented list-merge keys rather than like an override. A
command-line hook is additive to the project's hooks, not a replacement for them.

### Result 3: nested `permissions` also deep-merges (VERIFIED, indirect)

Running with a command-line document whose `permissions.allow` held a WebFetch rule still
produced the startup warning about the project file's own `permissions.allow` entry. The
loader was still reading and evaluating the project scope's allow list while the command
line supplied its own. Had `permissions` been replaced wholesale by the command-line
document, the project entry would never have been reached and the warning would not have
been emitted. Combined with the env result, deep merge is the general rule for nested
objects, not a special case for one key.

### Result 4: repeated `--settings` is LAST-WINS, and silently so (VERIFIED)

Two flags on one command line, each carrying a distinct env variable, a colliding
variable, and a distinct SessionStart hook. The first document set `NIWA_S1` and a hook
labelled `s1hook`; the second set `NIWA_S2` and a hook labelled `s2hook`. Result:

    HOOK_FIRED=projhook
    HOOK_FIRED=s2hook
      [projhook] NIWA_BOTHCLI=two
      [projhook] NIWA_BOTH=from-project
      [projhook] NIWA_PROJ_ONLY=projvalue
      [projhook] NIWA_S2=two

The first document vanished completely. No `NIWA_S1`, no `s1hook`. The project file's own
contributions were untouched, so it is specifically the earlier command-line document
that is discarded, not the whole lower stack.

Critically, no warning of any kind was printed. Full stderr for that run contained only
the unrelated trust warning. Anyone who added a second flag would get a working session
that had silently lost the first document.

The same last-wins behavior holds when mixing the two accepted forms of the flag. With a
file path first and an inline JSON string second, the file's variables were gone and only
the inline document's survived. Both forms feed the same single slot.

This confirms the guess recorded in niwa's own design notes, that the behavior is
undocumented and likely last-wins, and upgrades it from assumption to measurement. The
mechanism is consistent with a non-accumulating option parser: the flag takes one value,
and a later occurrence overwrites the earlier one before Claude Code ever sees it.

### Result 5: malformed JSON fails loudly (VERIFIED)

A deliberately broken inline document produced `Error: Invalid JSON provided to
--settings` and the session refused to start. A settings-document builder that emits
broken JSON will fail fast rather than silently degrade, which is a useful property for
niwa, since it would be assembling the document programmatically.

### What the documentation says (corroboration)

The CLI reference describes the flag as: "Path to a settings JSON file or an inline JSON
string. Values you set here override the same keys in your settings.json files for this
session. Keys you omit keep their file-based values. The file must be a regular file no
larger than 2 MiB."
https://code.claude.com/docs/en/cli-reference

The settings page is more explicit that the merge is the ordinary one: "Claude Code
merges JSON you pass with --settings <file-or-json> with your settings files by the same
rules as the other levels: it takes a key you set here over the same key in local,
project, or user settings, and keeps the lower-level value for a key you omit." It also
documents that lists merge: "When you set the same list key, such as permissions.allow,
in more than one file, Claude Code combines the lists instead of picking one, so each
file can add entries without removing another file's."
https://code.claude.com/docs/en/settings

Neither page says anything at all about passing the flag twice. That gap is real, and
Result 4 fills it empirically.

Documented exceptions to list-merging that a settings-document builder should know about,
since they take a whole value from the highest-precedence source rather than merging:
`fallbackModel`, `modelPicker` (which explicitly ranks the command line above project and
local scope and ignores those scopes entirely), `availableModels` under managed settings,
and `modelSettings`. None of these are keys niwa currently injects.

## Implications

niwa must build one merged settings document. This is not optional. The refactor that
niwa's design docs twice declined to do is now the only correct implementation. Adding a
second flag alongside the existing Remote Control one would silently discard the Remote
Control bridge configuration. The worker would start, print nothing unusual, and simply
not have the bridge. That is the worst failure shape available: no error, no warning, and
a capability quietly missing. The refactor itself is small, since it amounts to
collecting contributions into one object, marshalling it once, and passing a single flag.

The feared blast radius does not exist. Injecting a small document does not strip the
instance's materialized project settings file. Enabled plugins, marketplaces, env vars,
the SessionStart and PreToolUse hooks, and permissions all survive a command-line
document that does not mention them, and the merge reaches into nested objects, so even a
document that does mention `env` or `permissions` only overrides the specific leaf keys it
names. A document carrying a single top-level scalar touches nothing else in the stack.
Nothing breaks.

Two design consequences follow for the merged builder. First, the builder should own the
flag exclusively, so that one place in the code emits it and everything else registers a
contribution into it. That way a future third contributor cannot reintroduce the bug by
adding another flag. Second, because two contributors could in principle set the same
key, the builder should decide conflicts explicitly rather than relying on Go map
iteration order, and it is worth failing loudly on a duplicate key rather than picking a
winner silently. Claude Code's own silence on the repeated flag is precisely the behavior
that made this a gate in the first place.

Ordering within the document does not matter, only that there is one of them. And since
the merge with the project file is deep and additive, the injected document does not need
to restate anything the materializer already writes.

## Surprises

The deep merge is more generous than the documentation implies. The settings page calls
an `env` block "an ordinary key" that follows the levels, which reads as though a higher
scope's `env` would replace a lower scope's outright. It does not. Individual variables
from the project file survive a command-line `env` block that names different variables.
The same held for `permissions`. Anyone reasoning from the docs alone would have
over-estimated the destructiveness of an injected document.

Hooks union across scopes even though the documentation's list-merge paragraph only names
`permissions.allow` as its example. A command-line hook adds to the project's hooks rather
than replacing them. That is convenient here, but it also means a future niwa contributor
cannot use the flag to suppress an instance hook.

The untrusted-workspace gate is narrower than expected. In an untrusted directory, the
project file's `permissions.allow` was refused with a visible warning, but its `hooks` and
`env` blocks were applied without comment. Worth knowing for niwa, whose instances rely on
materialized hooks, though niwa's instances are presumably trusted in practice.

Unknown settings keys are accepted silently. Both a `crossSessionInbound` document and a
deliberately bogus key started cleanly with no warning. So a clean startup is not evidence
that an injected key was recognized. Any verification that `crossSessionInbound` actually
took effect has to observe the resulting behavior, not the absence of a complaint.

## Open Questions

Whether `crossSessionInbound: "accept"` delivered through the flag genuinely lifts the
inbound hold end to end was not tested here. It needs a real cross-session send against a
live receiver, which is outside this lead's scope. Result 4 makes the delivery mechanism
safe; it does not prove the setting's effect.

Whether managed settings could override a niwa-injected `crossSessionInbound` on an
enterprise-managed machine. Managed settings outrank the command line for ordinary keys,
and round 1 noted this key has its own inverted order, so the interaction deserves its own
check before niwa relies on the flag in managed environments.

Whether the deep merge has a depth limit, or whether objects nested more than two levels
down behave differently. Everything measured here was one or two levels deep, which covers
every key niwa currently writes.

## Summary

Measured on Claude Code 2.1.267, `--settings` layers onto the project settings file rather
than replacing it, and the merge is deep: a command-line `env` or `permissions` object
overrides only the leaf keys it names, project hooks still fire, and command-line hooks
union with them rather than replacing them, so an injected `crossSessionInbound` document
would break nothing in niwa's materialized instance settings. A repeated `--settings` on
the same command line is last-wins and completely silent, with the first document's env
var and hook both vanishing and no warning on stderr, and the same happening when mixing
the file-path and inline forms. niwa therefore must refactor to build a single merged
settings document rather than add a second flag, because a second flag would silently drop
the Remote Control bridge configuration it already passes through that slot.
