# Lead: What is the latency cost of dynamic completion on every tab press?

## Findings

### Code paths a completion function would traverse

- `config.LoadGlobalConfig` at `internal/config/registry.go:82-110` reads
  `$XDG_CONFIG_HOME/niwa/config.toml` (or `~/.config/niwa/config.toml`) with
  `os.ReadFile` + `toml.Unmarshal` into a `GlobalConfig` struct. One syscall,
  one TOML parse. No filesystem walks beyond the single file.
- `workspace.EnumerateInstances` at `internal/workspace/state.go:131-149` does a
  single `os.ReadDir(workspaceRoot)` plus an `os.Stat` on each entry's
  `.niwa/instance.json`. O(entries in workspace root).
- `workspace.findRepoDir` at `internal/workspace/status.go:88-108` does
  `os.Stat(instanceRoot/repoName)` and, on miss, `os.ReadDir(instanceRoot)`
  followed by `os.Stat` on each group-dir child. To *list* all repos for
  completion (rather than check one name), the equivalent walk is one
  `ReadDir` on the instance + one `ReadDir` per potential group dir. O(repos
  + groups in a single instance).
- Cold start: `rootCmd` pulls in every subcommand's package `init()` via
  blank imports, so cobra must build the full command tree before dispatching
  to `__complete`. Unavoidable unless a separate completion binary or
  lightweight path is introduced.

### Raw binary baselines (warm filesystem cache, empty HOME)

All measurements via subprocess wall time, 20-30 iterations each, Linux
6.17, SSD, no cache drop available (no sudo).

| Invocation                    | min | median | avg | p95 | max |
|-------------------------------|-----|--------|-----|-----|-----|
| `niwa --help`                 | 2ms | 2ms    | 2ms | 2ms | 2ms |
| `niwa version`                | 2ms | 2ms    | 2ms | 2ms | 2ms |
| `niwa __complete ''`          | 2ms | 2ms    | 2ms | 2ms | 3ms |

Cobra init + Go runtime start is the floor: ~2ms on this machine.

### Synthesized workloads

Synthesizer: `/tmp/synth-workload.sh` writes a fake `$HOME` with N registered
workspaces, M instances each, R repos per instance (half flat, half nested
under `group/repo`). Completion work is simulated by
`cmd/niwa-bench/main.go` which invokes `config.LoadGlobalConfig` and then
`workspace.EnumerateInstances`, with variants for full vs scoped walks.

Scenarios at 30 iterations each:

| Scenario (N ws × M inst × R repos)          | workspaces | instances (all)   | repos (all, full walk) |
|---------------------------------------------|------------|-------------------|------------------------|
| empty registry                              | 2ms        | -                 | -                      |
| 10 × 5 × 5     (50 inst, 250 repo dirs)     | 2ms        | 2ms median        | 4ms median             |
| 50 × 20 × 10   (1000 inst, 10k repo dirs)   | 2ms        | 5ms median        | 56ms median, 59 p95    |
| 100 × 50 × 20  (5000 inst, 100k repo dirs)  | ~5ms       | ~20ms             | ~450ms                 |
| 200 × 100 × 20 (20000 inst, 400k repo dirs) | 3ms        | 71ms median       | **1738ms median**      |

500 × 200 × 50 (5M repo dirs) aborted — the filesystem synthesis itself
exhausted time; the measurement would have been multi-second.

### Scoped vs full walks

The key realization: a properly-written completion function only walks what
the user has already narrowed. For `niwa go <ws> <TAB>` we only need
EnumerateInstances on ONE workspace root; for `niwa go <ws> <inst> <TAB>` we
only need one ReadDir on that instance.

At the 100×50×20 workload (5000 instances, 100k repo dirs), scoped vs full:

| Mode              | Wall time        |
|-------------------|-------------------|
| workspaces only   | <5ms              |
| scoped-instances  | <5ms              |
| scoped-repos      | <5ms              |
| all-instances     | ~20ms             |
| all-repos         | ~450ms            |

The only path that crosses the 100ms bar is "enumerate every repo under
every instance under every workspace." That's not a path any sensible
completion handler needs to hit.

### Registry parse is cheap

A 500-workspace `config.toml` (68 KiB, 2003 lines) parses in ~5ms including
Go startup. The TOML parse itself is sub-millisecond on top of the ~2ms
baseline.

