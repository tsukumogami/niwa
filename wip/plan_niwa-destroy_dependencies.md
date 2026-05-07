# Plan Dependencies: niwa-destroy

## Dependency graph (Mermaid)

```mermaid
graph TD
    I1[Issue 1: doc amendments]
    I2[Issue 2: shell wrapper]
    I3[Issue 3: picker copy]
    I4[Issue 4: ClassifyCwd]
    I5[Issue 5: scan.go]
    I6[Issue 6: DestroyWorkspace]
    I7[Issue 7: prompt.go]
    I8[Issue 8: rewrite runDestroy]
    I9[Issue 9: functional tests]

    I5 --> I6
    I2 --> I8
    I3 --> I8
    I4 --> I8
    I5 --> I8
    I6 --> I8
    I7 --> I8
    I8 --> I9
```

## Critical path

`<<ISSUE:5>>` → `<<ISSUE:6>>` → `<<ISSUE:8>>` → `<<ISSUE:9>>`

Length: 4 sequential issues. Total estimated effort: ~1 focused session each = ~4 sessions on the critical path.

## Parallelization opportunities

- `<<ISSUE:1>>` (docs) — fully independent, can land any time.
- `<<ISSUE:2>>` (wrapper), `<<ISSUE:3>>` (picker), `<<ISSUE:4>>` (classify), `<<ISSUE:7>>` (prompt) — independent of each other; can interleave with `<<ISSUE:5>>`+`<<ISSUE:6>>` on the critical path.
- `<<ISSUE:5>>` — gates `<<ISSUE:6>>` only.
- All work converges at `<<ISSUE:8>>`.

## Implementation sequence (recommended for single-pr delivery)

Since this is single-pr (one developer, one branch, one PR), there's no actual parallelization — issues land sequentially on the same branch. Recommended order:

1. **Doc amendments** (`<<ISSUE:1>>`) — safe and small, sets the spec context.
2. **Shell wrapper** (`<<ISSUE:2>>`) — small, independent.
3. **Picker copy** (`<<ISSUE:3>>`) — independent.
4. **`ClassifyCwd`** (`<<ISSUE:4>>`) — independent.
5. **`scan.go`** (`<<ISSUE:5>>`) — independent.
6. **`DestroyWorkspace`** (`<<ISSUE:6>>`) — needs `<<ISSUE:5>>`.
7. **`prompt.go`** (`<<ISSUE:7>>`) — independent.
8. **Rewrite `runDestroy`** (`<<ISSUE:8>>`) — needs all of 2–7.
9. **Functional tests** (`<<ISSUE:9>>`) — needs `<<ISSUE:8>>`.

Issues 1, 2, 3, 4, 7 could land in any order; 5 must precede 6; 8 must follow 2–7; 9 must follow 8.
