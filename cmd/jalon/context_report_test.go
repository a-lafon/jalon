package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/ctxreport"
)

func writeTranscript(t *testing.T, dir string, lines ...string) {
	t.Helper()
	writeSession(t, dir, "session.jsonl", lines...)
}

func writeSession(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	project := filepath.Join(dir, "-project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ts(at time.Time) string { return at.UTC().Format(time.RFC3339Nano) }

func tUser(at time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":"u%d","timestamp":%q,"origin":{"kind":"human"},"message":{"content":"x"}}`, at.UnixNano(), ts(at))
}

// tScripted is a prompt sent by a program: no origin.
func tScripted(at time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":"s%d","timestamp":%q,"message":{"content":"x"}}`, at.UnixNano(), ts(at))
}

func tAssistant(at time.Time, id string, cacheRead int) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"a%d","timestamp":%q,"message":{"id":%q,"content":[{"type":"text","text":"x"}],`+
		`"usage":{"input_tokens":5,"cache_read_input_tokens":%d,"cache_creation_input_tokens":0}}}`, at.UnixNano(), ts(at), id, cacheRead)
}

func tCompact(at time.Time, uuid, trigger string, pre int) string {
	return fmt.Sprintf(`{"type":"system","subtype":"compact_boundary","uuid":%q,"timestamp":%q,"compactMetadata":{"trigger":%q,"preTokens":%d}}`,
		uuid, ts(at), trigger, pre)
}

func TestContextReportComparesAdviceWithTheClientCompactions(t *testing.T) {
	now := time.Now()
	configPath := configWith(t, "")
	dir := t.TempDir()
	at := func(m int) time.Time { return now.Add(-time.Duration(120-m) * time.Minute) }
	writeTranscript(t, dir,
		tUser(at(0)), tAssistant(at(1), "m1", 10_000), // the floor: 210K is 200K above it
		tUser(at(2)), tAssistant(at(3), "m2", 210_000), // the advisor fires here
		tUser(at(4)), tAssistant(at(5), "m3", 230_000),
		tUser(at(6)), tAssistant(at(7), "m4", 250_000),
		tCompact(at(8), "c1", "manual", 255_000),
		tCompact(at(9), "c1", "manual", 255_000), // the same entry again
		tUser(at(10)), tAssistant(at(11), "m5", 30_000),
		`{"type":"hologram"}`,
	)
	// A script drives this one into the advice zone: it must not count.
	writeSession(t, dir, "script.jsonl", tScripted(at(0)), tAssistant(at(1), "s1", 250_000))

	var out bytes.Buffer
	if err := runContextReport(context.Background(), []string{"-config", configPath, "-since", "3h", "-transcripts", dir}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Transcripts: " + dir, "2 files", "1 skipped", "1 distinct compactions",
		"1 of 2 conversations left out of the simulation",
		"Starting size of the conversations", "1 conversations: p10 10K  p50 10K  p90 10K  max 10K tokens",
		"by requests, older than 10", "by requests, older than 25", "by requests, older than 50",
		"1 manual, 0 auto", "p50 255K",
		"Advice lead",
		"Advices no reset followed: 0 of 1",
		"Stale share at advised points", "1 points",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report does not contain %q:\n%s", want, out.String())
		}
	}
	// Advised at turn 1 of 4: two turns started after it; 20K + 40K carried beyond its 210K.
	row := regexp.MustCompile(`compaction\s+255K\s+2 turns\s+60K\s+no`)
	if len(row.FindAllString(out.String(), -1)) != 1 {
		t.Errorf("the repeated compaction must be listed once:\n%s", out.String())
	}
}

// tNotice is a prompt Claude Code sends itself: a background task's report.
func tNotice(at time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":"n%d","timestamp":%q,"origin":{"kind":"task-notification"},"message":{"content":"<task-notification> <task-id>b1</task-id> done"}}`, at.UnixNano(), ts(at))
}

