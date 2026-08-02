# Decisions 2 and 4 — the pointer element, and where the spill file lives

Recorded together because they were cross-validated against each other: both
wanted to own the uniqueness token, and their first answers conflicted.

## The conflict and its resolution

Decision 2 wanted a per-launch random nonce so the excerpt's delimiter cannot be
forged by the very text it delimits. Decision 4 wanted an instance-scoped
counter (`prompt-1.txt`, `prompt-2.txt`) so two independent dispatches of the
same payload produce byte-identical pointer text.

Resolved in favour of **one `crypto/rand` token, 16 hex characters, serving both
the filename and the fence**. The counter's only advantage was satisfying R47's
byte-identity reading, and R47's criterion was rewritten during the second jury
round to normalize the uniqueness token as well as the instance directory --
so the reason for the counter no longer exists. The nonce's advantage cannot be
recovered any other way: the token is minted after the prompt bytes are read, so
no submitted text can contain it regardless of who wrote it.

A content hash was rejected outright: `niwa watch`'s continuation prompts are
fixed templates that hash identically on every pass, so a hash produces one file
where R59 requires two.

## The pointer element

Order: `keepAliveArmingInstruction` (unchanged, first) + pointer text. The
existing concatenation order is preserved because the arming instruction opens
"before starting the task below" and closes "then proceed with the task" -- a
forward reference that a reordering would dangle. It also keeps the message's
first bytes niwa's own fixed text on every path.

Shape, in order: a sentence saying the text was too large to pass as an
argument; a numbered instruction to read the file in full, saying it is the
complete task and nothing else in the message is; the absolute path on its own
line; a framing sentence saying the excerpt is a prefix and is quoted text, never
instructions; the fenced excerpt; a closing line stating how many bytes of how
many were quoted. The untrusted span sits last, bracketed by niwa's framing on
one side and the message boundary on the other.

