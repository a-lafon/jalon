// Package transcript reads the session transcripts that Claude Code keeps under
// ~/.claude/projects. The format is internal to Claude Code and changes between
// versions, so the reader keeps only what the context report needs, skips what
// it cannot interpret and counts it, so a format change shows up as low coverage
// instead of wrong numbers. It never keeps conversation text, only sizes and
// the paths of handoff files.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/handoff"
)

// Conversation is the main thread of one session file. Subagent entries
// (sidechains) are left out: they run in their own context.
type Conversation struct {
	File string
	// Turns are the user turns that got an answer, in order.
	Turns []Turn
	// Requests are the API requests of the assistant, in order.
	Requests []Request
	// Blocks are the sizes of the messages that make up the context, in order.
	Blocks []Block
	// Compactions are the compactions found in this file, in order.
	Compactions []Compaction
	// Human is the number of user prompts that a person typed (origin kind
	// "human"). A conversation with none was driven by a program, such as an
	// SDK script, and nobody can /compact it.
	Human int
	// SessionID is the session ID in the file name, <id>.jsonl. Sessions are
	// the distinct session IDs its entries carry, in order.
	SessionID string
	Sessions  []string
	// Advices are the advice the prompt hook gave, in order.
	Advices []Advice
	// Resume is set when the first prompt a person typed names a handoff file.
	Resume *Resume
	// Writes are the Write tool uses of the main thread on a handoff file.
	Writes []Write
}

// Advice is an advice the prompt hook gave, recorded as its system message.
type Advice struct {
	UUID string
	Time time.Time
	// Turn is the index in Turns of the prompt it was given at, and Request the
	// number of requests made before it.
	Turn, Request int
}

// Resume is a prompt that resumes from a handoff file.
type Resume struct {
	Path string
	Time time.Time
	// Turn is the index in Turns of the prompt, and Request the index in Requests
	// of the first request that answered it.
	Turn, Request int
}

// Write is a Write tool use on a handoff file.
type Write struct {
	Path string
	Time time.Time
}

// Turn is a user prompt, with the prompt size of the first request it caused.
type Turn struct {
	Time         time.Time
	PromptTokens int
	// Block is the index in Blocks of the prompt itself.
	Block int
	// Request is the index in Requests of the first request that answered it,
	// that is the number of requests made before it.
	Request int
}

// Request is one assistant API request.
type Request struct {
	Time         time.Time
	Turn         int // index into Turns
	PromptTokens int
	// CacheRead is the part of PromptTokens read from the cache.
	CacheRead int
}

// Block is the size in bytes of one message of the context.
type Block struct {
	Turn       int // index of the user turn it belongs to
	Bytes      int
	ToolResult bool
	// Request is the number of requests made before this block, so a block
	// seen later is Request positions older.
	Request int
}

// Compaction is a compact_boundary entry. The After fields locate it in the
// conversation: how many turns, requests and blocks came before it.
type Compaction struct {
	UUID          string
	Time          time.Time
	Trigger       string
	PreTokens     int
	AfterTurns    int
	AfterRequests int
	AfterBlocks   int
}

// Coverage counts what the reader did with the lines it saw.
type Coverage struct {
	Files int
	// Read is the number of non-empty lines. Interpreted lines fed the
	// conversation, Ignored lines are known and unused (bookkeeping entries,
	// subagents, duplicates), Skipped lines could not be interpreted.
	Read, Interpreted, Ignored, Skipped int
	// Compactions counts distinct compactions, by uuid.
	Compactions int
}

// SkippedShare is the share of read lines that could not be interpreted.
func (c Coverage) SkippedShare() float64 {
	if c.Read == 0 {
		return 0
	}
	return float64(c.Skipped) / float64(c.Read)
}

// ignoredTypes are the entry types seen in transcripts that carry no context
// size. A type that is neither here nor interpreted is counted as skipped. An
// attachment is interpreted when it is an advice of the prompt hook.
var ignoredTypes = map[string]bool{
	"attachment": true, "queue-operation": true, "ai-title": true, "last-prompt": true,
	"file-history-snapshot": true, "file-history-delta": true, "mode": true,
	"cost-state": true, "atis-latch": true, "progress": true, "summary": true,
	"permission-mode": true, "custom-title": true, "agent-name": true,
}