// tPeer is a message from another session.
func tPeer(at time.Time) string {
	return fmt.Sprintf(`{"type":"user","uuid":"p%d","timestamp":%q,"origin":{"kind":"peer"},"message":{"content":[{"type":"text","text":"Another Claude session sent a message: done"}]}}`, at.UnixNano(), ts(at))
}

func TestContextReportReplaysOnlyTheTurnsTheHookActsOn(t *testing.T) {
	now := time.Now()
	at := func(m int) time.Time { return now.Add(-time.Duration(120-m) * time.Minute) }
	// Each conversation starts at 10K: 210K and more is 200K above its floor.
	tests := map[string]struct {
		lines []string
		want  []string
		row   string // a row of the simulated resets, when set
	}{
		"typed prompts": {
			lines: []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000), tUser(at(2)), tAssistant(at(3), "m2", 230_000)},
			want:  []string{"Advices no reset followed: 1 of 1", "230K tokens"},
		},
		// Before: advised at the notice, one turn before the compaction.
		"task notification crosses": {
			lines: []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000), tUser(at(2)), tAssistant(at(3), "m2", 100_000),
				tNotice(at(4)), tAssistant(at(5), "m3", 215_000), tUser(at(6)), tAssistant(at(7), "m4", 220_000),
				tCompact(at(8), "c1", "manual", 225_000)},
			want: []string{"Advices no reset followed: 0 of 1"},
			row:  `compaction\s+225K\s+0 turns\s+0K\s+no`,
		},
		// Before: 1 of 1.
		"peer message crosses": {
			lines: []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000), tPeer(at(2)), tAssistant(at(3), "m2", 230_000)},
			want:  []string{"Advices no reset followed: 0 of 0"},
		},
		// The compaction follows no turn the hook acts on: the threshold was crossed at the notice.
		"crossed at a notice only": {
			lines: []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000), tNotice(at(2)), tAssistant(at(3), "m2", 230_000),
				tCompact(at(4), "c1", "manual", 230_000)},
			want: []string{"Advices no reset followed: 0 of 0"},
			row:  `compaction\s+230K\s+crossed mid-turn\s+no`,
		},
		"claude -p session": {
			lines: []string{tScripted(at(0)), tAssistant(at(1), "m1", 10_000), tScripted(at(2)), tAssistant(at(3), "m2", 250_000)},
			want:  []string{"1 of 1 conversations left out of the simulation", "Advices no reset followed: 0 of 0"},
		},
		// The hook advises from the prompt after the first typed one. Before: at 230K.
		"program prompts before the first typed one": {
			lines: []string{tScripted(at(0)), tAssistant(at(1), "m1", 10_000), tScripted(at(2)), tAssistant(at(3), "m2", 230_000),
				tUser(at(4)), tAssistant(at(5), "m3", 235_000), tUser(at(6)), tAssistant(at(7), "m4", 240_000)},
			want: []string{"Advices no reset followed: 1 of 1", "240K tokens"},
		},
		// Once a person typed, the hook cannot tell a program's prompt from a typed one.
		"program prompt after a typed one": {
			lines: []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000), tScripted(at(2)), tAssistant(at(3), "m2", 230_000)},
			want:  []string{"Advices no reset followed: 1 of 1", "230K tokens"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTranscript(t, dir, tt.lines...)
			var out bytes.Buffer
			if err := runContextReport(context.Background(), []string{"-config", configWith(t, ""), "-since", "3h", "-transcripts", dir}, &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("report does not contain %q:\n%s", want, out.String())
				}
			}
			if tt.row != "" && !regexp.MustCompile(tt.row).MatchString(out.String()) {
				t.Errorf("no row %q:\n%s", tt.row, out.String())
			}
		})
	}
}

