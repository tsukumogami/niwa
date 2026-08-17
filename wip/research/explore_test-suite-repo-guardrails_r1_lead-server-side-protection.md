# Lead: Which server-side rule would actually have blocked this push, and what is reachable on the organization's current plan?

## Findings

### The precise scenario to test against

A fast-forward push of a single non-merge commit directly to `refs/heads/main` over SSH, using the repo owner's own credentials (i.e., an account with admin permission on the repo). No force flag. No PR. No CI run had ever touched the commit.

### Rule-by-rule semantics (source: GitHub REST API rule-types reference and the rulesets article)

Quoted from `https://docs.github.com/en/rest/repos/rules?apiVersion=2022-11-28` and `https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets`:

- **`non_fast_forward`** — "Prevent users with push access from force pushing to refs." Only blocks force pushes. A clean fast-forward push is exactly what this rule *lets through*. Does not apply here.
- **`deletion`** — "Only allow users with bypass permissions to delete matching refs." Governs branch/tag deletion, not push content. Does not apply here.
- **`creation`** — "Only allow users with bypass permission to create matching refs." Governs creating a new ref, not pushing to an existing one. Does not apply here.
- **`required_linear_history`** — "Prevent merge commits from being pushed to matching refs." A single ordinary commit is linear history; this rule has nothing to object to. Does not apply here.
- **`update`** — "Only allow users with bypass permission to update matching refs." This is the broadest rule: it restricts *any* push (fast-forward or not) to only bypass-listed actors. If `bypass_actors` is empty, this rule blocks every direct push from everyone, full stop. **This would have blocked the incident push.**
- **`pull_request`** — "Require all commits be made to a non-target branch and submitted via a pull request before they can be merged." This requires every change to land via PR merge, which categorically excludes direct pushes to the target ref. **This would have blocked the incident push.**
- **`required_status_checks`** — "Choose which status checks must pass before the ref is updated. When enabled, commits must first be pushed to another ref where the checks pass." I could not get GitHub's docs to state the direct-push case in so many words, but a GitHub Community Discussion (`https://github.com/orgs/community/discussions/86534`, corroborating the API wording above) confirms the practical behavior: "When you put required status checks in place, direct pushes to a protected branch will fail because there is no status associated with a direct push." A commit that was never run through CI has no recorded status for that SHA, so GitHub rejects the push citing the missing required check. **This would also have blocked the incident push** — not because it evaluates the push's content, but because a fresh commit arriving via direct `git push` has zero check runs against its SHA, and the rule requires the check to already be green before the ref can move.

So three of the seven candidate rule types actually would have stopped this exact incident: `update`, `pull_request`, and `required_status_checks`. The other four (`non_fast_forward`, `deletion`, `creation`, `required_linear_history`) are aimed at different failure modes (force pushes, ref deletion, ref creation, merge-commit topology) and do not react to an ordinary fast-forward push of a linear commit.

### Classic branch protection equivalents

From `https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches`:

- **"Require a pull request before merging"** forces all changes through a PR, same effect as the ruleset's `pull_request` rule — blocks direct pushes.
- **"Require status checks to pass before merging"** — same chicken-and-egg mechanism as above: a direct-pushed commit with no check runs gets rejected.
- **"Restrict who can push to matching branches"** — an allow-list; anyone not on it is blocked, similar in spirit to the ruleset's `update` rule, except "People and apps with admin permissions to a repository are always able to push to a protected branch" *regardless of the restriction list*, per GitHub's docs. That carve-out matters below.
- **Critically, all of this is gated by `enforce_admins`.** Per GitHub's docs: "By default, the restrictions of a branch protection rule don't apply to people with admin permissions" — this is exactly the `enforce_admins` / "Include administrators" toggle. When it's off, a repo admin (which is what "the repo owner's own credentials" means here) is exempt from **every** rule in that classic protection config, including required status checks and required PRs. I confirmed koto's live protection (`gh api /repos/tsukumogami/koto/branches/main/protection`) has `"enforce_admins":{"enabled":false}` alongside `required_status_checks` context `"validate"`. That means **koto's current protection would not have stopped the incident push**, because the pushing account is a repo admin and admins are explicitly exempted while `enforce_admins` is false. To close that gap, `enforce_admins` needs to flip to `true`.

