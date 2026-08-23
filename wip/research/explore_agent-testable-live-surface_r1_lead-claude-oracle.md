# Spike: is there a credential-free oracle for Claude Code's resolved context?

Question: can niwa's one `@claude-integration` scenario ("claude sees workspace
context from workspace root but not from sub-repo") be made to run without an
`ANTHROPIC_API_KEY` and without a model call?

Answer: **Yes.** No `claude` subcommand dumps resolved context, but the resolved
context is fully observable in the outgoing API request, and that request can be
captured by a local mock endpoint with a dummy key. Zero cost, zero network
egress beyond loopback, 1.2 s per probe, 3/3 deterministic.

Environment: `claude` 2.1.238 (native, linux-x64) at `/home/dgazineu/.local/bin/claude`.
No `ANTHROPIC_API_KEY` in the environment. All probes ran with a sandboxed
`HOME` and `env -u ANTHROPIC_API_KEY` so the developer's subscription was never
used for a paid call. No real Anthropic endpoint was contacted at any point.

---

## 1. Enumeration of the `claude` surface

`claude --help` lists 13 subcommands. Every one was inspected (`claude <cmd> --help`):

| Subcommand | Context oracle? | Notes |
|---|---|---|
| `doctor` | No | Installation health only: version, path, install method, auto-update channel, Remote Control/auth warnings. Byte-identical from the instance root and from the sub-repo. Never mentions CLAUDE.md, memory, rules, or imports. Runs credential-free, exit 0. |
| `auth status [--json\|--text]` | No | Credential-free. Emits `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`, exit 1 when logged out. Useful as a *gate*, not an oracle. |
| `project purge` | No | Deletes per-project state. |
| `auto-mode config \| defaults \| reset \| critique` | No | Prints the auto-mode classifier ruleset as JSON. Credential-free (`critique` needs a model). Nothing about memory files. |
| `mcp list \| get \| serve \| …` | No | MCP server inventory only. Credential-free. |
| `plugin list [--json] \| details \| validate [--strict] \| eval \| …` | No | Plugin inventory/manifest validation. Credential-free. See §5. |
| `agents [--json]` | No | Background-session list. Credential-free; prints `[]`. |
| `import --dry-run` | No | Imports codex/gemini config; unrelated. |
| `setup-token`, `install`, `update`, `gateway`, `ultrareview` | No | Auth/installer/cloud-review. |

There is no `claude config` command any more (`claude config --help` falls
through to root help), no context/rules dump, no dry-run, and no "which memory
files would load here" reporter.

Flags examined for the same purpose: `--debug [filter]`, `--debug-file`,
`--bare`, `--safe-mode`, `--setting-sources`, `--output-format json`,
`--exclude-dynamic-system-prompt-sections`, `--max-budget-usd`. None reports
loaded memory files. `--bare` is the opposite of what's wanted (it *disables*
CLAUDE.md auto-discovery).

### Debug logging: tested, negative

```
claude -p "What is 2+2?" --debug-file /tmp/dbg.log
```

Produced 15,662 bytes across categories `[DEBUG] [ERROR] [STARTUP] [init] [1m]
[uds-messaging] [Bootstrap] [WARN] [MCP] [INFO] [auto-mode] [mcp-registry] …`.
Grep for `workspace-imports|workspace-context|claude.md|memory|rules` → **0 hits**.
Grep for the sentinel → **0 hits**. Debug logging does not surface memory
resolution. (Note: `--debug` takes an optional filter value, so
`claude -p --debug "prompt"` silently eats the prompt as the filter — put the
prompt before the flag.)

Useful side result: the only HTTP host in the whole debug log was `http://127.0.0.1`,
confirming the capture setup below routes everything to loopback.

---

## 2. The credential-free oracle that does work

