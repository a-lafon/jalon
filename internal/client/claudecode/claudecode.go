// Package claudecode is the Claude Code adapter: its hook and status line
// input, its hook output, the transcript tail that holds the context size, and
// its reset commands.
package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"unicode"

	"github.com/a-lafon/jalon/internal/client"
)

// Adapter implements client.Adapter for Claude Code.
type Adapter struct{}

var _ client.Adapter = Adapter{}

func (Adapter) Name() string { return "claude-code" }

// notHumanPrefixes start the prompts Claude Code runs UserPromptSubmit for
// without a person having typed them. Its hook input has no field for the
// origin of a prompt, and the transcript, which has one, is written after the
// hook runs, so the text is all there is. Seen in real transcripts.
var notHumanPrefixes = []string{
	"Another Claude session sent a message", // a message from another session, with or without "while you were working"
	"[Cross-session ",                       // idle and delivery notices
	"<task-notification>",                   // background task reports
}

func (Adapter) ReadPrompt(stdin []byte) (client.Prompt, error) {
	var in struct {
		SessionID      string `json:"session_id"`
		Cwd            string `json:"cwd"`
		Prompt         string `json:"prompt"`
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.NewDecoder(bytes.NewReader(stdin)).Decode(&in); err != nil {
		return client.Prompt{}, err
	}
	// cwd follows the agent's cd, so the handoff folder would end up wherever
	// the session wandered. Claude Code sets CLAUDE_PROJECT_DIR for hooks to the
	// directory the session started in.
	dir := os.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir = in.Cwd
	}
	return client.Prompt{
		Session: in.SessionID,
		Dir:     dir,
		Human:   isHuman(in.Prompt),
		Text:    in.Prompt,
		Ref:     in.TranscriptPath,
	}, nil
}

func isHuman(prompt string) bool {
	text := strings.TrimLeftFunc(prompt, unicode.IsSpace)
	for _, p := range notHumanPrefixes {
		if strings.HasPrefix(text, p) {
			return false
		}
	}
	return true
}

func (Adapter) ContextSize(p client.Prompt) (int, bool) { return contextSize(p.Ref) }

func (Adapter) ContextFloor(p client.Prompt) (int, bool) { return contextFloor(p.Ref) }

func (Adapter) Commands() client.Commands {
	return client.Commands{Clear: "/clear", Compact: "/compact", Include: "@"}
}

func (Adapter) Reply(userMessage, modelContext string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{
		"systemMessage": userMessage,
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": modelContext,
		},
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// StatusSize reads context_window.total_input_tokens, which is null right after
// /compact until the next API response, and the session_id.
func (Adapter) StatusSize(stdin []byte) (int, string, bool) {
	var in struct {
		SessionID     string `json:"session_id"`
		ContextWindow struct {
			TotalInputTokens *float64 `json:"total_input_tokens"`
		} `json:"context_window"`
	}
	if json.NewDecoder(bytes.NewReader(stdin)).Decode(&in) != nil ||
		in.ContextWindow.TotalInputTokens == nil || *in.ContextWindow.TotalInputTokens <= 0 {
		return 0, "", false
	}
	return int(*in.ContextWindow.TotalInputTokens), in.SessionID, true
}
