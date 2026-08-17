# Lead: Measured Codex MCP and environment-policy schema (codex-cli 0.147.0)

## Method (commands run, scratch setup, safety measures taken)

Binary confirmed before anything else:

```
$ codex --version
codex-cli 0.147.0
$ which codex
/home/dgazineu/.tsuku/tools/current/codex   ->  /home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex
```

**The developer's real `~/.codex/` was never read and never written.** `CODEX_HOME`
is honored by this build (confirmed in `codex doctor --json`, which reports
`"CODEX_HOME": "/home/dgazineu/.claude/jobs/08526afa/tmp/codexspike/home5"` when
the variable is exported). Every probe exported `CODEX_HOME` to a scratch
directory under `/home/dgazineu/.claude/jobs/08526afa/tmp/codexspike/`. Scratch
homes used: `home` (schema generation), `home2`-`home5` (schema and env-policy
probes), `home6` (trust and precedence), `home7` (transport edge cases). The
project fixture is `…/codexspike/proj`, a scratch `git init` directory carrying
`.codex/config.toml`.

No interactive session was ever started. Every probe is a non-interactive
subcommand: `codex mcp add|list|get`, `codex doctor --json`, and
`codex sandbox -- env`. No secret values were sent anywhere; all injected values
are placeholders (`SPIKE_PROBE_VALUE_1`, `from_set`, `project_value`, …). The
niwa functional test suite was not run.

**The measurement instrument for section B.** `codex sandbox -- env` runs a real
command through Codex's own sandbox and prints the environment that command
receives, which makes the environment policy directly observable. The default
`bwrap` backend cannot start in this container:

```
$ codex sandbox -- env
bwrap: loopback: Failed RTM_NEWADDR: Operation not permitted
```

Switching backends works, and every B measurement below uses these flags:

```
codex --enable use_legacy_landlock --disable use_linux_sandbox_bwrap sandbox -- env
```

**Caveat on that instrument, stated up front.** `codex sandbox` demonstrably
applies `shell_environment_policy` — `inherit`, `set`, `exclude`, and
`include_only` all change its output in the ways reported below, so this is the
policy code path and not a raw `execve`. It is nonetheless the *user-invoked*
sandbox entry point, not the model's shell tool inside a live session. I did not
verify that a live session's shell tool resolves the policy identically, because
that needs an interactive or model-calling session, which the constraints
exclude. Treat section B as measured for `codex sandbox` and
**untested for the in-session shell tool**. The one finding where this caveat
actually bites is the `ignore_default_excludes` default, called out again there.

Scripts are at `/home/dgazineu/.claude/jobs/08526afa/tmp/codexspike/probe*.sh`,
`trust*.sh`, `final.sh`.

## A. `mcp_servers` Schema

### The authoritative shape, written by the binary itself

Rather than guess the TOML, I had Codex generate it. `codex mcp add` writes the
canonical form into `$CODEX_HOME/config.toml`:

```
$ codex mcp add fsprobe --env SPIKE_PROBE_VALUE_1=abc --env SPIKE_PROBE_VALUE_2=def \
    -- npx -y @modelcontextprotocol/server-filesystem /tmp
Added global MCP server 'fsprobe'.
$ codex mcp add httpprobe --url https://example.invalid/mcp \
    --bearer-token-env-var SPIKE_TOKEN_ENV --oauth-client-id cid123 \
    --oauth-resource https://example.invalid/res
Added global MCP server 'httpprobe'.
$ codex mcp add minimal -- true
Added global MCP server 'minimal'.
```

The resulting `config.toml`, verbatim — **this is the schema, emitted by the
tool that consumes it**:

```toml
[mcp_servers.fsprobe]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]

[mcp_servers.fsprobe.env]
SPIKE_PROBE_VALUE_1 = "abc"
SPIKE_PROBE_VALUE_2 = "def"

[mcp_servers.httpprobe]
url = "https://example.invalid/mcp"
bearer_token_env_var = "SPIKE_TOKEN_ENV"
oauth_resource = "https://example.invalid/res"

[mcp_servers.httpprobe.oauth]
client_id = "cid123"

[mcp_servers.minimal]
command = "true"
```

**Measured.** Table is `[mcp_servers.<name>]`; the server name is the TOML key.

### Full field set

`codex mcp list --json` and `codex mcp get <name> --json` expose every field
Codex resolves. I then hand-wrote a `config.toml` declaring all of them and
confirmed each round-trips, so these are TOML-writable keys and not
JSON-output-only artifacts. Hand-written input:

```toml
[mcp_servers.full]
command = "true"
args = ["a", "b"]
cwd = "/tmp"
env_vars = ["PATH", "HOME"]
enabled = true
startup_timeout_sec = 42
tool_timeout_sec = 99
enabled_tools = ["t1"]
disabled_tools = ["t2"]

[mcp_servers.full.env]
SPIKE_PROBE_VALUE_1 = "v1"

[mcp_servers.httpfull]
url = "https://example.invalid/mcp"
http_headers = { "X-Spike" = "hdr" }
env_http_headers = { "X-Spike-Env" = "SPIKE_PROBE_VALUE_1" }
```

`codex mcp get full --json` returned every value intact:

