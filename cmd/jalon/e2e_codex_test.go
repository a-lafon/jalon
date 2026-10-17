//go:build e2e

// Integration tests with the real Codex CLI: codex exec runs Jalon's hooks
// against a fake Responses API that reports scripted context sizes, with no
// model, no account and no network beyond localhost.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A hook command with JALON_E2E_CODEX_HOOK set runs codexTypedHook instead of
// the tests.
func init() {
	if event := os.Getenv("JALON_E2E_CODEX_HOOK"); event != "" {
		os.Exit(codexTypedHook(event, os.Getenv("JALON_E2E_JALON")))
	}
}

// codexTypedHook runs jalon hook <event> --client codex on a copy of the
// rollout whose session_meta says the session started from the interactive
// CLI, as Codex's terminal writes it: codex exec writes "exec", which Jalon
// never advises. Codex's own file is never written.
func codexTypedHook(event, jalon string) int {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	var hook map[string]any
	if err := json.Unmarshal(in, &hook); err != nil {
		return 1
	}
	if skipStop(event, hook) {
		return 0
	}
	if path, _ := hook["transcript_path"].(string); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "e2e:", err)
			return 1
		}
		first, rest, _ := bytes.Cut(raw, []byte("\n"))
		var meta map[string]any
		if json.Unmarshal(first, &meta) == nil {
			if p, ok := meta["payload"].(map[string]any); ok {
				p["source"] = "cli"
				first, _ = json.Marshal(meta)
			}
		}
		copied := path + ".typed"
		if err := os.WriteFile(copied, append(append(first, '\n'), rest...), 0o600); err != nil {
			return 1
		}
		hook["transcript_path"] = copied
		if in, err = json.Marshal(hook); err != nil {
			return 1
		}
	}
	cmd := exec.Command(jalon, "hook", event, "--client", "codex")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(in), os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// noStopMark in a prompt's text keeps the stop hook from running at the end of
// its turn, so a crossing during that turn is left to the next prompt's hook.
const noStopMark = "[no stop hook]"

// skipStop records the turn of a prompt holding noStopMark, and tells whether
// a stop event is of such a turn.
func skipStop(event string, hook map[string]any) bool {
	path, _ := hook["transcript_path"].(string)
	turn, _ := hook["turn_id"].(string)
	if path == "" || turn == "" {
		return false
	}
	marked := path + ".no-stop"
	if text, _ := hook["prompt"].(string); event == "prompt" && strings.Contains(text, noStopMark) {
		f, err := os.OpenFile(marked, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintln(f, turn)
			f.Close()
		}
		return false
	}
	ids, _ := os.ReadFile(marked)
	return event == "stop" && strings.Contains(string(ids), turn+"\n")
}

// fakeResponses is a Responses API that answers request n with script[n], or
// the last step past the end, and records every request body.
type fakeResponses struct {
	script []step

	mu     sync.Mutex
	bodies []string
}

func (a *fakeResponses) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.bodies...)
}

func (a *fakeResponses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.bodies = append(a.bodies, string(body))
	n := len(a.bodies)
	s := a.script[min(n, len(a.script))-1]
	a.mu.Unlock()

	text := "ok"
	if s.section {
		text = "ok\n\n> **Jalon** — Compact: keep the plan."
	}
	id := fmt.Sprintf("resp_e2e_%d", n)
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range []map[string]any{
		{"type": "response.created", "response": map[string]any{"id": id}},
		{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": fmt.Sprintf("msg_e2e_%d", n),
			"content": []any{map[string]any{"type": "output_text", "text": text}}}},
		{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{
			"input_tokens": s.size, "input_tokens_details": map[string]any{"cached_tokens": s.size - 1000},
			"output_tokens": 5, "output_tokens_details": map[string]any{"reasoning_tokens": 0}, "total_tokens": s.size + 5}}},
	} {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], b)
	}
}

// codexEnv is one isolated Codex: its own home, CODEX_HOME and git project.
type codexEnv struct {
	t       *testing.T
	home    string
	project string
	api     *fakeResponses
}

// newCodex sets up the isolated client against a fake API serving script.
// With typed, the hooks see the session as one started from the interactive
// CLI; without, the hooks are the ones jalon init --client codex writes.
func newCodex(t *testing.T, typed bool, script ...step) *codexEnv {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		if os.Getenv("JALON_E2E_REQUIRE") == "1" {
			t.Fatal("codex is not on the PATH and JALON_E2E_REQUIRE=1")
		}
		t.Skip("codex is not on the PATH")
	}
	buildJalon(t)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeResponses{script: script}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	e := &codexEnv{t: t, home: t.TempDir(), project: project, api: api}
	if out, err := exec.Command("git", "init", "-q", e.project).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeE2EConfig(t, e.home)
	if err := os.MkdirAll(e.codexHome(), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("model = \"e2e-model\"\nmodel_provider = \"e2e\"\n\n[model_providers.e2e]\nname = \"e2e\"\nbase_url = %q\nwire_api = \"responses\"\n", srv.URL+"/v1")
	if err := os.WriteFile(filepath.Join(e.codexHome(), "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if !typed {
		cmd := exec.Command(jalonBin, "init", "--client", "codex")
		cmd.Env = e.env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("jalon init --client codex: %v\n%s", err, out)
		}
		return e
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hook := func(event string) any {
		cmd := fmt.Sprintf("JALON_E2E_CODEX_HOOK=%s JALON_E2E_JALON='%s' '%s'", event, jalonBin, self)
		return []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": cmd, "timeout": 10}}}}
	}
	hooks, _ := json.Marshal(map[string]any{"hooks": map[string]any{"UserPromptSubmit": hook("prompt"), "Stop": hook("stop")}})
	if err := os.WriteFile(filepath.Join(e.codexHome(), "hooks.json"), hooks, 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *codexEnv) codexHome() string { return filepath.Join(e.home, ".codex") }

func (e *codexEnv) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"TMPDIR=" + os.TempDir(),
		"CODEX_HOME=" + e.codexHome(),
	}
}

