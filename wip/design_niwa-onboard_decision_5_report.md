<!-- decision:start id="config-authoring-mechanics" status="assumed" -->
### Decision: Config authoring mechanics for the onboarding wizard

**Context**

`niwa onboard` writes configuration in three places that behave nothing alike:
the operator's personal-overlay repo (a real git clone with history, entirely
the operator's own account), the operator-local config file that is not a git
repo at all, and the team's workspace source repo (a shared, review-gated
object the operator merely has merge access to). R12 and R22 already fix
*which* logical block lands in which of these three, and R9/AC-22 already fix
that the wizard must refuse to write anything when the credential-sync
provider's own `(kind, project)` would be bootstrapped from the pool it's
about to populate. What's undecided is the *mechanics*: how the wizard edits
an existing file without destroying what the operator already put there, and
whether it takes git actions on the operator's behalf or only tells the
operator what to do.

The constraint that gives this decision teeth is `.niwa/` being a pure,
atomically-replaced snapshot (`docs/guides/workspace-config-sources.md`,
"Snapshot model"): any wizard write that lands there is silently discarded by
the next `niwa apply`, reproducing exactly the far-from-the-cause failure
class this feature exists to kill. Two further facts from the codebase narrow
the space. First, niwa's only TOML library is `github.com/BurntSushi/toml`
(`go.mod`), which decodes into Go structs and encodes fresh output from them
but has no format-preserving edit path (no AST/Document type as `pelletier/
go-toml`'s does) — a struct-round-trip write is a full file replacement, not
an edit. Second, the one existing precedent for wizard-authored config
reaching a durable source, `niwa init --bootstrap`'s `RunBootstrap`
(`internal/workspace/bootstrap.go`), commits a scaffold to a
`niwa-bootstrap/<sid>` branch and deliberately never pushes it
(`TestRunBootstrap_R24_NoPush`) — the operator's own action is what gets it
upstream.

**Assumptions**

- The personal-overlay repo, once registered, is available to the wizard as a
  real local git clone with a `.git` directory (per `OverlayDir`,
  `internal/config/overlay.go:285-312` — `$XDG_CONFIG_HOME/niwa/overlays/
  <org>-<repo>/`), not merely a fetched file. If the DESIGN instead has the
  wizard operate on a bare fetched copy with no git history, the "commit
  locally, let the operator push" mechanic in this decision needs a session/
  worktree wrapper (comparable to bootstrap's `CreateSessionFunc`) rather than
  a plain `git commit` in place.
- The `[global.vault.provider]` table is treated as fully wizard-owned once
  R12/R22 assign it to the wizard: the wizard's insertion logic replaces the
  *whole table* it finds (header through the next top-level table or EOF)
  rather than merging key-by-key. If an operator hand-edits an unrecognized
  key inside that specific table between wizard runs, this decision's
  mechanism does not preserve it. Comments and other tables elsewhere in the
  same file are unaffected — this narrow, named table is the only content the
  mechanism overwrites.
- "Print a diff/snippet and instruct" for the team-config repo is read as
  including *no local git write at all* (not even an uncommitted working-tree
  edit), per the Out of Scope wording ("the wizard states what is needed but
  does not push it for them"). If the DESIGN wants the wizard to also stage a
  working-tree edit in the operator's already-checked-out team-config repo
  (still uncommitted, still requiring the operator's own `git add`/commit),
  that is a smaller variant of the same "instruct, don't act" posture and
  doesn't change the chosen mechanism below — it only changes how much of the
  instruction is pre-typed into a file versus printed to the terminal.

**Chosen: Per-site mechanism — surgical table-level TOML insertion with an idempotent landing check, split three ways by git posture**

Concretely, three distinct behaviors for three distinct write sites, unified
by one shared editing primitive:

1. **Personal-overlay repo** (`niwa.toml` at the overlay's root — the
   `[global.vault.provider]` declaration and any `[workspaces.<name>.env.
   secrets]` per-workspace personal secrets, R12 first bullet):
   - The wizard reads the file if it exists (or starts from empty if the
     repo/file is being scaffolded per R22's "repo does not exist yet" path).
   - A landing check runs first: does a `[global.vault.provider]` table
     already exist with the exact values the wizard is about to write? If
     yes, no-op — this is what makes re-runs idempotent (the constraint's
     explicit requirement) without ever producing a second table (which
     would in any case be a hard TOML parse error on duplicate top-level
     keys, not just an aesthetic problem).
   - If the table is absent, the wizard appends it, preceded by a blank line,
     leaving every byte before it untouched. If the table is present with
     different values (a re-run after a topology or project change), the
     wizard replaces only the span from that table's header line to the next
     top-level table header (or EOF) — a whole-table replace, not a
     key-by-key merge, per the assumption above. Everything else in the
     file — the operator's own comments, other tables, unrelated
     `[workspaces.*]` blocks — is copied through verbatim.
   - The wizard commits the change locally (no custom author identity,
     mirroring bootstrap's `TestRunBootstrap_R18_NoAuthorArgNoAuthorEnv` — it
     uses whatever git identity is already configured on the machine) and
     does **not** push. It reports the commit and tells the operator to
     `git push` it. This mirrors `RunBootstrap` exactly, including for the
     "repo doesn't exist yet" case R22 already specifies in that language.
   - This is a low-governance repo (the operator's own account, no PR/review
     gate implied anywhere in the PRD), so a same-mechanism "commit, don't
     push" posture is a deliberate simplification: it gives the operator one
     consistent habit ("onboard always leaves me a commit to push") across
     both the fresh-scaffold and edit-existing-file cases, rather than two
     different postures depending on whether the file existed yet.

2. **Local overlay pointer** (`~/.config/niwa/config.toml`'s `[global_config]`
   block, set by `niwa config set global <slug>` — R12 second bullet): this
   file is not a git repo at all (per the runbook research: "Local machine
   only"). The wizard writes it directly — no commit/push posture applies,
   because there is no upstream to sync it to. If an existing `niwa config
   set` code path already does this write, the wizard reuses it rather than
   inventing a second writer for the same file.

3. **Team's workspace source repo** (`[vault.provider]`, `[env.secrets]`
   refs, optional `[vault].team_only` / `[workspace].vault_scope` — R12 third
   bullet): the wizard makes **no git write of any kind** here. It computes
   the exact TOML snippet the team config needs (same table-aware generation
   logic as case 1, so the *content* is still produced by niwa rather than
   hand-typed) and prints it, naming the destination file, and stops. The
   operator carries it into their own edit/PR/review flow. This is the
   posture the Out of Scope section states in plain language ("the wizard
   states what is needed but does not push it for them") and it is the only
   posture consistent with "requires the operator's own review/merge access"
   — a repo gated behind review is not a repo the wizard should be creating
   commits against, even unpushed ones, because an unpushed local commit in
   a shared team clone is easy to mistake for a landed change.

All three sites share one editing primitive (table-header-aware insertion
with a pre-write landing check), but only sites 1 and 3 use its *write* path;
site 3 uses only its *render* path (produce the snippet text, skip the file
I/O). Site 2 doesn't use it at all — it's a different file shape entirely
(a flat pointer field, not a TOML table the wizard owns).

**Rationale**

Ties back to three constraints the decision context named as drivers:

- *Preserve operator content*: only a surgical, table-scoped insertion avoids
  the "clobbers comments/formatting/unknown keys" cost the constraints
  flagged as real. Given `BurntSushi/toml` has no round-trip-preserving encode
  path, a struct re-marshal is not a lighter version of the same guarantee —
  it's a materially worse one (see Alternatives).
- *Land in durable sources, never `.niwa/`*: satisfied structurally, since
  both write sites (1 and the deferred-write path for 3) target repos, never
  the snapshot directory — this was already fixed by R12/R22 and this
  decision doesn't revisit it, only how the writes happen.
- *Commit/push per write-site, not one blanket rule*: the PRD's own text
  already treats the team-config repo differently from the personal overlay
  (Out of Scope's explicit "states what is needed but does not push it for
  them" vs. R22's "produces a local commit the operator pushes" for the
  overlay). Choosing one uniform posture for both would either overreach on
  the team repo (making unpushed commits in a repo the wizard has no review
  standing in) or underserve the personal overlay (forcing the operator to
  hand-type a block the wizard already computed correctly, reintroducing the
  exact copy-paste risk this feature exists to eliminate for the credential
  body).
- *Idempotent re-run*: the landing check is not a nice-to-have layered on
  top — it's required simply to keep the file valid TOML on a second run
  (a duplicate top-level table is a parse error, not just noise), so this
  decision treats it as inseparable from the insertion mechanism itself.

**Alternatives Considered**

- **Struct re-marshal** (parse the overlay file into the existing
  `WorkspaceOverlay`/`GlobalOverride` Go structs via `config.go`'s decoders,
  mutate the `Vault` field, re-encode the whole struct back to TOML).
  Rejected because `BurntSushi/toml`'s `Encode` (the only TOML writer in the
  dependency tree today) produces a fresh serialization from the struct, with
  no memory of the original file's comments, key ordering, or blank-line
  structure — a "targeted mutation" that is, in practice, a full-file
  overwrite of every comment and human-added block in the operator's overlay.
  It also silently drops (or, depending on decoder strictness, errors on) any
  top-level key the overlay struct doesn't model, which real overlay files
  are free to carry (the schema is intentionally permissive — `Config
  map[string]any` on `VaultProviderConfig` only covers provider-specific
  sub-keys, not arbitrary top-level tables). This is exactly the "clobbers
  comments/formatting/unknown keys" cost the decision context named as real,
  not a hypothetical one — worse, adopting it would require *adding* a
  marshal-back code path that doesn't exist in the codebase today (writing is
  currently whole-file-scaffold only), so it carries the same net-new-code
  cost as the chosen surgical helper while delivering a strictly worse
  fidelity guarantee.
- **Guided templates for every site, including the personal overlay** (never
  write to any file directly; always print the block and require the
  operator to paste it in by hand, uniformly across all three write sites).
  Rejected for the personal-overlay and local-pointer sites specifically: the
  personal-overlay repo carries no review gate and no governance reason to
  force manual authoring, and the PRD's own automation philosophy (R8's
  "automate every mechanical step the provider gives niwa a safe surface
  for", and US-2's "so that I never learn the vault path, the key prefix, or
  the body format") extends the same logic from the credential body to this
  config block — forcing hand-typed TOML here reintroduces the identical
  exact-shape transcription risk (a table name, `kind`/`project` field typos)
  the feature exists to eliminate elsewhere. It would also still need a
  landing check to detect whether the operator's manual paste succeeded
  correctly, so it doesn't even avoid the idempotence-check implementation
  cost — it just relocates the correctness risk onto a human. This
  alternative is *right* for the team-config site specifically (case 3 above
  adopts it there), but wrong as a uniform rule across all three sites.
- **One uniform commit/push posture across all repo writes** (either "wizard
  always commits and pushes for the operator" or "wizard never touches git,
  everywhere"). Rejected as a blanket rule because the PRD text itself already
  draws the line between the two repos differently: R22 explicitly describes
  the overlay case ending in "a local commit the operator pushes," while the
  Out-of-Scope section explicitly forbids even that much for team-config
  ("does not push it for them," framed as a stronger and more absolute
  posture than R12's own "not committed... without their action"). Uniformly
  applying the overlay's commit-locally behavior to team-config would put the
  wizard in the business of creating commits in a repo it has no review
  standing in; uniformly applying team-config's print-only behavior to the
  personal overlay would abandon R22's already-settled scaffold behavior and
  needlessly demote the one write site where "commit, don't push" is both
  safe and already the established pattern.

**Consequences**

- A new, purpose-built table-insertion helper is needed (confirmed net-new by
  codebase research — "No file-write-to-upstream-repo helper for a
  wizard-authored TOML block" exists today); this is the one concrete
  implementation cost this decision imposes on the DESIGN. It's a much
  narrower piece of code than a general TOML editor — it only ever needs to
  find-and-replace one named top-level table by header line, never nested or
  array-of-table structures, since the wizard-owned blocks (`[global.vault.
  provider]`, `[workspaces.<name>.env.secrets]`) are all simple top-level or
  one-level-nested tables.
- The wizard needs local git plumbing for the personal-overlay repo (open,
  stage, commit, no push) but explicitly does not need any git-push
  credential or scope beyond what's already implied by the operator having
  cloned the repo themselves — this keeps the secret-hygiene surface (R17)
  untouched, since no new credential class is introduced.
- For the team-config site, the wizard's output there is purely textual
  (a snippet plus a named destination), which is easy to satisfy AC-25 with
  ("for each config write, the wizard states whether it landed in an upstream
  repo or in operator-local state") — the team-config case is by construction
  never "landed," only "stated," so the reporting logic for that site doesn't
  need to inspect any git state to know what to say.
- The whole-table-replace behavior (rather than key-by-key merge) means a
  hand-edited key inside `[global.vault.provider]` that the wizard doesn't
  know about is lost on the next wizard-driven update to that table. This is
  accepted as a documented limitation rather than solved, because the
  table's schema is small and fully wizard-assigned by R12/R22 in the first
  place — there's no expected operator-authored content inside it to lose.
<!-- decision:end -->