// Reader reads transcript files and deduplicates compactions across them.
type Reader struct {
	Coverage Coverage
	seen     map[string]bool
}

// NewReader returns a Reader with empty coverage.
func NewReader() *Reader { return &Reader{seen: map[string]bool{}} }

// ReadDir reads every <dir>/<project>/<session>.jsonl whose file was modified at
// or after since (the zero time reads them all), oldest first.
func (r *Reader) ReadDir(dir string, since time.Time) ([]Conversation, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) {
			continue
		}
		files = append(files, file{p, info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })

	var out []Conversation
	for _, f := range files {
		c, err := r.ReadFile(f.path)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// ReadFile reads one session file.
func (r *Reader) ReadFile(path string) (Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	return r.Read(path, f)
}

// entry is the part of a transcript line the reader looks at.
type entry struct {
	Type             string `json:"type"`
	Subtype          string `json:"subtype"`
	UUID             string `json:"uuid"`
	Timestamp        string `json:"timestamp"`
	IsSidechain      bool   `json:"isSidechain"`
	IsMeta           bool   `json:"isMeta"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	SessionID        string `json:"sessionId"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message    json.RawMessage `json:"message"`
	Attachment *struct {
		Type      string          `json:"type"`
		HookEvent string          `json:"hookEvent"`
		Content   json.RawMessage `json:"content"`
	} `json:"attachment"`
	CompactMetadata *struct {
		Trigger   string `json:"trigger"`
		PreTokens int    `json:"preTokens"`
	} `json:"compactMetadata"`
}

type message struct {
	ID      string          `json:"id"`
	Content json.RawMessage `json:"content"`
	Usage   *struct {
		Input       int `json:"input_tokens"`
		CacheRead   int `json:"cache_read_input_tokens"`
		CacheCreate int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// Read reads one session from rd. name is only kept as the conversation's File.
func (r *Reader) Read(name string, rd io.Reader) (Conversation, error) {
	r.Coverage.Files++
	conv := Conversation{File: name, SessionID: strings.TrimSuffix(filepath.Base(name), ".jsonl")}
	br := bufio.NewReaderSize(rd, 1<<20)

	var pending *Turn // a user prompt that has not been answered yet
	lastMessageID := ""
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			r.Coverage.Read++
			var e entry
			if json.Unmarshal(line, &e) != nil || e.Type == "" {
				r.Coverage.Skipped++
			} else {
				r.add(&conv, &pending, &lastMessageID, e)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return conv, nil
			}
			return conv, fmt.Errorf("read %s: %w", name, err)
		}
	}
}

// add folds one entry into the conversation and updates the coverage.
func (r *Reader) add(conv *Conversation, pending **Turn, lastMessageID *string, e entry) {
	ts, tsErr := time.Parse(time.RFC3339Nano, e.Timestamp)
	if e.SessionID != "" && !e.IsSidechain && !slices.Contains(conv.Sessions, e.SessionID) {
		conv.Sessions = append(conv.Sessions, e.SessionID)
	}
	switch {
	case e.Type == "attachment" && isAdvice(e):
		if e.IsSidechain || e.UUID == "" || tsErr != nil || r.seen[e.UUID] {
			r.Coverage.Ignored++
			return
		}
		r.seen[e.UUID] = true
		r.Coverage.Interpreted++
		conv.Advices = append(conv.Advices, Advice{UUID: e.UUID, Time: ts, Turn: len(conv.Turns), Request: len(conv.Requests)})
	case e.Type == "system" && e.Subtype == "compact_boundary":
		if e.CompactMetadata == nil || e.UUID == "" || tsErr != nil {
			r.Coverage.Skipped++
			return
		}
		if r.seen[e.UUID] {
			r.Coverage.Ignored++
			return
		}
		r.seen[e.UUID] = true
		r.Coverage.Compactions++
		r.Coverage.Interpreted++
		*pending = nil
		conv.Compactions = append(conv.Compactions, Compaction{
			UUID: e.UUID, Time: ts, Trigger: e.CompactMetadata.Trigger, PreTokens: e.CompactMetadata.PreTokens,
			AfterTurns: len(conv.Turns), AfterRequests: len(conv.Requests), AfterBlocks: len(conv.Blocks),
		})
	case e.Type == "user" || e.Type == "assistant":
		if e.IsSidechain {
			r.Coverage.Ignored++
			return
		}
		var m message
		if tsErr != nil || json.Unmarshal(e.Message, &m) != nil || len(m.Content) == 0 {
			r.Coverage.Skipped++
			return
		}
		r.Coverage.Interpreted++
		cur := len(conv.Turns)
		if *pending == nil {
			cur--
		}
		block := Block{Turn: max(cur, 0), Bytes: len(m.Content), Request: len(conv.Requests)}
		if e.Type == "user" {
			hasResult, hasText, ok := userContent(m.Content)
			if !ok {
				r.Coverage.Interpreted--
				r.Coverage.Skipped++
				return
			}
			block.ToolResult = hasResult
			if hasText && !hasResult && !e.IsMeta && !e.IsCompactSummary {
				*pending = &Turn{Time: ts, Block: len(conv.Blocks)}
				if e.Origin != nil && e.Origin.Kind == "human" {
					if conv.Human == 0 {
						conv.Resume = resumeOf(m.Content, ts, len(conv.Turns), len(conv.Requests))
					}
					conv.Human++
				}
				block.Turn = len(conv.Turns)
			}
			conv.Blocks = append(conv.Blocks, block)
			return
		}
		conv.Blocks = append(conv.Blocks, block)
		conv.Writes = append(conv.Writes, handoffWrites(m.Content, ts)...)
		if m.Usage == nil || (m.ID != "" && m.ID == *lastMessageID) {
			return // a content block of a request already counted
		}
		prompt := m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheCreate
		if prompt == 0 {
			return // synthetic message
		}
		*lastMessageID = m.ID
		if *pending != nil {
			(*pending).PromptTokens = prompt
			(*pending).Request = len(conv.Requests)
			conv.Turns = append(conv.Turns, **pending)
			*pending = nil
		}
		if len(conv.Turns) > 0 {
			conv.Requests = append(conv.Requests, Request{Time: ts, Turn: len(conv.Turns) - 1, PromptTokens: prompt, CacheRead: m.Usage.CacheRead})
		}
	case ignoredTypes[e.Type] || e.Type == "system":
		r.Coverage.Ignored++
	default:
		r.Coverage.Skipped++
	}
}

// isAdvice reports whether an attachment entry is the system message of an
// advice from the prompt hook.
func isAdvice(e entry) bool {
	a := e.Attachment
	if a == nil || a.Type != "hook_system_message" || a.HookEvent != "UserPromptSubmit" {
		return false
	}
	var content string
	return json.Unmarshal(a.Content, &content) == nil && strings.HasPrefix(content, advisor.MessagePrefix)
}

// resumeOf is the resume a prompt holds, nil when it names no handoff file by
// an absolute path. The last path named wins.
func resumeOf(raw json.RawMessage, at time.Time, turn, request int) *Resume {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return nil
		}
		for _, b := range blocks {
			if b.Type == "text" {
				text += b.Text + "\n"
			}
		}
	}
	var res *Resume
	for _, m := range handoff.Mentions(text) {
		if p := m.Path(); p != "" {
			res = &Resume{Path: p, Time: at, Turn: turn, Request: request}
		}
	}
	return res
}

// handoffWrites are the Write tool uses on a handoff file in the content of an
// assistant message.
func handoffWrites(raw json.RawMessage, at time.Time) []Write {
	if !bytes.Contains(raw, []byte(`"Write"`)) {
		return nil
	}
	var blocks []struct {
		Type  string `json:"type"`
		Name  string `json:"name"`
		Input struct {
			FilePath string `json:"file_path"`
		} `json:"input"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []Write
	for _, b := range blocks {
		if b.Type == "tool_use" && b.Name == "Write" && handoff.IsPath(b.Input.FilePath) {
			out = append(out, Write{Path: b.Input.FilePath, Time: at})
		}
	}
	return out
}

// userContent looks at the content of a user message: a string, or blocks.
// ok is false when it is neither.
func userContent(raw json.RawMessage) (hasResult, hasText, ok bool) {
	switch raw[0] {
	case '"':
		return false, true, true
	case '[':
		var blocks []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return false, false, false
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_result":
				hasResult = true
			case "text":
				hasText = true
			}
		}
		return hasResult, hasText, true
	}
	return false, false, false
}