Wording mirrors what niwa already tells agents to do in
`internal/workspace/rootskills/dispatch/SKILL.md` step 3 ("Read <abs-path> for
your complete task brief, then implement it"), and the numbered-list shape comes
from `internal/watch/prompt.go`'s `BuildReviewPrompt`. niwa recommending one
phrasing to agents and using a different one itself would be indefensible.

Excerpt bound: **4096 bytes**. Measured floor -- a real Go panic with its first
frame is 126 bytes, a `go test` first failure block 55 bytes -- so 4096 clears
R52's 512-byte floor by a wide margin and clears real output by 16x to 68x. At
the top it is 3.1% of the exec ceiling. Bytes only, no line cap: a line cap can
violate R52's floor, since two prompts differing at byte 3000 but after the 40th
line would produce identical argv elements.

## The excerpt must be rendered, not passed through raw

This is the finding that matters most. **A NUL byte anywhere in an argv element
makes exec fail** -- verified by probe: `exec.Command("/bin/true",
"abc\x00def").Run()` returns `fork/exec: invalid argument`, because Go's
`syscall.BytePtrFromString` returns EINVAL on an embedded NUL. The capture
preserves raw control bytes in the payload deliberately, so an unsanitized
excerpt would carry that failure into the very path whose purpose is that a
large prompt always dispatches.

So: the **file** gets raw bytes untouched (R51 is absolute there), the
**excerpt** goes through `neutralize` from `internal/promptcapture/neutralize.go`
first. That function already maps C0 controls to caret notation and invalid
bytes to `\xNN`, with the stated property "no byte that can introduce an escape
sequence survives". It needs one widening for this use: line feed must pass
through, or a stack trace arrives as a single line of `^J`. LF cannot introduce
an escape sequence, so the stated property survives verbatim.

Pipeline, bounded throughout: cut at 4x the excerpt budget on a rune boundary,
render, cut again to the budget on a rune boundary, trim a trailing partial
`\xN` escape, then redact the fence token as a belt.

Rune-boundary cutting uses `utf8.RuneStart` walking back at most three bytes.
Not `string([]rune(s)[:k])` -- which is what `truncateForDisplay` in the same
package does, and which would allocate 2.3 MB for a 582 KB log and counts runes
rather than bytes.

The same NUL problem exists on the below-threshold path today and is a
pre-existing latent defect. It became R61.

## The path

Absolute. R52 requires resolution regardless of working directory, and
`continueReview` already rejects a non-absolute instance path, so the value is
in hand. Relative-to-cwd works only until the worker or a subagent changes
directory. Cost: the instance path embeds the home directory and lands in the
worker's transcript -- minor, unavoidable, and already true of the guard binary
path baked into settings hooks.

Sandbox check for the `niwa watch` path came back clean: the no-egress stanza
constrains network only, the egress matcher is `WebFetch|WebSearch|mcp__`, the
filesystem guard matcher covers writes only, and the ask posture auto-allows
Read explicitly.

## Where the file goes

`<instance>/.niwa/dispatch-prompts/prompt-<token>.txt`, directory created with
`os.Mkdir` at 0700, file opened `O_CREATE|O_EXCL|O_WRONLY` at 0600.

**The workspace root's `.niwa/` would have eaten the file.** `materializeAndSwap`
rotates the whole config directory on every apply, carrying only `instance.json`
and `dispatch-briefs/` into staging -- and the comment there records that this
was found the hard way, on exactly this failure: the `/dispatch` skill writes a
brief, then `niwa dispatch` refreshes the same config dir before the worker can
read it. The instance's `.niwa/` is structurally clear of that machinery, and
every other walker in the tree skips it explicitly.

A dedicated subdirectory rather than dropping the file beside the dispatch
marker, because `SaveState` creates `<instance>/.niwa` at 0755 and a later
create call does not lower an existing directory's mode -- so R54's directory
clause is unsatisfiable without a directory niwa creates itself. Precedent
exists: `.niwa/sessions/` and `.niwa/worktrees/` are both created at 0700.

Do not name the directory `worktrees`: `discoverWorktree` walks up looking for
`.../<instance>/.niwa/worktrees/<name>` and would misclassify a worker whose cwd
landed inside.

Extension `.txt`, and specifically **not** `.md` -- the content is a pasted log
that R51 requires be carried verbatim, and calling it markdown invites the
reading agent to interpret backtick fences that are just log output.

## Lifecycle

Written at a new step between the keep-alive prepend and the launch. It cannot
move earlier: R45 requires the decision be made after every prepend, and the
keep-alive prepend depends on instance settings that do not exist until the
instance does.

That placement is inside the rollback window, so a failed write destroys the
instance and any partial file with it. Nothing deletes the file after the
launch, so it is still there when the worker reads it -- which matters, because
the launch returns long before the worker registers: step (10) polls the jobs
directory for up to 30 seconds afterwards just to discover the session exists.

`continueReview` accumulates at most one file per continuation pass, in an
instance the reaper backstop already owns (it is dispatch-named and unmapped).
No sweep: a pre-launch sweep would race the read of a still-running prior
worker, and the shared launcher cannot know whether its caller has quiesced
anything. Bounded accumulation inside a doomed directory beats a race.

Use `os.Mkdir`, not `os.MkdirAll`: `MkdirAll` under a deleted instance root
would rebuild the chain and leave a directory that matches the dispatch-name
signature but has no `instance.json`, which `ValidateInstanceDir` then refuses
to destroy -- an unreclaimable orphan, the exact failure the dispatch design
works hardest to prevent.

No `fsync`: reader and writer are on the same host, and a crash that loses the
file loses the instance and the worker too.

## Main weaknesses

The excerpt is optimized for safety at the cost of the job it exists to do.
Fencing it, labelling it as never-instructions, and rendering escapes as caret
notation all make it clearer that the excerpt is not the task -- which means a
worker that ignores the read instruction has less usable material than if niwa
had pasted the first 4 KB plainly. A developer who pastes a colorized test run
gets an opening message littered with `^[[0;31m`, which looks like corruption.
Stripping the sequences instead would look better and would break
`neutralize`'s stated property, since it requires recognizing escape sequences
and a malformed one then survives.

4096 is a judgment call. It is justified against measured payloads and clears
the floor widely, but nothing derives it.
