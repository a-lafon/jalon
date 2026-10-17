// Package codex is the Codex CLI adapter: its hook input and output, the
// rollout file that holds the context size, and its reset commands. Codex's
// hooks take the input and the output of Claude Code's, so the prompt reply and
// the hooks file are hookjson's, as Claude Code's are.
package codex

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

// Adapter implements client.Adapter for Codex.
type Adapter struct{}

var (
	_ client.Adapter = Adapter{}
	_ client.Stopper = Adapter{}
)

func (Adapter) Name() string { return "codex" }

// hookInput is the part of the prompt and stop hook input Jalon reads. A
// subagent's event has agent_id set and the root thread's session_id, so its
// sizes would mix with the main conversation's.
type hookInput struct {
	SessionID            string  `json:"session_id"`
	TurnID               string  `json:"turn_id"`
	Cwd                  string  `json:"cwd"`
	Prompt               string  `json:"prompt"`
	TranscriptPath       string  `json:"transcript_path"`
	AgentID              string  `json:"agent_id"`
	StopHookActive       bool    `json:"stop_hook_active"`
	LastAssistantMessage *string `json:"last_assistant_message"`
}

func readInput(stdin []byte) (hookInput, error) {
	var in hookInput
	err := json.NewDecoder(bytes.NewReader(stdin)).Decode(&in)
	return in, err
}

// Codex gives no project directory apart from cwd: Jalon takes the git top
// level above it as the project.
func (Adapter) ReadPrompt(stdin []byte) (client.Prompt, error) {
	in, err := readInput(stdin)
	if err != nil {
		return client.Prompt{}, err
	}
	return client.Prompt{
		Session: in.SessionID,
		ID:      in.TurnID,
		Dir:     in.Cwd,
		Human:   in.AgentID == "" && isHuman(in.Prompt),
		Text:    in.Prompt,
		Ref:     in.TranscriptPath,
	}, nil
}

// ReadStop reads the Stop hook input; a subagent's stop has no prompt ID, so
// the stop hook leaves it alone.
func (Adapter) ReadStop(stdin []byte) (client.Stop, error) {
	in, err := readInput(stdin)
	if err != nil {
		return client.Stop{}, err
	}
	id := in.TurnID
	if in.AgentID != "" {
		id = ""
	}
	last := ""
	if in.LastAssistantMessage != nil {
		last = *in.LastAssistantMessage
	}
	return client.Stop{
		Prompt:      client.Prompt{Session: in.SessionID, ID: id, Dir: in.Cwd, Ref: in.TranscriptPath},
		Blocked:     in.StopHookActive,
		LastMessage: last,
	}, nil
}

func (Adapter) IsTyped(prompt string) bool { return isHuman(prompt) }

// isHuman is false for Jalon's own stop hook reason, which Codex sends on as a
// continuation prompt.
func isHuman(prompt string) bool {
	return !strings.HasPrefix(strings.TrimLeftFunc(prompt, unicode.IsSpace), "Jalon: ")
}

func (Adapter) ReadTail(p client.Prompt, _ string, typed bool) client.Tail {
	tail := client.Tail{Size: readSize(p.Ref)}
	if typed {
		tail.Typed = typedSession(p.Ref)
	}
	return tail
}

func (Adapter) ContextFloor(p client.Prompt) (int, bool) { return contextFloor(p.Ref) }

// Commands: @ in Codex only inserts a path, which the model then reads, so the
// resume line gives the path alone.
func (Adapter) Commands() client.Commands {
	return client.Commands{Clear: "/new", Compact: "/compact"}
}

func (Adapter) Reply(userMessage, modelContext string) ([]byte, error) {
	return hookjson.Reply(userMessage, modelContext)
}

// StopReply makes Codex go on with the turn with a block decision: Claude
// Code's additional context on Stop is not tried with Codex.
func (Adapter) StopReply(reason, userMessage string) ([]byte, error) {
	return hookjson.StopReply(reason, userMessage)
}

// StatusSize reports no size: Codex's status line shows built-in items, it
// runs no command.
func (Adapter) StatusSize([]byte) (int, string, bool) { return 0, "", false }
