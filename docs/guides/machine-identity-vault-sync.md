# Machine-Identity Vault Sync

niwa can read the per-org machine-identity credentials it needs to
authenticate vault providers from your personal vault, instead of
making you maintain `~/.config/niwa/provider-auth.toml` on every
laptop. Edit one place; every machine picks up the new value on its
next `niwa apply`.

This guide walks you through enabling credential sync, setting up
the vault schema, and reading audit output.

## When to use this

Enable credential sync when you check at least one of these boxes:

- You work across two or more Infisical orgs and have to
  hand-create `provider-auth.toml` entries on every machine.
- You rotate machine-identity client_secrets and currently update
  every laptop separately.
- A new project adds an Infisical org you haven't onboarded
  before, and you want the credential added once instead of per
  machine.

If you only ever use the org you're logged into via `infisical
login`, you don't need this feature — niwa already routes all
vault calls through your active CLI session.

## What the local credential file is for, after this lands

`~/.config/niwa/provider-auth.toml` does NOT go away. It becomes a
per-machine override layer:

- **Vault entry exists, no local file entry** → niwa fetches from
  the vault.
- **Vault entry exists, local file entry exists** → the local file
  wins. Use this to pin one machine to a specific credential
  version while debugging without touching the vault.
- **No vault entry, local file entry** → existing behavior; no
  change.
- **Neither layer has an entry** → falls through to the backend's
  CLI session (`infisical login` for Infisical).

`niwa status --audit-auth` shows you which source authenticated
each provider in the last apply. See the audit-auth section below.

## Enabling credential sync

In your **personal overlay**'s `niwa.toml`, declare an anonymous
`[global.vault.provider]` block. That declaration is itself the
opt-in — no separate machine-identity opt-in block is needed:

```toml
[global.vault.provider]
kind = "infisical"
project = "your-personal-org-uuid"
```

Any anonymous personal-overlay vault provider serves both
roles: it resolves `vault://` URIs declared elsewhere in the
config AND supplies the machine-identity credentials niwa
needs to authenticate against other vault providers.

Named providers under `[global.vault.providers.<name>]`
participate in `vault://` URI resolution but do NOT serve as
the credential-sync source. If you want named providers for
URI resolution AND credential sync, add a separate anonymous
`[global.vault.provider]` block alongside them.

## Vault key schema

niwa reads each credential body from a single key in your personal
Infisical project. The path convention is:

```
/niwa/provider-auth/infisical/<project-uuid>
```