func TestContextReportReplaysTheStopHook(t *testing.T) {
	now := time.Now()
	at := func(m int) time.Time { return now.Add(-time.Duration(120-m) * time.Minute) }
	// Each conversation starts at 10K: the advice comes at 210K, again 150K higher.
	start := []string{tUser(at(0)), tAssistant(at(1), "m1", 10_000)}
	crossedInside := append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 150_000), tAssistant(at(4), "m3", 314_000),
		tAssistant(at(5), "m4", 320_000), tCompact(at(6), "c1", "manual", 320_000))
	tests := map[string]struct {
		lines []string
		flags []string
		want  []string
		row   string // a row of the simulated resets, when set
	}{
		// The stop hook reads 314K, the request before the last; 6K is carried after it.
		"crossed inside one turn": {
			lines: crossedInside,
			want:  []string{"Simulated advices at the end of a turn (stop hook): 1 of 1", "Advices no reset followed: 0 of 1"},
			row:   `compaction\s+320K\s+0 turns\s+6K\s+no`,
		},
		"crossed inside one turn, stop hook left out": {
			lines: crossedInside,
			flags: []string{"-stop-hook=false"},
			want:  []string{"Simulated advices at the end of a turn (stop hook): not replayed (-stop-hook=false)", "Advices no reset followed: 0 of 0"},
			row:   `compaction\s+320K\s+crossed mid-turn\s+no`,
		},
		"crossed by the turn's last request": {
			lines: append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 150_000), tAssistant(at(4), "m3", 160_000),
				tAssistant(at(5), "m4", 314_000), tCompact(at(6), "c1", "manual", 314_000)),
			want: []string{"(stop hook): 0 of 0"},
			row:  `compaction\s+314K\s+crossed mid-turn\s+no`,
		},
		"advised at the turn's start": {
			lines: append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 215_000), tAssistant(at(4), "m3", 300_000),
				tAssistant(at(5), "m4", 310_000)),
			want: []string{"(stop hook): 0 of 1", "215K tokens"},
		},
		// The hook then only reminds, even past the step.
		"advised at the turn's start, grown past the step": {
			lines: append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 215_000), tAssistant(at(4), "m3", 370_000),
				tAssistant(at(5), "m4", 380_000)),
			want: []string{"(stop hook): 0 of 1", "215K tokens"},
		},
		"next prompt after an end-of-turn advice": {
			lines: append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 150_000), tAssistant(at(4), "m3", 260_000),
				tAssistant(at(5), "m4", 261_000), tUser(at(6)), tAssistant(at(7), "m5", 262_000)),
			want: []string{"(stop hook): 1 of 1", "Advices no reset followed: 1 of 1", "260K tokens"},
		},
		"turn started by a background task's report": {
			lines: append(start[:2:2], tNotice(at(2)), tAssistant(at(3), "m2", 150_000), tAssistant(at(4), "m3", 260_000),
				tAssistant(at(5), "m4", 265_000)),
			want: []string{"(stop hook): 0 of 0"},
		},
		// The auto compaction closes the stretch before the hook reads 220K after it.
		"compaction inside the turn": {
			lines: append(start[:2:2], tUser(at(2)), tAssistant(at(3), "m2", 150_000), tAssistant(at(4), "m3", 314_000),
				tCompact(at(5), "c1", "auto", 314_000), tAssistant(at(6), "m4", 220_000), tAssistant(at(7), "m5", 225_000)),
			want: []string{"(stop hook): 1 of 1", "Advices no reset followed: 1 of 1", "220K tokens"},
			row:  `compaction\s+314K\s+crossed mid-turn\s+no`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTranscript(t, dir, tt.lines...)
			var out bytes.Buffer
			args := append([]string{"-config", configWith(t, ""), "-since", "3h", "-transcripts", dir}, tt.flags...)
			if err := runContextReport(context.Background(), args, &out); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("report does not contain %q:\n%s", want, out.String())
				}
			}
			if tt.row != "" && !regexp.MustCompile(tt.row).MatchString(out.String()) {
				t.Errorf("no row %q:\n%s", tt.row, out.String())
			}
		})
	}
}

