#!/bin/sh
# claude: niwa's copy-rule stub of Claude Code, for tests only. stub.go in
# this directory documents what it models and the knobs it reads.
#
# Shell functions share one variable namespace, so each function below uses
# variable names of its own.

LC_ALL=C
export LC_ALL

state="${FAKE_CLAUDE_STATE:?FAKE_CLAUDE_STATE is not set}"
log="${FAKE_CLAUDE_LOG:?FAKE_CLAUDE_LOG is not set}"
sessions="$state/sessions"
jobs="$HOME/.claude/jobs"
mkdir -p "$sessions" "$jobs"

# Every invocation is logged first: a record marker, then each argument, each
# terminated by a NUL byte.
{
  printf '\001CALL\000'
  for a in "$@"; do printf '%s\000' "$a"; done
} >> "$log"

# knob NAME prints a behavior knob: the file $state/knob-NAME when there is
# one, else $FAKE_CLAUDE_NAME. NAME is always one of the fixed names below.
knob() {
  if [ -f "$state/knob-$1" ]; then
    cat "$state/knob-$1"
    return
  fi
  eval "printf '%s' \"\${FAKE_CLAUDE_$1:-}\""
}

# json_str prints $1 escaped for a JSON string.
json_str() {
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/	/\\t/g' |
    awk 'BEGIN { ORS = "" } NR > 1 { print "\\n" } { print }'
}

# flags_json prints the arguments before a bare "--" as a JSON array.
flags_json() {
  fj_out='['
  fj_sep=''
  for fj_a in "$@"; do
    [ "$fj_a" = -- ] && break
    fj_out="$fj_out$fj_sep\"$(json_str "$fj_a")\""
    fj_sep=','
  done
  printf '%s]' "$fj_out"
}

# prompt_of prints the argument after the first bare "--".
prompt_of() {
  po_seen=0
  for po_a in "$@"; do
    if [ "$po_seen" = 1 ]; then
      printf '%s' "$po_a"
      return
    fi
    [ "$po_a" = -- ] && po_seen=1
  done
}

# ver_lt succeeds when version $1 is older than $2, or isn't a version.
ver_lt() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    if (a !~ /^[0-9]+\.[0-9]+\.[0-9]+/) exit 0
    split(a, x, "."); split(b, y, ".")
    for (i = 1; i <= 3; i++) {
      if (x[i] + 0 < y[i] + 0) exit 0
      if (x[i] + 0 > y[i] + 0) exit 1
    }
    exit 1
  }'
}

# mint prints a fresh session id.
mint() {
  mn_n=$(cat "$state/counter" 2>/dev/null || echo 0)
  mn_n=$((mn_n + 1))
  printf '%s' "$mn_n" > "$state/counter"
  printf 'c0de%04x-%04x-4%03x-8%03x-%012x' "$mn_n" "$mn_n" "$mn_n" "$mn_n" "$mn_n"
}

# new_session SHORT SID CWD RESPAWN starts a live, idle session.
new_session() {
  ns_d="$sessions/$1"
  mkdir -p "$ns_d"
  printf '%s' "$2" > "$ns_d/sid"
  printf '%s' "$3" > "$ns_d/cwd"
  printf 1 > "$ns_d/alive"
  printf '%s' "$((40000 + $(cat "$state/counter" 2>/dev/null || echo 0)))" > "$ns_d/pid"
  printf idle > "$ns_d/status"
  printf 0 > "$ns_d/settle"
  printf done > "$ns_d/state"
  printf idle > "$ns_d/tempo"
  printf '%s' "$4" > "$ns_d/respawn"
  : > "$ns_d/extra"
  if [ "$(knob ARMED)" = 1 ]; then
    printf working > "$ns_d/state"
    printf '%s' '"inFlight":{"tasks":1,"kinds":["session_cron"]}' > "$ns_d/extra"
  fi
}

