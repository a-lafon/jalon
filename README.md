<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/wordmark-dark.svg">
    <img alt="Jalon" src="assets/wordmark.svg" width="280">
  </picture>
</p>

**A context advisor for Claude Code.**

Jalon tells you when a long conversation is worth resetting, and has the session's own model prepare the reset: a new session, a handoff file to resume from, or a `/compact` line that names what to keep. One binary, two hooks, no daemon, no network.

<p align="center"><img alt="A Claude Code session on a todo API. Asked why PATCH /todos/:id returns 500 since a deploy, the model finds the cause in the production log and ends with a quoted block starting with Jalon that recommends a compact, with a /compact line keeping the cause and dropping the log; the line is pasted and the conversation compacted. Asked to fix the bug and plan a move to SQLite for tomorrow, the model recommends a handoff, writes it and gives /clear and /jalon-resume; after them, the new conversation sums up the handoff in three points, and the status line pole drops from full to one band" src="assets/demo.gif" width="860"></p>

*A real Claude Code session (Sonnet 5.5) on a small todo API, with `advise_tokens` lowered to 5K so the advice comes within a few prompts, and the model's work cut out. After the production log has been read, the same task goes on, so the model recommends a `/compact` that keeps the cause and drops the log. After a fix and a plan to build tomorrow, it writes a handoff, and `/jalon-resume` picks it up in a new conversation.*

> **Status: early.** Tested with Claude Code on macOS; the test suite also runs on Linux in CI. Expect changes between 0.x releases.

A coding agent resends its whole conversation on every request, so most of what you pay for, or burn from your quota, is that context being read again. Claude Code shows how full the context is, not when a reset is worth it; Jalon does.

- **A long context costs more per request**: in five days of my own sessions, a request at 400K tokens cost three and a half times one at 100K.
- **Resetting on time saves that re-reading**: over 30 days of my sessions, resets avoided about a third of it, an estimate.
- **It is about cost and usage limits, not better answers**: in my tests a long context answered as well as a reset one up to 560K tokens, and a reset lost exact details, which the advice asks to keep word for word.

See [cost and quality](docs/cost-and-quality.md).

## What you see

When a conversation's context has grown by 200K tokens above its starting size, and again after every further 60K, the model ends its answer with one recommendation, in your language, in a quoted block that starts with **Jalon**: a new session, a handoff to resume from, or a `/compact` line. You run the command; Jalon never does.

The advice also names the three largest tool results Jalon found, with the tool, what it acted on and a size, so the model says what its `/compact` line or its handoff drops. Those sizes are estimated from the text of the results, not measured token counts, and the list covers the recent context Jalon read, not the whole conversation.

