---
name: niwa-migrate-config
description: Help the user migrate a niwa workspace config source from the deprecated rank-2 whole-repo layout to the rank-1 `.niwa/workspace.toml` layout.
---

# /niwa:migrate-config

Use this skill when the user wants to migrate a workspace config source from the deprecated rank-2 layout (where `workspace.toml` sits at the repo root and niwa clones the entire repository) to the rank-1 layout (where `.niwa/workspace.toml` sits under a `.niwa/` subdirectory and niwa fetches only that subtree).

This skill is invoked as `/niwa:migrate-config <workspace-name>`.

## What this skill is for

niwa shows a one-time `note:` after `niwa apply` whenever a workspace config source uses the rank-2 layout. The note points the user here.

There are two paths the user may want:

- **Path (a) — in-place restructure**: the source repository's maintainer adds a `.niwa/` directory, moves `workspace.toml` into it, and pushes. After that, the source carries both rank-1 and rank-2 markers, which niwa rejects as ambiguous. The maintainer also needs to delete the root `workspace.toml`. Niwa's registry entry is untouched.

- **Path (b) — slug swap**: the user creates a NEW source repository that holds only the rank-1 layout, then rewrites the `source_url` in `~/.config/niwa/config.toml` to point at the new repo. The old repo is left alone.

## How to use this skill

1. Run `niwa source inspect <slug> --json` via the Bash tool against the current source. Inspect the `resolved.rank` and `resolved.deprecated` fields to confirm the source is rank-2.

2. Read `~/.config/niwa/config.toml` via the Read tool to discover the current `source_url` for the workspace.

3. Present both paths to the user. For each path, surface:
   - Concrete file edits the user (or the source-repo maintainer) needs to make
   - Whether the niwa registry needs to be edited
   - The follow-up niwa command to refresh the workspace snapshot

4. If the user chooses path (a):
   - Provide the exact file-move commands (`mkdir .niwa && git mv workspace.toml .niwa/workspace.toml`)
   - Remind the user that the source repository maintainer needs to commit and push the change
   - Print the follow-up command: `niwa apply --force <workspace-name>` so the workspace re-discovers and snapshots only the `.niwa/` subtree

5. If the user chooses path (b):
   - Confirm the new repository's slug
   - Run `niwa source inspect <new-slug> --json` via Bash to verify the new repo carries the rank-1 layout cleanly
   - Edit `~/.config/niwa/config.toml` via the Edit tool to rewrite the `source_url` for the workspace
   - Print the follow-up command: `niwa apply --force <workspace-name>` so the workspace switches to the new source

## What this skill MUST NOT do

- MUST NOT run `git push`, `niwa apply`, or any other materializing command
- MUST NOT modify the workspace snapshot at `<workspace>/.niwa/`
- MUST NOT emit any deprecation or disclosure notices (niwa owns those)
- MUST NOT delete or move files in the source repository on the user's behalf — at most show the commands; let the user run them

The skill is advisory: it inspects, plans, and edits `~/.config/niwa/config.toml` only.
