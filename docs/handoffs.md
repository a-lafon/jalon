# Handoffs

A handoff is a file the session's own model writes, in the language you write in, when you take the advice to carry a task over to a new conversation: the goal, the decisions and why, what was dropped and why, with every item the handoff it follows left open and this one does not carry, one line each, as done, abandoned or moot and why, the current state, split into what was checked, with the command or test run and its result in a few words, and what was not (ideas, assumptions, fixes not yet tested), your latest request and the answer that reply gives it, the files involved, the next steps and the pitfalls, and the facts the next steps need from outside sources such as tickets, docs or logs, since a new session cannot see them. No raw tool output. The current state gives the branch, the worktree and the base commit as the repository reports them when the handoff is written, never copied from the handoff it follows, and each pitfall and each "do not redo" names the branch, commit or directory it was verified on: one inherited from the parent is repeated only after checking it still holds, else the handoff says what changed. A path outside the project, a session scratchpad, a container or a port comes with the fact it establishes and the way to obtain that fact again, since none of them may still exist. The next steps are written as proposals to confirm with you, not orders: a push, a pull or merge request, an API call or a message to a person or another session is never listed as a thing to do, at most as waiting for your OK, and that step repeats the constraints on what the action may say. You can read and edit it before you resume. It is written in the reply to the prompt that crossed the threshold. Each prompt you type after it in the same conversation carries a short note to the model, which you do not see: if the turn changes what the handoff records, update it with a short edit. So work done after the advice reaches the handoff too. The note stops after a compaction, and at the next advice, which proposes a new handoff. Each update is one more request on the large context, made only when the turn changed something.

## Where they go

Handoffs go to `.jalon/handoffs/` at the root of the git repository, or in the directory where the session started when there is none, one folder per day. A session started in your home directory, outside a repository, therefore keeps its handoffs in `~/.jalon/handoffs/`, next to Jalon's own files:

```
.jalon/handoffs/2026-10-12/0900-r4sefxw4-advisor.md
```

The name is `<time>-<thread>-<slug>`: the time of the advice, the thread ID, and a slug the model picks. Jalon creates `.jalon/handoffs/` and `.jalon/.gitignore`, which holds `*`, so nothing there is ever committed. The day folder is made by the model when it writes a handoff, so an advice you do not take leaves no empty folder. It never modifies an existing file in your project.

## Resuming

The advice gives you the lines to copy: `/clear`, then `@` and the handoff's full path, followed by a request to sum it up in three lines, keeping what was not checked marked as not checked, and wait for yours, without checking again the state it describes, and naming no task or next step. So the new session starts from the handoff, not from a task, and does not spend requests redoing what the handoff already says. To save a turn, add your own request after the `@` line. When the skills that `jalon init` installs are there, the line is `/jalon-resume` and the handoff's full path instead. In a session driven by a skill or command, the resume line re-invokes it, followed by the handoff's path as plain text and the same request. You can also resume in a new window.

Measured on four real handoffs, two runs each, before the line asked to keep what was not checked marked as such, with Claude Opus 5.5 in `claude -p` in a copy of the repository where only reading files was allowed:

| Resume line | Tool calls before the answer | Cost of the run |
|---|---|---|
| `@` path and a request to resume from it, as 0.1.0 asked | 1 to 13, 7.5 on average, mostly git and test commands | $0.32 on average |
| `@` path and a request to sum it up in three lines and wait | none in the 8 runs: it summed up and waited | $0.17 on average |

In real sessions where the commands could run, the short line of 0.1.0 led to 101 and 104 tool calls before the user's second message. Eight runs per line, from my own handoffs with one model: a hint, not a proof.

Both commands below run `jalon handoffs`, which finds the project's handoff folder, picks the handoff and follows the thread in the binary, then prints them with their text: the model does not list, sort or count files, and runs almost no other command.

With the skills, `/jalon-resume` finds the latest handoff of the project itself, or `/jalon-resume <words>` the latest whose name or title holds them, or `/jalon-resume <path>` that file. It names the handoff it picked and the two before it, sums up its goal, state and next step, then asks before going on, unless your words ask it to: `/jalon-resume and go on`. When the handoff it picked has a `Follows:` line, `jalon handoffs` prints the parent handoff's text after it, named as the one it follows, so the resume reads the generation before it without opening the file; a parent that is missing from the folder, or that would take the two over the 100 KB bound `-thread` uses, is named and left out instead.

Its one allowed check is a single `git rev-parse` of the branch, worktree and commit, reported only when they differ from the handoff's; it does not run commands to check anything else the handoff describes unless your words ask for it. Your OK covers the one action it names and nothing else, for the whole session, not just the first reply: a push, a release tag, a pull or merge request, an API call, a message to a person or another session each waits for its own OK, and an action the handoff lists as waiting for your OK is still waiting. Builds, tests and commits are outside that rule.

`/jalon-recap` picks a handoff the same way, then follows its `Follows:` lines back to the first handoff of the thread, and sums up the whole thread in at most 15 lines: the goal, the decisions in order with their reason, what is checked now and what is not, what is still open, and the next step. `jalon handoffs -thread` prints every handoff of the thread when they total 100 KB or less, else the first and the last five, and names the ones it skipped; 100 KB is a bound above the longest thread measured (14 handoffs, 86 KB), not a measured limit. A `Follows:` line that names a missing file ends the thread there, and the command names that file.

## A handoff when you choose

`/jalon-handoff [what to stress]` asks for a handoff now, at any size, for example before a pause or at the end of a step. Jalon's prompt hook sees the command and gives the model the advice's own instruction, with the handoff's path and first lines, so it is written as an advised one would be, in the same thread; the model recommends the handoff and adds a `/compact` line in case you prefer compacting. When the conversation already wrote its handoff, at an advice or an earlier `/jalon-handoff`, even before a compaction, the model updates that file in place instead of writing a second one. It counts as an advice for what follows: the stop hook reminds once if the section is missing, the write-back note keeps the handoff up to date, and the next advice comes one step later. The context report does not count it as an advice. Without Jalon's hooks, the command says so and writes nothing.

## Threads

Threads link handoffs that continue one another. Jalon spots a resume from the handoff's path in the first prompt you type in a session, and a second handoff in the same session from the name it proposed at the last advice. A path in any later prompt of the session changes nothing.

Handoffs of one thread share an eight-character ID, so this lists them in order:

```sh
ls .jalon/handoffs/*/*-<thread>-*
```

Each handoff starts with `Follows: <parent>`, except the first of a thread, then `Session: <session ID>`, which names the conversation's transcript. The parent is a path relative to `.jalon/handoffs/`. A handoff with the date in its file name and no day folder, `2026-10-09-1358-r4sefxw4-advisor.md`, is recognized too.
