# Security Review: niwa-watch-once-pr-review

Reviewer stance: adversarial. The feature's entire value proposition is a
deterministic containment boundary around a Claude Code agent that reads a
potentially-hostile PR. The bar is therefore: assume the agent is fully
compromised by prompt injection in the PR body/diff on turn one, and ask what
it can still do. Anything that survives that assumption is the real boundary.

## Dimension Analysis

### External Artifact Handling
**Applies:** Yes -- this is the primary attack surface.

The design correctly splits the artifact into two paths: (a) the PR
title/body/diff never enter the launch prompt (metadata-only prompt, Decision
`prompt`), and (b) the PR head is fetched by trusted niwa code, not by the
agent (Decision 2A). Both are the right shape. The injection-proof prompt
closes the "dispatch decision" surface cleanly: even a PR titled
`ignore previous instructions and ...` cannot alter what session gets
launched, because only `owner/repo`, number, and URL are interpolated by a
pure function. Good.

The residual risk is entirely in half (b): **the trusted `git fetch` of
untrusted repo content.** The design's Negative/Mitigations sections
acknowledge the fetch "must fetch without running repository-supplied hooks"
but the body does not specify *how*, and this is the one place trusted niwa
code touches attacker-controlled bytes. Concrete execution vectors on a fetch
into a clone:

1. **Client-side git hooks in the clone.** `git fetch` itself does not run
   `.git/hooks/*` from the remote, but the *provisioning* step
   (`applier.Create`) clones/creates the instance. If provisioning ever runs
   `post-checkout`, `post-merge`, or `post-rewrite` hooks, and if any later
   step checks out the fetched ref, an attacker-supplied hook could run. The
   attacker does not control `.git/hooks` via PR content (hooks are not
   transferred), so this is LOW *unless* the recipe/clone copies hooks from
   the fetched tree. Must be verified, not assumed.

2. **`.gitattributes` + smudge/clean filters and `filter.*.process`.** This
   is the sharp one. If niwa ever does a *checkout* of the PR head (as opposed
   to leaving it as a fetched ref that the agent reads via `git show`), a
   `.gitattributes` in the PR tree can declare a filter driver. Filter
   drivers are read from git *config*, not from the tree, so a PR cannot
   define a new filter -- but it CAN route paths through a filter name that
   already exists in the developer's global/system git config
   (`~/.gitconfig`), and can trigger `text`/`eol` normalization. Severity:
   MEDIUM if any checkout happens, because the developer's ambient git config
   is outside niwa's control. Mitigation: fetch only, never check out into a
   working tree that honors `.gitattributes`; or checkout with
   `-c filter.<all>.process=` disabled.

3. **Git LFS smudge filter.** `git-lfs` installs a global smudge filter
   (`filter.lfs.smudge = git-lfs smudge`). A PR whose `.gitattributes` marks
   a path as `filter=lfs` will, on checkout, invoke `git-lfs smudge`, which
   makes a *network call* to the LFS endpoint declared in `.lfsconfig` /
   `lfs.url` -- and this runs as *trusted niwa code outside the sandbox*,
   before the empty-allowlist boundary exists. Severity: HIGH if checkout
   happens on a machine with git-lfs installed, because it is both code
   execution (the smudge process) and unsandboxed egress to an
   attacker-named URL. Mitigation: `git -c filter.lfs.smudge=cat -c
   filter.lfs.process= -c lfs.fetchexclude=* ...`, or set
   `GIT_LFS_SKIP_SMUDGE=1` for the fetch/checkout, or simply never check out.

4. **Submodules.** `git fetch` does not recurse submodules by default, but
   `git clone --recurse-submodules` or a later `git submodule update` would
   fetch attacker-named submodule URLs (`.gitmodules`), again as unsandboxed
   trusted code, and historically has been an RCE vector
   (`core.fsmonitor`, submodule path traversal CVEs). Severity: MEDIUM.
   Mitigation: never recurse submodules on the untrusted fetch; the agent
   reads the top-level tree only.

5. **`.git/config` injection via fetch.** Not applicable to a plain ref
   fetch (fetch does not import remote config), LOW.

6. **`core.fsmonitor` / `core.hooksPath` from a fetched-then-trusted config.**
   Only reachable if niwa runs git commands *inside* the clone after checkout
   with the repo's own config active AND the PR could write `.git/config`
   (it cannot via content). LOW, but note that any `git` invocation niwa
   makes with CWD inside the clone reads that clone's `.git/config`; keep
   niwa's post-fetch git operations minimal and prefer
   `git -c protocol.ext.allow=never -c core.fsmonitor=false`.

