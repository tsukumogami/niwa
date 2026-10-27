# release extension: niwa

niwa's declarations for shirabe's `/shirabe:release`, read when it cuts a niwa
release.

## After release

Run in order once the release is published. The version check stops the
list before `niwa apply` when the update didn't land the new binary (tsuku
hasn't seen the release yet, say), so a workspace is never re-applied with the
old one.

- `tsuku update niwa`: update the installed niwa to the newest release. An
  `@v<version>` install would pin the recipe source's ref, leaving a second or
  pinned install that later updates skip.
- `test "$(niwa --version)" = "niwa version $RELEASE_VERSION"`: fail unless
  the binary on PATH reports the released version, which `niwa --version`
  prints as `niwa version X.Y.Z`.
- `cd "${RELEASE_WORKSPACE_ROOT:?not in a niwa workspace}" && niwa apply`:
  re-apply every instance of the workspace this repository is in with the new
  niwa.
