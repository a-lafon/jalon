package claudecode

import (
	"os"
	"testing"
)

// FuzzScanTail checks that scanning the end of a transcript never panics. The
// window starts at an arbitrary offset, so the buffer is cut mid-line, and the
// prompt hook runs this on every prompt.
func FuzzScanTail(f *testing.F) {
	if b, err := os.ReadFile("../../transcript/testdata/session.jsonl"); err == nil {
		f.Add(b, "", false, "")
		f.Add(b[len(b)/2:], "", true, "")
	}
	f.Add([]byte("{\"type\":\"assistant\",\"message\":{\"usage\":{}}}\n"), "abc", true, "p1")
	f.Add([]byte("\n\n{\"type\":"), "", false, "")
	f.Fuzz(func(t *testing.T, buf []byte, promptID string, typed bool, since string) {
		scanTail(buf, promptID, typed, since)
	})
}
