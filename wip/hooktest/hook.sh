#!/bin/sh
cat >/dev/null
case "$MODE" in
  json0)  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}'; exit 0 ;;
  json1)  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}'; echo "stderr-line-one" >&2; echo "stderr-line-two" >&2; exit 1 ;;
  bare1)  echo "stderr-line-one" >&2; echo "stderr-line-two" >&2; exit 1 ;;
  text0)  printf '%s\n' 'MARKERALPHA the secret codeword is ZEBRAFISH'; exit 0 ;;
  halt0) printf '%s\n' '{"continue":false,"stopReason":"STOPDELTA provisioning failed"}'; exit 0 ;;
  json0err) printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA ok"}}'; echo "STDERRGAMMA warning: recommended env key not supplied" >&2; exit 0 ;;
  json1sys) printf '%s\n' '{"systemMessage":"SYSMSGBETA degraded provisioning","hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}'; echo "stderr-line-one" >&2; exit 1 ;;
  json0sys) printf '%s\n' '{"systemMessage":"SYSMSGBETA degraded provisioning","hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}'; exit 0 ;;
  json2)  printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}'; echo "stderr-exit2" >&2; exit 2 ;;
esac
