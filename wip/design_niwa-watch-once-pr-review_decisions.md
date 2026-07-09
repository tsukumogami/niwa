# DESIGN decisions: niwa-watch-once-pr-review (--auto, Tier 2 inline)

| id | question | decision | status |
|----|----------|----------|--------|
| DD1 | containment-profile carrier | instance `.claude/settings.json` merge seam (not --settings flag) | assumed |
| DD2 | egress model | empty allowedDomains (agent tools: no egress); niwa pre-fetches PR head during trusted provisioning; harness↔model channel is separate | assumed |
| DD3 | env allowlist | watch launch builds cmd.Env from explicit allowlist (Claude/Anthropic auth + PATH/HOME/locale); GitHub token and other secrets EXCLUDED | assumed |
| DD4 | poll | net-new github search method GET /search/issues?q=is:pr+is:open+user-review-requested:<login>; resolve login via GET /user; intersect with workspace repo set | assumed |
| DD5 | handled-set | flat file under workspace .niwa/ keyed by owner/repo#number; write only on successful contained dispatch | assumed |
| DD6 | trusted post step | `niwa watch post <handle>` / `niwa watch discard <handle>` subcommands, run in trusted context; read the known draft path + persisted PR coords; post via GitHub API with resolveGitHubToken; credential never in contained env | assumed |
| DD7 | review prompt + draft location | metadata-only prompt; agent reads pre-fetched local clone as untrusted, writes draft to niwa-defined known path, halts | assumed |
| DD8 | fail-closed detection | preflight: refuse (non-zero + stderr) if platform unsupported (GOOS==windows) or sandbox settings cannot be applied | assumed |
| DD9 | reuse vs new | reuse dispatch provisioning (applier.Create) + session mapping; add containment settings merge + PR-head pre-fetch + env-allowlist launch path as net-new dispatch surface | assumed |
