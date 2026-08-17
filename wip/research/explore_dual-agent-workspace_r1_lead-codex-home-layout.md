# Lead: Which CODEX_HOME entries are shared versus isolated?

Measured against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex`, a
258 MB stripped static-PIE ELF; no JS bundle ships, so implementation evidence comes from
`strings` over the binary — Rust source paths such as `login/src/auth/storage.rs` and
`core/src/config/requirements.rs` are embedded in panic/tracing metadata and are quoted below).

Every experiment ran with `CODEX_HOME` under `/home/dgazineu/.claude/jobs/7838923c/tmp/codexhome/`.
Nothing was written into `~/.codex`; a closing `stat` confirms `auth.json` (inode 30076294),
`config.toml` (inode 30077093) and `installation_id` (inode 30061840) still carry their original
pre-spike mtimes. No credential value is reproduced anywhere below.

---

## Findings

### 0. The authoritative path map comes from Codex itself

`codex doctor --json` emits a `state.paths` check that names every path Codex resolves. Run
against an isolated home:

```
## config.load ok - config loaded
     CODEX_HOME = .../inst1
     config.toml = .../inst1/config.toml
     log dir     = .../inst1/log
     sqlite home = .../inst1
## state.paths ok - state paths and databases are inspectable
     state DB    = .../inst1/state_5.sqlite      (missing)
     log DB      = .../inst1/logs_2.sqlite       (missing)
     goals DB    = .../inst1/goals_1.sqlite      (missing)
     memories DB = .../inst1/memories_1.sqlite   (missing)
     queue DB    = .../inst1/queue_1.sqlite      (missing)
     thread history DB = .../inst1/thread_history_1.sqlite (missing)
     active rollout files = 0 files
## auth.credentials ok - auth is configured
     auth file = .../inst1/auth.json
     auth storage mode = File
```

This is the fastest way for niwa to validate a materialized home in CI, and it is how most of
the claims below were checked.

### 1. What Codex creates on its own (so niwa need not)

Starting from a home containing **only** `config.toml` and an `auth.json` symlink, then running
`codex doctor`, `codex debug models`, and `codex debug prompt-input`:

```
### inst1 after those three commands:
auth.json  cache  config.toml  installation_id  models_cache.json
.sandbox_migration  shell_snapshots  skills  tmp

### host ~/.codex entries NOT created by those commands:
goals_1.sqlite  history.jsonl  logs_2.sqlite(+-shm/-wal)  memories_1.sqlite  plugins
queue_1.sqlite  rules  sessions  state_5.sqlite  thread_history_1.sqlite
thread-writer-locks  .tmp  version.json
```

The uncreated set is exactly the "needs a real session or a real plugin install" set. **niwa
should create nothing but `config.toml`, `AGENTS.md`, `skills/<its own>`, and the symlinks.**
Everything else is Codex's to make, lazily, on first use.

Note `skills/.system` appeared by itself, containing the six bundled Codex skills
(`imagegen`, `openai-docs`, `plugin-creator`, `review-agent`, `skill-creator`,
`skill-installer`). Codex writes into `skills/`; niwa does not own that directory outright.
Confirming that it is Codex-managed, the bundled imagegen skill's own instruction text
(binary string) hard-codes the path:

```
python "${CODEX_HOME:-$HOME/.codex}/skills/.system/imagegen/scripts/remove_chroma_key.py"
```

### 2. The decisive experiment: refresh-token write-through — VERIFIED, and the
atomic-rename risk does not materialize

The host's real refresh token was never used. Per the safety rule I built a **fully synthetic**
`auth.json` (same shape as the real one: `auth_mode`, `OPENAI_API_KEY`, `tokens.{id_token,
access_token, refresh_token, account_id}`, `last_refresh` — shape read with a redacting
`jq`-style script that printed only key names and string lengths), gave it an expired JWT and a
stale `last_refresh`, and pointed `CODEX_REFRESH_TOKEN_URL_OVERRIDE` (an env var found in the
binary next to `https://auth.openai.com/oauth/token`) at a local Python server that returns a
well-formed but synthetic refresh response.

Setup — the isolated home's `auth.json` is a symlink to the shared file:

```
synth/auth.json -> .../shared2/auth.json
BEFORE: inode=30472475 size=1492
```

