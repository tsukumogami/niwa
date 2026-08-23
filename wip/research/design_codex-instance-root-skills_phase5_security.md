# Security Review: codex-instance-root-skills

Serial-self review under the parent-orchestration dispatch context.

## Dimension Analysis

### External Artifact Handling
**Applies: Yes (bounded).** The root delivery links to trees the
instance already holds: fetched marketplace content (existing tarball
machinery, unchanged) and the binary-embedded niwa tree (no network).
No new fetch path. Delivered names are single path elements via
deliverableName; the deliberate no-containment-pass posture for
symlink deliveries carries over with the same justification as the
per-repo delivery. Mitigation: name rule at path-build time; niwa tree
name is a constant.

### Permission Scope
**Applies: Yes.** All new writes inside the instance; trust line
untouched by construction (skills load untrusted per the spike; config
keys stay inert). One pre-existing outside-instance write
(plugin.Install to ~/.claude) is bound, not widened. Key risk found:
EnsureRepoExclude searches upward and could write into an enclosing
repository's exclude file if used for root exclusions -- the design
forbids it and routes exclusions through EnsureInstanceGitignore /
InstanceExcludePatterns. Failures are loud (N2).

### Supply Chain / Dependency Trust
**Applies: No new surface.** Embedded tree ships in the binary; adding
.claude-plugin/plugin.json is content the build embeds, not a fetched
artifact. No new module dependencies (N1).

### Data Exposure
**Applies: Minimal.** Skills trees are non-secret workspace content;
the root .codex/ carries no config document, so payload-config secret
obligations (0600, excludes) do not attach. The live acceptance check
runs under an isolated CODEX_HOME with no credential present and its
gate deliberately does not copy the developer's credential (R19).

## Recommended Outcome

**OPTION 2 - Document considerations.** No design changes needed. The
Security Considerations section drafted into the design covers: trust
line, above-instance write prohibition (EnsureRepoExclude), path
safety, no secret movement, credential-free live check, deterministic
collisions.

## Summary

The design widens where already-declared content is readable, not what
a session can do; its one novel hazard (upward-searching exclude
writes from the instance root) is named and forbidden. Documented
considerations suffice.
