package hookjson

import (
	"bytes"
	"encoding/json"
)

// Reply is the prompt hook output: modelContext goes to the model, and
// userMessage, when not "", to the user.
func Reply(userMessage, modelContext string) ([]byte, error) {
	return contextReply("UserPromptSubmit", modelContext, userMessage)
}

// StopContextReply is the stop hook output that makes Claude Code go on with
// the turn: the reason reaches the model as additional context, and
// userMessage, when not "", the user.
func StopContextReply(reason, userMessage string) ([]byte, error) {
	return contextReply("Stop", reason, userMessage)
}

func contextReply(event, modelContext, userMessage string) ([]byte, error) {
	out := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     event,
			"additionalContext": modelContext,
		},
	}
	if userMessage != "" {
		out["systemMessage"] = userMessage
	}
	return encode(out)
}

// StopReply is the stop hook output that makes the client go on with the
// turn: the reason reaches the model, and userMessage, when not "", the user.
func StopReply(reason, userMessage string) ([]byte, error) {
	out := map[string]any{"decision": "block", "reason": reason}
	if userMessage != "" {
		out["systemMessage"] = userMessage
	}
	return encode(out)
}

// encode writes v as one line of JSON, leaving <, > and & as they are.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
