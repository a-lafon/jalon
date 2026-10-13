<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/wordmark-dark.svg">
    <img alt="Jalon" src="assets/wordmark.svg" width="280">
  </picture>
</p>

**A context advisor for AI coding agents.**

Jalon tells you when a long conversation is worth resetting, and has the session's own model prepare the reset: a new session, a handoff file to resume from, or a `/compact` line that names what to keep. One binary, two hooks, no daemon, no network.

> **Status: pre-alpha.** Tested with Claude Code on macOS.

A coding agent resends its whole conversation on every request, and most of what you pay for, or burn from your quota, is that context being read again. Long conversations can also lose track of earlier decisions. Resetting on time, without losing what matters, saves tokens and keeps the agent sharp: see [cost and quality](docs/cost-and-quality.md). The client cannot tell you when; Jalon can.

## What you see

When a conversation's context has grown by 200K tokens above its starting size, and again after every further 150K, the model ends its answer with one recommendation, in your language, in a quoted block that starts with **Jalon**: a new session, a handoff to resume from, or a `/compact` line. You run the command; Jalon never does. In a terminal you also get a one-line notice, and the status line shows a pole whose three bands fill as the context grows, full when a reset is advised: `jalon ▰▰▱ 160K`. A Claude Code mod can draw the same pole above the prompt instead, in the terminal and in Claude Desktop ([the pole above the prompt](docs/how-it-works.md#the-pole-above-the-prompt)).

<img alt="The status line in a Claude Code session: the pole fills as the context grows, then says reset advised" src="assets/statusline.png" width="320">

*A real Claude Code session, with `advise_tokens` lowered to 15K so the pole fills in a few prompts.*

See [how it works](docs/how-it-works.md) and [handoffs](docs/handoffs.md).

## Install

With [Homebrew](https://brew.sh) on macOS:

```sh
brew install a-lafon/tap/jalon
jalon init    # adds the hooks and the status line to ~/.claude/settings.json
```

For Linux and the other ways, see [installing Jalon](docs/install.md).

To draw the pole above the prompt instead of in the status line, with Claude Code 2.1.292 or later, run `jalon init --mod`: see [the pole above the prompt](docs/how-it-works.md#the-pole-above-the-prompt) for its limits.

Then start a new Claude Code session, in the terminal or in VS Code, and work as usual. `jalon init --dry-run` shows what it would change; see [configuration](docs/configuration.md) for its flags.

## Update

Jalon makes no network call, so it does not check for updates: run `brew outdated`, or watch the [releases](https://github.com/a-lafon/jalon/releases). Then `brew upgrade jalon`. The hooks keep working, but an upgrade from 0.1.0 needs `jalon init` once more, to add the stop hook; it keeps the mod if you use it. Other install methods update as described in [installing Jalon](docs/install.md#update).

## What Jalon touches

Jalon advises; you act. It never:

- **runs a command**: you type `/clear`, `/compact` or the resume line yourself;
- **rewrites anything** in a prompt, the conversation or a tool output;
- **sends anything anywhere**: it makes no network call, and reads your transcripts on your machine;
- **moves, edits or deletes a handoff**.

It writes only:

- **the advice**, added to the model's context by the prompt hook, on a prompt you typed, when the threshold is crossed; or by the stop hook at the end of a turn you started, which makes the model go on once to add the section: when the threshold was crossed during the turn, or when the reply left out the section asked for. Only a session you typed in gets it: a session a program started with `claude -p` does not, but a program that resumes a session you typed in is not detected by the prompt hook;
- **`.jalon/handoffs/`** in your project, with its `.gitignore`; the model writes the handoff files when you take that option;
- **`~/.claude/settings.json`**, only when you run `jalon init`, with a `.jalon.bak` backup;
- **`~/.jalon/`**: its session state, and its config when `jalon init` sets a threshold.

The one exception is the mod, when you ask for it: `jalon init --mod` runs Claude Code's `claude plugin install`, which downloads the mod from GitHub and records it in `~/.claude/settings.json`, and `jalon init --uninstall` runs `claude plugin marketplace remove jalon`. The mod itself only runs `jalon statusline` and draws its answer.

## Uninstall

Remove the hooks first, then the binary: `jalon init --uninstall` removes the hooks, the status line and the mod it installed, and nothing else. Then `brew uninstall jalon`, or delete the binary. Delete `~/.jalon/` to remove its state and config. Handoff files are yours, in your projects under `.jalon/handoffs/`.

## Documentation

- [Installing Jalon](docs/install.md): Homebrew, the release archives, `go install`, checksums, build provenance, macOS quarantine.
- [How it works](docs/how-it-works.md): the thresholds, the hooks, the status line, other clients.
- [Cost and quality](docs/cost-and-quality.md): why a large context costs, and what a replay of real conversations showed.
- [Handoffs](docs/handoffs.md): the folder, the threads, resuming.
- [Impact report](docs/impact-report.md): `jalon context-report`, to check the thresholds against your own history.
- [Configuration](docs/configuration.md): the `context` keys, `jalon init`, installing by hand, the files Jalon keeps.

## Development

```sh
gofmt -l .              # lists unformatted files; gofmt -w . fixes them
go vet ./...
go test -race ./...
golangci-lint run       # config in .golangci.yml
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

CI runs the same checks on every push to `main` and on every pull request.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE)
