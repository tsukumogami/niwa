# Spike: what the real `codex` binary can be asked, for free — and whether niwa's Go model agrees with it

Measured 2026-08-21 against `codex-cli 0.147.0`
(`/home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex`), Linux x86_64,
Go 1.25.3. Every measurement below ran with an **isolated, initially empty
`CODEX_HOME`**, no `auth.json`, and (in the control run) with
`OPENAI_API_KEY`/`OPENAI_API_BASE`/`CHATGPT_TOKEN` stripped from the
environment. No sandbox namespace was needed and none was constructed. The
developer's real `~/.codex` was never read or written.

The Go model under test is niwa's `codexProjectRoot` / `codexContextChain` /
`codexContextBytes` in
`/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_instance_root_orientation-335badf2/public/niwa/.claude/worktrees/root-orientation/test/functional/codex_agent_steps_test.go`.

## How the comparison was run (this is the strong-evidence version)

The model was **not** reimplemented. The checkout was copied to a scratch
directory and a throwaway probe test was added to the real package, so the
comparison calls the actual unexported helpers the feature files are decided
against:

```
cp -a .../public/niwa/.claude/worktrees/root-orientation \
      /home/dgazineu/.claude/jobs/e40f3334/tmp/niwacopy
# + /home/dgazineu/.claude/jobs/e40f3334/tmp/niwacopy/test/functional/zz_contract_probe_test.go
cd /home/dgazineu/.claude/jobs/e40f3334/tmp/niwacopy
CONTRACT_FX=/home/dgazineu/.claude/jobs/e40f3334/tmp/fx \
  go test ./test/functional/ -run TestCodexModelContractProbe -v -count=1
```

The real checkout was not modified. Fixtures were built by
`/home/dgazineu/.claude/jobs/e40f3334/tmp/build_fixtures.sh` and
`build_fixtures2.sh`; the binary side by `run_binary.py` (which does
`subprocess.run([codex,"debug","prompt-input"], cwd=<fixture>, env={CODEX_HOME:
isolated})` and pulls the sentinel sequence out of the rendered JSON).

Sentinel order in the rendered prompt is the comparison key, because the
binary never names the files it selected — it emits one concatenated block.
That means selection is observable only through distinguishable content, which
is exactly what the fixtures provide.

---

## 1. The credential-free, sandbox-free observable surface

`codex debug prompt-input` is the workhorse. Verified: exit 0, 0.11 s wall,
empty `CODEX_HOME`, no auth, no network, no user namespace. It writes a small
amount of scaffolding into `CODEX_HOME` on first run (`installation_id`,
`.sandbox_migration`, `skills/.system/`) and nothing else.

It renders a JSON array of developer/user messages. Each of the following is
directly assertable, and the trust column was measured by running the identical
fixture under two isolated homes — one empty, one holding nothing but
`[projects."<abs path>"] trust_level = "trusted"`.

| Observable | Where it appears | Trust needed? |
|---|---|---|
| **Context file content and concatenation order** | `<INSTRUCTIONS>` block in a user message, under one heading `# AGENTS.md instructions for <canonical cwd>` | **No** |
| **Which directory the session believes it is in** | that same heading, plus `<environment_context><cwd>` | No |
| **Workspace roots** | `<environment_context><filesystem><workspace_roots>` | No |
| **Skill inventory, with per-skill source locator** | `<skills_instructions>` block, `- name: desc (file: /abs/path/SKILL.md)` | **No** — a project-layer `.codex/skills/<n>/SKILL.md` showed up under an empty home. Confirms spike finding 5's "skills are the lone exception". |
| **Effective sandbox posture** | `` `sandbox_mode` is `read-only` `` in the permissions block | **Yes** — flipped `read-only` → `workspace-write` on adding the trust stanza alone, with no auth and no session. This is a **zero-cost read of the trust registry's effect on posture** (spike finding 14) that costs no model turn and needs no working sandbox. |
| **Effective `project_doc_max_bytes`** | indirectly: whether the rendered block is cut at 32768 | **Yes** — see §3 W. |
| **Permission profile shape** | `<permission_profile type="managed"><file_system type="restricted">` | No (the type strings did not change with trust in this run; the posture line did) |
| **Multi-agent / spawn-agent prompt text** | a developer message | No |
| `-c` override effects on all of the above | same render | n/a — `-c project_root_markers=[".hg"]` visibly changed the walk |

Other credential-free subcommands:

