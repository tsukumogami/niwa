# Lead: Can niwa pre-write a trusted_hash Codex accepts, and must Codex hooks be plugin-delivered?

All experiments ran against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex`,
ELF static-pie, stripped) with an isolated `CODEX_HOME` under
`/home/dgazineu/.claude/jobs/7838923c/tmp/`. The host `~/.codex` was read only; the auth file was
copied out, never written back. Every "fired" result below is a real `codex exec` session that made a
live model round-trip and returned exit 0.

**Headline: yes, and no.** niwa can pre-write a `trusted_hash` that Codex accepts with no prompt —
verified by a marker file written from a `SessionStart` hook in a real session. But hooks must *not*
be plugin-delivered: plugin-supplied `hooks.json` is dead code in 0.147.0. The working delivery is a
loose `$CODEX_HOME/hooks.json`, which is the exact mechanism the earlier probe concluded did not work.

## Findings

### 1. Where the `trusted_hash` comes from

The binary is stripped, but it retains Rust source paths from panic locations
(`hooks/src/engine/discovery.rs`, `config/src/hook_config.rs`, `app-server/src/effective_plugin_change.rs`),
and the crate layout matches the public `openai/codex` repository. Tag `rust-v0.147.0` exists upstream,
so I read the real source rather than reasoning from disassembly. Everything below was then
**independently reimplemented in Python and checked against hashes the shipped binary actually wrote**.

Two strings in the binary anchor the config write path:

```
$ strings -n 6 .../bin/codex | grep -n 'hooks.state\|trusted_hash'
510366:hooks.state."
510367:".trusted_hash
510369:failed to write hook trust:
```

(A nearby `: expected sha256:` / `, got sha256:` pair is a **false lead** — it belongs to
`linux-sandbox/src/bundled_bwrap.rs`, the bubblewrap digest check, not to hooks.)

The computation is `hook_hash` in `codex-rs/hooks/src/engine/discovery.rs:763`, which builds a
normalized identity and delegates to `version_for_toml` in `codex-rs/config/src/fingerprint.rs`:

```rust
// discovery.rs
#[derive(Serialize)]
struct NormalizedHookIdentity {
    event_name: &'static str,
    #[serde(flatten)]
    group: MatcherGroup,
}

fn hook_hash(event_name, matcher, group, normalized_handler) -> String {
    let mut group = group.clone();
    group.matcher = matcher.map(ToOwned::to_owned);
    group.hooks = vec![normalized_handler];
    let identity = NormalizedHookIdentity {
        event_name: crate::hook_event_key_label(event_name),
        group,
    };
    let Ok(value) = TomlValue::try_from(identity) else { unreachable!(...) };
    version_for_toml(&value)
}

