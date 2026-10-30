# Impact report

`jalon context-report` checks the thresholds against your own history. It reads your Claude Code transcripts on your machine, offline, and prints a report; nothing is sent anywhere and nothing is written.

```sh
jalon context-report [--since 7d] [--transcripts dir] [--split t1,t2] [--stop-hook=false] [--summary] [--config path]
```

- `--since` takes a Go duration (`90m`, `24h`) or days (`7d`); the default is 7 days.
- `--transcripts` is the folder to read, `~/.claude/projects` by default. It reads `<folder>/<project>/<session>.jsonl` and `<folder>/<session>.jsonl`, so one project's folder, such as `~/.claude/projects/-Users-me-work-app`, works too. The command fails, naming it, when it cannot be read, names the two layouts when it finds no file, and says so when files exist but none was modified in the window.
- `--split` takes times (`2026-10-12T09:00` or `2026-10-13`, local time) that cut the window into periods, printed one after the other, so you can compare before and after a change of thresholds.
- `--stop-hook=false` replays the prompt hook alone, as release 0.1.0 advised, so you can compare it with the current release on the same transcripts.
- `--summary` prints the summary alone, without the sections it comes from. `/jalon-report` reads it.

## Summary

The report opens with a summary of the whole window, each line naming the section it comes from:

- **Saved**: the prompt tokens the resets kept from being read again, and the part of it from those that followed an advice, an estimate (see Impact).
- **Late**: the resets that came 10 user turns or more after the advice, at the current thresholds, and the prompt tokens their requests read past it, against those taken within one turn.
- **Ignored**: the advices no reset followed although the conversation went on, and, apart, those a conversation ended at, which nobody refused.
- **Never shown**: when there is at least one, the advices the hook gave that no reply ever gave the Jalon section for. Nobody saw them, so no reset is credited to them, whatever reset followed (see Impact).
- **Subagents**: those that grew past the threshold, which no hook advises.
- **Starting size**: the share of the prompt tokens read by the conversations that started in the window that was their starting size, and the project whose starting size accounts for the most of it.
- **Cold returns**: when there is at least one, the prompts you typed after a pause that found the cache expired, and the share of the prompt tokens read in the window that they sent without reading them from the cache (see Returns after a pause).
- **Late resets to reopen**: the three that read the most past the advice, with their working directory and `claude --resume <session>`, then the start of each prompt you typed between the advice and the reset, at most 15 for each, which shows what held it.
- **To do**: the actions these lines call for, the most direct first: take the reset when the advice comes, when some came late; give subagents smaller tasks, when those past the threshold read 25% or more of the subagents' prompt tokens; trim what the project whose starting size costs the most loads at start, when the starting size is 25% or more of the prompt tokens.

The summary names your projects and session IDs, and quotes the start of some of your prompts: check it before you paste a report anywhere.

## Impact

After the summary, the report has an Impact section, read from the transcripts alone, for the conversations in which you typed a prompt. It lists the advice the hook gave and what followed each one: a compaction, a resume from a handoff, or nothing. An advice is counted as shown when a reply after it holds a line starting with `> **Jalon**`, before the next advice or a compaction: the model is asked for that section in the reply the advice lands in, but a prompt you type while it answers pushes the reply into the next turn. An advice no reply gave the section for was never shown to you: it is counted as never shown, and the reset that followed it, if any, is counted as unprompted. The section also names the Jalon version that gave the advice, read from the session state, which is swept after a week: an advice given by a version that recorded none, or whose state is gone, is named unknown rather than attributed to the version installed now. The state holds one version per session, that of its last advice, so a session that spans an upgrade credits all of its advice to the build it ended on. For each reset it shows the context before and after, and estimates the prompt tokens the following requests did not re-read: (before − after) × the number of requests until the next reset.

It prints the prompt tokens read, which cover every request of the period in the conversations you typed in, and, apart, the part from resets that followed advice, with its share of the same base (read plus not re-read). Each total the report prints names the conversations and the window it covers, so two that differ are not a contradiction: the starting size section counts the conversations that started in the period, this one every request made in it. The estimate assumes the conversation would have gone on the same way, counts input, cache read and cache write together, and is never a cost or a quota.

