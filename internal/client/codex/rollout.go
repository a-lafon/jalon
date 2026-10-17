package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// Rollout reads: a rollout reaches tens of MB and the size is in a line near
// its end, so it is read from the end, 1 MB then up to 8 MB; the floor and the
// session's origin are near its start, read up to 8 MB.
const (
	tailStart = 1 << 20
	tailMax   = 8 << 20
	headMax   = 8 << 20
)

// rolloutLine is the part of a rollout line Jalon reads. Codex documents the
// format as not a stable interface: unknown lines and fields are skipped.
type rolloutLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type   string          `json:"type"`
		Source json.RawMessage `json:"source"`
		Info   *struct {
			Last struct {
				Input int `json:"input_tokens"`
				Total int `json:"total_tokens"`
			} `json:"last_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

var tokenCountMark = []byte(`"token_count"`)

// tokenCount parses line when it is a token_count event with usage.
func tokenCount(line []byte) (rolloutLine, bool) {
	var l rolloutLine
	if !bytes.Contains(line, tokenCountMark) || json.Unmarshal(line, &l) != nil {
		return l, false
	}
	return l, l.Type == "event_msg" && l.Payload.Type == "token_count" && l.Payload.Info != nil
}

// readSize is the context size of the rollout at path, as of its last
// token_count event: the last request's input tokens, cached ones included.
// After a compaction Codex writes an estimate with only total_tokens set,
// taken then. 0 when it cannot be told.
func readSize(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	for window := int64(tailStart); ; window *= 8 {
		window = min(window, tailMax)
		start := max(fi.Size()-window, 0)
		buf := make([]byte, fi.Size()-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return 0
		}
		if start > 0 {
			// The first line of the window is usually cut.
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			} else {
				buf = nil
			}
		}
		for end := len(buf); end > 0; {
			begin := bytes.LastIndexByte(buf[:end], '\n') + 1
			line := buf[begin:end]
			end = begin - 1
			if l, ok := tokenCount(line); ok {
				if u := l.Payload.Info.Last; u.Input > 0 {
					return u.Input
				}
				return l.Payload.Info.Last.Total
			}
		}
		if start == 0 || window >= tailMax {
			return 0
		}
	}
}

// contextFloor is the input tokens of the rollout's first request, what the
// conversation starts with and no reset removes.
func contextFloor(path string) (int, bool) {
	found := 0
	eachHeadLine(path, func(line []byte) bool {
		if l, ok := tokenCount(line); ok && l.Payload.Info.Last.Input > 0 {
			found = l.Payload.Info.Last.Input
			return false
		}
		return true
	})
	return found, found > 0
}

// typedSession tells whether the rollout's first line is a session_meta whose
// source is the interactive CLI or the IDE extension, where a person types the
// prompts, not codex exec or another program. Codex 0.163.0 writes "vscode"
// for its terminal interface too, and "exec" for codex exec.
func typedSession(path string) bool {
	typed := false
	eachHeadLine(path, func(line []byte) bool {
		var l rolloutLine
		var source string
		typed = json.Unmarshal(line, &l) == nil && l.Type == "session_meta" &&
			json.Unmarshal(l.Payload.Source, &source) == nil && (source == "cli" || source == "vscode")
		return false
	})
	return typed
}

// eachHeadLine calls fn on the lines of the first 8 MB of the file at path
// until it returns false.
func eachHeadLine(path string, fn func([]byte) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, headMax))
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && !fn(bytes.TrimRight(line, "\n")) || err != nil {
			return
		}
	}
}
