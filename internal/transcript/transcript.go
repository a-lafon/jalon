// Package transcript reads the session transcripts that Claude Code keeps under
// ~/.claude/projects. The format is internal to Claude Code and changes between
// versions, so the reader keeps only what the context report needs, skips what
// it cannot interpret and counts it, so a format change shows up as low coverage
// instead of wrong numbers. It keeps sizes, the paths of handoff files, the
// working directory and the start of each prompt a person typed, no other
// conversation text.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	// Project is the folder the file is in, and Cwd the working directory of
	// its first entry that has one.
	Project, Cwd string
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
	// Advices are the advice the hooks gave, in order.
	Advices []Advice
	// Resume is set when the first prompt a person typed names a handoff file.
	Resume *Resume
	// Writes are the Write tool uses of the main thread on a handoff file.
	Writes []Write
	// Subagents are the subagents of the session, read from their own files.
	Subagents []Subagent
}

// Subagent is one subagent of a conversation, of which only the requests are
// kept: their Turn is always 0.
type Subagent struct {
	// Type is the agentType of its meta file, "" when unknown.
	Type     string
	Requests []Request
}

// Advice is an advice a hook gave: the prompt hook's system message, or the
// reason the stop hook blocked the end of a turn with.
type Advice struct {
	UUID string
	Time time.Time
	// Turn is the index in Turns of the turn whose reply holds it: the prompt it
	// was given at, or the turn whose end it was given at. Request is the number
	// of requests made before it.
	Turn, Request int
	// Rendered is set when an assistant message after it held the recommendation
	// section the advice asked for, before the next advice or compaction. When
	// it is not, the user never saw the advice.
	Rendered bool
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
	// Advisable is set when the prompt hook would act on the prompt: the hook
	// takes it for typed, and a person typed a prompt earlier in the
	// conversation, since the hook runs before its prompt reaches the transcript.
	Advisable bool
	// Human is set when a person typed the prompt itself (origin kind "human"),
	// the turns the stop hook acts on.
	Human bool
	// Prompt is the start of a prompt a person typed, on one line, "" for others.
	Prompt string
}

// promptRunes is the length of the start of a typed prompt that a Turn keeps.
const promptRunes = 120

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
	// Found is the number of transcript files ReadDir saw, before the time
	// filter; Files the number read.
	Found, Files int
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
// attachment is interpreted when it is an advice of a hook.
var ignoredTypes = map[string]bool{
	"attachment": true, "queue-operation": true, "ai-title": true, "last-prompt": true,
	"file-history-snapshot": true, "file-history-delta": true, "mode": true,
	"cost-state": true, "atis-latch": true, "progress": true, "summary": true,
	"permission-mode": true, "custom-title": true, "agent-name": true,
	"api-request": true, "api-request-blob": true, "api-request-shape": true,
}

// Reader reads transcript files and deduplicates compactions across them.
type Reader struct {
	Coverage Coverage
	// Typed is the prompt hook's own test of a prompt's text, set by the caller
	// so this package does not depend on the client adapter. Nil takes every
	// prompt for typed.
	Typed func(prompt string) bool
	seen  map[string]bool
}

// NewReader returns a Reader with empty coverage.
func NewReader() *Reader { return &Reader{seen: map[string]bool{}} }