### Does `bypass_actors: []` really mean nobody bypasses, including the owner?

I pulled the live ruleset on `tsukumogami/tsuku` (`gh api /repos/tsukumogami/tsuku/rulesets/11136062`) rather than relying on docs alone, because GitHub's ruleset article doesn't spell this out explicitly. The response includes:

```json
"bypass_actors": [],
"current_user_can_bypass": "never"
```

`current_user_can_bypass` is evaluated for the actual authenticated caller — the keyring OAuth token used for this investigation, which carries `admin:org` scope (i.e., an org-owner-level account). GitHub itself reports that this account **cannot** bypass a ruleset with an empty bypass list, even though it holds admin/owner permissions. This is empirical, not inferred: an empty `bypass_actors` list really does mean nobody bypasses — not repo admins, not org owners — for the *ruleset* system. This is the key difference from classic protection's `enforce_admins`, which defaults to letting admins through unless explicitly flipped on. Rulesets default closed (no bypass unless you add someone); classic protection defaults open for admins (no enforcement on admins unless you explicitly enable `enforce_admins`).

### Plan availability (verified live + against docs)

Confirmed the org's live plan via `gh api /orgs/tsukumogami`: `{"plan":{"name":"free", ...}}`.

From `https://docs.github.com/en/get-started/learning-about-github/githubs-plans` and `https://github.com/pricing`:

| Feature | Free | Team ($4/user/mo, first 12 months) | Enterprise Cloud ($21/user/mo, first 12 months) |
|---|---|---|---|
| Branch protection (classic) — public repos | Yes | Yes | Yes |
| Branch protection (classic) — private repos | No | Yes | Yes |
| Repository rulesets — public repos | Yes | Yes | Yes |
| Repository rulesets — private repos | No | Yes | Yes |
| Organization-level rulesets (apply across all repos in the org, public and private) | No | Yes | Yes |

This matches and extends what was already established: org-level rulesets need Team; private-repo branch protection *and* private-repo rulesets both need at least Team (both surface as "Upgrade to GitHub Pro/Team" prompts in the UI, which is consistent — GitHub's own docs gate this at Team, with Pro sufficient for a single private repo owned by an individual account but the org here needs Team or above since it's an organization, not a personal account).

One nuance directly relevant to the goal ("a guardrail that makes this impossible across the whole organization"): even on Team, org-level rulesets are configured once and apply to every repo (public and private) that matches the target pattern, instead of needing a per-repo ruleset or classic-protection config replicated six-plus times. On Free, the only way to get *any* server-side protection on **public** repos is a per-repo ruleset or classic protection (as tsuku, shirabe, and koto already have); **private** repos get nothing server-side on Free — no classic protection, no rulesets, at any scope. That means on the current Free plan, the four private repos are categorically unprotectable server-side, regardless of which rule type is chosen.

### Org billing detail

`gh api /orgs/tsukumogami` shows `filled_seats: 1`, `seats: 0` — this looks like a single-member org (typical of a Free-plan personal-adjacent org). Team pricing is per active user per month, so the cash cost of upgrading is small at this org's current size (on the order of $4/month for a 1-seat org, scaling with headcount), though GitHub's billing UI would give the authoritative current-cycle number.

## Implications

- Neither `non_fast_forward` alone (what tsuku and shirabe currently have) nor `required_linear_history` (what shirabe has) would have stopped this incident — they target force-pushes and merge-commit topology, not ordinary fast-forward direct pushes. Both tsuku's and shirabe's current rulesets are effectively silent on this exact failure mode.
- koto's classic protection, despite having a required status check, would *not* have stopped this incident as currently configured, because `enforce_admins` is false and the pushing credentials are the repo owner's (admin-exempt). Fixing this doesn't require a new rule type — it requires flipping `enforce_admins` to true on koto, or migrating koto to a ruleset (which has no equivalent admin carve-out issue, since ruleset bypass defaults to nobody).
- The two rule types that reliably stop this exact class of incident — `pull_request` (require PR before any change reaches the branch) and `required_status_checks` (block direct pushes lacking a green check on that SHA) — are both available today, per-repo, on the Free plan for **public** repos. No upgrade is required to close the gap on tsuku, shirabe, koto, niwa, dot-niwa, and .github.
- The private repos are the actual blocker. There is no Free-plan path to any server-side push protection on them — not classic protection, not repository rulesets, not org-level rulesets. Closing the gap there requires GitHub Team (or Enterprise) at roughly $4/user/month.
- If the org upgrades to Team, org-level rulesets become available and are the cleaner mechanism for "make this impossible org-wide": one ruleset targeting `~ALL` or a branch-name pattern across every repo (public and private) rather than seven repo-level rulesets that have to be created and kept in sync individually.
- Given `bypass_actors: []` empirically blocks even an org-owner account, an org-level (or per-repo) ruleset with `pull_request` and/or `required_status_checks` rules and an empty bypass list is a strong, verifiably-closed guardrail — there's no implicit admin escape hatch the way there is with classic protection's `enforce_admins`.

