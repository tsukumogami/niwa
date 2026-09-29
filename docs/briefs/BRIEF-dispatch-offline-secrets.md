---
schema: brief/v1
status: Accepted
problem: |
  Provisioning an instance needs a live vault login at the moment it runs. When the
  Infisical CLI session is logged out or expired, `niwa dispatch` and the create and apply
  steps behind it stop, even though the same values resolved earlier and already sit in
  existing instances. Unattended hosts have nobody to log back in and nothing can prompt.
outcome: |
  An owner can leave a host unattended and its sessions keep provisioning workers after
  the vault login lapses, on the last values that resolved, with a plain warning each time
  that says which provider was unavailable, how old the values are, and how to log back
  in. Errors a stale value would hide still stop the run.
---

# BRIEF: Provisioning keeps working when the vault login lapses

## Status

Accepted

## Problem Statement

niwa resolves `vault://` references at provisioning time by calling the vault provider's CLI,
and for this workspace that provider is Infisical. The call only succeeds while the operator's
Infisical CLI session is alive. When that session lapses (logged out, or expired after its
normal lifetime), every command that provisions an instance stops. That covers `niwa dispatch`,
`niwa create`, `niwa apply`, `niwa watch`, and the session-start hook that provisions an
instance for a new session. It stops even when the provider holds exactly the values it held yesterday, and when
those same values are already written in plain text into the instances the owner is running.

The failure is worst where nobody is watching. A workspace owner who runs long-lived coordinator
sessions expects them to keep starting worker sessions while the owner is away for days or
weeks. A CLI login doesn't last that long, and once it lapses there is no one at the machine to run
`infisical login` and no terminal a background process could prompt on. The coordinators stay
up, but every attempt to start a worker fails, so the whole setup quietly stops doing work until
the owner comes back.

It is also confusing when someone is watching. A logged-out Infisical CLI does not say "logged
out" in words niwa recognizes. It prints a message about triggering a login flow, or about not
finding logged-in details, and exits 1. niwa reads that as an unexplained provider error and
aborts, so the owner gets a generic failure rather than "you are logged out; log back in". The
lapse can also reach secrets that come from the host's personal global configuration, such as a
vault-sourced GitHub token, not only the workspace's own secrets. So a lapsed login can break
steps that have nothing to do with the workspace's configuration.

## User Outcome

A workspace owner can leave a host unattended and trust that its sessions keep starting workers
after the vault login lapses. When the provider can't be reached because nobody is logged in,
because the login expired, or because the vault service itself can't be reached, provisioning goes ahead on the last
set of values that did resolve. Each such run says so plainly. It names the provider, says the
values may be stale and how old they are, and gives the command that logs back in. The owner
comes back to finished work and a clear trail of warnings, not a pile of failed dispatches.

The fallback doesn't hide real problems. If the provider is reachable and authenticated but a
secret has been deleted or access to it was revoked, the run still stops. That's a configuration
change the owner needs to see, and serving yesterday's value would paper over it. On a host that
has never resolved its secrets successfully, there is nothing to fall back on, and the run fails
with an error that says so and says how to log in.

Once the owner logs back in, the next run resolves fresh values, the stored copy is refreshed,
and the warnings stop without anyone clearing anything by hand.

## User Journeys

### Unattended coordinator starts a worker after the login expired

A coordinator session, running alone on the owner's desktop while the owner is travelling, runs
`niwa dispatch` to start a worker for the next task. The Infisical login expired the night
before. The worker's instance is created, applied, and launched with the values that last
resolved, and the dispatch output carries a warning naming Infisical, the age of the values, and
`infisical login`. The coordinator carries on. Nothing waits for input, and nothing hangs on a
login prompt.

### Owner provisions by hand while logged out

The owner, at a terminal, runs `niwa create` or `niwa apply` on a workspace and has forgotten
that the Infisical session lapsed. The command completes on the stored values and prints the
same warning, so the owner learns about the lapse from the command they were running. They don't
have to diagnose a generic provider failure, and they get the same behaviour whichever
provisioning command they reached for.

### First run on a host that has never resolved its secrets

On a freshly set-up machine, the owner runs `niwa dispatch` before ever logging in to Infisical.
There is no stored set of values yet. The command fails, and the error says that the provider
could not be reached, that there are no previously resolved values to fall back on, and how to
log in. It does not fail with an unexplained provider error.

### A host-level token comes from the vault

The host's personal global configuration sources a GitHub token from the vault, and niwa prefers
that token over a static one the host also defines. With the login lapsed, the owner dispatches
a worker. The GitHub-authenticated steps still work, because the host-level secrets are covered
the same way the workspace's are. That lapse no longer turns into a failed clone or an
unauthenticated API call.

### A secret was deleted upstream while the owner was away

A teammate removes a secret the workspace still references. The owner's login is valid. The next
provisioning run reaches the provider, finds the key missing, and stops with the same error it
gives today. It does not quietly serve the last value it saw.

### The owner logs back in

On return, the owner runs `infisical login` and dispatches once. Resolution succeeds, the stored
values are replaced with the fresh ones, and no warning is printed.

## Scope Boundary

**In:**

- Every command that provisions an instance and resolves vault secrets: `niwa dispatch`, `niwa
  create`, `niwa apply`, the session-start hook's instance provisioning, and `niwa watch`, which
  shares dispatch's provisioner. Dispatch is where the pain was reported, but it runs the same resolution as the others. Fixing it alone would leave
  the others failing on the same lapse.
- Secrets from every configuration layer that resolves through the vault: the workspace config,
  its overlay, and the host's personal global configuration.
- Recognizing a lapsed login reliably, including the wording today's Infisical CLI prints, and
  making sure an unattended run can never block waiting for a login it cannot get.
- A stored copy of the last successfully resolved values, kept on the local machine outside
  anything committed or published, readable only by the owner.
- A warning on every run that falls back, and a clear error when there is nothing to fall back
  on.
- Deciding, per failure kind, which failures fall back and which still stop the run.

**Out:**

- Storing the values in an OS keyring or secret store, and encrypting the stored copy. The owner
  has accepted plain text on disk, since the same values already sit in plain text in every
  instance. A keyring is a later enhancement.
- Any change to how resolved values are written into instances today (which files, what format,
  what permissions).
- Logging in automatically or refreshing the Infisical session on the owner's behalf. niwa keeps
  delegating authentication to the provider's own CLI.
- Vault providers other than Infisical, beyond whatever falls out of the shared resolution path
  for free. Infisical is the one in use, and its failure wording is the one being handled.
- Falling back when the provider is reachable and authenticated but a secret is missing or
  access is denied. Those stay hard failures on purpose.
- Provisioning with no network at all. Creating an instance also refreshes the workspace and
  global configuration from their remotes, which needs the network whatever happens to secrets.
  The fallback covers an unreachable vault; it does not make provisioning work offline.
- Changing what strict-secrets mode means. A value served from the stored copy counts as
  supplied, so strict mode passes it, and the warning still prints.
