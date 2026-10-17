---
name: jalon-report
description: Jalon. Run jalon context-report on this machine's transcripts and explain what it shows.
argument-hint: "[context-report flags, e.g. -since 30d]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-report $ARGUMENTS`. The report gives the facts; your job is to judge them and advise:

1. Run `{jalon} context-report -summary $ARGUMENTS`, with `-since 7d` when the arguments give no `-since`. When it fails, give its error and stop.
2. Reply in the user's language, in three parts:
   - **Verdict**, in at most two lines: what Jalon brought, the tokens saved by the resets that followed its advice, as an estimate; then what the late resets read past the advice, and how that compares with resets taken at once. Credit Jalon with the part after its advice only, never the total.
   - **What held the late resets**: for each late reset it lists, its date, project, delay and tokens, then in one line what the prompts typed after the advice show held it: a new subject started in the same conversation, a chain of short go-aheads through one task, a debugging loop, waiting on a review or a merge... Quote a few words of a prompt at most. When the prompts do not show it, say so. Then its `claude --resume` line, to run from that project. Leave this part out when it lists none.
   - **Advice**: two to four concrete actions for this user, drawn from what you saw in the prompts and the summary's lines, such as when in their way of working to take the reset. The summary's To do lines are leads: adapt them, never copy them.
3. Write for someone who does not care how it is measured: plain words, few numbers, no p50, no prompt tokens, no section names; "typically" for a p50, "tokens" for prompt tokens, "an estimate" for Saved. Every number you give is one the summary printed; compute, round or sum up none. Give no value for `jalon init --advise-tokens`: the report prints none. The details are in the report, for whoever asks.
4. When the user asks about a line, always run the report again, without `-summary` or with other `-since` or `-split` values, even when the summary seems to answer, and answer from the section that line names. Write no file and change no setting.
