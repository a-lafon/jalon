package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/client"
)

// Tail reads: transcripts reach tens of MB and the context size is the usage of
// an entry near the end, so the file is read from its end, never whole.
const (
	tailStart = 1 << 20
	tailMax   = 8 << 20
)

// readAt reads the transcript; tests count the bytes it reads.
var readAt = (*os.File).ReadAt

// readTail reads the end of the transcript at path once. The size is the main
// conversation's context, as of the end of the transcript: the first of these
// found when scanning from the end:
//   - a compact_boundary entry: its compactMetadata.postTokens. When that field
//     is missing there is no size at all;
//   - an assistant entry that is not a sidechain, with usage: input, cache read
//     and cache creation tokens, the sum of Claude Code's own
//     context_window.total_input_tokens. Its cache_creation tells the cache
//     lifetime: 5 minutes or an hour, by the field its writes went to; none
//     when the request wrote nothing to the cache.
//
// With typed, Typed is whether the same part of the transcript holds a user
// entry, not a sidechain, whose origin kind is "human": Claude Code writes it on
// a typed prompt, and none on a claude -p prompt, a tool result or a meta
// message. With a promptID, only the entry of that prompt counts. A session a
// program drives has none, so looking for it reads the whole 8 MB: the hooks
// ask for it only when they are about to advise.
//
// Lines that cannot be parsed, including the partial first line of a read, are
// skipped. It reads the last 1 MB, then up to 8 MB until it finds what it looks
// for.
func readTail(path, promptID string, typed bool) client.Tail {
	f, err := os.Open(path)
	if err != nil {
		return client.Tail{}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return client.Tail{}
	}
	for window := int64(tailStart); ; window *= 8 {
		window = min(window, tailMax)
		start := max(fi.Size()-window, 0)
		buf := make([]byte, fi.Size()-start)
		if _, err := readAt(f, buf, start); err != nil && err != io.EOF {
			return client.Tail{}
		}
		if start > 0 {
			// The first line of the window is usually cut.
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil
			}
		}
		tail, sized := scanTail(buf, promptID, typed)
		if sized && (!typed || tail.Typed) || start == 0 || window >= tailMax {
			return tail
		}
	}
}

type tailEntry struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	IsSidechain bool   `json:"isSidechain"`
	PromptID    string `json:"promptId"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	CompactMetadata struct {
		PostTokens *int `json:"postTokens"`
	} `json:"compactMetadata"`
	Message struct {
		// Raw, since a user entry's content is a string where an assistant's is
		// a list of blocks, and a failed decode would lose the whole entry.
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int `json:"input_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
			CacheWrites   struct {
				FiveMinutes int `json:"ephemeral_5m_input_tokens"`
				OneHour     int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

var (
	usageMark   = []byte(`"usage"`)
	compactMark = []byte(`compact_boundary`)
	humanMark   = []byte(`"human"`)
	sectionMark = []byte(advisor.SectionMark)
)

// scanTail scans the lines of buf from the end, until the size and, with typed,
// a typed prompt are found. sized is true when an entry decided the size, even
// to say there is none.
func scanTail(buf []byte, promptID string, typed bool) (tail client.Tail, sized bool) {
	// The section is looked for back to the turn's own prompt: the model gives
	// it before the handoff file, so it is not always the turn's last message.
	section := typed && promptID != ""
	for end := len(buf); end > 0 && (!sized || typed && !tail.Typed || section); {
		start := bytes.LastIndexByte(buf[:end], '\n') + 1
		line := buf[start:end]
		end = start - 1
		// Cheap filter before parsing: most lines are none of these.
		lookSize := !sized && (bytes.Contains(line, usageMark) || bytes.Contains(line, compactMark))
		lookTyped := typed && !tail.Typed && bytes.Contains(line, humanMark)
		lookSection := section && bytes.Contains(line, sectionMark)
		if !lookSize && !lookTyped && !lookSection {
			continue
		}
		var e tailEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		// Apart, since the entry that gave the section often carries the size too.
		if lookSection && e.Type == "assistant" && !e.IsSidechain && messageSection(e.Message.Content) {
			tail.Section, section = true, false
		}
		switch {
		case lookTyped && e.Type == "user" && !e.IsSidechain && e.Origin.Kind == "human":
			tail.Typed = promptID == "" || e.PromptID == promptID
			// The turn's own prompt: what comes before it is an earlier turn.
			section = section && !tail.Typed
		case !lookSize:
		case e.Type == "system" && e.Subtype == "compact_boundary":
			sized = true
			if e.CompactMetadata.PostTokens != nil {
				tail.Size = *e.CompactMetadata.PostTokens
			}
		case e.Type == "assistant" && !e.IsSidechain && e.Message.Usage != nil:
			u := e.Message.Usage
			if n := u.Input + u.CacheRead + u.CacheCreation; n > 0 {
				tail.Size, sized = n, true
				// The shorter lifetime when a request wrote both: the context is lost
				// with its first part to expire.
				switch {
				case u.CacheWrites.FiveMinutes > 0:
					tail.CacheTTL = 5 * time.Minute
				case u.CacheWrites.OneHour > 0:
					tail.CacheTTL = time.Hour
				}
			}
		}
	}
	return tail, sized
}

// messageSection tells whether an assistant message's content holds a Jalon
// section. The content is the list of blocks Claude Code writes; anything else
// holds none.
func messageSection(content json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && advisor.HasSection(b.Text) {
			return true
		}
	}
	return false
}
