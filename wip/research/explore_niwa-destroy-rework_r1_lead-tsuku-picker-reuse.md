# Lead: Tsuku picker reuse

## Findings

### Where the picker lives

The picker is in the **tsuku** monorepo at:

- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/internal/tui/picker.go` (180 lines)
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/internal/tui/sanitize.go` (30 lines)
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/internal/tui/picker_test.go` (217 lines)

Package: `package tui` — Go import path would be `github.com/tsukumogami/tsuku/internal/tui`.

It was added in commit `c8f58101 feat(install): present arrow-driven picker when multiple recipes satisfy an alias (#2369)` — the picker was built specifically to disambiguate which recipe to install when an alias has multiple satisfiers. That commit is the only one that touches `picker.go`, so this is genuinely a recent, single-PR addition matching the user's "recently built" description.

### Public API surface

From `picker.go`:

- `var ErrCanceled = errors.New("picker: canceled")` — sentinel returned when the user hits Ctrl-C.
- `type Choice struct { Name string; Description string }` — one row in the picker. `Name` is the identifier returned to the caller; `Description` is rendered alongside as context.
- `func IsAvailable() bool` — returns `term.IsTerminal(int(os.Stderr.Fd()))`. Caller uses this to decide whether to render the picker or fall back.
- `func Pick(prompt string, choices []Choice) (int, error)` — renders the picker on stderr, returns the chosen **index** (not the Choice itself). Errors with `ErrCanceled` on Ctrl-C, or wrapping errors from `term.MakeRaw` / `stdin.Read`.

There is also an unexported `pick(stdin io.Reader, stderr io.Writer, prompt string, choices []Choice)` for unit tests that injects readers/writers; the public `Pick` wraps it with `os.Stdin` / `os.Stderr`.

`SanitizeDisplayString(s string) string` is also exported from `sanitize.go` and strips ANSI/VT100 escapes from caller-provided strings before rendering — used internally by `render` to defang malicious recipe descriptions.

### How the existing caller wires it

From `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/cmd/tsuku/install_alias.go` lines 59–70:

```go
if installYes || !tui.IsAvailable() {
    return "", errAmbiguousAlias
}

choices := buildPickerChoices(candidates)
prompt := fmt.Sprintf("Multiple recipes satisfy %q. Pick one:", alias)
idx, err := tui.Pick(prompt, choices)
if err != nil {
    return "", err
}
return candidates[idx], nil
```

Pattern: caller builds `[]tui.Choice` from its own domain types, calls `Pick`, and indexes back into its own slice using the returned int. The picker doesn't know anything about recipes — `Choice` is purely strings.

### Dependencies

Only one external dep: `golang.org/x/term`. That's it. No bubbletea, no charmbracelet, no survey, no huh — `tsuku/go.mod` has none of those. Rendering is hand-rolled ANSI escapes (`\x1b[2K`, `\x1b[?25l`, etc.) and arrow-key decoding is a 3-byte buffer match (`ESC [ A` for up, `ESC [ B` for down). The package's own doc comment calls this out: "intentionally small (no external TUI framework) and mirrors the pattern in internal/progress for terminal handling."

niwa's `go.mod` already requires `golang.org/x/term v0.42.0` (tsuku has v0.37.0). No new transitive deps.

### Non-TTY handling

Two-layer protocol:

1. **Caller pre-flight**: callers are expected to call `tui.IsAvailable()` and choose a non-interactive code path (error, default, JSON output) when it returns false. The `install_alias.go` example errors with `errAmbiguousAlias` and emits a structured JSON error so scripts can disambiguate non-interactively.
2. **Defensive layer inside Pick**: if invoked anyway, `Pick` calls `term.MakeRaw` on the stderr fd; if stderr is not a real terminal, `MakeRaw` errors and `Pick` returns `fmt.Errorf("picker: enter raw mode: %w", err)` rather than rendering a broken display. The raw-mode call is guarded by `term.IsTerminal(fd)`, so when stderr is a `*bytes.Buffer` (in tests), the raw-mode branch is skipped and the picker still works against synthetic input — that's how the unit tests drive it.

There is no built-in non-interactive list-and-pick fallback; the package punts that decision to the caller. niwa's destroy command would need its own equivalent (e.g., "destroy requires --instance NAME or a TTY").

### Module / import-path situation

- tsuku module: `github.com/tsukumogami/tsuku` (Go 1.25.8).
- niwa module: `github.com/tsukumogami/niwa` (Go 1.25.3).

These are separate Go modules in separate repos under the same org. The picker lives under `internal/tui/`, which is the **blocker for path A**: Go's `internal/` rule means `github.com/tsukumogami/niwa` cannot import `github.com/tsukumogami/tsuku/internal/tui` — the compiler will refuse. To enable a true module-import path, tsuku would have to move the package out of `internal/` (e.g., to `pkg/tui/` or `tui/`) and tag a release with a stable API.

Since both repos are public and live under the same parent path on disk, a `replace` directive could work for local dev, but anyone outside this workspace would still hit the `internal/` rule.

### Exported vs. internal

Identifiers (`Pick`, `Choice`, `ErrCanceled`, `IsAvailable`, `SanitizeDisplayString`) are all capitalized — public-facing within the package. The package itself is under `internal/`, so the public-facing API is only reachable from other packages within the tsuku module. The picker is not designed for cross-module reuse today.

## Implications

The picker is a 180-line, single-dep, zero-state-machine helper that already matches the UX the user wants. It exposes exactly the right surface: feed it `[]Choice{Name, Description}` and a prompt, get back an index or `ErrCanceled`. niwa already uses `golang.org/x/term` and has the same pattern (see `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/workspace/reporter.go` for hand-rolled TTY detection), so the helper is a natural fit stylistically.

For destroy, this means:

- Build `[]tui.Choice` from the discovered instances (Name = instance name, Description = e.g. branch / last-touched / repos count).
- Gate on `tui.IsAvailable()` — if no TTY, error with the candidate list and instruct the user to pass `niwa destroy <instance>` or `--force`.
- Index the returned int back into the discovered instances and proceed.
- Treat `ErrCanceled` as "user said no": print "Canceled." and exit non-zero without touching state. This matches the safety story for a destructive command.

**Recommended path: (B) Copy the helper into niwa.** Specifically, copy `picker.go` and `sanitize.go` into a new `niwa/internal/tui/` package, plus the test file. Justification:

- Path A (import via Go module) is blocked by the `internal/` rule. Fixing that means tsuku has to relocate the package and commit to a stable public API for an unrelated repo's benefit. That coupling is the wrong direction for a tool that's still actively iterating on its own UX.
- Path B costs ~210 lines of code and one upfront copy. Drift risk is bounded because the picker is feature-complete (arrow keys, Enter, Ctrl-C, that's the whole spec) and niwa would own its copy outright. The attack surface — recipe-supplied descriptions — is tsuku-specific anyway; niwa renders instance names it generated itself, so even the sanitize layer is more conservatism than necessity.
- Path C (extract to a shared library) means a third repo, a release process, and coordinating two consumers. Way too much ceremony for 210 lines.
- Path D (build fresh in niwa) means re-deriving raw-mode handling, escape sequences, and frame-clear logic. The bug surface on a homemade TUI is real; reusing the proven implementation is cheaper and lower-risk.

The deliberate approach: copy the package as `niwa/internal/tui/`, keep the same exported names, and add a comment noting the upstream source and commit (`tsukumogami/tsuku#2369`) so a future maintainer can compare.

## Surprises

- The picker is **under `internal/`**. That's the single biggest constraint on reuse — it kills option A outright unless tsuku is willing to move the package. The user's framing ("just import it") doesn't survive contact with Go's module visibility rules.
- It's **hand-rolled, not bubbletea-based**. That's good for niwa: niwa's `go.mod` is small (cobra, godog, fsnotify, term — that's it). A bubbletea-based picker would have dragged in lipgloss / bubbles / a runtime model loop. This one is 180 lines of `golang.org/x/term` plus ANSI escapes.
- The picker returns an **index, not a Choice or a Name**. That's a tighter contract than survey-style libs and a small but real ergonomic difference for callers.
- niwa **already has an analog pattern** in `internal/workspace/reporter.go` (TTY detection via `term.IsTerminal`) and `internal/cli/create.go` / `apply.go` (calling `term.IsTerminal(int(os.Stderr.Fd()))` to decide whether to enable progress rendering). So the picker copy lands in a code style niwa already knows.
- The picker writes to **stderr, not stdout**. That's the right choice for niwa destroy too — preserves stdout for any structured output the command might emit.

## Open Questions

- Does the destroy redesign also want a "destroy multiple instances" flow (multi-select)? The current picker is single-select only. If yes, that's an extension to the copied code, not a reason to pick a different reuse path.
- Should the copy live at `niwa/internal/tui/` or `niwa/internal/cli/picker.go`? Suggest a dedicated package since other niwa commands (e.g., a future `niwa switch`) might also want a picker.
- What goes in the `Description` column for an instance row? Likely the issue/branch the instance was opened against, possibly a "modified N minutes ago" hint. Needs design input from the destroy spec.
- Is the shell-wrapper landing-path protocol (mentioned in the parent exploration) something the picker has to know about, or is it strictly the destroy command's responsibility after the picker returns? Strongly suspect the latter — picker should stay UX-only.
- Should niwa's copy keep `SanitizeDisplayString`? Probably yes, even though instance names are niwa-generated, because a future destroy that surfaces externally-sourced strings (branch names, commit messages) would want defense in depth.

## Summary

The picker lives at `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/tsuku/internal/tui/picker.go` (added in tsuku PR #2369), exports `Pick(prompt, []Choice) (int, error)` plus `IsAvailable()` and `ErrCanceled`, and depends only on `golang.org/x/term` — which niwa already requires. Recommended reuse path is **(B) copy into niwa** at `niwa/internal/tui/`, because the package's `internal/` location blocks a true module import and the helper is small enough that a one-time copy beats coordinating cross-repo releases. The biggest open question is whether destroy ever needs multi-select; if so, the copied picker grows a second entry point rather than changing the reuse decision.
