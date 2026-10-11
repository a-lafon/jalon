package claudecode

import (
	"encoding/json"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
)

func TestReadPrompt(t *testing.T) {
	in := `{"session_id":"abc-1","cwd":"/work/app","transcript_path":"/t/abc-1.jsonl","hook_event_name":"UserPromptSubmit","prompt":"ok où en est-on ?"}`
	got, err := Adapter{}.ReadPrompt([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := client.Prompt{Session: "abc-1", Dir: "/work/app", Human: true, Text: "ok où en est-on ?", Ref: "/t/abc-1.jsonl"}
	if got != want {
		t.Errorf("prompt = %+v, want %+v", got, want)
	}
	if _, err := (Adapter{}).ReadPrompt([]byte("not json")); err == nil {
		t.Error("bad input gave no error")
	}
}

func TestHumanDetection(t *testing.T) {
	tests := map[string]struct {
		prompt string
		human  bool
	}{
		"typed by the user":         {"ok où en est-on ?", true},
		"empty":                     {"", true},
		"a slash command":           {"/clear", true},
		"peer message":              {"Another Claude session sent a message:\n<cross-session-message>", false},
		"peer message after blanks": {"\n  \tAnother Claude session sent a message: hi", false},
		"peer message mid-turn":     {"Another Claude session sent a message while you were working:\n<cross-session-message>", false},
		"idle notice":               {"[Cross-session idle notice] jalon-a1 is idle", false},
		"delivery notice":           {"[Cross-session delivery notice] refused", false},
		"task notification":         {"<task-notification>\n<task-id>b1</task-id>", false},
		"quoted later in the text":  {"what does \"Another Claude session sent a message:\" mean?", true},
		"similar but not the same":  {"[Cross-sessions] are cool", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			in, _ := json.Marshal(map[string]string{"session_id": "s", "prompt": tt.prompt})
			p, err := Adapter{}.ReadPrompt(in)
			if err != nil {
				t.Fatal(err)
			}
			if p.Human != tt.human {
				t.Errorf("Human = %v, want %v", p.Human, tt.human)
			}
		})
	}
}

func TestCommands(t *testing.T) {
	want := client.Commands{Clear: "/clear", Compact: "/compact", Include: "@"}
	if got := (Adapter{}).Commands(); got != want {
		t.Errorf("commands = %+v, want %+v", got, want)
	}
}

func TestReply(t *testing.T) {
	b, err := Adapter{}.Reply("to the user", "to the <model> & more")
	if err != nil {
		t.Fatal(err)
	}
	// Compared as bytes: this is the format Claude Code reads.
	want := `{"hookSpecificOutput":{"additionalContext":"to the <model> & more","hookEventName":"UserPromptSubmit"},"systemMessage":"to the user"}` + "\n"
	if string(b) != want {
		t.Errorf("reply = %q, want %q", b, want)
	}
}

func TestStatusSize(t *testing.T) {
	tests := map[string]struct {
		in      string
		size    int
		session string
		ok      bool
	}{
		"count":             {`{"context_window":{"total_input_tokens":230000}}`, 230000, "", true},
		"count and session": {`{"session_id":"s1","context_window":{"total_input_tokens":230000}}`, 230000, "s1", true},
		"null":              {`{"session_id":"s1","context_window":{"total_input_tokens":null}}`, 0, "", false},
		"missing":           {`{"context_window":{}}`, 0, "", false},
		"zero":              {`{"context_window":{"total_input_tokens":0}}`, 0, "", false},
		"not json":          {`nope`, 0, "", false},
		"no window":         {`{"session_id":"s"}`, 0, "", false},
		"fractional":        {`{"context_window":{"total_input_tokens":1500.0}}`, 1500, "", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			size, session, ok := Adapter{}.StatusSize([]byte(tt.in))
			if size != tt.size || session != tt.session || ok != tt.ok {
				t.Errorf("= %d, %q, %v; want %d, %q, %v", size, session, ok, tt.size, tt.session, tt.ok)
			}
		})
	}
}

func TestContextSizeReadsTheTranscriptOfThePrompt(t *testing.T) {
	path := writeLines(t, sizeLine(212_000, false))
	size, ok := Adapter{}.ContextSize(client.Prompt{Ref: path})
	if !ok || size != 212_000 {
		t.Errorf("= %d, %v", size, ok)
	}
	if _, ok := (Adapter{}).ContextSize(client.Prompt{}); ok {
		t.Error("a prompt without a transcript has a size")
	}
}

func TestReadPromptDirIsTheProjectDirectory(t *testing.T) {
	const in = `{"session_id":"s","cwd":"/work/app/moved","prompt":"hi"}`
	tests := map[string]struct{ env, want string }{
		"variable set, cwd different": {"/work/app", "/work/app"},
		"variable empty":              {"", "/work/app/moved"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_PROJECT_DIR", tt.env)
			p, err := Adapter{}.ReadPrompt([]byte(in))
			if err != nil || p.Dir != tt.want {
				t.Errorf("Dir = %q (%v), want %q", p.Dir, err, tt.want)
			}
		})
	}
	t.Run("both empty", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", "")
		p, err := Adapter{}.ReadPrompt([]byte(`{"session_id":"s","prompt":"hi"}`))
		if err != nil || p.Dir != "" {
			t.Errorf("Dir = %q (%v), want empty", p.Dir, err)
		}
	})
}
