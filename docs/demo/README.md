# Recording the demo GIF

The GIF at the top of the [README](../../README.md) is a real Claude Code session, recorded with [VHS](https://github.com/charmbracelet/vhs). Record it again when the advice or the commands change, so that it shows what a user gets.

The session runs on a small todo API in TypeScript whose deploy this morning broke `PATCH /todos/:id`:

1. Asked to find why, the model reads the production log, finds the cause and, the threshold crossed, recommends a compact. The tape pastes its `/compact` line.
2. Asked to fix it and plan a move to SQLite to build tomorrow, the model recommends a handoff and writes it.
3. `/clear` and `/jalon-resume` start a new conversation from the handoff.

## What it needs

macOS (the clipboard goes through `pbcopy`), `vhs`, `ffmpeg`, `jq`, Go, Node.js with npm, and `claude` logged in. The session uses Sonnet, so a take costs a few requests of your plan or your API key.

## Steps

1. `docs/demo/setup.sh` creates the project in `~/todo-api`, the path the GIF shows: its code, three commits, the morning's log, and hooks that run Jalon built from this repository with the thresholds of [jalon.yaml](jalon.yaml), lowered to 5K so the advice comes within a few prompts.
2. The first time, run `docs/demo/start.sh` and answer Claude Code's folder trust prompt for `~/todo-api`, which the project's hooks trigger. Then quit.
3. From `docs/demo`, run `vhs demo.tape`. A take lasts about six minutes, most of them hidden waits, and writes `demo-raw.gif` and a screenshot of each scene's end, `s1.png` to `s4.png`.
4. Check the screenshots: the model chooses the reset, so scene 1 must show a compact and scene 3 a handoff with its `/clear` and `/jalon-resume` lines. Scene 1 can also end with no advice at all, when only the turn's last request crossed the threshold: the stop hook cannot see that request yet, and the advice would come at the next prompt ([how it works](../how-it-works.md#when-it-advises)). That happened in one run of four at 5K. If a screenshot shows the model still working, lengthen the hidden `Sleep` before it in the tape. Run `setup.sh` again before the next take.
5. Shrink the GIF into the README's: `ffmpeg -i demo-raw.gif -vf "fps=12,split[a][b];[a]palettegen=stats_mode=diff:max_colors=64[p];[b][p]paletteuse=dither=none:diff_mode=rectangle" ../../assets/demo.gif`. That took the last take from 4.2 MB to 3.0 MB with no visible loss.
6. If the README's caption no longer matches what the take shows, the model or the thresholds, update it.
7. `docs/demo/setup.sh --clean` removes `~/todo-api`, its transcripts and the state Jalon kept for its sessions.

## How the session is kept apart from yours

[start.sh](start.sh) starts Claude Code with `--setting-sources project`, so none of your settings, hooks or plugins load, excludes your `~/.claude/CLAUDE.md`, and unsets the `CLAUDE*` variables of a session that runs the recording. The project's `.claude/settings.json` holds Jalon's hooks, its status line and the `/jalon-*` commands, and turns commit attribution off, which the model otherwise copies into its `/compact` line.

VHS cannot read the `/compact` line off the screen, and its `Wait+Screen` does not read Claude Code's screen reliably. So [watch.sh](watch.sh), started by `start.sh`, copies the line from the session's transcript to the clipboard for the tape's `Paste`, and the tape waits with fixed sleeps. The watcher empties your clipboard when the session starts and overwrites it during a take, and stops after 15 minutes.

The app's code lives in [setup.sh](setup.sh) rather than as files, so that GitHub's dependency graph does not read a `package.json` here. [log.mjs](log.mjs) writes the log, the same for the same day.
