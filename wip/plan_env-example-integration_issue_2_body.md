---
complexity: testable
complexity_rationale: The parser is a standalone pure function with no external dependencies. All syntax variants are independently exercisable via table-driven tests against string inputs, with no filesystem setup beyond a temp file path.
---

## Goal

Implement `parseDotEnvExample`, a new package-private function in `internal/workspace/env_example.go` that parses `.env.example` files using Node-style syntax with per-line tolerance.

## Context

`ResolveEnvVars` currently delegates to `parseEnvFile`, a minimal splitter that handles only bare `KEY=VALUE` pairs. Feeding a typical Node-ecosystem `.env.example` through it produces incorrect values or silently drops lines with quoted values or `export` prefixes.

The `.env.example` integration feature requires a richer parser with a fundamentally different error model: individual malformed lines warn and parsing continues rather than aborting the whole file. The two contracts cannot coexist in `parseEnvFile` without silently changing existing callers, so this parser lives as a distinct function.

This function is the foundation for Phase 4 (pre-pass integration), which calls `parseDotEnvExample` and stores results on `MaterializeContext`. Issue 4 depends directly on the signature and behavior established here.

Design: `docs/designs/current/DESIGN-env-example-integration.md`

## Acceptance Criteria

- `internal/workspace/env_example.go` exports (package-private) `parseDotEnvExample(path string) (map[string]string, []string, error)`.
- The `[]string` return carries per-line warning strings in the format `file:line:problem`. No value text, value fragment, or entropy score appears in any warning string.
- The `error` return is non-nil only for whole-file failures (permission denied, binary content detection, file larger than 512 KB). Per-line parse errors do not set the error return.
- **Precondition**: this function is called only after `os.Lstat` confirms the path exists and is not a symlink (the pre-pass in Issue 4 handles absence and symlink detection). File-not-found is not a valid input; tests must not call `parseDotEnvExample` with a nonexistent path.
- Single-quoted values are treated as literals: no escape processing occurs inside single quotes.
- Double-quoted values support the escape sequences `\n`, `\t`, `\"`, and `\\`. Other backslash sequences produce a per-line warning and the line is skipped.
- The `export KEY=VALUE` prefix is accepted; `export` is stripped and the remainder is parsed normally.
- CRLF line endings (`\r\n`) are normalized to `\n` before processing.
- Blank lines and lines whose first non-whitespace character is `#` are skipped silently.
- Key names are validated against `[A-Za-z0-9_]`. A key containing any other character produces a per-line warning and the line is skipped; it is not included in the output map.
- A line with no `=` separator produces a per-line warning and is skipped.
- Lines with a valid key but an empty value are included in the output map with an empty string value.
- Duplicate keys: the last occurrence wins (consistent with Node dotenv behavior).
- `internal/workspace/env_example_test.go` provides table-driven tests covering every syntax variant listed above, including boundary cases for single-quote literals, double-quote escape sequences, `export` prefix, CRLF, blank lines, comment lines, invalid key characters, missing `=`, empty values, and duplicate keys.
- `go test ./internal/workspace/...` passes with no failures.

## Dependencies

None

## Downstream Dependencies

Issue 4 (pre-pass integration) calls `parseDotEnvExample(path)` directly and relies on:

- The exact signature `(map[string]string, []string, error)`.
- The whole-file-error vs. per-line-warning distinction: a non-nil error causes the pre-pass to emit a single warning and skip the repo; non-empty `[]string` warnings are emitted to `EnvMaterializer.Stderr` line by line.
- The guarantee that no value text appears in warning strings (required for the R22 diagnostic safety invariant enforced in Issue 4's integration tests).