func TestContextReportSplitPrintsOnePeriodEach(t *testing.T) {
	now := time.Now()
	configPath := configWith(t, "")
	cut := now.Add(-30 * time.Minute).Format("2006-01-02T15:04")

	var out bytes.Buffer
	dir := t.TempDir()
	writeTranscript(t, dir, tUser(now.Add(-90*time.Minute)), tAssistant(now.Add(-89*time.Minute), "m1", 50_000),
		tUser(now.Add(-20*time.Minute)), tAssistant(now.Add(-19*time.Minute), "m2", 60_000))
	args := []string{"-config", configPath, "-since", "2h", "-transcripts", dir, "-split", cut}
	if err := runContextReport(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "=== Period") != 2 || !strings.Contains(out.String(), "Period 1 of 2") || !strings.Contains(out.String(), "Period 2 of 2") {
		t.Errorf("want two periods:\n%s", out.String())
	}
	if strings.Count(out.String(), "Impact (transcripts") != 2 || strings.Count(out.String(), "Starting size of the conversations") != 2 {
		t.Errorf("transcript sections must be printed per period:\n%s", out.String())
	}

	// A split outside the window changes nothing.
	out.Reset()
	args[len(args)-1] = now.Add(-10 * 24 * time.Hour).Format("2006-01-02")
	if err := runContextReport(context.Background(), args, &out); err != nil || strings.Contains(out.String(), "=== Period") {
		t.Errorf("err %v, output:\n%s", err, out.String())
	}
}

func TestGoldenImpact(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 10, 9, h, m, 0, 0, time.Local) }
	im := ctxreport.Impact{
		Advices: []ctxreport.GivenAdvice{
			{Time: at(13, 50), FollowedBy: ctxreport.KindHandoff, Lead: 1},
			{Time: at(15, 0)},
		},
		Resets: []ctxreport.Reset{
			{Kind: ctxreport.KindHandoff, Time: at(14, 2), Before: 311000, After: 45000, Requests: 38, NotReRead: 10108000,
				PromptTokens: 1900000, CacheRead: 1767000, Advised: true, Lead: 1},
			{Kind: ctxreport.KindCompaction, Time: at(16, 30), Before: 180000, After: 100000, Requests: 62, NotReRead: 4960000,
				PromptTokens: 800000, CacheRead: 744000},
			{Kind: ctxreport.KindCompaction, Time: at(17, 0), Before: 150000},
		},
		NotLinked:  []time.Time{at(18, 0)},
		PromptRead: 41200000,
	}
	var out bytes.Buffer
	printImpact(&out, im)
	checkGolden(t, "context-report-impact.golden", out.String())

	out.Reset()
	printImpact(&out, ctxreport.Impact{})
	checkGolden(t, "context-report-impact-empty.golden", out.String())
}

