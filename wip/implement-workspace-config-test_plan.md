# Test Plan: Workspace config format

Generated from: docs/designs/DESIGN-workspace-config.md
Issues covered: 8
Total scenarios: 28

---

## Scenario 1: Parse minimal workspace.toml
**ID**: scenario-1
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Create temp dir with `.niwa/workspace.toml` containing `[workspace] name = "test"`
- Call config parsing function on the file
**Expected**: WorkspaceConfig struct populated with name "test", all other fields at zero values. No parse error.
**Status**: pending

---

## Scenario 2: Parse full schema without error
**ID**: scenario-2
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Create `.niwa/workspace.toml` with all sections: workspace, sources, groups, repos, content, hooks, settings, env, channels
- Call config parsing function
**Expected**: All fields populated correctly. Unparsed/stub sections (hooks, settings, env, channels) parse without error even before their generation logic exists.
**Status**: pending

---

## Scenario 3: Config discovery walks up directories
**ID**: scenario-3
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Create `$TMPDIR/root/.niwa/workspace.toml`
- Call config discovery from `$TMPDIR/root/foo/bar/baz/`
**Expected**: Discovery finds `.niwa/workspace.toml` at `$TMPDIR/root/`. Returns correct path.
**Status**: pending

---

## Scenario 4: Config discovery fails when no workspace.toml exists
**ID**: scenario-4
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Create empty temp dir with no `.niwa/` directory
- Call config discovery from that dir
**Expected**: Returns a clear error indicating no workspace.toml found.
**Status**: pending

---

## Scenario 5: GitHub API repo discovery via mocked client
**ID**: scenario-5
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Configure mock GitHub client returning 3 repos for org "testorg" (2 public, 1 private)
- Parse config with `[[sources]] org = "testorg"` and groups filtering by visibility
- Run classification
**Expected**: 2 repos classified into public group, 1 into private group. No warnings or errors.
**Status**: pending

---

## Scenario 6: Repo matching no group produces warning
**ID**: scenario-6
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Mock GitHub returns repos with visibility "internal"
- Groups only define "public" and "private" visibility filters
- Run classification
**Expected**: Internal repos excluded with a warning message naming the unclassified repos.
**Status**: pending

---

## Scenario 7: Repo matching multiple groups produces error
**ID**: scenario-7
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Define two groups both matching the same repo (e.g., visibility "public" and explicit repos list containing same repo)
- Run classification
**Expected**: Error naming the repo and the conflicting group names. Apply does not proceed.
**Status**: pending

---

## Scenario 8: Clone repos into group directories
**ID**: scenario-8
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Set up workspace with mocked GitHub client and two groups
- Run apply with git clone stubbed/mocked
**Expected**: Clone called for each classified repo with target path `{instance_root}/{group}/{repo}/`. Group directories created if missing.
**Status**: pending

---

## Scenario 9: Workspace content file written with stub template expansion
**ID**: scenario-9
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- Create `.niwa/claude/workspace.md` with content "Workspace: {workspace_name}"
- Configure `content_dir = "claude"` and `[content.workspace] source = "workspace.md"`
- Run apply
**Expected**: `{instance_root}/CLAUDE.md` exists and contains "Workspace: test" (template variable expanded).
**Status**: pending

---

## Scenario 10: niwa apply wired to cobra CLI
**ID**: scenario-10
**Category**: Infrastructure
**Testable after**: #1
**Commands**:
- `go build -o niwa ./cmd/niwa`
- `./niwa apply --help`
**Expected**: Help output shows apply command with description. Exit code 0.
**Status**: pending

---

## Scenario 11: Group content placed as CLAUDE.md in group directory
**ID**: scenario-11
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Configure `[content.groups.public] source = "public.md"`
- Create `.niwa/claude/public.md` with "Group: {group_name}"
- Run apply
**Expected**: `{instance_root}/public/CLAUDE.md` exists with content "Group: public".
**Status**: pending

---

## Scenario 12: Repo content placed as CLAUDE.local.md
**ID**: scenario-12
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Configure `[content.repos.myrepo] source = "repos/myrepo.md"`
- Create the source file with "Repo: {repo_name}"
- Run apply with myrepo classified into "public" group
**Expected**: `{instance_root}/public/myrepo/CLAUDE.local.md` exists with content "Repo: myrepo".
**Status**: pending

---

## Scenario 13: Subdirectory content placed as CLAUDE.local.md in subdirectory
**ID**: scenario-13
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Configure `[content.repos.myrepo.subdirs] recipes = "repos/myrepo-recipes.md"`
- Create the source file
- Run apply
**Expected**: `{instance_root}/public/myrepo/recipes/CLAUDE.local.md` exists with expected content.
**Status**: pending

---

## Scenario 14: Template variable expansion uses plain string replacement
**ID**: scenario-14
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Create content file with all four variables: `{workspace}`, `{workspace_name}`, `{repo_name}`, `{group_name}`
- Run apply
**Expected**: All four variables replaced with correct values. No Go template syntax processed (e.g., `{{.Foo}}` left verbatim if present).
**Status**: pending

---

## Scenario 15: Content auto-discovery from content_dir convention
**ID**: scenario-15
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Configure `content_dir = "claude"` with no explicit `[content.repos.myrepo]` entry
- Create `.niwa/claude/repos/myrepo.md`
- Run apply with myrepo classified
**Expected**: `CLAUDE.local.md` written for myrepo using the auto-discovered source file.
**Status**: pending

---

