<!-- decision:start id="niwa-mesh-skill-installation" status="assumed" -->
### Decision: niwa-mesh skill installation and content

**Context**

PRD R9-R12 fixes most of the artifact contract: a directory-per-install at
`<instanceRoot>/.claude/skills/niwa-mesh/` (mirrored to each
`<repoDir>/.claude/skills/niwa-mesh/`), containing a `SKILL.md` with YAML
frontmatter (`name`, `description`, `allowed-tools`) followed by a body with
six fixed section headings. Overwrite on apply; emit a drift warning when
the on-disk file was hand-edited; do not back up; do not implement override
detection. `## Channels` in workspace-context.md shrinks to role, instance
root, MCP tool names, and a pointer. What the PRD leaves open is mechanical:
flat vs layered SKILL.md, uniform vs per-role content, and exact idempotency
plumbing.

The existing codebase already has the machinery this installer needs. File
writes flow through `*writtenFiles`; pipeline step 7 turns written paths
into `ManagedFile` entries with SHA-256 hashes; `writeFileMode` handles
atomic tmp-then-rename with explicit mode. No new state-schema work is
required.

**Assumptions**

- Claude Code's skill frontmatter field set is `{name, description,
  allowed-tools}`. If it adds required fields later, the template extends
  without changing the installer contract.
- Workers (`claude -p`) do not read workspace-context.md — they read their
  task envelope. So the Channels section's `Role: <role>` line can be
  hardcoded to `coordinator` at the instance root (the only place
  workspace-context.md exists) without confusing workers.
- A single uniform skill body triggers the LLM reliably enough across both
  coordinator and worker contexts. If empirical data later shows per-role
  triggering is measurably better, splitting into `niwa-mesh-coordinator`
  and `niwa-mesh-worker` is a non-breaking follow-up.

**Chosen: Flat uniform SKILL.md with in-memory-hash idempotency**

Install a single `SKILL.md` per target directory (instance root plus each
cloned repo). YAML frontmatter carries exactly three fields:

- `name: niwa-mesh`
- `description:` — front-loaded with when-to-use guidance (naming
  `niwa_delegate`, `niwa_report_progress`, `niwa_finish_task`, `niwa_ask`
  as primary trigger tools; covers sync/async dispatch, task state machine,
  message vocabulary, completion contract; applies to any agent in a
  niwa-managed workspace with channels enabled). Total frontmatter +
  description budgeted under 1,200 chars to leave headroom under Claude
  Code's 1,536 cap.
- `allowed-tools:` — enumerated list of all 11 niwa MCP tools: `niwa_delegate`,
  `niwa_query_task`, `niwa_await_task`, `niwa_report_progress`,
  `niwa_finish_task`, `niwa_list_outbound_tasks`, `niwa_update_task`,
  `niwa_cancel_task`, `niwa_ask`, `niwa_send_message`, `niwa_check_messages`.

Body contains the six R10 section headings in order: `## Delegation (sync
vs async)`, `## Reporting Progress`, `## Completion Contract`, `## Message
Vocabulary`, `## Peer Interaction`, `## Common Patterns`. Content is
uniform across all installs; role-scoped guidance within each section uses
short callouts ("When you are dispatching...", "When you are executing a
delegated task..."). Total body size target: 3,000-6,000 chars.

**Installer integration.** Add `InstallMeshSkill(instanceRoot,
repoDirs, prevState, warnFn, writtenFiles)` called from
`InstallChannelInfrastructure` after the `.mcp.json` writes. For each
target directory:

1. Compute intended SKILL.md bytes from a single Go string template.
2. Hash the intended bytes (sha256, `"sha256:" + hex`).
3. If a `ManagedFile` exists for the target path in `prevState`:
   - Hash the on-disk file. If absent, treat as drift-absent and write.
   - If on-disk hash == recorded hash AND recorded hash == intended hash:
     skip the write (preserves mtime, avoids git diff).
   - If on-disk hash == recorded hash AND recorded hash != intended hash:
     overwrite silently (legitimate upgrade).
   - If on-disk hash != recorded hash: drift detected. Call warnFn with
     the path and the personal-scope override pointer, then overwrite.
4. If no previous `ManagedFile` for the path:
   - If file absent on disk: write.
   - If file present on disk: drift (user hand-placed a copy), warn, overwrite.
5. In every case, append the path to `*writtenFiles`. Pipeline step 7
   records a fresh `ContentHash` in the new `ManagedFile` entry.

Use `writeFileMode(path, data, 0o644)` for the actual write. The file is
not a secret; users should be able to read it without elevated permissions.

**Drift warning format.** Emitted once per drifted file, to stderr:

    niwa: overwrote hand-edited skill at <path>.
    To preserve customizations, copy your version to
    ~/.claude/skills/niwa-mesh/SKILL.md — Claude Code loads personal-scope
    skills ahead of project-scope ones.

The second sentence is the sole discovery surface for the personal-scope
override. It fires at exactly the moment the user notices their edits are
being overwritten, which is the right moment to explain the escape hatch.
A complementary `docs/guides/mesh-skill-customization.md` page documents
the override mechanics for users who want to customize proactively.

**Channels section in workspace-context.md.** Replace the existing
`buildChannelsSection()` + `appendChannelsSection()` with a generator
producing exactly:

    ## Channels

    - Role: coordinator
    - Instance root: <instanceRoot>
    - MCP tools:
      - niwa_delegate
      - niwa_query_task
      - niwa_await_task
      - niwa_report_progress
      - niwa_finish_task
      - niwa_list_outbound_tasks
      - niwa_update_task
      - niwa_cancel_task
      - niwa_ask
      - niwa_send_message
      - niwa_check_messages

    See the `/niwa-mesh` skill for usage patterns.

…and a section-replacing helper that finds the `## Channels` header, spans
through the next `##` or EOF, and rewrites the block. When the generated
block matches the existing byte-for-byte: no-op. Otherwise: rewrite the
file.

`Role:` is hardcoded to `coordinator` because workspace-context.md exists
only at the instance root; the coordinator session is the only reader.
Workers are ephemeral `claude -p` subprocesses that learn their role from
the spawn environment, not from workspace-context.md.

**ManagedFiles integration.** Every installed path — each SKILL.md plus
the workspace-context.md — flows through `*writtenFiles` and is recorded
as a `ManagedFile` by pipeline step 7. `niwa destroy` walks
`ManagedFiles` and removes each; the skill directories are cleaned when
the last `ManagedFile` under them is removed (the existing destroy logic
already handles parent-directory cleanup).

**Rationale**

- Matches the PRD's philosophy ("narrow niwa surface; behaviour in skill")
  by shipping one opinionated default that users override wholesale at
  personal scope.
- Reuses existing `ManagedFiles` + `HashFile` + `writeFileMode` machinery —
  no new state-schema fields, no new drift-detection subsystem, no new
  atomic-write primitive.
- Mtime-stable behaviour: when intended content matches on-disk content,
  the file is not written. This keeps re-apply idempotent across mtime
  (important for tooling that keys off modification time) and clean under
  git diff (no spurious whitespace-only changes).
- The drift warning is both a correctness signal (user edits will be lost)
  and the single discovery mechanism for the personal-scope override. Docs
  back it up, but the warning is where the user will actually learn about
  the mechanism.
- Single template ≈ half the Go code of per-role variants, one-fifth the
  file churn of a layered references/ layout. Room to split later if data
  warrants; no architectural lock-in today.
- `allowed-tools` enumerated in frontmatter both gates skill tool use
  (Claude Code enforces it) and documents which tools the skill is
  authoritative for.

**Alternatives Considered**

- **Per-role variants (coordinator-flavored vs worker-flavored SKILL.md).**
  Rejected because (a) the R10 section set is symmetric in content
  (delegation has a sender and a receiver, completion has an emitter and
  an observer, etc.), (b) the installer doubles in template-string weight
  and branching, (c) override is two directories to copy instead of one,
  (d) there is no evidence that narrower skill scope would improve LLM
  triggering for this use case. Splitting later is non-breaking if data
  later justifies it.

- **Layered skill with `references/` subdirectory.** Rejected because the
  niwa-mesh body is 3,000-6,000 chars across six sections — well within a
  flat SKILL.md. `references/` is valuable when the body exceeds ~10kb or
  benefits from on-demand loading; neither applies here. Layered structure
  would multiply ManagedFiles entries, drift checks, and template
  maintenance for no behavioural gain.

- **Unconditional overwrite (no idempotency check).** Rejected because it
  would change the file's mtime on every apply and produce a spurious git
  diff whenever the file is tracked. The cost of hashing 4kb of content is
  negligible against the annoyance of every re-apply dirtying the working
  tree.

- **Per-repo workspace-context.md copies for per-role `Role:` values.**
  Rejected because workers are `claude -p` subprocesses and do not read
  workspace-context.md. Per-repo copies add complexity for a scenario that
  does not arise in v1.

**Consequences**

- Niwa installs a predictable, inspectable skill artifact. Users can open
  `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` and read the full
  contract in one file.
- Drift-warning ergonomics are consistent: hand-edit the project-scope file,
  get exactly one warning on the next apply plus one line telling you how
  to persist customizations. No backup-file churn under `.niwa/`.
- The `## Channels` section in workspace-context.md becomes a 12-15 line
  block that never grows. Behavioural prose that used to live there moves
  to the skill, where Claude Code's skill-loader can decide when to pull
  it in rather than inlining it into every context.
- Adding a new niwa MCP tool requires two edits: the `allowed-tools` list
  and the workspace-context.md MCP tools list. Both are in the installer
  template; forgetting one is caught by a simple round-trip test.
- The single-template approach means every niwa-mesh release ships
  identical content everywhere. If roles begin to diverge in behavioural
  expectations (e.g., a specialized reviewer role), the split-into-two
  migration is mechanical: introduce a second skill with a distinct
  `name:`, gate its install path by role.
- No new external runtime dependency. No new state-file field. The
  installer is ~150 lines of Go, all linting to `gofmt` + `go vet`.
<!-- decision:end -->
