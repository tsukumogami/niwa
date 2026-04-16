# Issue 6 — Maintainer Review (vault-integration PLAN)

Target commit: `ddfbf36f261523ebf4a23f8d068bccadf709a8ca` on `docs/vault-integration`.
Focus: clarity, naming, error messages, discoverability.

## Summary

Overall this change is clean. The new `gitignore.go` is well-documented,
idempotent, and has tight tests. `secretFileMode` is a named constant with
a good explanation. `EnsureInstanceGitignore` has actionable error messages.

The only item that would meaningfully mislead a next developer is the
*silent* rewrite of user-written destinations in `[files]` when the path
lacks `.local` — this is not surfaced in logs, the user-facing config
doc, nor the materializer's own comment on the `files` field. One smaller
item: `injectLocalInfix`'s substring check has implicit edge cases worth
calling out; the containing function comment is close but not quite
there. A further structural readability nit exists in `materializeDir`.

I would approve this PR after the **blocking** item (silent rewrite) is
addressed with either a user-facing log line, a `niwa validate` warning,
or (at minimum) a doc note. I would accept a comment-only fix as
resolution for the blocking finding.

## Findings

### 1. Silent `.local` injection on user-written `[files]` destinations — **Blocking**

File: `internal/workspace/materialize.go:645-685` (`materializeFile`) and
the comment at `internal/workspace/materialize.go:660-669`.

When the user writes:

```toml
[files]
"myconfig.json" = ".tool/config.json"
```

