package ctxreport

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/transcript"
)

const handoffPath = "/r/.jalon/handoffs/2026-10-06-0800-abcd1234-x.md"

// shifted moves every time of a conversation by d.
func shifted(c transcript.Conversation, d time.Duration) transcript.Conversation {
	c.Turns, c.Requests = slices.Clone(c.Turns), slices.Clone(c.Requests)
	for i := range c.Turns {
		c.Turns[i].Time = c.Turns[i].Time.Add(d)
	}
	for i := range c.Requests {
		c.Requests[i].Time = c.Requests[i].Time.Add(d)
	}
	return c
}

// advise records an advice given at turn i of c, as the reader would.
func advise(c *transcript.Conversation, i int) {
	c.Advices = append(c.Advices, transcript.Advice{UUID: c.File + string(rune('a'+i)), Time: c.Turns[i].Time.Add(-time.Second), Turn: i, Request: c.Turns[i].Request})
}

// resumed is a conversation that starts at at by resuming from path, one hour
// per turn.
func resumed(file, path string, at time.Time, prompts ...int) transcript.Conversation {
	c := shifted(conv(file, 100, prompts...), at.Sub(t0))
	c.Resume = &transcript.Resume{Path: path, Time: at, Turn: 0, Request: 0}
	return c
}

// advisedParent gives an advice at 311K at turn 1, one more turn, then writes
// the handoff.
func advisedParent() transcript.Conversation {
	p := conv("p", 100, 200000, 311000, 311000)
	advise(&p, 1)
	p.Writes = []transcript.Write{{Path: handoffPath, Time: t0.Add(2*time.Hour + 30*time.Minute)}}
	return p
}

func TestAdvisedHandoff(t *testing.T) {
	p := advisedParent()
	r := resumed("r", handoffPath, t0.Add(3*time.Hour), slices.Repeat([]int{45000}, 38)...)
	im := Impacts([]transcript.Conversation{p, r})

	if len(im.Resets) != 1 {
		t.Fatalf("resets = %+v", im.Resets)
	}
	got := im.Resets[0]
	if got.Kind != KindHandoff || got.File != "p" || got.Into != "r" || got.Before != 311000 || got.After != 45000 || got.Requests != 38 {
		t.Errorf("reset = %+v", got)
	}
	if got.NotReRead != 10108000 || !got.Advised || got.Lead != 1 {
		t.Errorf("not re-read %d, advised %v, lead %d, want 10108000, advised with a lead of 1", got.NotReRead, got.Advised, got.Lead)
	}
	if len(im.Advices) != 1 || im.Advices[0].FollowedBy != KindHandoff || im.Advices[0].Lead != 1 {
		t.Errorf("advices = %+v", im.Advices)
	}
	if im.PromptRead != 200000+311000+311000+38*45000 || len(im.NotLinked) != 0 {
		t.Errorf("prompt read %d, not linked %v", im.PromptRead, im.NotLinked)
	}
}

func TestAdviceNotFollowed(t *testing.T) {
	// Advice at turns 1 and 3, a compaction after turn 4: only the second advice
	// is followed.
	c := conv("a", 100, 100000, 210000, 250000, 320000, 330000, 40000)
	advise(&c, 1)
	advise(&c, 3)
	compactAfter(&c, 5, "manual", 330000)
	im := Impacts([]transcript.Conversation{c})
	if len(im.Advices) != 2 || im.Advices[0].FollowedBy != "" || im.Advices[1].FollowedBy != KindCompaction || im.Advices[1].Lead != 1 {
		t.Errorf("advices = %+v", im.Advices)
	}
	if len(im.Resets) != 1 || !im.Resets[0].Advised || im.Resets[0].Lead != 1 {
		t.Errorf("resets = %+v", im.Resets)
	}
}