```json
{
  "name": "full",
  "enabled": true,
  "transport": {
    "type": "stdio", "command": "true", "args": ["a","b"],
    "env": {"SPIKE_PROBE_VALUE_1": "v1"},
    "env_vars": ["PATH","HOME"], "cwd": "/tmp"
  },
  "enabled_tools": ["t1"], "disabled_tools": ["t2"],
  "startup_timeout_sec": 42.0, "tool_timeout_sec": 99.0
}
```

| Field | Type | Transport | Required | Notes |
|---|---|---|---|---|
| `command` | string | stdio | yes (stdio) | Selects stdio transport by its presence |
| `args` | array of string | stdio | no (default `[]`) | |
| `env` | table string→string | stdio | no (default absent) | Explicit vars handed to the server process |
| `env_vars` | array of string | stdio | no (default `[]`) | **Names** to forward from Codex's own env — distinct from `env` |
| `cwd` | string | stdio | no | Working directory for the server process |
| `url` | string | streamable_http | yes (http) | Selects HTTP transport by its presence |
| `bearer_token_env_var` | string | streamable_http | no | Name of an env var holding the token, never the token itself |
| `http_headers` | table string→string | streamable_http | no | Literal header values |
| `env_http_headers` | table string→string | streamable_http | no | Header name → **env var name** to read the value from |
| `oauth_resource` | string | streamable_http | no | |
| `oauth.client_id` | string (sub-table) | streamable_http | no | |
| `enabled` | bool | both | no (default `true`) | `false` shows as `disabled` in `codex mcp list` |
| `startup_timeout_sec` | number | both | no | Surfaces as float |
| `tool_timeout_sec` | number | both | no | Surfaces as float |
| `enabled_tools` | array of string | both | no | Visible via `mcp get`, not `mcp list` |
| `disabled_tools` | array of string | both | no | Visible via `mcp get`, not `mcp list` |

All **measured**.

### Transport selection

There are exactly two transports: `stdio` and `streamable_http`. **There is no
SSE transport.** Transport is inferred from which of `command`/`url` is present;
the CLI enforces mutual exclusivity:

```
$ codex mcp add both --url https://x.invalid/mcp -- somecmd
error: the argument '--url <URL>' cannot be used with '[COMMAND]...'
$ codex mcp add httpenv --url https://x.invalid/mcp --env A=B
Error: command is required
```

A literal `type` key in the TOML is **ignored**, not honored and not rejected:

```
$ codex -c 'mcp_servers.sse={type="sse",url="https://x.invalid/sse"}' mcp list --json
      "transport": { "type": "streamable_http", "url": "https://x.invalid/sse", ... }
```

**Measured, and this is a trap for section E:** a server declared `"type": "sse"`
does not fail — it is silently treated as streamable HTTP, a different wire
protocol against the same URL.

### Unknown fields: silently ignored

```
$ cat $CODEX_HOME/config.toml
[mcp_servers.unknownfield]
command = "true"
totally_bogus_key = "xyz"

$ codex mcp list --json     # exit 0, server present and usable
```

**Measured.** No warning, no stderr line, exit 0. The top-level `--strict-config`
flag exists to turn this into an error, but it is **not available on the `mcp`
subcommand**:

```
$ codex --strict-config mcp list --json
Error: `--strict-config` is not supported for `codex mcp`
```

So an unknown field is undetectable through the `mcp` subcommand family.

### Malformed entries: whole-config blast radius, not per-server

This is the most operationally important finding in section A.

```
$ cat $CODEX_HOME/config.toml
[mcp_servers.good]
command = "true"

[mcp_servers.broken]
args = ["no", "command", "and", "no", "url"]

$ codex mcp list --json
Error: failed to load bootstrap configuration

Caused by:
    invalid transport
    in `mcp_servers.broken`

$ codex mcp get good --json      # exit 1 — the VALID server is unreachable too
Error: failed to load bootstrap configuration

Caused by:
    invalid transport
    in `mcp_servers.broken`
```

**Measured.** One malformed entry fails the entire configuration load. The
well-formed sibling `good` cannot be read either. Cross-transport field misuse
behaves the same way:

```
$ codex -c 'mcp_servers.s={command="true",http_headers={A="b"}}' mcp list --json
Error: failed to load bootstrap configuration

Caused by:
    http_headers is not supported for stdio
    in `mcp_servers.s`
```

The practical consequence for niwa: a generator that emits one bad
`[mcp_servers.*]` entry does not degrade that one server, it bricks Codex
configuration for the whole directory. Errors do name the offending table
(`in \`mcp_servers.broken\``), which is at least diagnosable. Validation belongs
on niwa's side of the line, before the file is written.

## B. `shell_environment_policy` Semantics

### The field set

Extracted from the binary's own serde field list (**documented**, from
`strings` on the shipped binary — the struct's field names as compiled):

```
inherit  ignore_default_excludes  exclude  set  include_only  filters  experimental_use_profile
```

Types confirmed by feeding deliberate type errors and reading the rejection
(**measured**):

