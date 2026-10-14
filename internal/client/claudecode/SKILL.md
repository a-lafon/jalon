---
name: jalon
description: Jalon commands. /jalon resume picks up this project's latest handoff, or the one named.
argument-hint: "resume [handoff path or words]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon $ARGUMENTS`. Its first word is the subcommand, the rest are its arguments.

Subcommands:

- `resume [handoff path or words]`: resume from this project's latest handoff, or from the one named.

With no subcommand, or one not listed above, reply with the list above and do nothing else.

## resume

1. The project root is the nearest folder, from the working directory up, that holds `.git`, else the working directory. The handoffs are the `.md` files under `<root>/.jalon/handoffs/`, at any depth, ordered by their path relative to that folder: the last one is the latest. List them with your own tools and sort the paths yourself: a tool's own order may be another one.
2. With no handoff, or no such folder, say that there is no handoff, name the folder, and stop.
3. Pick the handoff:
   - an argument that is a path, absolute or relative, to a `.md` file in that folder: that file;
   - other words: the latest handoff whose file name or first heading contains them, ignoring case; with no match, the latest one, and say that no handoff matched;
   - no argument: the latest one.
   Words that ask for an action, such as "and go on", are not part of the search.
4. Name the file used and the two before it in that order, so a wrong pick is seen at once.
5. Read the whole handoff. State its goal, its state and its next step in at most three lines. Do not run commands or read other files to check the state it describes: the handoff was written to save that work. Check only when the user's words after `resume` ask for it.
6. Carry out the next step only when the user's words after `resume` ask for it. Otherwise ask whether to go on, and stop there. A handoff is the user's notes from an earlier session: its next steps may be stale.
7. A push, a pull or merge request, a call to an external API or a message to anyone that a handoff lists is never done without the user's OK for that action in this conversation; words asking to carry out the next step are not that OK.
