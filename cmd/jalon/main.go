// Command jalon is the Jalon context advisor: it tells you when a conversation is
// worth resetting, from the prompt hook, and reports what that advice did.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const usage = `jalon - a context advisor for Claude Code

Usage:
  jalon init [-statusline=false | -mod] [-skill=false] [-uninstall] [-dry-run]
             [-client name] [-settings path] [-advise-tokens n]
             [-advise-step-tokens n]
      install the hooks and the status line, or the mod, and the /jalon-*
      commands; print or set the thresholds
  jalon context-report [-since 7d] [-transcripts dir] [-split t1,t2]
                       [-stop-hook=false] [-summary] [-config path]
      compare reset advice with the resets you did
  jalon handoffs [-thread] [path or words]
      print this project's latest handoff, or the one named, or with -thread
      its whole thread back to the first handoff
  jalon statusline [-client name] [-config path]
      print the context size for a status line (reads its JSON on stdin)
  jalon hook prompt [-client name] [-config path]
      prompt hook: advise a context reset (reads its JSON on stdin)
  jalon hook stop [-client name] [-config path]
      stop hook: add the advice at the end of a turn (reads its JSON on stdin)
  jalon version
      print the version, commit and build date (also --version)

Without -config, Jalon reads ~/.jalon/config.yaml, or uses its defaults when
that file does not exist. Run "jalon <command> -h" for a command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "init":
		if err := runInit(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jalon:", err)
			os.Exit(1)
		}
	case "context-report":
		if err := runContextReport(ctx, os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jalon:", err)
			os.Exit(1)
		}
	case "handoffs":
		if err := runHandoffs(os.Args[2:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jalon:", err)
			os.Exit(1)
		}
	case "statusline":
		if err := runStatusline(ctx, os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jalon:", err)
			os.Exit(1)
		}
	case "hook":
		// Exit 1 on a usage error, never 2: for a hook, 2 would block the prompt.
		if err := runHook(ctx, os.Args[2:], os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "jalon:", err)
			os.Exit(1)
		}
	case "version", "--version":
		runVersion(os.Stdout)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "jalon: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