// ReadDir reads every <dir>/<project>/<session>.jsonl and <dir>/<session>.jsonl
// whose file was modified at or after since (the zero time reads them all),
// oldest first. The second layout is one project's folder.
func (r *Reader) ReadDir(dir string, since time.Time) ([]Conversation, error) {
	var paths []string
	for _, pattern := range []string{filepath.Join(dir, "*", "*.jsonl"), filepath.Join(dir, "*.jsonl")} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		paths = append(paths, matches...)
	}
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		r.Coverage.Found++
		if info.ModTime().Before(since) {
			continue
		}
		files = append(files, file{p, info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })

	var out []Conversation
	for _, f := range files {
		c, err := r.ReadFile(f.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted since it was listed
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// openFile opens a session file; tests replace it.
var openFile = os.Open

// ReadFile reads one session file.
func (r *Reader) ReadFile(path string) (Conversation, error) {
	f, err := openFile(path)
	if err != nil {
		return Conversation{}, err
	}
	defer f.Close()
	c, err := r.Read(path, f)
	if err != nil {
		return c, err
	}
	c.Subagents, err = r.readSubagents(path)
	return c, err
}

// readSubagents reads the subagent files of a session file, in name order:
// <session>/subagents/agent-*.jsonl, each with its .meta.json.
func (r *Reader) readSubagents(session string) ([]Subagent, error) {
	paths, err := filepath.Glob(filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "agent-*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []Subagent
	for _, p := range paths {
		f, err := openFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted since it was listed
		}
		if err != nil {
			return nil, err
		}
		s, err := r.readSubagent(p, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// readSubagent reads the requests of one subagent file.
func (r *Reader) readSubagent(path string, rd io.Reader) (Subagent, error) {
	var s Subagent
	if raw, err := os.ReadFile(strings.TrimSuffix(path, ".jsonl") + ".meta.json"); err == nil {
		var meta struct {
			AgentType string `json:"agentType"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			s.Type = meta.AgentType
		}
	}
	br := bufio.NewReaderSize(rd, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			r.Coverage.Read++
			var e entry
			switch {
			case json.Unmarshal(line, &e) != nil || e.Type == "":
				r.Coverage.Skipped++
			case e.Type == "assistant":
				var m message
				ts, tsErr := time.Parse(time.RFC3339Nano, e.Timestamp)
				if tsErr != nil || json.Unmarshal(e.Message, &m) != nil {
					r.Coverage.Skipped++
					break
				}
				r.Coverage.Interpreted++
				// Another content block of a request already counted, here or in
				// the copy of this subagent that a /clear leaves in the next session.
				if m.Usage == nil || (m.ID != "" && r.seen["msg:"+m.ID]) {
					break
				}
				if prompt := m.Usage.Input + m.Usage.CacheRead + m.Usage.CacheCreate; prompt > 0 {
					r.seen["msg:"+m.ID] = m.ID != ""
					s.Requests = append(s.Requests, Request{Time: ts, PromptTokens: prompt, CacheRead: m.Usage.CacheRead})
				}
			case e.Type == "user" || e.Type == "system" || ignoredTypes[e.Type]:
				r.Coverage.Ignored++
			default:
				r.Coverage.Skipped++
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return s, nil
			}
			return s, fmt.Errorf("read %s: %w", path, err)
		}
	}
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
	Cwd              string `json:"cwd"`
	Origin           *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message    json.RawMessage `json:"message"`
	Attachment *struct {
		Type          string          `json:"type"`
		HookEvent     string          `json:"hookEvent"`
		Content       json.RawMessage `json:"content"`
		BlockingError *struct {
			BlockingError string `json:"blockingError"`
		} `json:"blockingError"`
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
	conv := Conversation{File: name, Project: filepath.Dir(name), SessionID: strings.TrimSuffix(filepath.Base(name), ".jsonl")}
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
			if conv.Resume != nil && conv.Resume.Path == "" {
				conv.Resume = nil // a /jalon-resume that picked no handoff
			}
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
	if conv.Cwd == "" {
		conv.Cwd = e.Cwd
	}
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
		// The prompt hook's advice comes before the turn's first answer, which
		// adds the turn; the stop hook's comes after it, at the end of the turn.
		turn := len(conv.Turns)
		if e.Attachment.HookEvent == "Stop" && *pending == nil {
			turn = max(turn-1, 0)
		}
		conv.Advices = append(conv.Advices, Advice{UUID: e.UUID, Time: ts, Turn: turn, Request: len(conv.Requests)})
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
			if hasResult && conv.Human == 1 && conv.Resume != nil && conv.Resume.Path == "" {
				conv.Resume.Path = pickedHandoff(m.Content)
			}
			if hasText && !hasResult && !e.IsMeta && !e.IsCompactSummary {
				*pending = &Turn{Time: ts, Block: len(conv.Blocks),
					Advisable: conv.Human > 0 && (r.Typed == nil || r.Typed(promptText(m.Content)))}
				if e.Origin != nil && e.Origin.Kind == "human" {
					(*pending).Human = true
					(*pending).Prompt = startOf(promptText(m.Content), promptRunes)
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
		if hasSection(m.Content) {
			markRendered(conv)
		}
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

// isAdvice reports whether an attachment entry is an advice: the system
// message of the prompt hook, or the reason the stop hook blocked the end of a
// turn with. Both start with the advice's prefix; the stop hook's reminder of
// an advice already given does not, so it does not count twice.
func isAdvice(e entry) bool {
	a := e.Attachment
	switch {
	case a == nil:
		return false
	case a.Type == "hook_system_message" && a.HookEvent == "UserPromptSubmit":
		var content string
		return json.Unmarshal(a.Content, &content) == nil && strings.HasPrefix(content, advisor.MessagePrefix)
	case a.Type == "hook_additional_context" && a.HookEvent == "Stop":
		var content []string
		return json.Unmarshal(a.Content, &content) == nil && len(content) > 0 && strings.HasPrefix(content[0], advisor.MessagePrefix)
	// Before 0.5.0 the stop hook blocked with a decision, which older
	// transcripts still hold.
	case a.Type == "hook_blocking_error" && a.HookEvent == "Stop":
		return a.BlockingError != nil && strings.HasPrefix(a.BlockingError.BlockingError, advisor.MessagePrefix)
	}
	return false
}

// markRendered records that the last advice got its section. The advice asks
// for it in the reply of the turn it lands in, but a prompt typed while the
// model answers is recorded as a turn of its own before the advice, so the
// reply carrying the section belongs to the turn after the one the advice is
// numbered at. The section is therefore counted for the last advice wherever
// it comes, until the next advice or a compaction, which makes it moot,
// replaces it: a section further away belongs to another advice, not this one.
func markRendered(conv *Conversation) {
	if len(conv.Advices) == 0 {
		return
	}
	a := &conv.Advices[len(conv.Advices)-1]
	if n := len(conv.Compactions); n > 0 && conv.Compactions[n-1].Time.After(a.Time) {
		return
	}
	a.Rendered = true
}

// hasSection reports whether an assistant message holds the recommendation
// section the advice asks for: a line of a blockquote starting with **Jalon**,
// not the name in bold within an answer.
func hasSection(raw json.RawMessage) bool {
	return bytes.Contains(raw, []byte(advisor.SectionMark)) && advisor.HasSection(promptText(raw))
}

// resumeOf is the resume a prompt holds, nil when it names no handoff file by
// an absolute path. The last path named wins.
func resumeOf(raw json.RawMessage, at time.Time, turn, request int) *Resume {
	var res *Resume
	text := promptText(raw)
	// The tags of a slash command glue its arguments to <command-args>.
	for _, m := range handoff.Mentions(commandTag.ReplaceAllString(text, " ")) {
		if p := m.Path(); p != "" {
			res = &Resume{Path: p, Time: at, Turn: turn, Request: request}
		}
	}
	if res == nil && strings.Contains(text, "<command-name>/jalon-resume</command-name>") {
		// No path: jalon handoffs picks the handoff and prints it, see pickedHandoff.
		res = &Resume{Time: at, Turn: turn, Request: request}
	}
	return res
}

// handoffsOutput is how jalon handoffs prints the handoff it picked.
var handoffsOutput = regexp.MustCompile(`(?m)^Folder: (/.+)\nHandoff: (.+\.md)$`)

// pickedHandoff is the path of the handoff that jalon handoffs printed in a
// tool result, "" when it printed none.
func pickedHandoff(raw json.RawMessage) string {
	var blocks []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, b := range blocks {
		if b.Type != "tool_result" || len(b.Content) == 0 {
			continue
		}
		if m := handoffsOutput.FindStringSubmatch(promptText(b.Content)); m != nil {
			if p := filepath.Join(m[1], m[2]); handoff.IsPath(p) {
				return p
			}
		}
	}
	return ""
}

// promptText is the text of a prompt's content: a string, or its text blocks,
// "" when it is neither.
func promptText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &blocks) != nil {
			return ""
		}
		for _, b := range blocks {
			if b.Type == "text" {
				text += b.Text + "\n"
			}
		}
	}
	return text
}

var (
	// ideContext is what an IDE adds to a prompt: the file opened, the selection.
	ideContext = regexp.MustCompile(`(?s)<ide_\w+>.*?</ide_\w+>`)
	pasted     = regexp.MustCompile(`(?s)<pasted_content[^>]*>.*?</pasted_content[^>]*>`)
	// A slash command is recorded as its message, name and arguments in tags.
	commandMessage = regexp.MustCompile(`(?s)<command-message>.*?</command-message>`)
	commandTag     = regexp.MustCompile(`</?command-(name|args)>`)
)

// startOf is the first n runes of s without what an IDE added, with pasted
// text marked, a slash command as typed, and its spaces and line breaks
// collapsed, with … when it is cut.
func startOf(s string, n int) string {
	s = pasted.ReplaceAllString(ideContext.ReplaceAllString(s, ""), "[pasted]")
	s = commandTag.ReplaceAllString(commandMessage.ReplaceAllString(s, ""), " ")
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
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
