# work-on extension: niwa

niwa's verification map for shirabe's `/work-on` definition-of-done gate. Schema:
`skills/work-on/references/verification-map.md` in the shirabe repo. The default runs only when
no entry matches any changed file. The default copies the Linux steps of the Test job in
`.github/workflows/test.yml`, which point back here: change both together. Deliberate
differences, and checks PR CI does not run, are marked. Paths that carry behavior where no
command here checks what they do have their own entry at the end, which makes the gate
cannot-verify rather than letting the default pass them; this file is knowingly left to the
default, since halting every map edit would make the map painful to maintain. This file is
`@`-imported on every `/work-on` run, so it stays short; the reasons are in its commit history.

## Verification map

- `**/*.go`, `go.mod`, `go.sum`, `Makefile`, `cmd/**`, `internal/**`, `test/functional/**` ->
  every default command below
- `docs/**` -> all of:
  - `go test ./internal/agentplan/` (pins the generated gap list in `docs/guides/codex-agent.md`;
    the functional suite reads the same region)
  - `go test -count=1 ./internal/cli/` (its repository scan reads every `.md` outside
    `docs/prds` and `docs/designs`, so a guide can fail CI that the line above passes)
  - `B=$(git merge-base origin/main HEAD) && git diff --name-only --diff-filter=ACMR "$B" -- :/docs/ | grep -vE "(^|/)(evals|tests)/fixtures/" | xargs -r shirabe validate --visibility=public` (errors out when `origin/main` is missing: that is cannot-verify, not a failed change)
  - `shirabe validate --visibility=public --lifecycle . --mode=draft` (not `ready`: an in-flight `/execute` chain keeps its PLAN, which the ready posture rejects)
- `.tsuku-recipes/**` -> `tsuku validate .tsuku-recipes/niwa.toml` (not in PR CI: CI's recipe step installs niwa from main over the network, so it never reads the changed recipe; this checks structure only, so a download or checksum change passes it)
- `.github/**`, `install.sh`, `scripts/**`, `.goreleaser.yaml`, `test/live/**` -> no local check
  exists. Still run every other selected command; if one fails the outcome is failed, otherwise
  it is cannot-verify: no command here checks what these files do (`test/live/**` is behind the
  `live` build tag and needs a real `claude` session) and most are not read by PR CI either, so
  the run stops for a person to check them. (shirabe's schema has no form for such an entry yet:
  tsukumogami/shirabe#373.)

### Default verification command (when no map entry matches; all must pass)

The Linux steps of the Test job in `.github/workflows/test.yml`. Its credentialed steps (vault and
Claude integration), the tsuku network install and the macOS leg are left out on purpose.

- `go mod tidy -diff` (unlike CI: reports an untidy module without rewriting `go.mod` or `go.sum`)
- `F=$(git ls-files --cached --others --exclude-standard "*.go") && [ -n "$F" ] && out=$(printf "%s\n" "$F" | xargs gofmt -l) && [ -z "$out" ]`
  (unlike CI: checks only the repo's own Go files, tracked or new, because `gofmt -l .` also walks
  gitignored nested worktrees under `.claude/worktrees/`, where another session's unformatted file
  would fail this change; an empty list fails rather than passing over nothing)
- `go vet ./...`
- `go test -race ./...`
- `H=$(git rev-parse HEAD) && S=$(git status --porcelain) && NIWA_TEST_TAGS='~@codex-discovery && ~@codex-live && ~@claude-integration' make test-functional && [ "$(git rev-parse HEAD)" = "$H" ] && [ "$(git status --porcelain)" = "$S" ]`
  (CI's checkout tripwire around the functional suite; "another functional test run holds" the
  lock means a run is already going in this checkout: cannot-verify, not a failed change.
  Unlike CI, three tags are skipped, for two different reasons. `@codex-live` and
  `@claude-integration` stay out for good: with a Codex login or `ANTHROPIC_API_KEY` they make
  real, billed model calls (CI runs the Claude ones only in a separate step when its API-key
  secret is set, and never runs the Codex ones). `@codex-discovery` is out only because it fails
  wherever Codex is installed: remove it from the list when tsukumogami/niwa#304 is fixed. The
  hermetic `@codex-posture` still runs here, though CI's runners, having no Codex, skip it.)
- `go build -o niwa-test ./cmd/niwa && for shell in bash zsh; do output=$(./niwa-test shell-init "$shell") && [ -n "$output" ] && echo "$output" | grep -q "niwa()" && echo "$output" | grep -q "__complete" || exit 1; done && rm niwa-test`
