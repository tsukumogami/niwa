# Plan Milestone: session-store-teardown

## Milestone

**Name**: session-store-teardown

**Description**: `Design: \`docs/designs/DESIGN-session-store-teardown.md\``

**Source Document**: `docs/designs/DESIGN-session-store-teardown.md`

## Scope Assessment

**Estimated issues**: 7

**Assessment**: normal (3-15). The design is one cohesive change to one
repository: an ordering primitive plus the callers it covers, and a teardown
resolver that consumes one of those callers. Splitting it would separate the
lock from the writers it orders.