```
$ codex -c shell_environment_policy.inherit=bogusvalue sandbox -- true
Error: unknown variant `bogusvalue`, expected one of `core`, `all`, `none`
in `shell_environment_policy.inherit`

$ codex -c 'shell_environment_policy.set={SPIKE_NUM=42}' sandbox -- true
Error: invalid type: integer `42`, expected a string
in `shell_environment_policy.set.SPIKE_NUM`

$ codex -c 'shell_environment_policy.experimental_use_profile="bogus"' sandbox -- true
Error: invalid type: string "bogus", expected a boolean
in `shell_environment_policy.experimental_use_profile`

$ codex -c 'shell_environment_policy.filters={bogus="x"}' sandbox -- true
Error: invalid shell environment policy in session-flags: unknown variant `x`,
expected `include` or `exclude`
in `shell_environment_policy`
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `inherit` | enum `core` \| `all` \| `none` | **`all`** | Which parent-process vars form the base |
| `set` | table string→string | absent | Vars to add/override. **Values must be strings** |
| `exclude` | array of glob string | `[]` | Drops matching names from the inherited base |
| `include_only` | array of glob string | `[]` (no filtering) | Final whitelist over the merged result |
| `ignore_default_excludes` | bool | **`true`** | See the finding below — the default is not what the name suggests |
| `filters` | map keyed `include`/`exclude` | absent | Structured variant of the two lists; shape probed, semantics **untested** |
| `experimental_use_profile` | bool | absent | **Untested** |

### `inherit` — measured membership

```
$ codex … -c shell_environment_policy.inherit=none sandbox -- env | cut -d= -f1
CODEX_SANDBOX_NETWORK_DISABLED

$ codex … -c shell_environment_policy.inherit=core sandbox -- env | cut -d= -f1
CODEX_SANDBOX_NETWORK_DISABLED
HOME
LANG
LOGNAME
PATH
SHELL
USER
```

`CODEX_SANDBOX_NETWORK_DISABLED` is injected by the sandbox itself, not by the
policy, so `inherit = "none"` yields a genuinely empty environment. `core` is a
fixed six-name whitelist: `HOME`, `LANG`, `LOGNAME`, `PATH`, `SHELL`, `USER`.
(The binary's string table also carries `TMPDIR`, `TMP`, and `LC_ALL` adjacent to
these names; they did not appear in the measured `core` output, so either they
are conditional or belong to a neighbouring list. **Measured absent, cause
untested.**)

**The default is `all`.** With no `shell_environment_policy` table declared at
all, `codex sandbox -- env` returned 96 variables — byte-identical in count and
content to an explicit `inherit = "all"`. `inherit = "core"` returned 7.

### `set` is additive, and overrides on collision

```
no table declared                              -> 96 vars
set = {SPIKE_SET_MARKER="from_set"}            -> 97 vars, SPIKE_SET_MARKER=from_set
inherit="none" + same set                      ->  2 vars, SPIKE_SET_MARKER=from_set
set = {SPIKE_INHERITED_MARKER="overridden_by_set"}
                                               -> 96 vars, SPIKE_INHERITED_MARKER=overridden_by_set
```

**Measured.** `set` merges over the inherited base rather than replacing it, and
a name present in both takes the `set` value. This closes the open question the
r2 research flagged: it is additive, not replacing.

### Ordering: `set` beats `exclude`, `include_only` beats `set`

```
exclude=["SPIKE_SET_MARKER"] + set={SPIKE_SET_MARKER="from_set"}
  -> SPIKE_SET_MARKER=from_set          (set survives the exclude)

include_only=["PATH"] + set={SPIKE_SET_MARKER="from_set"}
  -> CODEX_SANDBOX_NETWORK_DISABLED, PATH     (set value is GONE)

include_only=["PATH","SPIKE_SET_MARKER"] + same set
  -> CODEX_SANDBOX_NETWORK_DISABLED, PATH, SPIKE_SET_MARKER
```

**Measured.** The pipeline is: inherit base → apply `exclude` → merge `set` →
apply `include_only` as a final whitelist. The asymmetry is a genuine trap — a
developer who sets `include_only` for their own reasons silently drops every
variable niwa delivers through `set`, with no error and no warning. Glob
wildcards work in both lists (`exclude=["SPIKE_INHERITED*"]` and
`exclude=["*KEY*"]` both matched).

### No interpolation

```
$ codex … -c 'shell_environment_policy.set={SPIKE_SET_MARKER="pre_${HOME}_post"}' sandbox -- env
SPIKE_SET_MARKER=pre_${HOME}_post
```

**Measured.** `${HOME}` is delivered as literal text. There is no variable
expansion, no shell evaluation, no reference syntax of any kind in `set` values.
niwa must resolve every value fully before writing it.

### `ignore_default_excludes` defaults to `true` — secrets are inherited by default

The binary carries default exclude patterns `*KEY*` and `*TOKEN*` in its string
table. They are **not applied unless you explicitly opt in**. Counts of
inherited variables whose names match `KEY|TOKEN`, same parent environment
throughout:

```
absent (no table at all)        : 12
inherit=all only                : 12
ignore_default_excludes = true  : 12
ignore_default_excludes = false : 0
```

With the default in force, names including `OPENAI_API_KEY`, `GH_TOKEN`,
`API_KEY`, and my placeholder `SPIKE_SECRET_KEY` / `SPIKE_NPM_TOKEN` were all
present in the command's environment. Setting `ignore_default_excludes = false`
dropped exactly the `*KEY*` and `*TOKEN*` matches; `*PASSWORD*` and `*SECRET*`
names (`FOOL_PASSWORD`, `MORNINGSTAR_PASSWORD`) survived even then, so the
default list is those two patterns only.

**Measured, with the section-Method caveat applied hardest here.** This is a
`codex sandbox` measurement. If the in-session shell tool constructs its policy
through a different default, this specific number could differ — and it is the
one finding worth re-measuring inside a live session before any security claim
is built on it. What is not in doubt is the direction of the flag: `true` means
"ignore the default excludes", and the field is `true` when unstated.

## C. Trust Gating

Fixture: `…/codexspike/proj` with a real `.git`, carrying
`.codex/config.toml` that declares `[mcp_servers.projectonly]`,
`[mcp_servers.shared]`, and `shell_environment_policy.set = { SPIKE_PROJECT_ENV
= "from_project_layer" }`. Scratch `CODEX_HOME` declares its own `globalonly`
and `shared` servers plus `SPIKE_GLOBAL_ENV`. All commands run with the project
as cwd.

**Untrusted** (no `[projects."…"]` entry anywhere):

```
--- mcp list ---
    "name": "globalonly",  "command": "global-only-server",
    "name": "shared",      "command": "GLOBAL_LAYER_WINS",
