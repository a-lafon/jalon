package claudecode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
			got, ok := contextSize(writeLines(t, tt.lines...))
			if got != tt.want || ok != tt.ok {
				t.Errorf("size = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestContextSizeMissingOrEmptyFile(t *testing.T) {
	if _, ok := contextSize(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("a missing file has no size")
	}
	if _, ok := contextSize(""); ok {
		t.Error("an empty path has no size")
	}
	empty := filepath.Join(t.TempDir(), "e.jsonl")
	os.WriteFile(empty, nil, 0o600)
	if _, ok := contextSize(empty); ok {
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
	if got, ok := contextSize(path); !ok || got != 333_000 {
		t.Errorf("size = %d, %v; want 333000 found beyond the first 1 MB", got, ok)
	}
}

func TestContextSizeGivesUpBeyond8MB(t *testing.T) {
	path := bigFile(t, []string{sizeLine(333_000, false)}, 9<<20, nil)
	if got, ok := contextSize(path); ok {
		t.Errorf("size = %d, want none past the 8 MB limit", got)
	}
}

func TestContextSizeOfALargeTranscriptIsFast(t *testing.T) {
	// A 10 MB transcript whose last entries are at the end, as in practice.
	path := bigFile(t, []string{sizeLine(1_000, false)}, 10<<20, []string{sizeLine(250_000, false), userLine})
	start := time.Now()
	got, ok := contextSize(path)
	if !ok || got != 250_000 {
		t.Fatalf("size = %d, %v", got, ok)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Errorf("took %v for a 10 MB transcript, the hook has 500 ms in all", d)
	}
}