**Hardening recommendation for the fetch (should be written into the design,
not left as a one-line Negative):**

- Fetch the single head ref only; do **not** clone-with-recurse and do **not**
  check out into a filter-honoring working tree. Prefer leaving the content as
  a fetched object the agent reads with `git show`/`git cat-file`, OR check
  out with all filters and hooks neutralized.
- Run the fetch with an explicit hardened invocation, e.g.:
  `GIT_LFS_SKIP_SMUDGE=1 git -c core.hooksPath=/dev/null -c
  filter.lfs.process= -c filter.lfs.smudge=cat -c
  protocol.ext.allow=never -c protocol.file.allow=never -c
  submodule.recurse=false -c uploadpack.allowFilter=false fetch --no-recurse-submodules
  --no-tags <remote> <sha>`.
- Fetch by **commit SHA** (which the trusted search/API path already knows),
  not by an attacker-influenced ref name, to avoid ref-name confusion.
- Cap fetch size / depth (`--depth=1`) and set a timeout, so a hostile PR
  cannot wedge the trusted pre-fetch (DoS). LOW-MEDIUM but cheap.

Severity summary for this dimension: the injection-proofing of the prompt is
**solid**. The trusted pre-fetch is the **unpatched hole** -- as written it is
HIGH-if-checkout (LFS smudge egress + code exec, outside the sandbox), and the
design must pin down "fetch as data, filters/hooks/submodules off" concretely.

### Permission Scope
**Applies:** Yes.

**Does an empty `allowedDomains` actually block tool egress while the harness
still reaches the model?** Conceptually yes, and the design's Note
(lines 124-126) correctly identifies that the harness model-API channel is
architecturally distinct from the agent's tool traffic. That distinction is
what makes an empty allowlist viable at all. But two things must be true and
neither is proven in the doc:

1. **The sandbox proxy must actually intercept *all* egress from
   agent-spawned processes, not just the harness's own Bash tool.** The threat
   model is a compromised agent that runs `curl`, `python -c 'import
   urllib...'`, `/usr/bin/wget`, a statically-linked dropper it writes to its
   clone and `chmod +x`es, or a language runtime already on PATH. If the
   sandbox is an in-harness allowlist on the Bash tool (string-matching the
   command), it is trivially bypassed (`ba''sh`, `$(printf ...)`,
   write-then-exec, exec via a different tool). The boundary only holds if
   `sandbox.enabled` is an **OS-level** egress deny (network namespace /
   packet filter / seccomp+proxy) applied to the whole process tree. The
   design *asserts* "OS sandbox" and "hold at the tool/OS layer" (lines 60-61,
   Decision Driver 1) -- good intent -- but the design should state the
   enforcement mechanism per platform (macOS Seatbelt? Linux
   namespaces/nftables? bubblewrap?) so a reviewer can confirm subprocesses
   and alternate binaries are covered. As written this is a **stated
   requirement without a named mechanism**: MEDIUM until the mechanism is
   pinned, because "sandbox" that is actually a tool-level filter would be
   defeated by the exact write-then-run / alternate-binary vectors the design
   claims to defend.

2. **Domain-fronting / SNI / TLS-termination caveat.** The prompt flags that
   the sandbox proxy "may not TLS-terminate by default." With an *empty*
   allowlist this is largely moot for the agent's tools -- nothing is allowed,
   so there is no allowed host to front behind. The residual concern is the
   **model-API channel the harness keeps open**: if the harness's egress to
   `api.anthropic.com` traverses the same proxy and the proxy allows that host
   by SNI without TLS-terminating, a compromised agent that can get its bytes
   onto the harness's model connection (e.g. by influencing request bodies, or
   by pointing its own traffic at the allowed SNI while sending an
   attacker Host header) could exfiltrate via domain-fronting to a CDN that
   also serves the API. This is a real but narrow channel. The design should
   record: (a) the model channel is allowlisted by the harness itself and is
   not a general-purpose hole the agent's shell can address, and (b) if the
   sandbox proxy is SNI-allowlist-only (no TLS termination), note the
   domain-fronting residual explicitly as an accepted caveat. Severity:
   LOW-MEDIUM, mostly informational, but it must be *recorded* since the
   prompt raises it and an empty allowlist does not by itself defeat
   SNI-fronting on whatever host remains reachable.

