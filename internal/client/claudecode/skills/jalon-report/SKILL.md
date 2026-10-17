---
name: jalon-report
description: Jalon. Run jalon context-report on this machine's transcripts and explain what it shows.
argument-hint: "[context-report flags, e.g. -since 30d]"
disable-model-invocation: true
---

<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->

The user typed `/jalon-report $ARGUMENTS`. Read Jalon's context report for them:

1. Run `{jalon} context-report $ARGUMENTS`, with `-since 7d` when the arguments give no `-since`. When it fails, give its error and stop.
2. Every number you give is one the report printed; name the section it comes from. Do not compute, estimate or round other numbers.
3. Reply with at most three findings, the ones that matter most for the user's cost or for when to reset, and at most one action, written as a command for the user to run, such as `jalon init --advise-tokens <n>`.
4. You may run the report again with other `-since` or `-split` values to answer the user's questions. Write no file and change no setting.
