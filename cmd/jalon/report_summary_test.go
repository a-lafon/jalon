package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/ctxreport"
)

func TestGoldenSummary(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.Local) }
	im := ctxreport.Impact{
		Resets: []ctxreport.Reset{
			{Kind: ctxreport.KindHandoff, Time: at(12, 10), NotReRead: 30_000_000, Advised: true},
			{Kind: ctxreport.KindCompaction, Time: at(13, 10), NotReRead: 10_000_000},
		},
		PromptRead: 60_000_000,
	}
	sim := ctxreport.Result{
		Resets: []ctxreport.SimReset{
			{Kind: ctxreport.KindHandoff, Time: at(12, 10), File: "/t/a.jsonl", Advised: true, Lead: 12, Beyond: 4_200_000},
			{Kind: ctxreport.KindHandoff, Time: at(12, 15), File: "/t/b.jsonl", Advised: true, Lead: 19, Beyond: 9_800_000},
			{Kind: ctxreport.KindHandoff, Time: at(13, 9), File: "/t/c.jsonl", Advised: true, Lead: 10, Beyond: 600_000},
			{Kind: ctxreport.KindCompaction, Time: at(13, 10), File: "/t/a.jsonl", Advised: true, Lead: 11, Beyond: 2_000_000},
			{Kind: ctxreport.KindHandoff, Time: at(14, 10), File: "/t/a.jsonl", Advised: true, Lead: 0, Beyond: 21_000},
			{Kind: ctxreport.KindHandoff, Time: at(14, 11), File: "/t/b.jsonl", Advised: true, Lead: 1, Beyond: 5_000},
			{Kind: ctxreport.KindHandoff, Time: at(14, 12), File: "/t/b.jsonl", Advised: true, Lead: 4, Beyond: 300_000},
			{Kind: ctxreport.KindCompaction, Time: at(15, 10), File: "/t/c.jsonl"},
		},
		Advices:    make([]ctxreport.Advice, 9),
		Unfollowed: make([]ctxreport.Advice, 2),
		Floors: []ctxreport.Floor{
			floor("/t/-home-me-alpha", "~/alpha", 0, 55_000, 10_000_000, 4_000_000),
			floor("/t/-home-me-beta", "~/beta", 1, 40_000, 10_000_000, 1_000_000),
		},
	}
	subs := ctxreport.Subagents{Runs: make([]ctxreport.SubagentRun, 6), Read: 8_000_000, PastRead: 3_000_000, Past: 2}
	at2 := map[string]where{
		"/t/a.jsonl": {"~/alpha", "11111111-aaaa"},
		"/t/b.jsonl": {"~/beta", "22222222-bbbb"},
		"/t/c.jsonl": {"~/alpha", "33333333-cccc"},
	}
	var out bytes.Buffer
	printSummary(&out, "Summary", im, sim, subs, at2, 200_000)
	checkGolden(t, "context-report-summary.golden", out.String())

	out.Reset()
	printSummary(&out, "Summary", ctxreport.Impact{}, ctxreport.Result{}, ctxreport.Subagents{}, nil, 200_000)
	checkGolden(t, "context-report-summary-empty.golden", out.String())
}

func TestContextReportSummaryOnly(t *testing.T) {
	var out bytes.Buffer
	if err := runContextReport(context.Background(), []string{"-config", configWith(t, ""), "-summary", "-transcripts", t.TempDir()}, &out); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	if !strings.Contains(report, "\nSummary\n") || strings.Contains(report, "Impact (") || strings.Contains(report, "Starting size of") {
		t.Errorf("want the summary and no section:\n%s", report)
	}
}
