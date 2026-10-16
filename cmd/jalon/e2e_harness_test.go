//go:build e2e

// The harness of the integration tests: a fake Messages API, an isolated
// Claude Code that runs Jalon's hooks against it, and the hook command that
// shows Jalon the prompts as typed by a person.
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

// A hook command that names this test binary with JALON_E2E_HOOK set runs
// typedHook instead of the tests: it shows Jalon the session's prompts as typed
// by a person, which claude -p never writes, then runs the real jalon hook.
func init() {
	if event := os.Getenv("JALON_E2E_HOOK"); event != "" {
		os.Exit(typedHook(event, os.Getenv("JALON_E2E_JALON")))
	}
}

// typedHook runs jalon hook <event> on a copy of the transcript where the
// prompts a person would have typed are marked so (origin kind "human"), as
// Claude Code's interactive terminal writes them. The prompt hook records each
// prompt's ID unless its text holds programMark; the copy marks those prompts,
// and adds an entry for one Claude Code has not written to the transcript yet,
// which claude -p can still be doing when the stop hook runs. Claude Code's own
// file is never written.
func typedHook(event, jalon string) int {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1
	}
	var hook map[string]any
	if err := json.Unmarshal(in, &hook); err != nil {
		return 1
	}
	if path, _ := hook["transcript_path"].(string); path != "" {
		if err := copyTyped(event, path, hook); err != nil {
			fmt.Fprintln(os.Stderr, "e2e:", err)
			return 1
		}
		if in, err = json.Marshal(hook); err != nil {
			return 1
		}
	}
	cmd := exec.Command(jalon, "hook", event)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(in), os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