// fingerprint.rs
pub fn version_for_toml(value: &TomlValue) -> String {
    let json = serde_json::to_value(value).unwrap_or(JsonValue::Null);
    let canonical = canonical_json(&json);          // recursively sorts object keys
    let serialized = serde_json::to_vec(&canonical).unwrap_or_default();
    let mut hasher = Sha256::new();
    hasher.update(serialized);
    format!("sha256:{hex}")
}
```

So the algorithm is, precisely:

1. Build the identity object: `event_name` (snake_case label) plus the *flattened* matcher group,
   where the group's `hooks` array holds exactly the one **normalized** handler.
2. Serialize through TOML — which **drops `None` fields entirely**. This is why hashing the raw JSON
   never matches, and it is what the comment at `hook_config.rs:189` ("cannot be represented in TOML
   for trust hashing") is guarding.
3. Convert to JSON, sort every object's keys recursively, serialize compactly (no whitespace).
4. `sha256` the UTF-8 bytes, hex-encode, prefix `sha256:`.

The normalization applied before hashing (`discovery.rs:520-576`, `732-752`) matters:

- `timeout` is **always materialized**: `timeout.unwrap_or(600).max(1)` for every event except
  `SessionEnd`, which is `timeout.unwrap_or(1).clamp(1, 3)` (`SESSION_END_DEFAULT_TIMEOUT_SEC = 1`,
  `SESSION_END_MAX_TIMEOUT_SEC = 3`).
- `command_windows` is **forced to `None`** and therefore always absent from the hash.
- `async` is a plain `bool`, so it is always present (`false` when unset).
- `additionalContextLimit` is dropped unless the event is one of PreToolUse / PostToolUse /
  SessionStart / UserPromptSubmit / SubagentStart, and is also dropped when it equals the default 2500.
- `matcher` is forced to `None` for `UserPromptSubmit` and `Stop` (`matcher_pattern_for_event`);
  for all other events it passes through, and an empty string `""` is preserved as an empty string.
- The command is hashed **before** `${VAR}` environment substitution, so the raw `hooks.json` text is
  what counts.

The state key format is `hook_key` in `codex-rs/hooks/src/lib.rs:109`:

```rust
format!("{key_source}:{}:{group_index}:{handler_index}", hook_event_key_label(event_name))
```

with labels `pre_tool_use`, `permission_request`, `post_tool_use`, `pre_compact`, `post_compact`,
`session_start`, `session_end`, `user_prompt_submit`, `subagent_start`, `subagent_stop`, `stop`.

**`key_source` is the critical detail, and it is not one thing:**

| Delivery | `key_source` | Source |
|---|---|---|
| Plugin `hooks.json` | `<plugin>@<marketplace>:hooks.json` | `plugin_hook_key_source()`, `declarations.rs:35` |
| Loose `hooks.json` | **the absolute path of the `hooks.json` file** | `discovery.rs:170`, `source_path.display().to_string()` |
| Inline `config.toml` | **the absolute path of the `config.toml` file** | `discovery.rs:129`, `config_toml_source_path(layer)` |

**Verification.** I reimplemented the whole thing in Python
(`/home/dgazineu/.claude/jobs/7838923c/tmp/codexhash.py`) and ran it against the one populated
real-world example on the host — the installed `trajectory` plugin's `hooks.json` (13 handlers, commands
~2.4 KB each) versus the 13 `trusted_hash` values in `~/.codex/config.toml` that the shipped binary
itself wrote:

```
$ python3 codexhash.py
MATCH trajectory@trajectory:hooks.json:permission_request:0:0
MATCH trajectory@trajectory:hooks.json:post_compact:0:0
... (all 13) ...
MATCH trajectory@trajectory:hooks.json:user_prompt_submit:0:0

13 matched, 0 missed
```

13/13 exact, including the `SessionEnd` entry that exercises the 1/3-second clamp. The algorithm is
reproduced exactly, from a Go program's point of view: JSON with sorted keys, compact separators,
sha256, hex.

### 2. Can a hook fire from a pre-written config? — **Yes. Verified.**

Isolated `CODEX_HOME` with a loose `hooks.json` and a hand-written `[hooks.state]` block, no
interaction of any kind:

```toml
[hooks.state."/home/dgazineu/.claude/jobs/7838923c/tmp/ch2/hooks.json:session_start:0:0"]
enabled = true
trusted_hash = "sha256:15b31a93beaec932ad6d9613cce7a9e8b67116c239f8d59c0c0e31a360406044"

[hooks.state."/home/dgazineu/.claude/jobs/7838923c/tmp/ch2/hooks.json:user_prompt_submit:0:0"]
enabled = true
trusted_hash = "sha256:b57f07e5c4516eb2119290c69967c31aadee1e6c1fcfbcb106d97d6d24fedf0a"
```

```
$ CODEX_HOME=.../ch2 codex exec --skip-git-repo-check --sandbox danger-full-access \
    "Reply with exactly: HI" </dev/null
...
hook: SessionStart
hook: SessionStart Completed
hook: UserPromptSubmit
hook: UserPromptSubmit Completed
codex
HI
EXIT=0

$ ls .../work2/
marker_ss.txt  marker_ups.txt
```

Both marker files were written by the hook commands. This is a real session with a real model call —
not a dry run. `codex exec` is a full non-interactive session and it runs hooks; there was no prompt,
no TTY requirement, and no manual trust step anywhere.

The same recipe works with hooks declared **inline in `config.toml`**, keyed by the config file's own
absolute path:

```toml
[[hooks.SessionStart]]

[[hooks.SessionStart.hooks]]
type = "command"
command = "echo FIRED_ch_toml >> /home/.../out_ch_toml.txt"
timeout = 10

