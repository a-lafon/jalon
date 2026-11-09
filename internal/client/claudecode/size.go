package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/transcript"
)

// Tail reads: transcripts reach tens of MB and the context size is the usage of
// an entry near the end, so the file is read from its end, never whole.
const (
	tailStart = 1 << 20
	tailMax   = 8 << 20
)

// readAt reads the transcript; tests count the bytes it reads.
var readAt = (*os.File).ReadAt

// maxOutputs is how many of the largest tool results a tail reports.
const maxOutputs = 3

// collectBudget bounds the collection of those results, well inside the
// hook's own limit: unlike the rest of the scan it has no early exit, it walks
// the whole window. The hook returns nothing at all when it overruns, so
// losing the advice to the collection would be worse than an advice with no
// list; past the budget the scan keeps what it has and stops collecting.
var collectBudget = 100 * time.Millisecond

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
// Compacted is whether a compact_boundary entry shows after the entry of the
// prompt with ID since, the last one the caller recorded; with no since, back
// to the start of what was read. It never widens the window: a boundary
// outside it shows as false.
//
// With collect, which needs typed, Outputs are also collected: the largest tool
// results of what was read, which only the advice path asks for, within
// collectBudget and back to a compact_boundary, since what precedes one is no
// longer in the context.
//
// Lines that cannot be parsed, including the partial first line of a read, are
// skipped. It reads the last 1 MB, then up to 8 MB until it finds what it looks
// for.
func readTail(path, promptID string, typed, collect bool, since string) client.Tail {
	f, err := os.Open(path)
	if err != nil {
		return client.Tail{}
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return client.Tail{}
	}
	deadline := time.Now().Add(collectBudget)
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
		tail, sized := scanTail(buf, promptID, typed, collect, since, deadline)
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
	resultMark  = []byte(`"tool_result"`)
	useMark     = []byte(`"tool_use"`)
	sectionMark = []byte(advisor.SectionMark)
)

// scanTail scans the lines of buf from the end, until the size, the compaction
// and, with typed, a typed prompt are found. sized is true when an entry
// decided the size, even to say there is none. With collect it also collects the
// largest tool results, until deadline passes.
func scanTail(buf []byte, promptID string, typed, collect bool, since string, deadline time.Time) (tail client.Tail, sized bool) {
	// The section is looked for back to the turn's own prompt: the model gives
	// it before the handoff file, so it is not always the turn's last message.
	section := typed && promptID != ""
	// The compaction is looked for on purpose: the scan stops at the first
	// size-bearing entry, and after a compaction followed by work that reported
	// back that entry comes long before the boundary.
	boundary := true
	// The largest tool results are collected on the advice path only, the one
	// that uses them: this search has no early exit, it walks the whole window,
	// so it runs on its own deadline and yields what it has.
	collect = collect && typed
	for end := len(buf); end > 0 && (!sized || typed && !tail.Typed || section || boundary || collect); {
		start := bytes.LastIndexByte(buf[:end], '\n') + 1
		line := buf[start:end]
		end = start - 1
		if collect && time.Now().After(deadline) {
			collect = false
		}
		// Cheap filter before parsing: most lines are none of these.
		lookSize := !sized && (bytes.Contains(line, usageMark) || bytes.Contains(line, compactMark))
		lookTyped := typed && !tail.Typed && bytes.Contains(line, humanMark)
		lookSection := section && bytes.Contains(line, sectionMark)
		lookBoundary := (boundary || collect) && bytes.Contains(line, compactMark)
		// The prompt the search stops at is a typed one, like the one typed looks for.
		lookSince := boundary && since != "" && bytes.Contains(line, humanMark)
		lookResult := collect && bytes.Contains(line, resultMark)
		// Scanning backwards, a result is met before the call it answers, and
		// one that did not make the list is never named.
		lookUse := collect && len(tail.Outputs) > 0 && bytes.Contains(line, useMark)
		if !lookSize && !lookTyped && !lookSection && !lookBoundary && !lookSince && !lookResult && !lookUse {
			continue
		}
		var e tailEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch {
		case lookResult && e.Type == "user" && !e.IsSidechain:
			tail.Outputs = keepLargest(tail.Outputs, transcript.ToolResults(e.Message.Content))
		case lookUse && e.Type == "assistant" && !e.IsSidechain:
			nameOutputs(tail.Outputs, transcript.ToolCalls(e.Message.Content))
		}
		// Apart, since the entry that gave the section often carries the size too.
		if lookSection && e.Type == "assistant" && !e.IsSidechain && messageSection(e.Message.Content) {
			tail.Section, section = true, false
		}
		// Apart too: the boundary entry carries the size, and the prompt the
		// search stops at is the one typed matches.
		switch {
		case lookBoundary && e.Type == "system" && e.Subtype == "compact_boundary":
			// The collection stops here too: what precedes the boundary is not in
			// the context. Compacted only while the search for it is still on.
			tail.Compacted, boundary, collect = boundary, false, false
		case lookSince && e.Type == "user" && !e.IsSidechain && e.Origin.Kind == "human" && e.PromptID == since:
			boundary = false
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

// keepLargest merges found into kept, the maxOutputs largest results so far,
// largest first.
func keepLargest(kept, found []transcript.Output) []transcript.Output {
	for _, o := range found {
		if len(kept) == maxOutputs && o.Bytes <= kept[maxOutputs-1].Bytes {
			continue
		}
		i := 0
		for i < len(kept) && kept[i].Bytes >= o.Bytes {
			i++
		}
		kept = slices.Insert(kept, i, o)
		if len(kept) > maxOutputs {
			kept = kept[:maxOutputs]
		}
	}
	return kept
}

// nameOutputs gives the kept results the tool and target of the calls they
// answer. A result whose call is not in what was read keeps none: the tool is
// never guessed from the result alone.
func nameOutputs(kept, calls []transcript.Output) {
	for _, c := range calls {
		if c.ID == "" {
			continue
		}
		for i := range kept {
			if kept[i].ID == c.ID {
				kept[i].Tool, kept[i].Target = c.Tool, c.Target
			}
		}
	}
}