func TestContextReportImpactFromTranscripts(t *testing.T) {
	now := time.Now()
	configPath := configWith(t, "")
	dir := t.TempDir()
	at := func(m int) time.Time { return now.Add(-time.Duration(120-m) * time.Minute) }
	path := "/repo/.jalon/handoffs/2026-10-09-1402-abcd1234-x.md"
	advice := fmt.Sprintf(`{"type":"attachment","uuid":"adv1","timestamp":%q,"attachment":{"type":"hook_system_message","hookEvent":"UserPromptSubmit","content":"Jalon: context at 311K tokens, a reset proposal follows the answer."}}`, ts(at(2)))
	write := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":"w1","content":[{"type":"tool_use","name":"Write","input":{"file_path":%q}}],"usage":{"input_tokens":5,"cache_read_input_tokens":311000}}}`, ts(at(5)), path)
	writeSession(t, dir, "parent.jsonl",
		tUser(at(0)), tAssistant(at(1), "m1", 250_000),
		advice, tUser(at(3)), tAssistant(at(4), "m2", 300_000), write,
	)
	resume := fmt.Sprintf(`{"type":"user","timestamp":%q,"origin":{"kind":"human"},"message":{"content":"@%s reprends à partir de ce handoff"}}`, ts(at(10)), path)
	lines := []string{resume, tAssistant(at(11), "r1", 45_000)}
	for i := 0; i < 3; i++ {
		lines = append(lines, tUser(at(12+2*i)), tAssistant(at(13+2*i), fmt.Sprintf("r%d", i+2), 50_000))
	}
	writeSession(t, dir, "resumed.jsonl", lines...)

	var out bytes.Buffer
	if err := runContextReport(context.Background(), []string{"-config", configPath, "-since", "3h", "-transcripts", dir}, &out); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	// Before is the parent's last request, 311K; after 45K; 4 requests follow.
	for _, want := range []string{
		"advice given 1: followed by a compaction 0, by a handoff 1, nothing seen 0; lead p50 0 turns",
		"resets 1 (1 advised, 0 unprompted); handoff resumes not linked 0",
		"not re-read thanks to resets about 1.1M prompt tokens",
		"of which after advice about 1.1M",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
	if !regexp.MustCompile(`handoff\s+311K\s+45K\s+4\s+1\.1M\s+yes, lead 0`).MatchString(report) {
		t.Errorf("no row for the handoff:\n%s", report)
	}
	// The simulation cuts the parent at the resume too. Its floor is 250K, so the
	// advisor would not have fired, but the hook did give an advice.
	if !regexp.MustCompile(`handoff\s+311K\s+no advice\s+yes`).MatchString(report) {
		t.Errorf("no simulated row for the handoff:\n%s", report)
	}
	for _, want := range []string{"Simulated advice before each reset", "cannot judge a higher threshold", "Advices no reset followed: 0 of 0"} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q:\n%s", want, report)
		}
	}
	if strings.Index(report, "Impact (transcripts") > strings.Index(report, "Starting size of the conversations") {
		t.Errorf("the Impact section must come first in the period:\n%s", report)
	}
}

// floor is a conversation of project folder, in working directory cwd, that
// started at size at minute m and read read prompt tokens, base of them below it.
func floor(folder, cwd string, m, size int, read, base int64) ctxreport.Floor {
	return ctxreport.Floor{Time: time.Date(2026, 10, 12, 9, m, 0, 0, time.UTC), PromptTokens: size,
		Project: folder, Cwd: cwd, PromptRead: read, FloorRead: base}
}

func TestStartingSizeByProject(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	floors := []ctxreport.Floor{
		// The later conversation of the squad ran in a subfolder: the earliest names it.
		floor("/t/-home-me-projects-squad", "/home/me/projects/squad/sub", 30, 71_000, 213_000, 213_000),
		floor("/t/-home-me-projects-squad", "/home/me/projects/squad", 10, 67_000, 201_000, 201_000),
		floor("/t/-home-me-projects-squad", "", 5, 74_000, 222_000, 222_000),
		floor("/t/-home-me-perso-cairn", "/home/me/perso/cairn", 0, 43_000, 430_000, 86_000),
		floor("/t/-home-me-perso-cairn", "/home/me/perso/cairn", 1, 47_000, 470_000, 94_000),
		// Starts at 70K, then reads 70K, 170K and 270K: 210K of 510K. No cwd.
		floor("/t/-work-app", "", 2, 70_000, 510_000, 210_000),
	}
	var out bytes.Buffer
	printStartingSize(&out, floors)
	checkGolden(t, "context-report-starting-size.golden", out.String())

	out.Reset()
	printStartingSize(&out, floors[len(floors)-1:])
	if want := "  41% of the prompt tokens read by the conversations that started in this period was this starting size (0.2M of 0.5M)"; !strings.Contains(out.String(), want) {
		t.Errorf("report does not contain %q:\n%s", want, out.String())
	}
}

func TestStartingSizeListsTenProjects(t *testing.T) {
	var floors []ctxreport.Floor
	for i := range 12 {
		floors = append(floors, floor(fmt.Sprintf("/t/p%02d", i), "", i, 10_000*(i+1), 1, 1))
	}
	var out bytes.Buffer
	printStartingSize(&out, floors)
	report := out.String()
	if strings.Count(report, " 1 conversation ") != 10 || !strings.Contains(report, "    2 more projects not shown\n") {
		t.Errorf("want the 10 largest and 2 more:\n%s", report)
	}
	if !strings.Contains(report, "    p11  1 conversation  p50 120K") || strings.Contains(report, "p00") || strings.Contains(report, "p01") {
		t.Errorf("want the largest p50 first and the two smallest left out:\n%s", report)
	}
}