Run `codex doctor` in that home. The fake endpoint was hit and the file changed:

```
=== server hits ===
REFRESH-HIT path=/oauth/token len=123
=== AFTER doctor ===
synth/auth.json -> .../shared2/auth.json      <-- STILL A SYMLINK
target inode=30472475 size=1539               <-- INODE UNCHANGED
grep ROUND-n in target: ROUND-2               <-- new token landed in the shared file
```

Repeated a second time with a directory-listing watcher running in a tight loop against the
home:

```
BEFORE2: inode=30472475 links=1 size=1210
AFTER2:  inode=30472475 links=1 size=1539
refresh hits total: 2
transient names observed in synth/: . .. auth.json config.toml tmp
```

**Conclusion (verified by experiment): Codex writes `auth.json` in place — open/truncate/write,
not create-temp-and-rename.** The inode is stable across two refreshes, no `.tmp` sibling ever
appears in the home, and the symlink survives. A symlinked `auth.json` correctly writes a
rotated refresh token back through to the single real file. Sharing the host login across
instances works, and stays working.

An incidental but load-bearing observation: `codex doctor` **does** perform a token refresh when
the stored access token is expired. That means doctor is not a purely read-only probe against
real credentials. It did not refresh when run against a copy of the real `auth.json` (hash
`2dae73e3…` and mtime `1786908702` unchanged before and after), because the host's access token
was still valid — but niwa should not treat `codex doctor` as side-effect-free on auth.

### 3. Codex resolves symlinks before writing — even when it *does* use atomic rename

`config.toml` behaves differently from `auth.json`, and the contrast is the important part.
With `C/config.toml -> sharedcfg/config.toml`, running `codex mcp add probe -- /bin/true`:

```
Added global MCP server 'probe'.
config.toml: before_inode=32656666 after_inode=32656667 still_symlink=yes
```

and a second write:

```
config.toml inode: 32656667 -> 32656666; symlink=yes
```

The inode flips on every write (freed inodes being reused), which is the signature of
create-temp-then-`rename()`. **But the rename lands on the resolved target path, not on the
link.** The symlink inside `CODEX_HOME` is untouched and the content appears in the shared file.

So the specific failure mode the brief warned about — a rename replacing the symlink with a
regular file and silently un-sharing the entry — does not occur in 0.147.0 for either of the two
files tested. Symlinks are a sound mechanism here.

Two caveats worth carrying into the design: (a) **hard links would break** for any
rename-written file such as `config.toml`, since the rename replaces the inode — symlinks, not
hard links, are the right primitive; (b) this is behaviour, not a documented contract, so the
`codex doctor --json` check from finding 0 is worth wiring into niwa's verification path to
catch a regression.

### 4. Config keys that relocate individual paths out of `$CODEX_HOME`

Read from the binary's `ConfigToml` field list (the serde field-name table, appearing near
`config/src/config_toml.rs`): `history`, `sqlite_home`, `log_dir`, `cli_auth_credentials_store`,
`mcp_oauth_credentials_store`. There is no `auth_file`, `sessions_dir`, `cache_dir`,
`skills_dir`, or `plugins_dir` — `grep -c` returns 0 for every one of those names.

**`sqlite_home` is real and it works.** From `core/src/config/requirements.rs`:

```
Environment value for `$CODEX_SQLITE_HOME` is overridden by the required `sqlite_home` value
`CODEX_SQLITE_HOME` is overridden by an exact requirement for sqlite_home
```

Verified by experiment — two homes with `sqlite_home = ".../sqlshared"` in their `config.toml`,
`doctor` run concurrently from both:

```
[A]  sqlite home = .../sqlshared (dir)
     state DB    = .../sqlshared/state_5.sqlite (file)
     state DB integrity = ok   (log/goals/memories/queue/thread-history all ok)
[B2] identical
both exit 0
```

`log_dir` relocates the `log/` directory (unset by default; `~/.codex` has no `log/` today).
`[history] persistence` is a mode, not a path — `persistence = "none"` parses cleanly
(`HistoryPersistence` enum in the binary), so per-instance prompt history can be switched off
rather than relocated.