# write_job SHORT writes the session's job state.
write_job() {
  wj_d="$sessions/$1"
  wj_extra=$(cat "$wj_d/extra" 2>/dev/null)
  [ -n "$wj_extra" ] && wj_extra=",$wj_extra"
  wj_respawn=$(cat "$wj_d/respawn" 2>/dev/null)
  [ -n "$wj_respawn" ] || wj_respawn='[]'
  mkdir -p "$jobs/$1"
  printf '{"sessionId":"%s","template":"bg","cwd":"%s","state":"%s","tempo":"%s","respawnFlags":%s%s}\n' \
    "$(json_str "$(cat "$wj_d/sid")")" "$(json_str "$(cat "$wj_d/cwd")")" \
    "$(cat "$wj_d/state")" "$(cat "$wj_d/tempo")" "$wj_respawn" "$wj_extra" > "$jobs/$1/state.json"
}

# transcript CWD SID PROMPT appends the prompt, as one line, to the session's
# transcript.
transcript() {
  tr_enc=$(printf '%s' "$1" | sed 's/[^A-Za-z0-9]/-/g')
  tr_dir="$HOME/.claude/projects/$tr_enc"
  mkdir -p "$tr_dir"
  printf '{"type":"user","prompt":"%s"}\n' "$(json_str "$3")" >> "$tr_dir/$2.jsonl"
}

# shows_process DIR succeeds while the session's row still shows a pid and a
# status.
shows_process() {
  sp_settle=$(cat "$1/settle" 2>/dev/null)
  [ "$(cat "$1/alive")" = 1 ] || [ "${sp_settle:-0}" -gt 0 ]
}

list() {
  printf '['
  ls_sep=''
  for ls_d in "$sessions"/*; do
    [ -d "$ls_d" ] || continue
    [ -f "$ls_d/hidden" ] && continue
    ls_live=''
    ls_show=0
    if [ "$(cat "$ls_d/alive")" = 1 ]; then
      ls_show=1
    else
      ls_settle=$(cat "$ls_d/settle" 2>/dev/null)
      if [ "${ls_settle:-0}" -gt 0 ]; then
        ls_show=1
        printf '%s' "$((ls_settle - 1))" > "$ls_d/settle"
      fi
    fi
    if [ "$ls_show" = 1 ]; then
      ls_live=",\"pid\":$(cat "$ls_d/pid")"
      ls_st=$(cat "$ls_d/status")
      [ -n "$ls_st" ] && ls_live="$ls_live,\"status\":\"$(json_str "$ls_st")\""
    fi
    printf '%s{"id":"%s","sessionId":"%s","cwd":"%s"%s}' "$ls_sep" \
      "$(json_str "$(basename "$ls_d")")" "$(json_str "$(cat "$ls_d/sid")")" \
      "$(json_str "$(cat "$ls_d/cwd")")" "$ls_live"
    ls_sep=','
  done
  printf ']\n'
}

launch() {
  la_prompt=$(prompt_of "$@")
  la_sid=$(mint)
  la_short=$(printf '%s' "$la_sid" | cut -c1-8)
  la_cwd=$(pwd -P)
  new_session "$la_short" "$la_sid" "$la_cwd" "$(flags_json "$@")"
  write_job "$la_short"
  transcript "$la_cwd" "$la_sid" "$la_prompt"
  printf 'backgrounded · %s\n' "$la_short"
}

wake() {
  wk_sid="$1"
  shift
  printf '%s' "$(pwd -P)" > "$state/last-wake-cwd"
  wk_prompt=$(prompt_of "$@")
  wk_short=''
  for wk_d in "$sessions"/*; do
    [ -d "$wk_d" ] || continue
    [ "$(cat "$wk_d/sid")" = "$wk_sid" ] && wk_short=$(basename "$wk_d")
  done

  if [ -z "$wk_short" ]; then
    # No job entry: Claude Code starts a worker on the conversation, under
    # its own id, with the options this resume passed.
    wk_short=$(printf '%s' "$wk_sid" | cut -c1-8)
    new_session "$wk_short" "$wk_sid" "$(pwd -P)" "$(flags_json "$@")"
    write_job "$wk_short"
    transcript "$(pwd -P)" "$wk_sid" "$wk_prompt"
    printf 'backgrounded · %s\n' "$wk_short"
    return 0
  fi

  wk_dir="$sessions/$wk_short"
  wk_copy=0
  if [ "$#" -ne 2 ] || [ "$1" != -- ]; then wk_copy=1; fi
  wk_ver=$(knob VERSION)
  ver_lt "${wk_ver:-2.1.314}" 2.1.314 && wk_copy=1
  [ "$(knob ALWAYS_COPY)" = 1 ] && wk_copy=1
  shows_process "$wk_dir" && wk_copy=1
  wk_cwd=$(cat "$wk_dir/cwd")

  if [ "$wk_copy" = 1 ]; then
    wk_nsid=$(mint)
    wk_nshort=$(printf '%s' "$wk_nsid" | cut -c1-8)
    new_session "$wk_nshort" "$wk_nsid" "$wk_cwd" "$(cat "$wk_dir/respawn")"
    write_job "$wk_nshort"
    transcript "$wk_cwd" "$wk_nsid" "$wk_prompt"
    printf 'Session %s could not be continued; started a copy as %s\n' "$wk_short" "$wk_nshort"
    return 0
  fi

  transcript "$wk_cwd" "$wk_sid" "$wk_prompt"
  case "$(knob WAKE)" in
    dead) ;;
    vanish) : > "$wk_dir/hidden" ;;
    *)
      printf 1 > "$wk_dir/alive"
      printf idle > "$wk_dir/status"
      ;;
  esac
  printf idle > "$wk_dir/tempo"
  if [ -s "$wk_dir/extra" ]; then printf working > "$wk_dir/state"; else printf done > "$wk_dir/state"; fi
  write_job "$wk_short"
  printf 'backgrounded · %s\n' "$wk_short"
}

# check_short refuses an id that can't name a session directory.
check_short() {
  case "$1" in
    '' | */* | .* | -*)
      echo "stub claude: bad id" >&2
      exit 1
      ;;
  esac
}

