# Lead: What do commuter and equity-planner's real workspace.toml files contain?

## Findings

**Neither `dangazineu/commuter` nor `dangazineu/equity-planner` is accessible from this environment, and neither appears to exist on GitHub at all.**

Verification steps taken (all via `gh`, authenticated as `dangazineu` with a fine-grained PAT that has confirmed read access to a mix of public `dangazineu/*` repos and private `tsukumogami/*` repos):

1. `gh repo view dangazineu/commuter` / `dangazineu/equity-planner` -> `GraphQL: Could not resolve to a Repository with the name ...` (both).
2. `gh api repos/dangazineu/commuter` and `repos/dangazineu/equity-planner` -> both `404 Not Found`.
3. `gh api users/dangazineu` confirms the account is real (id 422240, 11 public repos, created 2010). Listing all public repos for that user (`users/dangazineu/repos`) returns 11 repos -- `.airules`, `agentza`, `danielgazineu.github.io`, `devops-in-a-box`, `evento`, `gcp-java-packer`, `hello-vagrant-eclipse-gradle`, `tako`, `vagrant-eclipse-packer`, `vagrant-unity-packer`, `vyb` -- no `commuter`, no `equity-planner`.
4. `gh api user/repos` (repos the authenticated token can see, public + granted private) returns the same 10 `dangazineu/*` public repos plus several private `tsukumogami/*` org repos (`coding-tools`, `dot-niwa-overlay`, `tools`, `vision`) -- again, no `commuter` or `equity-planner` anywhere.
5. GitHub code/repo search (`gh api "search/repositories?q=user:dangazineu"`) returns the same 10 repos, confirming no repo named commuter or equity-planner exists under this account, public or otherwise.
6. A direct search-API scoped query, `gh api "search/repositories?q=repo:dangazineu/commuter"`, returns a `Validation Failed` error stating explicitly: *"The listed users and repositories cannot be searched either because the resources do not exist or you do not have permission to view them."*
7. Global keyword search for `commuter in:name` and `equity-planner in:name` across all of GitHub turns up 1000+ and several unrelated repos respectively (e.g. `nteract/commuter`, `dxxmngo/equity-planner`, `holdequity/planfi-equity-comp-planner`) but nothing under `dangazineu`.
8. Consequently, `.niwa/workspace.toml` commit history could not be checked for either repo -- `gh api repos/dangazineu/{commuter,equity-planner}/commits?path=.niwa/workspace.toml` was not attempted since the repos themselves don't resolve, and doing so would just reproduce the same 404.

**Within the niwa repo itself**, the only place these two repo names appear is `wip/explore_niwa-config-skill_scope.md` (the dispatch brief that seeded this exploration round) -- there is no other doc, spike, or code reference to `dangazineu/commuter` or `dangazineu/equity-planner` anywhere in this working tree. The specific claim that commuter's `workspace.toml` contains `[instance.files] "skills/" = ".claude/skills/"` to drop `commuter-booked`/`commuter-options` skills traces back only to that scope doc's own "Context" section -- it is not corroborated by any other artifact in-repo, and it cannot be verified against a real file because the repo is unreachable.

A tangential, unrelated hit: `docs/spikes/SPIKE-niwa-session-keep-alive.md` mentions a session/instance named `feature_4_real_commute` / `commuter_wip` -- this is an RC session name in a keep-alive spike, not the `dangazineu/commuter` repo, and has no bearing on this lead.

## Implications

- The brief's "live examples" framing cannot be grounded in real file content from within this environment. Either (a) the repos are private and owned by an account/org this token has zero visibility into (not even a 404-vs-403 leak, which GitHub normally gives even for private repos you lack access to but that exist under orgs you belong to), or (b) the repos are illustrative/hypothetical placeholders introduced in the dispatch brief rather than actual production repos.
- Given the authenticated token can see private `tsukumogami/*` org repos just fine, and a global search plus an explicit `repo:` search-scoped query both come back empty/invalid for these two names, the more likely explanation is (b): these are illustrative examples, not real accessible repos.
- Per the exploration instructions, the "common edits" content for the new config-editing skill should be derived from niwa's own schema and docs (`docs/guides/workspace-config-sources.md`, `docs/guides/vault-integration.md`, the `[instance.files]` mechanism per lead `r1_lead-instance-files-mechanism.md`, and `niwa init --bootstrap`'s scaffold per `r1_lead-bootstrap-scaffold.md`) rather than by copying a live example verbatim, since no live example is retrievable.
- Anyone continuing this exploration should not restate the `[instance.files] "skills/" = ".claude/skills/"` claim as verified fact in downstream artifacts (PRD/design) -- it should be flagged as unconfirmed/illustrative, sourced only to the dispatch brief, unless a maintainer with direct access can confirm it out-of-band.

## Surprises

- The repos don't just return "private, no access" (403) -- they 404 even via a broad, deep search, and don't show up in the account's own repo listing (10 public repos total, all clearly personal/devops-tooling projects like `vagrant-eclipse-packer`, `gcp-java-packer`, unrelated to a "commuter" or "equity-planner" product). This is a stronger-than-expected signal that these specific names may be invented for illustrative purposes in the brief rather than dropped/renamed real repos.

## Open Questions

- Are `dangazineu/commuter` and `dangazineu/equity-planner` real private repos under a different GitHub account/org, or purely illustrative placeholders coined for the dispatch brief? This can only be resolved by asking the brief's author directly -- it is not discoverable from this environment.
- If real, do they actually use `[instance.files]` the way the brief describes, or was that detail itself invented as a plausible-sounding illustration? Same caveat applies.
- Is there some other "real" example single-repo niwa workspace (inside the `tsukumogami` org, which this token *does* have private access to) that could serve as a genuine live example for grounding the skill's "common edits" content, in place of the inaccessible commuter/equity-planner repos?

## Summary
`dangazineu/commuter` and `dangazineu/equity-planner` do not resolve via `gh repo view`, `gh api repos/...`, or GitHub search (both direct name search and `repo:` scoped search return not-found/invalid), and neither appears in the `dangazineu` account's 10 public repos or in the private repos this token can otherwise see, so their `workspace.toml` content and edit history are unverifiable from this environment. The specific `[instance.files] "skills/" = ".claude/skills/"` claim traces only to the dispatch brief's own scope doc and should be treated as unconfirmed/illustrative rather than a verified live example. The resulting skill's "common edits" content should be derived from niwa's own schema/docs instead of a real commuter/equity-planner example.