`cli_auth_credentials_store` is **not** a path either. Its enum is `AuthCredentialsStoreMode`
with variants `keyring` and `ephemeral` (plus the default file mode — doctor reports
`auth storage mode = File`). A `keyring` store would share the login across every home with no
symlink at all, which is architecturally cleaner than symlink surgery; it is untested here
because switching it requires `codex login`, which the safety rules forbid. Flagged as an open
question, not a recommendation.

Net: `sqlite_home` is the one genuinely useful relocation key. It does not remove the need for
the `auth.json` symlink.

### 5. `sessions/` and `history.jsonl`

**The resume picker filters by cwd by default.** From `codex resume --help`:

```
--all    Show all sessions (disables cwd filtering and shows CWD column)
```

and the same string in the binary, `Show all sessions (disables cwd filtering and shows CWD
column)`. The cwd is recorded in each rollout's first line:

```json
{"type":"session_meta","payload":{"session_id":"01a00c2d-…","cwd":"/home/dgazineu/dev/niwaw/tsuku/tsuku+trajectories_install",…}}
```

and mirrored as a column in the state DB (schema read directly from `state_5.sqlite`):

```sql
CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, …, cwd TEXT NOT NULL,
  title TEXT NOT NULL, …, git_sha TEXT, git_branch TEXT, git_origin_url TEXT, …)
```

Since niwa instances are distinct directories, **even a shared `sessions/` would not pollute the
default picker** — only `codex resume --all` would surface other instances' work. That weakens
the "shared sessions are confusing" argument considerably. It does not eliminate it: `--all`,
`archive`/`delete`/`fork` by id, and any future cross-cwd surface would still see everything.

I could not drive the picker itself: `codex resume --last` in a non-tty exits with
`Error: stdin is not a terminal`, and starting a real session was out of scope under the safety
rules (see Open Questions).

`history.jsonl` is a different animal. Its records carry no cwd at all:

```json
{"session_id":"01a00c10-…","ts":1786908829,"text":"do you understand what this workspace is about…"}
```

Keys are exactly `['session_id', 'ts', 'text']`. This is the composer's up-arrow prompt history,
and **nothing filters it by workspace**. Shared, every prompt typed in every instance appears in
every other instance's recall. That is both confusing and a mild disclosure surface, since
prompts routinely quote project-specific content. Isolate it.

**`sessions/` and `state_*.sqlite` are two halves of one inventory**, and doctor checks their
agreement (`state.rollout_db_parity`). Copying `sessions/` into a home with no state DB gives:

```
⚠ threads   rollout files exist but the state DB is missing
    active rollouts   2 files · 1.82 MB
    → Start Codex with no state DB present so startup backfill can create it from rollout files.
```

So the rollout files are the source of truth and the state DB is a derived index that
self-heals on startup. niwa never needs to create or migrate it. One subtlety: `threads.rollout_path`
is **absolute**, so a shared state DB indexing per-instance sessions is internally coherent but
permanently cross-instance — another reason to keep the pair together on the same side of the
isolate/share line.

### 6. `installation_id` — evidence says share

The binary sends it as an HTTP header alongside the session identity, on the Responses API path:

```
…/responses/compact  x-codex-installation-id  x-codex-window-id  x-codex-turn-metadata
x-codex-parent-thread-id  x-openai-subagent
```

and it appears in the turn-metadata field list next to `session_id`, `thread_id`, `window_id`,
`request_kind`. It is also the identity used for remote-control enrolment
(`app-server-transport/src/transport/remote_control/`, fields `installation_id`,
`environment_id`). So it is per-installation identity attached to every model request —
telemetry and service-side attribution, not a local cache.

Behaviour, verified: Codex **generates a fresh UUID when the file is absent**. A bare home that
had never seen it gained one after `codex debug prompt-input`:

