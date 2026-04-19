<!-- decision:start id="clone-output-capture" status="assumed" -->
### Decision: Git subprocess stderr capture and classification

**Context**

Niwa runs git subprocesses (clone, fetch, pull) and setup scripts with
`cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr`, which passes git output
directly to the terminal file descriptor. This works today but it bypasses
the Reporter abstraction (established in Decision 1) and prevents inline
progress display — the goal of this design effort.

The complication is that `cmd.Run()` returns an `*exec.ExitError` whose
`Error()` string is always "exit status N". It does not embed stderr content.
Git's diagnostic lines ("fatal: repository not found", "error: authentication
failed") live only in the stderr stream. Any approach that suppresses stderr
without separately capturing error-classified lines silently drops actionable
diagnostic information — a clear regression.

`overlaysync.go` deliberately suppresses all git output for two load-bearing
reasons: clone failure is an expected outcome (the overlay repo may not exist)
and the overlay repo name must not leak to terminal output. This suppression
must remain unchanged.

**Assumptions**

- The Reporter abstraction (Decision 1) exposes at least a progress/info
  channel and an error channel (or a single channel with a severity parameter).
  If Reporter has only a single output method, classification is still useful
  for driving spinner vs. error display logic.
- Apply loop callers have access to a Reporter instance to pass into Cloner,
  FetchRepo, PullRepo, SyncConfigDir, and RunSetupScripts. Function signatures
  at these sites will require a Reporter parameter.
- bufio.Scanner with default ScanLines (\n delimiter) naturally elides
  \r-terminated git progress frames, producing only \n-terminated meaningful
  lines as scan tokens. This assumption is consistent with the research insight
  documented in the decision background.

**Chosen: Goroutine pipe with line-by-line classification**

Replace `cmd.Stderr = os.Stderr` with an `io.Pipe()`. A goroutine reads the
pipe with `bufio.Scanner` (default \n splitting), classifies each line by
prefix ("fatal:", "error:", "warning:"), and routes it through Reporter.
Error-classified lines are also accumulated so they can be embedded in the
error returned by `cmd.Run()` when it fails.

The pattern is the same at each call site (clone.go, sync.go, configsync.go,
setup.go) and can be extracted into a shared helper:

```go
func runGitWithReporter(ctx context.Context, r Reporter, cmd *exec.Cmd) error {
    pr, pw := io.Pipe()
    cmd.Stdout = pw
    cmd.Stderr = pw

    var errLines strings.Builder
    done := make(chan struct{})
    go func() {
        defer close(done)
        scanner := bufio.NewScanner(pr)
        for scanner.Scan() {
            line := scanner.Text()
            if isGitErrorLine(line) {
                r.Error(line)
                errLines.WriteString(line + "\n")
            } else {
                r.Progress(line)
            }
        }
    }()

    runErr := cmd.Run()
    pw.Close()
    <-done

    if runErr != nil {
        if detail := strings.TrimSpace(errLines.String()); detail != "" {
            return fmt.Errorf("%s", detail)
        }
        return runErr
    }
    return nil
}
```

`isGitErrorLine` checks for the "fatal:", "error:", or "warning:" prefix that
git uses consistently across all error conditions.

**Rationale**

This is the only option that satisfies all three requirements simultaneously:
it preserves git error detail, enables inline progress display during clone
and fetch operations, and cleanly handles \r-terminated progress frame noise
via Scanner's natural \n-splitting. Full suppression violates the hard
constraint against silently swallowing errors. Buffer-and-replay suppresses
progress during the operation (contradicting the design goal) and produces
noisy, \r-polluted output when replaying errors through Reporter. The goroutine
is low-cost in the sequential apply loop and its complexity is contained by
extracting it into a single shared helper function.

**Alternatives Considered**

- **Full suppression**: Set cmd.Stdout/Stderr to nil. On failure, only "exit
  status N" surfaces — git's "fatal: repository not found" is permanently lost.
  Rejected: violates the hard constraint that error messages must not be
  silently swallowed.
- **Buffer-and-replay on error**: Capture all git output in a bytes.Buffer;
  discard on success, replay on failure. Rejected: provides no inline progress
  display (the design goal is unmet), and the replayed buffer on failure
  contains raw \r-terminated progress frame noise that renders incorrectly
  through Reporter.

**Consequences**

- clone.go, sync.go, configsync.go, and setup.go gain a Reporter parameter.
  Callers (the apply loop) pass the active Reporter instance.
- overlaysync.go is unchanged.
- A shared `runGitWithReporter` helper (or equivalent) encapsulates the
  pipe/goroutine/scanner pattern so each call site doesn't repeat it.
- Error messages returned to callers are richer: they include git's own
  diagnostic text rather than just "exit status N".
- Inline progress lines from git (completion messages, object counts) flow
  through Reporter and can be rendered as a spinner update or suppressed,
  depending on Reporter's implementation.
<!-- decision:end -->
