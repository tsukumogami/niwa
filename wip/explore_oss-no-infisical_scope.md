# Explore Scope: oss-no-infisical

## Visibility

Public

## Core Question

A niwa workspace whose declared secrets are all developer conveniences becomes
unusable when the vault backend is absent or unauthenticated: `niwa init` fails
outright rather than degrading. We need to figure out where responsibility sits
between niwa's `required` secret contract and a workspace config's declaration
of it, and what "loud but non-fatal" should concretely look like along the
init -> create -> apply -> dispatch path.

## Context

Observed by running `niwa init tsukumogami --from tsukumogami/dot-niwa` on a
host with no Infisical CLI installed. Two failure paths converge on the same
wall:

- The public base config (`tsukumogami/dot-niwa`, `.niwa/workspace.toml`)
  declares `ANTHROPIC_API_KEY` and `GH_TOKEN` under `[env.secrets.required]`,
  plus `INFISICAL_TEST_PROJECT_ID`, `INFISICAL_CLIENT_ID`, and
  `INFISICAL_CLIENT_SECRET` under `[repos.niwa.env.secrets.required]`. No
  `[vault.provider]` exists in the public config; the provider and every
  `vault://` binding live only in the private overlay. Read on its own, the
  public config declares hard requirements it has no mechanism to satisfy.
- niwa's `checkRequiredKeys` (`internal/workspace/required.go`) is a deliberate
  hard failure. Its doc comment states `--allow-missing-secrets` does NOT
  downgrade a required miss (PRD R33/R34): the resolver softens an unreachable
  vault lookup to an empty value, and the required check then fails on that
  emptiness. `niwa init` does not expose the flag at all
  (`internal/cli/init.go` flag registration).

An OSS contributor with no overlay access and a maintainer with overlay access
but no Infisical credentials hit the identical wall from opposite directions,
with no escape hatch.

The expectation: creating and operating a tsukumogami workspace must succeed on
a host with no Infisical installed and no valid credential. A vault login
failure should be loud, but not fatal.

## In Scope

- Semantics of `required` / `recommended` / `optional` secret declarations in niwa
- Distinguishing "no vault provider configured" from "provider configured but
  unreachable"
- Overlay auto-discovery behaviour when the overlay is inaccessible vs. accessible
- Warning and escape-hatch surfaces along `init`, `create`, `apply`, `dispatch`,
  and the SessionStart ephemeral-instance path
- Correcting the tsukumogami base config's secret declarations (companion change)

## Out of Scope

- CI-specific / non-interactive behaviour as a distinct persona
- Infisical as a vendor choice; swapping or adding vault backends
- Secret redaction and audit-log design (already settled elsewhere)

## Research Leads

1. **Where exactly does the failure fire across init -> create -> apply -> dispatch,
   and what does a user actually see?** (lead-failure-trace)
   Trace every `checkRequiredKeys` call site and every command that reaches it,
   including `niwa dispatch`, the SessionStart ephemeral-instance hook, and
   `niwa create`. Reproduce both personas' terminal output verbatim. The error
   text and its placement in the flow are half the bug.

2. **What did `required` / `recommended` / `optional` mean by design, and why was
   `--allow-missing-secrets` deliberately blocked from downgrading required?**
   (lead-required-semantics)
   The exclusion is documented as intentional (PRD R33/R34). Whether the fix
   changes the contract or works within it depends on whether that reasoning
   still holds.

3. **Can niwa distinguish "no vault provider configured" from "provider
   configured but unreachable" at runtime?** (lead-provider-absence-vs-unreachable)
   These are different failures deserving different messages and possibly
   different severities. Today both funnel into an empty value that trips the
   same required check.

4. **Is an unsatisfiable required key statically detectable at merge time?**
   (lead-static-detection)
   A key marked required with no `vault://` binding and no `[vault.provider]`
   anywhere in the merged config is arguably a config error niwa could name
   precisely and early, rather than failing later on emptiness.

5. **How do comparable tools handle declared-but-unavailable secrets on first
   run?** (lead-prior-art)
   direnv, mise, devenv/devbox, sops- and 1Password-backed setups. What is the
   prevailing degraded-mode UX, and what do they treat as fatal.

6. **Which tsukumogami secrets are genuinely load-bearing for cloning and
   applying the workspace, versus only for development?** (lead-secret-necessity)
   `GH_TOKEN` plausibly gates the private overlay clone itself; the `INFISICAL_*`
   keys serve only niwa's own integration tests. This determines what the
   corrected base config looks like and whether "none should be required" is
   literally achievable.

### Round 2 leads

8. **What does Claude Code actually do with a non-zero SessionStart hook, and
   what should a degraded `niwa dispatch` do?** (lead-sessionstart-semantics)
   The launch-coupled paths pass no flags and today exit 1 with no JSON, booting
   a worker uninstrumented at the workspace root with no explanation. This
   decides what "loud but non-fatal" means where no human is watching.

9. **Do the incidental findings hold up end to end?** (lead-incidental-verify)
   Remote-control suppression when `ANTHROPIC_API_KEY` is set, whether
   unauthenticated `ListRepos` suffices for tsukumogami, and whether `GH_TOKEN`
   promotion is pure redundancy. Each is a candidate issue; none should be filed
   on a single agent's read.

10. **How do the `.local.env` annotation and the `--strict` flag actually land in
    the code?** (lead-annotation-and-strict)
    Where the env writer would carry comments, whether they survive rewrite and
    stay idempotent, and what `--strict` must cover given four independent gates
    and two commands that cannot pass flags.

### Round 1 leads

7. **What escape hatches and warning surfaces already exist, and are they
   reachable?** (lead-existing-surfaces)
   `--allow-missing-secrets` exists on `create` and `apply` but not `init`;
   there is a `niwa status --audit-auth` surface and an aggregated
   vault-unreachable warning path. The fix may be plumbing and discoverability
   rather than new mechanism.
