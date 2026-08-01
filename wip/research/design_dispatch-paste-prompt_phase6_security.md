# Security Review

**Verdict:** PASS

The security section characterizes its threats correctly and the mitigations work as described; the gaps found are narrow, cheap to close, and none of them is an exposure that shipping this design would create.

## Threat Assessment

### 1. Terminal escape sequences in pasted content -- characterized correctly, with an under-stated property

The neutralization *rule* is stronger than the *invariant* the section advertises, and the rule is what carries the security weight. "Tab passes through; every other C0 control byte and DEL renders in caret notation; C1 controls and invalid UTF-8 render as hex escapes" is an allowlist, not a blocklist of dangerous sequences. That matters for every case raised:

- **OSC (window title, and OSC 52 clipboard write/read):** an OSC sequence begins `ESC ]`. ESC is 0x1B, a C0 byte, so it renders as `^[` and the sequence is defused at its introducer; the trailing `]52;c;...` is printable ASCII that renders as literal text. The 8-bit form 0x9D is a C1 control, hex-escaped. Covered on both encodings.
- **DCS / APC / PM (0x90 / 0x9F / 0x9E, or `ESC P` / `ESC _` / `ESC ^`):** same argument. Covered.
- **Mode-setting rather than cursor-moving sequences** (`CSI ? 1049 h`, `CSI ? 2004 l`, and friends): these do not move the cursor, so the stated invariant does not exclude them -- but the rule does, again at the ESC. Covered in fact, not covered by the sentence the design calls "the security property."
- **Terminals that answer queries by writing to stdin** (DA, DSR, OSC 52 read): this cannot reach the payload. A terminal responds to a sequence it *renders*, and neutralization means the pasted bytes are never rendered -- the terminal sees `^[[6n` as five printable glyphs and has nothing to answer. Neutralization therefore protects payload integrity, not just display integrity, and that is the stronger argument for it than the one the section makes.

The correction is editorial: the cursor-motion invariant is a *consequence* of the escaping rule, not equivalent to it. An implementation that satisfied only the advertised invariant -- stripping cursor-motion CSI sequences and passing everything else -- would leak OSC 52 and title-setting. State the property as the rule ("the transcript emits only printable characters and tab") so the weaker restatement cannot be mistaken for the requirement. PRD acceptance already tests the broader form ("no executable control sequence from the input").

Not covered by the rule: bidirectional overrides (U+202E), zero-width joiners, and combining marks are valid UTF-8 in no control class, so they pass through and can reorder or hide transcript text. This is display spoofing with no path to the payload, and the transcript is a bounded summary in the large-paste case anyway. Theoretical.

### 2. ISIG reasoning -- sound, and accurate about what the call clears

`term.MakeRaw` does clear ISIG, IEXTEN, and IXON in one call, so the design's parenthetical is correct rather than hopeful. Clearing ISIG creates no new exposure of substance: a typed 0x03 still abandons, because the gesture table handles it as a byte, and a pasted 0x03 is literal inside a paste block, which is the point. The one thing genuinely lost is the kernel-level escape hatch: if the read loop itself wedges, no terminal key can kill or suspend the process and recovery needs a second terminal. Local developer tool, self-inflicted, minor.

**0x1C (Ctrl-\\, SIGQUIT) is handled for free and the design does not say so.** ISIG governs INTR, QUIT, and SUSP together, so clearing it means a pasted 0x1C cannot raise SIGQUIT. That is worth more than the design credits: SIGQUIT's default action is terminate *with core dump*, and a core would put the entire pasted payload on disk -- contradicting the section's own "the prompt is still never written to disk." The mitigation happens to cover it.

The residual is SIGQUIT arriving from outside the terminal (`kill -QUIT`, or a process-group signal). The handled set is SIGINT, SIGTERM, SIGHUP, SIGTSTP, SIGCONT. SIGQUIT is absent, so its default disposition applies: core dump containing the payload, and the terminal left in raw mode with no handler to restore it. R9 does not require SIGQUIT coverage, so this is not a requirements violation, but adding it to the existing handler is one line and closes both halves.