## Surprises

- The `update` rule type is easy to overlook in favor of `non_fast_forward` because the names sound adjacent, but `update` is strictly broader — it has no special case for fast-forward pushes at all, and alone would already have stopped the incident. It's a blunter instrument than `pull_request` though: it doesn't require review workflow, just membership on a bypass list, so it doesn't add process, only a bypass-list gate.
- Classic protection's admin exemption is opt-out (`enforce_admins` defaults to not enforced) while ruleset bypass is opt-in (`bypass_actors` defaults to empty, i.e., nobody exempted). This is a meaningful asymmetry: a repo that migrates from classic protection to a ruleset without deliberately adding bypass actors becomes *more* restrictive by default, including for its own admins. Worth flagging since koto is the one repo currently on the classic system.
- The required-status-checks mechanism blocking a direct push isn't really "the push is validated and rejected" — it's a side effect of the check never having run at all for that SHA. A CI-based required check protects against this exact incident about as effectively as an explicit `pull_request` rule, but only as an accident of how check runs are scoped to SHAs, not by design intent. `pull_request` is the semantically clean rule for "this must go through review," and `required_status_checks` is closer to a semantically clean rule for "CI must have run and passed," which happens to also block ad hoc direct pushes as a byproduct.

## Open Questions

- Exact current Team-plan invoice pricing and whether there's an existing promotional/nonprofit rate available to this org — the $4/user/month figure is GitHub's public list price for "the first 12 months"; the steady-state renewal price should be checked in GitHub's actual billing settings for this org, not assumed to hold indefinitely.
- Whether the org wants one org-level ruleset (Team-plan-gated, covers public + private uniformly) versus faster no-upgrade-required per-repo rulesets on the six public repos plus accepting the private repos stay unprotected server-side until/unless the org upgrades. That's a scope decision for the design/plan phase, not something this research resolves.
- Whether `required_status_checks` is desirable as a *primary* guardrail given it has no dedicated CI workflow to hook into yet for niwa (the repo where the incident occurred) — a `pull_request` rule doesn't need a CI check to already exist, so it may be the faster guardrail to stand up first, with `required_status_checks` added once a CI job exists.
- Should verify empirically (read-only, e.g., by checking a differently-permissioned account's `current_user_can_bypass` if one becomes available) that the "no implicit admin bypass" ruleset behavior holds org-wide and isn't specific to this one ruleset/repo — the evidence gathered here is a single data point (tsuku's ruleset, one caller identity).

## Summary

Only three of the seven candidate rule types actually stop this exact incident — `pull_request`, `required_status_checks`, and the ruleset `update` rule — while `non_fast_forward`, `deletion`, `creation`, and `required_linear_history` do not, and koto's existing classic protection wouldn't have blocked it either because `enforce_admins` is off and the push came from an admin-exempt account. The main implication: closing this gap on the six public repos needs no plan upgrade (Free already supports per-repo rulesets/protection with `pull_request` or `required_status_checks` and, empirically, an empty bypass list blocks even an org-owner account), but the organization's private repos have zero server-side push-protection options on Free at any scope, so a true org-wide guardrail — one that also covers private repos and is configured once — requires upgrading to GitHub Team (~$4/user/month) to unlock organization-level rulesets. The biggest open question is a scope call for the next phase: ship repo-by-repo protection on the public repos now versus wait for/pursue the Team upgrade to get one org-wide rule covering everything at once.