Where `<project-uuid>` is the Infisical project ID for the OTHER
org niwa needs to authenticate against (the one whose
`provider-auth.toml` entry you're replacing).

The body is a small TOML document with these fields:

```toml
version = "1"
client_id = "<your-machine-identity-client-id>"
client_secret = "<your-machine-identity-client-secret>"
api_url = "https://app.infisical.com"   # optional; omit for default
```

Future schema versions are gated by the `version` field. niwa v1
accepts only `version = "1"`; a body without a `version` field is
treated as version 1 for backward compatibility.

## Populating the vault

Use the Infisical CLI from your local machine, against your
personal org:

```sh
# Switch the active session to your personal org.
infisical login

# Set the credential body for, say, the team-A org whose
# project-uuid is 550e8400-e29b-41d4-a716-446655440000.
cat <<'EOF' > /tmp/team-a-cred.toml
version = "1"
client_id = "<team-a-machine-identity-client-id>"
client_secret = "<team-a-machine-identity-client-secret>"
EOF

infisical secrets set \
  --path "/niwa/provider-auth/infisical" \
  "550e8400-e29b-41d4-a716-446655440000=$(cat /tmp/team-a-cred.toml)"

rm /tmp/team-a-cred.toml
```

(The exact CLI invocation depends on your Infisical version. The
key is the path/key pair: path `/niwa/provider-auth/infisical/`,
key `<project-uuid>`, value the TOML body.)

Repeat for each org's machine identity.

## Fresh-laptop bootstrap

The bootstrap chain is:

```sh
# 1. Authenticate niwa against your home org once.
infisical login

# 2. Tell niwa about your personal overlay.
niwa config set global https://github.com/<you>/niwa-overlay

# 3. Run apply. niwa fetches each per-org machine identity from
#    your personal vault as the apply touches its provider.
niwa apply
```

The personal vault provider itself authenticates via the active
`infisical login` session — niwa never tries to use a
machine-identity credential to authenticate the vault that supplies
machine-identity credentials. niwa enforces this with a
chicken-and-egg check at apply time: if the personal vault's
`(kind, project)` collides with any local credential file entry or
any other vault provider declared in your overlays, the apply
aborts before opening the provider.

## The `niwa status --audit-auth` command

After an apply, this command renders the credential-source decision
for every `(kind, project)` pair niwa needed:

```sh
niwa status --audit-auth
```

Sample output:

```
KIND       PROJECT-UUID                          SOURCE              FALLBACK
infisical  550e8400-e29b-41d4-a716-446655440000  local-file          vault:personal
infisical  660f9511-f40c-52e5-b827-557766551111  vault:personal      —
infisical  770a0622-a51d-63f6-c938-668877662222  cli-session         —
```

Column meaning:

- `KIND` — the vault backend kind (today only `infisical`).
- `PROJECT-UUID` — the Infisical project the credential was for.
- `SOURCE` — where the credential came from in the last apply:
  - `local-file` — `~/.config/niwa/provider-auth.toml`.
  - `vault:<name>` — your personal vault. Anonymous renders as
    `vault:(anonymous)`.
  - `cli-session` — niwa fell through to the backend's active
    session (e.g., `infisical login`).
  - `none` — no source produced a usable credential.
- `FALLBACK` — when both file and vault had an entry but the file
  won, this column shows the vault entry that lost. Empty (`—`)
  otherwise.

The command runs **fully offline**: no vault calls, no network.
It reads `state.json` from the most recent apply. The exit code is
`0` when every row has a usable source, non-zero when any row has
SOURCE = `none`. A live-verification flag that re-fetches every
vault entry to detect drift is deferred to a future release.

## Apply-time stderr signal

When at least one `(kind, project)` was sourced from the vault on
this apply, niwa emits one stderr line per pair:

```
auth: infisical/550e8400-... source=vault:personal
```

When the local file overrode a vault entry, you also see a
fallback line:

```
auth: infisical/660f9511-... source=local-file fallback=vault:personal
```

The lines fire BEFORE backend authentication runs, so even an
apply that subsequently fails (e.g., because the vault is
unreachable and no fallback covers a needed pair) still tells you
which credentials were taken from where.

CLI-session sources and pure local-file sources (no vault entry)
are silent — there's nothing vault-sync-related to report for
those pairs.

## Rotation

Update one place: change the credential body in your personal
vault. Every machine picks up the new value on its next `niwa
apply`.

```sh
# On your laptop, after rotating the machine identity in Infisical:
infisical secrets set \
  --path "/niwa/provider-auth/infisical" \
  "<project-uuid>=$(cat new-cred.toml)"
```

niwa never caches machine-identity credentials between applies, so
the next `niwa apply` on every machine fetches fresh.

## Common errors

### `Personal vault provider (kind=..., project=...) cannot be bootstrapped by an entry in the local credential pool — this would create a chicken-and-egg cycle.`

You're trying to authenticate the personal vault using a credential
sourced from the credential pool — including the vault itself. niwa
forbids this because it would create a circular dependency.

Fix: authenticate the personal vault via the CLI session
(`infisical login` for Infisical) instead. Make sure there's no
`provider-auth.toml` entry whose `(kind, project)` matches the
personal vault's identity, and no other vault provider in your
overlay shares that identity.

### `vault-sourced provider-auth body at /niwa/provider-auth/.../... is malformed: TOML parse error`

The credential body fetched from the vault isn't valid TOML.
Common causes: pasting a JSON body, leaving a stray comma, or
running an Infisical CLI version that re-encodes the value.

Fix: re-set the body via `infisical secrets set` with a known-good
TOML document and verify it via `infisical secrets get`.

### `vault-sourced provider-auth body at /niwa/provider-auth/.../... is missing required field "client_id"` (or "client_secret")

The body parses but lacks one of the required fields. Both
`client_id` and `client_secret` are required.

Fix: re-set the body with both fields populated.

### `provider-auth body at /niwa/provider-auth/.../... has unsupported schema version "X"`

The body's `version` field is something niwa doesn't recognize
(today, only `"1"` is supported). Either upgrade niwa or update
the vault entry to `version = "1"`.

### `warning: personal vault provider <name> unreachable; falling back to local-file and cli-session credentials.`

niwa couldn't reach the personal vault on this apply (network,
authentication, or `infisical` CLI not installed). niwa emits ONE
warning per affected provider per apply, then continues:

- If every needed `(kind, project)` is covered by either a local
  file entry or the backend's CLI session, the apply succeeds
  with exit code 0.
- If some pair is uncovered, the apply fails at the backend's
  universal-auth call, with exit code non-zero.

Fix: ensure your `infisical login` session is active and the
network is reachable. If you're working offline, populate the
needed entries in `~/.config/niwa/provider-auth.toml` so the file
layer covers them.

## Public-overlay safety

The plaintext-secrets guardrail still walks `*.secrets` tables in
your committed config files. Enabling machine-identity sync from a
public personal-overlay repo does NOT expose secret values: your
credential bodies live in the vault, not in any TOML file you
commit. The only thing your `niwa.toml` reveals is the topology
(the provider kind and the project UUID of your personal vault),
which is the same information any committed `[global.vault.*]`
declaration already exposes regardless of credential sync.

## What this feature does not do

- It does not replace `provider-auth.toml`; the file remains the
  per-machine override layer.
- It does not auto-mint machine identities. You create them via
  the Infisical dashboard or CLI; niwa reads what's there.
- It does not write to your vault. niwa only reads.
- It does not cross user boundaries. Per-user distribution only;
  team-wide credential distribution is out of scope.
- It does not redirect `vault://` URIs. The team config still
  queries the team's vault. Only the auth credentials are sourced
  from your personal vault instead of from a hand-edited file.