// run sends one prompt with codex exec, resuming the session after the first,
// and returns the bodies of the requests the fake API got during it. The
// hooks run without the trust Codex asks of a person in /hooks.
func (e *codexEnv) run(prompt string) []string {
	e.t.Helper()
	args := []string{"exec", "--dangerously-bypass-hook-trust", "-C", e.project}
	if id := e.session(); id != "" {
		args = append(args, "resume", id)
	}
	args = append(args, prompt)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = e.project
	cmd.Env = e.env()
	before := len(e.api.requests())
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("codex %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return e.api.requests()[before:]
}

// rollout is the session's rollout path, "" before the first run.
func (e *codexEnv) rollout() string {
	paths, _ := filepath.Glob(filepath.Join(e.codexHome(), "sessions", "*", "*", "*", "rollout-*.jsonl"))
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

// session is the session ID, the UUID that ends the rollout's name.
func (e *codexEnv) session() string {
	name := strings.TrimSuffix(filepath.Base(e.rollout()), ".jsonl")
	if len(name) < 36 {
		return ""
	}
	return name[len(name)-36:]
}

// state is Jalon's state file of the session.
func (e *codexEnv) state() map[string]any { return readState(e.t, e.home, e.session()) }

// lastInput is the text of the last input item of a request body: the prompt,
// or what a stop hook block sent the model.
func lastInput(t *testing.T, body string) string {
	t.Helper()
	var b struct {
		Input []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil || len(b.Input) == 0 {
		t.Fatalf("request body: %v", err)
	}
	var texts []string
	for _, c := range b.Input[len(b.Input)-1].Content {
		texts = append(texts, c.Text)
	}
	return strings.Join(texts, "\n")
}

// The sizes stay below the window Codex assumes for an unknown model, so
// Codex never compacts on its own.

func TestE2ECodexPromptHookAdviceReachesTheModel(t *testing.T) {
	// Given a session typed in whose context grows 60K a request from 30K
	c := newCodex(t, true, step{size: 30_000, section: true}, step{size: 90_000, section: true}, step{size: 95_000, section: true})
	c.run("first")
	// and a turn that crosses advise_tokens with no stop hook to see it
	if second := c.run("second " + noStopMark); holding(second, "Jalon:") > 0 {
		t.Error("the turn with no stop hook got Jalon's text")
	}
	// When the next prompt comes
	third := c.run("third")
	// Then its one request holds the advice, with Codex's commands
	if len(third) != 1 {
		t.Fatalf("third prompt: %d requests, want 1 (a reply with the section is not blocked)", len(third))
	}
	for _, want := range []string{"Jalon: this conversation holds about 90K tokens", "code block: /new, then " + c.project + "/.jalon/handoffs/", "/compact <what to keep;"} {
		if !strings.Contains(third[0], want) {
			t.Errorf("the request does not hold %q", want)
		}
	}
	if st := c.state(); st["last_advised"] != float64(90_000) || st["floor"] != float64(30_000) {
		t.Errorf("state = %v", st)
	}
}

func TestE2ECodexStopHookAdvisesACrossingOnce(t *testing.T) {
	// Given a session typed in at 30K
	c := newCodex(t, true, step{size: 30_000, section: true}, step{size: 90_000}, step{size: 95_000, section: true})
	c.run("first")
	// When a turn's own request crosses advise_tokens
	second := c.run("second")
	// Then the stop hook blocks once: one more request, asking for the section
	if len(second) != 2 {
		t.Fatalf("second prompt: %d requests, want 2", len(second))
	}
	if got := lastInput(t, second[1]); !strings.Contains(got, "Jalon: context at 90K tokens. Your answer to the user is done") || !strings.Contains(got, "/new") {
		t.Errorf("what the stop hook sent the model: %q", got)
	}
	if st := c.state(); st["last_advised"] != float64(90_000) {
		t.Errorf("state = %v", st)
	}
}

func TestE2ECodexRemindsOnceWhenTheSectionIsMissing(t *testing.T) {
	// Given the prompt hook's advice, and replies that leave out the section
	c := newCodex(t, true, step{size: 30_000, section: true}, step{size: 90_000, section: true}, step{size: 95_000}, step{size: 96_000})
	c.run("first")
	c.run("second " + noStopMark)
	third := c.run("third")
	// Then the stop hook reminds the model once, and lets the second reply end the turn
	if len(third) != 2 {
		t.Fatalf("third prompt: %d requests, want 2", len(third))
	}
	if got := lastInput(t, third[1]); !strings.Contains(got, reminderPrefix+", context at 90K tokens") {
		t.Errorf("what the stop hook sent the model: %q", got)
	}
}

func TestE2ECodexExecIsNotAdvised(t *testing.T) {
	// Given the hooks jalon init --client codex writes, in a codex exec session
	c := newCodex(t, false, step{size: 30_000, section: true}, step{size: 90_000, section: true}, step{size: 150_000, section: true})
	// When its context grows well past advise_tokens
	for _, p := range []string{"first", "second", "third"} {
		if reqs := c.run(p); len(reqs) != 1 || holding(reqs, "Jalon:") > 0 {
			t.Errorf("%s: %d requests, %d with Jalon's text; want 1 and none", p, len(reqs), holding(reqs, "Jalon:"))
		}
	}
	// Then nothing was advised, though the hooks ran and recorded the size
	if st := c.state(); st["size"] != float64(150_000) || st["last_advised"] != float64(0) {
		t.Errorf("state = %v", st)
	}
}
