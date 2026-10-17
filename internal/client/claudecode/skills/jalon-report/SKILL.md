---
name: jalon-report
description: Jalon. Run jalon context-report on this machine's transcripts and explain what it shows.
argument-hint: "[context-report flags, e.g. -since 30d]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-report $ARGUMENTS`. Explain Jalon's context report to them:

1. Run `{jalon} context-report -summary $ARGUMENTS`, with `-since 7d` when the arguments give no `-since`. When it fails, give its error and stop.
2. Reply in three parts, from the summary's lines alone:
   - **Verdict**, in at most two lines: what the resets saved, as an estimate as its line says, against what the late resets read past the advice and how that compares with resets taken at once.
   - **To reopen**: each late reset it lists, with its date, project, delay and tokens, and its `claude --resume` line, to run from that project to see what held the reset. Leave this part out when it lists none.
   - **To do**: its first action word for word, every sentence of it; then each other one in one line.
3. Every number is one the summary printed, with its label; compute, round or sum up none. Give no value for `jalon init --advise-tokens`: the report prints none.
4. When the user asks about a line, always run the report again, without `-summary` or with other `-since` or `-split` values, even when the summary seems to answer, and answer from the section that line names. Write no file and change no setting.
