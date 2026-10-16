# Handoffs

A handoff is a file the session's own model writes, in the language you write in, when you take the advice to carry a task over to a new conversation: the goal, the decisions and why, what was dropped and why, the current state, split into what was checked, with the command or test run and its result in a few words, and what was not (ideas, assumptions, fixes not yet tested), your latest request and the answer that reply gives it, the files involved, the next steps and the pitfalls, and the facts the next steps need from outside sources such as tickets, docs or logs, since a new session cannot see them. No raw tool output. The next steps are written as proposals to confirm with you, not orders: a push, a pull or merge request, an API call or a message to a person or another session is never listed as a thing to do, at most as waiting for your OK. You can read and edit it before you resume. It is written in the reply to the prompt that crossed the threshold: a message you send after it is not in it, so ask the model to update the handoff first, or add that request yourself after the resume line Jalon gives, which names none.

## Where they go

Handoffs go to `.jalon/handoffs/` at the root of the git repository, or in the directory where the session started when there is none, one folder per day. A session started in your home directory, outside a repository, therefore keeps its handoffs in `~/.jalon/handoffs/`, next to Jalon's own files:

```
.jalon/handoffs/2026-10-12/0900-r4sefxw4-advisor.md
```

The name is `<time>-<thread>-<slug>`: the time of the advice, the thread ID, and a slug the model picks. Jalon creates `.jalon/handoffs/` and `.jalon/.gitignore`, which holds `*`, so nothing there is ever committed. The day folder is made by the model when it writes a handoff, so an advice you do not take leaves no empty folder. It never modifies an existing file in your project.

## Resuming

The advice gives you the lines to copy: `/clear`, then `@` and the handoff's full path, followed by a request to sum it up in three lines, keeping what was not checked marked as not checked, and wait for yours, without checking again the state it describes, and naming no task or next step. So the new session starts from the handoff, not from a task, and does not spend requests redoing what the handoff already says. To save a turn, add your own request after the `@` line. When the skill that `jalon init --skill` installs is there, the line is `/jalon resume` and the handoff's full path instead. In a session driven by a skill or command, the resume line re-invokes it, followed by the handoff's path as plain text and the same request. You can also resume in a new window.

Measured on four real handoffs, two runs each, before the line asked to keep what was not checked marked as such, with Claude Opus 5.5 in `claude -p` in a copy of the repository where only reading files was allowed:

| Resume line | Tool calls before the answer | Cost of the run |
|---|---|---|
| `@` path and a request to resume from it, as 0.1.0 asked | 1 to 13, 7.5 on average, mostly git and test commands | $0.32 on average |
| `@` path and a request to sum it up in three lines and wait | none in the 8 runs: it summed up and waited | $0.17 on average |

In real sessions where the commands could run, the short line of 0.1.0 led to 101 and 104 tool calls before the user's second message. Eight runs per line, from my own handoffs with one model: a hint, not a proof.

With the skill, `/jalon resume` finds the latest handoff of the project itself, or `/jalon resume <words>` the latest whose name or title holds them, or `/jalon resume <path>` that file. It names the handoff it picked and the two before it, sums up its goal, state and next step, then asks before going on, unless your words ask it to: `/jalon resume and go on`. It does not run commands to check the state the handoff describes unless your words ask for it. A push, a pull request, an API call or a message the handoff lists still waits for your OK.

## Threads

Threads link handoffs that continue one another. Jalon spots a resume from the handoff's path in the first prompt you type in a session, and a second handoff in the same session from the name it proposed at the last advice. A path in any later prompt of the session changes nothing.

Handoffs of one thread share an eight-character ID, so this lists them in order:

```sh
ls .jalon/handoffs/*/*-<thread>-*
```

Each handoff starts with `Follows: <parent>`, except the first of a thread, then `Session: <session ID>`, which names the conversation's transcript. The parent is a path relative to `.jalon/handoffs/`. A handoff with the date in its file name and no day folder, `2026-10-09-1358-r4sefxw4-advisor.md`, is recognized too.
