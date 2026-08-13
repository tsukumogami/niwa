#!/usr/bin/env bash
# Reproduction harness for the "required secrets with no vault backend" wall.
# Offline: uses a local bare git repo as the config source, and a PATH with no
# `infisical` binary.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SBX="${TMPDIR:-/tmp}/niwa-repro-sbx"
rm -rf "$SBX"
mkdir -p "$SBX"

export HOME="$SBX/home"
export XDG_CONFIG_HOME="$SBX/home/.config"
mkdir -p "$HOME" "$XDG_CONFIG_HOME"
# Strip any real infisical CLI from PATH.
export PATH="/usr/bin:/bin:/usr/sbin:/sbin:$(dirname "$(command -v git)")"
unset NIWA_RESPONSE_FILE GH_TOKEN GITHUB_TOKEN ANTHROPIC_API_KEY INFISICAL_TOKEN 2>/dev/null || true

NIWA="$SBX/niwa"
(cd "$ROOT" && HOME="$SBX/home" /usr/local/go/bin/go build -o "$NIWA" ./cmd/niwa) || { echo "build failed"; exit 1; }

GS="$SBX/gitserver"
mkdir -p "$GS"

mkrepo() { # $1=name $2=workspace.toml body
  local name="$1" body="$2"
  local bare="$GS/$name.git"
  git init --bare -q "$bare"
  git -C "$bare" symbolic-ref HEAD refs/heads/main
  local wd; wd="$(mktemp -d "$GS/wd-XXXX")"
  git clone -q "file://$bare" "$wd"
  mkdir -p "$wd/.niwa"
  printf '%s\n' "$body" > "$wd/.niwa/workspace.toml"
  git -C "$wd" add -A
  GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@e GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@e \
    git -C "$wd" commit -qm initial
  git -C "$wd" push -q -u origin HEAD
  rm -rf "$wd"
  echo "file://$bare"
}

banner() { echo; echo "################ $* ################"; echo; }

########## PERSONA A: public base config, no vault provider at all ##########
A_URL="$(mkrepo dot-niwa "$(cat <<'TOML'
[workspace]
name = "tsukumogami"
content_dir = "claude"

[groups.public]
visibility = "public"

[env.secrets.required]
ANTHROPIC_API_KEY  = "Anthropic API key for Claude - resolved by overlay vault"
GH_TOKEN           = "GitHub PAT with repo scope - supplied via personal overlay"

[env.secrets.recommended]
TAVILY_API_KEY     = "Tavily search API key - resolved by overlay vault"
BRAVE_API_KEY      = "Brave search API key - resolved by overlay vault"

[env.secrets.optional]
TELEGRAM_BOT_TOKEN = "Telegram bot token for workflow notifications - resolved by overlay vault"

[repos.niwa.env.secrets.required]
INFISICAL_TEST_PROJECT_ID = "Infisical test project ID - resolved by overlay vault"
INFISICAL_CLIENT_ID       = "Infisical machine identity client ID - resolved by overlay vault"
INFISICAL_CLIENT_SECRET   = "Infisical machine identity client secret - resolved by overlay vault"
TOML
)")"

banner "PERSONA A / step 1: niwa init tsukumogami --from <base>"
cd "$SBX"
"$NIWA" init tsukumogami --from "$A_URL" --no-overlay; echo "EXIT=$?"
echo "--- workspace root tree ---"
find "$SBX/tsukumogami" -maxdepth 3 -not -path '*/.git/*' | sed "s|$SBX|<sbx>|"
echo "--- registry ---"
cat "$XDG_CONFIG_HOME/niwa/config.toml" 2>/dev/null | sed "s|$SBX|<sbx>|"

banner "PERSONA A / step 2: niwa create tsukumogami"
cd "$SBX/tsukumogami"
"$NIWA" create tsukumogami; echo "EXIT=$?"

banner "PERSONA A / step 3: niwa create tsukumogami --allow-missing-secrets"
"$NIWA" create tsukumogami --allow-missing-secrets; echo "EXIT=$?"

banner "PERSONA A / step 4: leftover state after failed create"
ls -la "$SBX" | sed "s|$SBX|<sbx>|"
echo "--- registry still has entry? ---"
cat "$XDG_CONFIG_HOME/niwa/config.toml" 2>/dev/null | sed "s|$SBX|<sbx>|"

banner "PERSONA A / step 5: niwa apply tsukumogami"
"$NIWA" apply tsukumogami; echo "EXIT=$?"

banner "PERSONA A / step 6: niwa dispatch (no --allow-missing-secrets flag?)"
"$NIWA" dispatch --help 2>&1 | sed -n '/Flags:/,$p'

########## PERSONA B: config declares a vault provider, no infisical CLI ##########
B_URL="$(mkrepo dot-niwa-b "$(cat <<'TOML'
[workspace]
name = "vaultws"

[groups.public]
visibility = "public"

[vault.provider]
kind = "infisical"
project = "some-project"
env = "dev"

[env.secrets]
ANTHROPIC_API_KEY = "vault://ANTHROPIC_API_KEY"
GH_TOKEN = "vault://GH_TOKEN"

[env.secrets.required]
ANTHROPIC_API_KEY  = "Anthropic API key for Claude"
GH_TOKEN           = "GitHub PAT with repo scope"
TOML
)")"

banner "PERSONA B / step 1: niwa init vaultws --from <base-with-vault>"
cd "$SBX"
"$NIWA" init vaultws --from "$B_URL" --no-overlay; echo "EXIT=$?"

banner "PERSONA B / step 2: niwa create vaultws  (no infisical on PATH)"
cd "$SBX/vaultws"
"$NIWA" create vaultws; echo "EXIT=$?"

banner "PERSONA B / step 3: niwa create vaultws --allow-missing-secrets"
"$NIWA" create vaultws --allow-missing-secrets; echo "EXIT=$?"

banner "DONE"
