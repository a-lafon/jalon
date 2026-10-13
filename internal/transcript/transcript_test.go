package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures in testdata are excerpts of real transcripts with every string
// replaced by x's (at most 600 of them) except the structural fields.
// session.jsonl holds one user turn, a compaction, and a second user turn.
// resumed.jsonl repeats the compaction entry, as a resumed session file does.

func readFixture(t *testing.T, r *Reader, name string) Conversation {
	t.Helper()
	c, err := r.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestReadSessionFixture(t *testing.T) {
	r := NewReader()
	c := readFixture(t, r, "session.jsonl")

	want := Coverage{Files: 1, Read: 140, Interpreted: 54, Ignored: 86, Skipped: 0, Compactions: 1}
	if r.Coverage != want {
		t.Errorf("coverage = %+v, want %+v", r.Coverage, want)
	}
	if len(c.Turns) != 2 {
		t.Fatalf("turns = %+v, want 2", c.Turns)
	}
	// Two prompts have origin kind human, one has kind peer: only people count.
	if c.Human != 2 {
		t.Errorf("human prompts = %d, want 2", c.Human)
	}
	if c.Turns[0].Request != 0 || c.Turns[1].Request != 5 {
		t.Errorf("first request of the turns = %d, %d, want 0 and 5", c.Turns[0].Request, c.Turns[1].Request)
	}
	// A prompt is sized by the first request that answered it.
	if c.Turns[0].PromptTokens != 275841 || c.Turns[1].PromptTokens != 75417 {
		t.Errorf("prompt tokens = %d, %d", c.Turns[0].PromptTokens, c.Turns[1].PromptTokens)
	}
	if got := len(c.Requests); got != 12 {
		t.Errorf("requests = %d, want 12 (one per distinct message id)", got)
	}
	for _, req := range c.Requests {
		if req.PromptTokens == 0 || req.Turn < 0 || req.Turn > 1 {
			t.Fatalf("bad request %+v", req)
		}
	}

	if len(c.Compactions) != 1 {
		t.Fatalf("compactions = %+v", c.Compactions)
	}
	comp := c.Compactions[0]
	if comp.Trigger != "manual" || comp.PreTokens != 283512 {
		t.Errorf("compaction = %+v", comp)
	}
	if comp.AfterTurns != 1 || comp.AfterRequests != 5 {
		t.Errorf("compaction position = %d turns, %d requests, want 1 and 5", comp.AfterTurns, comp.AfterRequests)
	}
	if comp.Time.UTC() != time.Date(2026, 10, 6, 8, 5, 41, 268e6, time.UTC) {
		t.Errorf("compaction time = %v", comp.Time)
	}

	var results, resultBytes int
	for _, b := range c.Blocks {
		if b.ToolResult {
			results++
			resultBytes += b.Bytes
		}
		if b.Bytes <= 0 {
			t.Errorf("block without size: %+v", b)
		}
	}
	if results != 14 || resultBytes == 0 {
		t.Errorf("tool results = %d (%d bytes), want 14", results, resultBytes)
	}
}

func TestCompactionRepeatedAcrossFilesCountsOnce(t *testing.T) {
	r := NewReader()
	first := readFixture(t, r, "session.jsonl")
	second := readFixture(t, r, "resumed.jsonl")
	third := readFixture(t, r, "resumed.jsonl")

	if len(first.Compactions) != 1 || len(second.Compactions) != 0 || len(third.Compactions) != 0 {
		t.Errorf("compactions per file = %d, %d, %d, want 1, 0, 0", len(first.Compactions), len(second.Compactions), len(third.Compactions))
	}
	if r.Coverage.Compactions != 1 || r.Coverage.Files != 3 {
		t.Errorf("coverage = %+v", r.Coverage)
	}
}

func TestCompactionRepeatedInOneFile(t *testing.T) {
	boundary := `{"type":"system","subtype":"compact_boundary","uuid":"u1","timestamp":"2026-10-06T08:00:00Z","compactMetadata":{"trigger":"auto","preTokens":900}}`
	c, err := NewReader().Read("mem", strings.NewReader(boundary+"\n"+boundary+"\n"))
	if err != nil || len(c.Compactions) != 1 || c.Compactions[0].Trigger != "auto" || c.Compactions[0].PreTokens != 900 {
		t.Errorf("compactions = %+v, err %v", c.Compactions, err)
	}
}

func TestUnknownEntriesAreSkippedAndCounted(t *testing.T) {
	lines := []string{
		`{"type":"hologram","uuid":"a","timestamp":"2026-10-06T08:00:00Z"}`, // unknown type
		`not json at all`, // not JSON
		`{"uuid":"b"}`,    // no type
		`{"type":"user","timestamp":"2026-10-06T08:00:01Z","message":{"content":42}}`,                  // content of an unknown shape
		`{"type":"user","timestamp":"yesterday","message":{"content":"hi"}}`,                           // bad timestamp
		`{"type":"system","subtype":"compact_boundary","uuid":"c","timestamp":"2026-10-06T08:00:02Z"}`, // boundary without metadata
		``,
		`{"type":"attachment","uuid":"d"}`, // known and unused
		userText("2026-10-06T08:00:03Z", "hello"), // still read after all of that
		assistant("2026-10-06T08:00:04Z", "m1", 10, 20, 30, 100),
	}
	r := NewReader()
	c, err := r.Read("mem", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	want := Coverage{Files: 1, Read: 9, Interpreted: 2, Ignored: 1, Skipped: 6}
	if r.Coverage != want {
		t.Errorf("coverage = %+v, want %+v", r.Coverage, want)
	}
	if len(c.Turns) != 1 || c.Turns[0].PromptTokens != 60 {
		t.Errorf("turns = %+v, want one of 60 prompt tokens", c.Turns)
	}
	if got := r.Coverage.SkippedShare(); got < 0.66 || got > 0.67 {
		t.Errorf("skipped share = %v", got)
	}
}

func TestTurnsToolResultsAndSidechains(t *testing.T) {
	lines := []string{
		userText("2026-10-06T08:00:00Z", "first"),
		assistant("2026-10-06T08:00:01Z", "m1", 1, 1000, 0, 5),
		assistant("2026-10-06T08:00:01Z", "m1", 1, 1000, 0, 5), // another block of the same request
		toolResult("2026-10-06T08:00:02Z", 300),
		assistant("2026-10-06T08:00:03Z", "m2", 1, 1500, 0, 5),
		userText("2026-10-06T08:00:04Z", "never answered"),
		sidechain(userText("2026-10-06T08:00:05Z", "subagent prompt")),
		sidechain(assistant("2026-10-06T08:00:05Z", "s1", 1, 90000, 0, 5)),
		userText("2026-10-06T08:00:06Z", "second"),
		assistant("2026-10-06T08:00:07Z", "m3", 1, 1600, 400, 5),
		`{"type":"user","isMeta":true,"timestamp":"2026-10-06T08:00:08Z","message":{"content":"meta"}}`,
		`{"type":"assistant","timestamp":"2026-10-06T08:00:09Z","message":{"id":"syn","content":[{"type":"text","text":"x"}],"usage":{"input_tokens":0}}}`,
	}
	r := NewReader()
	c, err := r.Read("mem", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Turns) != 2 || c.Turns[0].PromptTokens != 1001 || c.Turns[1].PromptTokens != 2001 {
		t.Fatalf("turns = %+v", c.Turns)
	}
	if len(c.Requests) != 3 || c.Requests[1].Turn != 0 || c.Requests[2].Turn != 1 {
		t.Errorf("requests = %+v", c.Requests)
	}
	var turnOfResult = -1
	for _, b := range c.Blocks {
		if b.ToolResult {
			turnOfResult = b.Turn
			if b.Bytes < 300 {
				t.Errorf("tool result of %d bytes, want at least its 300 bytes of content", b.Bytes)
			}
		}
	}
	if turnOfResult != 0 {
		t.Errorf("tool result belongs to turn %d, want 0", turnOfResult)
	}
	// The unanswered prompt is not a turn, but its bytes are part of the context.
	if c.Turns[1].Block != 6 {
		t.Errorf("second turn block = %d, want 6", c.Turns[1].Block)
	}
	if r.Coverage.Ignored != 2 || r.Coverage.Skipped != 0 {
		t.Errorf("coverage = %+v, want the two sidechain entries ignored", r.Coverage)
	}
}

func TestReadDirFiltersByModTimeAndSortsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	write := func(project, name string, age time.Duration) {
		t.Helper()
		p := filepath.Join(dir, project)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(p, name)
		if err := os.WriteFile(f, []byte(userText("2026-10-06T08:00:00Z", "x")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mod := time.Now().Add(-age)
		if err := os.Chtimes(f, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("p1", "new.jsonl", time.Hour)
	write("p2", "old.jsonl", 72*time.Hour)
	write("p1", "ignored.txt", time.Hour)
	if err := os.MkdirAll(filepath.Join(dir, "p1", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}

	r := NewReader()
	convs, err := r.ReadDir(dir, time.Now().Add(-24*time.Hour))
	if err != nil || len(convs) != 1 || filepath.Base(convs[0].File) != "new.jsonl" {
		t.Fatalf("recent = %+v, err %v", convs, err)
	}
	if convs[0].Project != filepath.Join(dir, "p1") {
		t.Errorf("project = %q, want the folder of the file", convs[0].Project)
	}
	convs, err = NewReader().ReadDir(dir, time.Time{})
	if err != nil || len(convs) != 2 || filepath.Base(convs[0].File) != "old.jsonl" {
		t.Fatalf("all = %+v, err %v", convs, err)
	}

	// One project's folder holds the session files directly.
	convs, err = NewReader().ReadDir(filepath.Join(dir, "p1"), time.Time{})
	if err != nil || len(convs) != 1 || filepath.Base(convs[0].File) != "new.jsonl" || convs[0].Project != filepath.Join(dir, "p1") {
		t.Fatalf("one project = %+v, err %v", convs, err)
	}
}

func TestCwdIsTheFirstOneRecorded(t *testing.T) {
	withCwd := func(line, cwd string) string {
		return strings.Replace(line, `{"type"`, fmt.Sprintf(`{"cwd":%q,"type"`, cwd), 1)
	}
	for name, tc := range map[string]struct {
		lines []string
		want  string
	}{
		"first entry with one": {[]string{
			`{"type":"queue-operation","timestamp":"2026-10-06T07:59:00Z"}`,
			withCwd(userText("2026-10-06T08:00:00Z", "x"), "/work/app"),
			withCwd(assistant("2026-10-06T08:00:01Z", "m1", 1, 100, 0, 5), "/work/app/sub"),
		}, "/work/app"},
		"none": {[]string{
			userText("2026-10-06T08:00:00Z", "x"),
			assistant("2026-10-06T08:00:01Z", "m1", 1, 100, 0, 5),
		}, ""},
	} {
		c, err := NewReader().Read("/p/s1.jsonl", strings.NewReader(strings.Join(tc.lines, "\n")))
		if err != nil || c.Cwd != tc.want || c.Project != "/p" {
			t.Errorf("%s: cwd %q, project %q, err %v, want cwd %q in /p", name, c.Cwd, c.Project, err, tc.want)
		}
	}
}

func advice(uuid, ts, content string) string {
	return fmt.Sprintf(`{"type":"attachment","uuid":%q,"timestamp":%q,"sessionId":"s1","attachment":{"type":"hook_system_message","content":%q,"hookName":"UserPromptSubmit","hookEvent":"UserPromptSubmit"}}`,
		uuid, ts, content)
}

func humanText(ts, text string) string {
	return strings.Replace(userText(ts, text), `{"type"`, `{"origin":{"kind":"human"},"type"`, 1)
}

func TestAdviceIsReadOnceAcrossFiles(t *testing.T) {
	lines := []string{
		humanText("2026-10-06T08:00:00Z", "first"),
		assistant("2026-10-06T08:00:01Z", "m1", 1, 1000, 0, 5),
		advice("adv1", "2026-10-06T08:00:02Z", "Jalon: context at 211K tokens, a reset proposal follows the answer."),
		humanText("2026-10-06T08:00:02Z", "second"),
		advice("other", "2026-10-06T08:00:03Z", "Another hook: lint passed."),
		sidechain(advice("side", "2026-10-06T08:00:03Z", "Jalon: context at 300K tokens, a reset proposal follows the answer.")),
		`{"type":"attachment","uuid":"f1","timestamp":"2026-10-06T08:00:03Z","attachment":{"type":"file","filename":"/x","content":{"file":{}}}}`,
		assistant("2026-10-06T08:00:04Z", "m2", 1, 2000, 0, 5),
	}
	r := NewReader()
	first, err := r.Read("/p/s1.jsonl", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Advices) != 1 || first.Advices[0].UUID != "adv1" || first.Advices[0].Turn != 1 || first.Advices[0].Request != 1 {
		t.Fatalf("advices = %+v, want adv1 at turn 1 after 1 request", first.Advices)
	}
	// The advice is interpreted; the other hook's message, the sidechain and the
	// file attachment are known and unused, as attachments were before.
	want := Coverage{Files: 1, Read: 8, Interpreted: 5, Ignored: 3}
	if r.Coverage != want {
		t.Errorf("coverage = %+v, want %+v", r.Coverage, want)
	}
	if first.SessionID != "s1" || len(first.Sessions) != 1 || first.Sessions[0] != "s1" {
		t.Errorf("session = %q, %v", first.SessionID, first.Sessions)
	}

	second, err := r.Read("/p/s2.jsonl", strings.NewReader(lines[2]))
	if err != nil || len(second.Advices) != 0 {
		t.Errorf("advice repeated in a resumed file = %+v, err %v, want none", second.Advices, err)
	}
	if r.Coverage.Interpreted != 5 || r.Coverage.Ignored != 4 {
		t.Errorf("coverage = %+v, want the repeated advice ignored", r.Coverage)
	}
}

func TestResumeIsTheFirstHumanPrompt(t *testing.T) {
	path := "/home/me/repo/.jalon/handoffs/2026-10-09-1402-abcd1234-x.md"
	lines := []string{
		userText("2026-10-06T07:59:00Z", "@"+path+" not typed by a person"),
		humanText("2026-10-06T08:00:00Z", "@"+path+" reprends à partir de ce handoff"),
		assistant("2026-10-06T08:00:01Z", "m1", 1, 45000, 0, 5),
	}
	c, err := NewReader().Read("mem", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if c.Resume == nil || c.Resume.Path != path || c.Resume.Turn != 0 || c.Resume.Request != 0 ||
		!c.Resume.Time.Equal(time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("resume = %+v", c.Resume)
	}

	// A path quoted in a later prompt is not a resume.
	later := []string{
		humanText("2026-10-06T08:00:00Z", "hello"),
		assistant("2026-10-06T08:00:01Z", "m1", 1, 45000, 0, 5),
		humanText("2026-10-06T08:00:02Z", "pasted: @"+path+" reprends"),
	}
	c, err = NewReader().Read("mem", strings.NewReader(strings.Join(later, "\n")))
	if err != nil || c.Resume != nil {
		t.Errorf("resume = %+v, err %v, want none", c.Resume, err)
	}
}

func TestHandoffWritesOfTheMainThread(t *testing.T) {
	write := func(ts, path string) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":"w-%s","content":[{"type":"tool_use","id":"t","name":"Write","input":{"file_path":%q,"content":"Session: s1"}}],`+
			`"usage":{"input_tokens":1,"cache_read_input_tokens":100,"cache_creation_input_tokens":0}}}`, ts, ts, path)
	}
	lines := []string{
		humanText("2026-10-06T08:00:00Z", "go"),
		write("2026-10-06T08:00:01Z", "/r/.jalon/handoffs/a.md"),
		write("2026-10-06T08:00:02Z", "/r/docs/notes.md"),
		sidechain(write("2026-10-06T08:00:03Z", "/r/.jalon/handoffs/b.md")),
	}
	c, err := NewReader().Read("mem", strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Writes) != 1 || c.Writes[0].Path != "/r/.jalon/handoffs/a.md" {
		t.Errorf("writes = %+v, want only the handoff of the main thread", c.Writes)
	}
	if len(c.Requests) != 2 || c.Requests[0].CacheRead != 100 {
		t.Errorf("requests = %+v, want the cache read kept", c.Requests)
	}
}

func userText(ts, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":"u-%s","timestamp":%q,"message":{"role":"user","content":[{"type":"text","text":%q}]}}`, ts, ts, text)
}

func toolResult(ts string, size int) string {
	return fmt.Sprintf(`{"type":"user","uuid":"r-%s","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":%q}]}}`,
		ts, ts, strings.Repeat("x", size))
}

func assistant(ts, id string, input, cacheRead, cacheWrite, output int) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"a-%s-%s","timestamp":%q,"message":{"id":%q,"role":"assistant","content":[{"type":"text","text":"x"}],`+
		`"usage":{"input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"output_tokens":%d}}}`,
		id, ts, ts, id, input, cacheRead, cacheWrite, output)
}

func sidechain(line string) string {
	return strings.Replace(line, `{"type"`, `{"isSidechain":true,"type"`, 1)
}