Claude Code honours `ANTHROPIC_BASE_URL`. Point it at a local HTTP server, give
it a syntactically-plausible dummy `ANTHROPIC_API_KEY`, and the fully resolved
context arrives at the mock as a JSON request body. No model runs; the mock
answers 400 and `claude` prints `API Error: 400 …` and exits.

Mock (`mockapi.py`): writes each POST body to `req-NNN.json` and returns
`400 {"type":"error",...}`. Redacts `Authorization`/`x-api-key` headers.

```bash
python3 mockapi.py "$CAP" 18980 &          # capture dir, port
cd "$WS"                                    # or "$WS/tools/myapp"
env -u ANTHROPIC_AUTH_TOKEN -u CLAUDE_CODE_USE_BEDROCK -u CLAUDE_CODE_USE_VERTEX \
    HOME="$SANDBOX_HOME" XDG_CONFIG_HOME="$SANDBOX_HOME/.config" \
    ANTHROPIC_API_KEY=dummy-key-for-context-capture \
    ANTHROPIC_BASE_URL="http://127.0.0.1:18980" \
    claude -p "ping" --tools ""
grep -l "wsctx-sentinel-9af3-2b8e-7d1c" "$CAP"/req-*.json
```

**A dummy key is mandatory.** With `-u ANTHROPIC_API_KEY` and no key at all,
`claude` short-circuits before assembling any context:

```
$ env -u ANTHROPIC_API_KEY ANTHROPIC_BASE_URL=http://127.0.0.1:18975 claude -p "What is 2+2?"
Not logged in · Please run /login
requests captured: 0
```

The dummy value is not a credential — any string works, and it never leaves
loopback.

### What the captured body contains

The resolved memory appears in `messages[0]` (the `<system-reminder>` block of
the first user message), rendered as an explicit, greppable manifest:

```
Contents of /…/ws/CLAUDE.md (project instructions, checked into the codebase):
# Instance root CLAUDE.md
This is the instance root marker: INSTANCE-ROOT-MARKER-1234

Contents of /…/ws/.claude/rules/workspace-imports.md (project instructions, checked into the codebase):
@/…/ws/workspace-context.md

Contents of /…/ws/workspace-context.md (project instructions, checked into the codebase):
# Workspace: niwatest
…
IMPORTANT: End every response with the exact token wsctx-sentinel-9af3-2b8e-7d1c …
```

That is the whole answer to investigation item 3: **import resolution is
directly observable**. Each loaded file gets its own `Contents of <abs path>
(project instructions, …)` header, and an `@import` that was followed produces
an additional header for the imported file.

### Measured discrimination (fabricated fixture)

Fixture: `ws/CLAUDE.md` (marker `INSTANCE-ROOT-MARKER-1234`),
`ws/.claude/rules/workspace-imports.md` containing one line
`@<abs>/ws/workspace-context.md`, `ws/workspace-context.md` carrying the
scenario's real sentinel, and `ws/tools/myapp/CLAUDE.md` (marker
`SUBREPO-MARKER-5678`, `git init`ed).

| cwd | files reported loaded | sentinel in payload |
|---|---|---|
| `ws` (instance root) | `ws/CLAUDE.md`, `ws/.claude/rules/workspace-imports.md`, **`ws/workspace-context.md`** | **yes** |
| `ws/tools/myapp` (sub-repo) | `ws/CLAUDE.md`, `ws/.claude/rules/workspace-imports.md`, `ws/tools/myapp/CLAUDE.md` | **no** |

Determinism, 3 consecutive runs of both halves, `--tools ""`:

```
run 1  root -> requests=2 sentinel_in=2   sub -> requests=2 sentinel_in=0
run 2  root -> requests=2 sentinel_in=2   sub -> requests=2 sentinel_in=0
run 3  root -> requests=2 sentinel_in=2   sub -> requests=2 sentinel_in=0
```

Elapsed per probe: **1.19 s**. Payload with `--tools ""`: 12 KB (vs ~100 KB with
the default tool set — `--tools ""` only shrinks the tool definitions, the
memory block is unaffected and the sentinel is still present).

