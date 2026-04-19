# Documentation Plan: clone-output-ux

Generated from: docs/plans/PLAN-clone-output-ux.md
Issues analyzed: 4
Total entries: 2

---

## doc-1: README.md
**Section**: Commands
**Prerequisite issues**: #2
**Update type**: modify
**Status**: updated
**Details**: Add `--no-progress` to the Commands table. The flag is persistent (applies to all subcommands) and suppresses the status line regardless of TTY state. Add a brief note below the table explaining that `--no-progress` is the recommended opt-out for CI pipelines and scripts where the animated status line is unwanted.

---

## doc-2: README.md
**Section**: Commands
**Prerequisite issues**: #2, #3
**Update type**: modify
**Status**: pending
**Details**: Update the `niwa apply` and `niwa create` command descriptions to mention the new TTY output behavior: on a TTY, a single status line shows the current operation ("cloning <repo>...", "syncing <repo>...") in place, and completed-repo lines scroll normally. On non-TTY (piped output, CI), output is unchanged from the previous append-only behavior.
