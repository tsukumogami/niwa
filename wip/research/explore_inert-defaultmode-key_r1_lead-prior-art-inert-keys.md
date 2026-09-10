# Lead: Prior art for an inert generated key that the generator still reads

Round 1. Web research plus one empirical test against the locally installed
Claude Code (v2.1.267).

## Findings

### 0. Ground truth on the Claude Code side (checked, not assumed)

The official settings docs now state the behaviour explicitly:

> **The file can't set that value.** `permissions.defaultMode` values `auto` and
> `bypassPermissions` don't take effect from project or local settings; set them in
> user or managed settings instead, or pass `--permission-mode` for one session.
> Before v2.1.257, `bypassPermissions` took effect from any file.

Source: https://code.claude.com/docs/en/settings (section "Troubleshoot a setting
that doesn't apply" → "A value you set is ignored").

Two more facts from the same page that bear on the options:

- Settings files are **strict JSON**: "a `//` comment or a trailing comma is a
  syntax error, and Claude Code reports the file as a Settings Error at the next
  start." So the header-comment option is off the table outright.
- Failure modes are graded: a **Settings Error** (invalid JSON, or "a value the
  schema rejects") can cause the whole file to be skipped; a **Settings Warning**
  ("a malformed permission rule or an unknown hook event name") drops only the
  offending entry. Unknown top-level keys are not named in either bucket.

**The published schema is the concrete constraint.** Fetched
https://json.schemastore.org/claude-code-settings.json (230 KB, 142 top-level
properties):

- root: `"additionalProperties": true` → **unknown top-level keys are legal.**
- `permissions`: `"additionalProperties": false` → **an unknown key *inside* the
  `permissions` object is a schema violation.**
- `"allowTrailingCommas": false`, no `allowComments` → confirms strict JSON.
- `defaultMode` enum is `["acceptEdits","bypassPermissions","default","delegate","dontAsk","plan","auto","manual"]`.

That asymmetry is the single most decision-relevant fact in this whole lead. A
sibling `"_comment"` next to `defaultMode` sits in the one sub-object that
forbids extra keys; a sibling at the root does not.

Caveat on how binding that schema is: the docs describe it as an editor aid
("gives you autocomplete and inline validation in VS Code, Cursor…"), and warn it
"can lag behind the newest CLI releases," so it is not necessarily the CLI's own
validator. I tested empirically. With a project `.claude/settings.json`
containing both `_niwa` at root and `_niwaComment` inside `permissions`, a
`claude -p` run completed normally and `claude doctor` reported nothing. But the
same run also completed silently with `"defaultMode": "totallyBogusMode"` — a
value the schema definitely rejects — and `--debug` emitted no settings
diagnostics either. So **`-p` surfaces no settings diagnostics at all in
v2.1.267, and my test cannot distinguish "tolerated" from "dropped with a
warning only an interactive session shows."** Treat "unknown keys inside
`permissions` are safe" as unproven. Unknown keys at root are safe per the
schema; unknown keys inside `permissions` are a schema violation that today
happens not to break a headless run.

### 1. The general pattern: how generators handle a key the consumer stopped honoring

Four recognized resolutions, in roughly descending order of how well they hold up:

