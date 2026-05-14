<!-- decision:start id="plugin-distribution" status="assumed" -->
### Decision: How niwa installs the migration skill plugin

**Context**

niwa detects when a workspace is on the deprecated rank-2 config source layout (root `workspace.toml`) and emits a one-time deprecation notice pointing at a slash command (`/niwa:migrate-config <name>`) that resolves a Claude Code skill plugin. The plugin is owned by the niwa project -- its skill files live in the niwa repo and its release cadence is tied to niwa's. The question is the delivery mechanism: how the plugin files land on the user's machine at `~/.claude/plugins/marketplaces/<plugin>/`.

Three constraints dominate: (1) auto-install -- the user must not have to run setup commands after seeing the deprecation notice; (2) offline tolerance -- niwa runs on developer laptops that may be air-gapped or behind locked-down networks; (3) version coherence -- the skill calls niwa with specific flag shapes and parses specific JSON, so plugin/binary version drift is a silent-misbehaviour risk. A fourth constraint -- no shell-out to a `claude` CLI -- rules out delegating installation to Claude Code's own plugin manager.

The plugin payload is text-only (markdown skill files plus two small JSON manifests), comfortably under 200 KB. niwa already ships as a single binary via GitHub releases with sha256-verified `install.sh`; it has no existing `//go:embed` usage but no architectural objection to it.

**Assumptions**

- `~/.claude/plugins/marketplaces/<plugin>/` is the right install location (verified by inspecting the local shirabe plugin's resolved skill layout).
- Plugin contents are static at release time -- no runtime templating needed; the skill reads niwa state at runtime via niwa CLI calls.
- Plugin payload stays small (no binary assets baked in).
- Claude Code resolves slash commands by directory presence at `~/.claude/plugins/marketplaces/<plugin>/skills/<skill>/SKILL.md` without requiring an entry in `installed_plugins.json`. If this turns out wrong, niwa needs to merge a JSON entry into `installed_plugins.json` atomically -- a small addition applied equally to whichever option is chosen, not a tiebreaker.
- This decision was made in --auto mode without user confirmation of the chosen option.

**Chosen: Embed plugin in the niwa binary via Go's `embed` package**

The plugin source tree lives in the niwa repo at e.g. `plugin/` and is declared as:

```go
//go:embed plugin
var pluginFS embed.FS
```

When niwa needs the plugin on disk -- on first detection of a rank-2 source, on first invocation of the migrate command, or on `niwa apply` for any workspace that pulls in the migration scenario -- it walks `pluginFS` and writes each file to `~/.claude/plugins/marketplaces/<niwa-plugin-name>/` using atomic write semantics (temp dir + rename). A version sentinel file (`~/.claude/plugins/marketplaces/<niwa-plugin-name>/.installed-version`) records the niwa version that wrote the tree; if the sentinel matches the running binary's version, the extract is a no-op (idempotent). Mismatch triggers a full re-extract, replacing the existing tree atomically.

The plugin and the niwa binary are produced from the same source tree, in the same release. The user has exactly one artifact to install (niwa); the plugin appears on disk the next time niwa runs.

**Rationale**

The choice is dominated by **version coherence** and **offline tolerance**, and embed wins on both with the minimum number of moving parts:

- *Version coherence is automatic.* The plugin can never drift from the binary because they are the same artifact. The skill's expectations about `niwa source inspect --json` output are guaranteed to match the binary that produced it.
- *Offline tolerance is automatic.* No network call lives in the install path. Air-gapped users get the migration tool the same way they got niwa -- by copying the binary to the target machine.
- *Auto-install UX is preserved.* The user sees the deprecation notice, runs `/niwa:migrate-config <name>` in Claude Code, and the skill resolves because niwa wrote the plugin to disk on its previous invocation. Zero extra commands.
- *The binary-size cost is negligible.* The plugin is text-only and small; +50-200 KB on a multi-MB Go binary is noise.
- *No release-pipeline changes.* Goreleaser-equivalent CI keeps publishing the binary as the single asset (plus checksums). Nothing in `install.sh` changes.
- *Failure surface is one path.* Extracting from `embed.FS` to disk can fail only at the disk-write step, which has obvious error reporting. Download-based options have three failure modes (network, checksum, extract) and the hybrid carries all of them plus the embed path.

**Alternatives Considered**

- **Download from GitHub releases (Alt 2)**: Rejected. The plugin payload is small enough that the network round-trip buys nothing. The download path introduces three failure modes (timeout, checksum mismatch, extract error) that the embed path avoids entirely. Air-gapped users would need a fallback, which collapses the option into either (a) print-only or (b) hybrid. The only thing download-from-releases would buy is the ability to ship a plugin update without re-releasing niwa -- which is the wrong direction given the version-coherence requirement.

- **Print install instructions only (Alt 3)**: Rejected. Violates the explicit auto-install requirement. Users would have to remember and type at least two commands (`claude plugin marketplace add ...`, `claude plugin install ...`) before the slash command resolves. The decision context says "the user shouldn't have to run extra commands"; this option requires exactly that.

- **Hybrid: download first, fall back to embed (Alt 4)**: Rejected. Pays the embed cost (binary growth) AND the download cost (release asset, network failure modes, version-mismatch handling) AND adds orchestration code between the two paths. The only scenario it improves over pure embed is "user wants a newer skill against an older binary" -- which is precisely the version drift the constraint forbids. The combination is strictly worse than embed alone.

**Consequences**

What changes:
- The niwa repo grows a `plugin/` (or similarly named) directory containing the marketplace.json, plugin.json, and the migration skill's SKILL.md plus references. CI lints and tests that directory the way the shirabe repo lints its skills.
- niwa gains an `embed.FS`-backed installer module (somewhere under `internal/plugin/` or `internal/claude/`) with a clear "extract to target, atomic, idempotent via version sentinel" contract.
- The extract runs eagerly the first time niwa sees a rank-2 source (so the deprecation notice's slash command resolves immediately when the user opens Claude Code), and on every binary upgrade (when the version sentinel doesn't match).
- The niwa binary grows by the plugin payload size -- on the order of tens to a couple hundred KB. Within noise relative to current binary size.

What becomes easier:
- Version coherence: not a thing to think about; the plugin is part of the binary.
- Offline distribution: works automatically. Copy `niwa` to the air-gapped host, run it once, the plugin appears.
- Single-source-of-truth for the skill: the source of record is the niwa repo, and what ships is exactly what's there at release tag.
- Reproducing user bugs: "what version of the plugin did you have?" has a single answer -- the version of the niwa binary.

What becomes harder:
- Out-of-band plugin updates. If a skill bug needs a hotfix, the fix ships in the next niwa release rather than as a standalone plugin update. For a single-tool skill tied to niwa CLI shapes, this is acceptable -- a real skill bug usually points at a niwa bug anyway. For the rare typo-in-markdown case, the user waits for the next niwa release.
- Local plugin development. Iterating on the skill requires rebuilding niwa to pick up the embedded file changes. Mitigated by an `--external-plugin-dir` developer flag (or by `go run` from the niwa source tree during development), but that wiring is incremental work not blocking this decision.
- Cleanup. If a user uninstalls niwa, the plugin tree at `~/.claude/plugins/marketplaces/<niwa-plugin-name>/` is left behind. `niwa` should optionally clean it up on a future `niwa uninstall` command; until then it's an orphaned but harmless directory.

Confidence: **high**. The constraints push hard toward embed, the payload is small enough that the only counterargument (binary growth) is negligible, and the trade-offs are concrete and uncontested.
<!-- decision:end -->
