# Decision Report: Settings Env Resolution

## Question

How should `[claude.env]` resolve vars from inline declarations, promoted env keys, and per-repo overrides?

## Options Evaluated

### Option A: Promote list with inline override

`[claude.env]` has a `promote` list (keys to pull from resolved env) and inline key-value pairs. Inline wins over promoted for the same key. Per-repo overrides extend promote lists and override inline values.

```toml
[claude.env]
promote = ["GH_TOKEN"]
EXTRA = "settings-only"

[repos.special.claude.env]
promote = ["API_KEY"]
GH_TOKEN = "repo-specific-override"
```

Resolution order for a given repo:
1. Start with workspace `[claude.env]` inline vars
2. Overlay promoted vars from fully resolved env (env pipeline output)
3. Inline wins if same key appears in both inline and promote
4. Apply repo `[repos.X.claude.env]` inline vars (override per key)
5. Apply repo promote list (union with workspace promote, pull from repo's resolved env)
6. Again, inline wins over promote at repo level

Error: promoted key not found in resolved env -> hard error at materialize time.

**Pros:** Explicit about what goes where. Promote list is scannable. Inline override gives escape hatch.
**Cons:** "Inline wins over promote" at the same level is a subtle rule. Two sources of truth for the same key.

### Option B: Promote-only (no inline vars in claude.env)

Remove inline key-value support from `[claude.env]`. It becomes a pure promote mechanism -- a list of keys to pull from the resolved env into settings.

```toml
[claude.env]
promote = ["GH_TOKEN", "API_KEY"]

[repos.special.claude.env]
promote = ["EXTRA_TOKEN"]
```

Settings-only vars that shouldn't be in `.local.env` would go in `[claude.settings]` or a new section.

**Pros:** No ambiguity about resolution -- there's only one source. Simple mental model.
**Cons:** Can't inject settings-only env vars (vars that should be in settings.local.json but NOT in .local.env). This is a real use case -- e.g., a Claude Code-specific flag that makes no sense in the shell environment.

### Option C: Promote list with fallback to inline

Same as Option A but with different precedence: promoted values win over inline at the same level. Inline vars act as defaults/fallbacks for when a key isn't in the env pipeline.

```toml
[claude.env]
promote = ["GH_TOKEN"]
GH_TOKEN = "fallback-if-not-in-env"
EXTRA = "settings-only-default"
```

**Pros:** Promote is the "intended" path, inline is the fallback. Cleaner mental model for "I want env vars in settings."
**Cons:** Counter-intuitive -- more-specific (inline) loses to less-specific (promote from env file). If someone writes `GH_TOKEN = "override"` inline, they'd expect it to win.

## Recommendation

**Option A: Promote list with inline override.**

The key insight is that inline declarations are more specific than promote references, so they should win. This matches how `[env].vars` already wins over `[env].files` -- inline is always the override, files/promote are the base. The rule is consistent across the codebase.

Settings-only vars (Option B's weakness) are a real need. And the "inline wins" rule (Option C's weakness addressed by A) matches user expectations from every other config system.

The resolution order can be stated simply: "env pipeline resolves first, promoted keys are pulled in, then inline vars override."

## Confidence

High. The resolution semantics mirror existing patterns in the codebase.

## Assumptions

- Settings-only vars (not in .local.env) are a real use case
- Users expect inline declarations to override file/promoted sources
- Hard errors for missing promoted keys are preferable to silent omissions
