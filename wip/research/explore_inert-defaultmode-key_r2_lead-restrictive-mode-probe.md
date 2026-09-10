# Lead: Is project-scope defaultMode honored for restrictive values?

Measured against **Claude Code 2.1.267** (`claude --version` -> `2.1.267 (Claude Code)`,
commit `a9e1808c8204`, native install, linux-x64). Every result below is an
observation, not an inference.

## Method

Two things made this measurable without guessing.

**A direct readout.** The headless `stream-json` init event carries a
`permissionMode` field naming the mode the session actually started in:

```
claude -p 'say OK' --output-format stream-json --verbose \
  --setting-sources project --strict-mcp-config --tools Read < /dev/null
```

```json
{"type":"system","subtype":"init","cwd":"...","permissionMode":"default", ...}
```

**A scope isolator.** `--setting-sources <user,project,local>` selects which
settings files load, so a project-scope value can be tested alone or layered
against a user-scope value with no ambiguity about where it came from.

Behavioral corroboration used `--permission-prompts none` ("anything that would
prompt is denied automatically; the permission mode still decides everything
else") plus a Write task. The `result` event's `permission_denials` array and
the file's existence on disk say whether the mode was enforced or merely
labelled.

All probes used `--strict-mcp-config --tools Read` (or `Write,Read`) and
`< /dev/null` to keep runs cheap and avoid the stdin-wait warning.

## Findings

### 1. Project scope alone: which values survive

Fresh throwaway directory per value, `.claude/settings.json` containing only
`{"permissions":{"defaultMode":"<value>"}}`, user settings not loaded.

```
cd <probe-dir>
claude -p 'say OK' --output-format stream-json --verbose \
  --setting-sources project --permission-prompts none \
  --strict-mcp-config --tools Read < /dev/null | head -1
```

| `defaultMode` written | reported `permissionMode` | verdict |
|---|---|---|
| *(no settings file)* | `default` | baseline |
| `acceptEdits` | `acceptEdits` | **honored** |
| `plan` | `plan` | **honored** |
| `default` | `default` | ambiguous here, resolved in §2 |
| `dontAsk` | `dontAsk` | **honored** |
| `manual` | `default` | alias, see §5 |
| `bypassPermissions` | `default` | **ignored** |
| `auto` | `default` | **ignored** |

The round-1 inference holds for `acceptEdits` and `plan`: both take effect from
project scope. Round 1's finding on `bypassPermissions` and `auto` reproduces
exactly.

### 2. The hard case: `"default"` is honored, not merely a fallback

Two independent discriminating probes, both decisive.

**Probe 2a — pull down a user-scope `plan`.** The real user settings file at
`~/.claude/settings.json` already carries `"defaultMode": "plan"` (untouched, read
only). `plan` is not the fallback, so if project-scope `"default"` is inert the
merged value must stay `plan`.

```
claude -p 'say OK' --output-format stream-json --verbose \
  --setting-sources user,project --strict-mcp-config --tools Read < /dev/null
```

| user scope | project scope | reported `permissionMode` |
|---|---|---|
| `plan` | *(no file)* | `plan` |
| `plan` | `default` | **`default`** |
| `plan` | `acceptEdits` | `acceptEdits` |
| `plan` | `bypassPermissions` | `default` |
| `plan` | `auto` | `default` |

Row 2 is the result. Project-scope `"default"` overrode a user-scope `plan`. The
file was read, the value was applied, and it won the merge.

**Probe 2b — pull down a user-scope `acceptEdits`, with behavior.** Isolated
`HOME` at `/tmp/dmprobe/fakehome`, its `.claude/settings.json` set to
`acceptEdits`, `.credentials.json` copied in for auth. The task was to Write a
file, so enforcement is observable and not just reported.

```
env HOME=/tmp/dmprobe/fakehome claude \
  -p 'Use the Write tool to create a file named probe.txt in the current directory containing exactly the word HELLO. Do nothing else.' \
  --output-format stream-json --verbose \
  --setting-sources user,project --permission-prompts none \
  --strict-mcp-config --tools Write,Read < /dev/null
```

| user scope | project scope | init mode | `probe.txt` written | `permission_denials` |
|---|---|---|---|---|
| `acceptEdits` | *(no file)* | `acceptEdits` | **true** | `[]` |
| `acceptEdits` | `default` | `default` | **false** | `['Write']` |
| `acceptEdits` | `bypassPermissions` | `default` | false | `['Write']` |

Row 2 is the lead's exact proposed probe and it lands the way "project scope is
honored for restrictive values" predicts: a project-scope `"default"` pulled a
permissive user-scope `acceptEdits` back down to prompting, and the write was
really denied.

**Isolation worked.** The proof is in the data rather than in an assertion: under
the fake `HOME` the baseline reported `acceptEdits`, whereas the real `HOME`
reports `plan` for the same command. The real user settings did not leak. One
caveat — the reported model stayed `claude-opus-5[1m]`, matching the real user
settings' `"model": "opus[1m]"`; I did not chase whether that is a leak through
some other channel or simply the account default, because it is orthogonal to the
permission key.

### 3. The reported mode is real enforcement, not a label

`permissionMode` in the init event could in principle be a declared intent that
nothing enforces. It is not. Neutral base directory (`/tmp/dmprobe/...`),
project scope only, same Write task:

| project `defaultMode` | init mode | `probe.txt` written | `permission_denials` |
|---|---|---|---|
| `acceptEdits` | `acceptEdits` | **true** | `[]` |
| `default` | `default` | false | `['Write']` |
| `plan` | `plan` | false | `[]` |

`acceptEdits` and `default` diverge in observable behavior from the same prompt,
which is what makes the whole readout trustworthy. `plan` is distinguishable from
`default` too, and by a different signature: the write did not happen but nothing
was *denied* — the model was held at the planning layer instead. Its result text
confirms it:

> Plan mode is active, so I haven't created the file yet. ... `ExitPlanMode` is
> disabled in this session, so I can't signal plan approval through the normal
> mechanism.

### 4. `settings.local.json` behaves identically to `settings.json`

```
claude ... --setting-sources local ...
```

| `.claude/settings.local.json` `defaultMode` | init mode | written |
|---|---|---|
| `acceptEdits` | `acceptEdits` | true |
| `bypassPermissions` | `default` | false |

Same split, same direction. Nothing about the local scope changes the picture.

### 5. The restriction is scope-specific, and `manual` is an alias

The CLI flag is a different channel and is not subject to the restriction:

| invocation | reported `permissionMode` |
|---|---|
| `--permission-mode bypassPermissions` (no settings file) | `bypassPermissions` |
| `--permission-mode manual` (no settings file) | `default` |
| project `default` + `--permission-mode bypassPermissions` | `bypassPermissions` |

Two things follow. `bypassPermissions` is rejected *from settings files*, not
globally — the CLI flag still works, which is precisely why niwa's dispatch
derivation exists. And `manual` reporting `default` from project scope is not a
rejection: the CLI flag spelled `manual` also reports `default`, so `manual` is
simply an accepted alias for `default`. It does not belong on the ignored list.

Row 3 also fixes the precedence: the CLI flag beats project settings. A
project-scope `"default"` cannot defend itself against a forwarded
`--permission-mode bypassPermissions`.

### 6. `claude doctor` reports nothing relevant

Confirmed as the lead suspected. Full output covers install method, version,
platform, auto-update channel, managed-settings fetch status and organization
policy. There is no settings-layer resolution, no effective permission mode, no
mention of `defaultMode`. The init event of a headless run is the only diagnostic
that names the resolved mode.

## Implications

**`niwa watch`'s operator-approval posture is sound.** `ApplyReviewSettings` in
`internal/watch/containment.go` writes `permissions.defaultMode = "default"` into
the instance-root `.claude/settings.json` when `sandbox && ask`, so that a
PreToolUse hook's `ask` decision is honored rather than silently allowed. That
value is honored from project scope — measured, twice, including behaviorally.
`VerifyReviewSettings`'s hard check on `permissions.defaultMode == "default"` is
verifying something that actually governs the session. This is **not** a second
instance of the bug.

The posture is also internally consistent: `internal/cli/dispatch.go:551-557`
derives `--permission-mode bypassPermissions` onto the worker command line only
when the instance's `DefaultMode` reads `bypassPermissions`. In the ask posture
the value is `"default"`, so nothing is forwarded, and §5 row 3 confirms that a
forwarded flag would otherwise have overridden the file. The two mechanisms do
not collide.

**The exploration's framing narrows rather than widens.** The dead set is exactly
`{bypassPermissions, auto}` from project and local scope. `default`,
`acceptEdits`, `plan` and `dontAsk` all work, and `manual` is an alias for
`default` rather than a casualty. So the fix is "stop writing the values that
cannot work," not "stop writing the key" — the key has a legitimate resident in
`niwa watch`, and removing it wholesale would break the ask posture that
currently functions.

**One thing gets worse, not better, than the round-1 framing.** "Silently
ignored" undersells what happens to `bypassPermissions` and `auto`. They are not
skipped over in favor of the next layer down — they win the scope merge and are
*then* downgraded to `default`. §2a rows 4-5 and §2b row 3 show a project-scope
`bypassPermissions` landing on `default` when the user scope said `plan` or
`acceptEdits`. Writing `bypassPermissions` into an instance's settings therefore
leaves the session **more restrictive than writing nothing at all**, by
destroying whatever the user's own settings would have contributed. For niwa's
non-watch instances, which get `defaultMode: "bypassPermissions"` from the
materializer, this means the file is not merely inert — it actively suppresses a
developer's personal `acceptEdits` default. That is a real behavior change, and a
user-visible one, worth naming in the writeup.

## Surprises

`~/.claude/` is a protected path and `acceptEdits` does not cover it. The first
behavioral probe ran in the assigned scratch space under
`/home/dangazineu/.claude/jobs/.../tmp/`, and the Write was denied *even with
`permissionMode: acceptEdits` correctly reported*. The denial message was the
generic "requires permission approval." Rerunning the identical probe from
`/tmp/dmprobe/` succeeded. Anything writing under the Claude config directory
gets an extra guard that the permission mode does not lift. This nearly produced
a false "acceptEdits is not enforced either" result, and it is a trap for anyone
running permission experiments in a scratch directory that happens to live under
`~/.claude/`.

`dontAsk` is honored from project scope. It reads as a permissive value and sits
next to `auto` in the `--permission-mode` choice list, so I expected it to share
`auto`'s fate. It does not. Whatever criterion Anthropic applied to pick the
blocked set, "is this permissive" is not it.

The clobbering behavior in §2a. I set up that matrix expecting the ignored values
to fall through to the user-scope setting, which would have made
`bypassPermissions` harmless. Getting `default` instead of `plan` was the
opposite of the prediction and is the most consequential incidental finding here.

## Open Questions

**Whether the enterprise/managed settings scope behaves like project scope.**
`claude doctor` reports "Managed settings (remote): not fetched — requires an
Enterprise or Team subscription," and this account is Pro/Max. I could not test
whether a managed settings file honors `bypassPermissions`. Not load-bearing for
niwa, which writes instance-local files.

**Why `bypassPermissions` downgrades to `default` rather than falling through.**
I measured the behavior but not the mechanism; distinguishing "merged then
sanitized" from "sanitized to an explicit `default` at parse time" would need
either debug output that narrates settings resolution (I found none) or reading
the bundled source. The observable outcome is identical either way.

**The exact version boundary.** I measured 2.1.267 only. Round 1's claim that the
restriction landed in 2.1.257 for `bypassPermissions` and 2.1.142 for `auto`
comes from release notes, not from measurement — I did not install older versions
to bisect. The niwa comment at `dispatch.go` says "pre-2.1.258 behavior," which
is consistent with round 1 but likewise untested here.

**Interactive sessions.** All probes were headless (`-p`). An interactive session
reads the same settings files through the same resolver and there is no reason to
expect divergence, but I did not drive a TTY session to confirm it, so this is
strictly a headless measurement.

## Summary

Measured against Claude Code 2.1.267: project-scope `permissions.defaultMode` is
honored for `default`, `acceptEdits`, `plan` and `dontAsk`, and ignored only for
`bypassPermissions` and `auto` — confirmed both by the `permissionMode` field in
the headless init event and behaviorally, including the decisive case where a
project-scope `"default"` pulled a user-scope `acceptEdits` back down to
prompting and the write was actually denied. `niwa watch`'s operator-approval
posture is therefore legitimate: the `"default"` it writes really governs the
session, `VerifyReviewSettings` guards something live, and the exploration's fix
narrows to "stop writing the values that cannot work" rather than "stop writing
the key." The one thing that got worse: the ignored values do not fall through to
the user's setting but win the merge and are then downgraded to `default`, so
writing `bypassPermissions` into an instance leaves the session more restrictive
than writing nothing at all.
