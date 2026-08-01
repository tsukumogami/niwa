# Cross-Validation: dispatch-paste-prompt

Five decisions ran independently. This records where their assumptions had to be
reconciled before the architecture could be written.

## Conflict 1: the submit gesture (resolved by author decision)

Decision 2 recommended Ctrl-D as submit and Enter as the manual newline, deriving
it correctly from R4's then-unconditional no-truncation guarantee. The author
took the opposite trade: R4 is now scoped to terminals that delimit pasted blocks
(R40 records the degradation), and Enter submits.

**Resolution.** The gesture set is:

| Gesture | Byte(s) outside a paste | Effect |
|---|---|---|
| Submit | `0x0D` (Enter) | Submit the buffer |
| Submit (alias) | `0x04` (Ctrl-D) or `io.EOF`, non-empty buffer | Submit the buffer (R28) |
| End of input | `0x04` or `io.EOF`, empty buffer | End without dispatching (R28) |
| Manual newline | `0x0A` (Ctrl-J) | Append one `0x0A` |
| Manual newline (passive) | `0x1B 0x0D`, `ESC[13;2u`, `ESC[27;2;13~` | Append one `0x0A` if they arrive; never negotiated (R23) |
| Cancel | `0x03` (Ctrl-C), or SIGINT | Abandon, `ErrCanceled` (R8, R39) |
| Delete rune | `0x7F`, `0x08` | Remove the last rune |
| Delete word | `0x17` (Ctrl-W) | Remove trailing non-whitespace run, then preceding whitespace |
| Delete line | `0x15` (Ctrl-U) | Remove back to and including the previous `0x0A` |

Inside a bracketed-paste block every byte is literal, including `0x0D` and
`0x0A`, which is what makes Enter-submits safe on delimiting terminals. Adjacent
`0x0D 0x0A` outside a paste is one newline, not two.

Decision 2's remaining recommendations stand unchanged: clear ISIG via
`term.MakeRaw` (a pasted `0x03` or `0x1A` must not kill or suspend the process,
which R30 requires), reuse a canceled sentinel matching the house convention, and
keep the buffer append-and-truncate-only so no line editor is needed.

## Conflict 2: two retired "measurements"

Decision 1 re-measured the two facts PRD phase 2 supplied and retired both. The
20-second single-line hang is a short-write defect in the test harness -- the
child receives 4095, 4095, 2, 1 bytes and then nothing -- and it affects every
candidate reader. Neither reader has a per-line size limit. The PRD's Known
Limitations has been corrected.

**Consequence for the plan.** The functional harness must feed input in chunks
rather than as one burst, or the terminal-driven scenarios fail for reasons
unrelated to the code under test. This is a prerequisite, not an optimization.

## Conflict 3: which file descriptor enters raw mode

Decision 5 puts **stdin** into raw mode, deliberately departing from
`internal/tui/picker.go`, which calls `MakeRaw` on the stderr descriptor while
reading from stdin. That works only because the two usually refer to the same
terminal. Decision 3's core signature `read(ctx, stdin, stderr, limit)` keeps the
two roles separate, so the departure is consistent across both decisions.

## Agreements that survived independent derivation

- Decisions 1, 3, and 5 independently concluded that no new dependency is needed:
  `golang.org/x/term` already provides `MakeRaw`, `Restore`, and
  `SetBracketedPasteMode`, and the picker already uses all three.
- Decisions 1 and 4 independently concluded that the payload and the rendering
  must be produced by separate code paths rather than one echoing loop, which is
  what R30 requires and what disqualifies any reader that owns its own echo.
- Decisions 3 and 4 independently concluded that `internal/tui` is the wrong home
  for new code, because every file there is a synced copy shared with tsuku.
  Decision 4 additionally concluded `SanitizeDisplayString` should be left alone
  for the same reason, and a stronger neutralizer written in the new package.

## Open coupling for the plan

The reader's two pieces of cross-chunk state -- a held-back marker prefix and a
carriage-return flag that survives a read boundary -- are where decision 1 places
the real implementation risk. They must be tested at chunk size 1. This is a
test-design constraint the plan has to carry into the issue that builds the
reader.
