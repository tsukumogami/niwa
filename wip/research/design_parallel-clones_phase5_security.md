# Security Review: parallel-clones

## Dimension Analysis

### External Artifact Handling

**Applies:** No

This design changes the scheduling of git clone operations — how many run
concurrently — but not what is cloned or how. Clone URLs are already present
in the workspace config and validated by Steps 1 and 2 of the pipeline before
Step 3 (where the parallelism lands). No new external inputs are introduced.

### Permission Scope

**Applies:** No

Workers use the same `exec.CommandContext(ctx, "git", ...)` calls as the current
sequential code. No new filesystem permissions are needed. The partial clone
directories left behind by context cancellation are within the instance root, which
is owned by the user and cleaned up by the existing `os.RemoveAll(instanceRoot)`
call in Create on failure.

### Supply Chain or Dependency Trust

**Applies:** No

No new dependencies are introduced. The implementation uses Go standard library
primitives: goroutines, channels, `context.WithCancel`. No third-party concurrency
library is added.

### Data Exposure

**Applies:** No

Workers do not log credentials or transmit any data beyond what the existing git
clone operations already do. The summary spinner ("cloning repos... 3/10 done")
exposes only repo counts, not repo names or URLs.

## Recommended Outcome

**OPTION 3 - N/A with justification:**

This design parallelizes the execution scheduling of existing git clone operations.
It does not change what is cloned, from where, with what credentials, or what
data is exposed. No new attack surfaces are introduced. The context cancellation
mechanism (which kills git processes mid-clone) is a standard Go pattern with no
security implications; partial directories left in the instance root are harmless
and cleaned up by the existing error path.

## Summary

Parallelizing git clones is a scheduling change, not a privilege or trust change.
All security dimensions — external artifact handling, permission scope, supply chain
trust, data exposure — are unaffected. The security review recommends Option 3
(N/A with justification).