### 3. "Pre-existing and unchanged" -- right about the code, too strong about the threat

The code-level claim checks out. The prompt reaches the worker as a single argv element and is never passed through a shell (`internal/cli/dispatch.go:334`), and the keep-alive prepend concatenates into that same element rather than adding another (`internal/cli/dispatch.go:327`). Capture changes neither, so nothing about command construction widens.

But "the capture does not widen this surface; it changes only where the bytes come from" is the wrong framing for prompt injection, where provenance *is* the surface. The feature exists precisely to hand a worker content the developer has not read -- the PRD's own user story is "so that I do not have to summarize an error I do not yet understand." Argument-path prompts are authored by the person typing them; capture-path prompts are quoted from CI logs, issue bodies, and web pages. The volume and provenance of untrusted-to-the-model text flowing into an autonomous worker's opening instruction changes materially even though the syntax does not.

The rendering decision compounds it: for a large paste the transcript shows a count line plus the first and last line, so text crafted as instructions sitting in the middle of a pasted log is never displayed to the developer who is nominally confirming it.

No mitigation belongs at this layer -- you cannot sanitize instructions out of a log, and the boundary that actually applies is the worker's own permission surface, not argv construction. The section should concede the provenance shift in a clause rather than deny it, and name where the real boundary lives.

Worth recording on the other side of the ledger: capture *removes* an exposure the positional path has. `niwa dispatch "<pasted log>"` writes the entire prompt into the developer's shell history file. Interactive capture does not. That is a real improvement and the section does not claim it.

### 4. Terminal restoration -- sufficient for what it claims; two real gaps

SIGKILL is correctly out of scope. No in-process handler catches it, every raw-mode program shares the property (vim, less, ssh all leave a terminal that needs `reset`), the design does not claim coverage, and R9 does not require it. Not a finding.

Two paths are thinner than the section's "suspend and resume are covered rather than assumed":

- **SIGQUIT**, as above: unhandled, terminal left raw.
- **Resume into a background process group.** If the developer suspends with Ctrl-Z and then types `bg`, the SIGCONT branch re-enters raw mode from a background process group. `tcsetattr` from a background pgrp raises SIGTTOU; if SIGTTOU is ignored, the call *succeeds* and stamps raw settings onto the terminal the shell is now sitting at -- exactly the leak the whole discipline exists to prevent -- and the subsequent read raises SIGTTIN. The fix is a foreground check (`tcgetpgrp(fd) == getpgrp()`) in the SIGCONT branch before re-entering raw mode. R38 only requires resume-to-foreground, and backgrounding a capture is a nonsense action, so this is bounded and self-inflicted -- but it is the one place the design's stated coverage outruns its mechanism.

### 5. Additional surfaces

- **Transcript redirected to a file: structurally excluded, correctly.** R21 gates the capture on standard error being a terminal, so a redirected stderr means no capture runs at all and the neutralized transcript can never land in a file. Correct by construction rather than by care. The residual is stderr pointing at a *different* tty than stdin (`2>/dev/pts/N`), which splits rendering from the device under raw mode; the design already applies raw mode to stdin's descriptor rather than stderr's, which is the right call and an improvement on the existing picker.
- **Denial of service via the size ceiling: a real gap.** R17 requires the buffer retain everything including the input that crossed the ceiling, and neither document caps how far past the ceiling retention goes. A sufficiently large paste (`tmux load-buffer` of a big file, or any process writing to the pty) grows the buffer without bound. The tail is an OOM kill, which is a SIGKILL, which leaves the terminal raw. A stated hard retention cap -- ceiling plus slack, refuse to append beyond it -- closes this and does not weaken R17, whose purpose is that the developer can delete down to a submittable size.
- **Unintended dispatch, path one: a forged paste-end marker.** Pasted content containing the literal bytes `ESC [ 2 0 1 ~` terminates the paste block early on a terminal that does not filter it; a following CR is then read as a submit gesture and the remainder of the paste reaches the shell as commands. Mainstream terminals strip C0 controls from pasted data by default, and bash and zsh's own line editors carry the identical weakness, so this is inherited from the terminal ecosystem rather than introduced here. It still deserves a sentence beside R40, because it produces R40's exact failure mode on a *modern* terminal and is triggered by content rather than by terminal age.
- **Unintended dispatch, path two: EOF on hangup.** R28 makes end-of-input on a non-empty buffer a submit. When a terminal window closes, the read returns EOF and the SIGHUP handler fires, and the race between them decides whether a half-composed prompt dispatches a worker the developer never confirmed. Cheap fix: set a hangup flag in the SIGHUP handler and treat EOF-after-hangup as abandonment rather than submission.

