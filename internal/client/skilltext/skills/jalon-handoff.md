The user typed `{/}jalon-handoff {args}` to get a handoff of this conversation now.

Jalon's prompt hook answers this command with an instruction that starts `Jalon: the user typed {/}jalon-handoff`: it gives the handoff's path and first lines, which only the hook knows. Follow that instruction.

When no such instruction came with this prompt, Jalon's hooks are not installed or the hook failed: say so in one line, suggest `{init}`, and write no file.
