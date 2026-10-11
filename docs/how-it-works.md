# How it works

Jalon cannot reset a conversation itself, since editing history would break the prompt cache. So it tells you when, and the session's own model prepares how. There is no extra API call, and no conversation text leaves your machine.

## When it advises

The prompt hook reads the context size from the conversation's transcript, and the **starting size** (the floor) from the conversation's first request, once. The floor is what a reset cannot remove: the system prompt, the tool definitions (MCP included), the agent and skill listings. A conversation that starts at 220K is not advised at its first prompt.

- `advise_tokens` is the growth above the floor at which the first advice comes (200K by default).
- `advise_step_tokens` is how much the context must grow after an advice before the next one (150K by default). It counts from the size at the last advice.
- After a compaction or a new session the context is much smaller, and the step starts again.

The defaults are a judgment, not a measurement: the [replay test](cost-and-quality.md) backs resetting a large context, not these exact values. Check them against your own history with the [impact report](impact-report.md), and change them with `jalon init --advise-tokens` (see [configuration](configuration.md)).

The advice comes only on prompts you type, not on messages from other sessions or background tasks. The hook works in the terminal and in the VS Code extension, needs no other process, never blocks your prompt, and gives up after 500 ms.

## What you see

At the first prompt past the threshold, the terminal shows a one-line notice, `Jalon: context at 212K tokens, a reset proposal follows the answer.` (the VS Code extension does not show it), and the model is asked to answer your message first, then end its reply with one recommendation, in the language you write in, in a quoted block that starts with **Jalon** so you can tell where it comes from, and only in that reply:

1. a **new session**, when the task has changed;
2. a **handoff**: the model writes a file in `.jalon/handoffs/` and gives you the lines to resume from it (see [handoffs](handoffs.md));
3. a **`/compact` line** naming what to keep and what to drop.

You run the command; Jalon never does.

## Status line

`jalon init` adds it. It shows in the terminal only (the VS Code extension has none): a pole of three bands, then the size:

```
jalon ▱▱▱ 40K
jalon ▰▱▱ 95K
jalon ▰▰▱ 160K
jalon ▰▰▰ 230K · reset advised
```

A band fills at each third of `advise_tokens` of growth above the floor, so the pole is full, and `· reset advised` shown, by the hook's rule. It stays full after the first advice. The filled bands are red; set `NO_COLOR` to drop the color. With the advisor disabled, it shows the size alone: `jalon 230K`. It reads the floor from the state file the hook keeps for the session, and writes nothing; until the hook has run in a session, it counts from zero. It prints nothing when the size is unknown, and gives up after 200 ms.

`jalon init` sets it when you have none, and never replaces yours; `jalon init --statusline=false` leaves it out. To add it to an existing status line, pipe the same stdin to both commands in a small script and join their outputs:

```sh
#!/bin/sh
input=$(cat)
printf '%s  %s' "$(printf '%s' "$input" | ~/bin/my-status.sh)" "$(printf '%s' "$input" | jalon statusline --client claude-code)"
```

## The pole above the prompt

An alternative to the status line: a Claude Code mod, in `plugin/`, draws the same pole on a row of its own above the prompt, with `· reset advised` in red. It also shows in the Code tab of Claude Desktop; the VS Code chat panel does not draw mods, so it shows nothing there, and does not run the binary. Install it in place of the status line:

```
jalon init --mod
```

<img alt="The pole above the prompt in a Claude Code session, full, with reset advised in red" src="../assets/mod.png" width="640">

*A real Claude Code session, with `advise_tokens` lowered to 6K so the pole fills in a few prompts.*

The mod only draws. After each turn, and when a session starts or resumes, it runs `jalon statusline` with the context size Claude Code reports and draws its answer, so the rule stays the binary's and the mod writes nothing. It updates at the end of a turn, where the status line updates during it. It draws nothing when the binary is missing or fails, or when the advisor is disabled.

Prefer the status line unless you want the pole in Claude Desktop or on a row of its own:

- **Claude Code's mod API is early access** and may change between releases; a change can stop the mod from drawing until it is updated.
- **It needs Claude Code 2.1.292 or later**, and runs as code inside Claude Code, with your permissions; an organization can block mods it does not manage.
- **It does not update with Jalon.** Claude Code does not update mods from a marketplace like this one unless you turn auto-update on, in `/plugin`, Marketplaces. Otherwise run `claude plugin update jalon@jalon`.

`jalon init --uninstall` removes it with the rest; `jalon init --statusline` cannot be combined with `--mod`.

## Other clients

Everything specific to Claude Code sits in `internal/client/claudecode`, behind the `client.Adapter` interface in `internal/client/client.go`: how to read the hook input, find the context size, write the hook output, the reset commands, and, through the optional `client.Installer`, how to install the hook. To add a client, implement that interface in `internal/client/<name>/`, register it in `cmd/jalon/clients.go`, and install `jalon hook prompt --client <name>` as that client's prompt hook.
