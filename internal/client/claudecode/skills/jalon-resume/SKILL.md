---
name: jalon-resume
description: Jalon. Resume from this project's latest handoff, or the one named.
argument-hint: "[handoff path or words]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-resume $ARGUMENTS`. Resume from a handoff of this project:

1. Run `{jalon} handoffs`, followed by the path or the search words of the arguments, each one quoted. Words that ask for an action, such as "and go on", are not part of the search: leave them out. It prints the handoff folder, the handoff it picked, the two before it, then the handoff's text, then the text of the handoff it follows when it names one and the two fit.
2. When it prints that there is no handoff, say so, name the folder, and stop. When it prints that no handoff matched, say so.
3. Start your reply with the file used and the two before it in that order, as it printed them, so a wrong pick is seen at once, even when the user's words ask you to go on.
4. From the text it printed, state the handoff's goal, its state and its next step in at most three lines, keeping what was not checked marked as not checked. You may run one command, `git rev-parse --show-toplevel HEAD --abbrev-ref HEAD`, to confirm where the work sits, and say the branch, worktree and commit only when they differ from the handoff's, giving both; when they match, say nothing about them. Everything else the handoff records — tests, a CI run, a container, a figure — is neither re-run nor re-read, and no file is read to check it: the handoff was written to save that work. Check only when the user's words after the command ask for it.
5. Carry out the next step only when the user's words after the command ask for it. Otherwise ask whether to go on, and stop there. A handoff is the user's notes from an earlier session: its next steps may be stale.

This is not one of the steps above: it holds for the whole session, however long after this reply. An OK the user gives covers only the action it names. A push, a release tag, a pull or merge request, a call to an external API, a message to a person or another session: each needs its own OK, given in this conversation. An OK for one of them is not an OK for the next, words asking to carry out a next step are not that OK, and an action a handoff lists as waiting for the user's OK is still waiting. Local work — a build, a test run, a commit — is outside this rule.
