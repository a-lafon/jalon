---
name: jalon-recap
description: Jalon. Sum up a whole thread of handoffs, from the latest one or the one named back to the first.
argument-hint: "[handoff path or words]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-recap $ARGUMENTS`. Sum up a thread of handoffs of this project:

1. Find the handoff folder and pick the handoff as `/jalon-resume` does: the project root is the nearest folder, from the working directory up, that holds `.git`, else the working directory; the handoffs are the `.md` files under `<root>/.jalon/handoffs/`, at any depth, ordered by their path relative to that folder, the last one being the latest. List them with your own tools and sort the paths yourself. An argument that is a path to a `.md` file in that folder picks that file; other words, the latest handoff whose file name or first heading contains them, ignoring case, else the latest one, saying that no handoff matched; no argument, the latest one. With no handoff, say so, name the folder, and stop.
2. Build the thread: a handoff's first lines may hold `Follows: <path>`, the handoff it continues, by its path relative to the handoff folder. From the picked handoff, follow each `Follows:` line to the file it names, until a handoff has none, or names a file that is not in the folder: then say that the thread goes on in a missing file.
3. Check the files' sizes with your own tools before reading them. When the thread's files total 100 KB or less, read all of them. Otherwise read the first handoff and the last five, and name the others, which you did not read.
4. Name the thread's first and last handoff and how many it holds. Then sum it up in at most 15 lines: the goal; the decisions, in order, each with its reason; what is checked now and what is not, as the latest handoff says; what is still open; the latest handoff's next step, as a proposal to confirm with the user. A handoff alone in its thread is summed up alone: say that it starts the thread.
5. Read nothing but the handoffs, run no command and check nothing the handoffs describe: they were written to save that work. Carry out nothing, unless the user's words after the command ask for it; a push, a pull or merge request, a call to an external API or a message to anyone always needs the user's OK for that action in this conversation.
