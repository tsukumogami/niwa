<!-- decision:start id="config-location" status="confirmed" -->
### Decision 7: Config and content file location

**Context**

The design defines workspace.toml and a content directory (source markdown files that become CLAUDE.md files) but doesn't specify where they live relative to the workspace root and instances. The config needs to be version-controlled, shareable via GitHub, and discoverable by `niwa apply` when run from within an instance. It also needs to survive instance creation and destruction, since instances are ephemeral subdirectories.

The current bash installer stores its equivalent (install.sh + content files) inside a managed repo (`tsukumogami/tools`), but this creates a circular dependency for niwa: the config would live inside something the config defines.

**Assumptions**

- The workspace root is not itself a git repo — it's a plain directory containing the config and instance subdirectories.
- Content files, hook scripts, and env files must travel alongside workspace.toml since they're referenced by relative paths.

**Chosen: .niwa/ directory at the workspace root**

workspace.toml and its content directory live in `.niwa/` at the workspace root. When initialized from a remote source (`niwa init --from <org/repo>`), `.niwa/` is the git checkout of the config repo. When scaffolded locally, it's a plain directory.

```
tsuku-root/
  .niwa/                          # config directory (git repo if from remote)
    workspace.toml
    claude/                       # content source files
      workspace.md
      public.md
      private.md
      repos/
        tsuku.md
        koto.md
    hooks/                        # hook scripts referenced by [hooks]
      gate-online.sh
    env/                          # env files referenced by [env]
      workspace.env
  tsuku/                          # instance 1
    .niwa/                        # instance state (NOT a git repo)
      instance.json
    CLAUDE.md                     # generated from claude/workspace.md
    public/
      CLAUDE.md                   # generated from claude/public.md
      tsuku/                      # cloned repo
        CLAUDE.local.md           # generated from claude/repos/tsuku.md
      koto/
        CLAUDE.local.md
    private/
      CLAUDE.md
      vision/
        CLAUDE.local.md
  tsuku-2/                        # instance 2
    .niwa/
      instance.json
    ...
```

`.niwa/` serves dual purpose at two levels, distinguished by contents:
- **Workspace root** `.niwa/`: contains `workspace.toml` — the config source
- **Instance** `.niwa/`: contains `instance.json` — local state

Discovery: `niwa apply` walks up from cwd looking for `.niwa/workspace.toml` (config root) or `.niwa/instance.json` (instance root). Finding instance.json means you're in an instance; keep walking up to find workspace.toml.

The `content_dir` field in workspace.toml is relative to the directory containing workspace.toml (i.e., `.niwa/`). So `content_dir = "claude"` resolves to `.niwa/claude/`.

**Rationale**

The workspace root stays clean — no config files or content directories cluttering the top level alongside instances. The `.niwa/` dotdir convention is already established for instance state, so extending it to the root level is natural. When the config comes from a remote source, `.niwa/` being the git checkout means `niwa update` is just a `git pull` (or fetch + diff) inside that directory. For locally-scaffolded workspaces, `.niwa/` is a plain directory that could later be turned into a repo with `git init` if the user wants to share it.

**Alternatives Considered**

- **Flat in workspace root**: workspace.toml and claude/ sit directly at the root alongside instances. Rejected because it's not portable to GitHub (the root isn't a git repo), clutters the workspace root with config files, and mixes config with instance directories.

- **Inside a managed repo**: workspace.toml lives in one of the repos niwa manages (e.g., a "tools" repo). Rejected because it creates a circular dependency — the config defines the repos, so it can't live inside one of them. Also breaks the discovery model since `niwa apply` would need to know which repo contains the config.

- **Named subdirectory (e.g., niwa-config/)**: A visible directory instead of a dotdir. Rejected because it adds a visible directory to the workspace root that isn't an instance, and doesn't reuse the existing `.niwa/` convention.

**Consequences**

- The existing hierarchy diagram in the design doc needs updating to show `.niwa/` at the workspace root instead of flat workspace.toml.
- The `niwa apply` discovery logic becomes: walk up looking for `.niwa/workspace.toml`, with `.niwa/instance.json` as a waypoint indicating you're inside an instance.
- `content_dir` and other relative paths in workspace.toml resolve from `.niwa/`, not from the workspace root.
- The registry's `root` field still points to the workspace root (parent of instances), not to `.niwa/`.
<!-- decision:end -->
