# Impact report

`jalon context-report` checks the thresholds against your own history. It reads your Claude Code transcripts on your machine, offline, and prints a report; nothing is sent anywhere and nothing is written.

```sh
jalon context-report [--since 7d] [--transcripts dir] [--split t1,t2] [--config path]
```

- `--since` takes a Go duration (`90m`, `24h`) or days (`7d`); the default is 7 days.
- `--transcripts` is the folder to read, `~/.claude/projects` by default. The command fails, naming it, when it cannot be read.
- `--split` takes times (`2026-10-12T09:00` or `2026-10-13`, local time) that cut the window into periods, printed one after the other, so you can compare before and after a change of thresholds.

## Impact

The report opens with an Impact section, read from the transcripts alone, for the conversations in which you typed a prompt. It lists the advice the hook gave and what followed each one: a compaction, a resume from a handoff, or nothing. For each reset it shows the context before and after, and estimates the prompt tokens the following requests did not re-read: (before − after) × the number of requests until the next reset.

It prints the total and, apart, the part from resets that followed advice, with its share of the same base (read plus not re-read). The estimate assumes the conversation would have gone on the same way, counts input, cache read and cache write together, and is never a cost or a quota.

A handoff resume is linked to the conversation that wrote the handoff, or else to the one its `Session:` line names. The report cannot see a `/clear` or a new session started without a handoff: nothing links them to the conversation they replace.

## The simulation

For each conversation, the report replays the advisor with your current thresholds, with each conversation's starting size, and compares its advice with the resets you did: your compactions and your handoffs. For each reset it prints how many turns before it the advice would have come (the lead), or `crossed mid-turn` when the context grew past the threshold inside one turn's tool loop, or `no advice`. It also prints the compactions with their size, the starting sizes, and the share of the context taken by stale tool results at each advised point.

"Extra prompt tokens after the advice" is the sum, over the requests from the first simulated advice to the reset, of the prompt tokens above the size at that advice: roughly what resetting at the advice would have avoided reading.

Once the hook advises, you reset at the threshold then in force, so those resets cannot judge a higher one; the report marks the resets that followed an advice the hook gave.
