# Decision: Description content in decorated candidates

## Context

Option B (union + decoration) has been selected as the disambiguation strategy for
`niwa go [target]`. Completion candidates unite repos from the current workspace
instance with workspaces from the global registry, and each candidate is decorated
via cobra's TAB-separated description protocol. The remaining question is what
text to put after the TAB. The string must be short enough to render cleanly in
bash V2's two-column tabular layout, must not contain TAB or newline (the test
parser in `test/functional/steps_test.go` splits on TAB and keeps the first field),
and must be treated as an enhancement because bash V1 drops descriptions silently.

The goal is to help the user distinguish a repo named `tsuku` from a workspace
named `tsuku` at completion time, mirroring the stderr hint `resolveContextAware`
prints at run time. Description text that over-informs turns into background
noise; text that under-informs fails the discoverability goal that justified
Option B in the first place.

## Options Considered

### Option 1: Kind only
- Description: The literal kind of the candidate - `repo` or `workspace`.
- Pros: Shortest possible payload (4-9 chars). Renders identically across zsh,
  fish, and bash V2 with no wrapping risk. Zero filesystem cost. Trivially
  matches the existing stderr hint's vocabulary (`also a workspace; use -w`).
  Scales: when the user has 40 candidates, descriptions stay legible.
- Cons: When kinds don't collide, the description conveys only the kind taxonomy,
  which the user quickly internalizes and then stops reading - classic
  description blindness. Offers no context for "which instance does this repo
  belong to" when the user is inside instance 2 but has repos from instance 1
  visible somewhere (not applicable here since we only enumerate the current
  instance, but if that ever changes, this option conveys nothing).
- Example: `tsuku  -- repo`

### Option 2: Kind + path
- Description: Kind plus absolute path to where the completion would land.
- Pros: Maximum information. Every candidate tells you exactly where `niwa go`
  would take you, which is arguably the most decision-relevant detail.
- Cons: Paths commonly run 40-80 chars. In bash V2's columnar layout with a
  dozen candidates, the description column either wraps or pushes wider than
  the terminal. Users reading a dense list learn to ignore the path column
  after the first use (description blindness at its worst). Requires path
  resolution work per candidate (cheap, but not free). Fish's parenthesized
  format `tsuku (repo: /very/long/path/...)` is particularly unwieldy.
- Example: `tsuku  -- repo: /ws-root/1/tsukumogami/tsuku`

### Option 3: Kind + instance qualifier
- Description: For repos, kind plus the instance number (`repo in 1`). For
  workspaces, kind only since there's no instance to qualify.
