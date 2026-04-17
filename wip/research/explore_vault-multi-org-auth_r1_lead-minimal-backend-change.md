# Lead 5: Minimal Backend Change for `--token` Support in subprocess.go

## Executive Summary

The minimal insertion point for multi-org token support is in **subprocess.go:runInfisicalExport()**, specifically lines 123-129 where the args slice is built. The token would be passed as a new field on Provider (set during Factory.Open from ProviderConfig), then forwarded as an optional parameter to runInfisicalExport. No changes to the commander test hook are needed. Estimated scope: ~15-25 lines of code across two functions.

---

## Current State Analysis

### infisical.go: Factory.Open() - Lines 89-146

**Current behavior:**
- Reads `project`, `env`, `path` (and optionally `name`, `_commander`) from ProviderConfig
- Stores them as fields on the Provider struct
- ProviderConfig is `map[string]any` (pkg vault/provider.go:143), so any new field just needs type assertion

**Existing pattern** (env, path handling at lines 109-127):
```go
if raw, ok := config["env"]; ok {
    s, ok := raw.(string)
    if !ok {
        return nil, fmt.Errorf("infisical: config[env] must be string, got %T", raw)
    }
    if s != "" {
        p.env = s
    }
}
```

**Test hook** already exists at lines 138-144 (`_commander`), so tests can inject stubs without modification.

### subprocess.go: runInfisicalExport() - Lines 119-130

**Current implementation:**
```go
func runInfisicalExport(ctx context.Context, c commander, project, env, path string) 
    (map[string]string, vault.VersionToken, error) {
    args := []string{
        "export",
        "--projectId", project,
        "--env", env,
        "--path", path,
        "--format", "json",
    }
```

**Key observations:**
1. Args are built as a slice literal (lines 123-129)
2. Insertion point for `--token` is between line 129 and the c.Run() call
3. Flag ordering: `--token` can go anywhere in the flags (Infisical CLI uses flag parsing, not positional args)
4. Preferred position: after `--path` and before `--format` (maintains logical grouping: project/env/path metadata, then token, then output format)

### Provider struct - Lines 152-169

**Fields currently stored:**
- name, project, env, path
- commander (test hook)
- mu, loaded, closed, values, versionToken (internal state)

**Pattern:** All config parameters are stored as fields on Provider.

---

## Proposed Minimal Change

### Option A: Direct token field (preferred)

**3 files affected, ~20 lines total.**

#### 1. infisical.go: Provider struct (line ~157, add 1 line)
```go
type Provider struct {
    name    string
    project string
    env     string
    path    string
    token   string  // NEW: machine-identity token, optional
    
    commander commander
    // ... rest unchanged
}
```

#### 2. infisical.go: Factory.Open() (after path handling, ~20 lines added)
```go
// Existing pattern, add after path handling block (line ~127):
if raw, ok := config["token"]; ok {
    s, ok := raw.(string)
    if !ok {
        return nil, fmt.Errorf("infisical: config[token] must be string, got %T", raw)
    }
    if s != "" {
        p.token = s
    }
}
```

#### 3. infisical.go: ensureLoaded() (line 315, pass token)
Change:
```go
values, token, err := runInfisicalExport(ctx, p.commander, p.project, p.env, p.path)
```
To:
```go
values, token, err := runInfisicalExport(ctx, p.commander, p.project, p.env, p.path, p.token)
```

#### 4. subprocess.go: runInfisicalExport() signature (line 119)
Change:
```go
func runInfisicalExport(ctx context.Context, c commander, project, env, path string) ...
```
To:
```go
func runInfisicalExport(ctx context.Context, c commander, project, env, path, token string) ...
```

#### 5. subprocess.go: args building (lines 123-129)
Replace:
```go
args := []string{
    "export",
    "--projectId", project,
    "--env", env,
    "--path", path,
    "--format", "json",
}
```
With:
```go
args := []string{
    "export",
    "--projectId", project,
    "--env", env,
    "--path", path,
}
if token != "" {
    args = append(args, "--token", token)
}
args = append(args, "--format", "json")
```