--- env ---
SPIKE_GLOBAL_ENV=from_global_layer
--- doctor ---
"mcp servers": "2",
```

`projectonly` is absent. `SPIKE_PROJECT_ENV` is absent. `shared` resolves to the
global definition.

**Trusted** — adding to the scratch `CODEX_HOME` config only:

```toml
[projects."/home/dgazineu/.claude/jobs/08526afa/tmp/codexspike/proj"]
trust_level = "trusted"
```

```
--- mcp list ---
    "name": "globalonly",   "command": "global-only-server"
    "name": "projectonly",  "command": "project-only-server", args ["--from-project-layer"]
    "name": "shared",       "command": "PROJECT_LAYER_WINS"
--- env ---
SPIKE_GLOBAL_ENV=from_global_layer
SPIKE_PROJECT_ENV=from_project_layer
--- doctor ---
"mcp servers": "3",
```

**Measured. Both land on the same side of the line: `mcp_servers` and
`shell_environment_policy` are equally trust-gated.** Neither is honored from an
untrusted project directory; both activate on the trust entry alone, with no
other change. This resolves the "unevenly" language in the existing spike for
these two keys specifically — the unevenness is between *configuration keys* and
*skills*, not within the configuration keys. `shell_environment_policy` was
previously only assumed to be trust-gated by grouping; it is now measured.

The trust entry itself must live in the developer's own `CODEX_HOME` config.
That reconfirms the existing spike's finding 4 and is the part niwa cannot do
from inside an instance.

## D. Precedence vs the Developer's Own Config

The naive expectations are "project wins" or "developer wins". **Neither is what
happens.** The layers are merged **recursively, field by field**, with the
project layer winning only on the individual keys it declares.

Developer's own `CODEX_HOME` config:

```toml
[mcp_servers.shared]
command = "GLOBAL_LAYER_WINS"
args = ["--global-args-should-they-survive"]
cwd = "/tmp/global-cwd"
startup_timeout_sec = 11
env = { SPIKE_FROM_GLOBAL = "g" }

[shell_environment_policy]
inherit = "core"
set = { SPIKE_COLLIDE = "global_value", SPIKE_GLOBAL_ENV = "from_global_layer" }
```

Project `.codex/config.toml`:

```toml
[mcp_servers.shared]
command = "PROJECT_LAYER_WINS"
env = { SPIKE_FROM_PROJECT = "p" }

