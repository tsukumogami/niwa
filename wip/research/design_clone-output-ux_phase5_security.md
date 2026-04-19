# Security Review: clone-output-ux

## Dimension Analysis

### External Artifact Handling

**Applies:** No

This design changes how git subprocess output is displayed; it does not introduce any new external artifact downloads, execution of remote content, or parsing of structured data from external sources. The `runGitWithReporter` helper reads git's stderr/stdout through an `io.Pipe` and a `bufio.Scanner`, but this is text classification of output that git already produces—not ingestion of executable or structured artifacts. No new network connections are opened by this change. The git binary itself is already trusted by the existing design; the new code only changes how its console output is routed.

### Permission Scope

**Applies:** No

The Reporter abstraction routes output to existing file descriptors (stderr, stdout). The `golang.org/x/term` package is used exclusively for TTY detection via `term.IsTerminal(int(os.Stderr.Fd()))`, which performs a single `ioctl` syscall and requires no elevated permissions. No new filesystem writes, network sockets, or process privileges are introduced. The `--no-progress` flag reduces permissions relative to the current design (suppressing terminal control). No escalation path exists.

### Supply Chain or Dependency Trust

**Applies:** Yes (limited scope)

The design adds `golang.org/x/term` as a new dependency. This package is part of the Go extended standard library maintained by the Go team at `golang.org/x`, making it among the most trustworthy external packages available in the Go ecosystem. It has no transitive dependencies of its own for the `IsTerminal` function. The existing `go.mod` already trusts packages from the same `golang.org/x` namespace indirectly. The supply chain risk from this addition is minimal but should be acknowledged: the module must be pinned in `go.sum` and reviewed on upgrade as with any dependency. No other new dependencies are introduced by this design.

### Data Exposure

**Applies:** No

This design does not access or transmit any user data beyond what git already produces on its stderr. No credentials, tokens, file contents, or workspace configuration values flow through the new Reporter path. The `isGitErrorLine` classifier operates only on git's own diagnostic messages. The design explicitly preserves R22 (overlaysync unchanged, no overlay URL or repo name in output), so the privacy constraint governing the most sensitive output path is maintained. Error accumulation in `strings.Builder` is held in memory for the duration of the command and included in the returned Go error value—it never leaves the process.

### ANSI/Terminal Escape Injection

**Applies:** Yes

This is the most relevant security dimension for this design.

**The risk:** Git error messages can contain content that originates from a remote server. A malicious git server can craft error messages (e.g., in SSH banner text, server-side hook output, or HTTP response bodies surfaced as git errors) that contain ANSI escape sequences or terminal control codes. When `runGitWithReporter` routes these lines through `reporter.Warn()` and that method writes them to a terminal without sanitization, the terminal emulator executes the embedded control sequences.

**Practical consequence:** Depending on the terminal emulator, embedded escape sequences can:
- Overwrite previously displayed text (cursor positioning)
- Clear the screen or scroll region
- Change terminal title or window dimensions
- In older or misconfigured terminals: trigger OSC sequence handlers that could, for example, change the working directory displayed in the title bar or open URLs

**Severity assessment: Low-Medium.** Git itself performs no sanitization of server-provided text before writing it to stderr. However, the actual attack surface requires a user to clone from or sync with a repository hosted on a server under attacker control. Users who clone from untrusted servers already accept a wide threat surface (arbitrary code execution via git hooks, malicious repository contents, etc.). Terminal escape injection is a real but secondary concern in that context. The severity is higher if niwa is used in automation pipelines where output is captured by downstream tools that interpret ANSI codes.

**Specific injection vectors in this design:**

1. `reporter.Warn()` is called for lines matching `isGitErrorLine` ("fatal:", "error:", "warning:"). These prefixes can appear in server-side messages.
2. `reporter.Log()` is called for all other lines, including progress frames. Progress frame content (the portion after `\r`) is also not sanitized.
3. The `Status()` method writes its own string (controlled by niwa), so it is not an injection vector.
4. Lines accumulated in `strings.Builder` for error embedding are never written to a terminal directly, so the error accumulation path is safe.

**Recommended mitigation:** Strip non-printable bytes and ANSI escape sequences from git output lines before passing them to `reporter.Warn()` and `reporter.Log()`. A targeted approach: before calling either method, pass the scanner line through a function that removes ESC (`\x1b`) and sequences matching `\x1b\[[0-9;]*[A-Za-z]` (CSI sequences) and `\x1b\][^\x07]*\x07` (OSC sequences). This can be implemented without any new dependency using a small regular expression compiled once at package init. Progress frames with `\r` are already split by line boundary in the goroutine; stripping escapes from the retained portion is sufficient.

Alternatively, classify TTY output separately from non-TTY output and only strip on TTY paths, but stripping universally is simpler and has no practical downside since ANSI codes in error logs are noise.

### Goroutine Lifecycle Safety