This preserves the fallback behavior: when token is empty, `--token` is not passed and the CLI uses the session (inherited from parent env). When token is present, it is prepended to format, which is the correct Infisical CLI flag ordering.

---

## Why This Is Minimal

1. **No new interfaces:** Uses the same pattern as `env` and `path` (simple field + config extraction)
2. **No command hook changes:** Tests already inject via `_commander`; tests can pass `token: "test-token"` in ProviderConfig and verify the args via fakeCommander.capturedArgs
3. **No signature proliferation:** Token is optional (empty string = omit flag); zero breaking changes to the lazy-load flow
4. **Backward compatible:** Existing configs (without "token") work unchanged; token defaults to "" (empty), args does not include `--token` flag
5. **Fallback-friendly:** When token is "", the subprocess inherits INFISICAL_TOKEN from parent env (current behavior); when token is set, it takes precedence on the command line

---

## Infisical CLI Compatibility

From `infisical export --help`:
```
--token string   Fetch secrets using service token or machine identity access token
```

- Flag is supported in current Infisical CLI
- No version gate needed; it's a straightforward flag
- `--token` can appear anywhere in the flag sequence (not positional)
- Preferred position (after --path, before --format) maintains semantic grouping

---

## Test Coverage Implications

No changes to the test hook contract. Tests already inject via `_commander` field.

**Test pattern for new token branch:**
```go
func TestResolveWithToken(t *testing.T) {
    cmd := &fakeCommander{stdout: jsonBody(map[string]string{"K": "v"})}
    cfg := vault.ProviderConfig{
        "project": "proj-1",
        "token":   "eyJ0eXA...",  // Machine-identity token
        "_commander": commander(cmd),
    }
    p := openWithCommander(t, cfg, cmd)
    // ... resolve ...
    // Assert cmd.capturedArgs contains "--token" and the token value
}
```

The fakeCommander already captures args (infisical_test.go line 47), so verifying `--token` presence in args is trivial.

---

## Estimated Scope

- **Files modified:** 2 (infisical.go, subprocess.go)
- **Lines added:** ~20-25 (4-5 in struct, 8-10 in Open, 2-3 in ensureLoaded, 6-8 in runInfisicalExport signature+args building)
- **Lines removed:** 0
- **Backward compatibility:** 100% (token field optional, defaults to "")
- **Risk:** Very low (follows existing patterns; no interface changes; existing tests pass unchanged)

---

## Decision: Insertion Points

| Location | Change | Lines |
|----------|--------|-------|
| Provider.token field | Add string field | 1 |
| Factory.Open (token parsing) | Add config extraction block | ~8 |
| ensureLoaded call site | Pass p.token to runInfisicalExport | 1 (edit existing) |
| runInfisicalExport signature | Add token param | 1 (edit existing) |
| runInfisicalExport args building | Conditionally append --token | ~6-8 |
| **Total** | | ~17-23 |

---

## Next Steps

1. Implement the 5-point change above
2. Write test in infisical_test.go asserting --token is in capturedArgs when token is provided
3. Write test asserting --token is NOT in capturedArgs when token is empty (fallback behavior)
4. Verify cmd.Env = nil still holds (it does; only the args change)
5. Verify ScrubStderr does not leak the token (it won't; token is not in stderr, only in args which are not logged)

---

## Open Questions

1. **Where does the token come from at runtime?** The lead assumes a caller (e.g., a higher-level orchestrator) populates ProviderConfig["token"] at factory.Open time. This is TBD by the multi-org design.
2. **How is the token obtained?** Is it from a side channel (environment variable, file, API)? Out of scope for this backend change.
3. **Token format?** Assumed to be Infisical's machine-identity JWT format; no validation performed in the backend (CLI does that).
