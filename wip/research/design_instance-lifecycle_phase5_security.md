# Security Review: instance-lifecycle

## Dimension Analysis

### External Artifact Handling
**Applies:** No. The lifecycle commands operate on already-cloned repos and existing config. No new external inputs are fetched (that's init's job).

### Permission Scope
**Applies:** Yes, but minimal. All operations use user-level filesystem access. DestroyInstance validates .niwa/instance.json exists before RemoveAll, preventing accidental deletion of arbitrary directories.

### Supply Chain or Dependency Trust
**Applies:** No. No new dependencies introduced. Git is already a dependency.

### Data Exposure
**Applies:** No. No data transmitted. Status reads local state only.

## Recommended Outcome
**OPTION 2 - Document considerations.** The destroy safety check (validate instance.json before RemoveAll) and uncommitted changes warning are worth documenting. Already present in the design doc's Security Considerations section.

## Summary
Clean security profile. The main risk (accidental directory deletion) is mitigated by the instance.json validation check. No design changes needed.