[shell_environment_policy]
set = { SPIKE_COLLIDE = "project_value", SPIKE_PROJECT_ENV = "from_project_layer" }
```

Resolved (`codex mcp list --json`, trusted):

```json
{
  "name": "shared",
  "transport": {
    "type": "stdio",
    "command": "PROJECT_LAYER_WINS",
    "args": ["--global-args-should-they-survive"],
    "env": { "SPIKE_FROM_PROJECT": "p", "SPIKE_FROM_GLOBAL": "g" },
    "cwd": "/tmp/global-cwd"
  },
  "startup_timeout_sec": 11.0
}
```

**Measured.** The result is a hybrid that neither layer describes: the project's
`command` runs with the developer's `args`, the developer's `cwd`, the
developer's `startup_timeout_sec`, and an `env` table merged key-by-key from
both. The merge is recursive all the way into sub-tables.

For niwa this is worse than either clean outcome. If a workspace emits
`[mcp_servers.github]` and the developer already has a `github` server, the
session launches niwa's command with the developer's flags — a configuration
neither party wrote and neither can debug from their own file. niwa cannot
silently clobber the developer's setup, but it also cannot avoid corrupting it
by name collision alone. **Name collision is the hazard, and it is only
avoidable by choosing names that cannot collide, or by detecting the collision
before writing.** Detection is possible non-interactively: `codex mcp list
--json` from outside the project enumerates exactly what the developer already
has.

Environment policy merges the same way:

```
SPIKE_COLLIDE=project_value          <- project wins the key collision
SPIKE_GLOBAL_ENV=from_global_layer   <- both layers' set entries coexist
SPIKE_PROJECT_ENV=from_project_layer
```

and the surrounding fields carry over — the developer's `inherit = "core"`
stayed in force (only the six core names plus the `set` values were present),
because the project layer declared `set` but not `inherit`. So a developer who
has narrowed their environment narrows niwa's delivery too, except for the
explicit `set` names, which survive because `set` is applied after the inherit
base is chosen. A developer with `include_only`, per section B, would drop them.

## E. What `.mcp.json` Can Say That Codex Cannot

niwa distributes `.mcp.json` verbatim today and never parses it
(`docs/guides/file-distribution.md`, `docs/briefs/BRIEF-mcp-root-instance-distribution.md`).
Any translator has to account for these. The Codex column below is **measured**;
the Claude Code column is **documented** from that product's published
`.mcp.json` contract and was **not** measured in this spike — verify it
independently before building on it.

| Claude `.mcp.json` construct | Codex equivalent | Verdict |
|---|---|---|
| `command`, `args`, `env` (stdio) | `command`, `args`, `env` | Maps cleanly |
| `"type": "http"` + `url` | `url` | Maps cleanly (Codex calls it `streamable_http`) |
| `headers` on an HTTP server | `http_headers` | Maps cleanly |
| `"type": "sse"` + `url` | **nothing** | **Unmappable and dangerous.** Codex has no SSE transport, and it does not reject `type = "sse"` — it silently serves the URL as streamable HTTP. A dropped-to-different-protocol server is a live failure, not a missing one. |
| `${VAR}` / `${VAR:-default}` expansion in any field | **nothing** | **Unmappable.** Measured: Codex performs no interpolation anywhere, in `mcp_servers` or `shell_environment_policy`. niwa must fully resolve values before writing, and a `.mcp.json` relying on expansion cannot be copied through. |
| Per-server tool allow/deny | `enabled_tools` / `disabled_tools` | Codex has *more* here than a plain `.mcp.json` carries |
| — | `env_vars`, `cwd`, `startup_timeout_sec`, `tool_timeout_sec`, `bearer_token_env_var`, `env_http_headers`, `oauth.client_id`, `oauth_resource`, `enabled` | Codex-only; no `.mcp.json` source to populate them |

The structural mismatches beyond field names:

- **Codex has no per-server failure isolation.** A `.mcp.json` server that
  translates to something Codex rejects takes down the whole Codex config for
  that directory, including servers that translated fine. niwa must validate
  before writing, not after.
- **Unknown fields vanish silently** on the Codex side, and `--strict-config` is
  unavailable on the `mcp` subcommand, so a mistranslated field name produces a
  server that loads and simply lacks the behavior.
- **Name collision corrupts rather than overrides** (section D). Claude's
  `.mcp.json` at a project has no equivalent hazard against a user-scope server
  of the same name in the same way, because Codex's field-level merge has no
  counterpart there.

## F. Approval and Sandbox Posture at the Project Layer

### Both keys work from the project layer, and both are trust-gated

Fixture: `…/codexspike/f/proj`, a scratch `git init` directory, with a scratch
`CODEX_HOME` whose only content is the trust entry (or nothing). Observable is
`codex doctor --json`, which reports the resolved posture directly. Four runs,
one variable changed at a time:

```
=== 1. no project config at all (baseline defaults), trusted ===
  approvalpolicy:OnRequest, filesystemsandbox:restricted, networksandbox:restricted
=== 2. project sets approval_policy=never sandbox_mode=danger-full-access, TRUSTED ===
  approvalpolicy:Never,     filesystemsandbox:unrestricted, networksandbox:enabled
=== 3. same project config, UNTRUSTED (no projects entry) ===
  approvalpolicy:OnRequest, filesystemsandbox:restricted, networksandbox:restricted
=== 4. trusted, plus notify/profile/openai_base_url alongside ===
  approvalpolicy:Never,     filesystemsandbox:unrestricted, networksandbox:enabled
```

**Measured. `approval_policy` and `sandbox_mode` are NOT on the project-layer
denylist.** Both take effect from `.codex/config.toml`, and both revert to the
defaults (`on-request` / restricted) the moment the trust entry is removed. They
sit on exactly the same side of the trust line as `mcp_servers` and
`shell_environment_policy`.

This settles the matrix's hard UNRESOLVED row affirmatively: a workspace **can**
stop a Codex session prompting for approval on work it has already been trusted
to do — by writing `approval_policy` into the project layer — but only for a
directory that already carries a trust entry in the developer's own config. The
capability and the trust bootstrap are the same dependency, not two.

Worth stating plainly for whoever designs this: writing `approval_policy =
"never"` and `sandbox_mode = "danger-full-access"` into a project layer removes
the approval prompt *and* the filesystem and network sandbox together. Measured
above: `filesystem sandbox` went `restricted` → `unrestricted` and `network
sandbox` `restricted` → `enabled` from that one line. These are two independent
keys and should be treated as two independent decisions.

### The binary enumerates its own denylist

Codex reports ignored project-local keys, and `codex doctor --json` surfaces the
message as a `startup warning` row (it does **not** appear on `codex mcp list`,
`codex features list`, `codex debug models`, or `codex doctor --summary` — I
checked all four):

```
"startup warning": "Ignored unsupported project-local config keys in
/home/…/f/proj/.codex/config.toml: openai_base_url, model_provider, notify,
profile, experimental_realtime_ws_base_url. If you want these settings to apply,
manually set them in your user-level config.toml."
```

This is the authoritative instrument, and it is self-service: put any key in a
project config, run `codex doctor --json`, read whether it is named.

I ran three batches of candidate keys through it. **Measured denylist, eight
keys confirmed:**

| Ignored project-local key | Category |
|---|---|
| `openai_base_url` | provider endpoint |
| `chatgpt_base_url` | provider endpoint |
| `model_provider` | provider selection |
| `model_providers` | provider table |
| `notify` | external command execution |
| `profile` | config profile selection |
| `experimental_realtime_ws_base_url` | provider endpoint |
| `experimental_realtime_webrtc_call_base_url` | provider endpoint |

Confirmed effective: `openai_base_url = "https://example.invalid"` in a trusted
project layer left the resolved endpoint at `https://api.openai.com/v1`
(`codex doctor --json`: `"openai API base URL": "https://api.openai.com/v1
reachable (HTTP 404)"`). The denylist is real, not advisory.

