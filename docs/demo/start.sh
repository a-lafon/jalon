#!/bin/zsh
# Starts the demo session, as the tape types it: none of your settings, plugins or
# CLAUDE.md, only the project's hooks, with the Jalon setup.sh built.
here=${0:A:h}
dir=${1:-$HOME/todo-api}
for v in ${(k)parameters[(I)CLAUDE*]}; do unset $v; done
export PATH=$here/.bin:$PATH
pkill -f "$here/watch.sh"
"$here/watch.sh" "$dir" >/dev/null 2>&1 &!
cd "$dir" && exec claude --setting-sources project --model sonnet --permission-mode acceptEdits \
  --allowedTools "Bash,Read,Edit,Write,Glob,Grep" \
  --settings "{\"claudeMdExcludes\":[\"$HOME/.claude/CLAUDE.md\"]}"
