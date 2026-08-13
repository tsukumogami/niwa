#!/usr/bin/env bash
# Round 2: exercise `niwa dispatch` and the SessionStart ephemeral-instance hook
# (`niwa instance from-hook`) against the Persona A workspace left by repro.sh.
set -u

SBX="${TMPDIR:-/tmp}/niwa-repro-sbx"
export HOME="$SBX/home"
export XDG_CONFIG_HOME="$SBX/home/.config"
FAKEBIN="$SBX/fakebin"
mkdir -p "$FAKEBIN"
# dispatch requires a `claude` binary on PATH before it provisions.
cat > "$FAKEBIN/claude" <<'EOF'
#!/bin/sh
echo "FAKE CLAUDE INVOKED: $*" >&2
exit 0
EOF
chmod +x "$FAKEBIN/claude"
export PATH="$FAKEBIN:/usr/bin:/bin:/usr/sbin:/sbin"
NIWA="$SBX/niwa"

banner() { echo; echo "################ $* ################"; echo; }

banner "DISPATCH: niwa dispatch \"do a thing\" --detach  (Persona A workspace)"
cd "$SBX/tsukumogami"
"$NIWA" dispatch "do a thing" --detach; echo "EXIT=$?"

banner "DISPATCH: leftover instance dirs?"
ls -1 "$SBX/tsukumogami" "$SBX" | sed "s|$SBX|<sbx>|"

banner "SESSIONSTART HOOK: ephemeral mode OFF (guard should no-op)"
printf '%s' '{"hook_event_name":"SessionStart","session_id":"11111111-2222-3333-4444-555555555555","cwd":"'"$SBX"'/tsukumogami"}' \
  | "$NIWA" instance from-hook; echo "EXIT=$?"

banner "SESSIONSTART HOOK: ephemeral mode ON + fake bg job state (guard passes)"
# turn on ephemeral-session mode in the workspace-root state
python3 - "$SBX/tsukumogami/.niwa/instance.json" <<'PY'
import json,sys
p=sys.argv[1]
d=json.load(open(p))
d["ephemeral_session_mode"]=True
d["ephemeral_sessions"]=True
json.dump(d,open(p,"w"),indent=2)
print(json.dumps(d,indent=2))
PY
SID="11111111-2222-3333-4444-555555555555"
JOBS="$HOME/.claude/jobs"
mkdir -p "$JOBS/${SID:0:12}"
printf '{"sessionId":"%s","template":"bg"}' "$SID" > "$JOBS/${SID:0:12}/state.json"
cd "$SBX"
printf '%s' '{"hook_event_name":"SessionStart","session_id":"'"$SID"'","cwd":"'"$SBX"'/tsukumogami"}' \
  | "$NIWA" instance from-hook; echo "EXIT=$?"

banner "state file keys (to confirm the ephemeral flag name)"
cat "$SBX/tsukumogami/.niwa/instance.json"

banner "DONE"