- **`codex mcp list`** — trust **required**. `[mcp_servers.probe_server]` in a
  project `.codex/config.toml` printed `No MCP servers configured yet.` under
  an empty home and printed the server row (`probe_server /bin/true --probe
  enabled Unsupported`) under the trusted home. Exact reproduction of finding 5.
- **`codex doctor --json`** — runs and exits 0 without credentials, but it
  **does make network calls** (`network.provider_reachability`,
  `network.websocket_reachability`) and `auth.credentials` reports
  `fail: no Codex credentials were found`, which makes `overallStatus: fail`.
  Useful, non-network checks inside it: `config.load` (carries `CODEX_HOME`,
  `cwd`, resolved `model`, `mcp servers` count, the full enabled-feature list,
  and — per the spike — startup warnings for denylisted keys), `mcp.config`,
  `git.environment` (reports `.git entry: directory|file` and `repo root`),
  `sandbox.helpers`, `state.paths`. Schema is `{schemaVersion, generatedAt,
  overallStatus, codexVersion, checks: {<id>: {id, category, status, summary,
  details, remediation, durationMs}}}`.
- **`codex debug models`** — static JSON model catalogue, no auth, no network
  observed. Pins model slugs and reasoning levels. Nothing niwa asserts.
- **`codex plugin list`** / **`codex plugin marketplace list`** — run clean,
  print `No marketplace plugins found.` / `No plugin marketplaces in scope.`
  Assertable for "niwa registered no plugins / registered exactly this one".
- **`codex features list`** — enumerates feature flags and effective state.
- **`codex exec`** — **not** free. Needs auth to do anything past startup.
  (The spike's `-m bogus-model-xyz` trick gets the startup header for free, but
  it constructs a session and is not needed for anything in §3.)

**What `prompt-input` cannot see:** the actual file *paths* chosen — only the
merged bytes. Also nothing about approval prompts, sandbox construction, or
trust write-back side effects; those still need a live or a filesystem probe.

---

## 2. Fixture matrix — binary vs. niwa's Go model

26 fixtures. **23 agree, 3 disagree.**
Column "binary" is the sentinel sequence in the rendered `<INSTRUCTIONS>`
block; "model" is the sentinel sequence of `codexContextBytes(codexContextChain(cwd))`.

### Round 1 — the rules the model is meant to encode (17/17 agree)

| # | Fixture | Binary | Model | Verdict |
|---|---|---|---|---|
| A | no marker anywhere; `AGENTS.md` at cwd | `A-CWD` | root=`A`, chain=`[AGENTS.md]` | agree |
| B | no marker anywhere; cwd is a subdir, ancestor also has `AGENTS.md` | `B-CWD` only | root=`B/sub`, chain=`[AGENTS.md]` | agree — fallback root **is** the cwd |
| C | marker ancestor + child, both with files | `C-ROOT, C-CWD` | `[AGENTS.md, sub/AGENTS.md]` | agree |
| D | marker root + 3 nested dirs, all four with files | `D-ROOT, D-A, D-B, D-C` | 4-file chain, root-first | agree |
| E | gap directory in the middle of the walk | `E-ROOT, E-LEAF` | `[AGENTS.md, a/b/AGENTS.md]` | agree — gaps contribute nothing, no abort |
| F | `AGENTS.override.md` beside `AGENTS.md` | `F-OVERRIDE` | `[AGENTS.override.md]` | agree |
| G | **0-byte** `AGENTS.override.md` beside a real `AGENTS.md` | **no block at all** | chain=`[AGENTS.override.md]`, 1 byte, no sentinel | agree on substance (the empty file claims the slot and the native file is suppressed) |
| H | whitespace-only `AGENTS.override.md` beside a real `AGENTS.md` | **no block at all** | chain=`[AGENTS.override.md]`, 10 bytes, no sentinel | agree on substance; binary additionally trims-and-drops |
| I | **linked git worktree**, `.git` is a pointer FILE, cwd two dirs below | `I-WORKTREE-ROOT` only | root=`I/wt`, `[AGENTS.md]` | agree — and the main repo's `AGENTS.md` is **not** reachable, closing the spike finding 11 "untested" note |
| J | negative control for I: same depth, no marker, root file only | **nothing** | root=`J/x/y`, chain empty | agree |
| K | `AGENTS.override.md` is a **directory** | `K-NATIVE` | `IsRegular()` rejects it, falls to `AGENTS.md` | agree — the `IsRegular` check is load-bearing and correct |
| L | `AGENTS.override.md` is a **symlink** to a file elsewhere | `L-VIA-SYMLINK` | `os.Stat` follows, selects the override | agree |
| M2 | `.git` is a plain file holding **junk**, not a gitdir pointer | `M-ROOT, M-CWD` | `os.Lstat` succeeds → marker | agree — the marker test is a bare existence stat, **not** a git validity check |
| N | nested marker roots (outer `.git` above an inner `.git`) | `N-INNER, N-CWD` — outer never read | root=`N/inner` | agree — nearest marker wins |
| O | marker root has a file, cwd does not | `O-ROOT` | `[AGENTS.md]` | agree |
| P | lowercase `agents.md` under a marker root | **nothing** | chain empty | agree — candidate names are case-sensitive on Linux |
| Q | `CLAUDE.md` only, under a marker root | **nothing** | chain empty | agree (under default config; see §4) |

