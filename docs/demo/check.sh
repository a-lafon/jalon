#!/bin/sh
# Checks a take from its transcripts, before its GIF is used: scene 1 ends with a
# Jalon section and a /compact line, that very line is what the tape pasted, scene 2
# ends with a handoff, and /jalon-resume ran in a new conversation.
dir=${1:-$HOME/todo-api}
transcripts="$HOME/.claude/projects/$(printf '%s' "$dir" | sed 's/[^A-Za-z0-9]/-/g')"
texts() { jq -r 'select(.type=="assistant") | .message.content[]? | select(.type=="text") | .text' "$@"; }
prompts() { jq -r 'select(.type=="user") | .message.content | if type=="string" then . else empty end' "$@"; }
fail=0
check() { if [ "$1" -gt 0 ]; then echo "ok   $2"; else echo "FAIL $2"; fail=1; fi; }

first=$(ls -tr "$transcripts"/*.jsonl | head -1)
line=$(texts "$first" | grep -E '^[> `]*/compact ' | head -1 | sed -E 's/^[> `]*//; s/`*[[:space:]]*$//')
check "$(texts "$first" | grep -c 'Jalon\*\* — .*compact')" "scene 1 recommends a compact"
check "${#line}" "scene 1 gives a /compact line"
check "$(prompts "$first" | grep -cF -- "<command-args>${line#/compact }</command-args>")" "the tape pasted that line"
check "$(texts "$first" | grep -c 'Jalon\*\* — .*handoff')" "scene 2 recommends a handoff"
check "$(ls "$dir"/.jalon/handoffs/*/*.md 2>/dev/null | wc -l)" "a handoff file was written"
check "$(cat "$transcripts"/*.jsonl | grep -c '<command-name>/jalon-resume</command-name>')" "/jalon-resume ran"
exit $fail
