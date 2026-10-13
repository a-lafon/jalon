package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	tail := readTail(path, false)
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
		lines    []string
		anyTyped bool
	}{
		"typed prompt":             {[]string{typedLine("p1", true, false), sizeLine(60_000, false)}, true},
		"claude -p prompt":         {[]string{typedLine("p1", false, false), toolResult, sizeLine(60_000, false)}, false},
		"typed earlier, -p now":    {[]string{typedLine("p0", true, false), sizeLine(50_000, false), typedLine("p1", false, false), sizeLine(60_000, false)}, true},
		"typed in a subagent only": {[]string{typedLine("p1", true, true), sizeLine(60_000, false)}, false},
		"typed after the size":     {[]string{sizeLine(60_000, false), typedLine("p1", true, false)}, true},
		"typed, no size":           {[]string{typedLine("p1", true, false)}, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeLines(t, tt.lines...)
			if got := readTail(path, true); got.Typed != tt.anyTyped {
				t.Errorf("any prompt typed = %v, want %v", got.Typed, tt.anyTyped)
			}
			if got := readTail(path, false); got.Typed {
				t.Error("typed found without being looked for")
			}
		})
	}
}

func TestReadTailLooksForTheTypedPromptAsFarAsForTheSize(t *testing.T) {
	// A long turn: the prompt is 3 MB before the end, the size at the end.
	path := bigFile(t, []string{typedLine("p1", true, false)}, 3<<20, []string{sizeLine(250_000, false)})
	if got := readTail(path, true); !got.Typed || got.Size != 250_000 {
		t.Errorf("tail = %+v, want a typed prompt and size 250000", got)
	}
	path = bigFile(t, []string{typedLine("p1", true, false)}, 9<<20, []string{sizeLine(250_000, false)})
	if got := readTail(path, true); got.Typed || got.Size != 250_000 {
		t.Errorf("tail = %+v, want the typed prompt out of reach past 8 MB and size 250000", got)
	}
}
