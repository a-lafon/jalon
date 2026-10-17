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

	"github.com/a-lafon/jalon/internal/client/codex"
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
	// Codex still gets a block decision, not Claude Code's additional context.
	var stop struct{ Decision, Reason string }
	if err := json.Unmarshal([]byte(runCodexStop(t, cfg, codexInput(repo, rollout, map[string]any{"hook_event_name": "Stop", "stop_hook_active": false, "last_assistant_message": "Done."}))), &stop); err != nil {
		t.Fatal(err)
	}
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

// writeCodexSkills writes Jalon's Codex skills in the test's home, as jalon
// init --client codex does.
func writeCodexSkills(t *testing.T) {
	t.Helper()
	skills, err := codex.Adapter{}.Skills(testExe)
	if err != nil {
		t.Fatal(err)
	}
	for _, sk := range skills {
		if err := os.MkdirAll(filepath.Dir(sk.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sk.Path, sk.Content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCodexPromptHookAnswersTheHandoffCommand(t *testing.T) {
	prompt := func(t *testing.T, text string) string {
		t.Helper()
		useHome(t)
		writeCodexSkills(t)
		cfg := configWith(t, thresholdsConfig)
		return runCodexPrompt(t, cfg, codexInput(gitRepo(t), codexRollout(t, "cli", 50_000), map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": text}))
	}
	// Below advise_tokens, $jalon-handoff alone gets the handoff instruction.
	var out hookOutput
	raw := prompt(t, "$jalon-handoff the parser")
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	if ctxt := out.HookSpecificOutput.AdditionalContext; out.SystemMessage != "Jalon: handoff asked at 50K tokens." ||
		!strings.HasPrefix(ctxt, "Jalon: the user typed $jalon-handoff to ask for a handoff") || !strings.Contains(ctxt, `say what to stress: "the parser".`) {
		t.Errorf("output = %s", raw)
	}
	for _, text := range []string{"$jalon-handoffs", "please run $jalon-handoff", "/jalon-handoff", "$jalon-handoff-x now"} {
		if got := prompt(t, text); got != "" {
			t.Errorf("%q: output = %q, want nothing", text, got)
		}
	}
	// Without the skill, the command is not Jalon's.
	useHome(t)
	if got := runCodexPrompt(t, configWith(t, thresholdsConfig), codexInput(gitRepo(t), codexRollout(t, "cli", 50_000), map[string]any{"prompt": "$jalon-handoff"})); got != "" {
		t.Errorf("no skill: output = %q", got)
	}
}

func TestInitCodexInstallsTheHooksAndSkills(t *testing.T) {
	withExecutable(t, testExe)
	hooks := filepath.Join(t.TempDir(), "hooks.json")
	skills, err := codex.Adapter{}.Skills(testExe)
	if err != nil || len(skills) != 10 {
		t.Fatalf("skills = %d, %v", len(skills), err)
	}

	runInitOK(t, "-settings", hooks, "-client", "codex")
	if b, _ := os.ReadFile(hooks); !strings.Contains(string(b), "hook prompt --client codex") || !strings.Contains(string(b), "hook stop --client codex") {
		t.Errorf("hooks = %s", b)
	}
	for _, sk := range skills {
		if !strings.HasPrefix(sk.Path, filepath.Join(os.Getenv("HOME"), ".agents", "skills")+string(filepath.Separator)) {
			t.Errorf("skill outside ~/.agents/skills: %s", sk.Path)
		}
		if b, err := os.ReadFile(sk.Path); err != nil || !bytes.Equal(b, sk.Content) {
			t.Errorf("%s: %v\n%s", sk.Path, err, b)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".claude")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something written under ~/.claude: %v", err)
	}
	if out := runInitOK(t, "-settings", hooks, "-client", "codex"); strings.Contains(out, "Wrote") {
		t.Errorf("a rerun wrote:\n%s", out)
	}

	// CODEX_HOME moves the hooks, not the skills: Codex reads ~/.agents/skills whatever it is.
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	runInitOK(t, "-client", "codex")
	if _, err := os.Stat(filepath.Join(codexHome, "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("skills under CODEX_HOME: %v", err)
	}
}

func TestInitCodexLeavesSomeoneElsesSkill(t *testing.T) {
	withExecutable(t, testExe)
	hooks := filepath.Join(t.TempDir(), "hooks.json")
	theirs := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "jalon-help", "SKILL.md")
	writeSkill(t, theirs, foreignSkill)

	// Their SKILL.md stays, and no openai.yaml of Jalon's goes into their skill.
	out := runInitOK(t, "-settings", hooks, "-client", "codex")
	if b, _ := os.ReadFile(theirs); string(b) != foreignSkill {
		t.Errorf("their skill = %q", b)
	}
	yaml := filepath.Join(filepath.Dir(theirs), "agents", "openai.yaml")
	if _, err := os.Stat(yaml); !errors.Is(err, os.ErrNotExist) || !strings.Contains(out, yaml+" not written, "+theirs+" in its folder is not Jalon's") {
		t.Errorf("openai.yaml: %v\n%s", err, out)
	}

	// Uninstall removes Jalon's skills, their folders, and only them.
	out = runInitOK(t, "-settings", hooks, "-client", "codex", "-uninstall")
	if b, _ := os.ReadFile(theirs); string(b) != foreignSkill || !strings.Contains(out, theirs+" not written by Jalon, left as is") {
		t.Errorf("their skill = %q\n%s", b, out)
	}
	entries, _ := os.ReadDir(filepath.Dir(filepath.Dir(theirs)))
	if len(entries) != 1 || entries[0].Name() != "jalon-help" {
		t.Errorf("left in the skills folder: %v", entries)
	}
}
