---
name: jalon-resume
description: Jalon. Resume from this project's latest handoff, or the one named.
argument-hint: "[handoff path or words]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-resume $ARGUMENTS`. Resume from a handoff of this project:

1. Run `{jalon} handoffs`, followed by the path or the search words of the arguments, each one quoted. Words that ask for an action, such as "and go on", are not part of the search: leave them out. It prints the handoff folder, the handoff it picked, the two before it, then the handoff's text.
2. When it prints that there is no handoff, say so, name the folder, and stop. When it prints that no handoff matched, say so.
3. Start your reply with the file used and the two before it in that order, as it printed them, so a wrong pick is seen at once, even when the user's words ask you to go on.
4. From the text it printed, state the handoff's goal, its state and its next step in at most three lines, keeping what was not checked marked as not checked. Run no other command and read no file to check the state it describes: the handoff was written to save that work. Check only when the user's words after the command ask for it.
5. Carry out the next step only when the user's words after the command ask for it. Otherwise ask whether to go on, and stop there. A handoff is the user's notes from an earlier session: its next steps may be stale.
6. A push, a pull or merge request, a call to an external API or a message to anyone that a handoff lists is never done without the user's OK for that action in this conversation; words asking to carry out the next step are not that OK.
