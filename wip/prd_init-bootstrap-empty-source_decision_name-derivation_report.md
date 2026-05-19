<!-- decision:start id="init-bootstrap-empty-source/name-derivation" status="assumed" -->
### Decision: Workspace Name Derivation for `niwa init --from <slug> --bootstrap` (no positional)

**Context**

When `niwa init --from <owner>/<repo> --bootstrap` runs without a positional
workspace name, niwa needs a name from somewhere. Today's no-positional
detached-clone path (`init.go:111-117`) falls back to the cloned config's
`[workspace].name` (`init.go:316-317`), but the bootstrap case targets an
EMPTY remote — there is no `.niwa/workspace.toml` to read a name from.
Bootstrap must therefore choose a different derivation rule, and the
chosen rule cascades into the workspace directory name, the registry
entry key, the scaffolded `[workspace].name` that will be pushed back to
the empty remote, the shell wrapper's landing-path emission, the instance
name (when bootstrap chains into create), and `niwa go <name>`
discoverability later.

This decision is observable at exactly the moment niwa is most heavily
scrutinized: a first-time user typing the "intro to niwa" command. The
PRD frames bootstrap as that intro command, so the default behavior here
sets the tone for everything that follows. The rule must be deterministic
enough for scripted callers, predictable enough for humans, and
compatible with the existing init.go pipeline that already assumes a
non-empty name in modeNamed/modeClone branches.

**Assumptions**

- Users who want a workspace name that differs from the GitHub repo
  basename will supply the positional explicitly
  (`niwa init <chosen-name> --from owner/repo --bootstrap`). The
  no-positional case targets the convergent default.
- Registry collisions on the derived name are rare; when they happen,
  the existing `--rebind` remediation path (init.go:194-205) is
  adequate.
- GitHub repo basenames almost always pass `ValidateInitName`. The rare
  exception (a repo literally named `.niwa`, or one containing
  characters GitHub permits in URLs but niwa rejects) should surface a
  clean validation error rather than crash later.
- A future `--name` flag (out of scope for this decision) would
  override derivation cleanly with the precedence positional > flag >
  derivation. The chosen rule must not foreclose that path.
- This decision is made in --auto mode without interactive user
  confirmation, hence the `status="assumed"` block marker. A reviewer
  may upgrade to `confirmed` after sign-off.

**Chosen: N1 — Derive name from slug's repo basename**

When `niwa init --from <owner>/<repo> --bootstrap` is invoked without a
positional workspace name, niwa derives the workspace name from the
slug's repo segment (`source.Source.Repo`, the parser at
`internal/source/source.go:30`). The derived name passes through
`ValidateInitName` and is then used exactly as if the user had typed
`niwa init <repo> --from <owner>/<repo> --bootstrap`:

1. Workspace directory: `<cwd>/<repo>/` (child directory created via
   the existing modeClone path).
2. Registry entry: keyed by `<repo>` (the existing
   `registryName = name` branch at init.go:315 stays unchanged because
   `name` is no longer empty).
3. Scaffolded `[workspace].name = "<repo>"` is written into the new
   `.niwa/workspace.toml` that bootstrap pushes to the empty remote.
4. Landing-path file is written (the wrapper cd's into the new
   workspace), because the `name != ""` gate at init.go:389 is now
   satisfied.
5. Success message names the rule: "Workspace 'foo' created at
   /abs/path/to/foo (name derived from --from slug)." The
   parenthetical teaches the rule once so users can predict subsequent
   bootstrap invocations.

The implementation delta is small: in `resolveInitMode` (or a
bootstrap-specific helper that wraps it), when `--bootstrap` is set and
`args` is empty, derive `name` from `sourcepkg.Parse(slug).Repo`,
validate via `ValidateInitName`, and then take the modeClone branch
exactly as it exists today. No new failure modes are introduced;
registry collisions, target-exists conflicts, and name-validation
failures all fall through to existing error paths with existing
remediation hints.

**Rationale**

N1 wins on every decision driver from Phase 0:

- **CLI idiom consistency.** `git clone owner/foo` creates `./foo/`,
  `gh repo clone owner/foo` creates `./foo/`. N1 mirrors this
  decades-deep convention. Bootstrap is fundamentally a clone-shaped
  operation (a remote already determines identity), so it should
  follow clone-shaped naming.

- **Predictability for scripted callers.** The derivation depends only
  on the slug the user already typed. No filesystem state, no TTY
  detection, no interactive prompt. Scripts produce identical results
  across machines and environments.