### Round 2 — fixtures built to break the model (6/9 agree)

| # | Fixture | Binary | Model | Verdict |
|---|---|---|---|---|
| **R** | **cwd reached through a symlinked path** (`R/link/here` → `R/real/sub`; `R/link` has its own `AGENTS.md`, `R/real` is a marker root with one) | `R-REALROOT, R-REALSUB` | root=`R/link/here`, chain=`[AGENTS.md]` → `R-REALSUB` only | **DISAGREE** |
| **S** | a chain file that exists but is **unreadable** (mode 000) at the cwd, readable file at the root | **no block at all** — the root's readable file is dropped too | chain=`[AGENTS.md, sub/AGENTS.md]`; `codexContextBytes` returns a hard error | **DISAGREE** |
| T | **dangling** symlink claims the override slot | `T-NATIVE` | `os.Stat` fails → falls through | agree |
| U | 12 levels below the marker root, every level with a file | all 13, in order | all 13, in order | agree — no depth cap |
| V | `.hg`, `.svn`, `.jj`, `package.json` at the would-be root | `V-CWD` only | root = cwd | agree — the **default marker list is `.git` alone** |
| **W** | over-budget chain: root file 30043 B, cwd file 3041 B, sum 33084 B | `W-ROOT-HEAD, W-ROOT-TAIL, W-CWD-HEAD` — **`W-CWD-TAIL` is gone** | all four sentinels | **DISAGREE** |
| X | same shape, 8084 B total (under budget) | all four | all four | agree |
| Y | `AGENTS.md` is a directory, nothing else | nothing | chain empty | agree |
| Z | marker root reached through a **symlinked ancestor** (`Z/alias` → `Z/repo`) | `Z-ROOT, Z-CWD` | root=`Z/alias`, `[AGENTS.md, a/AGENTS.md]` | agree — the symlink is transparent here because it sits *above* everything in the chain |

---

## 3. The three disagreements

### R — the model can be handed a path a real session can never be in

The binary's heading and its chain both report the **physical** path. Forced
two ways:

```
cd $FX/R/link/here          # bash keeps PWD logical
bash PWD=/…/R/link/here
physical=/…/R/real/sub
heading: # AGENTS.md instructions for /…/fx2/R/real/sub
sentinels: ['SENTINEL-R-REALROOT', 'SENTINEL-R-REALSUB']

cd $FX/R/real/sub
PWD=/…/R/link/here codex debug prompt-input
heading: # AGENTS.md instructions for /…/fx2/R/real/sub    # $PWD ignored entirely
```

So a Codex process's working directory is canonical by construction — the
kernel resolved it before the binary ever ran, and Codex does not consult
`$PWD`. `codexProjectRoot`/`codexContextChain` use `filepath.Abs`, which does
**not** resolve symlinks, so given a logical path they walk a tree no session
can ever be in. Here that flipped the project root itself (`R/link/here`
fallback-root vs. the real `R/real` marker root) and lost the root layer.

How much this matters today: `TestMain` already calls
`filepath.EvalSymlinks(processSandboxRoot)`, so the suite's sandbox is
canonical and no current fixture reaches a chain through a symlinked
*directory*. It is a latent trap, not a live bug — the guard lives in `TestMain`
for an unrelated reason (`GIT_CEILING_DIRECTORIES`) and nothing in
`codexContextChain` documents the dependency. One `filepath.EvalSymlinks` at
the top of `codexContextChain` closes it permanently. **Note the asymmetry with
L and T: symlinked *files* are handled correctly; it is symlinked *directories
in the walk path* that break.**

### S — an unreadable chain file: the binary drops everything, the model errors

Mode-000 `AGENTS.md` at the cwd, readable `AGENTS.md` at the marker root:

