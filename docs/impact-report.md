# Impact report

`jalon context-report` checks the thresholds against your own history. It reads your Claude Code transcripts on your machine, offline, and prints a report; nothing is sent anywhere and nothing is written.

```sh
jalon context-report [--since 7d] [--transcripts dir] [--split t1,t2] [--stop-hook=false] [--config path]
```

- `--since` takes a Go duration (`90m`, `24h`) or days (`7d`); the default is 7 days.
- `--transcripts` is the folder to read, `~/.claude/projects` by default. It reads `<folder>/<project>/<session>.jsonl` and `<folder>/<session>.jsonl`, so one project's folder, such as `~/.claude/projects/-Users-me-work-app`, works too. The command fails, naming it, when it cannot be read, names the two layouts when it finds no file, and says so when files exist but none was modified in the window.
- `--split` takes times (`2026-10-12T09:00` or `2026-10-13`, local time) that cut the window into periods, printed one after the other, so you can compare before and after a change of thresholds.
- `--stop-hook=false` replays the prompt hook alone, as release 0.1.0 advised, so you can compare it with the current release on the same transcripts.

## Impact

The report opens with an Impact section, read from the transcripts alone, for the conversations in which you typed a prompt. It lists the advice the hook gave and what followed each one: a compaction, a resume from a handoff, or nothing. For each reset it shows the context before and after, and estimates the prompt tokens the following requests did not re-read: (before − after) × the number of requests until the next reset.

It prints the total and, apart, the part from resets that followed advice, with its share of the same base (read plus not re-read). The estimate assumes the conversation would have gone on the same way, counts input, cache read and cache write together, and is never a cost or a quota.

A handoff resume is linked to the conversation that wrote the handoff, or else to the one its `Session:` line names. The report cannot see a `/clear` or a new session started without a handoff: nothing links them to the conversation they replace. Nor does it link a resume with `/jalon resume`, whose prompt holds no handoff path.

## The simulation

For each conversation, the report replays the advisor with your current thresholds, with each conversation's starting size, and compares its advice with the resets you did: your compactions and your handoffs. It replays the advice only at the turns the prompt hook acts on: a prompt you typed, in a conversation where you had typed before. A background task's report, a message from another session or a prompt sent before your first one gets none, as with the hook; a conversation a program drove alone (`claude -p`) is left out. Like the hook, it cannot tell a program's prompt from yours once you have typed in the conversation. The lead still counts every turn. It also replays the advice the stop hook gives at the end of a turn whose prompt you typed, when the prompt hook did not advise at its start: on the size of the turn's request before its last one, the size the hook reads, since the transcript does not hold the last one yet. Such an advice counts like one at the next prompt, which then does not repeat it, and the report prints how many advices fall at the end of a turn.

For each reset it prints how many turns before it the advice would have come (the lead), or `crossed mid-turn` when the context grew past the threshold where no hook would advise: at a turn's last request, at a turn no hook acts on, or inside a turn with `--stop-hook=false`; or `no advice`. It also prints the compactions with their size, the starting sizes, and the share of the context taken by stale tool results at each advised point.

"Extra prompt tokens after the advice" is the sum, over the requests from the first simulated advice to the reset, of the prompt tokens above the size at that advice: roughly what resetting at the advice would have avoided reading.

The starting size of a conversation is the size of its first request: the instructions, memory and tools the client loads, and your first prompt. The report prints its distribution, the share of the prompt tokens read by the conversations that started in the period that was this starting size, which no reset can remove (the Impact section's total also counts the conversations that started earlier), and one line per project: its working directory (or its folder's name), the number of conversations, the p50 and max of their starting size and that share, largest p50 first, at most 10 lines. A project whose starting size stands out is one whose instructions are worth trimming; `--split` shows whether a change lowered it.

The last section of each period covers the subagents, which the hooks do not see: each runs in its own context, kept in `<session>/subagents/` next to the session file. For the subagents that started in the period, of every conversation, it prints the prompt tokens they read and their share of all the prompt tokens read in the period, the share of their first requests, how many grew past the advice threshold above their first request and what share they read, and the five that read the most, with their agent type, requests, first and largest request. A subagent that went past the threshold had a task large enough to be worth splitting.

Once the hook advises, you reset at the threshold then in force, so those resets cannot judge a higher one; the report marks the resets that followed an advice the hook gave.
