# Lead: claude --resume failure modes (empirical)

Tested against `claude` CLI v2.1.138 on this machine
(`/home/dgazineu/.local/bin/claude`). All commands run on 2026-05-09.

## Methodology

**What I tested empirically (commands run, output captured verbatim):**

1. Generated two real conversations under throwaway CWDs (`/tmp/claude-resume-test`,
   `/tmp/claude-resume-test-other`) using `claude --print "..."`. Each run
   produced a JSONL transcript at
   `~/.claude/projects/<encoded-cwd>/<conv_id>.jsonl`.
2. Used the captured `conv_id` (`9b45ae65-8de1-469f-84b7-38af69876a1a`) to
   probe `claude --resume` against the following adversarial inputs:
   - correct CWD + valid id (control: should succeed)
   - correct CWD + bogus UUID (`00000000-...`)
   - correct CWD + non-UUID free-form string
   - **wrong CWD** + valid id (the canonical attach hazard)
   - JSONL truncated mid-line
   - JSONL truncated at a clean line boundary (only the first 2 records)
   - JSONL zero-byte
   - JSONL containing pure garbage (`this is not json at all\n{"broken":`)
   - empty project dir (dir exists, no JSONL inside)
   - non-existent project dir (CWD never seen by claude)
   - `--continue` from a CWD with no project history (silent-fresh probe)
   - `--fork-session --resume` from wrong CWD
   - `--add-dir <correct-cwd> --resume` from wrong CWD
3. Tested both `--print` (non-interactive) and bare interactive
   (`claude --resume X </dev/null`) to see if either path silently
   degrades to a fresh session.
4. Probed CWD encoding by running fresh `claude --print` sessions in
   directories containing dots, underscores, spaces, uppercase, `@`,
   and `+` to derive the actual encoding rule.
5. Inspected `claude --help`, `claude project --help` for any
   pre-launch resume-validation primitive.

**What I could NOT test safely:**

- Behaviour when `~/.claude/projects/` itself is on a read-only or full
  filesystem. Out of scope (filesystem-level pathology, surfaces as a
  generic OS error long before resume).
- Behaviour when two processes attempt `--resume <same id>`
  concurrently. Not strictly a transcript failure mode; covered by the
  separate lock-semantics lead.
- Authenticated-vs-unauthenticated states. The machine I tested on is
  authenticated; an unauthenticated caller fails earlier (`Not logged
  in · Please run /login`) and never reaches resume resolution.

**Cleanup:** all test artifacts removed via
`claude project purge -y <path>` plus `rm -rf` of the temp dirs. No
production niwa session data was touched at any point.

## Findings

### Project-dir CWD encoding (correction to round-1 finding)

Round 1 reported the encoding as `base64url(cwd)`. **That is wrong.**
The actual encoding is a non-reversible character substitution:
**every character that is not `[A-Za-z0-9]` is replaced with `-`**, and
the result is prefixed by the leading `/` becoming a leading `-`.

| Real CWD | Project dir |
|----------|-------------|
| `/tmp/claude-resume-test` | `-tmp-claude-resume-test` |
| `/tmp/cr.dotted/sub_dir` | `-tmp-cr-dotted-sub-dir` |
| `/tmp/cr space/test` | `-tmp-cr-space-test` |
| `/tmp/cr@upper-CASE+plus/x` | `-tmp-cr-upper-CASE-plus-x` |

Implication: the encoding **is not collision-free**. Two distinct
worktrees `/tmp/foo-bar` and `/tmp/foo_bar` both map to
`-tmp-foo-bar`. Realistic worktree paths under
`<workspace>/<instance>/<repo>` will not collide in practice (no
underscores or dots in niwa-generated names today), but a deterministic
file-existence check is still safe because it asks the same encoded
question claude does.

### Nonexistent conv_id (correct CWD)

```
$ cd /tmp/claude-resume-test
$ claude --print --resume 00000000-0000-0000-0000-000000000000 "say only NOPE"
EXIT=1
STDERR: No conversation found with session ID: 00000000-0000-0000-0000-000000000000
STDOUT: (empty)
```

Loud failure. Single-line message on stderr, exit code 1. **No silent
fallback to a fresh session.**

### Non-UUID, no matching title (`--print` path)

```
$ claude --print --resume not-a-uuid-at-all "say X"
EXIT=1
STDERR: Error: --resume requires a valid session ID or session title
        when used with --print. Usage: claude -p --resume <session-id|title>.
        Provided value "not-a-uuid-at-all" is not a UUID and does not
        match any session title.
```