func TestContextReportRejectsBadFlags(t *testing.T) {
	configPath := configWith(t, "")
	for name, args := range map[string][]string{
		"bad split":  {"-config", configPath, "-split", "tomorrow-ish"},
		"bad window": {"-config", configPath, "-since", "soon"},
	} {
		if err := runContextReport(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestContextReportConfigFile(t *testing.T) {
	useHome(t) // no ~/.jalon/config.yaml
	dir := t.TempDir()
	now := time.Now()
	writeTranscript(t, dir, tUser(now.Add(-time.Hour)), tAssistant(now.Add(-59*time.Minute), "m1", 210_000))

	t.Run("no config file", func(t *testing.T) {
		var out bytes.Buffer
		if err := runContextReport(context.Background(), []string{"-since", "2h", "-transcripts", dir}, &out); err != nil {
			t.Fatalf("with the default thresholds: %v", err)
		}
	})
	t.Run("explicit missing file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing.yaml")
		var out bytes.Buffer
		err := runContextReport(context.Background(), []string{"-config", missing, "-since", "2h", "-transcripts", dir}, &out)
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("err = %v, want it to name %s", err, missing)
		}
	})
}

func TestContextReportNamesTheLayoutsWhenNoFileIsFound(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runContextReport(context.Background(), []string{"-config", configWith(t, ""), "-transcripts", dir}, &out); err != nil {
		t.Fatal(err)
	}
	want := "  0 files, 0 entries read: 0 used, 0 known and unused, 0 skipped (0.0%); 0 distinct compactions\n" +
		"  no transcript file found: the report reads " + filepath.Join(dir, "<project>", "<session>.jsonl") + " and " + filepath.Join(dir, "<session>.jsonl") + "\n"
	if !strings.Contains(out.String(), want) || strings.Contains(out.String(), "none modified in this window") {
		t.Errorf("report does not contain only %q:\n%s", want, out.String())
	}
}

func TestContextReportSaysNoFileIsInTheWindow(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-3 * time.Hour)
	writeTranscript(t, dir, tUser(old), tAssistant(old.Add(time.Minute), "m1", 40_000))
	if err := os.Chtimes(filepath.Join(dir, "-project", "session.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runContextReport(context.Background(), []string{"-config", configWith(t, ""), "-since", "1h", "-transcripts", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if want := "0 distinct compactions\n  1 transcript file found, none modified in this window\n"; !strings.Contains(out.String(), want) {
		t.Errorf("report does not contain %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "no transcript file found") {
		t.Errorf("a file exists, outside the window:\n%s", out.String())
	}
}

func TestContextReportReadsOneProjectFolder(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	writeTranscript(t, dir, tUser(now.Add(-time.Hour)), tAssistant(now.Add(-59*time.Minute), "m1", 40_000))
	var out bytes.Buffer
	args := []string{"-config", configWith(t, ""), "-since", "2h", "-transcripts", filepath.Join(dir, "-project")}
	if err := runContextReport(context.Background(), args, &out); err != nil {
		t.Fatal(err)
	}
	// No entry has a cwd: the project is named by its folder.
	for _, want := range []string{"1 files", "1 conversations: p10 40K", "    -project  1 conversation  p50 40K  max 40K  100%"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report does not contain %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "no transcript file found") {
		t.Errorf("files were found:\n%s", out.String())
	}
}

func TestContextReportFailsWithoutTranscriptsDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "projects")
	var out bytes.Buffer
	err := runContextReport(context.Background(), []string{"-config", configWith(t, ""), "-transcripts", missing}, &out)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf("err = %v, want it to name %s", err, missing)
	}
	if out.Len() != 0 {
		t.Errorf("the report printed %q before failing", out.String())
	}
}
