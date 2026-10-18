package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/cli"
	"github.com/a-lafon/jalon/internal/config"
	"github.com/a-lafon/jalon/internal/ctxreport"
	"github.com/a-lafon/jalon/internal/transcript"
)

// runContextReport prints the offline context report. It reads the Claude Code
// transcripts only, and never touches the network. To
// link a handoff resume whose handoff was written outside the window, it reads
// the Session: line of the handoff file.
func runContextReport(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("context-report", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath(), "path to the config file")
	sinceFlag := flags.String("since", "7d", "time window, as a Go duration such as 90m or 24h (Nd for days)")
	transcripts := flags.String("transcripts", defaultTranscripts(), "directory of the Claude Code transcripts")
	split := flags.String("split", "", "comma-separated times (2026-10-12T09:00 or 2026-10-13, local time) that cut the window into periods")
	stopHook := flags.Bool("stop-hook", true, "replay the stop hook's advice too (false: the prompt hook alone, as 0.1.0)")
	summaryOnly := flags.Bool("summary", false, "print the summary only, not the sections it comes from")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// A shell glob given to -transcripts expands to several folders: the first is read, the rest land here.
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected argument %s: -transcripts takes one directory", flags.Arg(0))
	}
	window, err := cli.ParseWindow(*sinceFlag)
	if err != nil {
		return err
	}
	now := time.Now()
	since := now.Add(-window)
	cuts, err := cli.ParseSplits(*split, since, now)
	if err != nil {
		return err
	}

	cfg, err := cli.LoadConfig(flags, *configPath)
	if err != nil {
		return err
	}
	params := ctxreport.Params{
		Advisor:         advisor.Config{AdviseTokens: cfg.Context.AdviseTokens, StepTokens: cfg.Context.AdviseStepTokens},
		StaleAfterTurns: cfg.Context.StaleAfterTurns,
		StopHook:        *stopHook,
	}

	if _, err := os.Stat(*transcripts); err != nil {
		return fmt.Errorf("cannot read the transcripts directory %s: %w", *transcripts, unwrapPathError(err))
	}

	fmt.Fprintf(out, "Context report, last %s (since %s)\n", cli.FormatWindow(window), since.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "Advisor: advise at +%s tokens above the first request, again after +%s more; stale = tool results older than %d turns. Nothing leaves this machine.\n",
		cli.Thousands(int64(params.Advisor.AdviseTokens)), cli.Thousands(int64(params.Advisor.StepTokens)), params.StaleAfterTurns)

	reader := transcript.NewReader()
	// The transcripts are Claude Code's, so its adapter tells the typed prompts.
	reader.Typed = adapters[defaultClient].IsTyped
	convs, err := reader.ReadDir(*transcripts, since)
	if err != nil {
		return err
	}
	printCoverage(out, reader.Coverage, *transcripts)
	sim := ctxreport.Simulate(convs, params)
	impact := ctxreport.Impacts(convs)
	returns := ctxreport.ColdReturns(convs, params.Advisor.AdviseTokens)
	title := "Summary"
	if len(cuts) > 0 {
		title = "Summary of the whole window"
	}
	printSummary(out, title, impact.Between(since, now), sim.Between(since, now),
		ctxreport.SubagentsBetween(convs, params.Advisor.AdviseTokens, since, now), returns.Between(since, now), wheres(convs), params.Advisor.AdviseTokens)
	if *summaryOnly {
		return nil
	}

	bounds := append(append([]time.Time{since}, cuts...), now)
	for i := 0; i+1 < len(bounds); i++ {
		from, to := bounds[i], bounds[i+1]
		if len(cuts) > 0 {
			fmt.Fprintf(out, "\n=== Period %d of %d: %s to %s ===\n", i+1, len(bounds)-1,
				from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"))
		}
		printImpact(out, impact.Between(from, to))
		printReturns(out, returns.Between(from, to), params.Advisor.AdviseTokens)
		printTranscriptPeriod(out, sim.Between(from, to), params)
		printSubagents(out, ctxreport.SubagentsBetween(convs, params.Advisor.AdviseTokens, from, to), params.Advisor.AdviseTokens)
	}
	return nil
}

func defaultTranscripts() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude", "projects")
	}
	return filepath.Join(home, ".claude", "projects")
}

func unwrapPathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func printCoverage(out io.Writer, c transcript.Coverage, dir string) {
	fmt.Fprintf(out, "\nTranscripts: %s\n", dir)
	fmt.Fprintf(out, "  %d files, %s entries read: %s used, %s known and unused, %s skipped (%s); %d distinct compactions\n",
		c.Files, cli.Thousands(int64(c.Read)), cli.Thousands(int64(c.Interpreted)), cli.Thousands(int64(c.Ignored)),
		cli.Thousands(int64(c.Skipped)), cli.Percent(c.SkippedShare()), c.Compactions)
	if c.Found > 0 && c.Files == 0 {
		fmt.Fprintf(out, "  %s found, none modified in this window\n", cli.Plural(c.Found, "transcript file"))
	}
	if c.Found == 0 {
		fmt.Fprintf(out, "  no transcript file found: the report reads %s and %s\n",
			filepath.Join(dir, "<project>", "<session>.jsonl"), filepath.Join(dir, "<session>.jsonl"))
	}
	if c.Read > 0 && c.SkippedShare() > 0.05 {
		fmt.Fprintln(out, "  warning: many entries were not understood, the transcript format may have changed; read the transcript sections with care")
	}
}

// printImpact prints what the advice given and the resets of a period did.
func printImpact(out io.Writer, im ctxreport.Impact) {
	fmt.Fprintln(out, "\nImpact (transcripts; estimates)")
	followed := map[string]int{}
	var leads []float64
	for _, a := range im.Advices {
		if a.FollowedBy != "" {
			followed[a.FollowedBy]++
			leads = append(leads, float64(a.Lead))
		}
	}
	fmt.Fprintf(out, "  advice given %d: followed by a compaction %d, by a handoff %d, nothing seen %d",
		len(im.Advices), followed[ctxreport.KindCompaction], followed[ctxreport.KindHandoff], len(im.Advices)-len(leads))
	if len(leads) > 0 {
		fmt.Fprintf(out, "; lead p50 %s", cli.Plural(int(ctxreport.Distribution(leads).P50), "turn"))
	}
	fmt.Fprintln(out)

	advised := 0
	var inN, cacheRead int64
	for _, r := range im.Resets {
		if r.Advised {
			advised++
		}
		inN += r.PromptTokens
		cacheRead += r.CacheRead
	}
	fmt.Fprintf(out, "  resets %d (%d advised, %d unprompted); handoff resumes not linked %d\n",
		len(im.Resets), advised, len(im.Resets)-advised, len(im.NotLinked))
	notReRead, advisedNotReRead, base := notReReadSums(im)
	fmt.Fprintf(out, "  prompt tokens read %s; not re-read thanks to resets about %s prompt tokens (%s of what would have been read)\n",
		tokensM(im.PromptRead), tokensM(notReRead), cli.PercentOf(shareOf(notReRead, base)))
	fmt.Fprintf(out, "    of which after advice about %s (%s of the same base)\n", tokensM(advisedNotReRead), cli.PercentOf(shareOf(advisedNotReRead, base)))
	fmt.Fprint(out, "    estimate: assumes the conversations would have gone on the same way; all cache types counted together")
	if inN > 0 {
		fmt.Fprintf(out, " (%s cache read)", cli.PercentOf(float64(cacheRead)/float64(inN)))
	}
	fmt.Fprintln(out)
	if len(im.Resets) == 0 {
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  time\tkind\tbefore\tafter\trequests\tnot re-read\tadvised")
	for _, r := range im.Resets {
		after := "-"
		if r.Requests > 0 {
			after = cli.TokensK(float64(r.After))
		}
		advice := "no"
		if r.Advised {
			advice = fmt.Sprintf("yes, lead %d", r.Lead)
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%d\t%s\t%s\n", r.Time.Local().Format("2006-01-02 15:04"), r.Kind,
			cli.TokensK(float64(r.Before)), after, r.Requests, tokensM(r.NotReRead), advice)
	}
	w.Flush()
}

// printReturns prints the prompts typed after a pause that found the cache
// expired, and what they did not read from it.
func printReturns(out io.Writer, rs ctxreport.Returns, adviseTokens int) {
	fmt.Fprintln(out, "\nReturns after a pause (transcripts)")
	if len(rs.Cold) == 0 {
		fmt.Fprintln(out, "  none in this period")
		return
	}
	notRead := rs.NotRead()
	var pauses []float64
	grown, handoff := 0, 0
	for _, r := range rs.Cold {
		pauses = append(pauses, r.Pause.Minutes())
		if r.Grown {
			grown++
		}
		if r.AfterHandoff {
			handoff++
		}
	}
	fmt.Fprintf(out, "  %s came more than %s after the previous request and read less than half of it from the cache: %s prompt tokens not read from it, %s of the %s read in this period; pause p50 %s\n",
		cli.Plural(len(rs.Cold), "typed prompt"), minutes(ctxreport.MinPause.Minutes()), tokensM(notRead), smallPercent(shareOf(notRead, rs.PromptRead)), tokensM(rs.PromptRead),
		minutes(ctxreport.Distribution(pauses).P50))
	fmt.Fprintf(out, "  %d came with the context +%s tokens or more above its starting size; %d after a handoff was written in the conversation\n",
		grown, cli.Thousands(int64(adviseTokens)), handoff)
}

// smallPercent is PercentOf with one decimal below 1%: cold returns are a small
// share of the tokens read, but each costs forty reads, so 0% would hide them.
func smallPercent(v float64) string {
	if v < 0.01 {
		return fmt.Sprintf("%.1f%%", v*100)
	}
	return cli.PercentOf(v)
}

// minutes formats a pause in minutes: 42m, 2h05m.
func minutes(m float64) string {
	n := int(m)
	if n < 60 {
		return fmt.Sprintf("%dm", n)
	}
	return fmt.Sprintf("%dh%02dm", n/60, n%60)
}

// notReReadSums is the prompt tokens the resets kept from being read again, by
// all of them and by the advised ones, and what would have been read without
// any reset.
func notReReadSums(im ctxreport.Impact) (all, advised, base int64) {
	for _, r := range im.Resets {
		all += r.NotReRead
		if r.Advised {
			advised += r.NotReRead
		}
	}
	return all, advised, im.PromptRead + all
}

// printTranscriptPeriod prints the sections that come from the transcripts.
func printTranscriptPeriod(out io.Writer, r ctxreport.Result, p ctxreport.Params) {
	printStartingSize(out, r.Floors)
	var compactions []ctxreport.SimReset
	for _, c := range r.Resets {
		if c.Kind == ctxreport.KindCompaction {
			compactions = append(compactions, c)
		}
	}
	fmt.Fprintln(out, "\nCompactions done by the client (transcripts)")
	if len(compactions) == 0 {
		fmt.Fprintln(out, "  none in this period")
	} else {
		byTrigger := map[string]int{}
		var pre []float64
		for _, c := range compactions {
			byTrigger[c.Trigger]++
			pre = append(pre, float64(c.PreTokens))
		}
		d := ctxreport.Distribution(pre)
		fmt.Fprintf(out, "  %d: %d manual, %d auto", len(compactions), byTrigger["manual"], byTrigger["auto"])
		for trigger, n := range byTrigger {
			if trigger != "manual" && trigger != "auto" {
				fmt.Fprintf(out, ", %d %s", n, trigger)
			}
		}
		fmt.Fprintf(out, "\n  tokens before: p25 %s  p50 %s  p75 %s  max %s\n", cli.TokensK(d.P25), cli.TokensK(d.P50), cli.TokensK(d.P75), cli.TokensK(d.Max))
	}

	if len(r.Resets) > 0 {
		fmt.Fprintln(out, "\nSimulated advice before each reset")
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  Time\tKind\tTokens\tAdvice lead\tExtra prompt tokens after the advice\tAdvice given")
		given := false
		for _, c := range r.Resets {
			lead, beyond := "no advice", ""
			switch {
			case c.Advised:
				lead, beyond = cli.Plural(c.Lead, "turn"), cli.TokensK(float64(c.Beyond))
			case c.CrossedMidTurn:
				lead = "crossed mid-turn"
			}
			advice := "no"
			if c.AdviceGiven {
				advice, given = "yes", true
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", c.Time.Local().Format("2006-01-02 15:04"), c.Kind, cli.TokensK(float64(c.PreTokens)), lead, beyond, advice)
		}
		w.Flush()
		if given {
			fmt.Fprintln(out, "  Resets marked \"yes\" followed an advice the hook gave, at the threshold in force then: they cannot judge a higher threshold.")
		}
	}

	if r.Excluded > 0 {
		fmt.Fprintf(out, "\n%d of %d conversations left out of the simulation: no prompt typed by a person (driven by a program).\n",
			r.Excluded, r.Excluded+r.Conversations)
	}
	atStop := "not replayed (-stop-hook=false)"
	if p.StopHook {
		n := 0
		for _, a := range r.Advices {
			if a.AtStop {
				n++
			}
		}
		atStop = fmt.Sprintf("%d of %d", n, len(r.Advices))
	}
	fmt.Fprintf(out, "\nSimulated advices at the end of a turn (stop hook): %s\n", atStop)
	fmt.Fprintf(out, "Advices no reset followed: %d of %d\n", len(r.Unfollowed), len(r.Advices))
	for _, a := range r.Unfollowed {
		fmt.Fprintf(out, "  %s  %s tokens\n", a.Time.Local().Format("2006-01-02 15:04"), cli.TokensK(float64(a.PromptTokens)))
	}

	fmt.Fprintln(out, "\nStale share at advised points (tool results older than N turns or requests, share of context bytes)")
	if len(r.Advices) == 0 {
		fmt.Fprintln(out, "  no advice in this period")
		return
	}
	shares := make([]float64, len(r.Advices))
	for i, a := range r.Advices {
		shares[i] = a.StaleShare
	}
	d := ctxreport.Distribution(shares)
	fmt.Fprintf(out, "  by turns, older than %d: %d points: p25 %s  p50 %s  p75 %s  max %s\n", p.StaleAfterTurns, d.N, cli.PercentOf(d.P25), cli.PercentOf(d.P50), cli.PercentOf(d.P75), cli.PercentOf(d.Max))
	for i, age := range ctxreport.RequestAges {
		by := make([]float64, len(r.Advices))
		for j, a := range r.Advices {
			by[j] = a.StaleByRequests[i]
		}
		d := ctxreport.Distribution(by)
		fmt.Fprintf(out, "  by requests, older than %d: p25 %s  p50 %s  p75 %s\n", age, cli.PercentOf(d.P25), cli.PercentOf(d.P50), cli.PercentOf(d.P75))
	}
}

// maxSubagents is the number of subagents the subagents section lists.
const maxSubagents = 5

// printSubagents prints the subagents that started in a period: what they read
// and how much of it was read by those that grew past the advice threshold.
func printSubagents(out io.Writer, s ctxreport.Subagents, adviseTokens int) {
	fmt.Fprintln(out, "\nSubagents (transcripts)")
	if len(s.Runs) == 0 {
		fmt.Fprintln(out, "  none in this period")
		return
	}
	fmt.Fprintf(out, "  %s: %s prompt tokens read, %s of the %s read in this period with the main conversations; first requests %s of it\n",
		cli.Plural(len(s.Runs), "subagent"), tokensM(s.Read), cli.PercentOf(shareOf(s.Read, s.Read+s.MainRead)),
		tokensM(s.Read+s.MainRead), cli.PercentOf(shareOf(s.FirstRead, s.Read)))
	fmt.Fprintf(out, "  %d went past the advice threshold (+%s tokens above their first request): %s of the subagents' prompt tokens; the hooks advise the main conversation only\n",
		s.Past, cli.Thousands(int64(adviseTokens)), cli.PercentOf(shareOf(s.PastRead, s.Read)))
	fmt.Fprintln(out, "  most read:")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "    time\ttype\trequests\tfirst\tlargest\tread")
	for _, r := range s.Runs[:min(len(s.Runs), maxSubagents)] {
		kind := r.Type
		if kind == "" {
			kind = "-"
		}
		fmt.Fprintf(w, "    %s\t%s\t%d\t%s\t%s\t%s\n", r.Time.Local().Format("2006-01-02 15:04"), kind, r.Requests,
			cli.TokensK(float64(r.First)), cli.TokensK(float64(r.Peak)), tokensM(r.Read))
	}
	w.Flush()
}

// maxProjects is the number of projects the starting size section lists.
const maxProjects = 10

// printStartingSize prints the starting sizes of a period, overall and by
// project, with the share of the prompt tokens read that they were.
func printStartingSize(out io.Writer, floors []ctxreport.Floor) {
	fmt.Fprintln(out, "\nStarting size of the conversations (first request, transcripts)")
	if len(floors) == 0 {
		fmt.Fprintln(out, "  none in this period")
		return
	}
	d, base, read := floorStats(floors)
	fmt.Fprintf(out, "  %d conversations: p10 %s  p50 %s  p90 %s  max %s tokens; a reset cannot remove it, the advice counts the growth above it\n",
		d.N, cli.TokensK(d.P10), cli.TokensK(d.P50), cli.TokensK(d.P90), cli.TokensK(d.Max))
	fmt.Fprintf(out, "  %s of the prompt tokens read by the conversations that started in this period was this starting size (%s of %s), which no reset can remove\n",
		cli.PercentOf(shareOf(base, read)), tokensM(base), tokensM(read))

	lines := projectLines(floors)
	fmt.Fprintln(out, "  by project, largest p50 first (starting size share of the prompt tokens read):")
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, l := range lines[:min(len(lines), maxProjects)] {
		fmt.Fprintf(w, "    %s\t%s\tp50 %s\tmax %s\t%s\n", l.name, cli.Plural(l.d.N, "conversation"),
			cli.TokensK(l.d.P50), cli.TokensK(l.d.Max), cli.PercentOf(l.share))
	}
	w.Flush()
	if more := len(lines) - maxProjects; more > 0 {
		fmt.Fprintf(out, "    %s not shown\n", cli.Plural(more, "more project"))
	}
}

// projectLine is a project's starting sizes, and the share of the prompt
// tokens its conversations read that was their starting size.
type projectLine struct {
	name  string
	d     ctxreport.Dist
	share float64
}

// projectLines groups the starting sizes by project, named by the working
// directory of its earliest conversation, the largest p50 first.
func projectLines(floors []ctxreport.Floor) []projectLine {
	type project struct {
		name   string
		floors []ctxreport.Floor
		named  time.Time // time of the conversation the name comes from
	}
	home, _ := os.UserHomeDir()
	byFolder := map[string]*project{}
	var projects []*project
	for _, f := range floors {
		p := byFolder[f.Project]
		if p == nil {
			p = &project{name: filepath.Base(f.Project)}
			byFolder[f.Project] = p
			projects = append(projects, p)
		}
		p.floors = append(p.floors, f)
		// The working directory of the earliest conversation that has one.
		if f.Cwd != "" && (p.named.IsZero() || f.Time.Before(p.named)) {
			p.name, p.named = homeTilde(f.Cwd, home), f.Time
		}
	}
	// Two transcript folders can show the same working directory, as a worktree's
	// folder whose first conversation started in the repository: one line each.
	byName := map[string]*project{}
	merged := projects[:0]
	for _, p := range projects {
		if q := byName[p.name]; q != nil {
			q.floors = append(q.floors, p.floors...)
			continue
		}
		byName[p.name] = p
		merged = append(merged, p)
	}
	projects = merged
	lines := make([]projectLine, len(projects))
	for i, p := range projects {
		d, base, read := floorStats(p.floors)
		lines[i] = projectLine{p.name, d, shareOf(base, read)}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].d.P50 != lines[j].d.P50 {
			return lines[i].d.P50 > lines[j].d.P50
		}
		return lines[i].name < lines[j].name
	})
	return lines
}

// floorStats is the distribution of the starting sizes, and the prompt tokens
// read that were the starting size, of all those read.
func floorStats(floors []ctxreport.Floor) (d ctxreport.Dist, base, read int64) {
	sizes := make([]float64, len(floors))
	for i, f := range floors {
		sizes[i] = float64(f.PromptTokens)
		read, base = read+f.PromptRead, base+f.FloorRead
	}
	return ctxreport.Distribution(sizes), base, read
}

func shareOf(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

// homeTilde writes the home directory at the start of path as ~.
func homeTilde(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

// tokensM formats a token count in millions: 10.1M.
func tokensM(n int64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1e6)
}