- binary: **zero** `<INSTRUCTIONS>` blocks — the root's perfectly readable
  layer went with it, exit still 0, nothing on stderr.
- model: `codexContextChain` happily returns both files (it only stats), and
  `codexContextBytes` then fails with
  `reading …/sub/AGENTS.md: permission denied`.

So `the Codex context at "X" selects "…"` would report a two-file chain for a
case where a real session sees no context at all, and the `contains` steps
would blow up with a Go error instead of failing the criterion. This is the
divergence that most resembles a real production incident shape: a delivery
target niwa cannot read is *exactly* the failure the "unwritable Codex delivery
target" scenario is about, from the other side.

### W — the budget is a real cut and the model has no truncation at all

Measured to the byte:

```
raw outer 30043  raw inner 3041  (sum 33084)
rendered block 32882 B; body 32787 B; o-runs 30000, i-runs 2705
tail: 'iiii…\n</INSTRUCTIONS>'    ← SENTINEL-W-CWD-TAIL absent
```

`30043 + 2725 = 32768` exactly. So the budget counts **raw concatenated file
bytes**, the wrapper/heading (~114 B) is not counted, the drain is
outermost-first, and the cut lands mid-file inside the innermost layer with no
marker and nothing on stderr — spike finding 3, now pinned arithmetically.

Two consequences for the model:

1. `codexChainBytesAt` (sum of `os.Stat` sizes) is **exactly the right
   quantity**. `the Codex context at "X" exceeds the default Codex budget` is
   validated as written.
2. `codexContextBytes` returns the whole chain untruncated, so
   `… contains "SENTINEL-REPO-TAIL"` passes for content a default-budget
   session would never see. In the over-budget scenario
   (`features/codex-agent.feature:459-466`) that is *intentional* — the comment
   says "nothing is dropped on niwa's side: the innermost layer is on disk
   whole" — but the very next comment claims **"a session reads all of it,
   because the budget covering the chain is declared in the project layer"**,
   and nothing in the suite tests that claim.

That claim is true, and I measured it — **but only with trust**:

```
project .codex/config.toml: project_doc_max_bytes = 99999   (AGENTS.md = 50037 B)
  empty CODEX_HOME  → rendered block 32883 B, tail sentinel kept: False
  trusted CODEX_HOME→ rendered block 50152 B, tail sentinel kept: True
```

The declared budget and the trust entry are load-bearing for each other, and
the suite asserts each half in a *different* scenario (budget at :466, trust at
:556). Their conjunction — the thing that actually makes the over-budget
scenario's claim true — is asserted nowhere.

---

## 4. One more latent divergence (not counted, config-dependent)

`project_doc_fallback_filenames` is a real key and it **adds candidates**:

```
cd $FX/Q                                   # only CLAUDE.md present, under a marker root
codex debug prompt-input                                        → (no AGENTS block)
codex debug prompt-input -c 'project_doc_fallback_filenames=["CLAUDE.md"]' → ['SENTINEL-Q-CLAUDE']
```

(`experimental_instructions_file_fallbacks` and `agents_md_fallback_filenames`
are not the spelling.) `codexContextCandidates` hardcodes exactly
`{AGENTS.override.md, AGENTS.md}` with no fallback support and no comment
saying it is valid only under default config. Likewise
`project_root_markers` — `-c 'project_root_markers=[".hg"]'` from `V/sub`
pulled in `V-ROOT`, which the model has no way to express. Neither is niwa's
to set, but both are things a **developer's own** `~/.codex/config.toml` can
set, and a `@codex-live` scenario runs against a sandbox `$HOME/.codex` that is
seeded from the developer's credential file only — so today the live path is
clean. Worth a one-line comment in the model rather than a code change.

---

## 5. Judgement: is a contract test worth building?

**Yes, and it is cheap.** Concretely: one Go test, `@codex-binary`-gated on
`exec.LookPath("codex")` alone — no login, no sandbox, no network, no model
spend — that builds a small table of trees in the process sandbox, runs
`codex debug prompt-input` with `CODEX_HOME` pointed at a scratch directory,
and asserts the rendered `<INSTRUCTIONS>` sentinel sequence equals what
`codexContextChain` + `codexContextBytes` predict for the same tree.

**Cost.** The whole harness in this spike is ~120 lines of Go plus ~90 lines of
fixture setup. Runtime is 0.11 s per fixture; 26 fixtures is ~3 s wall,
comfortably inside the `@critical` budget. The gate is strictly weaker than
`codexIsAvailable` — it needs no credential, so it does **not** need the
credential-copying dance in `codexIsAvailable`, and it runs green on any CI box
that has the binary. It is also the first check in this suite that would notice
a Codex **version bump** changing the rules, which is the failure the spike's
own "these findings are version-specific" warning is about and which nothing
currently detects.

