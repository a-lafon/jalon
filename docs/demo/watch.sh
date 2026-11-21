#!/bin/sh
# Copies the /compact line of the session's latest Jalon section to the clipboard,
# where the tape's Paste takes it, since VHS cannot read it off the screen.
# Stops after 15 minutes. macOS only (pbcopy).
dir=${1:-$HOME/todo-api}
transcripts="$HOME/.claude/projects/$(printf '%s' "$dir" | sed 's/[^A-Za-z0-9]/-/g')"
# Empty it first: when no advice came, the tape then pastes nothing rather than
# whatever you had copied.
: | pbcopy
last=""
i=0
while [ "$i" -lt 900 ]; do
  f=$(ls -t "$transcripts"/*.jsonl 2>/dev/null | head -1)
  if [ -n "$f" ]; then
    line=$(jq -r 'select(.type=="assistant") | .message.content[]? | select(.type=="text") | .text' "$f" |
      grep -E '^[> `]*/compact ' | tail -1 | sed -E 's/^[> `]*//; s/`*[[:space:]]*$//')
    if [ -n "$line" ] && [ "$line" != "$last" ]; then
      printf '%s' "$line" | pbcopy
      last=$line
    fi
  fi
  i=$((i + 1))
  sleep 1
done
