---
name: jalon-help
description: Jalon. List Jalon's commands.
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

Reply with this list, as is, and do nothing else: run no tool.

Jalon's commands in Claude Code:
- `/jalon-resume [path or words]`: resume from this project's latest handoff, or the one named.
- `/jalon-recap [path or words]`: sum up a whole thread of handoffs.
- `/jalon-handoff [what to stress]`: write a handoff of this conversation now.
- `/jalon-report [flags]`: run `jalon context-report` and explain it.
- `/jalon-help`: this list.

In the terminal:
- `jalon init`: install or update the hooks, the status line or mod, and these commands (`--skill`); `--uninstall` removes them.
- `jalon context-report`: what the advice did, from this machine's transcripts.
- `jalon statusline`: the context size, for the status line.
- `jalon version`: the installed version.