they get `.tool/config.local.json` on disk. The materializer rewrites
the user-written basename without any stderr note, audit record, or
error. The rewrite is covered by `TestFilesMaterializerInjectsLocalInfix`
and there is an inline code comment explaining *why* the materializer
does this, but there is no user-facing signal. The next developer who
hits a bug report ("I told niwa to write `config.json` and it wrote
`config.local.json`") has to trace through the materializer to
understand what happened; worse, a user who relies on the literal
filename (a tool that only reads `config.json`) will silently see no
effect and no diagnostic. The misread risk is *the user's config file
does what it says* — the next developer or user will form exactly that
wrong mental model.

The issue doc (PLAN §6) states "Enforce `.local` filename infix" but
does not say whether enforcement is silent or loud. `niwa` elsewhere
(apply.go:339-357, :525-527) prints stderr lines freely for cloned,
pulled, skipped, and failed repos, so emitting a similar line here
would be consistent with the project's established style.

Fix options, in order of preference:
- Emit a stderr notice on the first injection per apply, e.g.
  `materializer: rewrote %q -> %q to preserve *.local* gitignore`.
- Add a comment block to the `[files]` docstring (and/or a `niwa
  validate` warning) explaining the implicit rewrite.
- If loud-rewrite is undesirable for UX, at minimum document in the
  config-distribution design doc that destination basenames without
  `.local` will be rewritten.

### 2. `injectLocalInfix` substring check has undocumented edge cases — **Advisory**

File: `internal/workspace/materialize.go:38-50`.

The current logic is `strings.Contains(filename, ".local")`. Edge cases
the comment does not cover:

- `my.locale.json` contains `.local` as a substring (via `.locale`) and
  is returned unchanged. Fine in practice (`*.local*` still matches),
  but a next developer reading the comment "ensures the given filename
  contains `.local`" will not anticipate that the substring check also
  satisfies `.localhost`, `.localization.yaml`, `.localeconv.json`,
  `locale.json`, etc. Most of these still match the `*.local*` glob,
  so the behavior is safe — but the coincidence is load-bearing and
  the comment does not flag it.
- Pure dotfile `.localrc`: substring match is true, returned unchanged.
  `*.local*` gitignore will match it (gitignore `*` is permissive about
  leading dots), so the outcome is correct, but the code path relies
  on that gitignore-level detail; the comment only promises matching
  the pattern. A next developer who rewrites the check to be stricter
  (e.g., "exact `.local` segment") would break this case without a
  test catching it. `TestLocalRename` covers `.eslintrc` via
  `localRename` directly but nothing tests `injectLocalInfix` on a
  leading-dot filename.

Recommended: add one or two test cases to lock in the contract
(`injectLocalInfix("my.locale.json") == "my.locale.json"` and
`injectLocalInfix(".localrc") == ".localrc"`), and tighten the
comment to "returns unchanged if `.local` appears anywhere in the
basename (substring match) — callers rely on this being conservative
rather than exact."

The user-asked edge cases behave as expected:
- `foo.bar.json` → `foo.bar.local.json` (via `localRename`; the dot
  in the middle is preserved because `filepath.Ext` only looks at the
  final `.`).
- `secret` (no extension) → `secret.local` (handled by the `ext == ""`
  branch in `localRename`).

Neither needs behavior change, just explicit coverage in the test
table or in the comment.

### 3. Dead structural branch in `materializeDir` — **Advisory**

File: `internal/workspace/materialize.go:721-734`.

The `if strings.HasSuffix(dest, "/")` and the `else` branches compute
identical `targetPath` expressions. A next developer reading the if/else
will assume the two branches differ in some intentional way — they
don't. This is a divergent-twin trap: if someone later needs to treat
directory-dest differently from file-dest (e.g., to not prepend the
rel subdir for a file-dest single-file copy), they will edit one
branch and wonder why the other exists. Collapse to a single
assignment, or add a comment that the branches are intentionally
identical pending some future divergence.

### 4. `secretFileMode` naming and placement — **Pass**

File: `internal/workspace/materialize.go:15-20`.

`secretFileMode` is declared at the top of `materialize.go` with a
four-line doc comment explaining (a) that it applies to every
materialized file, (b) that it fixes a pre-existing `0o644` bug for
non-vault users, (c) that the value is `0o600`. It is used in four
write sites (`materialize.go:423, 538, 680, 745`) and the name itself
reads correctly at each call site (`os.WriteFile(..., secretFileMode)`).
The next developer searching for why their file is not world-readable
will find the constant on first grep. No change needed.

One micro-nit (not flagged): the name says "secret" but the code
comment is clear that the mode applies even when no secret is present.
I considered flagging this as a name-behavior mismatch but the comment
is unambiguous and the name is intentional (the constant records the
*purpose* — secret-file hardening — not the contents). Leave as is.

### 5. `EnsureInstanceGitignore` error messages — **Pass**

File: `internal/workspace/gitignore.go:33-67`.

Three error paths, three distinct messages:
- `reading .gitignore: <err>` — fires only on non-`IsNotExist` read errors.
- `writing .gitignore: <err>` — fresh-file creation path.
- `updating .gitignore: <err>` — merge-and-rewrite path.

The caller wraps once more (`apply.go:86`: `preparing instance
.gitignore`). A next developer seeing `preparing instance .gitignore:
updating .gitignore: permission denied` knows exactly which phase
failed and can act on it. The three distinct verbs (read/write/update)
are useful; they would be even more useful with the path included, but
the caller-side wrap supplies the `instanceRoot` context indirectly
via the `InstanceState` location. Not blocking.

### 6. `hasInstanceGitignorePattern` comment — **Pass**

File: `internal/workspace/gitignore.go:69-83`.

The comment explicitly explains why the check is strict (exact
trimmed-line match) rather than permissive (substring). This is
exactly the kind of "why" comment the next developer needs — without
it, someone would be tempted to "improve" the function by loosening
the match and silently break idempotency.

## Counts

- blocking_count: 1
- non_blocking_count: 2 (findings 2 and 3)

## Verdict

Approve once finding #1 is addressed. A doc-only or comment-only
resolution is acceptable — the code behavior is defensible, the
missing piece is the signal to the user that their literal
destination was rewritten.
