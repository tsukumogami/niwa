# Design decisions: instance-dispatch

- D1 Command surface: top-level `niwa dispatch <prompt>` with `--label`, and
  pass-through `--model`/`--permission-mode`/`--agent`; returns immediately with
  management hints (does not attach). Alt rejected: `niwa run`/`niwa agent` (less
  discoverable next to create/reap), attach-by-default (blocks the terminal).
- D2 Instance naming: `--name <config>-disp-<8 random hex>` via the customName branch,
  sidestepping the TOCTOU numbered scan. Alt rejected: numbered scan (racy, no lock),
  timestamp (collision under concurrency).
- D3 Session-id capture: poll the jobs dir for the state.json whose cwd == the unique
  instance dir; read sessionId (full UUID). No stdout scraping. Requires a `Cwd` field on
  the job-state struct and EvalSymlinks+Clean path comparison. Bounded poll + timeout.
  Subsumes PRD R18 (scrape) under the stronger R19/R21 path. Alt rejected: scrape
  `backgrounded · <short-id>` (fragile undocumented format, needs stdout-capture seam).
- D4 Atomicity: command self-rollback (deferred, success-flagged destroy of the
  just-created instance on any pre-mapping failure) PLUS a marker+TTL reaper backstop for
  the SIGKILL/crash gap (a Go defer never runs on SIGKILL). The backstop reclaims an
  instance only when its mapping is ABSENT, a create-time pending-marker is present, and
  its age exceeds a TTL strictly longer than worst-case dispatch wall-clock (preserving
  R38). Alt rejected: self-rollback only (leaves SIGKILL orphan, fails strict R32);
  provisional mapping (impossible — store rejects non-UUID keys).
- D5 In-flight protection: no new lock. The reaper targets only instances WITH an
  ephemeral mapping; an unmapped in-flight instance is invisible. The D4 backstop's TTL
  gate keeps it from reaping a young in-flight instance.
- D6 Provenance: additive `Origin` field on SessionMapping (absent decodes to ""),
  values dispatch/hook; informational, does not change reaper eligibility (R41).
- D7 Reuse/structure: new internal/cli/dispatch.go; a capture-capable background launcher
  (package-var function for test injection) generalized from the sessionattach exec
  pattern; reuse realProvisionInstance (create), destroyInstanceFunc (rollback/teardown),
  WriteSessionMapping, reapWorkspace.
- D8 Prompt: single argv element (no shell), reject empty, clear error at ARG_MAX.
- D9 Test seams: injectable launcher func var, injectable jobs-dir root, destroyInstanceFunc
  var, injectable clock for the TTL, localGitServer harness for create.
