# Decision 1: Command Surface

How should pull be triggered for managed repos in a niwa workspace?

Scoring key: 0 = poor fit, 1 = acceptable, 2 = strong fit.

## Options Evaluated

### Option A: Separate `niwa sync` command

A dedicated command for pulling repos, independent of apply.

```
niwa sync                    # pull all repos
niwa sync --instance dev     # target specific instance
```

**Pros:**
- Architecturally clean: each command has one job (apply = converge config, sync = freshen repos)
- Matches industry consensus -- repo, meta, gita, mani all use explicit sync commands
- No backward compatibility risk whatsoever
- Natural place to hang advanced flags later (--rebase, --parallel, --force)
- Clear documentation story

**Cons:**
- Two commands for the common case ("make my workspace current" = sync + apply)
- Requires discipline: users must remember to run sync, or repos stay stale
- Config sync already happens inside apply, so the boundary is already blurred -- config freshness is automatic, repo freshness is manual
- Doesn't match user's stated goal of "one low-friction operation"

**Driver scores:**
| Driver | Score | Notes |
|--------|-------|-------|
| Non-destructive | 2 | Explicit invocation = no surprises |
| Low friction | 0 | Requires a separate step every session |
| Backward compatibility | 2 | Zero impact on existing behavior |
| Clarity of mental model | 2 | Clean separation of concerns |
| Safe git operations | 2 | Same fetch + ff-only strategy regardless |

**Total: 8/10**

**Risks:**
- Users forget to sync, then blame niwa for stale code
- The config-already-auto-pulls precedent makes the separation feel inconsistent

---

### Option B: `niwa apply --pull` flag

Apply gains an opt-in flag that also pulls existing repos during the apply pipeline.

```
niwa apply --pull              # apply config AND pull repos
niwa apply --pull --instance dev
```

**Pros:**
- Single command for "make workspace current" when you want it
- Fully backward compatible: apply without --pull behaves identically to today
- Discoverable via `niwa apply -h`
- Composable with existing flags (--instance, --allow-dirty)
- Low implementation risk: slot pull logic into the existing apply pipeline

**Cons:**
- Adds a third flag to apply (alongside --instance and --allow-dirty)
- Slightly ambiguous: "does --pull pull config, repos, or both?" (answer: repos only, since config already auto-pulls)
- Still requires remembering the flag -- not zero friction
- Muddies apply's conceptual purity (converge config vs. freshen repos)

**Driver scores:**
| Driver | Score | Notes |
|--------|-------|-------|
| Non-destructive | 2 | Opt-in means no surprises |
| Low friction | 1 | One command but requires remembering the flag |
| Backward compatibility | 2 | Zero impact without the flag |
| Clarity of mental model | 1 | Apply now does two things, but opt-in makes it manageable |
| Safe git operations | 2 | Same fetch + ff-only strategy |

**Total: 8/10**

**Risks:**
- Flag fatigue: users who always want pull must always type --pull (mitigated by shell aliases)
- "Why doesn't apply just do this?" becomes a recurring question

---

### Option C: `apply` always pulls (new default)

Apply gains pull-where-safe behavior with no opt-out. Every apply run fetches and fast-forward-pulls clean repos on their default branch.

```
niwa apply  # always pulls config + repos
```

**Pros:**
- Maximum simplicity: one command, zero flags, workspace is current
- Achieves the "pit of success" -- the easy path is the correct path
- Conceptually clean: apply = "converge workspace to desired state" including freshness
- Consistent with config auto-pull precedent (SyncConfigDir already does this)

**Cons:**
- Breaks backward compatibility: users who run apply expecting no network calls to repos get new behavior
- Users with dirty repos or non-default branches see new warnings on every apply
- Slower: every apply now does network I/O for all repos even when unnecessary
- No way to skip pulls for users who don't want them
- Highest surprise factor of all options

**Driver scores:**
| Driver | Score | Notes |
|--------|-------|-------|
| Non-destructive | 2 | Still uses safe ff-only strategy, skips dirty repos |
| Low friction | 2 | Zero extra steps or flags |
| Backward compatibility | 0 | Changes existing behavior with no opt-out |
| Clarity of mental model | 1 | "Apply does everything" is simple but surprising to existing users |
| Safe git operations | 2 | Same fetch + ff-only strategy |

**Total: 7/10**

**Risks:**
- Users in CI or scripted workflows get unexpected network calls and potential failures
- No escape hatch for environments where pulling is undesirable (air-gapped, metered connections)

---

### Option D: `apply` pulls by default, `--no-pull` opt-out

Apply gains pull-where-safe behavior as the new default, with --no-pull for users who want the old behavior.

```
niwa apply             # pulls config + repos (new default)
niwa apply --no-pull   # old behavior: config only, skip repo pulls
```

**Pros:**
- Zero friction for the common case (daily freshness)
- Opt-out available for users who need it (CI, air-gapped, metered)
- Consistent with config auto-pull precedent
- "Apply = make workspace current" is a strong, simple mental model
- The opt-out flag provides the escape hatch Option C lacks

**Cons:**
- Still changes default behavior -- existing users see new output and warnings
- --no-pull is a negative flag (generally considered a UX smell, but common in CLIs)
- Apply now has three flags with a negative one mixed in
- Users must learn about --no-pull to restore old behavior
- Slower default: network I/O on every apply

