# One-Time Notices

Some informational messages are only useful the first time a user encounters
them. Repeating them on every `niwa apply` adds noise without adding value.
The one-time notice pattern handles this: a notice is shown once per workspace
instance and then recorded in instance state so subsequent runs skip it.

## How it works

`InstanceState` carries a `DisclosedNotices []string` field (JSON:
`disclosed_notices`, omitted when empty). Each notice has a short key string
that identifies it. Before emitting a notice, the pipeline checks whether that
key is already recorded; after emitting it, the key is added and persisted with
the next `SaveState` call.

Two helpers in `internal/workspace/state.go` do the work:

- `noticeDisclosed(s *InstanceState, notice string) bool` — returns true if
  `notice` is already in `s.DisclosedNotices`.
- `mergeDisclosedNotices(existing, added []string) []string` — deduplicating
  union used by `Apply` when building the new state from the old one plus the
  notices emitted during the current run.

Notice keys are per-workspace-instance, not per-user. A second instance of the
same workspace will see the notice on its first apply.

## When to use it

Use a one-time notice for messages that:

- Describe a configuration fact that doesn't change between runs (e.g. "your
  personal overlay shadows the team provider")
- Have no actionable remediation — they're informational, not warnings
- Would be noise if shown on every apply after the user has already seen them

Do not use one-time notices for warnings that reflect the current state of the
workspace (e.g. drift, missing secrets). Those should appear on every run.

## Adding a new notice

Three steps:

**1. Define a key constant** in `internal/workspace/apply.go`:

```go
const noticeMyFeature = "my-feature"
```

Use a short kebab-case string that names the feature or condition being
disclosed.

**2. Guard the emission** in `runPipeline`:

```go
if someCondition && !noticeDisclosed(opts.existingState, noticeMyFeature) {
    a.Reporter.Log("your informational message here")
    newDisclosures = append(newDisclosures, noticeMyFeature)
}
```

`opts.existingState` is nil on the first `Create`, so `noticeDisclosed` returns
false and the notice fires. On `Apply`, it reflects the saved state from the
previous run.

**3. That's it.** `newDisclosures` is already wired into `pipelineResult` and
merged into `DisclosedNotices` by both `Create` and `Apply`.

## Existing notice keys

| Key | Condition | File |
|-----|-----------|------|
| `provider-shadow` | Personal overlay declares a provider that shadows the team config's provider | `apply.go` |

## Testing

Unit tests for `noticeDisclosed` and `mergeDisclosedNotices` live in
`internal/workspace/state_test.go`. Add a test there when you add a new key if
the suppression logic has any non-trivial condition.