Loud failure with a more detailed message than the missing-UUID case.
Reveals an undocumented feature: **`--resume <title>` is also
accepted** if claude can match it against a recorded session title
(set via `-n/--name`). niwa won't pass titles, but worth knowing.

### Wrong CWD (valid conv_id, JSONL exists elsewhere)

```
$ cd /tmp/claude-resume-test-other          # different from origin CWD
$ claude --print --resume 9b45ae65-8de1-469f-84b7-38af69876a1a "say DONE2"
EXIT=1
STDERR: No conversation found with session ID: 9b45ae65-8de1-469f-84b7-38af69876a1a
```

Same loud failure as nonexistent — **claude does not search across
project dirs.** Identical interactive behaviour:

```
$ claude --resume 9b45ae65-... </dev/null
EXIT=1
STDERR: No conversation found with session ID: 9b45ae65-...
```

No new project dir was created in `~/.claude/projects/` for the wrong
CWD when the resume failed, so the failure is also side-effect-free.

`--add-dir <origin-cwd>` does **not** relax the rule:

```
$ cd /tmp/claude-resume-test-other
$ claude --print --add-dir /tmp/claude-resume-test --resume 9b45ae65-... "say X"
EXIT=1
STDERR: No conversation found with session ID: 9b45ae65-...
```

### Corrupted JSONL (file exists, content is bad)

All four corruption variants produced the same outcome:

| Corruption | Bytes | Result |
|------------|-------|--------|
| truncated mid-line | 500 | exit 1, "No conversation found" |
| 2 clean records only | 336 | exit 1, "No conversation found" |
| zero-byte | 0 | exit 1, "No conversation found" |
| pure garbage (broken JSON) | ~40 | exit 1, "No conversation found" |

```
$ : > ~/.claude/projects/.../9b45ae65-...jsonl    # zero bytes
$ cd /tmp/claude-resume-test
$ claude --print --resume 9b45ae65-... "say X"
EXIT=1
STDERR: No conversation found with session ID: 9b45ae65-...
```

**Crucial observation:** claude does not crash, panic, or partially
recover — it treats unreadable transcripts the same as missing
transcripts. After restoring the original JSONL, resume worked again
(`EXIT=0`), confirming the file content was the failure cause.

This means **"file exists at the deterministic path" is a
necessary-but-not-sufficient signal** for niwa's pre-flight check. A
zero-byte JSONL or a partially-flushed one would still pass an
existence test but fail in claude. Filing this as an open question
below.

### Empty project dir (dir exists, no JSONL inside)

```
$ mkdir -p ~/.claude/projects/-tmp-claude-resume-empty
$ cd /tmp/claude-resume-empty
$ claude --print --resume 11111111-1111-1111-1111-111111111111 "say X"
EXIT=1
STDERR: No conversation found with session ID: 11111111-...
```

Same loud failure path. No special-casing.

### Non-existent project dir (CWD claude has never seen)

```
$ mkdir -p /tmp/cr-never-seen-XYZ && cd /tmp/cr-never-seen-XYZ
$ claude --print --resume 9b45ae65-... "say X"
EXIT=1
STDERR: No conversation found with session ID: 9b45ae65-...
```

Same.

### Resume-validation primitive (none)

`claude --help` and `claude project --help` expose **no** way to
list, inspect, or pre-validate sessions. The closest commands are:

- `claude project purge` — destructive, not for inspection
- `claude --resume` (no value) — opens an interactive picker, but only
  for the current CWD's project dir, and `--print --resume` errors out
  if no value is given

There is no `claude conversations list`, no `claude --check-resume`,
no `claude session info <id>`. **The deterministic-path file-existence
check is the only pre-flight option niwa has** without forking the
process speculatively.

### Conv_id format

