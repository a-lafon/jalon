package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/client"
)

func sizeLine(size int, sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"message":{"role":"assistant","usage":{"input_tokens":1,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":5}}}`,
		sidechain, size/2, size-size/2-1)
}

const (
	compact       = `{"type":"system","subtype":"compact_boundary","isSidechain":false,"compactMetadata":{"trigger":"manual","preTokens":283512,"postTokens":22246}}`
	compactNoPost = `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto"}}`
	userLine      = `{"type":"user","message":{"role":"user","content":"hello"}}`
	filler        = `{"type":"attachment","text":"` + "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" + `"}`
)

func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContextSize(t *testing.T) {
	tests := map[string]struct {
		lines []string
		want  int
		ok    bool
	}{
		"last assistant entry":           {[]string{sizeLine(50_000, false), userLine, sizeLine(212_000, false), userLine}, 212_000, true},
		"sidechain entries are ignored":  {[]string{sizeLine(212_000, false), sizeLine(9_000, true), userLine}, 212_000, true},
		"only sidechain entries":         {[]string{sizeLine(9_000, true)}, 0, false},
		"compaction after the assistant": {[]string{sizeLine(283_000, false), compact, userLine}, 22_246, true},
		"assistant after the compaction": {[]string{sizeLine(283_000, false), compact, sizeLine(40_000, false)}, 40_000, true},
		"compaction without postTokens":  {[]string{sizeLine(283_000, false), compactNoPost}, 0, false},
		"bad lines are skipped":          {[]string{sizeLine(100_000, false), `{"type":"assistant","message":{"usage":`, `not json "usage"`}, 100_000, true},
		"empty usage is not a size":      {[]string{sizeLine(100_000, false), `{"type":"assistant","message":{"usage":{"input_tokens":0}}}`}, 100_000, true},
		"no relevant entry":              {[]string{userLine, filler}, 0, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := sizeOf(writeLines(t, tt.lines...))
			if got != tt.want || ok != tt.ok {
				t.Errorf("size = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCacheTTL(t *testing.T) {
	writes := func(fiveMin, oneHour int, sidechain bool) string {
		return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":%d,"cache_read_input_tokens":90000,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
			sidechain, fiveMin+oneHour, fiveMin, oneHour)
	}
	tests := map[string]struct {
		lines []string
		want  time.Duration
	}{
		"one hour":                       {[]string{writes(0, 800, false)}, time.Hour},
		"five minutes":                   {[]string{writes(800, 0, false)}, 5 * time.Minute},
		"both: the shorter":              {[]string{writes(10, 800, false)}, 5 * time.Minute},
		"nothing written":                {[]string{writes(0, 0, false)}, 0},
		"no cache_creation":              {[]string{sizeLine(100_000, false)}, 0},
		"from the entry that gives size": {[]string{writes(800, 0, false), writes(0, 800, false)}, time.Hour},
		"sidechain ignored":              {[]string{writes(0, 800, false), writes(800, 0, true)}, time.Hour},
		"after a compaction":             {[]string{writes(0, 800, false), compact}, 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := readTail(writeLines(t, tt.lines...), "", false, false, "").CacheTTL; got != tt.want {
				t.Errorf("CacheTTL = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContextSizeMissingOrEmptyFile(t *testing.T) {
	if _, ok := sizeOf(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("a missing file has no size")
	}
	if _, ok := sizeOf(""); ok {
		t.Error("an empty path has no size")
	}
	empty := filepath.Join(t.TempDir(), "e.jsonl")
	os.WriteFile(empty, nil, 0o600)
	if _, ok := sizeOf(empty); ok {
		t.Error("an empty file has no size")
	}
}

// bigFile writes head, then filler lines up to about total bytes, then tail.
func bigFile(t *testing.T, head []string, total int, tail []string) string {
	t.Helper()
	var b strings.Builder
	for _, l := range head {
		b.WriteString(l + "\n")
	}
	for b.Len() < total {
		b.WriteString(filler + "\n")
	}
	for _, l := range tail {
		b.WriteString(l + "\n")
	}
	path := filepath.Join(t.TempDir(), "big.jsonl")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContextSizeExtendsBackwardsWhenTheTailHasNone(t *testing.T) {
	// The last entry with a size is about 3 MB from the end: beyond the first 1 MB.
	path := bigFile(t, []string{sizeLine(333_000, false)}, 3<<20, nil)
	if got, ok := sizeOf(path); !ok || got != 333_000 {
		t.Errorf("size = %d, %v; want 333000 found beyond the first 1 MB", got, ok)
	}
}

func TestContextSizeGivesUpBeyond8MB(t *testing.T) {
	path := bigFile(t, []string{sizeLine(333_000, false)}, 9<<20, nil)
	if got, ok := sizeOf(path); ok {
		t.Errorf("size = %d, want none past the 8 MB limit", got)
	}
}

func TestContextSizeOfALargeTranscriptReadsOnlyTheLastMB(t *testing.T) {
	// A 10 MB transcript whose last entries are at the end, as in practice.
	path := bigFile(t, []string{sizeLine(1_000, false)}, 10<<20, []string{sizeLine(250_000, false), userLine})
	saved, read := readAt, 0
	readAt = func(f *os.File, b []byte, off int64) (int, error) {
		n, err := saved(f, b, off)
		read += n
		return n, err
	}
	t.Cleanup(func() { readAt = saved })
	got, ok := sizeOf(path)
	if !ok || got != 250_000 {
		t.Fatalf("size = %d, %v", got, ok)
	}
	if read > tailStart {
		t.Errorf("read %d bytes of a 10 MB transcript, want at most the last %d", read, tailStart)
	}
}

// sizeOf is the size readTail finds, and whether it found one.
func sizeOf(path string) (int, bool) {
	tail := readTail(path, "", false, false, "")
	return tail.Size, tail.Size > 0
}

// typedLine is a prompt entry as Claude Code writes it: origin kind "human" when
// a person typed it, no origin when claude -p sent it.
func typedLine(promptID string, human, sidechain bool) string {
	origin := ""
	if human {
		origin = `"origin":{"kind":"human"},`
	}
	return fmt.Sprintf(`{"type":"user",%s"isSidechain":%t,"promptId":%q,"message":{"role":"user","content":"a human request"}}`, origin, sidechain, promptID)
}

func TestReadTailTyped(t *testing.T) {
	toolResult := `{"type":"user","promptId":"p1","message":{"role":"user","content":[{"type":"tool_result","content":"\"human\""}]}}`
	tests := map[string]struct {
		lines             []string
		anyTyped, p1Typed bool
	}{
		"typed prompt":             {[]string{typedLine("p1", true, false), sizeLine(60_000, false)}, true, true},
		"claude -p prompt":         {[]string{typedLine("p1", false, false), toolResult, sizeLine(60_000, false)}, false, false},
		"typed earlier, -p now":    {[]string{typedLine("p0", true, false), sizeLine(50_000, false), typedLine("p1", false, false), sizeLine(60_000, false)}, true, false},
		"typed in a subagent only": {[]string{typedLine("p1", true, true), sizeLine(60_000, false)}, false, false},
		"typed after the size":     {[]string{sizeLine(60_000, false), typedLine("p1", true, false)}, true, true},
		"typed, no size":           {[]string{typedLine("p1", true, false)}, true, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeLines(t, tt.lines...)
			if got := readTail(path, "", true, true, ""); got.Typed != tt.anyTyped {
				t.Errorf("any prompt typed = %v, want %v", got.Typed, tt.anyTyped)
			}
			if got := readTail(path, "", false, false, ""); got.Typed {
				t.Error("typed found without being looked for")
			}
			if got := readTail(path, "p1", true, true, ""); got.Typed != tt.p1Typed {
				t.Errorf("p1 typed = %v, want %v", got.Typed, tt.p1Typed)
			}
		})
	}
}

// sectionLine is an assistant entry whose content blocks hold the section; the
// handoff write that may follow it is a tool_use block, with no text.
func sectionLine(sidechain bool) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":%t,"message":{"role":"assistant","content":[{"type":"text","text":"Voilà.\n\n> **Jalon** — Handoff : …"}]}}`, sidechain)
}

func TestReadTailSection(t *testing.T) {
	const instruction = `{"type":"user","promptId":"p1","message":{"role":"user","content":"Jalon: … whose first words are\n**Jalon** —, in the language the user writes in"}}`
	tests := map[string]struct {
		lines []string
		want  bool
	}{
		"the reply ends with it":        {[]string{typedLine("p1", true, false), sectionLine(false), sizeLine(60_000, false)}, true},
		"the handoff file follows it":   {[]string{typedLine("p1", true, false), sectionLine(false), sizeLine(60_000, false), sizeLine(61_000, false)}, true},
		"no message of the turn has it": {[]string{typedLine("p1", true, false), sizeLine(60_000, false)}, false},
		"given in an earlier turn":      {[]string{sectionLine(false), typedLine("p1", true, false), sizeLine(60_000, false)}, false},
		"given by a subagent":           {[]string{typedLine("p1", true, false), sectionLine(true), sizeLine(60_000, false)}, false},
		"the instruction is not one":    {[]string{typedLine("p1", true, false), instruction, sizeLine(60_000, false)}, false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeLines(t, tt.lines...)
			if got := readTail(path, "p1", true, true, ""); got.Section != tt.want {
				t.Errorf("section = %v, want %v", got.Section, tt.want)
			}
			// It is looked for only with the prompt ID that bounds the turn.
			if got := readTail(path, "", true, true, ""); got.Section {
				t.Error("section found without a prompt ID")
			}
			if got := readTail(path, "p1", false, false, ""); got.Section {
				t.Error("section found without being looked for")
			}
		})
	}
}

// The section is bounded by the turn's prompt, so looking for it never grows
// the window beyond what the typed prompt already needs.
func TestReadTailSectionReadsNoMoreThanTheTypedPrompt(t *testing.T) {
	path := bigFile(t, nil, 10<<20, []string{typedLine("p1", true, false), sectionLine(false), sizeLine(250_000, false)})
	saved, read := readAt, 0
	readAt = func(f *os.File, b []byte, off int64) (int, error) {
		n, err := saved(f, b, off)
		read += n
		return n, err
	}
	t.Cleanup(func() { readAt = saved })
	if got := readTail(path, "p1", true, true, ""); !got.Section || !got.Typed || got.Size != 250_000 {
		t.Fatalf("tail = %+v", got)
	}
	if read > tailStart {
		t.Errorf("read %d bytes of a 10 MB transcript, want at most the last %d", read, tailStart)
	}
}

func TestReadTailLooksForTheTypedPromptAsFarAsForTheSize(t *testing.T) {
	// A long turn: the prompt is 3 MB before the end, the size at the end.
	path := bigFile(t, []string{typedLine("p1", true, false)}, 3<<20, []string{sizeLine(250_000, false)})
	if got := readTail(path, "p1", true, true, ""); !got.Typed || got.Size != 250_000 {
		t.Errorf("tail = %+v, want p1 typed and size 250000", got)
	}
	path = bigFile(t, []string{typedLine("p1", true, false)}, 9<<20, []string{sizeLine(250_000, false)})
	if got := readTail(path, "p1", true, true, ""); got.Typed || got.Size != 250_000 {
		t.Errorf("tail = %+v, want p1 out of reach past 8 MB and size 250000", got)
	}
}

// compactedFixture is a session advised at 274K, compacted to 77K, then grown
// back to 150K by fifteen entries a person did not type, then a typed prompt.
const compactedFixture = "testdata/compacted-then-grown.jsonl"

func TestReadTailFindsTheCompactionPastTheSize(t *testing.T) {
	// The scan stops at the first size-bearing entry, which here is fifteen
	// entries after the boundary.
	got := readTail(compactedFixture, "", false, false, "p0")
	if got.Size != 150_000 || !got.Compacted {
		t.Errorf("tail = %+v, want size 150000 and the compaction", got)
	}
}

func TestReadTailStopsTheCompactionSearchAtTheLastRecordedPrompt(t *testing.T) {
	// p1 was typed after the compaction: the boundary is older than the last
	// prompt recorded, so an advice given since must not be cleared by it.
	if got := readTail(compactedFixture, "", false, false, "p1"); got.Size != 150_000 || got.Compacted {
		t.Errorf("tail = %+v, want size 150000 and no compaction", got)
	}
	// With no prompt recorded, the search is bounded by what was read.
	if got := readTail(compactedFixture, "", false, false, ""); !got.Compacted {
		t.Errorf("tail = %+v, want the compaction", got)
	}
	// A conversation with no boundary reports none.
	path := writeLines(t, typedLine("p0", true, false), sizeLine(150_000, false))
	if got := readTail(path, "", false, false, "p0"); got.Compacted {
		t.Errorf("tail = %+v, want no compaction", got)
	}
}

// The fixture must be what Claude Code writes: every line parses.
func TestCompactedFixtureParses(t *testing.T) {
	raw, err := os.ReadFile(compactedFixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 20 {
		t.Errorf("%d lines, want 20: a prompt, two replies, the boundary, fifteen entries and a prompt", len(lines))
	}
	for i, line := range lines {
		var e tailEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil || e.Type == "" {
			t.Errorf("line %d skipped: %v", i+1, err)
		}
	}
}

// The compaction is looked for on every prompt: it must cost no more than the
// read it is part of. The figures are reported in the change's design notes.
func TestReadTailCostOfTheCompactionSearch(t *testing.T) {
	raw, err := os.ReadFile(compactedFixture)
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	timed := func(path, since string) time.Duration {
		readTail(path, "", false, false, since) // warm the page cache
		start := time.Now()
		for range 50 {
			readTail(path, "", false, false, since)
		}
		return time.Since(start) / 50
	}
	// The common case: the fixture at the end of a 1 MB tail, the search
	// stopping at the prompt last recorded.
	bounded := timed(bigFile(t, nil, 1<<20, fixture), "p0")
	// The worst case: nothing recorded and no boundary in the window, so every
	// line of it is looked at.
	whole := timed(bigFile(t, nil, 1<<20, []string{sizeLine(150_000, false)}), "")
	// What the read cost before the search: it stopped at the first entry from
	// the end that gave the size, here a boundary that ends both searches.
	exit := timed(bigFile(t, nil, 1<<20, []string{compact}), "")
	t.Logf("prompt hook read of a 1 MB tail: %v bounded at the last prompt, %v scanning the whole window, %v stopping at the first entry", bounded, whole, exit)
	if whole > 50*time.Millisecond {
		t.Errorf("the read takes %v, well past the hook's 500ms budget", whole)
	}
}

// toolsFixture is the transcript package's tool fixture: a large Read, a large
// Bash, two small calls answered together, an MCP call, a result whose
// tool_use is absent, and a subagent's result larger than all of them.
const toolsFixture = "../../transcript/testdata/tools.jsonl"

// toolsTail reads the fixture as the advice path reads a transcript, with a
// last entry giving the size.
func toolsTail(t *testing.T) client.Tail {
	t.Helper()
	raw, err := os.ReadFile(toolsFixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := append(strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"), sizeLine(212_000, false))
	return readTail(writeLines(t, lines...), "", true, true, "")
}

func TestReadTailCollectsTheLargestOutputs(t *testing.T) {
	got := toolsTail(t)
	if len(got.Outputs) != maxOutputs {
		t.Fatalf("outputs = %+v, want %d", got.Outputs, maxOutputs)
	}
	if got.Outputs[0].Tool != "Read" || got.Outputs[0].Target != "/src/internal/parser/parser.go" {
		t.Errorf("first = %+v, want the large Read with its path", got.Outputs[0])
	}
	if got.Outputs[1].Tool != "Bash" || !strings.HasPrefix(got.Outputs[1].Target, "go vet ./...") {
		t.Errorf("second = %+v, want the large Bash with its command", got.Outputs[1])
	}
	for i := 1; i < len(got.Outputs); i++ {
		if got.Outputs[i-1].Bytes < got.Outputs[i].Bytes {
			t.Errorf("outputs are not largest first: %+v", got.Outputs)
		}
	}
	// The two small results answered together did not displace them.
	if got.Outputs[2].Bytes >= got.Outputs[1].Bytes {
		t.Errorf("third = %+v", got.Outputs[2])
	}
}

// A subagent's result costs the main conversation's context nothing: the
// largest result of the fixture is one, and it is not listed.
func TestReadTailLeavesOutASubagentResult(t *testing.T) {
	for _, o := range toolsTail(t).Outputs {
		if o.ID == "toolu_subagent_read" {
			t.Errorf("outputs hold the sidechain result: %+v", o)
		}
	}
}

// A result whose tool_use is not in the window keeps its size and no name:
// nothing in the list names a tool that was not read.
func TestReadTailLeavesAnOrphanResultUnnamed(t *testing.T) {
	got := toolsTail(t)
	orphan := got.Outputs[len(got.Outputs)-1]
	if orphan.ID != "toolu_gone" {
		t.Fatalf("last = %+v, want the result whose call is absent", orphan)
	}
	if orphan.Tool != "" || orphan.Target != "" {
		t.Errorf("orphan = %+v, want no tool and no target", orphan)
	}
	if orphan.Bytes == 0 {
		t.Error("the orphan lost its size")
	}
}

// Past its deadline the collection yields what it has: the size is still read,
// so the advice is never lost to the scan.
func TestScanTailGivesUpCollectingPastItsDeadline(t *testing.T) {
	raw, err := os.ReadFile(toolsFixture)
	if err != nil {
		t.Fatal(err)
	}
	buf := append(raw, []byte(sizeLine(212_000, false)+"\n")...)
	tail, sized := scanTail(buf, "", true, true, "", time.Now().Add(-time.Second))
	if !sized || tail.Size != 212_000 || !tail.Typed {
		t.Errorf("tail = %+v, sized = %v; want the size and the typed prompt read", tail, sized)
	}
	if len(tail.Outputs) != 0 {
		t.Errorf("outputs = %+v, want none past the deadline", tail.Outputs)
	}
}

// The collection has no early exit: it walks the whole window, on the advice
// path only. The figures are reported in the change's design notes; they are
// measured, not asserted. A wall clock says as much about the machine as about
// the code, and under -race, the one run CI makes, it reads about four times
// higher. What the budget protects, the deadline cutting the collection while
// the size still comes back, is pinned by the test above.
func TestReadTailCostOfTheOutputCollection(t *testing.T) {
	raw, err := os.ReadFile(toolsFixture)
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	timed := func(path string, typed, collect bool) time.Duration {
		readTail(path, "", typed, collect, "") // warm the page cache
		start := time.Now()
		for range 20 {
			readTail(path, "", typed, collect, "")
		}
		return time.Since(start) / 20
	}
	// The advice path on a 1 MB tail holding the tool fixture at its end.
	head := []string{typedLine("p0", true, false)}
	collected := timed(bigFile(t, head, 1<<20, append(fixture, sizeLine(212_000, false))), true, true)
	// The same read without the collection: it stops at the first entry from
	// the end that gives the size and the typed prompt.
	exit := timed(bigFile(t, head, 1<<20, append(fixture, sizeLine(212_000, false))), false, false)
	// The worst case: a 1 MB window of tool results, every line of it parsed.
	results := make([]string, 0, 2000)
	for range 2000 {
		results = append(results, `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"`+strings.Repeat("x", 400)+`"}]}}`)
	}
	whole := timed(bigFile(t, head, 0, append(results, sizeLine(212_000, false))), true, true)
	t.Logf("advice-path read of a 1 MB tail: %v collecting, %v without the collection, %v collecting a window that is all tool results", collected, exit, whole)
	// The reminder path reads the same window for the section and the typed
	// prompt of the turn, and pays none of that: it asks for no list.
	reminder := timed(bigFile(t, head, 0, append(results, sizeLine(212_000, false))), true, false)
	t.Logf("the same window read for the reminder, with no collection: %v", reminder)
}

// Nothing before a compaction is in the context: the collection stops at the
// boundary, as the size and compaction searches do. The reproduction: a 48 KB
// result, the boundary, a 1 KB result, then the size.
func TestReadTailCollectsNoResultPastTheCompaction(t *testing.T) {
	result := func(id string, n int) string {
		return `{"type":"user","isSidechain":false,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"` + strings.Repeat("x", n) + `"}]}}`
	}
	boundary := `{"type":"system","subtype":"compact_boundary","isSidechain":false,"compactMetadata":{"trigger":"auto","preTokens":212000,"postTokens":90000}}`
	path := writeLines(t, typedLine("p0", true, false), result("toolu_dropped", 48<<10), boundary, result("toolu_kept", 1000), sizeLine(212_000, false))
	got := readTail(path, "", true, true, "")
	if !got.Compacted {
		t.Errorf("tail = %+v, want the compaction", got)
	}
	if len(got.Outputs) != 1 || got.Outputs[0].ID != "toolu_kept" {
		t.Errorf("outputs = %+v, want only the result after the boundary", got.Outputs)
	}
}

// Only the read that uses the list pays for it: the reminder path asks for the
// section and the typed prompt, and its read walks the window once, with no
// collection.
func TestReadTailCollectsOnlyWhenAsked(t *testing.T) {
	raw, err := os.ReadFile(toolsFixture)
	if err != nil {
		t.Fatal(err)
	}
	lines := append(strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"), sizeLine(212_000, false))
	path := writeLines(t, lines...)
	if got := readTail(path, "", true, false, ""); len(got.Outputs) != 0 || !got.Typed {
		t.Errorf("tail = %+v, want the typed prompt and no outputs", got)
	}
	if got := readTail(path, "", true, true, ""); len(got.Outputs) == 0 {
		t.Error("the read that asks for the outputs got none")
	}
}