case "${1:-}" in
  --version)
    ve=$(knob VERSION)
    printf '%s (Claude Code)\n' "${ve:-2.1.314}"
    ;;
  agents)
    list
    ;;
  stop)
    check_short "${2:-}"
    if [ "$(knob STOP_FAIL)" = 1 ]; then
      echo "stub claude: stop failed" >&2
      exit 1
    fi
    st_d="$sessions/$2"
    if [ ! -d "$st_d" ]; then
      echo "No job matching $2" >&2
      exit 1
    fi
    if [ "$(cat "$st_d/alive")" = 1 ]; then
      printf 0 > "$st_d/alive"
      st_n=$(knob SETTLE_LISTINGS)
      printf '%s' "${st_n:-2}" > "$st_d/settle"
    fi
    echo "stopped $2"
    ;;
  rm)
    check_short "${2:-}"
    if [ "$(knob RM_FAIL)" = 1 ]; then
      echo "stub claude: rm failed" >&2
      exit 1
    fi
    rm_d="$sessions/$2"
    if [ ! -d "$rm_d" ]; then
      echo "No job matching $2" >&2
      exit 1
    fi
    if shows_process "$rm_d"; then
      echo "stub claude: session $2 is running; stop it first" >&2
      exit 1
    fi
    rm -rf "$rm_d" "$jobs/$2"
    echo "removed $2"
    ;;
  attach | logs)
    ;;
  --bg)
    shift
    if [ "${1:-}" = --resume ]; then
      wk_arg="${2:-}"
      shift 2
      wake "$wk_arg" "$@"
    else
      launch "$@"
    fi
    ;;
  *)
    echo "stub claude: unsupported invocation" >&2
    exit 1
    ;;
esac