3. **Windows.** Handled correctly: Decision 7A preflights `GOOS ==
   "windows"` and fails closed. Good. One nit: the preflight must also fail
   closed on **any platform where the OS sandbox binary/capability is absent
   at runtime** (e.g. Linux without the required namespace/seccomp support,
   bubblewrap not installed, running inside a container that forbids nested
   namespaces). "Not Windows" is necessary but not sufficient; the preflight
   should be a positive capability probe ("can I actually create the egress
   deny right now?"), not just an OS check. Otherwise there is a gap:
   non-Windows platform, sandbox silently unavailable, session launches
   uncontained. Severity: MEDIUM -- this is the exact fail-open the design
   says it forbids (Driver "Fail closed", Decision 7B rejection).

**Escape via subprocesses / alternate binaries / write-then-run:** covered by
point 1 -- only safe if OS-level. **Filesystem scope:** the design says writes
are "scoped to its clone." If that scoping is also OS-level (mount/overlay,
sandbox fs allowlist) it prevents the agent writing a dropper to a PATH
directory or tampering with the *other* instances / the handled-set / another
session's draft. If fs scoping is advisory only, a compromised agent could
write outside its clone -- e.g. poison a sibling instance's
`.claude/settings.json` before that instance launches, or overwrite another
staged draft so the trusted `post` step posts attacker text. This
cross-session write vector deserves an explicit note. Severity: MEDIUM if fs
scoping is not OS-enforced.

### Supply Chain or Dependency Trust
**Applies:** Partially -- reframed as "credential scoping + the trusted-post
supply chain," which is where the interesting trust question lives.

**Credential allowlist (Decision 3A).** Allowlist over denylist is the correct
call and the reasoning (denylist fails open) is sound. Concrete checks:

- The allowlist must be **exact-match, not prefix-match.** A prefix rule like
  "keep `ANTHROPIC*`" is fine, but a sloppy "keep anything matching `TOKEN`?
  no" -- the risk is the *inverse*: make sure the allowlist does not
  accidentally admit `GITHUB_TOKEN`, `GH_TOKEN`, `GH_ENTERPRISE_TOKEN`,
  `AWS_*`, `ANTHROPIC_API_KEY` *plus* any org SSO cookie. The design names
  "Claude/Anthropic auth + PATH/HOME/locale." Enumerate exactly which
  Anthropic vars (`ANTHROPIC_API_KEY` or the OAuth creds file path?) and
  confirm nothing GitHub-shaped is on the list. The Phase 3 test ("subset +
  canary absence") is the right test; the canary set must include
  `GITHUB_TOKEN`, `GH_TOKEN`, `NIWA_GITHUB_API_URL`(-adjacent), SSH agent
  socket (`SSH_AUTH_SOCK`), and any git-credential-helper env.

- **`SSH_AUTH_SOCK` / gitconfig credential helpers.** Even with the GitHub
  token excluded from env, if `SSH_AUTH_SOCK` is inherited the agent can push
  over SSH using the developer's forwarded agent -- a full write credential
  that bypasses the "token absent" guarantee. It MUST be on the deny/canary
  list. Likewise `HOME` is on the *allow* list, which means
  `~/.gitconfig`, `~/.config/gh/hosts.yml`, `~/.netrc`,
  `~/.ssh/`, and `~/.config/git/credentials` are all readable by the agent.
  **This is a hole:** the design excludes the token from *env* but hands the
  agent `HOME`, and the real developer GitHub credential very commonly lives
  in `~/.config/gh/hosts.yml` or `~/.netrc` on disk. Excluding the env var is
  not enough if `HOME` points at a directory containing the same credential.
  Severity: **HIGH** -- this defeats the stated "GitHub token not reachable
  from the session" guarantee via the filesystem rather than the environment.
  Mitigation: either give the contained session a *scrubbed / synthetic
  HOME* (empty dir with only what the model-auth needs), or ensure the OS
  sandbox fs-denies `~/.ssh`, `~/.netrc`, `~/.config/gh`, `~/.config/git`,
  `~/.gitconfig`. The env allowlist and the fs allowlist must be designed
  together; as written they disagree.

- **Can the model auth itself be abused?** The session holds Anthropic model
  credentials by necessity. A compromised agent can spend tokens (cost DoS)
  and can use the model channel as a covert exfil path (point 2 above). It
  cannot use Anthropic creds to reach GitHub. Accept cost-DoS as a known,
  bounded residual; note it.

**Trusted-post supply chain (Decision 6A) -- the draft is attacker-influenced
data flowing into a trusted action.** This is the subtle one and the design
under-treats it. The drafting agent is untrusted (it read the hostile PR).
It authors `watch-review-draft.md`. Then `niwa watch post` -- running with the
GitHub token, outside the sandbox -- reads that file and POSTs it to the PR
reviews API. So **untrusted-authored bytes are posted under the developer's
identity with the developer's token.** The containment on the *drafting* side
is airtight, but the *post* side re-introduces the attacker's content into a
trusted, credentialed operation. Risks:

- **Content injection into the review.** The draft could contain
  `@mentions` mass-pinging, `Fixes #<n>` / `Closes #<n>` autolinks that close
  unrelated issues when the review comment lands, malicious links, or social
  engineering ("LGTM, merging") that the developer's identity now vouches for.
  Because a human approves before `post`, this is MEDIUM not HIGH -- but the
  approval gesture is "one gesture" by design (Decision 6 rejects the
  copy-paste specifically to make it frictionless), which means the human is
  *not* expected to read every byte. The design should state that `post`
  treats the draft as **data, not commands**: it must POST the draft solely as
  the review *body* string via the JSON API (never construct a shell `gh`
  command from it -- Decision 6B's rejection already avoids the shell path,
  good), and should consider stripping/escaping `Closes/Fixes` autolink
  keywords or at least surfacing them at approval time. Also: the POST must
  set `event: COMMENT` (or an explicit approve/request-changes the developer
  chose) -- the draft must **not** be able to dictate `event: APPROVE`. If the
  draft or record carries the review *decision*, a hostile PR could steer its
  own auto-approval. **Pin `event` in trusted code, never from the draft.**
  Severity: MEDIUM, and currently unaddressed.

- **Handle/record integrity.** `post <handle>` resolves the handle to
  `{owner,repo,number,draftPath}` from the staged-review record niwa persisted
  *at dispatch* (trusted). Good -- the PR coordinates are not taken from the
  agent. But `draftPath` points into the instance the agent could write to.
  Confirm `post` reads the *fixed* known path recorded by trusted niwa, and
  that the agent cannot redirect `draftPath` (it must be niwa-authored in the
  record, never agent-authored). If the agent can influence the record or
  symlink the draft path to `/etc/...` or to another PR's draft, `post` could
  read the wrong file. Recommend: `post` reads a path niwa fixed at dispatch,
  resolves symlinks and asserts the resolved path is inside the instance root
  before reading. Severity: MEDIUM.

### Data Exposure
**Applies:** Yes, low-to-moderate.

- **What the session accesses/transmits.** With the fixes above (scrubbed
  HOME, OS egress deny), the session sees only its own clone (the one PR's
  content) and the model channel. It transmits only to the model. Without the
  HOME fix it also sees the developer's on-disk credentials and git identity
  -- see the HIGH above. The PR content itself is already visible to the
  developer (they're the requested reviewer), so exposing *that* to the model
  is within the user's intent; note it as accepted (PR diff/body goes to the
  model API as review input -- expected).

- **Handled-set at rest** (`.niwa/watch-handled`, `owner/repo#number` lines):
  low sensitivity. It reveals which private-repo PRs the developer was asked
  to review -- mildly sensitive metadata (repo names, PR numbers) but no
  credentials or content. Stored under the workspace `.niwa/`. Ensure it is
  not committed to any repo (it lives in the workspace `.niwa/`, not a repo
  tree) and inherits normal file perms. LOW.

- **Staged-review record** (`{handle,owner,repo,number,url,draftPath}` +
  the draft file): contains the PR URL and the model-authored review of a
  possibly-private PR, at rest in the instance until `post`/`discard`. If
  instances are world-readable or shared, that's exposure. LOW-MEDIUM;
  recommend the record and draft live under the instance root with restrictive
  perms and are cleaned on `discard`/`post`. Note also that `discard` "records
  handled, posts nothing" -- confirm it also does not leave the draft lying
  around indefinitely (housekeeping, not security-critical).

- **PR URL/coords in the prompt:** platform-vouched identifiers only, no
  secret. Fine.

**Act boundary (containment never lifted for the drafting agent):** The
drafting agent genuinely never holds the posting credential and never has
egress (given the fixes). The separation of "draft under containment" from
"post under trust" is the right architecture and is *airtight on the drafting
side*. It is **not** airtight on the *post* side purely because the post
consumes attacker-influenced data (the draft) -- see the Decision 6 analysis.
The boundary correctly prevents the agent from *acting*, but the design must
add that the trusted actor treats the drafted artifact as inert data and pins
the review `event`.

**Fail-closed preflight gap:** As noted in Permission Scope point 3, the
preflight must be a positive runtime capability probe, not merely `GOOS !=
windows`. The other gap: preflight runs once before the loop, but each per-PR
step does provision -> fetch -> **merge sandbox profile into settings** ->
launch. If the settings-merge (the thing that actually turns the sandbox on
for *this* instance) fails or is a no-op for any instance, the design must
guarantee that instance does not launch. Decision 7 preflights the *platform*;
it does not obviously re-verify that the merged settings for *this specific
instance* actually contain `sandbox.enabled: true` and empty `allowedDomains`
before `claude --bg` fires. Recommend a per-instance post-merge assertion
("read back the merged settings; confirm sandbox stanza present; else abort
this PR, do not record handled") so a merge that silently loses the stanza
(key collision with an instance-local override, `MergeInstanceOverrides`
precedence) cannot launch an uncontained session. Severity: MEDIUM -- this is
the realistic fail-open in an otherwise fail-closed design, because the
containment lives in a *merged* document and merges can drop keys.

## Recommended Outcome

**OPTION 1 - Design changes needed.** The architecture is sound and the two
headline properties (injection-proof prompt, agent-holds-no-egress/no-token)
are the right boundaries. But three concrete holes must be closed in the
design before it is safe to build, plus the Security Considerations section
(currently a placeholder, lines 313-315) must be written. Specifically:

1. **Pin the trusted pre-fetch hardening (Decision 2 / Solution
   Architecture).** State that niwa fetches by commit SHA, as data, with
   hooks/LFS-smudge/submodule-recursion/ext-protocols all disabled, and never
   checks out into a filter-honoring working tree. As written, an LFS smudge
   filter on a PR `.gitattributes` is unsandboxed code-exec + egress in the
   trusted path. (HIGH-if-checkout)

2. **Reconcile the env allowlist with `HOME` (Decision 3 / Security
   Considerations).** Excluding `GITHUB_TOKEN` from env while inheriting the
   developer's `HOME` leaves the GitHub/SSH credentials reachable on disk
   (`~/.config/gh`, `~/.netrc`, `~/.ssh`, `SSH_AUTH_SOCK`). Give the contained
   session a scrubbed HOME and OS-fs-deny those paths, and add
   `SSH_AUTH_SOCK` + `GH_TOKEN`/`GH_*` to the canary-absence test. This is
   required for the design's own "token not reachable from the session"
   guarantee to hold. (HIGH)

3. **Name the sandbox enforcement mechanism and make preflight a positive
   runtime probe (Decisions 1 & 7).** State per-platform how egress is denied
   at the OS layer (so subprocesses/alternate-binaries/write-then-run are
   covered, not just the Bash tool), make the preflight verify the sandbox can
   actually be created *now* (not just `GOOS != windows`), and add a
   per-instance post-merge assertion that the merged
   `.claude/settings.json` really contains the sandbox stanza before launch.
   (MEDIUM, closes the fail-open path.)

4. **Harden the trusted post (Decision 6).** Pin the review `event` in trusted
   code (never let the draft dictate APPROVE), treat the draft strictly as an
   API body string (already avoided the shell path -- keep it that way),
   resolve/validate `draftPath` inside the instance root before reading, and
   surface `Closes/Fixes`/`@mention` autolinks at approval time. (MEDIUM.)

5. **Record the accepted residuals in Security Considerations:** model-channel
   is the one egress the harness keeps (cost-DoS + narrow domain-fronting
   caveat if the proxy is SNI-only/no-TLS-termination); PR content flows to
   the model API by design; handled-set/staged-record are low-sensitivity
   metadata at rest.

## Summary
The design's core boundary -- an injection-proof launch prompt plus an agent
that holds neither egress nor the GitHub token -- is the right architecture and
is genuinely airtight on the drafting side. But three real holes undercut the
"deterministic containment" claim as written: the trusted `git fetch` of
untrusted content can trigger unsandboxed code-exec/egress via LFS smudge or
filter/submodule drivers on checkout; excluding the token from the *env* while
inheriting the developer's *HOME* leaves the same GitHub/SSH credentials
reachable on disk; and the containment lives in a *merged* settings document
with only a platform-level (not per-instance, not positive-probe) fail-closed
check. All are fixable with design-level changes (fetch-as-data hardening,
scrubbed HOME + fs-deny, named OS-sandbox mechanism + post-merge assertion,
and a trusted-post that treats the draft as inert data with a pinned review
event). Recommend OPTION 1.
