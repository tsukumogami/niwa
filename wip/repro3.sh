#!/usr/bin/env bash
set -u
SBX="${TMPDIR:-/tmp}/niwa-repro-sbx"
export HOME="$SBX/home"
export XDG_CONFIG_HOME="$SBX/home/.config"
export PATH="/usr/bin:/bin:/usr/sbin:/sbin"
NIWA="$SBX/niwa"
cd "$SBX/tsukumogami"
echo "### niwa status"
"$NIWA" status
echo "STATUS_EXIT=$?"
echo
echo "### niwa status check-vault"
"$NIWA" status check-vault
echo "CV_EXIT=$?"
echo
echo "### niwa create --help | grep allow"
"$NIWA" create --help 2>&1 | grep -A3 "allow-missing"
echo
echo "### niwa init --help | grep allow"
"$NIWA" init --help 2>&1 | grep -c "allow-missing"
