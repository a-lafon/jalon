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
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

// Adapter implements client.Adapter for Claude Code.
type Adapter struct{}

var (
	_ client.Adapter = Adapter{}
	_ client.Stopper = Adapter{}
)

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
		PromptID       string `json:"prompt_id"`
		Cwd            string `json:"cwd"`
		Prompt         string `json:"prompt"`
		TranscriptPath string `json:"transcript_path"`
	}
	if err := json.NewDecoder(bytes.NewReader(stdin)).Decode(&in); err != nil {
		return client.Prompt{}, err
	}
	return client.Prompt{
		Session: in.SessionID,
		ID:      in.PromptID,
		Dir:     projectDir(in.Cwd),
		Human:   isHuman(in.Prompt),
		Text:    in.Prompt,
		Ref:     in.TranscriptPath,
	}, nil
}

// ReadStop reads the Stop hook input. stop_hook_active is true when a Stop
// hook already blocked this turn.
func (Adapter) ReadStop(stdin []byte) (client.Stop, error) {
	var in struct {
		SessionID            string `json:"session_id"`
		PromptID             string `json:"prompt_id"`
		Cwd                  string `json:"cwd"`
		TranscriptPath       string `json:"transcript_path"`
		StopHookActive       bool   `json:"stop_hook_active"`
		LastAssistantMessage string `json:"last_assistant_message"`
	}
	if err := json.NewDecoder(bytes.NewReader(stdin)).Decode(&in); err != nil {
		return client.Stop{}, err
	}
	return client.Stop{
		Prompt:      client.Prompt{Session: in.SessionID, ID: in.PromptID, Dir: projectDir(in.Cwd), Ref: in.TranscriptPath},
		Blocked:     in.StopHookActive,
		LastMessage: in.LastAssistantMessage,
	}, nil
}

// projectDir is the directory the session started in. cwd follows the agent's
// cd, so the handoff folder would end up wherever the session wandered; Claude
// Code sets CLAUDE_PROJECT_DIR for hooks to the directory the session started in.
func projectDir(cwd string) string {
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	return cwd
}

func (Adapter) IsTyped(prompt string) bool { return isHuman(prompt) }

// isHuman reports whether the prompt hook takes prompt for one a person typed.
func isHuman(prompt string) bool {
	text := strings.TrimLeftFunc(prompt, unicode.IsSpace)
	for _, p := range notHumanPrefixes {
		if strings.HasPrefix(text, p) {
			return false
		}
	}
	return true
}

func (Adapter) ReadTail(p client.Prompt, promptID string, typed bool) client.Tail {
	return readTail(p.Ref, promptID, typed)
}

func (Adapter) ContextFloor(p client.Prompt) (int, bool) { return contextFloor(p.Ref) }

func (Adapter) Commands() client.Commands {
	cmds := client.Commands{Clear: "/clear", Compact: "/compact", Include: "@"}
	// A command is offered only when the skill jalon init wrote for it is there.
	if ownedSkill("jalon-resume") {
		cmds.Resume = "/jalon-resume"
	}
	if ownedSkill("jalon-handoff") {
		cmds.Handoff = "/jalon-handoff"
	}
	return cmds
}

func (Adapter) Reply(userMessage, modelContext string) ([]byte, error) {
	return hookjson.Reply(userMessage, modelContext)
}

// StopReply makes Claude Code go on with the turn: the reason reaches the model
// as a meta user message, and is not shown to the user.
func (Adapter) StopReply(reason, userMessage string) ([]byte, error) {
	return hookjson.StopReply(reason, userMessage)
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
