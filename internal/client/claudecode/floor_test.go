package claudecode

import (
	"path/filepath"
	"testing"
)

func TestContextFloor(t *testing.T) {
	tests := map[string]struct {
		lines []string
		want  int
		ok    bool
	}{
		"first assistant entry":      {[]string{userLine, sizeLine(60_000, false), sizeLine(212_000, false)}, 60_000, true},
		"sidechain first":            {[]string{sizeLine(9_000, true), sizeLine(60_000, false), sizeLine(212_000, false)}, 60_000, true},
		"three token types":          {[]string{`{"type":"assistant","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":0,"cache_creation_input_tokens":221904}}}`}, 221_906, true},
		"no assistant entry yet":     {[]string{userLine, filler}, 0, false},
		"only sidechain entries":     {[]string{sizeLine(9_000, true)}, 0, false},
		"bad lines are skipped":      {[]string{`{"type":"assistant","message":{"usage":`, `not json "usage"`, sizeLine(60_000, false)}, 60_000, true},
		"empty usage is not a floor": {[]string{`{"type":"assistant","message":{"usage":{"input_tokens":0}}}`, sizeLine(60_000, false)}, 60_000, true},
		"compaction does not count":  {[]string{compact, sizeLine(60_000, false)}, 60_000, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := contextFloor(writeLines(t, tt.lines...))
			if got != tt.want || ok != tt.ok {
				t.Errorf("floor = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestContextFloorMissingFile(t *testing.T) {
	if _, ok := contextFloor(filepath.Join(t.TempDir(), "nope.jsonl")); ok {
		t.Error("a missing file has no floor")
	}
}

func TestContextFloorReadsOnlyTheHead(t *testing.T) {
	// The first assistant entry sits past the 8 MB read: it is not found.
	lines := []string{}
	for n := 0; n < headMax/len(filler)+2; n++ {
		lines = append(lines, filler)
	}
	lines = append(lines, sizeLine(60_000, false))
	if got, ok := contextFloor(writeLines(t, lines...)); ok {
		t.Errorf("floor = %d, want none past 8 MB", got)
	}
	// An entry within it is, whatever follows.
	lines = append([]string{sizeLine(60_000, false)}, lines...)
	if got, ok := contextFloor(writeLines(t, lines...)); !ok || got != 60_000 {
		t.Errorf("floor = %d, %v; want 60000", got, ok)
	}
}
