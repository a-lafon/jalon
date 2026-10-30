---
name: jalon-recap
description: Jalon. Sum up a whole thread of handoffs, from the latest one or the one named back to the first.
argument-hint: "[handoff path or words]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-recap $ARGUMENTS`. Sum up a thread of handoffs of this project:

1. Run `{jalon} handoffs -thread`, followed by the path or the search words of the arguments, each one quoted. It picks the handoff, follows its `Follows:` lines back to the first handoff, and prints the thread: how many handoffs it holds, its first and last, a missing file it goes on in, the handoffs it did not print, then the text of the others, first to last.
2. When it prints that there is no handoff, say so, name the folder, and stop. When it prints that no handoff matched, say so.
3. Give the thread's first and last handoff and how many it holds, as printed, and name the missing file and the handoffs not printed when it prints them. Then sum it up in a flat list of at most 15 lines, with no nested items and no heading followed by sub-items: the goal in one line; the decisions, in order, at most five lines, one decision and its reason per line, merging the minor ones; what is checked now and what is not, as the latest handoff says, in two lines; what is still open, at most three lines; the latest handoff's next step in one line, as a proposal to confirm with the user. A thread of one handoff is summed up alone: say that it starts the thread.
4. Run no other command and read no file, except the file your tools saved its output to when it was too long to show, and check nothing the handoffs describe: they were written to save that work. Carry out nothing, unless the user's words after the command ask for it.

This is not one of the steps above: it holds for the whole session, however long after this reply. An OK the user gives covers only the action it names. A push, a release tag, a pull or merge request, a call to an external API, a message to a person or another session: each needs its own OK, given in this conversation. An OK for one of them is not an OK for the next, words asking to carry out a next step are not that OK, and an action a handoff lists as waiting for the user's OK is still waiting. Local work — a build, a test run, a commit — is outside this rule.