func TestUnpromptedCompaction(t *testing.T) {
	c := conv("a", 100, 100000, 180000, 30000, 35000)
	compactAfter(&c, 2, "manual", 180000)
	im := Impacts([]transcript.Conversation{c})
	if len(im.Resets) != 1 {
		t.Fatalf("resets = %+v", im.Resets)
	}
	got := im.Resets[0]
	if got.Kind != KindCompaction || got.Advised || got.Before != 180000 || got.After != 30000 || got.Requests != 2 || got.NotReRead != 300000 {
		t.Errorf("reset = %+v, want unprompted, (180K - 30K) x 2", got)
	}
	if got.PromptTokens != 65000 || got.CacheRead != 0 {
		t.Errorf("tokens in N = %d, cache read %d", got.PromptTokens, got.CacheRead)
	}
}

func TestCompactionThatGrowsTheContext(t *testing.T) {
	c := conv("a", 100, 100000, 120000)
	compactAfter(&c, 1, "manual", 90000)
	im := Impacts([]transcript.Conversation{c})
	if len(im.Resets) != 1 || im.Resets[0].NotReRead != 0 || im.Resets[0].After != 120000 {
		t.Errorf("resets = %+v, want one listed with 0 not re-read", im.Resets)
	}
}

func TestDeletedHandoffStillLinkedThroughItsWrite(t *testing.T) {
	// handoffPath does not exist on disk.
	if _, err := os.Stat(handoffPath); err == nil {
		t.Skip(handoffPath + " exists")
	}
	im := Impacts([]transcript.Conversation{advisedParent(), resumed("r", handoffPath, t0.Add(3*time.Hour), 45000)})
	if len(im.Resets) != 1 || im.Resets[0].File != "p" || len(im.NotLinked) != 0 {
		t.Errorf("resets = %+v, not linked %v", im.Resets, im.NotLinked)
	}
}

func TestWriteAfterTheResumeDoesNotLink(t *testing.T) {
	p := advisedParent()
	p.Writes[0].Time = t0.Add(4 * time.Hour)
	im := Impacts([]transcript.Conversation{p, resumed("r", handoffPath, t0.Add(3*time.Hour), 45000)})
	if len(im.Resets) != 0 || len(im.NotLinked) != 1 {
		t.Errorf("resets = %+v, not linked %v", im.Resets, im.NotLinked)
	}
}

func TestLinkedThroughTheSessionLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".jalon", "handoffs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	byFile := write("a.md", "Follows: x.md\nSession: s-parent\n# Handoff\n")
	byEntries := write("b.md", "Session: s-entries\n")
	none := write("c.md", "# Handoff, no session line\n")

	p := conv("/projects/x/s-parent.jsonl", 100, 250000)
	p.SessionID = "s-parent"
	q := shifted(conv("/projects/x/renamed.jsonl", 100, 260000), time.Hour)
	q.SessionID, q.Sessions = "renamed", []string{"s-entries"}
	start := t0.Add(5 * time.Hour)
	im := Impacts([]transcript.Conversation{
		p, q,
		resumed("r1", byFile, start, 40000),
		resumed("r2", byEntries, start.Add(time.Hour), 40000),
		resumed("r3", none, start.Add(2*time.Hour), 40000),
		resumed("r4", filepath.Join(dir, "missing.md"), start.Add(3*time.Hour), 40000),
	})
	if len(im.Resets) != 2 || im.Resets[0].File != p.File || im.Resets[1].File != q.File {
		t.Errorf("resets = %+v, want r1 linked by file name and r2 by session ID", im.Resets)
	}
	if len(im.NotLinked) != 2 {
		t.Errorf("not linked = %v, want r3 and r4", im.NotLinked)
	}
}