## Blocking Issues

None.

## Non-Blocking Observations

1. **State the neutralization property as the escaping rule, not as the cursor-motion invariant.** The rule defuses OSC 52, title-setting, DCS/APC/PM, and mode-setting sequences; the invariant as written excludes only cursor motion, and an implementation satisfying only the invariant would leak the rest.
2. **Add that neutralization also protects the payload.** Because the terminal never renders pasted bytes, it never answers embedded queries, so query responses cannot be injected into the capture. This is a stronger argument for the mitigation than display integrity alone.
3. **Add SIGQUIT to the handled signal set.** It is currently unhandled: default disposition dumps core (payload on disk, contradicting "never written to disk") and leaves the terminal raw. Same handler as SIGTERM.
4. **Note that clearing ISIG also disables terminal-generated SIGQUIT**, so a pasted 0x1C is captured as payload. The section enumerates 0x03 and 0x1A; 0x1C is covered by the same mechanism and is the one with the worst default action.
5. **Check foreground process-group ownership in the SIGCONT branch** before re-entering raw mode, so a suspended-then-backgrounded capture cannot stamp raw settings onto the shell's terminal.
6. **Cap buffer retention.** R17's "retain everything above the ceiling" has no upper bound; state one so a pathological paste cannot exhaust memory (and thereby leave the terminal raw via the OOM kill).
7. **Soften the provenance claim in the third paragraph.** Argv construction is genuinely unchanged, but capture does shift what typically flows into a worker's opening instruction from authored text to quoted, unread text -- and the bounded transcript means the middle of a large paste is never displayed. Name the worker's permission boundary as the control that applies.
8. **Document the forged `ESC[201~` case beside R40**, as content-triggered early submission producing R40's failure mode on a terminal that does delimit pastes.
9. **Treat EOF-after-SIGHUP as abandonment**, so closing a terminal window mid-capture cannot dispatch a half-composed prompt.
10. **Credit the shell-history improvement.** The positional path writes the whole prompt to the developer's history file; capture does not.
11. **Bidi overrides and zero-width characters pass the neutralizer** and can reorder or hide transcript text. Display-only, no payload path, and the transcript is already a summary. Noted for completeness; no action recommended.

## Summary

The two mitigations the section rests on -- byte-level neutralization and clearing ISIG -- both work as described, and the neutralization rule is in fact stronger than the invariant used to advertise it, covering OSC 52, DCS, APC, PM, and mode-setting sequences that the cursor-motion phrasing alone would not; the terminal-query-injection concern dissolves because a terminal only answers sequences it renders. The gaps found are narrow and cheap: SIGQUIT is unhandled and dumps core with the payload in it, resume into a background process group can stamp raw settings onto the shell's terminal, buffer retention above the ceiling has no upper bound, and two paths -- a forged paste-end marker and EOF on hangup -- can submit without the developer intending it. The one framing correction is that "the capture does not widen this surface" is too strong for prompt injection, where provenance is the surface and this feature exists specifically to move unread text into an autonomous worker's opening instruction; that is a wording change and a pointer to the worker's permission boundary, not a blocker.