```
--- installation_id comparison ---   DIFFERENT from host (newly generated)
format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

And a symlinked one is read, not replaced:

```
instid: after=30472363 symlink=yes
instid == host's: YES
```

Left alone, N niwa instances would present as N distinct installations of Codex to OpenAI for
one developer. Symlinking it costs nothing and keeps the developer looking like one install.

### 7. Multi-instance concurrency

Two isolated homes (`A`, `B2`), each symlinking `auth.json`, `models_cache.json` and
`installation_id` to the same shared files, running `codex doctor` simultaneously:

```
A exit=0
B2 exit=0
AFTER: models inode=30472497  instid inode=30472363  auth inode=30472475   (all unchanged)
all three entries in both homes: still symlinks
```

No error, no block, no clobbering. The shared-`sqlite_home` concurrency run in finding 4 also
passed with integrity `ok` on all six databases from both processes at once.

All six databases are **WAL** — read from the files themselves and confirmed in the binary
(`WAL journal_mode`, `NORMAL synchronous`, `ON foreign_keys`, `INCREMENTAL auto_vacuum`, and a
`locking_mode`/`cipher_*` set indicating optional SQLCipher):

```
goals_1.sqlite          journal=wal   tables=['_sqlx_migrations','thread_goal_continuation_deferrals','thread_goals']
logs_2.sqlite           journal=wal   tables=['_sqlx_migrations','logs','sqlite_sequence']
memories_1.sqlite       journal=wal   tables=['_sqlx_migrations','jobs','stage1_outputs']
queue_1.sqlite          journal=wal   tables=['_sqlx_migrations','queued_items']
state_5.sqlite          journal=wal   tables=['_sqlx_migrations','backfill_state',…,'threads']
thread_history_1.sqlite journal=wal   tables=['_sqlx_migrations','thread_items','thread_turns',…]
```

WAL means concurrent readers plus one writer across processes — corruption is not the risk;
writer contention (`SQLITE_BUSY`) is, and only if the DBs are shared. Isolating them removes the
question entirely.

Codex also maintains its own locks: `thread-writer-locks/<thread-uuid>.lock` plus a
`.coordination.lock` (`thread-store/src/local/writer_lock.rs`), a `rollout-maintenance.lock`,
`.tmp/plugins.sync.lock`, and a `.lock` inside each `tmp/arg0/codex-arg0XXXXXX` helper-extraction
directory. Per-thread locks are UUID-keyed so they never collide, but `.coordination.lock` and
`plugins.sync.lock` are singletons — another argument for keeping those directories per-instance.

### 8. `rules/` is a security-relevant surface

`~/.codex/rules/default.rules` is command-approval memory, and the entries are workspace-shaped:

```
prefix_rule(pattern=["gh", "pr", "view"], decision="allow")
prefix_rule(pattern=["git", "-C", "private/vision", "worktree", "add", "--detach",
                     "/tmp/vision-codex-adoption-558", "origin/…"], decision="allow")