**Measured NOT on the denylist** — every one of these was accepted from the
project layer with no warning: `approval_policy`, `sandbox_mode`,
`project_doc_max_bytes`, `project_doc_fallback_filenames`, `project_root_markers`,
`model`, `model_reasoning_effort`, `instructions`, `developer_instructions`,
`model_instructions_file`, `compact_prompt`, `model_catalog_json`,
`tool_output_token_limit`, `allow_login_shell`, `forced_chatgpt_workspace_id`,
`forced_login_method`, `cli_auth_credentials_store`, `mcp_oauth_credentials_store`,
`mcp_oauth_callback_port`, `mcp_oauth_callback_url`, `sqlite_home`, `log_dir`,
`check_for_update_on_startup`, `experimental_thread_config_endpoint`,
`experimental_realtime_ws_model`, `experimental_realtime_ws_backend_prompt`,
`experimental_realtime_ws_startup_context`,
`experimental_realtime_start_instructions`, `oss_provider`,
`preferred_auth_method`, `history`, `analytics`, `service_tier`,
`hide_agent_reasoning`, `suppress_unstable_features_warning`, `disable_paste_burst`,
`projects`.

Two of those deserve flagging rather than burying in a list:

- **`model_instructions_file` and `model_catalog_json` are actively read from
  the project layer.** Both produced real filesystem errors when pointed at
  missing paths (`failed to read model instructions file /tmp/fake-instructions.md`;
  `failed to parse model_catalog_json path … as JSON: missing field 'models'`),
  which is proof the project layer drives a file read, not merely a parse.
- **`project_root_markers` is not on the denylist**, which contradicts the
  existing spike's claim that project-root marker configuration cannot be
  carried at this layer. It is *accepted*. Whether it is *effective* is a
  different question — the marker list is what finds the project root in the
  first place, so a value declared inside the root it would have to find is
  plausibly inert. I measured acceptance, not effect. **Untested: whether a
  project-layer `project_root_markers` changes discovery.**

### On the "eleven denylisted keys" figure

The existing spike says eleven; I measured eight. I am not claiming the spike is
wrong. My enumeration covered roughly fifty keys I chose, not the whole config
surface, so three more may sit among keys I never submitted. **The count is
unresolved; the mechanism to resolve it is not** — anyone can finish the list by
feeding the remaining keys through the `codex doctor --json` startup warning.
Note also that denylisted keys are still type-checked before being ignored:
`forced_login_method = "apikey"` failed the whole config load with
`unknown variant 'apikey', expected 'chatgpt' or 'api'`, and
`experimental_thread_store_endpoint` is rejected outright with
`is no longer supported; remove it from config.toml`. So a denylisted key is not
inert — a malformed one still bricks the config per section A.

## G. Does a Git Worktree's `.git` FILE Satisfy the Project-Root Marker?

**Yes. Measured.**

In a linked worktree `.git` is a regular file holding a pointer, confirmed on
the fixture:

```
--- main repo .git ---
drwxrwxr-x 9 dgazineu dgazineu 4096 … /codexspike/g/main/.git
--- worktree .git ---
-rw-rw-r-- 1 dgazineu dgazineu   85 … /codexspike/g/wt/.git
--- worktree .git contents ---
gitdir: /home/…/codexspike/g/main/.git/worktrees/wt
```

The obvious test — put `AGENTS.md` in the worktree and see if it is read — does
**not** discriminate, for the exact reason the existing spike warns about: a
directory with no marker anywhere above it yields a walk of one directory, which
also reads the cwd's own file. So I placed `AGENTS.md` **only at the worktree
root** and ran from `wt/sub/deeper`, two levels down. The file can only be
reached if the walk found a root, and the only candidate root marker is the
`.git` file.

First, the confound check — no ancestor of the fixture carries a marker:

```
=== confound check: any .git ABOVE the worktree? ===
  (nothing listed == no ancestor marker)
```

Then the three-way result:

```
=== A. run from $G/wt/sub/deeper -- root-only AGENTS.md via the .git FILE ===
  hits for worktree-root marker: 1
=== B. control: same depth inside the MAIN repo (real .git directory) ===
  hits for main-repo-root marker: 1
=== C. negative control: a deep dir with NO marker anywhere above ===
  hits for no-marker-root file: 0
```

The negative control is what makes A trustworthy: an identical layout without a
marker returns zero, so the hit in A is the marker working and not the walk
picking the file up some other way. Raw evidence from A:

```
$ codex debug prompt-input          # cwd = .../g/wt/sub/deeper
": "# AGENTS.md instructions for /home/…/codexspike/g/wt/sub/deeper\n\n
<INSTRUCTIONS>\nWORKTREE_CONTEXT_MARKER_SPIKE\n\n</INSTRUCTIONS>"
```

**The matrix's worktree-context row stays Implemented.** Codex treats a `.git`
file exactly as it treats a `.git` directory for project-root purposes, so a
session rooted anywhere inside a linked worktree discovers context at that
worktree's root. The acceptance scenario built on this is satisfiable.