func TestHandoffPathQuotedLaterIsNotAResume(t *testing.T) {
	// Read through the reader: only the first human prompt can be a resume.
	lines := []string{
		`{"type":"user","origin":{"kind":"human"},"timestamp":"2026-10-06T08:00:00Z","message":{"content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-10-06T08:00:01Z","message":{"id":"m1","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":1,"cache_read_input_tokens":100000}}}`,
		`{"type":"user","origin":{"kind":"human"},"timestamp":"2026-10-06T08:00:02Z","message":{"content":"pasted: @` + handoffPath + ` reprends"}}`,
		`{"type":"assistant","timestamp":"2026-10-06T08:00:03Z","message":{"id":"m2","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":1,"cache_read_input_tokens":110000}}}`,
	}
	c, err := transcript.NewReader().Read("q", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	im := Impacts([]transcript.Conversation{advisedParent(), c})
	if len(im.Resets) != 0 || len(im.NotLinked) != 0 {
		t.Errorf("resets = %+v, not linked %v, want none", im.Resets, im.NotLinked)
	}
}

func TestHandoffResumedTwice(t *testing.T) {
	p := advisedParent()
	r1 := resumed("r1", handoffPath, t0.Add(3*time.Hour), 45000, 50000)
	r2 := resumed("r2", handoffPath, t0.Add(4*time.Hour), 60000, 65000, 70000)
	im := Impacts([]transcript.Conversation{p, r1, r2})
	if len(im.Resets) != 2 {
		t.Fatalf("resets = %+v", im.Resets)
	}
	a, b := im.Resets[0], im.Resets[1]
	if a.Into != "r1" || a.After != 45000 || a.Requests != 2 || a.NotReRead != 2*266000 {
		t.Errorf("first = %+v", a)
	}
	if b.Into != "r2" || b.Before != 311000 || b.After != 60000 || b.Requests != 3 || b.NotReRead != 3*251000 {
		t.Errorf("second = %+v", b)
	}
	if !a.Advised || !b.Advised || len(im.Advices) != 1 {
		t.Errorf("both resumes answer the one advice: %+v, %+v", a, b)
	}
}

func TestCompactionOfTheResumedConversationEndsN(t *testing.T) {
	p := advisedParent()
	r := resumed("r", handoffPath, t0.Add(3*time.Hour), 45000, 90000, 150000, 30000, 35000)
	compactAfter(&r, 3, "auto", 150000)
	r.Compactions[0].Time = r.Turns[2].Time.Add(time.Minute)
	im := Impacts([]transcript.Conversation{p, r})
	if len(im.Resets) != 2 {
		t.Fatalf("resets = %+v", im.Resets)
	}
	h, c := im.Resets[0], im.Resets[1]
	if h.Kind != KindHandoff || h.Requests != 3 || h.NotReRead != 3*266000 {
		t.Errorf("handoff = %+v, want N 3: the compaction ends it", h)
	}
	if c.Kind != KindCompaction || c.Requests != 2 || c.After != 30000 || c.Advised {
		t.Errorf("compaction = %+v", c)
	}
}

func TestImpactBetween(t *testing.T) {
	p := advisedParent()
	r := resumed("r", handoffPath, t0.Add(3*time.Hour), 45000, 45000)
	im := Impacts([]transcript.Conversation{p, r})
	early := im.Between(t0, t0.Add(150*time.Minute))
	if len(early.Advices) != 1 || len(early.Resets) != 0 || early.PromptRead != 200000+311000+311000 {
		t.Errorf("early = %+v", early)
	}
	late := im.Between(t0.Add(150*time.Minute), t0.Add(24*time.Hour))
	if len(late.Advices) != 0 || len(late.Resets) != 1 || late.PromptRead != 90000 {
		t.Errorf("late = %+v", late)
	}
}

func TestImpactLeavesOutProgramDrivenConversations(t *testing.T) {
	c := conv("script", 100, 180000, 30000)
	c.Human = 0
	compactAfter(&c, 1, "manual", 180000)
	im := Impacts([]transcript.Conversation{c})
	if len(im.Resets) != 0 || im.PromptRead != 0 {
		t.Errorf("impact = %+v", im)
	}
}