```

A relative path like `private/vision` resolves differently in every instance. Sharing this file
means a command a developer approved once, in one workspace, is silently pre-approved in a
different workspace against different repositories. Isolate, unconditionally.

### 9. Complete decision table

Sizes are from `du -sh ~/.codex/*` on the live host (total ≈ 40 MB, dominated by `cache/` at
18 MB and `logs_2.sqlite` at 15 MB + 4.1 MB WAL).

| Entry | What it is / what writes it | Size, growth | Decision | Why |
|---|---|---|---|---|
| `auth.json` | OAuth tokens + mode; written in place on refresh (verified §2) | 4 KB, rewritten per refresh | **share** (symlink) | Isolating forces a login per instance. Write-through verified; no rename hazard. |
| `config.toml` | All Codex settings; also project trust levels, hook trust hashes, plugin enable state, marketplace pins. Written by Codex via temp+rename on the resolved path (§3) | 3 KB, occasional | **isolate** | The whole point of a per-instance home. Note Codex *writes* it, so niwa cannot treat it as static — see the hook-trust and delivery leads. |
| `AGENTS.md` | Composed workspace context | small, niwa-written | **isolate** | Per-workspace by definition. |
| `skills/` | niwa's workspace skills **plus** Codex's `skills/.system/` (six bundled skills, auto-created) | 628 KB | **isolate**, but never wipe | Workspace-specific skill set. Codex co-owns the directory; niwa must write beside `.system/`, not replace the tree. |
| `rules/` | Command-approval prefix rules (§8) | 8 KB, grows with approvals | **isolate** | Cross-workspace auto-approval of commands is a real security regression. |
| `history.jsonl` | Composer prompt history, `{session_id, ts, text}`, **no cwd** (§5) | 3 KB, one line per prompt | **isolate** | Nothing filters it; shared, every prompt leaks into every instance's up-arrow. |
| `sessions/` | Rollout JSONL, `sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`; source of truth for resume; compressed variants `.jsonl.zst` | 1.9 MB for 2 sessions (~930 KB each) | **isolate** | Largest fast-growing artifact; cwd filtering means sharing is *survivable*, but `--all` and id-addressed commands still cross over. Isolating matches "this instance's work". |
| `state_5.sqlite` | Index over `sessions/` (`threads` table, absolute `rollout_path`, `cwd`, git metadata); backfilled from rollouts on startup | 176 KB | **isolate, with `sessions/`** | Two halves of one inventory; doctor checks their parity. Derived, so it self-heals — niwa never creates it. |
| `thread_history_1.sqlite` | Thread items/turns projection | 552 KB | **isolate** | Same session-scoped state; belongs with `sessions/`. |
| `queue_1.sqlite` | Queued items for a session | 28 KB | **isolate** | Session-scoped. |
| `goals_1.sqlite` | `/goal` objectives and continuation deferrals | 32 KB | **isolate** | Goals are about the work in *this* workspace. |
| `memories_1.sqlite` | Extracted/consolidated memories (`[memories]` config: `generate_memories`, `max_rollout_age_days`, …) | 40 KB | **isolate** | Memories derived from one workspace's rollouts would mislead in another. Cross-contamination of exactly the kind that matters. |
| `logs_2.sqlite` (+`-wal`, `-shm`) | Structured runtime logs; **the fastest grower on the host** | 15 MB + 4.1 MB WAL after ~3 h of use | **isolate** | Diagnostic, per-instance. Growth rate makes a shared file a contention hot spot. Consider pointing `sqlite_home` at a niwa-managed dir to keep it out of the instance tree if size becomes an issue. |
| `models_cache.json` | Model catalog with etag; refetched on miss | 129 KB, rare | **either — share** (symlink) | Cheap to rebuild (one fetch), but sharing is one symlink and saves a startup round-trip per instance. Verified to survive as a symlink across runs. Isolating is equally correct if niwa prefers fewer shared links. |
| `installation_id` | UUID sent as `x-codex-installation-id` on every request (§6) | 36 B, write-once | **share** (symlink) | Per-instance ids make one developer look like N installations. Verified: generated when absent, read (not replaced) when symlinked. |
| `cache/` | Content-addressed HTTP caches: `remote_plugin_catalog` (12.8 MB), `codex_app_directory` (4.6 MB), `codex_apps_tools`, `codex_apps_server_info` | **18 MB — the largest entry** | **share** (symlink) | Content-addressed by hash, so no cross-contamination is possible; isolating costs ~18 MB of re-download per instance. Highest-value share after auth. |
| `plugins/` | `plugins/cache/<marketplace>/<plugin>/<version>/` installed plugin trees, plus `.remote-plugin-install-staging/` | 420 KB | **share `plugins/cache`** (already verified in the prior spike); leave the rest to Codex | Version-addressed content; sharing avoids re-downloading each plugin per instance. *Which* plugins are enabled is in the isolated `config.toml`, so the instance-specific part stays per-instance. |
| `.tmp/` | Plugin/marketplace sync staging: a shallow git checkout keyed by `.tmp/plugins.sha`, plus `plugins.sync.lock`, `marketplaces/` | 20 KB (+ git objects) | **leave to Codex** (isolate by omission) | Rebuildable staging; contains a singleton sync lock, so per-instance avoids contention. |
| `shell_snapshots/` | Captured shell environment per session, `<thread-uuid>.<nanos>.sh` | 44 KB, ~18 KB per session | **leave to Codex** (isolate by omission) | Session-scoped, regenerated automatically. |
| `thread-writer-locks/` | `<thread-uuid>.lock` + `.coordination.lock` (`writer_lock.rs`) | 4 KB | **leave to Codex** (isolate by omission) | Must sit with `sessions/`. The singleton `.coordination.lock` is a contention point if shared. |
| `tmp/arg0/` | Extracted helper binaries (`codex-linux-sandbox`, `apply_patch`, `codex-execve-wrapper`) + a `.lock` | 20 KB | **leave to Codex** | Auto-extracted on first run; isolating costs a trivial re-extract. |
| `version.json` | Update-check state: `latest_version`, `last_checked_at`, `dismissed_version` | 105 B | **either — leave to Codex** | Trivial. Sharing would propagate a dismissed-update choice across instances, which is arguably nicer, but not worth a symlink. |
| `.sandbox_migration` | One-line migration marker, currently `v1` | 3 B | **leave to Codex** | Auto-created; verified to appear on its own. |
| `log/` | Text log dir; **absent on the host**, relocatable via `log_dir` | — | **leave to Codex** | Not created by default in 0.147.0. |
| `app-server-daemon/`, `app-server-control/` | Background app-server pid files, `settings.json`, and a unix control socket | absent until used | **leave to Codex — must stay per-instance** | A shared control socket would let one instance's TUI attach to another instance's daemon. Isolation here is the safe default and is free. |

Nothing in `~/.codex` is left undecided.

---

## Implications

**Symlinks are sufficient; no bind mount, hard link, or config override is required for auth.**
The mechanism the earlier spike assumed is now verified end to end, including the failure mode
that would have killed it. Codex resolves symlinks before writing, whether it writes in place
(`auth.json`) or via temp+rename (`config.toml`). The one thing niwa must *not* do is use hard
links — a rename-written file would silently diverge.

**The share list is short and stable: `auth.json`, `installation_id`, `plugins/cache`, `cache/`,
and optionally `models_cache.json`.** Everything else is either isolated because it is
workspace-shaped state, or simply not created by niwa at all. That last category is large — of
the 25 entries on a real host home, niwa creates four (`config.toml`, `AGENTS.md`,
`skills/<its own>`, and the symlinks) and Codex creates the rest lazily. That is a much smaller
surface for niwa to own than the brief's framing implied.

**`cache/` deserves to be on the share list even though nobody asked about it.** At 18 MB it is
the largest entry in the home and it is content-addressed, so sharing carries no contamination
risk at all. For a developer with ten instances, isolating it means 180 MB and ten redundant
downloads of the same plugin catalog.

**`rules/` is the entry most likely to be got wrong.** It looks like a cache and it is not: it is
persisted command-approval decisions containing relative paths that mean different things in
different instances. It must be isolated, and that should be stated explicitly in the design
rather than left to the "everything else is isolated" default.

**`skills/` is co-owned.** Codex materializes `skills/.system/` and the bundled imagegen skill
hard-codes its own absolute path under `$CODEX_HOME/skills/.system/`. If niwa's materialization
step ever does `rm -rf skills/ && cp -r …`, it deletes six working skills and breaks one of them
by absolute path. niwa must write into `skills/` additively.

**`sqlite_home` gives niwa a lever it may not need.** It cleanly relocates all six databases out
of the instance tree in one config line, verified working under concurrent access. The obvious
use is keeping the 15 MB (and growing) `logs_2.sqlite` out of an instance directory the
developer might expect to be small. It is not needed for correctness.

**`codex doctor --json` is the verification hook.** `state.paths`, `auth.credentials`,
`config.load`, and `state.rollout_db_parity` between them assert almost everything niwa's
materialization needs to be true, in machine-readable form. Wiring it into niwa's own doctor or
its tests would catch a future Codex release changing the write behaviour that this whole design
rests on.

---

## Surprises

**`codex doctor` refreshes the auth token.** It is not a read-only probe. Against expired
credentials it performs a full token refresh and rewrites `auth.json`. That is worth knowing
before anyone wires `codex doctor` into a health check that runs frequently, and it is why this
spike used a synthetic credential with a redirected refresh endpoint rather than the host's real
token.

**`auth.json` and `config.toml` use *different* write strategies** — in-place versus temp+rename.
Both write through a symlink correctly, but only because the rename resolves the link first. The
divergence means "Codex writes files atomically" is not a safe generalisation in either
direction.

**The resume picker already filters by cwd.** The brief's worry that a shared `sessions/` would
show unrelated workspaces in the picker turns out to be mostly handled by Codex itself
(`--all` exists precisely to *disable* cwd filtering). This makes the sessions decision much less
consequential than expected — isolation is still the right call, but for tidiness and growth
rate rather than to prevent a confusing picker.

**`installation_id` is not merely telemetry.** It rides on every Responses API request as
`x-codex-installation-id` and is the identity for remote-control enrolment. That is a firmer
argument for sharing it than "telemetry hygiene."

**`history.jsonl` has no cwd field**, while sessions and the state DB both do. It is the one
piece of user-typed content with no workspace scoping whatsoever.

**`rules/` was not on the brief's undecided list**, and it is the most security-relevant entry in
the home.

**`cli_auth_credentials_store = "keyring"` exists**, which would make the whole `auth.json`
sharing question disappear if the host used it.

---

## Open Questions

**A real interactive session was never run.** Everything session-created — `sessions/`,
`state_5.sqlite`, `history.jsonl`, `thread-writer-locks/`, `logs_2.sqlite`, the goals/memories
databases — was reasoned about from copies of the host's files, from the schemas inside them,
and from `codex doctor`'s checks, not from watching an instance create them. The blocker is the
safety rule: OpenAI rotates refresh tokens on use (the binary carries the error string *"Your
access token could not be refreshed because your refresh token was already used"*), so refreshing
against a *copy* of the host's `auth.json` would invalidate the host's live login server-side —
a copy protects the file, not the token. Exercising this properly needs a second, disposable
Codex login. With one, the tests to run are: two instances holding sessions concurrently against
a shared `sqlite_home` (does `SQLITE_BUSY` surface to the user?); whether `codex resume`'s picker
in a pty really hides another instance's sessions; and whether a genuine refresh mid-session
still preserves the `auth.json` inode under load.

**`cli_auth_credentials_store = "keyring"` is untested.** It would be a strictly cleaner sharing
mechanism than a symlink — no per-instance file at all — but switching to it requires
`codex login`, which the safety rules put out of bounds. Worth one experiment on a scratch
machine before committing to the symlink design, since it would change the shape of the answer.

**`models_cache.json`'s write path is unverified.** `codex debug models` did *not* rewrite a
deliberately corrupted cache (inode and the 15-byte stale content both survived), so the file is
refreshed only on etag miss or expiry and I never observed an actual write. Whether it is
in-place or rename-based is unknown. It does not matter much — the file is cheap to rebuild, so
if a future Codex release clobbered the symlink the only cost would be one re-download — but the
table's "share" recommendation for it rests on weaker evidence than the rest.

**`plugins/cache` sharing is carried over from the prior spike, not re-verified here.** What this
spike adds is the surrounding context: `.tmp/plugins.sha` keys a shallow git checkout used for
sync, and `.tmp/plugins.sync.lock` is a singleton — so sharing `plugins/cache` while leaving
`.tmp/` per-instance is coherent, but the interaction between an isolated `config.toml`'s
`[marketplaces]`/`[plugins]` state and a shared cache directory has not been exercised with two
instances enabling different plugin sets.

**Long-run growth is estimated from a three-hour-old home.** `logs_2.sqlite` reached 15 MB plus a
4.1 MB WAL in that time. Whether it plateaus, and whether Codex prunes it, is unknown; if it does
not, `sqlite_home` or `log_dir` becomes a requirement rather than an option.

---

## Summary

A symlinked `auth.json` correctly writes rotated refresh tokens through to the single real file —
verified by driving two real token refreshes against a synthetic credential and a redirected
refresh endpoint, with the target inode unchanged and the symlink intact both times; the feared
atomic-rename hazard does not materialize because Codex resolves symlinks before writing, even
for `config.toml`, which genuinely does use temp-and-rename. The design implication is that
symlinks suffice for the whole share list — `auth.json`, `installation_id`, `plugins/cache`, and
the 18 MB content-addressed `cache/` — while niwa creates only `config.toml`, `AGENTS.md`, and
its own skills, leaving Codex to lazily create the other twenty-odd entries itself; `rules/` must
be isolated because it stores command approvals with workspace-relative paths, and `skills/` must
be written additively because Codex co-owns it via `skills/.system/`. The biggest open question
is that no real interactive session was ever run — every session-created artifact was reasoned
about from copies and schemas rather than observed being written — because refreshing a copied
token would invalidate the host's live login server-side, so closing it needs a second disposable
Codex login.