[hooks.state.".../ch_toml/config.toml:session_start:0:0"]
enabled = true
trusted_hash = "sha256:..."
```

```
########## ch_toml
exit=0
hook: SessionStart
hook: SessionStart Completed
MARKER: /home/dgazineu/.claude/jobs/7838923c/tmp/out_ch_toml.txt
```

And it works from a **project-level** `<project>/.codex/hooks.json`, keyed by that file's absolute path
(`ch_toml` and project runs both fired). This last one is a genuine surprise — see below.

### 3. Is plugin delivery required? — **No, and plugin delivery is actively broken.**

This is the finding that inverts the prior assumption. I built a synthesized local marketplace,
installed it cleanly, and computed a correct `trusted_hash` for the plugin key:

```
$ codex plugin marketplace add .../mp2
Installed marketplace root: .../mp2
$ codex plugin add hp@mp2
Installed plugin root: .../chp/plugins/cache/mp2/hp/0.1.0
```

```toml
[plugins."hp@mp2"]
enabled = true

[hooks.state."hp@mp2:hooks.json:session_start:0:0"]
enabled = true
trusted_hash = "sha256:3c07a46a8e9745e26381dab5e7cc989fe9c2f17fdff20248bb8d21aa03a4d72f"
```

```
exit=0
MARKER: NONE
```

The hook did not fire. The byte-identical hook payload fires from a loose `hooks.json`, so the payload
and the hash are not the problem. The cause:

```
$ codex features list | grep -i 'hook'
hooks                                stable             true
plugin_hooks                         removed            false
```

`plugin_hooks` is at stage **removed** with effective state **false**. I confirmed it cannot be revived
and that the failure is not a trust failure:

- `codex exec --enable plugin_hooks ...` → `MARKER: NONE`
- `codex exec --dangerously-bypass-hook-trust ...` (which *does* fire loose hooks with no state entry
  at all) → `MARKER: NONE`

Plugin hook sources are simply never fed to discovery in 0.147.0. The `[hooks.state]` entries for the
`trajectory` plugin sitting in the host's real config are vestigial — written by an earlier version,
now inert.

So: not only is plugin delivery not required, it is the one delivery mechanism that does **not** work.
The earlier probe's conclusion ("a loose `$CODEX_HOME/hooks.json` produced NO `[hooks.state]` entry at
all") was a correct observation with the wrong inference. Nothing auto-populates `[hooks.state]` for
any delivery mechanism — that is exactly the writer's job, and writing it by hand is sufficient.

A local-directory marketplace and a remote one behave identically here, because plugin hooks are dead
either way.

### 4. Failure mode when the hash is wrong or missing — **silent skip, always**

Three isolated homes, each with a valid `hooks.json` and a differently-broken state block:

| Home | `[hooks.state]` content | Result |
|---|---|---|
| `ch_wrong` | `enabled = true`, `trusted_hash = "sha256:000…0"` | exit 0, no marker |
| `ch_none` | (no state block at all) | exit 0, no marker |
| `ch_enonly` | `enabled = true`, no `trusted_hash` | exit 0, no marker |

```
########## ch_wrong
exit=0
MARKER: NONE
$ grep -ci hook log_ch_wrong.txt
0
```

Zero occurrences of the string "hook" anywhere in the session output. The session starts normally,
completes normally, exits 0, and never mentions that a hook was declined. No prompt, no stderr warning,
no non-zero exit. This is the `HookTrustStatus::Untrusted` / `Modified` branch in
`hook_trust_status()` — the handler is built into `hook_entries` (so the TUI's hooks browser can show it)
but is never pushed into `handlers` (`discovery.rs:704-708`).

For niwa this is the worst of the plausible failure modes: a wrong hash is indistinguishable at runtime
from a working one, so a stale or miscomputed hash degrades silently rather than failing loudly.

### 5. Supported escape hatch

**`--dangerously-bypass-hook-trust`** — a real, global CLI flag, present in the shipped binary and
covered by an upstream test (`codex-rs/exec/tests/suite/hooks.rs::exec_hook_trust_bypass_runs_session_start_hook`).
Verified against `ch_none`, which has **no** `[hooks.state]` block whatsoever:

```
$ codex exec ... --dangerously-bypass-hook-trust "Reply with exactly: HI" </dev/null
warning: `--dangerously-bypass-hook-trust` is enabled. Enabled hooks may run without review for this invocation.
warning: `--dangerously-bypass-hook-trust` is enabled. Enabled hooks may run without review for this invocation.
hook: SessionStart
hook: SessionStart Completed
exit=0
MARKER: /home/dgazineu/.claude/jobs/7838923c/tmp/out_ch_none.txt
```

It works, and it warns twice on stderr. It is per-invocation only — **there is no config or `-c`
equivalent**. The binary contains an app-server override key `bypass_hook_trust` with the error string
`` `bypass_hook_trust` override must be a boolean ``, but that is an app-server RPC-level override, not a
config surface. Both routes were tested and both failed:

```
### A: -c bypass_hook_trust=true      → exit=0, MARKER: NONE
### B: config.toml bypass_hook_trust = true → exit=0, MARKER: NONE
```

There is a second, stronger status — `HookTrustStatus::Managed`, which is trusted unconditionally with
no hash at all (`hook_trust_status()` returns `Managed` before ever looking at `trusted_hash`). It comes
from `ManagedHooksRequirementsToml` / `append_managed_requirement_handlers`, a managed-requirements
config layer rather than the user's `config.toml`. **I did not exercise this**, and I would not build on
it: it reads as an enterprise/managed-deployment surface, not a user-writable one.

`[projects."<path>"] trust_level = "trusted"` is **not** a hooks escape hatch — it does not substitute
for a `trusted_hash` (see the project-layer caveat below).

## Implications

**niwa can ship working Codex hooks by writing files alone.** The mechanism is a per-instance
`CODEX_HOME` containing `hooks.json` plus a `config.toml` with a matching `[hooks.state]` block. niwa
already materializes exactly that `CODEX_HOME`, so this adds one file and one TOML section — no
interactive step, no trust prompt, no TTY, no shelling out to `codex`. The `SessionStart` hook that later
work depends on will fire. Reimplementing the hash in Go is roughly twenty lines: build the identity
map, `encoding/json` with sorted keys (Go's `json.Marshal` sorts map keys natively, which is the
canonicalization Codex wants), `sha256.Sum256`, hex, prefix `sha256:`. The one subtlety is emitting
`timeout` explicitly (defaulted to 600, or clamped to 1..3 for `SessionEnd`) and omitting every optional
field that is unset.

**The delivery decision is settled, and it is the opposite of the working assumption.** Do not attempt to
ship Codex hooks inside a plugin. Claude plugins still install into Codex unmodified and remain the right
vehicle for skills and MCP servers, but any `hooks.json` inside them is inert in 0.147.0. Hooks must be a
separate, niwa-written `$CODEX_HOME/hooks.json`. This actually simplifies the design: hook injection stops
depending on the plugin/marketplace machinery entirely, so it cannot be broken by a marketplace add
failing or a plugin being disabled.

**The state key is an absolute path, which makes the config non-relocatable.** Because `key_source` for a
loose `hooks.json` is that file's absolute path, `config.toml` is bound to the instance directory. If an
instance is moved or its path changes, every `[hooks.state]` key goes stale and — because the failure is
silent — hooks quietly stop firing with no diagnostic. niwa should regenerate `config.toml` from the
instance's real path on every apply rather than templating it once, and treat the hooks block as derived
state, never as something a user hand-edits.

**Silent degradation is the main operational risk.** A wrong hash produces a session that looks completely
healthy. Anything niwa builds on Codex `SessionStart` should carry its own liveness signal rather than
assuming the hook ran. `--dangerously-bypass-hook-trust` exists as a debugging aid and could be a
last-resort fallback if niwa controls the launch command, but it is per-invocation, it prints a scary
warning twice, and it disables review for *every* hook in the session — the computed hash is strictly
better and costs nothing.

**Version risk is real but bounded.** `plugin_hooks` moving to stage "removed" is evidence this area is
actively churning. The hash algorithm itself is more stable — it is deliberately designed so that "hooks
from config TOML and hooks.json converge on the same trust identity" (the doc comment on
`NormalizedHookIdentity`), which means it is a normalized identity rather than a source-text digest, and
normalized identities change less often than formats. Still, niwa should pin or at least assert the Codex
version it generates for, because a normalization change (a new default, a new field) silently
invalidates every hash it has written.

## Surprises

**Codex does have project-level config.** The stated assumption that every setting comes from
`$CODEX_HOME` is wrong. `codex-rs/config/src/loader/mod.rs` defines a `ConfigLayerSource::Project {
dot_codex_folder }` layer with a `PROJECT_LOCAL_CONFIG_DENYLIST` (`model_provider`, `profiles`, `notify`,
`otel`, base URLs, …), and a project-local `<project>/.codex/hooks.json` fired for me with a
`trusted_hash` keyed by its absolute path. Caveat, stated precisely: the binary contains the string
"Project-local config, hooks, and exec policies are disabled in the following folders until the project
is trusted, but skills still load." My test directory was not a git repo and I passed
`--skip-git-repo-check`, and the hook fired both with and without a `[projects."<path>"] trust_level =
"trusted"` entry — so I verified the mechanism works, but I did **not** establish the general trust
precondition. This opens a design option worth considering separately: per-instance hooks without a
per-instance `CODEX_HOME` at all.

**The loose `hooks.json` was right all along.** The earlier probe tested the correct mechanism and read
the absence of an auto-written `[hooks.state]` entry as failure. Nothing ever auto-writes that entry for
any delivery path — the entry is the input, not the output.

**The plugin `[hooks.state]` entries on the host are dead.** They look like proof that plugin hooks work.
They are the opposite: a fossil from a version where the feature existed.

**`codex plugin` commands rewrite `config.toml` wholesale.** Running `plugin remove` / `plugin add`
replaced the file, dropping the `[hooks.state]` block I had written, and an unrelated
`[marketplaces.shirabe]` entry appeared in an isolated home that never had one. My symlinked `auth.json`
also vanished from that home during the same window. I could not fully explain this — no `CODEX_*` env
var, no `/etc/codex`, no `~/.config/codex`, and the entry is absent from the host config — so something
in the surrounding session environment concurrently touched the isolated `CODEX_HOME`. The reliable
lesson stands regardless: niwa should write `config.toml` last and should not assume anything it writes
survives a subsequent `codex plugin` invocation. Copying `auth.json` rather than symlinking it also
proved more robust, and avoids any chance of a token refresh writing through to the host's real file.

## Open Questions

- **Managed hooks.** `HookTrustStatus::Managed` bypasses the hash entirely and is the only truly
  hash-free path. I did not exercise it, and I did not determine where a managed-requirements layer is
  read from on Linux. If it turns out to be an ordinary file path, it would be a cleaner mechanism than
  per-hash entries. Determining this means tracing `ManagedHooksRequirementsToml` and the requirements
  layer loader upstream, then testing a synthesized managed layer.
- **Project-layer trust precondition.** Whether `<project>/.codex/hooks.json` fires inside a *git repo*
  that has not been trusted. Testing this means running `codex exec` in an untrusted git working copy
  without `--skip-git-repo-check`, with and without a `[projects.…] trust_level` entry.
- **Interactive TUI behavior.** Everything here was `codex exec`. `tui/src/startup_hooks_review.rs` and
  the snapshot `codex_tui__app__tests__bypass_hook_trust_startup_warning.snap` imply the TUI shows a
  startup review screen for untrusted hooks. Whether a *correctly* pre-written hash suppresses that screen
  entirely (it should — `review_is_needed` keys off trust status) is unverified; I did not drive a TUI.
  This matters if humans attach interactively to niwa instances.
- **Version stability.** Whether the normalization or the `sha256:`-prefixed identity changes across
  0.148.x. Checking this means re-running the Python reimplementation against a newer binary's own output.
- **MCP-tool hooks.** Only `type: "command"` was tested. `mcp_tool` hooks exist in the schema but are
  rejected at load time in 0.147.0 ("MCP tool hooks are not supported yet"), so they are not an option today.

## Summary

The `trusted_hash` is `sha256:` + hex of the SHA-256 of a key-sorted, compact JSON rendering of a
TOML-normalized hook identity (event label, matcher, and the single handler with `timeout` defaulted and
unset fields dropped), and I reproduced all 13 hashes the shipped binary wrote for a real installed
plugin — so niwa can compute it in Go and pre-write `[hooks.state]` with no prompt, which I confirmed
end-to-end by having a `SessionStart` hook write a marker file during a live `codex exec` session.
Hooks must be delivered as a loose `$CODEX_HOME/hooks.json` (or inline in `config.toml`) rather than
inside a plugin, because plugin-delivered hooks are stage-removed and inert in 0.147.0 — they do not fire
even with a correct hash or with trust bypassed — which inverts the working assumption but simplifies the
design by decoupling hook injection from the plugin machinery. The biggest open question is operational
rather than mechanical: a wrong or stale hash is skipped in complete silence with exit 0 and no
diagnostic, and since the state key embeds the instance's absolute path, niwa must regenerate the block on
every apply and carry its own liveness signal rather than trusting that the hook ran.