## Implications

- **No caching layer is needed for the common case.** Cold start + config
  parse + scoped enumeration lands well under the 100ms perceptibility bar
  for any realistic workload (hundreds of workspaces, dozens of instances
  each). A raw call per tab press is fine.

- **The cliff is at "list every repo everywhere."** Cross-workspace,
  cross-instance repo enumeration crosses 100ms around 10k repo dirs and
  crosses 1s around 100k. Completion UX should never do this: it can always
  scope to the positional argument already typed. Commands like `niwa go`
  that accept a bare identifier should require the user to either (a) be
  inside a workspace (discover from cwd) or (b) complete through
  `workspace -> instance -> repo` positional stages rather than offering a
  flat global list.

- **No need for a dedicated lightweight code path either.** `rootCmd` init
  is ~2ms even with the full subcommand tree; cobra's `__complete` machinery
  is not measurable. The Go binary cold start dominates and it's already
  tiny.

- **Filesystem-cache cold case was not measurable** (no sudo to drop
  caches). Worst case on an HDD or cold NFS could be 10-100x slower per
  ReadDir. On a laptop SSD with fs cache warm (the realistic case for
  interactive shells), nothing got close to the perceptibility bar outside
  the pathological all-repos walk.

- **If caching were ever added**, the obvious design is: in-memory cache
  within a single `niwa __complete` invocation is worthless (each tab press
  is a new process). On-disk cache keyed by `stat` mtime of
  `~/.config/niwa/config.toml` and of each workspace root would skip
  `ReadDir`s. But given scoped calls are already <5ms even at 100k repos,
  caching is a solution looking for a problem until some benchmark
  disproves this.

## Surprises

- The Go binary cold-start cost is only 2ms, not the 20-50ms I expected
  from seeing other Go CLIs feel sluggish at tab completion. Either the
  niwa binary is smaller than typical or modern Linux process spawn + Go
  runtime have gotten faster than I remembered. (Binary size: 10 MB.)

- TOML parse of a 68KB / 2000-line config is invisible in the numbers. The
  BurntSushi TOML parser is fast enough that even unrealistic registry
  sizes don't matter.

- `EnumerateInstances` on a 100-instance workspace root takes <1ms. The
  per-entry Stat for `.niwa/instance.json` does not meaningfully slow it
  down. It's the fanout (iterating across hundreds of workspace roots in a
  single completion call) that accumulates, and fanout isn't required.

## Open Questions

- **Cold-filesystem performance.** Couldn't drop caches without sudo. A
  shell that just launched or a laptop woken from sleep may hit cold
  pagecache and see 5-20x worse ReadDir times. Worth measuring on a
  slower disk or with `posix_fadvise`-style cache invalidation before
  declaring victory.

- **WSL / macOS behavior.** Linux ext4 ReadDir is fast. WSL2 virtiofs and
  macOS APFS over a rosetta'd binary may behave differently. The lead
  should be re-measured on those platforms before shipping.

- **What `niwa go` completion actually looks like.** The measured code
  paths match what completion *would* do, but the exact positional-arg
  structure of `niwa go <workspace> <instance> <repo>` (vs a flat
  `<anything>`) determines whether scoped walks are even possible. If the
  design collapses all three into one positional, the all-repos cost
  becomes real.

- **Antivirus / security scanners.** On managed corporate Windows/macOS,
  process-start hooks can add 50-200ms per exec. The 2ms Go cold-start
  measurement is from an untampered Linux laptop. A realistic enterprise
  worst case could be dominated by AV, not by niwa.

## Summary

Dynamic completion latency is not a problem at any realistic scale: Go
cold start + cobra init is ~2ms, a 500-workspace TOML registry parses in
~3ms, and a scoped single-workspace or single-instance filesystem walk
completes in under 5ms even at 100k repo dirs. The only measured code
path that crosses the 100ms bar is enumerating every repo under every
instance across every workspace (~450ms at 100k repos, ~1.7s at 400k),
which a well-designed completion handler never needs to do because each
tab press has already narrowed by a positional arg. The biggest remaining
unknown is whether cold-pagecache, WSL/macOS filesystems, or enterprise
AV process hooks invalidate these numbers on platforms we haven't
measured.