One thing I did not test: whether the *main* repository's context is also
reachable from inside the worktree. It should not be — the worktree root is the
project root and the walk never goes above it — but that follows from the
existing spike's finding 1 rather than from anything I measured here.
**Untested.**

## H. Is `shell_environment_policy.set` Trust-Gated?

**Yes. Measured, with the trust entry as the only variable, and reversibly.**

Section C already established this alongside `mcp_servers`, but that fixture had
a global config carrying its own servers and policy, so I re-ran it in isolation:
a scratch project whose `.codex/config.toml` declares nothing but
`shell_environment_policy.set`, and a scratch `CODEX_HOME` that is empty except
for the trust entry being added and removed.

```
=== project .codex/config.toml ===
[shell_environment_policy]
set = { SPIKE_PROJECT_ENV = "from_project_layer" }

=== 1. UNTRUSTED (CODEX_HOME config.toml is empty) ===
  ABSENT
=== 2. TRUSTED (only change: add the projects trust entry) ===
SPIKE_PROJECT_ENV=from_project_layer
=== 3. back to UNTRUSTED (revert the entry, nothing else) ===
  ABSENT
```

Absent → present → absent, toggled by nothing but
`[projects."<path>"] trust_level = "trusted"`. **The row carries a
`Requires: DirectoryTrust` edge on measured grounds, not by analogy to the byte
budget.**

A further control shows the gating is coarser than "filter the keys": when the
directory is untrusted the project config is **not parsed at all**. Adding a
denylisted key (`notify`) to the untrusted project layer produced no startup
warning, where the same key in a trusted layer always does:

```
=== control: does the project layer even get READ when untrusted? ===
  (no warning -- project layer not parsed at all)
```

So an untrusted project layer is skipped wholesale rather than having its
individual keys evaluated and dropped. That is worth knowing for diagnosis: the
absence of a Codex warning about a project config is not evidence the file is
well-formed, only that it was never read.

## Measured vs Documented vs Untested (explicit table)

