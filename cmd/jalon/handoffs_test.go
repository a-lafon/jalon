package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handoffProject makes a git project whose handoff folder holds files, by their
// path relative to it, and moves the test into its sub folder.
func handoffProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	folder := filepath.Join(root, ".jalon", "handoffs")
	for _, d := range []string{filepath.Join(root, ".git"), filepath.Join(root, "sub")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range files {
		p := filepath.Join(folder, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(root, "sub"))
	return folder
}

func handoffsOut(t *testing.T, args ...string) string {
	t.Helper()
	var out strings.Builder
	if err := runHandoffs(args, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

var threadFiles = map[string]string{
	"2026-10-09/0900-aaaaaaaa-start.md":  "Session: s1\n\n# Tokenizer split\nfirst\n",
	"2026-10-10/0930-aaaaaaaa-keys.md":   "Follows: 2026-10-09/0900-aaaaaaaa-start.md\nSession: s2\n\n# Keyword lookup\nsecond\n",
	"2026-10-10/1200-bbbbbbbb-typos.md":  "Session: s3\n\n# Docs typos\nother\n",
	"2026-10-11/0800-aaaaaaaa-parser.md": "Follows: 2026-10-10/0930-aaaaaaaa-keys.md\nSession: s4\n\n# Parser\nthird\n",
	"2026-10-08-1000-cccccccc-flat.md":   "# Flat name\n",
	"2026-10-12/0700-dddddddd-broken.md": "Follows: 2026-10-01/0100-dddddddd-gone.md\nSession: s5\n\n# Broken link\nfourth\n",
	"2026-10-12/0800-eeeeeeee-picked.md": "Session: s6\n\n# Latest\nlatest\n",
}

func TestHandoffsPicksTheLatestAndNamesTheTwoBefore(t *testing.T) {
	handoffProject(t, threadFiles)
	out := handoffsOut(t)
	for _, want := range []string{
		"Handoff: 2026-10-12/0800-eeeeeeee-picked.md\n",
		"Before it: 2026-10-12/0700-dddddddd-broken.md, 2026-10-11/0800-aaaaaaaa-parser.md\n",
		"=== 2026-10-12/0800-eeeeeeee-picked.md\nSession: s6\n\n# Latest\nlatest\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "second") {
		t.Errorf("printed another handoff:\n%s", out)
	}
}

func TestHandoffsPrintsTheHandoffItFollows(t *testing.T) {
	handoffProject(t, threadFiles)
	out := handoffsOut(t, "parser")
	for _, want := range []string{
		"Handoff: 2026-10-11/0800-aaaaaaaa-parser.md\n",
		"=== 2026-10-11/0800-aaaaaaaa-parser.md\n",
		"=== 2026-10-10/0930-aaaaaaaa-keys.md, the handoff it follows\n",
		"second\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "first") {
		t.Errorf("printed past the parent:\n%s", out)
	}
}

func TestHandoffsWithoutAParentToPrint(t *testing.T) {
	handoffProject(t, threadFiles)
	// The first of a thread: its own text alone, as before.
	if out := handoffsOut(t, "flat"); strings.Count(out, "\n=== ") != 1 {
		t.Errorf("printed more than the handoff:\n%s", out)
	}
	out := handoffsOut(t, "broken")
	if !strings.Contains(out, "Goes on in 2026-10-01/0100-dddddddd-gone.md, which 2026-10-12/0700-dddddddd-broken.md names and which is not in the folder.\n") ||
		strings.Count(out, "\n=== ") != 1 {
		t.Errorf("missing parent:\n%s", out)
	}
}

func TestHandoffsLeavesOutAParentOverTheLimit(t *testing.T) {
	pad := strings.Repeat("x", threadPrintLimit)
	handoffProject(t, map[string]string{
		"2026-10-10/0100-aaaaaaaa-big.md":  "# Big\n" + pad + "\n",
		"2026-10-10/0200-aaaaaaaa-next.md": "Follows: 2026-10-10/0100-aaaaaaaa-big.md\n\n# Next\nhere\n",
	})
	out := handoffsOut(t)
	if !strings.Contains(out, "Not printed, as the thread is over 100 KB: 2026-10-10/0100-aaaaaaaa-big.md.\n") ||
		!strings.Contains(out, "here\n") || strings.Count(out, "\n=== ") != 1 {
		t.Errorf("got:\n%s", firstLines(out, 6))
	}
}

func TestHandoffsPicksByPathOrWords(t *testing.T) {
	folder := handoffProject(t, threadFiles)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{filepath.Join(folder, "2026-10-10", "1200-bbbbbbbb-typos.md")}, "Handoff: 2026-10-10/1200-bbbbbbbb-typos.md\n"},
		{[]string{"2026-10-10/0930-aaaaaaaa-keys.md"}, "Handoff: 2026-10-10/0930-aaaaaaaa-keys.md\n"},
		{[]string{"keyword", "LOOKUP"}, "Handoff: 2026-10-10/0930-aaaaaaaa-keys.md\n"},
		{[]string{"typos"}, "Handoff: 2026-10-10/1200-bbbbbbbb-typos.md\n"},
		{[]string{"flat"}, "Before it: none\n"},
	} {
		if out := handoffsOut(t, tc.args...); !strings.Contains(out, tc.want) || strings.Contains(out, "No handoff matched") {
			t.Errorf("%q: want %q in:\n%s", tc.args, tc.want, out)
		}
	}
	out := handoffsOut(t, "zebra")
	if !strings.Contains(out, `No handoff matched "zebra": the latest one is used.`) || !strings.Contains(out, "Handoff: 2026-10-12/0800-eeeeeeee-picked.md\n") {
		t.Errorf("no match:\n%s", out)
	}
}

// A handoff path copied from another checkout matches nothing here: it must
// not fall through to this project's latest handoff, which /jalon-resume would
// then feed to the model as the work to resume.
func TestHandoffsRefusesAPathOutsideTheProject(t *testing.T) {
	other := filepath.Join(t.TempDir(), "elsewhere.md")
	if err := os.WriteFile(other, []byte("# Another project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := handoffProject(t, threadFiles)
	var out strings.Builder
	err := runHandoffs([]string{other}, &out)
	if err == nil || !strings.Contains(err.Error(), other) {
		t.Fatalf("err = %v, want it to name %s", err, other)
	}
	if out.String() != "" {
		t.Errorf("printed a handoff anyway:\n%s", out.String())
	}
	// A path that does not exist at all still falls through to the word match.
	if got := handoffsOut(t, filepath.Join(folder, "..", "gone.md")); !strings.Contains(got, "No handoff matched") {
		t.Errorf("missing file: want the fall-through, got:\n%s", got)
	}
}

func TestHandoffsWithoutHandoff(t *testing.T) {
	folder := handoffProject(t, nil)
	if out := handoffsOut(t, "-thread"); out != "No handoff in "+folder+".\n" {
		t.Errorf("got %q", out)
	}
}

func TestHandoffsThreadFollowsTheChainOnly(t *testing.T) {
	handoffProject(t, threadFiles)
	out := handoffsOut(t, "-thread", "parser")
	if !strings.Contains(out, "Thread: 3 handoffs, 1 KB, from 2026-10-09/0900-aaaaaaaa-start.md to 2026-10-11/0800-aaaaaaaa-parser.md.\n") {
		t.Errorf("header:\n%s", out)
	}
	first, second, third := strings.Index(out, "first"), strings.Index(out, "second"), strings.Index(out, "third")
	if first < 0 || first > second || second > third {
		t.Errorf("not first to last:\n%s", out)
	}
	if strings.Contains(out, "other") || strings.Contains(out, "Not printed") {
		t.Errorf("printed outside the chain or skipped:\n%s", out)
	}
}

func TestHandoffsThreadStopsAtAMissingFile(t *testing.T) {
	handoffProject(t, threadFiles)
	out := handoffsOut(t, "-thread", "broken")
	for _, want := range []string{
		"Thread: 1 handoff, 1 KB, 2026-10-12/0700-dddddddd-broken.md: it starts the thread.\n",
		"Goes on in 2026-10-01/0100-dddddddd-gone.md, which 2026-10-12/0700-dddddddd-broken.md names and which is not in the folder.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHandoffsThreadOverTheLimitPrintsFirstAndLastFive(t *testing.T) {
	files := map[string]string{}
	pad := strings.Repeat("x", threadPrintLimit/7)
	prev := ""
	for i := range 8 {
		name := "2026-10-10/0" + string(rune('1'+i)) + "00-aaaaaaaa-step.md"
		text := "# step " + string(rune('1'+i)) + "\n" + pad + "\n"
		if prev != "" {
			text = "Follows: " + prev + "\n" + text
		}
		files[name], prev = text, name
	}
	handoffProject(t, files)
	out := handoffsOut(t, "-thread")
	if !strings.Contains(out, "Thread: 8 handoffs,") ||
		!strings.Contains(out, "Not printed, as the thread is over 100 KB: 2026-10-10/0200-aaaaaaaa-step.md, 2026-10-10/0300-aaaaaaaa-step.md.\n") {
		t.Errorf("header:\n%s", firstLines(out, 4))
	}
	if got := strings.Count(out, "\n=== "); got != 6 {
		t.Errorf("printed %d handoffs, want 6", got)
	}
	if strings.Contains(out, "# step 2\n") {
		t.Error("printed a skipped handoff")
	}
}

func TestHandoffsThreadLoopEnds(t *testing.T) {
	handoffProject(t, map[string]string{
		"a.md": "Follows: b.md\n",
		"b.md": "Follows: a.md\n",
	})
	if out := handoffsOut(t, "-thread"); !strings.Contains(out, "Thread: 2 handoffs") {
		t.Errorf("got:\n%s", out)
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	return strings.Join(lines[:min(n, len(lines))], "\n")
}
