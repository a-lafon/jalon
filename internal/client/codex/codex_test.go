package codex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/client"
)

func TestReadPrompt(t *testing.T) {
	p, err := Adapter{}.ReadPrompt([]byte(`{"session_id":"s1","turn_id":"t1","cwd":"/work/app","prompt":"go on","transcript_path":"/r.jsonl","hook_event_name":"UserPromptSubmit","model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := client.Prompt{Session: "s1", ID: "t1", Dir: "/work/app", Human: true, Text: "go on", Ref: "/r.jsonl"}
	if p != want {
		t.Errorf("prompt = %+v, want %+v", p, want)
	}
}

func TestPromptsNotWrittenByAPerson(t *testing.T) {
	for _, in := range []string{
		// A subagent's prompt carries the root thread's session.
		`{"session_id":"s1","turn_id":"t1","prompt":"look","agent_id":"a1"}`,
		// The stop hook's reason, which Codex sends on as a continuation prompt.
		`{"session_id":"s1","turn_id":"t1","prompt":" ` + advisor.MessagePrefix + `212K tokens."}`,
	} {
		if p, err := (Adapter{}).ReadPrompt([]byte(in)); err != nil || p.Human {
			t.Errorf("%s: human = %v, err = %v", in, p.Human, err)
		}
	}
}

func TestReadStop(t *testing.T) {
	ev, err := Adapter{}.ReadStop([]byte(`{"session_id":"s1","turn_id":"t1","cwd":"/work/app","transcript_path":"/r.jsonl","stop_hook_active":true,"last_assistant_message":"Done."}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.Session != "s1" || ev.ID != "t1" || ev.Dir != "/work/app" || ev.Ref != "/r.jsonl" || !ev.Blocked || ev.LastMessage != "Done." {
		t.Errorf("stop = %+v", ev)
	}
	ev, err = Adapter{}.ReadStop([]byte(`{"session_id":"s1","turn_id":"t1","last_assistant_message":null,"agent_id":"a1"}`))
	if err != nil || ev.ID != "" || ev.Blocked || ev.LastMessage != "" {
		t.Errorf("subagent stop = %+v, err = %v", ev, err)
	}
}

func TestCommandsAndNoStatusLine(t *testing.T) {
	if got := (Adapter{}).Commands(); got != (client.Commands{Clear: "/new", Compact: "/compact"}) {
		t.Errorf("commands = %+v", got)
	}
	if _, _, ok := (Adapter{}).StatusSize([]byte(`{"context_window":{"total_input_tokens":5}}`)); ok {
		t.Error("a status line size from Codex")
	}
}

// rollout writes a rollout file holding lines and returns its path.
func rollout(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func meta(source string) string {
	return `{"timestamp":"2026-10-17T09:00:00.000Z","type":"session_meta","payload":{"id":"a1b2c3d4","cwd":"/work/app","originator":"codex_cli_rs","source":` + source + `}}`
}

func tokenCountLine(input, total int) string {
	return `{"timestamp":"2026-10-17T09:00:01.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":999999,"total_tokens":999999},"last_token_usage":{"input_tokens":` +
		strconv.Itoa(input) + `,"cached_input_tokens":0,"output_tokens":10,"total_tokens":` + strconv.Itoa(total) + `},"model_context_window":272000},"rate_limits":null}}`
}

const userMessage = `{"timestamp":"2026-10-17T09:00:02.000Z","type":"event_msg","payload":{"type":"user_message","message":"token_count?"}}`

func TestReadTail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		size  int
		typed bool
	}{
		{"last request", []string{meta(`"cli"`), tokenCountLine(50_000, 50_400), userMessage, tokenCountLine(212_000, 212_300)}, 212_000, true},
		{"estimate after a compaction", []string{meta(`"vscode"`), tokenCountLine(212_000, 212_300), `{"type":"compacted","payload":{"message":"summary"}}`, tokenCountLine(0, 8113)}, 8113, true},
		{"no usage yet", []string{meta(`"cli"`), `{"type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{}}}`, userMessage}, 0, true},
		{"codex exec", []string{meta(`"exec"`), tokenCountLine(60_000, 60_100)}, 60_000, false},
		{"subagent", []string{meta(`{"subagent":"review"}`), tokenCountLine(60_000, 60_100)}, 60_000, false},
		{"no session_meta first", []string{tokenCountLine(60_000, 60_100), meta(`"cli"`)}, 60_000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := client.Prompt{Ref: rollout(t, tc.lines...)}
			got := Adapter{}.ReadTail(p, "", true)
			if got.Size != tc.size || got.Typed != tc.typed || got.CacheTTL != 0 {
				t.Errorf("tail = %+v, want size %d typed %v", got, tc.size, tc.typed)
			}
			if (Adapter{}).ReadTail(p, "", false).Typed {
				t.Error("typed looked for when not asked")
			}
		})
	}
	if got := (Adapter{}).ReadTail(client.Prompt{Ref: filepath.Join(t.TempDir(), "none.jsonl")}, "", true); got != (client.Tail{}) {
		t.Errorf("missing rollout: %+v", got)
	}
}

func TestReadSizeLooksBeyondTheFirstMegabyte(t *testing.T) {
	filler := `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + strings.Repeat("x", 4000) + `"}]}}`
	lines := []string{meta(`"cli"`), tokenCountLine(70_000, 70_100)}
	for range 400 { // about 1.6 MB after the last token_count
		lines = append(lines, filler)
	}
	if got := readSize(rollout(t, lines...)); got != 70_000 {
		t.Errorf("size = %d, want 70000", got)
	}
}

func TestContextFloor(t *testing.T) {
	path := rollout(t, meta(`"cli"`), `{"type":"event_msg","payload":{"type":"token_count","info":null}}`, tokenCountLine(0, 100), tokenCountLine(48_000, 48_200), tokenCountLine(90_000, 90_100))
	if got, ok := (Adapter{}).ContextFloor(client.Prompt{Ref: path}); got != 48_000 || !ok {
		t.Errorf("floor = %d, %v; want 48000", got, ok)
	}
	if _, ok := (Adapter{}).ContextFloor(client.Prompt{Ref: rollout(t, meta(`"cli"`))}); ok {
		t.Error("a floor before any request")
	}
}