**Applies:** Yes

**The pattern as described:**

```go
pr, pw := io.Pipe()
cmd.Stdout = pw
cmd.Stderr = pw
go func() {
    defer close(done)
    scanner := bufio.NewScanner(pr)
    for scanner.Scan() { ... }
}()
runErr := cmd.Run()
pw.Close()
<-done
```

**Leak risk analysis:**

1. **Normal completion path:** `cmd.Run()` returns, `pw.Close()` closes the write end of the pipe, `pr.Read()` in the scanner goroutine receives EOF, the goroutine exits, `<-done` unblocks. This path is correct.

2. **`cmd.Run()` panic:** If `cmd.Run()` panics, `pw.Close()` is never called. The scanner goroutine blocks forever on `pr.Read()`, and `<-done` is never reached. The goroutine leaks. **Mitigation:** `defer pw.Close()` immediately after `pw` is created. With `defer`, panics and early returns both close the write end.

3. **Context cancellation during cmd.Run():** When the context passed to `exec.CommandContext` is cancelled, `cmd.Run()` returns an error. `pw.Close()` is called, the goroutine unblocks. This path is safe provided the fix in point 2 is applied.

4. **Scanner error (`scanner.Err() != nil`):** If `bufio.Scanner` hits a token too long (default max token size is 64KB) or another scan error, `scanner.Scan()` returns false and the goroutine exits—but `pr` is not closed from the read side. The write side (`pw`) is still open. Subsequent writes from the git process's stdout/stderr will block until `pw.Close()` is called after `cmd.Run()` returns. This can cause `cmd.Run()` to hang if the git process is blocked on a write. **Mitigation:** After the scanner loop exits, call `pr.CloseWithError(scanner.Err())` (or unconditionally `pr.Close()`) inside the goroutine before `close(done)`. This unblocks any pending git writes and causes `cmd.Run()` to see a broken pipe, which surfaces as an error rather than a hang.

5. **`io.Pipe` propagation:** `io.Pipe` pairs are synchronous; there is no internal buffer. If the goroutine stops consuming (scanner exits early) without closing `pr`, the git process blocks on write. Point 4's mitigation handles this.

**Summary of required fixes for goroutine safety:**
- Add `defer pw.Close()` immediately after pipe creation (fixes panic and early-return paths).
- Add `pr.Close()` (or `pr.CloseWithError(scanner.Err())`) at the end of the goroutine body, before `close(done)` (fixes scanner-exit-without-drain paths).

Neither fix changes observable behavior on the normal completion path.

---

## Recommended Outcome

**OPTION 2 - Document considerations:**

The design is sound for its stated scope. No blocking security issues exist. Two dimensions warrant documented implementation guidance: ANSI escape injection (a real but low-medium severity risk addressable with a small sanitization function) and goroutine lifecycle safety (two missing defensive patterns that prevent hangs and leaks on abnormal paths). Neither requires a design change; both are implementation-level requirements.

**Security Considerations section for the design document:**

---

### Security Considerations

**Terminal escape injection.** Git relays server-provided text verbatim on stderr. A malicious server can embed ANSI or OSC escape sequences in error messages. When `runGitWithReporter` routes these lines to a terminal via `reporter.Warn()` or `reporter.Log()`, the sequences are executed by the terminal emulator. Mitigate by stripping non-printable characters and escape sequences from each scanned line before passing it to the reporter. A single `regexp.MustCompile` at package init (matching `\x1b\[[0-9;]*[A-Za-z]` for CSI and `\x1b\][^\x07]*\x07` for OSC) applied to every line is sufficient and adds no new dependency. Strip unconditionally, not just on TTY paths, to keep log output clean as well.

**Goroutine lifecycle.** The `io.Pipe` + goroutine pattern requires two defensive additions beyond the design's pseudocode:

1. `defer pw.Close()` immediately after `io.Pipe()` — ensures the write end closes even if `cmd.Run()` panics or an early-return path is added later.
2. `pr.Close()` inside the goroutine after the scanner loop exits — prevents the git process from blocking on a write when the scanner exits early (e.g., due to a token-too-long error), which would otherwise cause `cmd.Run()` to hang indefinitely.

**Supply chain.** `golang.org/x/term` is the only new dependency introduced by this design. Pin it in `go.sum` and review the diff on any future upgrade.

---

## Summary

This design is a UX-only change with a narrow but real security consideration in terminal escape injection: git can relay server-crafted escape sequences that `reporter.Warn()`/`reporter.Log()` will pass to the terminal unfiltered. This is addressable with a small sanitization function and does not require design changes. The goroutine pipe pattern also needs two defensive closes (defer on the write side, explicit close on the read side after the scanner loop) to prevent hangs on abnormal paths. All other security dimensions—artifact handling, permission scope, supply chain, and data exposure—are either not applicable or carry negligible risk given the design's narrow scope.