| Claim | Status |
|---|---|
| `codex-cli 0.147.0` is the binary under test | Measured |
| `CODEX_HOME` redirects all config/state | Measured |
| `[mcp_servers.<name>]` table shape and all 16 fields | Measured (binary-generated, then round-tripped from hand-written TOML) |
| Two transports only, `stdio` / `streamable_http`; selected by `command` vs `url` | Measured |
| `type` key ignored; `type="sse"` becomes streamable_http | Measured |
| Unknown `mcp_servers` fields silently ignored | Measured |
| `--strict-config` unavailable on `codex mcp` | Measured |
| One malformed server fails the entire config load | Measured |
| `shell_environment_policy` field names | Documented (binary's compiled serde field list) |
| `inherit` enum `core`/`all`/`none`; `core` = HOME/LANG/LOGNAME/PATH/SHELL/USER | Measured |
| `inherit` defaults to `all` | Measured |
| `set` additive; overrides inherited names; values must be strings | Measured |
| Order: exclude → set → include_only; `include_only` drops `set` values | Measured |
| No interpolation in `set` values | Measured |
| `ignore_default_excludes` defaults to `true`; defaults are `*KEY*`/`*TOKEN*` only | Measured via `codex sandbox`; **untested for the in-session shell tool** |
| `filters` is a map keyed `include`/`exclude` | Measured (shape only); semantics untested |
| `experimental_use_profile` is a bool | Measured (type only); semantics untested |
| Both `mcp_servers` and `shell_environment_policy` are trust-gated | Measured |
| Trust entry is `[projects."<abs path>"] trust_level = "trusted"` in the developer's own config | Measured |
| Layers merge recursively field-by-field; project wins per-key only | Measured |
| `set` key collision resolves to the project layer | Measured |
| Claude `.mcp.json` field set and `${VAR}` expansion | Documented (that product's contract); not measured here |
| `TMPDIR`/`TMP`/`LC_ALL` appear near `core` in the binary but not in measured `core` output | Measured absent; cause untested |
| **F** — `approval_policy` settable from the project layer, and trust-gated | Measured |
| **F** — `sandbox_mode` settable from the project layer, and trust-gated | Measured |
| **F** — `sandbox_mode = "danger-full-access"` disables filesystem *and* network sandboxing together | Measured |
| **F** — `codex doctor --json` `startup warning` row enumerates ignored project-local keys | Measured |
| **F** — the warning appears on no other cheap subcommand (`mcp list`, `features list`, `debug models`, `doctor --summary`) | Measured |
| **F** — eight denylisted keys: `openai_base_url`, `chatgpt_base_url`, `model_provider`, `model_providers`, `notify`, `profile`, `experimental_realtime_ws_base_url`, `experimental_realtime_webrtc_call_base_url` | Measured |
| **F** — `openai_base_url` from a trusted project layer does not move the resolved endpoint | Measured |
| **F** — ~37 further keys accepted at the project layer (list in section F) | Measured (acceptance only) |
| **F** — `model_instructions_file` / `model_catalog_json` drive a real file read from the project layer | Measured |
| **F** — `project_root_markers` accepted at the project layer | Measured (acceptance); **effect untested** |
| **F** — denylisted keys are still type-checked, and a malformed one still fails the whole load | Measured |
| **F** — the denylist is exactly eleven keys | **Unresolved** — 8 measured across ~50 keys submitted; not an exhaustive sweep |
| **G** — a linked worktree's `.git` is a regular file with a `gitdir:` pointer | Measured |
| **G** — that `.git` FILE satisfies the project-root marker; root context found from 2 levels deep | Measured, with passing positive and negative controls |
| **G** — whether the main repo's context is also reachable from inside the worktree | Untested (follows from prior spike's finding 1, not measured here) |
| **H** — `shell_environment_policy.set` is trust-gated, toggled reversibly by the trust entry alone | Measured |
| **H** — an untrusted project config is not parsed at all, not filtered key-by-key | Measured |

## Blocked Measurements

- **In-session shell-tool environment.** Everything in section B is measured
  through `codex sandbox`. Confirming the live session's shell tool resolves the
  same policy — especially the `ignore_default_excludes` default — needs a
  session that calls the model, which the no-long-lived-session and
  no-network-secrets constraints exclude. What would close it: a bounded
  `codex exec` run with scratch credentials in a throwaway `CODEX_HOME`, asking
  the model to run `env`, in an environment with no real secrets present.
- **Default sandbox backend.** `bwrap` cannot start in this container, so all
  environment measurements used `use_legacy_landlock`. If the two backends
  construct the child environment differently, the counts could shift. Would
  need a host where `bwrap` can create a loopback device.
- **`filters` and `experimental_use_profile` semantics.** Types and accepted
  variants are measured; what they actually do is not. They were out of the
  question's scope and probing them properly needs upstream source.
- **The developer's real `~/.codex/config.toml` was neither read nor written**,
  by choice. So this spike says nothing about what the developer currently has
  configured — including whether they already use `include_only` (which would
  silently defeat `set` delivery) or already own server names niwa might emit.
  `codex mcp list --json` run against their real `CODEX_HOME` would answer both;
  that is a read-only command and safe to run, but it is theirs to run.
- **The complete project-layer denylist (F).** I measured eight keys across
  roughly fifty submitted; the existing spike says eleven. Finishing the sweep
  needs no new technique, only patience — feed each remaining top-level config
  key through a trusted project layer and read the `startup warning` row of
  `codex doctor --json`. The obstacle is that denylisted keys are still
  type-checked, so each batch has to carry type-valid values and errors have to
  be cleared one at a time.
- **Whether `project_root_markers` is *effective* from the project layer (F).**
  Measured accepted, but the marker list is what locates the project root, so a
  value declared inside that root may be read too late to matter. Testing it
  needs a fixture with a non-`.git` marker and a way to distinguish "root found
  by the new marker" from "root found by `.git`".
- **Whether the main repository's context is reachable from inside a linked
  worktree (G).** Not measured. Expected no, from the prior spike's bounded
  downward walk, but that is inference.
- **`-c 'projects={}'` did not retract trust** in an attempted control, so the
  untrusted/trusted comparison rests on two separately-written config files
  rather than one toggled flag. The two measurements are clean; the toggle just
  is not a usable instrument.

## Summary

The `mcp_servers` schema is now pinned exactly — `[mcp_servers.<name>]` with
`command`/`args`/`env`/`env_vars`/`cwd` for stdio or `url`/`http_headers`/
`env_http_headers`/`bearer_token_env_var`/`oauth.*` for streamable HTTP, plus
`enabled`, the two timeouts, and tool allow/deny lists — measured by having
`codex mcp add` generate the TOML and then round-tripping every field, and
`shell_environment_policy` resolves as inherit (default `all`) → `exclude` →
`set` (additive, overriding, no interpolation, strings only) → `include_only`
as a final whitelist that silently drops `set` values.

Both keys are equally trust-gated: measured absent from an untrusted project
directory and active after nothing but a `[projects."…"] trust_level =
"trusted"` entry in the developer's own config, which settles the question the
prior spike left open for `shell_environment_policy`. The precedence answer is
neither "project wins" nor "developer wins" — layers merge recursively field by
field, so a name collision on an MCP server yields a hybrid running niwa's
`command` with the developer's `args` and `cwd`, and one malformed entry fails
the entire config load rather than just that server.

Two findings deserve to reach the design directly: Codex has no SSE transport
and silently serves a `type = "sse"` server as streamable HTTP, and it performs
no `${VAR}` interpolation anywhere, so any `.mcp.json` relying on either cannot
be translated and must be reported rather than dropped.

The three later measurements all resolve affirmatively. `approval_policy` and
`sandbox_mode` are **not** denylisted — both take effect from a project layer and
both revert when trust is removed, so a workspace can suppress approval prompts,
though the same line that does it also disables the filesystem and network
sandbox and those should be separate decisions; the denylist itself turns out to
be self-enumerating through the `startup warning` row of `codex doctor --json`,
which named eight keys (all provider endpoints, `notify`, and `profile`) across
the ~50 I submitted, leaving the spike's "eleven" figure unresolved but trivially
completable. A linked git worktree's `.git` **file** does satisfy the
project-root marker — proven with a root-only context file read from two levels
deep, against a passing negative control — so the worktree-context row stays
Implemented. And `shell_environment_policy.set` is trust-gated on measured
grounds rather than by analogy, toggling absent → present → absent with the trust
entry as the only variable; the gate is wholesale, since an untrusted project
config is never parsed at all, which means a missing Codex warning about such a
file says nothing about whether it is well-formed.
