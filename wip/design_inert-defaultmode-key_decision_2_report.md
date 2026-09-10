# Decision 2: How the materializer maps posture to a permission mode, and where invalid values are rejected

resolution: inline (Decision-bypass-with-inline-resolution, parent `/scope`)
complexity: standard
status: complete
chosen: B -- replace the mapping table with an explicit function returning (mode, write, error); keep validation at materialize time with an error naming the accepted values
confidence: high

## Question

How does `buildSettingsDoc` express `bypass` as no `permissions.defaultMode`
and `ask` as `"default"`? And where is an invalid declared value rejected, so
that every level, including the personal overlay, fails with an error naming
the accepted values and no document carries it?

## Constraints

- `bypass`: no `defaultMode`. `ask`: `"default"`. Undeclared: nothing (R6-R8).
- Only `default`, `acceptEdits`, `plan`, and `dontAsk` may ever be written (R5).
- `permissions` may be `vault://`-backed (`maybeSecretString`), so its
  plaintext exists only during materialization.
- The worktree-delegation `deny` entries share the permissions map and must
  keep coexisting with whatever posture is written (R7: every other key stays
  identical).

## Options

**A. Change the table's values.** Map `bypass` to `""`, treat `""` as "don't
write", and map `ask` to `"default"`.
- For: the smallest edit.
- Against: an empty string in a table named for Claude Code modes becomes a
  hidden sentinel. The table would still read as "the Claude mode for this
  posture", which is the false claim this feature removes, and a future entry
  added with `""` by mistake would silently write nothing.

**B. An explicit function (chosen).** `claudeDefaultMode(posture string)
(mode string, write bool, err error)` returns `("", false, nil)` for `bypass`,
`("default", true, nil)` for `ask`, and an error naming `bypass` and `ask` for
anything else. The recognized-mode set lives beside it as the one place R5 is
enforced.
- For: "write nothing" is a first-class result, not an encoding. The doc
  comment can say plainly why `bypass` writes nothing: the posture travels on
  the dispatch flag. The function is the natural unit for the differential
  test.
- Against: slightly more code than A.

**C. Validate at config load instead of materialize time.** Reject unknown
values when `workspace.toml` and the overlays are parsed.
- For: fails earlier, before any document is written.
- Against: a `vault://` value has no plaintext at load time, so load-time
  validation can't see it. Materialize-time validation is still required, and
  C alone doesn't meet R10. Adding it as well as materialize-time validation
  would create two error paths for one rule.

## Rationale

B makes the new semantics legible where a reader looks, and it keeps the one
validation site that can see every value, including vault-backed ones. The
error text changes to name the accepted values. The existing behavior that an
unknown value fails materialization before its document is written already
satisfies "no document carries it", and it applies at every level because the
personal-overlay value flows into the same merged config the materializer
reads.

## Assumptions

- Every generated document, including the workspace-root one, is built by
  `buildSettingsDoc`, so one function covers all four locations.

## Rejected

A (hidden sentinel in a table that keeps making the retired claim),
C (can't see vault-backed values; duplicates the rule).
