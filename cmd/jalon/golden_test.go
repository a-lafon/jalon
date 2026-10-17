package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden files hold the exact bytes the Claude Code hook and status line
// produced before the client adapters were introduced. Run with -update only
// when the output is meant to change.
var update = flag.Bool("update", false, "rewrite the golden files")

const goldenDir = "testdata/golden"

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *update {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs\n got: %q\nwant: %q", name, got, want)
	}
}

func TestGoldenStatusline(t *testing.T) {
	off := "context:\n  enabled: false\n  advise_tokens: 200000\n  advise_step_tokens: 150000\n  stale_after_turns: 5\n"
	tests := map[string]struct{ cfg, stdin string }{
		"below":    {"", statusInput("s1", 120_000)},
		"above":    {"", statusInput("s1", 230_000)},
		"at":       {"", statusInput("s1", 200_000)},
		"rounded":  {"", statusInput("s1", 45_600)},
		"disabled": {off, statusInput("s1", 230_000)},
		"null":     {"", statusInput("s1", nil)},
		"missing":  {"", `{"session_id":"s1"}`},
		"bad json": {"", `not json`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			useHome(t)
			checkGolden(t, "statusline-"+strings.ReplaceAll(name, " ", "-")+".golden", runStatus(t, configWith(t, tt.cfg), tt.stdin))
		})
	}
}

func TestGoldenHook(t *testing.T) {
	// Each case is a transcript tail and a run of prompts; the output of the last
	// prompt and the state file after it are compared. The repository path is
	// the only part that changes between runs, so it is replaced.
	tests := map[string]struct {
		lines  []string
		cwd    string // "repo", "repo/sub", "plain" (not a git repository) or ""
		prompt int    // how many prompts are submitted
	}{
		"crossing":        {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "repo", 1},
		"crossing-subdir": {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "repo/sub", 1},
		"crossing-plain":  {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "plain", 1},
		// The last request wrote to the one-hour cache: the advice says how long it lasts.
		"crossing-cache":    {[]string{typedPrompt("p0"), assistantLine(50_000), `{"type":"assistant","isSidechain":false,"message":{"usage":{"input_tokens":2,"cache_creation_input_tokens":2000,"cache_read_input_tokens":210000,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":2000}}}}` + "\n"}, "repo", 1},
		"crossing-no-cwd":   {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "", 1},
		"repeat":            {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "repo", 2},
		"below":             {[]string{typedPrompt("p0"), assistantLine(120_000)}, "repo", 1},
		"after-compaction":  {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(300_000), `{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":15000}}` + "\n"}, "repo", 1},
		"sidechain-ignored": {[]string{typedPrompt("p0"), assistantLine(startSize), assistantLine(212_000), `{"type":"assistant","isSidechain":true,"message":{"usage":{"input_tokens":5}}}` + "\n"}, "repo", 1},
		// The conversation starts at 212K: nothing a reset can remove, so no advice.
		"heavy-start":       {[]string{typedPrompt("p0"), assistantLine(212_000)}, "repo", 1},
		"heavy-start-grown": {[]string{typedPrompt("p0"), assistantLine(230_000), assistantLine(385_000)}, "repo", 1},
		"no-usage":          {[]string{`{"type":"user"}` + "\n"}, "repo", 1},
		// Below the threshold, the user asks for a handoff.
		"handoff-asked": {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(120_000)}, "repo", 1},
		// The agent cd'ed out of the project: the folder stays in the project.
		"crossing-moved-cwd": {[]string{typedPrompt("p0"), assistantLine(50_000), assistantLine(212_000)}, "plain", 1},
	}
	// CLAUDE_PROJECT_DIR for the cases that set it; useHome empties it for the others.
	projects := map[string]string{"crossing-moved-cwd": "repo/sub"}
	// The prompt of the cases that type a command, with Jalon's skills installed;
	// "hi" for the others.
	commands := map[string]string{"handoff-asked": "/jalon-handoff the parser work"}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := useHome(t)
			cfg := configWith(t, thresholdsConfig)
			appendTranscript(t, "s1", tt.lines...)
			repo := gitRepo(t)
			plain := t.TempDir()
			if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			cwd := map[string]string{"repo": repo, "repo/sub": filepath.Join(repo, "sub"), "plain": plain, "": ""}[tt.cwd]
			if p := projects[name]; p != "" {
				t.Setenv("CLAUDE_PROJECT_DIR", map[string]string{"repo/sub": filepath.Join(repo, "sub")}[p])
			}
			text, ok := commands[name]
			if ok {
				writeJalonSkills(t)
			} else {
				text = "hi"
			}
			var out string
			for range tt.prompt {
				out = runPrompt(t, cfg, hookInputText("s1", cwd, text))
			}
			norm := func(s string) string {
				s = strings.ReplaceAll(s, repo, "<repo>")
				return strings.ReplaceAll(s, plain, "<plain>")
			}
			if name == "crossing-moved-cwd" {
				if _, err := os.Stat(filepath.Join(plain, ".jalon")); err == nil {
					t.Error(".jalon was created in the directory the agent moved to")
				}
				if _, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs")); err != nil {
					t.Errorf("no handoff folder in the project: %v", err)
				}
			}
			checkGolden(t, "hook-"+name+".golden", norm(out))
			state, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
			checkGolden(t, "hook-"+name+".state", string(state))
		})
	}
}

func TestGoldenStopHook(t *testing.T) {
	// The prompt hook sees prompt p1, then the stop hook ends its turn. The
	// output of the stop hook and the state file after it are compared.
	tests := map[string]struct {
		before, after []string // the transcript at the prompt, and what the turn adds
		last          string   // the turn's last assistant message
	}{
		// Advised at the prompt, the reply lacks the section.
		"reminder": {[]string{typedPrompt("p0"), assistantLine(startSize), assistantLine(274_000)}, []string{assistantLine(280_000)}, "Le fichier de reprise est écrit."},
		// Below the threshold at the prompt, past it at the end of the turn.
		"crossing": {[]string{typedPrompt("p0"), assistantLine(startSize), assistantLine(150_000)}, []string{assistantLine(296_000)}, "Done."},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := useHome(t)
			cfg := configWith(t, thresholdsConfig)
			repo := gitRepo(t)
			appendTranscript(t, "s1", tt.before...)
			runPrompt(t, cfg, promptInput("s1", "p1", repo))
			appendTranscript(t, "s1", append([]string{typedPrompt("p1")}, tt.after...)...)
			out := runStop(t, cfg, stopInput("s1", "p1", repo, tt.last, false))
			checkGolden(t, "stop-"+name+".golden", strings.ReplaceAll(out, repo, "<repo>"))
			state, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
			checkGolden(t, "stop-"+name+".state", string(state))
		})
	}
}