**What it catches that today's suite cannot.** Today every
`the Codex context at "X" selects "Y"` step is a tautology: the model decides
the criterion and the model is the only thing under test. The contract test
turns the model into a *claim about the binary*. Specifically it would have
caught, as real failures rather than as a spike:

- R (symlinked directory in the walk) — silent wrong-tree walk;
- S (unreadable chain file) — model reports a chain where a session gets nothing;
- W's downstream claim (over-budget content actually reaching a session),
  including its hidden dependency on a trust entry;
- any future change to the precedence list, the marker list, the 32768 default,
  the outermost-first drain, or the empty-file-claims-the-slot rule.

**Scope it to the four rules the model actually encodes** — marker walk,
per-directory first-match, root-first order, budget arithmetic — and treat the
sentinel sequence as the assertion. Do not try to assert the paths; the binary
does not report them.

**Fix S and R regardless of whether the contract test lands.** They are
three-line changes (`filepath.EvalSymlinks` in `codexContextChain`; skip
unreadable files rather than erroring, or better, drop the whole chain the way
the binary does).

---

## 6. Gated live assertions that could become credential-free

Two `@codex-live` scenarios exist. Each has a piece that can move offline and a
piece that cannot.

### `a live interactive Codex session starts clean …` (feature :967)

Its subject is "niwa's preparation raises no trust or approval prompt". The
**mechanism** behind that — whether the directory is trusted, and therefore
what posture and what prompt a session would face — is directly readable
credential-free from `prompt-input`'s permissions block:

```
untrusted CODEX_HOME → `sandbox_mode` is `read-only`
trusted   CODEX_HOME → `sandbox_mode` is `workspace-write`
```

So "niwa wrote a trust entry that Codex actually honours for this exact path"
becomes a free assertion, at both the repo root and the nested directory, and
it is *more* discriminating than the current string-matching on TUI output
(which searches for `"approve"`, `"y/n"` and friends and would silently pass on
a reworded prompt).

**Coverage given up if the live scenario is deleted rather than supplemented:**
the pty scenario is the only thing in the suite that exercises the **TUI
startup path** end to end — the actual modal rendering, the hook-review prompt
(spike finding 7), terminal handling, and `theCodexSessionReachedReadyState`'s
proof that the session got past its own login. `prompt-input` constructs no
session and renders no UI, so it can confirm the *posture* is right and cannot
confirm that a real TUI, having read that posture, draws nothing. Keep the
live scenario; make the free check the `@critical` one and let the live one
stay the belt-and-braces.

### `a live Codex session writes a file on its first attempt` (feature :932)

Two of its three gates can be dropped for the offline half. The claim "a
session standing here would be permitted to write" is exactly the posture line
above and needs neither a login nor a working user namespace — which means the
offline version **runs on the very hosts where `theCodexSandboxCanRunHere`
currently makes the scenario pending**, and the spike records that this
measuring host is one of them (`bwrap: setting up uid map: Permission denied`).
Today that scenario is silently pending on any AppArmor-restricted box, i.e.
plausibly always.

**Coverage given up:** everything past the permission grant. The offline check
cannot show that the sandbox actually *constructs*, that the write actually
lands, or that a model turn happens at all — and the spike's finding 16 (exit 0
is not a success signal) plus finding 14's host caveat mean those are precisely
the things that fail in practice. So this one is a genuine trade: the offline
assertion converts a scenario that is pending-and-silent into one that is
green-and-narrow. Do both, and name the offline one for what it proves
("the prepared repository is trusted and would run workspace-write"), not for
what the live one proves.

### Not movable

`the git status of every repo … is clean` after a live session, and anything
about trust **write-back** side effects (finding 14), stay where they are —
both are filesystem consequences of a session having actually run.

---

## Artifacts

All under `/home/dgazineu/.claude/jobs/e40f3334/tmp/` (scratch, disposable):
`build_fixtures.sh`, `build_fixtures2.sh`, `run_binary.py`, `run_binary2.py`,
`probe_surface.sh`, `probe_budget.sh`, `probe_R_W.sh`, `probe_fallback.sh`,
`probe_nocred.sh`, `binary_results.json`, `binary_results2.json`,
`niwacopy/test/functional/zz_contract_probe_test.go`.
No credential file was read or printed at any point.