// copyTyped records the typed prompt of a prompt event, writes the marked copy
// of the transcript at path, and points the hook input at it.
func copyTyped(event, path string, hook map[string]any) error {
	idsPath := path + ".typed-ids"
	id, _ := hook["prompt_id"].(string)
	if text, _ := hook["prompt"].(string); event == "prompt" && id != "" && !strings.Contains(text, programMark) {
		f, err := os.OpenFile(idsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(f, id)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	ids, err := os.ReadFile(idsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	copied := path + ".typed"
	if err := os.WriteFile(copied, markTyped(raw, strings.Fields(string(ids))), 0o600); err != nil {
		return err
	}
	hook["transcript_path"] = copied
	return nil
}

// programMark in a prompt's text keeps it from being recorded as typed: the
// turn it starts is one a program sent, which the stop hook never advises.
// The scenarios send the turn that crosses the threshold so, and the advice
// comes from the prompt hook at the next prompt: whether the stop hook sees the
// turn's own request depends on when Claude Code writes it to the transcript.
const programMark = "[sent by a program]"

// markTyped adds origin kind "human" to the transcript's prompt entries whose
// prompt ID is in typed, and appends an entry for each one it does not hold.
func markTyped(raw []byte, typed []string) []byte {
	want := map[string]bool{}
	for _, id := range typed {
		want[id] = true
	}
	var out bytes.Buffer
	seen := map[string]bool{}
	for _, line := range bytes.SplitAfter(raw, []byte("\n")) {
		var e map[string]any
		msg := map[string]any(nil)
		if json.Unmarshal(line, &e) == nil {
			msg, _ = e["message"].(map[string]any)
		}
		id, _ := e["promptId"].(string)
		if _, prompt := msg["content"].(string); !prompt || e["type"] != "user" || !want[id] || e["isMeta"] == true {
			out.Write(line)
			continue
		}
		seen[id] = true
		e["origin"] = map[string]any{"kind": "human"}
		b, _ := json.Marshal(e)
		out.Write(append(b, '\n'))
	}
	for _, id := range typed {
		if !seen[id] {
			seen[id] = true
			b, _ := json.Marshal(map[string]any{"type": "user", "isSidechain": false, "promptId": id,
				"origin": map[string]any{"kind": "human"}, "message": map[string]any{"role": "user", "content": "(typed)"}})
			out.Write(append(b, '\n'))
		}
	}
	return out.Bytes()
}

// step is what the fake API answers to one request: the context size it
// reports, where its cache writes go, and whether the reply has the section.
type step struct {
	size    int  // input + cache read + cache creation tokens
	fiveMin bool // cache writes to the 5-minute cache instead of the 1-hour one
	section bool // the reply ends with a Jalon section, as the instruction asks
}

// fakeAPI is a Messages API that answers request n with script[n], or the last
// step past the end, and records every request body.
type fakeAPI struct {
	script []step

	mu     sync.Mutex
	bodies []string
}

func (a *fakeAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.bodies...)
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/messages/count_tokens"):
		_, _ = io.WriteString(w, `{"input_tokens":1000}`)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
	default:
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.bodies = append(a.bodies, string(body))
	s := a.script[min(len(a.bodies), len(a.script))-1]
	a.mu.Unlock()

	const created = 1000
	writes := map[string]int{"ephemeral_5m_input_tokens": 0, "ephemeral_1h_input_tokens": created}
	if s.fiveMin {
		writes = map[string]int{"ephemeral_5m_input_tokens": created, "ephemeral_1h_input_tokens": 0}
	}
	usage := map[string]any{"input_tokens": 3, "cache_creation_input_tokens": created,
		"cache_read_input_tokens": s.size - created - 3, "cache_creation": writes, "output_tokens": 1}
	w.Header().Set("Content-Type", "text/event-stream")
	event := func(name string, data map[string]any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	}
	event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": fmt.Sprintf("msg_e2e_%d", len(a.bodies)), "type": "message", "role": "assistant",
		"model": "claude-haiku-5-5", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	text := "ok"
	if s.section {
		text = "ok\n\n> **Jalon** — Compact: keep the plan."
	}
	event("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""}})
	event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
		"delta": map[string]any{"type": "text_delta", "text": text}})
	event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	event("message_delta", map[string]any{"type": "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}})
	event("message_stop", map[string]any{"type": "message_stop"})
}

var (
	buildOnce sync.Once
	jalonBin  string
	buildErr  error
)

// claudeEnv is one isolated Claude Code: its own home, config folder and git
// project, with Jalon's hooks and a dummy key from --settings only.
type claudeEnv struct {
	t        *testing.T
	home     string
	project  string
	settings string
	api      *fakeAPI
	baseURL  string
}

// newClaude sets up the isolated client against a fake API serving script.
// With typed, the hooks see the session's prompts as typed by a person.
func newClaude(t *testing.T, typed bool, script ...step) *claudeEnv {
	t.Helper()
	if _, err := exec.LookPath("claude"); err != nil {
		if os.Getenv("JALON_E2E_REQUIRE") == "1" {
			t.Fatal("claude is not on the PATH and JALON_E2E_REQUIRE=1")
		}
		t.Skip("claude is not on the PATH")
	}
	buildOnce.Do(func() {
		// A folder of this run's own, so two runs never run each other's build.
		dir, err := os.MkdirTemp("", "jalon-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		afterTests = append(afterTests, dir)
		jalonBin = filepath.Join(dir, "jalon")
		out, err := exec.Command("go", "build", "-o", jalonBin, ".").CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	// The project's real path: Claude Code gives the hooks the resolved one
	// (/private/var on macOS, where the temp folder is under the /var symlink).
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{script: script}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	e := &claudeEnv{t: t, home: t.TempDir(), project: project, api: api, baseURL: srv.URL}
	if out, err := exec.Command("git", "init", "-q", e.project).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(e.home, ".jalon"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.home, ".jalon", "config.yaml"),
		[]byte("context:\n  advise_tokens: 50000\n  advise_step_tokens: 50000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook := func(event string) string {
		if typed {
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			return fmt.Sprintf("JALON_E2E_HOOK=%s JALON_E2E_JALON='%s' '%s'", event, jalonBin, self)
		}
		return fmt.Sprintf("'%s' hook %s", jalonBin, event)
	}
	settings, _ := json.Marshal(map[string]any{
		"apiKeyHelper": "echo sk-jalon-e2e",
		"hooks": map[string]any{
			"UserPromptSubmit": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hook("prompt")}}}},
			"Stop":             []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hook("stop")}}}},
		},
	})
	e.settings = filepath.Join(e.home, "settings.json")
	if err := os.WriteFile(e.settings, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

// run sends one prompt with claude -p, resuming the session after the first,
// and returns the bodies of the requests the fake API got during it.
func (e *claudeEnv) run(prompt string) []string {
	e.t.Helper()
	args := []string{"-p", prompt, "--settings", e.settings, "--model", "claude-haiku-5-5"}
	if id := e.session(); id != "" {
		args = append(args, "--resume", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = e.project
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + e.home,
		"TMPDIR=" + os.TempDir(),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(e.home, ".claude"),
		"ANTHROPIC_BASE_URL=" + e.baseURL,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
	}
	before := len(e.api.requests())
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("claude %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return e.api.requests()[before:]
}

// transcript is the session's transcript path, "" before the first run.
func (e *claudeEnv) transcript() string {
	paths, _ := filepath.Glob(filepath.Join(e.home, ".claude", "projects", "*", "*.jsonl"))
	if len(paths) != 1 {
		return ""
	}
	return paths[0]
}

func (e *claudeEnv) session() string {
	if t := e.transcript(); t != "" {
		return strings.TrimSuffix(filepath.Base(t), ".jsonl")
	}
	return ""
}

// state is Jalon's state file of the session.
func (e *claudeEnv) state() map[string]any {
	e.t.Helper()
	var st map[string]any
	b, err := os.ReadFile(filepath.Join(e.home, ".jalon", "sessions", e.session()+".json"))
	if err != nil || json.Unmarshal(b, &st) != nil {
		e.t.Fatalf("state: %v %s", err, b)
	}
	return st
}
