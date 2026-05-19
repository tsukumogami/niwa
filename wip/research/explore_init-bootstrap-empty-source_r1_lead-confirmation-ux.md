# Lead: What confirmation UX fits the empty-remote fallback?

## Findings

### Existing niwa confirmation patterns

niwa today has a small, well-defined idiom set. Cataloged by surface:

| Pattern | Surface | Gate? | Trigger | Reference |
|---------|---------|-------|---------|-----------|
| `note: ...` | stderr | No (informational) | Vault bootstrap, name override, task-redelegate fork | `init.go:365, 508`, `task_redelegate.go:127` |
| `warning: ...` (lowercase) | stderr | No (proceed anyway) | Registry/state write failures, channel env vars, deprecated flags | `init.go:330,343,596,623`, `apply.go:130,183`, `channels.go:64` |
| `WARNING: ...` (uppercase, blank line after) | stderr | No (already happened) | `--rebind` registry retarget — flagged because it's a destructive registry write that lands an audit trail | `init.go:357-359` |
| Rank-2 deprecation notice | stderr (via Reporter) | No (one-time per workspace, persisted in `DisclosedNotices`) | Source repo resolves to legacy rank-2 layout | `init.go:275,283,592,619`, `apply.go:311-317,454-459` |
| One-time notice mechanism | stderr (via Reporter) | No | Configuration facts that don't change between runs (e.g., provider-shadow) | `docs/guides/one-time-notices.md` |
| Typed-confirmation prompt | stderr prompt + stdin read | YES (gates the destructive action) | `niwa destroy` when scan detects non-pushed work | `destroy.go:116-127, 328-339` |
| Interactive picker | stderr renders, stdin reads | YES (selects which instance) | `niwa destroy` at workspace root with ≥2 instances and no `--force` | `destroy.go:226-262`, `tui/picker.go` |
| `--force` flag bypass | (skips both scan and prompt) | Bypasses gate | `niwa destroy --force` for scripted use | `destroy.go:18-20, 108` |

Three key observations:

1. **Prompts are reserved for irreversible-on-the-filesystem operations.** Today's only prompt-gated action is `niwa destroy` (and its workspace-wipe variant). The bar for a prompt is "this action will destroy work the user didn't push."
2. **`--rebind` is destructive-but-recoverable** (rewrites a registry entry, doesn't touch disk) and uses an **after-the-fact uppercase WARNING**, not a prompt. The user can re-bind it back. The blank line after the WARNING (`init.go:358`) intentionally raises its visual weight.
3. **`--overlay` cloning a private repo is silent on success** (just emits the rank-2 notice if applicable). Convention-based overlay discovery also runs silently and silently skips failures (`init.go:602-630`). The user opts in by passing the flag; niwa doesn't second-guess.

### TTY detection / interactive-mode

niwa already has the primitives needed and uses them consistently:

- **`cli.IsStdinTTY()`** in `internal/cli/prompt.go:21` — single-line wrapper around `golang.org/x/term.IsTerminal(int(os.Stdin.Fd()))`. The package-level doc-comment for `prompt.go` explicitly says the helpers are "kept generic so future commands (e.g., a hypothetical irreversible operation) can reuse them."
- **`cli.ReadConfirmation(prompt, expected, in, out)`** in the same file — reads a single line and matches against `expected`. Mismatch returns `(false, nil)` so the caller chooses the policy; EOF on an empty read returns `(false, err)`.
- **Reporter TTY-awareness**: `workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))` in `apply.go:122` and `create.go:153`. Used to gate progress-bar rendering but not prompts.
- **`tui.IsAvailable()`** in `internal/tui/picker.go:45` — checks `term.IsTerminal(int(os.Stderr.Fd()))`. The destroy command pairs `IsStdinTTY() && tui.IsAvailable()` before showing the picker.

The pattern in `destroy.go` is the template to follow:

```go
if !IsStdinTTY() {
    return fmt.Errorf("... and stdin is not a terminal; aborting (resolve X, or use --force ...)")
}
matched, err := ReadConfirmation("> ", expectedToken, os.Stdin, cmd.ErrOrStderr())
```

Non-TTY context fails closed with an actionable error pointing at the `--force` (or analogous) escape hatch. Interactive context prompts.

### Option assessments (interactive vs. non-interactive)

**Note on the worktree-session framing.** The exploration scope mentions landing on a branch in a niwa worktree session. That paradigm doesn't exist for `niwa init` today — worktrees are per-repo (`niwa session create`), created under an already-applied instance. So "the worktree IS a confirmation gate" can mean two different things:

- (a) Scaffold writes a `.niwa/workspace.toml` on the user's main branch in cwd, then prints next steps. The user inspects the file before doing anything else. This is what `niwa init` does today on the no-args path.
- (b) Build new infrastructure that creates a feature branch in the cloned (empty) repo and lands the scaffolded config there for the user to push. This doesn't exist yet and is a much larger change.

Sub-bullets below assume (a) unless explicitly noted, since (b) requires its own design.

#### Option 1: Silent scaffold (worktree gate is the only check)

- **Interactive UX**: User runs `niwa init myws --from org/empty-repo`. niwa clones, finds no config, scaffolds. Success message says "Workspace 'myws' initialized at /path from remote config." Identical to a successful materialize. **No signal whatsoever** that the remote was empty and niwa scaffolded instead.
- **Non-interactive UX**: Same. Script/CI sees a success exit code; everything looks normal. The next `niwa apply` will succeed scaffolded-but-empty (no sources, no groups), which is correct behavior but obscures intent.
- **Risk**: A typo (`org/empty-rpo` happens to also exist empty somewhere) produces an indistinguishable success. The user discovers the mistake at `niwa apply` time when nothing materializes the way they expected. The failure is recoverable (delete and re-init) but cost a debugging cycle.

#### Option 2: Stderr notice + scaffold (informational, no prompt)

- **Interactive UX**: niwa prints `note: <source> contains no workspace.toml; scaffolded a minimal one at <path>. Edit it and run niwa apply.` then exits success. Matches the `note:` idiom for vault bootstrap (`init.go:508`) and the override note (`init.go:365`). The user sees the surprise immediately on the success path.
- **Non-interactive UX**: Identical. CI captures the note on stderr; success exit code. Scripts that redirect stderr lose the note, but the success message on stdout still correctly describes what happened (could be tightened: "scaffolded from empty remote" vs. "initialized from remote config").
- **Risk**: A user who isn't reading stderr can still proceed without noticing the empty-remote case. But this is consistent with how niwa handles every other configuration surprise (vault bootstrap, override, rank-2). The pattern says: surprises go on stderr; users are expected to read what their tools say.

#### Option 3: Prompt before scaffolding

- **Interactive UX**: niwa prints "Source <url> has no workspace.toml. Scaffold a minimal one? [Y/n]" and reads stdin. Happy path: user types Enter, scaffold proceeds, success message identical to Option 2. Typo path: user sees the empty-remote surprise, types `n`, init exits without writing.
- **Non-interactive UX**: niwa MUST detect `!IsStdinTTY()` and either (a) abort with an actionable error pointing at a `--bootstrap` flag, or (b) auto-accept silently. The destroy idiom is (a); applying it here means every CI invocation of `niwa init --from <empty>` needs to add `--bootstrap`. Auto-accept would defeat the prompt's purpose because the surprise case would slip through silently.
- **Risk**: Adds friction to the happy path. The user *just specified the remote*. They opted in. A `[Y/n]` prompt every time is the same kind of noise niwa avoided for `--overlay` cloning (which silently clones a whole private repo). It also breaks the "first command, before the user has even seen anything" use case for `--from <empty>` — they have no context to consent against.

#### Option 4: Require explicit `--bootstrap` flag (no fallback)

- **Interactive UX**: `niwa init myws --from org/empty-repo` fails with "source has no workspace.toml; pass --bootstrap to scaffold one, or push a workspace.toml to the remote and retry." User re-runs with `--bootstrap`. Two steps for the happy path.
- **Non-interactive UX**: Identical. CI invocation must include `--bootstrap` to handle the empty-remote case. Explicit, scriptable, no TTY check needed.
- **Risk**: This is the most consistent option with niwa's existing "explicit user intent" idiom — `--overlay` is explicit clone-this-repo intent (clone failure is a hard error per `init.go:589`); `--no-overlay` is explicit skip-discovery; `--rebind` is explicit accept-the-collision. Bootstrap-on-empty-source becomes another explicit-intent flag. The cost is the empty-remote case never works first-try; the gain is that **every fall-back to scaffolding is a user-typed flag**, leaving zero room for typo-induced silent scaffolds.

## Recommendation

**Option 2 (stderr notice + scaffold) for the default path, with Option 4 (`--bootstrap` flag) available for callers who want the loud, explicit form.**

Grounding:

1. **niwa's existing idiom for "informational surprise on the success path" is the stderr `note:` line.** Vault bootstrap (`init.go:508`), name-override (`init.go:365`), task-redelegate fork (`task_redelegate.go:127`) all use it. The empty-remote fallback fits the same shape: the action proceeded, you should know about this fact, no remediation needed unless the user wants to push a different config.
2. **Prompts cost more than they protect here.** Today's prompt-gated action (destroy) costs the user nothing in the happy path — there's nothing to destroy yet, so the scan-then-skip path never prompts. Adding a `[Y/n]` to init's happy path would be the first command in niwa where the user has to type "y" to get the expected behavior of a flag they just passed. The destroy precedent (prompt only when destruction is at stake) argues against it.
3. **The typo case is real but bounded.** A typo'd slug almost always either (a) returns 404 (loud error today via the materialize 404 path) or (b) resolves to a repo with config (loud surprise: wrong workspace cloned, user sees unexpected groups/repos). The narrow case where (c) the typo'd slug resolves to a *different empty repo* is rare, recoverable (delete and re-init), and the stderr note still surfaces "this remote was empty" so the user gets a chance to catch it before running `niwa apply`.
4. **`--bootstrap` as an opt-in flag covers the agent/CI use case.** Scripts that *expect* the empty case (e.g., automated workspace provisioning from a template-empty repo) can pass `--bootstrap` to express that intent explicitly. The flag is documented but optional. When not passed, the stderr note is the surface. This is symmetric with `--overlay` (explicit intent: clone and hard-fail) vs. convention discovery (implicit intent: try, silently skip).

In the happy path, the user types nothing extra and sees a single `note:` line. In the surprise path, the same line on stderr gives them a beat to notice before they run `niwa apply`. In the explicit path, `--bootstrap` documents intent for scripts and reviewers. None of these paths requires a TTY check.

## Implications

- **No new TTY-detection code needed.** `IsStdinTTY()` and `ReadConfirmation` stay reserved for destructive operations. The empty-remote case is non-destructive (it writes a new file in a directory niwa created milliseconds earlier).
- **Message wording matters.** The `note:` line should mention both the cause (empty source) and the action (scaffolded a minimal config at $path), matching the format vault bootstrap uses ("note: this workspace declares a vault (kind: %s). Bootstrap with: ...").
- **Success-message branching.** `printSuccess` (`init.go:642-672`) currently has three modes (Scaffold, Named, Clone). The empty-remote fallback is conceptually a fourth: "Clone landed in Scaffold." Either a new mode `modeCloneScaffolded` with its own success text, or the existing `modeClone` text plus the stderr note. The latter is less invasive but the former gives the user a clearer "next steps" block (edit the toml first, then apply).
- **`--bootstrap` interacts with `--from`.** Both flags should be allowed together; `--bootstrap` without `--from` is meaningless (scaffold-only is already the no-args mode) and should be rejected at flag-parse time.
- **State propagation.** If the scaffolded path needs to be distinguished by `niwa apply` later (e.g., to gate a "you scaffolded this but never pushed it" warning), an `InstanceState` field could record it. Probably YAGNI for v1 — the workspace.toml file itself is the source of truth.

## Surprises

- **The exploration question's worktree-handoff framing isn't a current niwa idiom for `init`.** `niwa init` lands the user in cwd or `cwd/<name>/` on the main branch (no branch creation, no worktree). The worktree-session paradigm exists for `niwa session create`, which operates on a repo *inside* an already-applied instance. Treating the worktree as a confirmation gate for init would require new infrastructure (init-time branch creation, post-apply session-style handoff). That's a separate design surface; the recommendation above works within the existing init shape.
- **`--rebind` uses an uppercase WARNING after the fact, not a prompt.** The action is destructive (rewrites a registry entry tied to a directory) and yet niwa lets it through without confirmation, on the rationale that an automated agent still leaves an audit trail. The empty-remote fallback is *less* destructive (no existing state is overwritten — the workspace dir was just created) so a softer surface (lowercase `note:`) is consistent with this gradient.
- **The team rejected silent overlay clones**: convention-based overlay discovery silently skips on failure (`init.go:612-616`), but explicit `--overlay` is a hard error. So niwa already encodes "explicit user intent → loud; convention → silent." The empty-remote case has no user-typed intent to scaffold (the user typed `--from`, not `--scaffold`), so the loud-but-non-blocking middle of stderr notice is the right vertical position.

## Open Questions

- **Should the scaffold use the `--from` slug to populate any TOML fields?** E.g., a comment near `[workspace]` saying `# Cloned from org/repo (was empty)`. Useful audit trail, easy to write, but new behavior. Probably a follow-up not gating the recommendation.
- **Does the scaffolded `[workspace] name` come from the positional `<name>` arg, or default to `"workspace"`?** The cloned-config path uses the override mechanism. The fallback should match: if the user passed `niwa init myws --from <empty>`, the scaffolded config should have `name = "myws"`. Trivial change to the existing `Scaffold(workspaceRoot, name)` call.
- **Should a one-time `DisclosedNotices` key be added for the empty-remote scaffold?** The user runs `niwa apply` next; if apply re-emits the "empty remote, scaffolded" note, that's noise. But the note is on init's success path, not apply's pipeline, so this is probably moot — the one-time-notice mechanism is for apply-time runtime, not init.
- **404 vs. empty repo: are they distinguishable to the materialize path?** A 404 from the GitHub tarball API is a hard error today. An empty-but-existing repo (HEAD points at no commits) might 404 the tarball endpoint too, or might 200 with an empty tar. The fallback design depends on whether the materializer can distinguish "no such repo" from "repo exists, no workspace.toml." Worth a separate research lead.
- **Should `--bootstrap` also imply `--no-overlay`?** Convention overlay discovery runs against the workspace source URL. For an empty source, the overlay almost certainly doesn't exist either. Currently the silent-skip path handles this gracefully (clone fails, init proceeds). No change needed, but worth noting.

## Summary

niwa's existing confirmation idiom reserves typed prompts for destructive filesystem operations (destroy) and uses stderr `note:` lines for informational surprises on the success path (vault bootstrap, name override, rank-2 deprecation), with `--force`-style flags as the non-interactive escape hatch. The recommendation is an Option 2 + Option 4 pairing: stderr `note:` on the auto-fallback path so the empty-remote surprise surfaces without forcing the user to type "y" in their happy path, plus an opt-in `--bootstrap` flag that scripts and agents can pass to express explicit empty-remote-scaffold intent (matching how `--overlay`/`--no-overlay` encode explicit overlay intent today). No new TTY-detection code is needed; `IsStdinTTY()` and `ReadConfirmation` stay reserved for genuinely destructive operations.
