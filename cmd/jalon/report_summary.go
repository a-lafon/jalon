package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"text/tabwriter"

	"github.com/a-lafon/jalon/internal/cli"
	"github.com/a-lafon/jalon/internal/ctxreport"
	"github.com/a-lafon/jalon/internal/transcript"
)

// lateTurns is the advice lead from which a reset counts as late: the user
// turns between the advice and the reset.
const lateTurns = 10

// maxLate is the number of late resets the summary lists.
const maxLate = 3

// maxPrompts is the number of prompts the summary lists for each late reset.
const maxPrompts = 15

// heavyShare is the share of the prompt tokens from which the summary advises
// on the subagents or on the starting size.
const heavyShare = 0.25

// where is the project and the session of a transcript, to reopen it.
type where struct{ project, session string }

// wheres maps each conversation's transcript to its working directory, with
// the home folder as ~, and its session ID.
func wheres(convs []transcript.Conversation) map[string]where {
	home, _ := os.UserHomeDir()
	m := make(map[string]where, len(convs))
	for _, c := range convs {
		project := homeTilde(c.Cwd, home)
		if project == "" {
			project = "-"
		}
		m[c.File] = where{project, c.SessionID}
	}
	return m
}

// printSummary prints what the report's sections add up to: what the resets
// saved, what late and ignored advice cost, what no reset can remove, the late
// resets to reopen, and what to do, the most direct first. Each line names the
// section it comes from.
func printSummary(out io.Writer, title string, im ctxreport.Impact, sim ctxreport.Result, subs ctxreport.Subagents, rs ctxreport.Returns, at map[string]where, adviseTokens int) {
	fmt.Fprintf(out, "\n%s\n", title)
	notReRead, advised, base := notReReadSums(im)
	nAdvised := 0
	for _, r := range im.Resets {
		if r.Advised {
			nAdvised++
		}
	}
	fmt.Fprintf(out, "  Saved: about %s prompt tokens not re-read thanks to the %s that followed a Jalon advice; %s with all %d resets, %s of what would have been read (Impact; estimate: assumes the conversations would have gone on the same way)\n",
		tokensM(advised), cli.Plural(nAdvised, "reset"), tokensM(notReRead), len(im.Resets), cli.PercentOf(shareOf(notReRead, base)))

	var late []ctxreport.SimReset
	var lateBeyond []float64
	var onTime []float64
	var lateRead int64
	for _, r := range sim.Resets {
		switch {
		case !r.Advised:
		case r.Lead >= lateTurns:
			late = append(late, r)
			lateBeyond = append(lateBeyond, float64(r.Beyond))
			lateRead += int64(r.Beyond)
		case r.Lead <= 1:
			onTime = append(onTime, float64(r.Beyond))
		}
	}
	if len(late) == 0 {
		fmt.Fprintf(out, "  Late: no reset came %d turns or more after the advice (Simulated advice before each reset)\n", lateTurns)
	} else {
		fmt.Fprintf(out, "  Late: %s came %d turns or more after the advice and read %s prompt tokens past it, %s each at p50",
			cli.Plural(len(late), "reset"), lateTurns, tokensM(lateRead), tokensKM(ctxreport.Distribution(lateBeyond).P50))
		if len(onTime) > 0 {
			fmt.Fprintf(out, ", against %s at p50 for the %d taken within one turn", tokensKM(ctxreport.Distribution(onTime).P50), len(onTime))
		}
		fmt.Fprintln(out, " (Simulated advice before each reset)")
	}
	fmt.Fprintf(out, "  Ignored: %d of %s were followed by no reset (Advices no reset followed)\n", len(sim.Unfollowed), cli.Plural(len(sim.Advices), "advice"))

	subShare := shareOf(subs.PastRead, subs.Read)
	if len(subs.Runs) > 0 {
		fmt.Fprintf(out, "  Subagents: %d of %d went past +%s tokens and read %s of the subagents' prompt tokens; the hooks do not advise subagents (Subagents)\n",
			subs.Past, len(subs.Runs), cli.Thousands(int64(adviseTokens)), cli.PercentOf(subShare))
	}
	projects := projectLines(sim.Floors)
	_, floorRead, read := floorStats(sim.Floors)
	startShare := shareOf(floorRead, read)
	if len(projects) > 0 {
		fmt.Fprintf(out, "  Starting size: %s of the prompt tokens read, which no reset removes; largest p50 %s in %s (Starting size)\n",
			cli.PercentOf(startShare), cli.TokensK(projects[0].d.P50), projects[0].name)
	}
	if len(rs.Cold) > 0 {
		fmt.Fprintf(out, "  Cold returns: %s after a pause found the cache expired and sent %s of the prompt tokens without reading them from it (Returns after a pause)\n",
			cli.Plural(len(rs.Cold), "typed prompt"), smallPercent(shareOf(rs.NotRead(), rs.PromptRead)))
	}

	if len(late) > 0 {
		slices.SortStableFunc(late, func(a, b ctxreport.SimReset) int { return b.Beyond - a.Beyond })
		fmt.Fprintln(out, "  Late resets to reopen, most read past the advice first:")
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, r := range late[:min(len(late), maxLate)] {
			wh := at[r.File]
			fmt.Fprintf(w, "    %s\t%s\t%s\t+%s\tclaude --resume %s\n", r.Time.Local().Format("2006-01-02 15:04"), wh.project,
				cli.Plural(r.Lead, "turn"), tokensKM(float64(r.Beyond)), wh.session)
		}
		w.Flush()
		for _, r := range late[:min(len(late), maxLate)] {
			if len(r.Prompts) == 0 {
				continue
			}
			fmt.Fprintf(out, "  Typed after the advice, before the %s reset:\n", r.Time.Local().Format("2006-01-02 15:04"))
			for i, p := range r.Prompts[:min(len(r.Prompts), maxPrompts)] {
				fmt.Fprintf(out, "    %d. %s\n", i+1, p)
			}
			if n := len(r.Prompts) - maxPrompts; n > 0 {
				fmt.Fprintf(out, "    and %d more\n", n)
			}
		}
	}

	var todo []string
	if len(late) > 0 {
		todo = append(todo, fmt.Sprintf("Take the reset when the advice comes: the late resets read %s past it; the prompts typed after the advice, above, show what held each one.", tokensM(lateRead)))
	}
	if len(subs.Runs) > 0 && subShare >= heavyShare {
		todo = append(todo, fmt.Sprintf("Give subagents smaller tasks: the %d that grew past +%s tokens read %s of the subagents' prompt tokens, and Jalon cannot advise them.",
			subs.Past, cli.Thousands(int64(adviseTokens)), cli.PercentOf(subShare)))
	}
	if len(projects) > 0 && startShare >= heavyShare {
		todo = append(todo, fmt.Sprintf("Trim what conversations load at start in %s (MCP servers, CLAUDE.md, skills): they start at %s at p50, and every request reads it again.",
			projects[0].name, cli.TokensK(projects[0].d.P50)))
	}
	switch {
	case len(todo) > 0:
	case len(sim.Advices) == 0:
		todo = append(todo, "Nothing to judge yet: no advice in this window.")
	default:
		todo = append(todo, "Nothing to change: the advice is taken on time.")
	}
	fmt.Fprintln(out, "  To do, most direct first:")
	for i, t := range todo {
		fmt.Fprintf(out, "    %d. %s\n", i+1, t)
	}
}

// tokensKM formats a token count in thousands below a million, in millions
// from it: 21K, 10.2M.
func tokensKM(v float64) string {
	if v < 1e6 {
		return cli.TokensK(v)
	}
	return tokensM(int64(v))
}
