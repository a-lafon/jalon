package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// Tail reads: transcripts reach tens of MB and the context size is the usage of
// an entry near the end, so the file is read from its end, never whole.
const (
	tailStart = 1 << 20
	tailMax   = 8 << 20
)

// ContextSize returns the size in tokens of the main conversation's context, as
// of the end of the transcript at path. It is the first of these found when
// scanning from the end:
//   - a compact_boundary entry: its compactMetadata.postTokens. When that field
//     is missing there is no size at all;
//   - an assistant entry that is not a sidechain, with usage: input, cache read
//     and cache creation tokens, the sum of Claude Code's own
//     context_window.total_input_tokens.
//
// Lines that cannot be parsed, including the partial first line of a read, are
// skipped. It reads the last 1 MB, then up to 8 MB when nothing usable is found.
func contextSize(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false
	}
	for window := int64(tailStart); ; window *= 8 {
		window = min(window, tailMax)
		start := max(fi.Size()-window, 0)
		buf := make([]byte, fi.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return 0, false
		}
		if start > 0 {
			// The first line of the window is usually cut.
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil
			}
		}
		if size, done := lastSize(buf); done {
			return size, size > 0
		}
		if start == 0 || window >= tailMax {
			return 0, false
		}
	}
}

type sizeEntry struct {
	Type            string `json:"type"`
	Subtype         string `json:"subtype"`
	IsSidechain     bool   `json:"isSidechain"`
	CompactMetadata struct {
		PostTokens *int `json:"postTokens"`
	} `json:"compactMetadata"`
	Message struct {
		Usage *struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

var (
	usageMark   = []byte(`"usage"`)
	compactMark = []byte(`compact_boundary`)
)

// lastSize scans the lines of buf from the end. done is true when an entry
// decided the size, even to say there is none.
func lastSize(buf []byte) (size int, done bool) {
	for end := len(buf); end > 0; {
		start := bytes.LastIndexByte(buf[:end], '\n') + 1
		line := buf[start:end]
		end = start - 1
		// Cheap filter before parsing: most lines are neither.
		if !bytes.Contains(line, usageMark) && !bytes.Contains(line, compactMark) {
			continue
		}
		var e sizeEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch {
		case e.Type == "system" && e.Subtype == "compact_boundary":
			if e.CompactMetadata.PostTokens == nil {
				return 0, true
			}
			return *e.CompactMetadata.PostTokens, true
		case e.Type == "assistant" && !e.IsSidechain && e.Message.Usage != nil:
			u := e.Message.Usage
			if n := u.Input + u.CacheRead + u.CacheCreation; n > 0 {
				return n, true
			}
		}
	}
	return 0, false
}
