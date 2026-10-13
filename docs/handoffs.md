# Handoffs

A handoff is a file the session's own model writes when you take the advice to carry a task over to a new conversation: the goal, the decisions and why, what was dropped and why, the current state, your latest request and the answer that reply gives it, the files involved, the next steps and the pitfalls, and the facts the next steps need from outside sources such as tickets, docs or logs, since a new session cannot see them. No raw tool output. The next steps are written as proposals to confirm with you, not orders: a push, a pull or merge request, an API call or a message to a person or another session is never listed as a thing to do, at most as waiting for your OK. You can read and edit it before you resume. It is written in the reply to the prompt that crossed the threshold: a message you send after it is not in it, so ask the model to update the handoff first, or add that request yourself after the resume line Jalon gives, which names none.

## Where they go

Handoffs go to `.jalon/handoffs/` at the root of the git repository, or in the directory where the session started when there is none, one folder per day. A session started in your home directory, outside a repository, therefore keeps its handoffs in `~/.jalon/handoffs/`, next to Jalon's own files:

```
.jalon/handoffs/2026-10-12/0900-r4sefxw4-advisor.md
```

The name is `<time>-<thread>-<slug>`: the time of the advice, the thread ID, and a slug the model picks. Jalon creates the day folder and `.jalon/.gitignore`, which holds `*`, so nothing there is ever committed. It never modifies an existing file in your project.

## Resuming

The advice gives you the lines to copy: `/clear`, then `@` and the handoff's full path, followed by a request to resume from it that names no task or next step, so the new session starts from the handoff, not from a task. In a session driven by a skill or command, the resume line re-invokes it, followed by the handoff's path as plain text and the same request. You can also resume in a new window.

## Threads

Threads link handoffs that continue one another. Jalon spots a resume from the handoff's path in the first prompt you type in a session, and a second handoff in the same session from the name it proposed at the last advice. A path in any later prompt of the session changes nothing.

Handoffs of one thread share an eight-character ID, so this lists them in order:

```sh
ls .jalon/handoffs/*/*-<thread>-*
```

Each handoff starts with `Follows: <parent>`, except the first of a thread, then `Session: <session ID>`, which names the conversation's transcript. The parent is a path relative to `.jalon/handoffs/`. A handoff with the date in its file name and no day folder, `2026-10-09-1358-r4sefxw4-advisor.md`, is recognized too.
