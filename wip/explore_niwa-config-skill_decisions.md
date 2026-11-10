# Exploration Decisions: niwa-config-skill

## Round 1
- Rule out bootstrap-scaffold-only delivery: it can never reach already-adopted single-repo workspaces (the primary target population), since `niwa init --bootstrap`'s discovery probe never re-fires once a `.niwa/workspace.toml` marker exists.
- Rule out a bare static schema copy baked into the skill: `docs/designs/current/DESIGN-workspace-config.md` is concrete in-repo evidence that this fails within months, specifically on the `[claude]` and file-distribution blocks the skill needs to teach.
- Treat the dispatch brief's `dangazineu/commuter`/`dangazineu/equity-planner` "live example" claims (including the specific `[instance.files] "skills/" = ".claude/skills/"` usage) as unverified/illustrative in downstream artifacts -- both repos are unreachable from this environment via `gh` and don't appear in the account's real repo listing.
- Defer the final delivery-mechanism choice (extend/generalize the embedded-plugin install gate vs. use `[instance.files]` vs. a combination) to the design phase -- evidence shows real trade-offs on each side rather than one obvious winner, which is what `/shirabe:design` exists to resolve.