- **Registry collision behavior.** When `foo` is already registered,
  the existing message at init.go:484 ("Pass --rebind to retarget the
  entry, or remove the [registry.foo] section from
  ~/.config/niwa/config.toml and retry") is fully actionable for a
  name the user can see in the slug they just typed.

- **First-run observability.** The workspace name matches the GitHub
  repo name the user typed two seconds ago. `niwa go foo` works
  because `foo` is what they typed AND what they see on GitHub. The
  memory burden is zero.

- **No-positional coherence.** The rule applies only when `--bootstrap`
  is set; detached-clone (no bootstrap, no positional) keeps its
  current "materialize in cwd, register under cloned config name"
  behavior. Bootstrap's no-positional default behaves like
  positional-present in every observable way except the user didn't
  have to type the name twice. No surprise for either audience.

- **Validation safety.** GitHub repo names use a subset of niwa's
  allowed name characters; `ValidateInitName` catches the edge cases
  (`.niwa`, control characters) before any state is written.

The competing options each lose on a primary driver:

- **N2** loses on first-run UX. Refusing to proceed when the slug
  ALREADY supplies a reasonable default is pedantically explicit at
  the cost of the "intro to niwa" experience the PRD frames bootstrap
  around. It also forces scripted callers to re-derive the basename
  themselves.

- **N3** loses on predictability and CLI idiom. Tying the workspace
  name to cwd basename breaks when cwd is generic (`~/work/`,
  `~/projects/`) and diverges from `git clone` conventions. It also
  drops files into cwd without a clear "this is your workspace"
  marker, which is exactly the first-run footgun bootstrap should
  avoid.

- **N4** loses on complexity-vs-value. The interactive prompt is
  polish that adds niwa's first interactive `init` code path, a
  TTY/non-TTY behavioral split, and new failure modes (signal
  handling mid-prompt) — to make N1 slightly more explicit for one
  audience segment that already has the positional as an override.

**Alternatives Considered**

- **N2 (Require positional name; refuse if absent):** errors with
  "name required when --bootstrap." Rejected because it makes the
  "intro to niwa" command longer than `git clone`, breaks muscle
  memory parity, and refuses to proceed when an obvious default is
  available. Scripted callers would replicate N1's derivation logic
  in shell to comply.

- **N3 (Derive from cwd basename):** mirrors `git init` style;
  materializes in cwd with no child directory. Rejected because it
  ties the workspace name to a filesystem path (the coupling
  `--rebind` exists to break), breaks when cwd is generic, and
  diverges from the dominant `git clone`/`gh repo clone` convention
  that bootstrap most closely resembles.

- **N4 (Slug derivation + TTY confirmation prompt):** defaults to N1
  but adds an interactive `Workspace name [<repo>]:` prompt in
  TTY-attached invocations. Rejected because the override mechanism
  already exists (the positional), the TTY/non-TTY split introduces
  documentation and demo complexity, and an interactive prompt adds
  failure modes to a code path that is currently single-shot.

**Consequences**

What becomes easier:
- First-run bootstrap is `niwa init --from owner/foo --bootstrap` — as
  short and discoverable as `git clone owner/foo`.
- Scripted callers can rely on a deterministic, slug-only derivation
  without inspecting cwd or detecting TTYs.
- The PRD's "intro to niwa" framing is honored: the command is short,
  obvious, and produces a workspace with a name the user already
  understands.

What becomes harder:
- A user who wants the workspace name to differ from the GitHub repo
  basename MUST type the positional. This is the same friction every
  other clone-shaped CLI imposes, and the workaround is documented.
- Registry-collision messages now reference a derived name the user
  didn't explicitly type. Mitigated by the existing suggestion text
  naming the exact `[registry.X]` section to edit.

What stays the same:
- Detached-clone behavior (no bootstrap, no positional) is unchanged.
- All existing error paths (target exists, registry collision, name
  validation) fire with their existing messages.
- The shell wrapper landing-path mechanism continues to gate on
  `name != ""`, which N1 satisfies by deriving the name before the
  gate.

What this enables next:
- A future `--name <name>` flag can be added with precedence
  positional > flag > derivation without contradicting N1.
- A future "bootstrap from URL" extension (vs slug) needs to specify
  its own derivation rule, but slug-based bootstrap stays simple.
<!-- decision:end -->