- Always a lowercase UUIDv4 (regex `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).
- The flag `--session-id <uuid>` (separate from `--resume`) explicitly
  requires "must be a valid UUID" in its help text, confirming the
  contract.
- No escaping needed when passing via `exec.Cmd` argv — UUIDs contain
  no shell metacharacters.
- `--resume` accepts a session **title** as a fallback (the
  user-facing name set via `-n/--name`); niwa should never rely on
  this, only ever pass the captured UUID.

### Exit codes

| Mode | Outcome | Exit |
|------|---------|------|
| `--resume <good-id>` from origin CWD | resumes, prints completion | 0 |
| `--resume <missing-id>` (any reason) | "No conversation found" | **1** |
| `--resume <non-UUID-non-title>` with `--print` | Usage error | **1** |
| `--print` no resume | fresh session, prints completion | 0 |
| `--continue` from origin CWD with history | resumes most-recent | 0 |
| `--continue` from CWD with **no** history | **silent fresh session** | **0** |
| `--fork-session --resume <id>` from wrong CWD | "No conversation found" | 1 |

**Critical: `--continue` is the only mode that silently degrades to a
fresh session.** `niwa session attach` MUST use `--resume <uuid>`,
never `--continue`, even if the captured UUID is "the most recent
session in this project dir."

## Implications

### Does niwa session attach need to validate before launch?

**Yes — but not for safety. For UX.**

Safety-wise, claude's `--resume` already fails loudly (exit 1, stderr
message). niwa could trust-and-let-claude-fail and the user would never
get a deceptive "fresh session that looks resumed" outcome.

What pre-flight validation buys is **error-message quality**. claude's
error is `No conversation found with session ID: <uuid>`, which is
user-hostile in the niwa context — the user typed a niwa session id
(8-char hex), not a claude conv_id, and "No conversation found" sounds
like the niwa session is gone when actually only the transcript is.

### Recommended pre-flight check

In `niwa session attach <sid>`:

1. Read `<instance>/.niwa/sessions/<sid>.json`.
2. Pull `claude_conversation_id` and `worktree_path` from the state.
3. Compute the deterministic transcript path:
   ```go
   func claudeTranscriptPath(worktree, convID string) string {
       enc := nonAlnumToDash(worktree)         // [^A-Za-z0-9] -> "-"
       return filepath.Join(homeDir, ".claude", "projects", enc, convID+".jsonl")
   }
   ```
4. Stat the path. Three cases:
   - missing: emit a niwa-specific error (see below) and **do not**
     exec claude.
   - present but zero bytes: emit a degraded-transcript error.
   - present with content: chdir to `worktree_path`, exec
     `claude --resume <convID>`. Trust claude to handle deeper
     corruption (it already does, with exit 1 + clear stderr).

A JSON-header sniff (read the first line, parse, check `sessionId`
matches) is feasible but redundant — claude already revalidates and
fails loudly on any internal mismatch. Skip it.

### Recommended error messages

Phrasing all reuse niwa's existing voice (lowercase command name +
suggestion). The user's suggested wording is close; tighten the noun
("transcript" not "conversation") and split the two failure cases:

**Case A: state file says `claude_conversation_id` is empty**
(daemon never captured one — first task crashed before exit, or
session was init'd but never ran).

```
niwa: error: session <sid> has no recorded claude conversation
hint: the session was created but never ran a worker, or the worker
      crashed before finishing. inspect with `niwa session show <sid>`
      or remove with `niwa session destroy <sid>`.
```

**Case B: conv_id present, but transcript file missing** (claude
purged it, user ran `claude project purge`, daemon recorded a stale
id from a previous machine, or path encoding mismatch).

```
niwa: error: claude transcript missing for session <sid>
expected: ~/.claude/projects/<encoded>/<convid>.jsonl
hint: claude may have purged the transcript, or the worktree was
      moved. start a fresh worker with `niwa session run <sid>` or
      remove with `niwa session destroy <sid>`.
```

**Case C: transcript file is zero bytes** (rare — partial flush, disk
full mid-write).

```
niwa: error: claude transcript is empty for session <sid>
path: ~/.claude/projects/<encoded>/<convid>.jsonl
hint: the transcript was started but no records were written. start a
      fresh worker with `niwa session run <sid>`.
```

The user's draft wording (`session may have been initialized but
never ran a worker`) conflates cases A and B. Splitting them helps the
user pick the right next action: case A is "your session is broken at
the niwa layer", case B is "your session is fine but the underlying
claude data is gone".

### CWD requirement

`niwa session attach` MUST exec `claude` with the worktree as its
working directory — claude indexes transcripts by CWD and there is no
flag to override this lookup (`--add-dir` does not help; verified
empirically).

**Precedent:** there is no exact precedent for "exec a long-running
foreign process in a worktree". The closest patterns:

- `niwa go` / `niwa go <repo> <sid>` resolves a worktree path and
  emits a landing-path file via `writeLandingPath` for the shell
  wrapper to consume — i.e., the user's shell does the `cd`, niwa
  itself never runs `os.Chdir` and never execs anything.
- `niwa apply`, `niwa init`, `niwa destroy` exec git/scripts via
  `os/exec` with `cmd.Dir` set explicitly.

`niwa session attach` will need the second pattern, not the first,
because the user expects an *interactive* claude session inheriting
this terminal's stdio. Concretely:

```go
cmd := exec.Command(claudeBin, "--resume", convID)
cmd.Dir = worktreePath
cmd.Stdin = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
return cmd.Run()
```

Or, to free niwa's PID, `syscall.Exec` after the pre-flight check
(loses ability to do post-cleanup but matches `niwa go`'s
"hand-off-and-disappear" feel).

The PRD should specify which one — `cmd.Run()` lets niwa observe the
claude exit and could log/clean up; `syscall.Exec` is cleaner for the
user (Ctrl-C goes straight to claude, no double-fork). Recommendation:
`syscall.Exec`. niwa has nothing useful to do after claude exits.

## Surprises

1. **Round 1's encoding claim was wrong.** Project dirs are not
   `base64url(cwd)`; they are `s/[^A-Za-z0-9]/-/g`. This matters — a
   niwa implementation that base64-encoded the worktree path would
   look in a directory claude never writes to. Caught only by running
   real sessions and observing the actual filenames.
2. **`--continue` silently degrades to a fresh session** (exit 0) when
   run from a CWD with no history. This is the worst possible UX for
   attach. Use `--resume <uuid>` exclusively.
3. **`--add-dir` does not help.** I expected it might broaden the
   transcript search. It does not — it only widens tool permissions
   for the live session.
4. **All corruption modes collapse to one error message.** Whether the
   file is missing, zero-byte, truncated, or garbage, claude reports
   "No conversation found with session ID: ...". Nice for niwa — only
   one branch to wrap — but means claude's error doesn't help us
   distinguish "transcript was purged" from "transcript is corrupted".
5. **Failed resumes leave no side effects.** No empty project dir, no
   stub JSONL. niwa can attempt resume speculatively without
   polluting `~/.claude/projects/`. (We still recommend pre-flight
   validation for error-message quality, not safety.)
6. **`claude --resume <title>` works** as a free-form fallback. Not
   relevant to niwa (we always have a UUID), but explains the more
   verbose error message in the non-UUID path.

## Open Questions

1. **Race between worker write and attach read.** If the user runs
   `niwa session attach` while a worker is mid-run, the JSONL is open
   for append by the worker. Does claude's `--resume` cooperate with a
   concurrent writer, error out, or silently see a stale view? Could
   not reproduce safely without spinning up a real niwa worker. The
   lock-semantics lead should cover this; flagging here so it isn't
   dropped.
2. **Multi-host transcripts.** If a niwa session was created on
   machine A (and `claude_conversation_id` recorded), then someone
   syncs the niwa state to machine B and runs `niwa session attach`
   there, the transcript file does not exist on machine B and resume
   will fail loudly. This is correct behaviour, but the error message
   should hint at the cause. Probably out of scope for v1.
3. **Claude version drift.** All findings here are for v2.1.138.
   Whether claude maintains the `s/[^A-Za-z0-9]/-/g` path encoding
   across versions is an undocumented contract. niwa should treat the
   pre-flight existence check as a best-effort optimisation and still
   tolerate `claude --resume` returning exit 1, in case a future
   claude changes the path scheme.
4. **What does `claude --resume` do interactively against a missing
   id?** Tested with `</dev/null`, which produces the same exit 1 +
   stderr message. Whether a real TTY changes anything (e.g., a TUI
   error dialog instead of a stderr line) was not tested. Worth a
   quick manual verification before finalising the error-message
   wrapping, since niwa may end up double-printing if claude already
   shows a dialog.

## Summary

The dominant failure mode is **wrong-CWD or missing-transcript, which
claude reports loudly** (exit 1, stderr "No conversation found with
session ID: <uuid>") with zero risk of silent fresh-session
degradation — provided niwa uses `--resume <uuid>` and never
`--continue`. The recommended validation layer is a deterministic
file-stat at `~/.claude/projects/<sNonAlnumToDash(worktree)>/<convid>.jsonl`
done before `syscall.Exec`-ing claude with `cmd.Dir = worktree`,
solely so niwa can emit a niwa-shaped error (case A: missing
conv_id in state file; case B: transcript file missing; case C: zero
bytes) instead of letting users see claude's UUID-shaped one. The
biggest open question is concurrent-writer behaviour when a worker is
mid-run and the user attaches — that is not safely testable without
a live niwa worker and should be folded into the lock-semantics lead
rather than guessed at here.