In a terminal you also get a one-line notice, and the status line shows a pole whose three bands fill as the context grows, full when a reset is advised: `jalon ▰▰▱ 160K`. A Claude Code mod can draw the same pole above the prompt instead, in the terminal and in Claude Desktop ([the pole above the prompt](docs/how-it-works.md#the-pole-above-the-prompt)).

See [how it works](docs/how-it-works.md) and [handoffs](docs/handoffs.md), with [three handoffs to read](docs/handoffs.md#three-of-them-to-read).

## Install

On macOS or Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/a-lafon/jalon/main/install.sh | sh
jalon init    # adds the hooks and the status line to ~/.claude/settings.json, and the /jalon-* commands
```

The script puts `jalon` in `~/.local/bin` after checking it against the release's checksums; when that folder is not on your `PATH`, run `~/.local/bin/jalon init`. With [Homebrew](https://brew.sh): `brew install a-lafon/tap/jalon`. For the other ways, see [installing Jalon](docs/install.md).

To draw the pole above the prompt instead of in the status line, with Claude Code 2.1.292 or later, run `jalon init --mod`: see [the pole above the prompt](docs/how-it-works.md#the-pole-above-the-prompt) for its limits.

Then start a new Claude Code session, in the terminal or in VS Code, and work as usual. `jalon init --dry-run` shows what it would change; see [configuration](docs/configuration.md) for its flags.

When the stop hook makes the model go on, the terminal shows what it asked the model, labelled `Stop hook feedback`; this needs Claude Code 2.1.163 or later ([how it works](docs/how-it-works.md#what-you-see)).

## Commands

`jalon init` adds these commands to Claude Code, unless you run it with `--skill=false`. Only you invoke them: the model never runs one on its own.

| Command | What it does |
|---|---|
| `/jalon-resume [path or words]` | Resumes from this project's latest handoff, or the one named, rather than `@` and its path. |
| `/jalon-recap [path or words]` | Sums up a whole thread of handoffs, back to the first. |
| `/jalon-handoff [what to stress]` | Writes the handoff now, without asking what else could be done. |
| `/jalon-advise` | Gives the reset advice now, at any size: a new session, a handoff or a compact, whichever the conversation calls for. |
| `/jalon-report [flags]` | Runs `jalon context-report -summary` and judges it: what the resets saved, what held the late ones, what to change in how you work. |
| `/jalon-help` | Lists these commands. |

See [handoffs](docs/handoffs.md#resuming).

## How Jalon differs

- **From context gauges and budget warnings** ([ccstatusline](https://github.com/sirmalloc/ccstatusline), [claude-powerline](https://github.com/Owloops/claude-powerline), [claude-context-optimizer](https://github.com/egorfedorov/claude-context-optimizer) and the like), which show the context's size, its share of the window or its share of a token budget, and leave the reset to you: Jalon counts the growth above the conversation's starting size, so a session that starts heavy with MCP tools or a long `CLAUDE.md` is not advised earlier, and the model, which knows the task, picks one reset and prepares it.
- **From lowering the auto-compact threshold** (`/autocompact`): that moves the trigger, but a compaction keeps the summary a summarizer writes without knowing what comes next. In a replay of four of my conversations cut at about 300K, a handoff re-read about 1.6M tokens against about 3M for a plain `/compact`, counting the compaction requests; on answers, later tests found neither better, each losing different details ([cost and quality](docs/cost-and-quality.md)).
- **From handoff skills** (`/handoff` and the like), which you run when you think of it: Jalon tells you when, asks the handoff to separate what was checked from what was not, keeps it up to date until you reset, and gives a resume line that cost $0.17 a run against $0.32 for a plain "resume from it" ([handoffs](docs/handoffs.md#resuming)).
- **From handoff tools that act for you** ([claude-handoff](https://github.com/REMvisual/claude-handoff), [auto-handoff](https://github.com/obie/auto-handoff)): claude-handoff can replace the conversation with its handoff in place, and commits the handoff with the session's changes; auto-handoff writes a handoff on its own after 50 idle minutes. Jalon only advises: you type the reset, and it runs no command and commits nothing.
- **From compaction helpers** ([compact-plus](https://github.com/u-ichi/compact-plus)), which improve what a compaction keeps: Jalon does not touch the compaction; it advises before one.

Every figure here comes from my own sessions with one model: a hint, not a proof. `jalon context-report` replays the advice on your own transcripts, so you can check it against your history ([impact report](docs/impact-report.md)). Jalon makes no network call and no model call of its own: the advice, the handoff and its updates are requests of your own session, and the stop hook makes the model go on at most once per turn ([how it works](docs/how-it-works.md)).

## Update

Jalon makes no network call, so it does not check for updates: run `brew outdated`, or watch the [releases](https://github.com/a-lafon/jalon/releases). Then `brew upgrade jalon`. Some upgrades need `jalon init` once more: [installing Jalon](docs/install.md#update) says which, and how the other install methods update.

## What Jalon touches

Jalon advises; you act. It never:

- **runs a command**: you type `/clear`, `/compact` or the resume line yourself;
- **rewrites anything** in a prompt, the conversation or a tool output;
- **sends anything anywhere**: it makes no network call, and reads your transcripts on your machine;
- **moves, edits or deletes a handoff**.

What it reads from Claude Code's transcripts, and the few files it writes, are in [what Jalon reads and writes](docs/how-it-works.md#what-jalon-reads-and-writes).

## Uninstall

Remove the hooks first, then the binary: `jalon init --uninstall` removes the hooks, the status line, the mod and the skill it installed, and nothing else. Then `brew uninstall jalon`, or delete the binary. Delete `~/.jalon/` to remove its state and config. Handoff files are yours, in your projects under `.jalon/handoffs/`.

## Documentation

- [Installing Jalon](docs/install.md): Homebrew, the release archives, `go install`, checksums, build provenance, macOS quarantine.
- [How it works](docs/how-it-works.md): the thresholds, the hooks, the status line, what Jalon reads and writes, other clients.
- [Cost and quality](docs/cost-and-quality.md): why a large context costs, and what a replay of real conversations showed.
- [Handoffs](docs/handoffs.md): the folder, the threads, resuming.
- [Impact report](docs/impact-report.md): `jalon context-report`, to check the thresholds against your own history.
- [Configuration](docs/configuration.md): the `context` keys, `jalon init`, installing by hand, the files Jalon keeps.

## Development

The checks CI runs, the integration tests and the fuzzers are in [CONTRIBUTING.md](CONTRIBUTING.md); re-recording the demo above, in [docs/demo](docs/demo/README.md).

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