- Pros: Short (10-20 chars), well under the 30-char wrapping threshold. Adds
  the one piece of context that's genuinely disambiguating for repos when
  multiple instances of a workspace exist on disk: which instance am I sitting
  in, and therefore which physical checkout am I about to enter. Matches
  `niwa`'s existing vocabulary - instance numbers are user-visible throughout
  the CLI. Asymmetry (repos get qualified, workspaces don't) is semantically
  honest: workspaces have no instance dimension, so adding noise there would
  be dishonest.
- Cons: Slightly more complex to build than Option 1 - the completion helper
  must already know the instance number to decorate (which it does, since
  `DiscoverInstance` returns the instance root). The asymmetric format may
  look slightly uneven in bash V2's tabular layout. For users who never run
  more than one instance of a workspace, the `in 1` suffix is noise.
- Example: `tsuku  -- repo in 1`

### Option 4: Kind + shortened path
- Description: Kind plus a relative path (relative to the workspace root or
  instance root), e.g. `repo: ./tsukumogami/tsuku` for repos, and kind-only
  for workspaces.
- Pros: More informative than Option 1 while shorter than Option 2 (typically
  20-40 chars). Shows the directory structure within the instance, which can
  help when two repos share a prefix.
- Cons: Still flirts with the 30-char wrapping threshold for deeply nested
  repos. "Relative to what" is ambiguous: `./tsukumogami/tsuku` reads
  naturally only if the reader knows which root the `./` anchors to. Path
  computation is cheap but the presentation is noisier than Option 3 without
  a proportional UX payoff. Repo directory paths within an instance are rarely
  deep enough for the path structure to be the disambiguating signal.
- Example: `tsuku  -- repo: ./tsukumogami/tsuku`

### Option 5: No descriptions
- Description: Candidates are plain names with no TAB suffix.
- Pros: Simplest possible implementation. Identical rendering across all
  shells including bash V1. No test-parser concerns.
- Cons: This is Option A from the exploration (plain union), which the
  exploration explicitly ranked below Option B on discoverability. Adopting
  it here effectively reverses the Option B decision that gated this whole
  sub-question. Users completing `tsuku` can't tell repo from workspace - the
  very collision Option B was chosen to surface becomes invisible again.
- Example: `tsuku`

## Decision

Chosen option: **3 (Kind + instance qualifier)**

### Rationale

Grounding in the constraints from the design doc:

1. **Unsurprising UX across shells.** Option 3's descriptions stay under 20
   characters, which fits bash V2's tabular layout with plenty of margin
   before wrapping. Fish's parenthesized form (`tsuku (repo in 1)`) reads
   naturally. Bash V1 drops the description entirely, which is fine because
   the kind/instance qualifier is enhancement, not load-bearing - the
   candidate names alone still resolve correctly through `resolveContextAware`.

2. **Cross-shell readability.** At 10-20 chars, Option 3 is comfortably under
   the ~30-char threshold where bash V2's two-column display starts wrapping.
   Option 2 blows past that threshold on typical paths; Option 4 sits right
   on the edge.

3. **Latency envelope.** Option 3 needs no additional filesystem work beyond
   what the exploration already planned: `DiscoverInstance` returns the
   instance root, from which the instance number is trivially derivable. No
   per-candidate stat, no path normalization, no relative-path computation.

4. **Discoverability (the reason we decorated at all).** The exploration
   chose Option B to make the repo-vs-workspace collision visible. Option 3
   discharges that goal - users see `tsuku -- repo in 1` beside
   `tsuku -- workspace` and immediately understand what's colliding. The
   instance qualifier additionally answers "which checkout is this" for users
   who keep multiple instances of a workspace, which is an expected niwa usage
   pattern (the gap-filling logic from PR #41 suggests instance numbering is a
   visible first-class concept).

5. **Noise and description blindness.** Option 1 is readable but conveys only
   a taxonomy users learn once. Option 2 is informative but noisy enough that
   users learn to ignore it. Option 3 threads the needle: short enough to read
   every time, specific enough to stay informative because the instance number
   actually varies per candidate when the user has multiple instances.

6. **Test parser compatibility.** `repo in 1` and `workspace` contain no TAB
   or newline, so `completionSuggestions`'s split-on-TAB-and-keep-first-field
   behavior works unchanged.

### Trade-offs accepted

- Users who only ever run one instance per workspace will see `in 1` on every
  repo candidate, which adds a tiny amount of non-varying noise. Accepted
  because the cost (three characters) is small and the benefit (honest
  disambiguation) appears the moment a second instance exists.
- Workspace candidates get a shorter description (`workspace`) than repo
  candidates (`repo in 1`), making the tabular layout slightly uneven. Accepted
  because symmetry for its own sake (e.g. `workspace in -` or fabricating a
  field) would be dishonest.
- Paths are not shown. A user who wants to know exactly where they'll land
  must run `niwa go --dry-run` (or equivalent) or rely on the post-cd prompt.
  Accepted because path length is the thing that kills bash V2 rendering and
  breeds description blindness.

## Rejected alternatives

- **Option 1 (kind only):** Close runner-up. Simpler and equally safe across
  shells, but wastes the opportunity to communicate instance context when
  niwa's whole value proposition involves per-instance isolation. Choose this
  if future telemetry shows users never have multi-instance workspaces, or if
  the `in N` suffix tests poorly.
- **Option 2 (kind + path):** Rejected primarily on bash V2 wrapping and
  description blindness. The information density is too high and the signal
  degrades with every additional candidate in the list.
- **Option 4 (kind + shortened path):** Rejected because the "relative to
  what" ambiguity undermines the clarity benefit, and the character budget
  is worse than Option 3 without a commensurately higher signal.
- **Option 5 (no descriptions):** Rejected because it silently reverses the
  Option B decision already recorded as settled. If descriptions aren't
  wanted, we should revisit Option A in the exploration, not smuggle it in
  here.

## Assumptions

- The completion helper already has access to the instance number (or can
  derive it cheaply from the instance root path), matching the sketch in the
  exploration research. If instance-number retrieval turns out to require
  parsing registry state, reconsider against Option 1.
- Workspace candidates don't need an instance qualifier because `niwa go
  <workspace>` navigates to the workspace root (the shared parent of
  instances), not into any specific instance. This matches the documented
  `resolveContextAware` semantics.
- "Instance" is a user-facing concept the niwa CLI already surfaces; users
  reading `repo in 1` will understand "1" refers to the instance number.
  If future naming changes replace numeric instances with slugs or aliases,
  the description format changes accordingly but the decision holds.
- Collision cases (same name is both a repo and a workspace) continue to
  produce two separate candidates, per Option B. The descriptions then
  clearly distinguish them: `tsuku -- repo in 1` versus `tsuku -- workspace`.