**(a) Stop writing it; keep the intent in your own store.** The flagship real
change is Kubernetes moving off
`kubectl.kubernetes.io/last-applied-configuration`. Client-side apply stashed a
whole copy of the applied manifest in an annotation on somebody else's object;
server-side apply replaced it with `.metadata.managedFields`, a first-class API
field owned by the API server (KEP-555:
https://github.com/kubernetes/enhancements/blob/master/keps/sig-api-machinery/555-server-side-apply/README.md,
docs: https://kubernetes.io/docs/reference/using-api/server-side-apply/). The
annotation is the closest available analogue to what niwa is doing, and the
Kubernetes ecosystem spent years paying for it — see (3) below.

**(b) Keep accepting it, mark it deprecated, make it a no-op, remove at a major.**
This is the Terraform provider playbook, and it is written down:
https://developer.hashicorp.com/terraform/plugin/framework/deprecations and
https://developer.hashicorp.com/terraform/plugin/sdkv2/best-practices/deprecations.
Set `DeprecationMessage` on the attribute with a practitioner-actionable string
("Remove this attribute's configuration as it's no longer in use and the
attribute will be removed in the next major version of the provider"); the
framework raises a warning diagnostic when a known value is configured; removal
only in a MAJOR bump. Note this is advice for the *schema owner*, not for a
generator writing into someone else's schema — Terraform HCL has no tolerance for
unknown attributes at all, so there is no "stash a note in the resource block"
option there; people use `tags` or `description` fields instead.

**(c) Migrate on read.** Keep loading the legacy key for compatibility, rewrite
it into the new shape on first load. Real example surfaced in search: Hermes
Agent's `display.tool_progress_overrides` still loads but is migrated into
`display.platforms` on first load
(https://hermesagent.org.cn/en/docs/user-guide/configuration). npm's
`lockfileVersion` bumps are the same shape at larger scale. This is the option
that applies when *you* own the schema, which niwa does for its own config.

**(d) Move the value into a purpose-built extension namespace in the foreign
document.** Covered in (4).

There is a fifth non-resolution that is what niwa is doing today: keep writing
the dead key and keep reading it. Nobody recommends this in writing, and the
dbt-core / dbt-databricks threads
(https://github.com/dbt-labs/dbt-core/issues/12314,
https://github.com/databricks/dbt-databricks/issues/1636) show the cost when the
consumer later starts warning on custom keys — users who had done nothing wrong
got `CustomKeyInConfigDeprecation` warnings they could not act on.

### 2. Annotation in place, and how it fails

**Comment-bearing formats.** Where the format allows comments, the generated-file
header is a genuine, load-bearing convention rather than folklore. Go's is
specified: a line matching `^// Code generated .*DO NOT EDIT\.$`, anywhere in the
file, recognized by linters and tooling (proposal
https://github.com/golang/go/issues/13560, spec at
https://golang.org/s/generatedcode). `yarn.lock` and `package-lock.json`-adjacent
tools carry the same header in comment form. This works and does not rot,
because the comment is not data — nothing reads it back.

**JSON has no comments, so this splits into two sub-conventions:**

- **The `//` key.** In `package.json` this is real, widely used, and npm has said
  the key will never be used by npm — but it is convention, not spec: it is not in
  npm's normative `package.json` docs, and it has a documented failure mode, since
  duplicate `//` keys get dropped when npm rewrites the file. Roundups:
  https://bobbyhadz.com/blog/add-comments-to-package-json,
  https://jsonlinter.dev/comments-in-json/. I would call this "documented
  folklore" — safe in `package.json` specifically, not transferable.
- **`_`-prefixed or `x-`-prefixed sibling keys.** See (4).

**What went wrong afterwards, concretely.** Two clean citations, both the exact
failure mode you would be signing up for:

- **Docker Compose.** `x-` fields are the *one* case where Compose silently
  ignores unrecognized keys
  (https://docs.docker.com/reference/compose-file/extension/, spec:
  https://github.com/compose-spec/compose-spec/blob/main/spec.md). Then someone
  added a stricter sub-schema and it stopped being true in one place: adding an
  `x-` field under a long-form `depends_on` entry produces "Additional property is
  not allowed," while the identical field is ignored everywhere else
  (https://github.com/docker/compose/issues/11930). That is precisely the
  Claude Code shape — root `additionalProperties: true`, `permissions`
  `additionalProperties: false` — and it is the argument against putting your
  note inside `permissions`.
- **Kubernetes CRDs.** Before 1.16, custom resources tolerated arbitrary extra
  fields and people stashed things in them. Structural schemas made pruning the
  default, and unknown fields are now **silently deleted** unless the schema opts
  out with `x-kubernetes-preserve-unknown-fields: true`
  (https://kubernetes.io/blog/2019/06/20/crd-structural-schema/,
  https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/).
  Note the failure is silent data loss, not an error — a generator that read its
  signal back out of a pruned field would just see it missing.

I found **no project that used an unknown-key convention specifically to
annotate a now-inert key it was still writing**. The unknown-key namespaces I
found all exist to carry *live* configuration for a second tool, not to explain a
dead one. So the "annotate in place" option here is not backed by prior art; it
would be an invention.

### 3. Recomputing rather than round-tripping

I could not find a settled, widely used name for this. Searches for a canonical
term returned nothing but generic "single source of truth" material, so anyone
who tells you it is called something specific is probably reaching. Be honest
about that in the writeup. The nearest real framings:

- **Single-source-of-truth violation / derived data promoted to input.** The
  generated file is a *projection* of niwa's config; reading it back makes the
  projection authoritative for a value niwa already knows. In network-automation
  literature this is discussed as source-of-truth versus source-of-intent
  (https://codilime.com/blog/source-of-truth-vs-source-of-intent-in-network-automation/) —
  intent lives upstream, the rendered artifact is derived and disposable.
- **Terraform's version of the rule:** state records what exists; the
  configuration records what you meant. Recovering intent from state is drift
  detection running backwards.
- **The Kubernetes annotation story is the empirical case for the fix.** The
  `last-applied-configuration` annotation was a generator round-tripping its own
  intent through a foreign document, and the failure modes are documented and
  ugly: it duplicates the object into `metadata`, blows the 262144-byte
  annotation limit for large ConfigMaps and CRDs
  (https://github.com/kubernetes/kubectl/issues/712,
  https://github.com/kubernetes/kubernetes/issues/85840,
  https://devopscube.com/argocd-metadata-annotations-too-long/), and goes stale
  the moment a second actor touches the object, so the annotation "contains data
  that does not match either Git or the actual live state." The fix was not a
  better annotation. It was moving ownership tracking into a field the platform
  itself owns and maintains.

**The standard fix, stated plainly:** the generator recomputes the value from its
own inputs at the moment it needs it, and keeps any state it genuinely cannot
recompute in its own sidecar. Real sidecars in the wild: chezmoi's
`chezmoistate.boltdb` next to its config, entirely separate from the dotfiles it
writes (https://www.chezmoi.io/reference/command-line-flags/global/); npm's hidden
lockfile at `node_modules/.package-lock.json`; Flux's `.status.inventory` on the
Kustomization object rather than a marker on each managed resource
(https://fluxcd.io/flux/components/kustomize/kustomizations/); Helm's release
records in dedicated Secrets rather than in the rendered manifests.

For niwa specifically, the permission posture is derived from niwa's own
workspace/instance config. Nothing needs to survive a round trip. The read is
pure redundancy.

### 4. Naming a private key inside a foreign namespace

Three conventions with real specs behind them:

- **`x-` prefix.** OpenAPI specification extensions; Compose extension fields;
  `x-kubernetes-*` in CRD schemas. The strongest of the three because the *host*
  spec reserves the prefix and promises to ignore it.
- **A single reserved sub-object per tool.** The devcontainer spec's
  `customizations`, where each tool takes a unique key (`vscode`, `codespaces`)
  (https://containers.dev/supporting,
  https://github.com/devcontainers/spec/blob/main/docs/specs/supporting-tools.md).
  This is the cleanest design: one blessed door, no collisions, the host spec
  never has to grow to accommodate you.
- **`_`-prefixed keys.** Common in practice, reserved by nobody. Weakest.

**What breaks when the host adds strict validation.** Three distinct failure
modes, all attested:

1. **Hard rejection** of the extra key (Compose `depends_on`, #11930).
2. **Silent pruning** — the key is accepted, dropped, and your later read finds
   nothing (Kubernetes CRD structural-schema pruning).
3. **Collateral damage to the whole document.** This is the Claude Code-specific
   risk: the docs say a "value the schema rejects" produces a Settings Error and
   the option to "continue without the broken settings," i.e. one bad key can
   cost you the entire file, including the `permissions.allow` list that actually
   does work. A Settings Warning would only drop the entry, but which bucket an
   unknown key lands in is not documented.

Add the mundane one: editors. Anyone with VS Code, Cursor, or JetBrains pointed
at the SchemaStore schema will see a red squiggle under a custom key inside
`permissions`, forever, in every managed instance.

### 5. What applies cleanly to niwa

Ranked by track record:

1. **Move it to niwa's own instance metadata** (the sidecar). This is what the
   Kubernetes, Flux, Helm, npm and chezmoi precedents all converge on, and it is
   the only option with a real migration story behind it. But note the sharper
   version: niwa should not need *any* stored signal, because the value is
   derivable from niwa's config. Recompute at dispatch time. Storing it in a
   sidecar is the fallback if some intent genuinely cannot be recomputed.
2. **Move it into the `--settings` payload.** Strong second, and it is the option
   the Claude Code docs themselves point at: "pass `--permission-mode` for one
   session," with `--settings` sitting above every file except managed in the
   precedence stack. This makes the written artifact and the actual posture agree
   again, which is the real complaint. The cost is that the posture becomes
   invisible in the checked-in file — worth stating, not disqualifying.
3. **Rename to a niwa-owned key at the document root.** Defensible and cheap:
   root `additionalProperties: true` makes it schema-legal today, and it is
   honest about ownership. Follow the devcontainer pattern — one object,
   `"niwa": { ... }`, not scattered `_niwa*` keys. But it is still a round trip
   through a foreign document, so it does not fix (3), and it inherits the
   silent-pruning risk if Anthropic ever tightens the root.
4. **Annotate in place.** Worst of the four. It is the one option with no prior
   art for this exact use, it lands in the one sub-object whose schema says
   `additionalProperties: false`, it keeps the misleading value in the file
   (a reader still sees `"defaultMode": "bypassPermissions"` and still draws the
   wrong conclusion — the comment mitigates for humans reading carefully and does
   nothing for a grep or an agent), and it keeps the round trip.

The combination I would put forward: **recompute the posture from niwa's config
and pass it on the command line (2 + the sharp version of 1), and stop writing
`permissions.defaultMode` into project-scope settings at all.** That removes the
false premise from the artifact, removes the round trip, and matches what the
Claude Code docs say the mechanism is.

## Implications

- The "annotate in place" option is not just aesthetically weak, it is
  schema-illegal in the specific spot it would go. `permissions` is
  `additionalProperties: false` in the published schema. If the exploration was
  leaning that way, this should move it.
- The key question for the design is not "where do we put the signal" but "why is
  there a stored signal at all." Every precedent found says the generator should
  recompute from its own inputs. Framing the fix as a *naming* problem
  under-solves it.
- Whatever is chosen, the misleading `permissions.defaultMode` value should stop
  being written. Leaving it in place with an explanatory sibling still hands the
  next reader an authoritative-looking permissions block that does not govern the
  session, which is the originating complaint.

## Surprises

- Root `additionalProperties: true` but `permissions: additionalProperties:
  false` — a near-exact replay of the Docker Compose `depends_on` extension-field
  bug, in the same shape, in the same year.
- `claude -p` in v2.1.267 surfaced **no** settings diagnostic even for a
  flat-out invalid `defaultMode` enum value, and `claude doctor` reported nothing
  afterwards despite the docs saying it lists dropped entries. Whatever niwa does,
  it cannot rely on a headless run to tell it the settings file is wrong.
- There is no established name for the round-trip anti-pattern. I expected one.
- No project appears to have used an unknown-key namespace to annotate a dead key
  it kept writing. Extension namespaces exist for live second-tool config, not
  for tombstones.

## Open Questions

- Does Claude Code (interactive) actually warn on an unknown key inside
  `permissions`, and is it a Settings Warning (entry dropped) or a Settings Error
  (file dropped)? My headless test could not tell. Someone should launch an
  interactive session against a settings file with a custom key under
  `permissions` and read the startup dialog.
- Is `permissions.defaultMode` from project scope inert for *all* values, or only
  for `auto` and `bypassPermissions`? The docs name only those two, which implies
  `plan`, `acceptEdits`, and `dontAsk` still take effect from project scope. If
  niwa ever writes those, the key is not uniformly dead and the framing needs
  narrowing.
- Does niwa have an existing instance-metadata file that could carry the signal,
  or would option 1 mean inventing one? That changes the cost comparison between
  options 1 and 2 materially.

## Summary

Claude Code's docs confirm `permissions.defaultMode` values `auto` and `bypassPermissions` are inert from project scope as of v2.1.257, and the published settings schema has root `additionalProperties: true` but `permissions.additionalProperties: false` — so a sibling `_comment` key next to `defaultMode` is the one placement that is actually schema-illegal, replaying the Docker Compose `x-` under `depends_on` bug. Every real precedent for this situation — Kubernetes replacing the `last-applied-configuration` annotation with server-side apply's `managedFields`, Flux's status inventory, chezmoi's own state DB, npm's hidden lockfile — resolves it by moving the signal out of the foreign document entirely and recomputing or storing it where the generator owns the schema; nobody found has used an unknown-key convention to annotate a dead key they kept writing. The recommendation with the best track record is to stop writing `permissions.defaultMode` into project settings, recompute the posture from niwa's own config, and pass it via `--permission-mode`/`--settings` on the worker command line.