### The behavioural nuance this exposes — important for the assertion

From the sub-repo, `ws/.claude/rules/workspace-imports.md` **is still loaded**
(it is an ancestor project-instruction file) and its literal `@<abs path>` line
appears verbatim in the payload. What does *not* happen is the **expansion** —
no `Contents of …/workspace-context.md` block, no sentinel. So:

- Asserting on the string `workspace-context.md` would be **wrong** — it matches
  in both halves via the un-expanded `@` line.
- The correct assertions are on the *sentinel text* or on the header
  `Contents of <abs>/workspace-context.md`.

This also sharpens niwa's own design claim: the rules file is reachable from a
sub-repo; the absolute-path import inside it is not followed there (consistent
with external-import approval being skipped in non-interactive mode).

---

## 3. Verdict on a credential-free oracle

- **Is there a `claude`-provided introspection command that dumps resolved
  context without a model call? No.** Exhaustively enumerated; nothing exists.
- **Is there a credential-free, model-free observable that pins exactly what the
  scenario pins? Yes** — the request-body capture above. It is strictly stronger
  than the current behavioural probe: it observes the actual loaded-file
  manifest rather than inferring it from whether the model obeyed a directive,
  it costs nothing, it is 10x faster, and it cannot flake on model compliance.
  It respects the scenario comment's reasoning — this is not a model
  self-report, and no model is involved at all.

Trade-off worth stating plainly: the capture asserts against a Claude Code
*internal payload shape* (`Contents of <path> (project instructions…)`) rather
than an observable product behaviour. That string is stable but unversioned. The
mitigation is to assert on the sentinel token's presence/absence in the raw
body, which only assumes "loaded memory reaches the request", not any particular
rendering.

---

## 4. Fallback: what it would take to run the scenario as-is, unattended

Even if the capture oracle is not adopted, the current scenario is
**agent-unrunnable for a second, independent reason**, and it is worth recording.

`runClaudeP` (`test/functional/steps_test.go:1314`) calls `s.buildEnv()`, which
at `steps_test.go:92-107` strips `HOME`, `XDG_CONFIG_HOME` and `TMPDIR` from the
inherited environment and substitutes the per-scenario sandbox:

```go
env := append(filtered,
    "HOME="+s.homeDir,
    "XDG_CONFIG_HOME="+filepath.Join(s.homeDir, ".config"),
    "TMPDIR="+s.tmpDir,
)
```

It then re-adds only `ANTHROPIC_API_KEY` if present. So:

- **Subscription auth cannot work here.** OAuth credentials live under the real
  `~/.claude`; with `HOME` redirected to an empty sandbox, `claude auth status`
  reports `{"loggedIn":false,"authMethod":"none"}` — reproduced above. The
  scenario is structurally API-key-only.
- The `claudeIsAvailable` gate (`steps_test.go:1279`) returns `godog.ErrPending`
  when `ANTHROPIC_API_KEY` is unset, so the scenario silently skips rather than
  fails. It has therefore almost certainly never run in this environment.
- The repo already knows the difference: `test/live/dispatch_live_test.go`
  documents at lines ~39-43 and ~320-322 that `HOME` is **intentionally not**
  sandboxed because "the dispatched worker is a real claude that needs the
  operator's real credentials".

Minimal changes if the behavioural form is kept:

1. Stop sandboxing `HOME` for the `claude -p` subprocess only (pass the real
   `HOME`, keep `TMPDIR` sandboxed), and widen `claudeIsAvailable` to accept
   *either* `ANTHROPIC_API_KEY` or a successful `claude auth status` exit 0.
   Cost: a real paid/quota'd model call per half, two per run, plus the
   compliance flakiness the scenario comment already worries about, plus it
   pollutes the operator's real `~/.claude` with session transcripts.