**Driver scores:**
| Driver | Score | Notes |
|--------|-------|-------|
| Non-destructive | 2 | Safe ff-only strategy, skips dirty repos |
| Low friction | 2 | Zero extra steps for the common case |
| Backward compatibility | 1 | Changed default, but opt-out exists |
| Clarity of mental model | 1 | Simple default, but "why is it pulling?" surprises existing users initially |
| Safe git operations | 2 | Same fetch + ff-only strategy |

**Total: 8/10**

**Risks:**
- Existing scripts or habits break silently (apply takes longer, produces new output)
- The transition period creates confusion ("apply used to be fast, now it's slow")
- Negative flags tend to accumulate (--no-pull, --no-drift-check, etc.)

---

## Comparison Matrix

| Driver | Weight | A: sync | B: --pull | C: always | D: default+opt-out |
|--------|--------|---------|-----------|-----------|---------------------|
| Non-destructive | High | 2 | 2 | 2 | 2 |
| Low friction | High | 0 | 1 | 2 | 2 |
| Backward compat | Medium | 2 | 2 | 0 | 1 |
| Mental model | Medium | 2 | 1 | 1 | 1 |
| Safe git ops | Low | 2 | 2 | 2 | 2 |
| **Raw total** | | **8** | **8** | **7** | **8** |

Raw scores are tied at 8 for A, B, and D. Applying weights breaks the tie:

- **Option A** scores perfectly on everything except the highest-weighted driver (low friction = 0). For a daily-use tool, this is disqualifying as a standalone solution.
- **Option B** is solid across the board but scores only 1 on low friction. Users must remember the flag every time.
- **Option D** matches the user's stated goal (one operation, zero flags) and only loses a point on backward compatibility, which is mitigated by the opt-out.
- **Option C** is Option D without the escape hatch, making it strictly worse.

## Recommendation

**Option D: `niwa apply` pulls by default with `--no-pull` opt-out.**

Rationale:

1. **Matches the user's stated need.** The primary use case is daily workspace freshness for Claude sessions. "Run `niwa apply`, everything is current" is the target experience. Option D is the only one that achieves this without requiring the user to remember a flag or a second command.

2. **Config auto-pull precedent.** Apply already auto-pulls the workspace config via SyncConfigDir. Extending this to managed repos is a natural evolution of the same pattern, not a conceptual break. Users already expect apply to reach the network.

3. **Opt-out addresses the backward compatibility gap.** The --no-pull flag gives CI pipelines, scripted workflows, and air-gapped environments a clean way to restore old behavior. This is a well-established CLI pattern (git pull --no-rebase, docker build --no-cache).

4. **Safe-by-design pull strategy limits blast radius.** Since pull only touches repos that are clean, on the default branch, and behind remote -- and skips everything else with warnings -- the behavior change is benign for most real workspaces. Users with dirty repos or feature branches see a warning, not a failure.

5. **Industry tools use separate sync, but niwa already broke that pattern.** The config auto-pull in apply means niwa isn't a pure "apply config only" tool. Adding repo pull completes the story rather than introducing inconsistency.

**Confidence: Medium-High**

The recommendation is sound for the stated use case (daily freshness, single user, small workspace). Confidence isn't "high" because the backward compatibility concern is real -- any user who has `niwa apply` in a script will see changed behavior. The mitigation (--no-pull, safe-skip strategy, warnings not errors) is solid but not invisible.

## Assumptions

For this recommendation to hold:

1. **The primary use case is daily, interactive use** -- a developer running `niwa apply` before starting a Claude session. If the primary use case were CI/CD or automated scripting, Option B would be better.

2. **Workspaces are small enough that pulling all repos is fast** (seconds, not minutes). For workspaces with 50+ repos, the network overhead of default pulling could be painful and would push toward Option B.

3. **Most repos in a typical workspace are clean and on their default branch.** If users routinely have dirty repos or feature branches across many sibling repos, the warning noise from default pulling would be excessive.

4. **The safe-skip strategy (clean + default branch + behind remote) is sufficient.** No user will lose work from the default behavior. This is guaranteed by the ff-only constraint but depends on correct implementation of the state checks.

5. **The --no-pull flag is discoverable enough** for users who need it. It should appear in `niwa apply -h` output and in any migration notes.

## Rejected Alternatives

- **Option A (separate `niwa sync`)**: Rejected because it scores zero on low friction, the highest-weighted driver. The user explicitly wants one operation. A separate sync command could be added later as a power-user tool, but it shouldn't be the primary mechanism. The config auto-pull precedent already blurs the apply/sync boundary.

- **Option B (`niwa apply --pull`)**: Rejected because it requires remembering a flag on every invocation. For a daily-use command, opt-in friction compounds. Shell aliases mitigate this but push the problem to user configuration. B is the right answer if backward compatibility is weighted above low friction -- but the user's stated priority is the opposite.

- **Option C (apply always pulls, no opt-out)**: Rejected because it's strictly worse than Option D. Same benefits, but no escape hatch for CI, scripting, or constrained network environments. There's no reason to omit the --no-pull flag.