## Scenario 16: Gitignore warning when writing CLAUDE.local.md
**ID**: scenario-16
**Category**: Infrastructure
**Testable after**: #2
**Commands**:
- Set up a repo directory with a `.gitignore` that does NOT contain `*.local*`
- Write CLAUDE.local.md to that repo
**Expected**: Warning emitted mentioning the missing `*.local*` pattern. File still written.
**Status**: pending

---

## Scenario 17: Source auto-discovery threshold enforcement
**ID**: scenario-17
**Category**: Infrastructure
**Testable after**: #3
**Commands**:
- Mock GitHub returns 15 repos for org "bigorg"
- Config has `[[sources]] org = "bigorg"` with no max_repos override
**Expected**: Error with clear message: org exceeds default threshold of 10, suggest setting max_repos or using explicit repos list.
**Status**: pending

---

## Scenario 18: Per-source max_repos override
**ID**: scenario-18
**Category**: Infrastructure
**Testable after**: #3
**Commands**:
- Mock GitHub returns 15 repos
- Config has `max_repos = 20` on the source
**Expected**: Discovery succeeds, all 15 repos processed.
**Status**: pending

---

## Scenario 19: Explicit repos list skips GitHub API
**ID**: scenario-19
**Category**: Infrastructure
**Testable after**: #3
**Commands**:
- Config has `repos = ["repo-a", "repo-b"]` on a source
- Mock GitHub client that would error if called
**Expected**: Only repo-a and repo-b processed. GitHub API not called for this source.
**Status**: pending

---

## Scenario 20: Multiple sources merge repos and detect duplicates
**ID**: scenario-20
**Category**: Infrastructure
**Testable after**: #3
**Commands**:
- Two sources, both returning a repo named "shared-lib"
**Expected**: Error: duplicate repo name "shared-lib" across sources.
**Status**: pending

---

## Scenario 21: Group with explicit repos list
**ID**: scenario-21
**Category**: Infrastructure
**Testable after**: #4
**Commands**:
- Configure `[groups.infra] repos = ["terraform", "deploy"]`
- Repos "terraform" and "deploy" discovered from sources
**Expected**: Both repos classified into "infra" group.
**Status**: pending

---

## Scenario 22: claude = false skips config generation for repo
**ID**: scenario-22
**Category**: Infrastructure
**Testable after**: #5
**Commands**:
- Configure `[repos.".github"] claude = false`
- Run apply with ".github" repo discovered and classified
**Expected**: No CLAUDE.local.md written for ".github". No hooks/settings/env generated for it.
**Status**: pending

---

## Scenario 23: Per-repo settings override workspace defaults
**ID**: scenario-23
**Category**: Infrastructure
**Testable after**: #5
**Commands**:
- Workspace `[settings] permissions = "bypass"`
- `[repos.vision.settings] permissions = "ask"`
- Run apply
**Expected**: vision repo gets permissions = "ask". Other repos get permissions = "bypass".
**Status**: pending

---

## Scenario 24: Instance state written and updated on apply
**ID**: scenario-24
**Category**: Infrastructure
**Testable after**: #6
**Commands**:
- Run apply for the first time
- Read `.niwa/instance.json`
- Run apply again
**Expected**: First apply creates instance.json with schema_version, config_name, instance_name, created, last_applied, managed_files with hashes. Second apply updates last_applied and managed_files hashes.
**Status**: pending

---

## Scenario 25: Drift detection warns on modified managed file
**ID**: scenario-25
**Category**: Infrastructure
**Testable after**: #6
**Commands**:
- Run apply (writes CLAUDE.md and records hash in instance.json)
- Manually modify the written CLAUDE.md
- Run apply again
**Expected**: Warning that CLAUDE.md has been modified since last apply. File overwritten with new content.
**Status**: pending

---

## Scenario 26: Global registry parsed and used for workspace lookup
**ID**: scenario-26
**Category**: Infrastructure
**Testable after**: #7
**Commands**:
- Create `$XDG_CONFIG_HOME/niwa/config.toml` with `[registry.myws] root = "/tmp/myroot"`
- Call registry lookup for "myws"
**Expected**: Returns root "/tmp/myroot". Respects XDG_CONFIG_HOME. Missing file returns empty defaults without error.
**Status**: pending

---

## Scenario 27: Name validation rejects directory traversal
**ID**: scenario-27
**Category**: Infrastructure
**Testable after**: #8
**Commands**:
- Set group name to `../../etc`
- Set repo name to `foo/../../bar`
- Set content source to `../../../etc/passwd`
**Expected**: All three rejected with clear error messages naming the offending value and the validation rule.
**Status**: pending

---

## Scenario 28: End-to-end apply with real GitHub API
**ID**: scenario-28
**Category**: Use-case
**Environment**: manual -- requires GitHub API access and a test org with known repos
**Testable after**: #1, #2, #3, #4, #5, #6, #7, #8
**Commands**:
- Create a workspace root directory
- Place a `.niwa/workspace.toml` pointing to a real GitHub org (e.g., "tsukumogami") with content_dir, groups by visibility, and content source files
- Create corresponding content source files with template variables
- Run `niwa apply` from within the workspace root
**Expected**:
- Repos discovered from the org via GitHub API
- Repos classified into correct groups by visibility
- Group directories created with repos cloned inside
- `CLAUDE.md` written at workspace root with template variables expanded
- `CLAUDE.md` written in each group directory
- `CLAUDE.local.md` written in each repo directory
- `.niwa/instance.json` created with correct metadata and file hashes
- Global registry updated at `~/.config/niwa/config.toml`
- Warnings for repos missing `*.local*` in gitignore
- Second run of `niwa apply` is idempotent (no unnecessary re-clones, hashes match)
**Status**: pending