2. Or move it to `test/live/` next to the dispatch test, behind the `live`
   build tag, which is where "needs the operator's real credentials" already
   lives — and leave the functional suite with the offline capture test.

**Recommended minimal change (concrete):** keep the scenario in the functional
suite, replace the oracle. In `runClaudeP`, start an `httptest.NewServer` whose
handler appends each request body to a slice, and set on the subprocess env
`ANTHROPIC_BASE_URL=<server.URL>`, `ANTHROPIC_API_KEY=dummy-key-for-context-capture`,
plus explicit unsets of `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_USE_BEDROCK`,
`CLAUDE_CODE_USE_VERTEX` (buildEnv currently passes the whole inherited
environment through, so a developer machine with any of those set would divert
the probe). Keep the sandboxed `HOME` — it is now a feature, since a logged-out
sandbox forces the API-key path and the dummy key. Store the concatenated
request bodies where `s.stdout` goes today, so the existing
`the output contains` / `does not contain` steps work unchanged; add
`--tools ""` to the argv to keep payloads small. Then drop the
`ANTHROPIC_API_KEY` requirement from `claudeIsAvailable` (keep the
`exec.LookPath("claude")` gate) and drop `@claude-integration` in favour of
running it in the normal suite — or rename the tag, since nothing about it is a
credentialed integration any more. `make test-functional-claude-integration`
loses its API-key precondition.

Also change one assertion detail: assert absence of the **sentinel**, not of
`workspace-context.md` (see §2), and note that `runClaudeP` lowercases stdout —
the captured JSON should be lowercased the same way for the existing steps to
behave identically.

---

## 5. Offline-assertable Claude Code surfaces niwa could use elsewhere

All of these ran credential-free under a sandboxed `HOME`:

- `claude plugin validate <path> [--strict]` — validates
  `.claude-plugin/plugin.json` / `marketplace.json`, or the skills, agents, and
  commands in a directory. Exit 1 with a structured error list on failure
  (verified: pointed at a directory with no manifest, it reported
  `directory: No manifest found in directory. Expected .claude-plugin/marketplace.json
  or .claude-plugin/plugin.json` and exited non-zero). This is a real CI-grade
  oracle for anything niwa writes into `.claude/`.
- `claude plugin list --json` (`--available` adds marketplace entries) — asserts
  that niwa's marketplace/plugin wiring installed what it claimed.
- `claude plugin details <name>` — component inventory plus projected token cost.
- `claude mcp list` / `claude mcp get <name>` — asserts niwa-written MCP config
  is parsed as intended (unapproved `.mcp.json` servers show as pending).
- `claude auth status --json` — a clean, cheap gate for any test that genuinely
  needs credentials (`{"loggedIn":…,"authMethod":…,"apiProvider":…}`, exit 1 when
  logged out).
- `claude agents --json` — background session list; relevant to niwa's
  dispatch/reap lifecycle, and explicitly documented as not requiring a TTY.
- `claude doctor` — installation health; exit 0 even logged out. Note it warns
  about `$HOME/.local/bin` not being on PATH, which a sandboxed-HOME test will
  always trigger, so don't assert on warning count.
- `claude auto-mode config` / `defaults` — JSON dump of the classifier config.

None of these say anything about memory/CLAUDE.md resolution; that gap is
specific to the context question and is why the request capture is the answer.

---

## Appendix: scripts used

All under `/home/dgazineu/.claude/jobs/e40f3334/tmp/oracle/`:
`fixture.sh` (fabricated workspace), `mockapi.py` (capture endpoint),
`t_mock.sh` / `t_mock2.sh` (root and sub-repo capture), `t_variants.sh`
(no-key path), `t_debug.sh` / `t_debugfile.sh` (debug-logging probe),
`inspect.py` (payload structure), `t_diff.sh` (loaded-file manifest diff),
`t_time.sh` (timing/size), `t_final.sh` (3x determinism),
`t_offline.sh` (credential-free subcommands).