A handoff resume is linked to the conversation that wrote the handoff, or else to the one its `Session:` line names. The report cannot see a `/clear` or a new session started without a handoff: nothing links them to the conversation they replace. A resume with `/jalon-resume` is linked too: by the path it names, or else by the handoff `jalon handoffs` printed for it. A handoff asked with `/jalon-handoff` is not counted as an advice, so the reset that follows shows as one with no advice.

## Returns after a pause

After a pause longer than the prompt cache's lifetime, the next request writes the whole context to the cache again, at a write price instead of a read price (see [cost and quality](cost-and-quality.md)). After the Impact section, each period has a section on these cold returns: the first request answering a prompt you typed, more than 5 minutes after the conversation's previous request, which read from the cache less than half of what that previous request sent. Five minutes is the shortest cache lifetime Claude Code writes; the read share, not the length of the pause, tells an expired cache, so the report works whatever lifetime your sessions use. A request right after a compaction does not count, since the compaction changed the context, nor does a conversation's first request, a prompt you did not type, or a subagent's request.

The section prints how many there were, the prompt tokens they did not read from the cache and their share of all the prompt tokens of the main conversations in the period, the median pause (the share is small, but each of these tokens was written to the cache at forty times the price of reading it), how many came with the context at or above the advice threshold over its starting size, and how many came after a handoff file was written in the same conversation, which could have been resumed instead.

It cannot see a pause in the middle of a running turn, such as a long tool run or a permission prompt left waiting: only the first request after a typed prompt counts. Nor does it see a return in a new session, such as one resumed from a handoff: that starts a new conversation, not a return.

## The simulation

For each conversation, the report replays the advisor with your current thresholds, with each conversation's starting size, and compares its advice with the resets you did: your compactions and your handoffs. It replays the advice only at the turns the prompt hook acts on: a prompt you typed, in a conversation where you had typed before. A background task's report, a message from another session or a prompt sent before your first one gets none, as with the hook; a conversation a program drove alone (`claude -p`) is left out. Like the hook, it cannot tell a program's prompt from yours once you have typed in the conversation. The lead still counts every turn. It also replays the advice the stop hook gives at the end of a turn whose prompt you typed, when the prompt hook did not advise at its start: on the size of the turn's request before its last one, the size the hook reads, since the transcript does not hold the last one yet. Such an advice counts like one at the next prompt, which then does not repeat it, and the report prints how many advices fall at the end of a turn.

It also replays the advice at the request the stop hook acted on: when the hook advised inside a prompt span, the transcript says where, and the replay reads the size the hook saw rather than the larger one the span reached after the block.

For each reset it prints how many turns before it the advice would have come (the lead) — 0 turns is the normal case, since the model writes the handoff in the turn the advice lands —, or `crossed mid-turn` when the context grew past the threshold where no hook would advise: at a turn's last request, at a turn no hook acts on, or inside a turn with `--stop-hook=false`; or `no advice`. It also prints the compactions with their size, the starting sizes, and the share of the context taken by stale tool results at each advised point.

"Extra prompt tokens after the advice" is the sum, over the requests from the first simulated advice to the reset, of the prompt tokens above the size at that advice: roughly what resetting at the advice would have avoided reading.

The starting size of a conversation is the size of its first request: the instructions, memory and tools the client loads, and your first prompt. The report prints its distribution, the share of the prompt tokens read by the conversations that started in the period that was this starting size, which no reset can remove (the Impact section's total also counts the conversations that started earlier), and one line per project: its working directory (or its folder's name), the number of conversations, the prompt tokens their starting size accounts for, the p50 and max of that starting size and that share, the most tokens first, at most 10 lines. The ranking is by those tokens, not by the median, so a project holding one heavy conversation does not outrank one holding many that cost more. A project whose starting size stands out is one whose instructions are worth trimming; `--split` shows whether a change lowered it.

The last section of each period covers the subagents, which the hooks do not see: each runs in its own context, kept in `<session>/subagents/` next to the session file. For the subagents that started in the period, of every conversation, it prints the prompt tokens they read and their share of all the prompt tokens read in the period, the share of their first requests, how many grew past the advice threshold above their first request and what share they read, and the five that read the most, with their agent type, requests, first and largest request. A subagent that went past the threshold had a task large enough to be worth splitting.

Once the hook advises, you reset at the threshold then in force, so those resets cannot judge a higher one; the report marks the resets that followed an advice the hook gave.
