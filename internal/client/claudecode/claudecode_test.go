package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
)

func TestReadPrompt(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	in := `{"session_id":"abc-1","prompt_id":"p1","cwd":"/work/app","transcript_path":"/t/abc-1.jsonl","hook_event_name":"UserPromptSubmit","prompt":"ok où en est-on ?"}`
	got, err := Adapter{}.ReadPrompt([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	want := client.Prompt{Session: "abc-1", ID: "p1", Dir: "/work/app", Human: true, Text: "ok où en est-on ?", Ref: "/t/abc-1.jsonl"}
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
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillsDir := filepath.Join(home, ".claude", "skills")
	resume, _ := skills.ReadFile("skills/jalon-resume/SKILL.md")
	handoff, _ := skills.ReadFile("skills/jalon-handoff/SKILL.md")
	old := []byte("---\nname: jalon\n---\n" + skillMarker + "\n")
	for _, tt := range []struct {
		name            string
		files           map[string][]byte // skill folder: content
		resume, handoff string
	}{
		{"no skill", nil, "", ""},
		{"someone else's skills", map[string][]byte{"jalon-resume": []byte("---\nname: jalon-resume\n---\n"), "jalon-handoff": []byte("x")}, "", ""},
		{"jalon's skills", map[string][]byte{"jalon-resume": resume, "jalon-handoff": handoff}, "/jalon-resume", "/jalon-handoff"},
		{"resume only", map[string][]byte{"jalon-resume": resume}, "/jalon-resume", ""},
		// The 0.2.0 skill's /jalon resume is never offered: jalon init replaces it.
		{"0.2.0 skill only", map[string][]byte{"jalon": old}, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.RemoveAll(skillsDir); err != nil {
				t.Fatal(err)
			}
			for dir, content := range tt.files {
				if err := os.MkdirAll(filepath.Join(skillsDir, dir), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillsDir, dir, "SKILL.md"), content, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := client.Commands{Clear: "/clear", Compact: "/compact", Include: "@", Resume: tt.resume, Handoff: tt.handoff}
			if got := (Adapter{}).Commands(); got != want {
				t.Errorf("commands = %+v, want %+v", got, want)
			}
		})
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
	// No message for the user: no systemMessage, rather than an empty one.
	b, _ = Adapter{}.Reply("", "note")
	if want := `{"hookSpecificOutput":{"additionalContext":"note","hookEventName":"UserPromptSubmit"}}` + "\n"; string(b) != want {
		t.Errorf("reply = %q, want %q", b, want)
	}
}

func TestReadStop(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	tests := map[string]struct {
		in   string
		want client.Stop
	}{
		// The input read from Claude Code on 2026-10-12, its unused fields included.
		"stop event": {
			`{"session_id":"s1","prompt_id":"p1","transcript_path":"/t.jsonl","cwd":"/work/app","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"Bonjour !","permission_mode":"default","effort":"high","background_tasks":[],"session_crons":[]}`,
			client.Stop{Prompt: client.Prompt{Session: "s1", ID: "p1", Dir: "/work/app", Ref: "/t.jsonl"}, LastMessage: "Bonjour !"},
		},
		"already blocked": {
			`{"session_id":"s1","prompt_id":"p1","stop_hook_active":true,"last_assistant_message":"> **Jalon** — Compact"}`,
			client.Stop{Prompt: client.Prompt{Session: "s1", ID: "p1"}, Blocked: true, LastMessage: "> **Jalon** — Compact"},
		},
		"no stop_hook_active nor prompt_id": {
			`{"session_id":"s1","cwd":"/work/app"}`,
			client.Stop{Prompt: client.Prompt{Session: "s1", Dir: "/work/app"}},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Adapter{}.ReadStop([]byte(tt.in))
			if err != nil || got != tt.want {
				t.Errorf("stop = %+v (%v), want %+v", got, err, tt.want)
			}
		})
	}
	t.Run("project directory", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", "/work/app")
		if got, _ := (Adapter{}).ReadStop([]byte(`{"cwd":"/tmp/other"}`)); got.Dir != "/work/app" {
			t.Errorf("Dir = %q, want /work/app", got.Dir)
		}
	})
	if _, err := (Adapter{}).ReadStop([]byte("not json")); err == nil {
		t.Error("bad input gave no error")
	}
}

func TestStopReply(t *testing.T) {
	tests := map[string]struct{ reason, message, want string }{
		"without a message": {"Jalon: reminder …", "", `{"decision":"block","reason":"Jalon: reminder …"}` + "\n"},
		"with a message": {"Jalon: context at 296K tokens. <dir> & more", "Jalon: context at 296K tokens, a reset proposal follows.",
			`{"decision":"block","reason":"Jalon: context at 296K tokens. <dir> & more","systemMessage":"Jalon: context at 296K tokens, a reset proposal follows."}` + "\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := Adapter{}.StopReply(tt.reason, tt.message)
			// Compared as bytes: this is the format Claude Code reads.
			if err != nil || string(b) != tt.want {
				t.Errorf("reply = %q (%v), want %q", b, err, tt.want)
			}
		})
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

func TestReadTailReadsTheTranscriptOfThePrompt(t *testing.T) {
	path := writeLines(t, typedLine("p1", true, false), sizeLine(212_000, false))
	if got := (Adapter{}).ReadTail(client.Prompt{Ref: path}, "p1", true); got != (client.Tail{Size: 212_000, Typed: true}) {
		t.Errorf("tail = %+v", got)
	}
	if got := (Adapter{}).ReadTail(client.Prompt{}, "", true); got != (client.Tail{}) {
		t.Errorf("a prompt without a transcript has %+v", got)
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
