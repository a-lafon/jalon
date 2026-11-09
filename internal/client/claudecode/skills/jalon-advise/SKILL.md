---
name: jalon-advise
description: Jalon. Give the reset advice for this conversation now, at any size.
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-advise` to get Jalon's reset advice for this conversation now, whatever its size.

Jalon's prompt hook answers this command with an instruction that starts `Jalon: the user typed /jalon-advise`: it describes the section to give and gives the handoff's path and first lines, which only the hook knows. Follow that instruction.

When no such instruction came with this prompt, Jalon's hooks are not installed or the hook failed: say so in one line, suggest `jalon init`, and write no file.
