package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// codexRollout writes a Codex rollout whose session started from source, with
// a request of startSize then one of size, and returns its path.
func codexRollout(t *testing.T, source string, size int) string {
	t.Helper()
	line := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"total_tokens":%d}}}}` + "\n"
	content := `{"type":"session_meta","payload":{"id":"a1b2c3d4","source":"` + source + `"}}` + "\n" +
		fmt.Sprintf(line, startSize, startSize+100) +
		`{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}` + "\n" +
		fmt.Sprintf(line, size, size+100)
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func codexInput(repo, rollout string, extra map[string]any) string {
	in := map[string]any{"session_id": "a1b2c3d4", "turn_id": "t1", "cwd": repo, "transcript_path": rollout, "model": "m"}
	for k, v := range extra {
		in[k] = v
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func runCodexPrompt(t *testing.T, cfg, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPromptHook(context.Background(), []string{"-config", cfg, "-client", "codex"}, strings.NewReader(stdin), &out, hookNow); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func runCodexStop(t *testing.T, cfg, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runStopHook(context.Background(), []string{"-config", cfg, "-client", "codex"}, strings.NewReader(stdin), &out, hookNow); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestCodexSessionIsAdvisedThenReminded(t *testing.T) {
	useHome(t)
	cfg := configWith(t, thresholdsConfig)
	repo := gitRepo(t)
	rollout := codexRollout(t, "cli", 212_000)

	var out hookOutput
	raw := runCodexPrompt(t, cfg, codexInput(repo, rollout, map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "hi"}))
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	ctxt := out.HookSpecificOutput.AdditionalContext
	if out.SystemMessage != "Jalon: context at 212K tokens, a reset proposal follows the answer." ||
		!regexp.MustCompile(`code block: /new, then `+regexp.QuoteMeta(repo)+`/\.jalon/handoffs/2026-10-12/0900-[0-9a-z]{8}-<short-slug>\.md \(the full path\) followed by a request`).MatchString(ctxt) ||
		!strings.Contains(ctxt, "/compact <what to keep;") ||
		!strings.Contains(ctxt, `Its first line is "Session: a1b2c3d4".`) {
		t.Errorf("output = %s", raw)
	}

	// The turn ends without the section: the stop hook blocks it once.
	stop := parseStop(t, runCodexStop(t, cfg, codexInput(repo, rollout, map[string]any{"hook_event_name": "Stop", "stop_hook_active": false, "last_assistant_message": "Done."})))
	if stop.Decision != "block" || !strings.HasPrefix(stop.Reason, reminderPrefix) || !strings.HasSuffix(stop.Reason, "or a /compact line.") {
		t.Errorf("stop = %+v", stop)
	}
	if again := runCodexStop(t, cfg, codexInput(repo, rollout, map[string]any{"stop_hook_active": true, "last_assistant_message": nil})); again != "" {
		t.Errorf("second block: %s", again)
	}
}

func TestCodexExecSessionIsNotAdvised(t *testing.T) {
	useHome(t)
	cfg := configWith(t, thresholdsConfig)
	repo := gitRepo(t)
	if got := runCodexPrompt(t, cfg, codexInput(repo, codexRollout(t, "exec", 212_000), map[string]any{"prompt": "run the task"})); got != "" {
		t.Errorf("output = %s", got)
	}
}

func TestInitCodexInstallsOnlyTheHooks(t *testing.T) {
	withExecutable(t, testExe)
	hooks := filepath.Join(t.TempDir(), "hooks.json")

	// The default skills are Claude Code's: Codex gets the hooks alone.
	runInitOK(t, "-settings", hooks, "-client", "codex")
	if b, _ := os.ReadFile(hooks); !strings.Contains(string(b), "hook prompt --client codex") || !strings.Contains(string(b), "hook stop --client codex") {
		t.Errorf("hooks = %s", b)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something written under ~/.claude: %v", err)
	}
	// An explicit -skill is refused.
	var out bytes.Buffer
	if err := runInit([]string{"-settings", hooks, "-client", "codex", "-skill"}, &out); err == nil || !strings.Contains(err.Error(), "codex has no skill") {
		t.Errorf("err = %v", err)
	}
}
