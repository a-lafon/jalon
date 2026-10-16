package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/client/claudecode"
)

// TestMain lifts the time limits of the hook and the status line: a test's
// output must not depend on how fast the runner is.
func TestMain(m *testing.M) {
	hookTimeout, statuslineTimeout = time.Hour, time.Hour
	os.Exit(m.Run())
}

// useHome points ~ at a temporary folder, so the state files never touch the real one.
func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A shell or a hook that exported it would move every handoff folder.
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	// Thread IDs are drawn from a fixed seed, so outputs compare byte for byte.
	saved := threadRand
	threadRand = mathrand.NewChaCha8([32]byte{})
	t.Cleanup(func() { threadRand = saved })
	return filepath.Join(home, ".jalon", "sessions")
}

// configWith writes a config file holding the given YAML.
func configWith(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// runStatus runs the status line without color, so outputs compare as text.
func runStatus(t *testing.T, configPath, stdin string) string {
	t.Helper()
	t.Setenv("NO_COLOR", "1")
	var out bytes.Buffer
	if err := runStatusline(context.Background(), []string{"-config", configPath}, strings.NewReader(stdin), &out); err != nil {
		t.Fatalf("statusline: %v", err)
	}
	return out.String()
}

func statusInput(session string, tokens any) string {
	b, _ := json.Marshal(map[string]any{"session_id": session, "context_window": map[string]any{"total_input_tokens": tokens}})
	return string(b)
}

func TestStatuslinePrintsTheContextSize(t *testing.T) {
	tests := map[string]struct {
		tokens int
		want   string
	}{
		"empty":               {40_000, "jalon ▱▱▱ 40K\n"},
		"one band":            {95_000, "jalon ▰▱▱ 95K\n"},
		"two bands":           {140_000, "jalon ▰▰▱ 140K\n"},
		"above the threshold": {230_000, "jalon ▰▰▰ 230K · reset advised\n"},
		"at the threshold":    {200_000, "jalon ▰▰▰ 200K · reset advised\n"},
		"rounds to thousands": {45_600, "jalon ▱▱▱ 46K\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			home := useHome(t)
			if got := runStatus(t, configWith(t, ""), statusInput("s1", tt.tokens)); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
			// It is a display only: it writes nothing.
			if _, err := os.Stat(filepath.Join(filepath.Dir(home), "sessions")); err == nil {
				t.Error("the status line wrote a state folder")
			}
		})
	}
}

func TestStatuslineCountsTheGrowthAboveTheFloorOfTheSession(t *testing.T) {
	tests := map[string]struct {
		session string
		floor   int // the floor in the session's state file, -1 for no state file
		tokens  int
		want    string
	}{
		"grown above the floor":      {"s1", 66_000, 230_000, "jalon ▰▰▰ 230K · reset advised\n"},
		"heavy start":                {"s1", 212_000, 230_000, "jalon ▱▱▱ 230K\n"},
		"heavy start, grown":         {"s1", 212_000, 362_000, "jalon ▰▰▰ 362K · reset advised\n"},
		"heavy start, on the way":    {"s1", 212_000, 330_000, "jalon ▰▰▱ 330K\n"},
		"state file without a floor": {"s1", 0, 230_000, "jalon ▰▰▰ 230K · reset advised\n"},
		"no state file":              {"s1", -1, 230_000, "jalon ▰▰▰ 230K · reset advised\n"},
		"no session ID":              {"", 212_000, 230_000, "jalon ▰▰▰ 230K · reset advised\n"},
		"unsafe session ID":          {"../x", 212_000, 230_000, "jalon ▰▰▰ 230K · reset advised\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := useHome(t)
			// A file the unsafe ID would reach, and the state of the safe one.
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "x.json"), []byte(`{"floor":212000}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.floor >= 0 {
				if err := advisor.SetFloor(dir, "s1", tt.floor); err != nil {
					t.Fatal(err)
				}
			}
			cfg := configWith(t, thresholdsConfig)
			before := snapshot(t, filepath.Dir(dir))
			if got := runStatus(t, cfg, statusInput(tt.session, tt.tokens)); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
			if after := snapshot(t, filepath.Dir(dir)); after != before {
				t.Errorf("the status line wrote a file:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

// snapshot lists the files under root with their sizes and modification times.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s %d %d\n", path, fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestStatuslineSaysAdvisedOnlyWhenTheAdvisorIsEnabled(t *testing.T) {
	useHome(t)
	off := configWith(t, "context:\n  enabled: false\n  advise_tokens: 200000\n  advise_step_tokens: 150000\n  stale_after_turns: 5\n")
	if got := runStatus(t, off, statusInput("s1", 230_000)); got != "jalon 230K\n" {
		t.Errorf("output = %q, want the size without pole or advice", got)
	}
}

func TestPoleBandsFillAtEachThirdOfTheThreshold(t *testing.T) {
	tests := map[string]struct{ growth, want int }{
		"shrunk below the floor": {-5_000, 0},
		"no growth":              {0, 0},
		"just below a third":     {66_666, 0},
		"a third":                {66_667, 1},
		"just below two thirds":  {133_333, 1},
		"two thirds":             {133_334, 2},
		"just below the advice":  {199_999, 2},
		"the advice":             {200_000, 3},
		"far above":              {900_000, 3},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := poleBands(tt.growth, 200_000); got != tt.want {
				t.Errorf("poleBands(%d, 200000) = %d, want %d", tt.growth, got, tt.want)
			}
		})
	}
}

func TestStatuslineColorsTheFilledBands(t *testing.T) {
	useHome(t)
	cfg := configWith(t, "")
	tests := map[string]struct {
		noColor string
		tokens  int
		want    string
	}{
		"one band":     {"", 95_000, "jalon \x1b[38;2;232;56;27m▰\x1b[0m▱▱ 95K\n"},
		"full":         {"", 230_000, "jalon \x1b[38;2;232;56;27m▰▰▰\x1b[0m 230K · reset advised\n"},
		"empty":        {"", 40_000, "jalon ▱▱▱ 40K\n"},
		"NO_COLOR set": {"1", 95_000, "jalon ▰▱▱ 95K\n"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			var out bytes.Buffer
			if err := runStatusline(context.Background(), []string{"-config", cfg}, strings.NewReader(statusInput("s1", tt.tokens)), &out); err != nil {
				t.Fatalf("statusline: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStatuslineIsSilentOnAnyFailure(t *testing.T) {
	useHome(t)
	cfg := configWith(t, "")
	tests := map[string]struct{ cfg, stdin string }{
		"null count (after /compact)": {cfg, statusInput("s1", nil)},
		"count missing":               {cfg, `{"session_id":"s1","context_window":{}}`},
		"no context window":           {cfg, `{"session_id":"s1"}`},
		"zero count":                  {cfg, statusInput("s1", 0)},
		"input is not JSON":           {cfg, `not json`},
		"empty input":                 {cfg, ``},
		"config does not parse":       {configWith(t, "listen: [\n"), statusInput("s1", 5000)},
		"config has an unknown field": {configWith(t, "colour: blue\n"), statusInput("s1", 5000)},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := runStatus(t, tt.cfg, tt.stdin); got != "" {
				t.Errorf("output = %q, want nothing", got)
			}
		})
	}
}

type hookOutput struct {
	SystemMessage      string `json:"systemMessage"`
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

var hookNow = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func runPrompt(t *testing.T, configPath, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPromptHook(context.Background(), []string{"-config", configPath}, strings.NewReader(stdin), &out, hookNow); err != nil {
		t.Fatalf("hook: %v", err)
	}
	return out.String()
}

// transcriptFile is where hookInput says the session's transcript is.
func transcriptFile(session string) string {
	return filepath.Join(os.Getenv("HOME"), "transcripts", session+".jsonl")
}

// assistantLine is a main-conversation assistant entry whose usage sums to size.
func assistantLine(size int) string {
	return fmt.Sprintf(`{"type":"assistant","isSidechain":false,"message":{"role":"assistant","usage":{"input_tokens":2,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":9}}}`+"\n", size/4, size-size/4-2)
}

// typedPrompt is the transcript entry of a prompt a person typed: Claude Code
// writes it after the prompt hook ran, with origin kind "human". A claude -p
// prompt has no origin.
func typedPrompt(promptID string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":false,"promptId":%q,"origin":{"kind":"human"},"message":{"role":"user","content":"hi"}}`+"\n", promptID)
}

// programPrompt is the transcript entry of a prompt claude -p sent.
func programPrompt(promptID string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":false,"promptId":%q,"message":{"role":"user","content":"run the task"}}`+"\n", promptID)
}

// appendTranscript appends lines to the session's transcript.
func appendTranscript(t *testing.T, session string, lines ...string) {
	t.Helper()
	path := transcriptFile(session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
}

// startSize is the size of the first request of the transcripts the tests build:
// the floor, which the first advice counts its growth from.
const startSize = 50_000

// thresholdsConfig sets the thresholds the sizes in the hook and status line
// tests are chosen against, the ones the client-hooks scenarios state, so they
// do not move with the default.
const thresholdsConfig = "context:\n  advise_tokens: 150000\n  advise_step_tokens: 150000\n"

// hookState gives a session a transcript of a first request of startSize and a
// last one of the given size, under a temporary home, and returns the state
// folder and a config holding thresholdsConfig.
func hookState(t *testing.T, session string, size int) (dir, configPath string) {
	t.Helper()
	dir = useHome(t)
	appendTranscript(t, session, typedPrompt("p0"), assistantLine(startSize), assistantLine(size))
	return dir, configWith(t, thresholdsConfig)
}

func hookInput(session, cwd string) string {
	return hookInputText(session, cwd, "hi")
}

func hookInputText(session, cwd, prompt string) string {
	b, _ := json.Marshal(map[string]string{"session_id": session, "cwd": cwd, "transcript_path": transcriptFile(session), "hook_event_name": "UserPromptSubmit", "prompt": prompt})
	return string(b)
}

// gitRepo returns a temporary directory that is a git repository root.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHookResumesWithTheSkillWhenInstalled(t *testing.T) {
	_, cfg := hookState(t, "s1", 212_000)
	repo := gitRepo(t)
	skill := filepath.Join(os.Getenv("HOME"), ".claude", "skills", "jalon", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, claudecode.Adapter{}.Skill(), 0o644); err != nil {
		t.Fatal(err)
	}
	var out hookOutput
	if err := json.Unmarshal([]byte(runPrompt(t, cfg, hookInput("s1", repo))), &out); err != nil {
		t.Fatal(err)
	}
	ctxt := out.HookSpecificOutput.AdditionalContext
	if !regexp.MustCompile(`then /jalon resume `+regexp.QuoteMeta(repo)+`/\.jalon/handoffs/2026-10-12/0900-[0-9a-z]{8}-<short-slug>\.md \(the full path\), with nothing after it\.`).MatchString(ctxt) ||
		strings.Contains(ctxt, "then @") || strings.Contains(ctxt, "{") {
		t.Errorf("additionalContext = %q", ctxt)
	}
}

func TestHookAdvisesWhenTheThresholdIsCrossed(t *testing.T) {
	dir, cfg := hookState(t, "s1", 212_000)
	repo := gitRepo(t)
	sub := filepath.Join(repo, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	raw := runPrompt(t, cfg, hookInput("s1", sub))
	var out hookOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	if out.SystemMessage != "Jalon: context at 212K tokens, a reset proposal follows the answer." {
		t.Errorf("systemMessage = %q", out.SystemMessage)
	}
	ctxt := out.HookSpecificOutput.AdditionalContext
	if out.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
		!strings.Contains(ctxt, "holds about 212K tokens of context") ||
		!regexp.MustCompile(`Write `+regexp.QuoteMeta(repo)+`/\.jalon/handoffs/2026-10-12/0900-[0-9a-z]{8}-<short-slug>\.md`).MatchString(ctxt) ||
		!regexp.MustCompile(`then @`+regexp.QuoteMeta(repo)+`/\.jalon/handoffs/2026-10-12/0900-[0-9a-z]{8}-<short-slug>\.md`).MatchString(ctxt) ||
		!strings.Contains(ctxt, `Its first line is "Session: s1".`) ||
		!strings.Contains(ctxt, "/compact <what to keep;") ||
		strings.Contains(ctxt, "{") {
		t.Errorf("additionalContext = %q", ctxt)
	}

	// The handoff folder is at the git top level, ignored by itself; the day
	// folder is left to the model's write.
	if fi, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs")); err != nil || !fi.IsDir() {
		t.Errorf("handoff folder at the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs", "2026-10-12")); err == nil {
		t.Error("the day folder was created before any handoff")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, ".jalon", ".gitignore")); string(b) != "*\n" {
		t.Errorf(".gitignore = %q, want *", b)
	}
	if _, err := os.Stat(filepath.Join(sub, ".jalon")); err == nil {
		t.Error("a .jalon folder was created below the repository root")
	}

	// Delivered: the step now counts from here, and the next prompt is silent.
	if got, _ := advisor.Load(dir, "s1"); got.LastAdvised != 212_000 || got.Size != 212_000 {
		t.Errorf("last advised = %d, want 212000", got.LastAdvised)
	}
	if again := runPrompt(t, cfg, hookInput("s1", repo)); again != "" {
		t.Errorf("second prompt output = %q, want nothing", again)
	}
}

func TestHookStaysSilentWithinTheStepAndAdvisesAfterIt(t *testing.T) {
	_, cfg := hookState(t, "s1", 210_000)
	repo := gitRepo(t)
	if runPrompt(t, cfg, hookInput("s1", repo)) == "" {
		t.Fatal("the first crossing must advise")
	}
	appendTranscript(t, "s1", assistantLine(260_000))
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got != "" {
		t.Errorf("within the step: output = %q, want nothing", got)
	}
	appendTranscript(t, "s1", assistantLine(365_000))
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got == "" {
		t.Error("after the step the hook must advise again")
	}
}

func TestHookStepResetsAfterACompaction(t *testing.T) {
	dir, cfg := hookState(t, "s1", 210_000)
	repo := gitRepo(t)
	runPrompt(t, cfg, hookInput("s1", repo))
	// /compact: a boundary with the size it left, then the context grows again.
	appendTranscript(t, "s1", `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":210000,"postTokens":22000}}`+"\n")
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got != "" {
		t.Errorf("after the compaction: output = %q, want nothing", got)
	}
	if st, _ := advisor.Load(dir, "s1"); st.Size != 22_000 || st.LastAdvised != 0 {
		t.Errorf("state after compaction = %+v, want size 22000 and last advised 0", st)
	}
	appendTranscript(t, "s1", assistantLine(205_000))
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got == "" {
		t.Error("the step was not reset: no advice at 205K after the compaction")
	}
}

func TestHookBelowTheThresholdDoesNothing(t *testing.T) {
	_, cfg := hookState(t, "s1", 120_000)
	repo := gitRepo(t)
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon")); err == nil {
		t.Error("no .jalon folder must be created without advice")
	}
}

func TestHookOutsideAGitRepositoryUsesCwd(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	dir := t.TempDir()
	if runPrompt(t, cfg, hookInput("s1", dir)) == "" {
		t.Fatal("expected advice")
	}
	if _, err := os.Stat(filepath.Join(dir, ".jalon", "handoffs")); err != nil {
		t.Errorf("handoffs folder in cwd: %v", err)
	}
}

func TestHookGitWorktreeFileMarksTheRoot(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "a")
	os.Mkdir(sub, 0o755)
	runPrompt(t, cfg, hookInput("s1", sub))
	if _, err := os.Stat(filepath.Join(root, ".jalon", "handoffs")); err != nil {
		t.Errorf("handoffs folder at the worktree root: %v", err)
	}
}

func TestHookNeverModifiesAnExistingGitignore(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	repo := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".jalon"), 0o755); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(repo, ".jalon", ".gitignore")
	if err := os.WriteFile(own, []byte("keep-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if runPrompt(t, cfg, hookInput("s1", repo)) == "" {
		t.Fatal("expected advice")
	}
	if b, _ := os.ReadFile(own); string(b) != "keep-me\n" {
		t.Errorf(".gitignore = %q, want it unchanged", b)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs")); err != nil {
		t.Errorf("handoffs folder: %v", err)
	}
}

func TestHookStillAdvisesWhenTheFolderCannotBeCreated(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A cwd below a regular file: nothing can be created there.
	if got := runPrompt(t, cfg, hookInput("s1", filepath.Join(blocker, "sub"))); !strings.Contains(got, "additionalContext") {
		t.Errorf("output = %q, want the advice anyway", got)
	}
}

// A client may not show the hook's message, so the model's recommendation must
// say where it comes from.
func TestHookInstructionStartsTheRecommendationWithJalon(t *testing.T) {
	_, cfg := hookState(t, "s1", 212_000)
	var out hookOutput
	if err := json.Unmarshal([]byte(runPrompt(t, cfg, hookInput("s1", gitRepo(t)))), &out); err != nil {
		t.Fatal(err)
	}
	if ctxt := out.HookSpecificOutput.AdditionalContext; !strings.Contains(ctxt, "end your reply with a short section written as a Markdown blockquote") || !strings.Contains(ctxt, "first words are\n**Jalon** —") || !strings.Contains(ctxt, "do not repeat it in later replies") {
		t.Errorf("the instruction does not ask for a blockquote section starting with **Jalon** —, given once:\n%s", ctxt)
	}
}

func TestHookCreatesThePrivateFolders(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	repo := gitRepo(t)
	if runPrompt(t, cfg, hookInput("s1", repo)) == "" {
		t.Fatal("expected advice")
	}
	for _, d := range []string{".jalon", ".jalon/handoffs"} {
		fi, err := os.Stat(filepath.Join(repo, d))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want no access for group and others", d, fi.Mode().Perm())
		}
	}
}

func TestHookLeavesExistingFolderModes(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	repo := gitRepo(t)
	existing := filepath.Join(repo, ".jalon")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if runPrompt(t, cfg, hookInput("s1", repo)) == "" {
		t.Fatal("expected advice")
	}
	if fi, _ := os.Stat(existing); fi.Mode().Perm() != 0o755 {
		t.Errorf(".jalon is %v, want it left at 0755", fi.Mode().Perm())
	}
}

func TestHookCreatesNothingThroughASymlink(t *testing.T) {
	tests := map[string]func(repo, outside string) error{
		".jalon":                func(repo, outside string) error { return os.Symlink(outside, filepath.Join(repo, ".jalon")) },
		".jalon/handoffs":       func(repo, outside string) error { return linkBelowJalon(repo, outside, "handoffs") },
		".jalon/handoffs/<day>": func(repo, outside string) error { return linkBelowJalon(repo, outside, "handoffs", "2026-10-12") },
		".jalon is a file": func(repo, _ string) error {
			return os.WriteFile(filepath.Join(repo, ".jalon"), []byte("x"), 0o644)
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			_, cfg := hookState(t, "s1", 300_000)
			repo, outside := gitRepo(t), t.TempDir()
			if err := setup(repo, outside); err != nil {
				t.Fatal(err)
			}
			if got := runPrompt(t, cfg, hookInput("s1", repo)); !strings.Contains(got, "additionalContext") {
				t.Errorf("output = %q, want the advice anyway", got)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("%d entries were created outside the project", len(entries))
			}
		})
	}
}

// linkBelowJalon makes .jalon/<dirs...> a symlink to outside, creating the
// real folders above it.
func linkBelowJalon(repo, outside string, dirs ...string) error {
	parent := filepath.Join(append([]string{repo, ".jalon"}, dirs[:len(dirs)-1]...)...)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	return os.Symlink(outside, filepath.Join(parent, dirs[len(dirs)-1]))
}

func TestHookNeverBlocksThePrompt(t *testing.T) {
	_, cfg := hookState(t, "s1", 300_000)
	tests := map[string]struct{ cfg, stdin string }{
		"transcript is missing":      {cfg, hookInput("other", t.TempDir())},
		"no transcript in the input": {cfg, `{"session_id":"s1","cwd":"/"}`},
		"unsafe session":             {cfg, hookInput("../s1", t.TempDir())},
		"input is not JSON":          {cfg, "nope"},
		"no session":                 {cfg, `{"cwd":"/"}`},
		"config does not parse":      {configWith(t, "listen: [\n"), hookInput("s1", t.TempDir())},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := runPrompt(t, tt.cfg, tt.stdin); got != "" {
				t.Errorf("output = %q, want nothing", got)
			}
		})
	}
}

func TestHookIsSilentWhenTheAdvisorIsDisabled(t *testing.T) {
	dir, _ := hookState(t, "s1", 300_000)
	off := configWith(t, "context:\n  enabled: false\n  advise_tokens: 200000\n  advise_step_tokens: 150000\n  stale_after_turns: 5\n")
	repo := gitRepo(t)
	if got := runPrompt(t, off, hookInput("s1", repo)); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
	if st, _ := advisor.Load(dir, "s1"); st.LastAdvised != 0 {
		t.Errorf("last advised = %d, nothing was advised", st.LastAdvised)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon")); err == nil {
		t.Error("no .jalon folder must be created without advice")
	}
}

func TestHookDeletesOldStateFiles(t *testing.T) {
	dir, cfg := hookState(t, "s1", 100_000)
	advisor.Record(dir, "old", 100_000, hookNow.Add(-8*24*time.Hour), "", false)
	advisor.Record(dir, "recent", 100_000, hookNow.Add(-6*24*time.Hour), "", false)
	runPrompt(t, cfg, hookInput("s1", t.TempDir()))
	if _, ok := advisor.Load(dir, "old"); ok {
		t.Error("a state file older than 7 days was kept")
	}
	if _, ok := advisor.Load(dir, "recent"); !ok {
		t.Error("a recent state file was deleted")
	}
}

func TestHookRejectsUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{{"frobnicate"}, {}} {
		err := runHook(context.Background(), args, strings.NewReader(""), &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "prompt") || !strings.Contains(err.Error(), "stop") {
			t.Errorf("%v: err = %v, want a usage error naming prompt and stop", args, err)
		}
	}
	if !strings.Contains(usage, "jalon hook stop [-client name] [-config path]") {
		t.Error("the usage does not list hook stop with its flags")
	}
}

func hookInputWithPrompt(session, cwd, prompt string) string {
	b, _ := json.Marshal(map[string]string{"session_id": session, "cwd": cwd, "transcript_path": transcriptFile(session), "hook_event_name": "UserPromptSubmit", "prompt": prompt})
	return string(b)
}

func TestHookIgnoresPromptsNotWrittenByAPerson(t *testing.T) {
	dir, cfg := hookState(t, "s1", 275_000)
	repo := gitRepo(t)
	for _, prompt := range []string{
		"Another Claude session sent a message:\n<cross-session-message from=\"x\">hi</cross-session-message>",
		"[Cross-session idle notice] jalon-a1 is idle",
		"  <task-notification>\n<task-id>b1</task-id></task-notification>",
	} {
		if got := runPrompt(t, cfg, hookInputWithPrompt("s1", repo, prompt)); got != "" {
			t.Errorf("output for %.30q = %q, want nothing", prompt, got)
		}
	}
	// Nothing is written: no size, no advice, no handoff folder, no state folder.
	if _, err := os.Stat(dir); err == nil {
		t.Error("a state folder was created")
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon")); err == nil {
		t.Error("a handoff folder was created")
	}

	// The user's next message gets the advice the notices did not spend.
	got := runPrompt(t, cfg, hookInputWithPrompt("s1", repo, "ok, on continue ?"))
	var out hookOutput
	if err := json.Unmarshal([]byte(got), &out); err != nil || !strings.Contains(out.SystemMessage, "275K") {
		t.Errorf("output after the notices = %q (%v)", got, err)
	}
	if st, _ := advisor.Load(dir, "s1"); st.LastAdvised != 275_000 {
		t.Errorf("last advised = %d, want 275000", st.LastAdvised)
	}
}

func TestHookNoticeDoesNotSweepOrRecordAfterAnAdvice(t *testing.T) {
	dir, cfg := hookState(t, "s1", 275_000)
	repo := gitRepo(t)
	runPrompt(t, cfg, hookInputWithPrompt("s1", repo, "first"))
	before, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	appendTranscript(t, "s1", assistantLine(120_000))
	runPrompt(t, cfg, hookInputWithPrompt("s1", repo, "[Cross-session idle notice] x"))
	after, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	if string(before) != string(after) {
		t.Errorf("a notice changed the state:\n%s\n%s", before, after)
	}
}

func TestUnknownClientIsSilentAndSaysWhy(t *testing.T) {
	_, cfg := hookState(t, "s1", 275_000)
	var errBuf bytes.Buffer
	old := stderr
	stderr = &errBuf
	defer func() { stderr = old }()

	var out bytes.Buffer
	args := []string{"-client", "nope", "-config", cfg}
	if err := runPromptHook(context.Background(), args, strings.NewReader(hookInput("s1", t.TempDir())), &out, hookNow); err != nil || out.Len() != 0 {
		t.Errorf("hook: err=%v output=%q, want silence", err, out.String())
	}
	if !strings.Contains(errBuf.String(), `unknown client "nope"`) || !strings.Contains(errBuf.String(), "claude-code") {
		t.Errorf("stderr = %q", errBuf.String())
	}
	if _, ok := advisor.Load(filepath.Join(os.Getenv("HOME"), ".jalon", "sessions"), "s1"); ok {
		t.Error("an unknown client wrote a state file")
	}

	errBuf.Reset()
	if err := runStatusline(context.Background(), []string{"-client", "nope"}, strings.NewReader(statusInput("s1", 230_000)), &out); err != nil || out.Len() != 0 {
		t.Errorf("statusline: err=%v output=%q, want silence", err, out.String())
	}
	if !strings.Contains(errBuf.String(), `unknown client "nope"`) {
		t.Errorf("stderr = %q", errBuf.String())
	}
}

func TestClientFlagNamesTheDefaultClient(t *testing.T) {
	_, cfg := hookState(t, "s1", 212_000)
	var out bytes.Buffer
	args := []string{"--client", "claude-code", "-config", cfg}
	if err := runPromptHook(context.Background(), args, strings.NewReader(hookInput("s1", t.TempDir())), &out, hookNow); err != nil || !strings.Contains(out.String(), "212K") {
		t.Errorf("err=%v output=%q", err, out.String())
	}
	out.Reset()
	if got := runStatus(t, configWith(t, ""), statusInput("s1", 120_000)); got != "jalon ▰▱▱ 120K\n" {
		t.Errorf("default client status line = %q", got)
	}
}

func TestCacheLifetime(t *testing.T) {
	for d, want := range map[time.Duration]string{5 * time.Minute: "5 minutes", time.Hour: "an hour", 2 * time.Hour: "2 hours"} {
		if got := cacheLifetime(d); got != want {
			t.Errorf("cacheLifetime(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestNewThreadIDDropsBiasedBytes(t *testing.T) {
	src := append(bytes.Repeat([]byte{252, 255}, 8), []byte{0, 9, 10, 35, 36, 71, 251, 1, 2, 3, 4, 5, 6, 7, 8, 9}...)
	got, err := newThreadID(bytes.NewReader(src))
	if err != nil || got != "09az0zz1" {
		t.Errorf("id = %q, %v, want 09az0zz1", got, err)
	}
	if _, err := newThreadID(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("a short source gave no error")
	}
}

// adviseAt submits one prompt for session, whose transcript holds size tokens,
// at time now, and returns the instruction given to the model ("" for none).
func adviseAt(t *testing.T, cfg, session, cwd, text string, size int, now time.Time) string {
	t.Helper()
	if _, err := os.Stat(transcriptFile(session)); err != nil {
		appendTranscript(t, session, typedPrompt("p0"), assistantLine(startSize))
	}
	appendTranscript(t, session, assistantLine(size))
	var out bytes.Buffer
	if err := runPromptHook(context.Background(), []string{"-config", cfg}, strings.NewReader(hookInputText(session, cwd, text)), &out, now); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return ""
	}
	var o hookOutput
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatal(err)
	}
	return o.HookSpecificOutput.AdditionalContext
}

// writeHandoff creates a handoff file in the repository's handoff folder.
func writeHandoff(t *testing.T, repo, name string) string {
	t.Helper()
	path := filepath.Join(repo, ".jalon", "handoffs", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# Handoff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHookThreadsHandoffs(t *testing.T) {
	const parent = "2026-10-09/1358-r4sefxw4-advisor.md"
	nameOf := regexp.MustCompile(`handoffs/(\d{4}-\d{2}-\d{2}/\d{4})-([0-9a-z]{8})-<short-slug>\.md`)
	thread := func(t *testing.T, ctxt string) (stamp, id string) {
		t.Helper()
		m := nameOf.FindStringSubmatch(ctxt)
		if m == nil {
			t.Fatalf("no handoff name in %q", ctxt)
		}
		return m[1], m[2]
	}

	t.Run("first handoff of a thread", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		ctxt := adviseAt(t, cfg, "s1", repo, "hi", 212_000, hookNow)
		stamp, id := thread(t, ctxt)
		if stamp != "2026-10-12/0900" || strings.Contains(ctxt, "Follows:") || !strings.Contains(ctxt, `Its first line is "Session: s1".`) {
			t.Errorf("instruction = %q", ctxt)
		}
		if fi, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs")); err != nil || !fi.IsDir() {
			t.Errorf("handoff folder: %v", err)
		}
		if st, _ := advisor.Load(dir, "s1"); st.Thread != id || st.Proposed != "2026-10-12/0900-"+id || st.Parent != "" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("resume after a clear", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		ctxt := adviseAt(t, cfg, "s2", repo, "@"+path+" resume from it", 212_000, hookNow)
		if _, id := thread(t, ctxt); id != "r4sefxw4" ||
			!strings.Contains(ctxt, `Its first lines are "Follows: `+parent+`" and "Session: s2".`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("resume with the skill", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		ctxt := adviseAt(t, cfg, "s2", repo, "/jalon resume "+path, 212_000, hookNow)
		if _, id := thread(t, ctxt); id != "r4sefxw4" || !strings.Contains(ctxt, `"Follows: `+parent+`"`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("resume below the threshold, advice later", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		if got := adviseAt(t, cfg, "s2", repo, "@"+path+" resume", 20_000, hookNow); got != "" {
			t.Fatalf("advice below the threshold: %q", got)
		}
		ctxt := adviseAt(t, cfg, "s2", repo, "go on", 212_000, hookNow)
		if _, id := thread(t, ctxt); id != "r4sefxw4" || !strings.Contains(ctxt, "Follows: "+parent) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("resume from a flat handoff with a thread", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		const flat = "2026-10-10-0847-4c7vifcm-parser-refactor.md"
		path := writeHandoff(t, repo, flat)
		ctxt := adviseAt(t, cfg, "s2", repo, "@"+path+" reprends", 212_000, hookNow)
		if stamp, id := thread(t, ctxt); stamp != "2026-10-12/0900" || id != "4c7vifcm" || !strings.Contains(ctxt, `"Follows: `+flat+`"`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("resume from a handoff without a thread", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		// A handoff without a thread starts a new one at the next advice.
		const old = "2026-10-09-notes.md"
		path := writeHandoff(t, repo, old)
		adviseAt(t, cfg, "s2", repo, "@"+path+" reprends", 20_000, hookNow)
		if st, _ := advisor.Load(dir, "s2"); st.Thread != "" || st.Parent != old {
			t.Errorf("state = %+v, want the parent without a thread", st)
		}
		ctxt := adviseAt(t, cfg, "s2", repo, "go on", 212_000, hookNow)
		if _, id := thread(t, ctxt); id == "" || id == "r4sefxw4" || !strings.Contains(ctxt, `"Follows: `+old+`"`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("handoff path quoted later in a session", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		other := writeHandoff(t, repo, "2026-10-10/0857-g20vobbs-positioning.md")
		adviseAt(t, cfg, "s2", repo, "@"+path+" resume", 20_000, hookNow)
		adviseAt(t, cfg, "s2", repo, "compare with @"+other, 25_000, hookNow)
		if st, _ := advisor.Load(dir, "s2"); st.Parent != parent || st.Thread != "r4sefxw4" {
			t.Errorf("state = %+v, want the first resume kept", st)
		}
	})

	t.Run("later path in a session that did not start with one", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		adviseAt(t, cfg, "s2", repo, "hi", 20_000, hookNow)
		adviseAt(t, cfg, "s2", repo, "@"+writeHandoff(t, repo, parent), 25_000, hookNow)
		if st, _ := advisor.Load(dir, "s2"); st.Parent != "" || st.Thread != "" {
			t.Errorf("state = %+v, want no handoff", st)
		}
	})

	t.Run("message from another session before the first prompt", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		peer := "Another Claude session sent a message:\n<cross-session-message from=\"x\">hi</cross-session-message>"
		if got := adviseAt(t, cfg, "s2", repo, peer, 20_000, hookNow); got != "" {
			t.Fatalf("output for the message = %q", got)
		}
		adviseAt(t, cfg, "s2", repo, "@"+path+" resume", 20_000, hookNow)
		if st, _ := advisor.Load(dir, "s2"); st.Parent != parent || st.Thread != "r4sefxw4" {
			t.Errorf("state = %+v, want the resume read", st)
		}
	})

	t.Run("second handoff in the same session", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		first := adviseAt(t, cfg, "s1", repo, "hi", 212_000, hookNow)
		stamp, id := thread(t, first)
		written := stamp + "-" + id + "-advisor.md"
		writeHandoff(t, repo, written)
		later := hookNow.Add(8 * time.Hour)
		ctxt := adviseAt(t, cfg, "s1", repo, "hi", 365_000, later)
		stamp2, id2 := thread(t, ctxt)
		if stamp2 != "2026-10-12/1700" || id2 != id || !strings.Contains(ctxt, `"Follows: `+written+`"`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("second handoff after a flat parent", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		adviseAt(t, cfg, "s1", repo, "hi", 20_000, hookNow)
		if err := advisor.SetHandoff(dir, "s1", "", "r4sefxw4", "2026-10-09-1358-r4sefxw4"); err != nil {
			t.Fatal(err)
		}
		const flat = "2026-10-09-1358-r4sefxw4-advisor.md"
		writeHandoff(t, repo, flat)
		ctxt := adviseAt(t, cfg, "s1", repo, "hi", 212_000, hookNow)
		if _, id := thread(t, ctxt); id != "r4sefxw4" || !strings.Contains(ctxt, `"Follows: `+flat+`"`) {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("no handoff written from the last advice", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		_, id := thread(t, adviseAt(t, cfg, "s1", repo, "hi", 212_000, hookNow))
		ctxt := adviseAt(t, cfg, "s1", repo, "hi", 365_000, hookNow.Add(time.Hour))
		if _, id2 := thread(t, ctxt); id2 != id || strings.Contains(ctxt, "Follows:") {
			t.Errorf("instruction = %q", ctxt)
		}
	})

	t.Run("thread split", func(t *testing.T) {
		useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		path := writeHandoff(t, repo, parent)
		for _, s := range []string{"s2", "s3"} {
			ctxt := adviseAt(t, cfg, s, repo, "@"+path+" resume", 212_000, hookNow)
			if _, id := thread(t, ctxt); id != "r4sefxw4" || !strings.Contains(ctxt, `"Follows: `+parent+`"`) {
				t.Errorf("%s: instruction = %q", s, ctxt)
			}
		}
	})

	t.Run("paths that are not a handoff of this project", func(t *testing.T) {
		dir := useHome(t)
		repo, other, cfg := gitRepo(t), gitRepo(t), configWith(t, thresholdsConfig)
		writeHandoff(t, other, parent)
		for _, text := range []string{
			"@" + filepath.Join(repo, ".jalon", "handoffs", "missing.md"),
			"@" + filepath.Join(repo, ".jalon", "handoffs", "2026-10-09", "missing.md"),
			"@" + filepath.Join(other, ".jalon", "handoffs", parent),
			"@" + filepath.Join(repo, ".jalon", "handoffs", ".md"),
		} {
			adviseAt(t, cfg, "s2", repo, text, 20_000, hookNow)
		}
		if st, _ := advisor.Load(dir, "s2"); st.Parent != "" || st.Thread != "" || st.Proposed != "" {
			t.Errorf("state = %+v, want no handoff", st)
		}
	})

	t.Run("several paths: the last one", func(t *testing.T) {
		dir := useHome(t)
		repo, cfg := gitRepo(t), configWith(t, thresholdsConfig)
		a := writeHandoff(t, repo, "2026-10-09-1358-aaaaaaaa-a.md")
		b := writeHandoff(t, repo, "2026-10-09/1400-bbbbbbbb-b.md")
		adviseAt(t, cfg, "s2", repo, "@"+a+", @"+b+" compare", 20_000, hookNow)
		if st, _ := advisor.Load(dir, "s2"); st.Parent != "2026-10-09/1400-bbbbbbbb-b.md" || st.Thread != "bbbbbbbb" {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestThreadOf(t *testing.T) {
	for name, want := range map[string]string{
		"2026-10-10/0847-4c7vifcm-impl.md":     "4c7vifcm",
		"2026-10-10-0847-4c7vifcm-impl.md":     "4c7vifcm",
		"2026-10-09-notes.md":                  "",
		"2026-10-10/notes.md":                  "",
		"2026-10-10/0847-4c7vifcm-impl.md.bak": "",
	} {
		if got := threadOf(name); got != want {
			t.Errorf("threadOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestHookKeepsTheFloorOfTheSession(t *testing.T) {
	dir, cfg := hookState(t, "s1", 120_000)
	repo := gitRepo(t)
	runPrompt(t, cfg, hookInput("s1", repo))
	if st, _ := advisor.Load(dir, "s1"); st.Floor != startSize {
		t.Fatalf("floor = %d, want %d from the head of the transcript", st.Floor, startSize)
	}
	// Once kept, it is not read again: a state that holds another floor keeps it.
	if err := advisor.SetFloor(dir, "s1", 70_000); err != nil {
		t.Fatal(err)
	}
	runPrompt(t, cfg, hookInput("s1", repo))
	if st, _ := advisor.Load(dir, "s1"); st.Floor != 70_000 {
		t.Errorf("floor = %d, want the kept 70000", st.Floor)
	}
}

func TestHookTriesTheFloorAgainWhenTheTranscriptHasNone(t *testing.T) {
	dir := useHome(t)
	cfg := configWith(t, "")
	repo := gitRepo(t)
	// A size from a compaction boundary, and no assistant entry to give a floor.
	appendTranscript(t, "s1", `{"type":"system","subtype":"compact_boundary","compactMetadata":{"postTokens":15000}}`+"\n")
	runPrompt(t, cfg, hookInput("s1", repo))
	if st, ok := advisor.Load(dir, "s1"); !ok || st.Floor != 0 {
		t.Fatalf("state = %+v, %v; want the size and no floor", st, ok)
	}
	appendTranscript(t, "s1", assistantLine(40_000))
	runPrompt(t, cfg, hookInput("s1", repo))
	if st, _ := advisor.Load(dir, "s1"); st.Floor != 40_000 {
		t.Errorf("floor = %d, want 40000 on the next prompt", st.Floor)
	}
}

// promptInput is a prompt hook input with a prompt ID.
func promptInput(session, promptID, cwd string) string {
	b, _ := json.Marshal(map[string]string{"session_id": session, "prompt_id": promptID, "cwd": cwd, "transcript_path": transcriptFile(session), "hook_event_name": "UserPromptSubmit", "prompt": "hi"})
	return string(b)
}

// stopInput is a stop hook input shaped as Claude Code's, read on 2026-10-12.
func stopInput(session, promptID, cwd, last string, active bool) string {
	in := map[string]any{"session_id": session, "transcript_path": transcriptFile(session), "cwd": cwd, "hook_event_name": "Stop",
		"stop_hook_active": active, "last_assistant_message": last, "permission_mode": "default", "effort": "high"}
	if promptID != "" {
		in["prompt_id"] = promptID
	}
	b, _ := json.Marshal(in)
	return string(b)
}

func runPromptAt(t *testing.T, configPath, stdin string, now time.Time) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPromptHook(context.Background(), []string{"-config", configPath}, strings.NewReader(stdin), &out, now); err != nil {
		t.Fatalf("hook: %v", err)
	}
	return out.String()
}

func runStop(t *testing.T, configPath, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runStopHook(context.Background(), []string{"-config", configPath}, strings.NewReader(stdin), &out, hookNow); err != nil {
		t.Fatalf("stop hook: %v", err)
	}
	return out.String()
}

type stopOutput struct {
	Decision      string  `json:"decision"`
	Reason        string  `json:"reason"`
	SystemMessage *string `json:"systemMessage"`
}

func parseStop(t *testing.T, raw string) stopOutput {
	t.Helper()
	var out stopOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, raw)
	}
	if out.Decision != "block" {
		t.Errorf("decision = %q, want block", out.Decision)
	}
	return out
}

func TestPromptHookRecordsThePromptIDs(t *testing.T) {
	t.Run("threshold crossed", func(t *testing.T) {
		dir, cfg := hookState(t, "s1", 210_000)
		if runPrompt(t, cfg, promptInput("s1", "p1", gitRepo(t))) == "" {
			t.Fatal("no advice")
		}
		if st, _ := advisor.Load(dir, "s1"); st.LastAdvised != 210_000 || st.Prompt != "p1" || st.AdvisedPrompt != "p1" {
			t.Errorf("state = %+v, want last advised 210000, prompt and advised prompt p1", st)
		}
	})
	t.Run("below the threshold", func(t *testing.T) {
		dir, cfg := hookState(t, "s1", 120_000)
		if got := runPrompt(t, cfg, promptInput("s1", "p2", gitRepo(t))); got != "" {
			t.Fatalf("output = %q", got)
		}
		if st, _ := advisor.Load(dir, "s1"); st.Prompt != "p2" || st.AdvisedPrompt != "" {
			t.Errorf("state = %+v, want prompt p2 and no advised prompt", st)
		}
	})
	t.Run("first prompt, no size yet", func(t *testing.T) {
		dir := useHome(t)
		if got := runPrompt(t, configWith(t, thresholdsConfig), promptInput("s1", "p1", gitRepo(t))); got != "" {
			t.Fatalf("output = %q", got)
		}
		if st, _ := advisor.Load(dir, "s1"); st.Prompt != "p1" || st.Size != 0 {
			t.Errorf("state = %+v, want prompt p1 and no size", st)
		}
	})
	t.Run("no size keeps the size and the advice", func(t *testing.T) {
		dir, cfg := hookState(t, "s1", 210_000)
		if runPrompt(t, cfg, promptInput("s1", "p1", gitRepo(t))) == "" {
			t.Fatal("no advice")
		}
		// A compaction that gives no size: the next size decides the step reset.
		appendTranscript(t, "s1", `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto"}}`+"\n")
		runPrompt(t, cfg, promptInput("s1", "p2", gitRepo(t)))
		if st, _ := advisor.Load(dir, "s1"); st.Prompt != "p2" || st.Size != 210_000 || st.LastAdvised != 210_000 {
			t.Errorf("state = %+v, want prompt p2, size and last advised 210000", st)
		}
	})
	t.Run("input without a prompt ID", func(t *testing.T) {
		dir, cfg := hookState(t, "s1", 210_000)
		if runPrompt(t, cfg, hookInput("s1", gitRepo(t))) == "" {
			t.Fatal("no advice")
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "s1.json")); strings.Contains(string(b), "prompt") {
			t.Errorf("state file = %s, want no prompt ID", b)
		}
	})
}

// advisedAt1748 has the prompt hook advise at prompt p1, at 274K tokens and
// 17:48, as on 2026-10-12, and returns the repository, the config and the
// proposed handoff name prefix.
func advisedAt1748(t *testing.T) (repo, cfg, prefix string) {
	t.Helper()
	dir, cfg := hookState(t, "s1", 274_000)
	repo = gitRepo(t)
	if runPromptAt(t, cfg, promptInput("s1", "p1", repo), time.Date(2026, 10, 12, 17, 48, 0, 0, time.UTC)) == "" {
		t.Fatal("no advice")
	}
	appendTranscript(t, "s1", typedPrompt("p1"), assistantLine(275_000))
	st, _ := advisor.Load(dir, "s1")
	if !strings.HasPrefix(st.Proposed, "2026-10-12/1748-") {
		t.Fatalf("proposed = %q", st.Proposed)
	}
	return repo, cfg, st.Proposed
}

func TestStopHookSectionGiven(t *testing.T) {
	repo, cfg, _ := advisedAt1748(t)
	home := os.Getenv("HOME")
	before := snapshot(t, home)
	if got := runStop(t, cfg, stopInput("s1", "p1", repo, "Voilà.\n\n> **Jalon** — Compact: …", false)); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
	if after := snapshot(t, home); after != before {
		t.Errorf("the stop hook wrote:\nbefore %s\nafter  %s", before, after)
	}
}

func TestStopHookSectionMissing(t *testing.T) {
	repo, cfg, prefix := advisedAt1748(t)
	state := filepath.Join(os.Getenv("HOME"), ".jalon", "sessions", "s1.json")
	before, _ := os.ReadFile(state)
	out := parseStop(t, runStop(t, cfg, stopInput("s1", "p1", repo, "Le fichier de reprise est écrit.", false)))
	proposed := filepath.Join(repo, ".jalon", "handoffs", prefix) + "-<short-slug>.md"
	if !strings.HasPrefix(out.Reason, "Jalon: reminder, context at 274K tokens: ") ||
		!strings.Contains(out.Reason, "Do not repeat your answer") ||
		!strings.Contains(out.Reason, "a handoff written to "+proposed) ||
		!strings.Contains(out.Reason, "starting with **Jalon** —") ||
		// Short: the terminal prints it, and the body was given at the start of the turn.
		strings.Count(out.Reason, "\n") > 2 || strings.Contains(out.Reason, "first words are") ||
		strings.Contains(out.Reason, "already written") || strings.Contains(out.Reason, "{") {
		t.Errorf("reason = %q", out.Reason)
	}
	// The notice was shown at the start of the turn.
	if out.SystemMessage != nil {
		t.Errorf("systemMessage = %q, want none", *out.SystemMessage)
	}
	if after, _ := os.ReadFile(state); string(after) != string(before) {
		t.Errorf("state changed:\n%s\n%s", before, after)
	}

	t.Run("handoff written", func(t *testing.T) {
		written := writeHandoff(t, repo, prefix+"-squad-mr-tour-2.md")
		out := parseStop(t, runStop(t, cfg, stopInput("s1", "p1", repo, "Le fichier de reprise est écrit.", false)))
		if !strings.HasPrefix(out.Reason, "Jalon: reminder") ||
			!strings.Contains(out.Reason, "The handoff "+written+" is already written: do not write another file") {
			t.Errorf("reason = %q", out.Reason)
		}
	})
}

func TestStopHookNoReminderAfterACompaction(t *testing.T) {
	repo, cfg, _ := advisedAt1748(t)
	// Claude Code compacted during the turn: the advice is moot.
	appendTranscript(t, "s1", `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto","postTokens":22000}}`+"\n", assistantLine(30_000))
	if got := runStop(t, cfg, stopInput("s1", "p1", repo, "Le fichier de reprise est écrit.", false)); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
}

func TestStopHookCrossedDuringTheTurn(t *testing.T) {
	dir := useHome(t)
	cfg := configWith(t, "context:\n  advise_tokens: 200000\n  advise_step_tokens: 150000\n")
	repo := gitRepo(t)
	appendTranscript(t, "s1", typedPrompt("p1"), assistantLine(67_000), assistantLine(150_000))
	if got := runPrompt(t, cfg, promptInput("s1", "p2", repo)); got != "" {
		t.Fatalf("prompt hook output = %q, want nothing", got)
	}
	appendTranscript(t, "s1", typedPrompt("p2"), assistantLine(296_000))
	out := parseStop(t, runStop(t, cfg, stopInput("s1", "p2", repo, "Done.", false)))
	if !strings.HasPrefix(out.Reason, "Jalon: context at 296K tokens.") ||
		!strings.Contains(out.Reason, "do not repeat it") ||
		!strings.Contains(out.Reason, "Add, as your last message, a short section written as a Markdown blockquote") ||
		!regexp.MustCompile(`Write `+regexp.QuoteMeta(repo)+`/\.jalon/handoffs/2026-10-12/0900-[0-9a-z]{8}-<short-slug>\.md`).MatchString(out.Reason) ||
		strings.Contains(out.Reason, "{") {
		t.Errorf("reason = %q", out.Reason)
	}
	if out.SystemMessage == nil || *out.SystemMessage != "Jalon: context at 296K tokens, a reset proposal follows." {
		t.Errorf("systemMessage = %v", out.SystemMessage)
	}
	if st, _ := advisor.Load(dir, "s1"); st.LastAdvised != 296_000 || st.AdvisedPrompt != "p2" || st.Prompt != "p2" {
		t.Errorf("state = %+v, want last advised 296000 at prompt p2", st)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon", "handoffs")); err != nil {
		t.Errorf("handoff folder: %v", err)
	}

	// The model added the section: the stop hook runs again, already active.
	if got := runStop(t, cfg, stopInput("s1", "p2", repo, "> **Jalon** — …", true)); got != "" {
		t.Errorf("second stop output = %q, want nothing", got)
	}
	// The next prompt does not repeat the advice.
	appendTranscript(t, "s1", typedPrompt("p3"), assistantLine(301_000))
	if got := runPrompt(t, cfg, promptInput("s1", "p3", repo)); got != "" {
		t.Errorf("next prompt output = %q, want nothing", got)
	}
}

func TestStopHookFirstTurnCrossesTheThreshold(t *testing.T) {
	dir := useHome(t)
	cfg := configWith(t, thresholdsConfig)
	repo := gitRepo(t)
	// The session's first prompt: the transcript holds no size yet.
	if got := runPrompt(t, cfg, promptInput("s1", "p1", repo)); got != "" {
		t.Fatalf("prompt hook output = %q, want nothing", got)
	}
	appendTranscript(t, "s1", typedPrompt("p1"), assistantLine(25_000), assistantLine(210_000))
	out := parseStop(t, runStop(t, cfg, stopInput("s1", "p1", repo, "Done.", false)))
	if !strings.HasPrefix(out.Reason, "Jalon: context at 210K tokens.") {
		t.Errorf("reason = %q", out.Reason)
	}
	// The floor is read at Stop, from the turn's first request.
	if st, _ := advisor.Load(dir, "s1"); st.Floor != 25_000 || st.LastAdvised != 210_000 || st.AdvisedPrompt != "p1" {
		t.Errorf("state = %+v, want floor 25000, last advised 210000 at prompt p1", st)
	}
}

func TestStopHookBelowTheThresholdWritesOnlyTheState(t *testing.T) {
	dir, cfg := hookState(t, "s1", 100_000)
	repo := gitRepo(t)
	runPrompt(t, cfg, promptInput("s1", "p3", repo))
	appendTranscript(t, "s1", typedPrompt("p3"), assistantLine(120_000))
	home := os.Getenv("HOME")
	homeBefore, repoBefore := tree(t, home), tree(t, repo)
	if got := runStop(t, cfg, stopInput("s1", "p3", repo, "Done.", false)); got != "" {
		t.Errorf("output = %q, want nothing", got)
	}
	if st, _ := advisor.Load(dir, "s1"); st.Size != 120_000 || st.Prompt != "p3" {
		t.Errorf("state = %+v, want size 120000 recorded for p3", st)
	}
	expectFiles(t, "the stop hook in the home", created(t, home, homeBefore))
	expectFiles(t, "the stop hook in the project", created(t, repo, repoBefore))
}

func TestStopHookDoesNothing(t *testing.T) {
	off := "context:\n  enabled: false\n  advise_tokens: 200000\n  advise_step_tokens: 150000\n"
	tests := map[string]struct {
		cfg   string // "" for thresholdsConfig
		stdin func(repo string) string
	}{
		"already blocked":          {"", func(repo string) string { return stopInput("s1", "p1", repo, "no section", true) }},
		"turn not started by them": {"", func(repo string) string { return stopInput("s1", "p5", repo, "no section", false) }},
		"no prompt ID":             {"", func(repo string) string { return stopInput("s1", "", repo, "no section", false) }},
		"session never seen":       {"", func(repo string) string { return stopInput("s9", "p1", repo, "no section", false) }},
		"unsafe session":           {"", func(repo string) string { return stopInput("../s1", "p1", repo, "no section", false) }},
		"advisor disabled":         {off, func(repo string) string { return stopInput("s1", "p1", repo, "no section", false) }},
		"config does not parse":    {"listen: [\n", func(repo string) string { return stopInput("s1", "p1", repo, "no section", false) }},
		"input is not JSON":        {"", func(string) string { return "nope" }},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// Advised at p1, the reply lacks the section, and the size has grown
			// past the step: anything but a stop for p1 would block.
			_, cfg := hookState(t, "s1", 210_000)
			repo := gitRepo(t)
			if runPrompt(t, cfg, promptInput("s1", "p1", repo)) == "" {
				t.Fatal("no advice")
			}
			appendTranscript(t, "s1", typedPrompt("p1"), assistantLine(400_000), assistantLine(9_000_000))
			if tt.cfg != "" {
				cfg = configWith(t, tt.cfg)
			}
			home := os.Getenv("HOME")
			before := snapshot(t, home)
			if got := runStop(t, cfg, tt.stdin(repo)); got != "" {
				t.Errorf("output = %q, want nothing", got)
			}
			if after := snapshot(t, home); after != before {
				t.Errorf("the stop hook wrote:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}

func TestStopHookUnknownClientIsSilentAndSaysWhy(t *testing.T) {
	_, cfg := hookState(t, "s1", 275_000)
	var errBuf bytes.Buffer
	old := stderr
	stderr = &errBuf
	defer func() { stderr = old }()
	var out bytes.Buffer
	args := []string{"-client", "nope", "-config", cfg}
	if err := runStopHook(context.Background(), args, strings.NewReader(stopInput("s1", "p1", t.TempDir(), "", false)), &out, hookNow); err != nil || out.Len() != 0 {
		t.Errorf("err=%v output=%q, want silence", err, out.String())
	}
	if !strings.Contains(errBuf.String(), `unknown client "nope"`) || !strings.Contains(errBuf.String(), "claude-code") {
		t.Errorf("stderr = %q", errBuf.String())
	}
	if err := runStopHook(context.Background(), []string{"-bogus"}, strings.NewReader(""), &out, hookNow); err == nil {
		t.Error("an invalid flag must be an error, for exit 1")
	}
}

// The hooks share bounded: work that outlasts the limit gives no output. The
// work blocks until released, so the test reads no clock.
func TestBoundedGivesUpAfterTheLimit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	if got := bounded(context.Background(), time.Nanosecond, func() []byte {
		<-release
		return []byte("late")
	}); got != nil {
		t.Errorf("output = %q, want nothing at the limit", got)
	}
	if got := bounded(context.Background(), time.Hour, func() []byte { return []byte("on time") }); string(got) != "on time" {
		t.Errorf("output = %q, want the work's", got)
	}
}

// A program that runs claude -p reads the model's last message as its output:
// the hooks never advise a session no person typed in.
func TestProgramsAreNeverAdvised(t *testing.T) {
	t.Run("program resumes a session", func(t *testing.T) {
		dir := useHome(t)
		cfg := configWith(t, thresholdsConfig)
		repo := gitRepo(t)
		appendTranscript(t, "s1", programPrompt("p6"), assistantLine(startSize), programPrompt("p7"), assistantLine(300_000))
		if got := runPrompt(t, cfg, promptInput("s1", "p8", repo)); got != "" {
			t.Errorf("output = %q, want nothing", got)
		}
		if st, _ := advisor.Load(dir, "s1"); st.Size != 300_000 || st.Prompt != "p8" || st.LastAdvised != 0 || st.Typed {
			t.Errorf("state = %+v, want the size and the prompt ID recorded, no advice", st)
		}
		if _, err := os.Stat(filepath.Join(repo, ".jalon")); err == nil {
			t.Error("a handoff folder was created")
		}

		// A person then types in it: the prompt counts from the next one on.
		if got := runPrompt(t, cfg, promptInput("s1", "p9", repo)); got != "" {
			t.Errorf("output at the typed prompt = %q, want nothing: it is not in the transcript yet", got)
		}
		appendTranscript(t, "s1", typedPrompt("p9"), assistantLine(305_000))
		if got := runPrompt(t, cfg, promptInput("s1", "p10", repo)); !strings.Contains(got, "305K") {
			t.Errorf("output after the typed prompt = %q, want the advice", got)
		}
	})

	t.Run("recorded once", func(t *testing.T) {
		dir := useHome(t)
		cfg := configWith(t, thresholdsConfig)
		// The typed prompt is out of the part read, but the state recorded it.
		appendTranscript(t, "s1", assistantLine(startSize), assistantLine(300_000))
		if err := advisor.Record(dir, "s1", 100_000, hookNow, "p1", true); err != nil {
			t.Fatal(err)
		}
		if got := runPrompt(t, cfg, promptInput("s1", "p2", gitRepo(t))); !strings.Contains(got, "300K") {
			t.Errorf("output = %q, want the advice", got)
		}
	})

	t.Run("turn started by a program", func(t *testing.T) {
		_, cfg := hookState(t, "s1", 100_000)
		repo := gitRepo(t)
		runPrompt(t, cfg, promptInput("s1", "p6", repo))
		appendTranscript(t, "s1", programPrompt("p6"), assistantLine(400_000))
		if got := runStop(t, cfg, stopInput("s1", "p6", repo, "The task's output.", false)); got != "" {
			t.Errorf("output = %q, want nothing: the program's output is the last message", got)
		}
		assertNoAdvice(t, repo, "p6")
	})

	t.Run("prompt entry out of reach", func(t *testing.T) {
		_, cfg := hookState(t, "s1", 100_000)
		repo := gitRepo(t)
		runPrompt(t, cfg, promptInput("s1", "p1", repo))
		// The transcript holds no entry for p1, only the earlier typed p0.
		appendTranscript(t, "s1", assistantLine(400_000))
		if got := runStop(t, cfg, stopInput("s1", "p1", repo, "Done.", false)); got != "" {
			t.Errorf("output = %q, want nothing", got)
		}
		assertNoAdvice(t, repo, "p1")
	})

	// The reminder path: a program resumes a session a person typed in, the
	// prompt hook advises, and the program's turn must still end on its own
	// last message.
	t.Run("reminder, turn started by a program", func(t *testing.T) {
		_, cfg := hookState(t, "s1", 210_000)
		repo := gitRepo(t)
		if runPrompt(t, cfg, promptInput("s1", "p6", repo)) == "" {
			t.Fatal("no advice")
		}
		appendTranscript(t, "s1", programPrompt("p6"), assistantLine(212_000))
		if got := runStop(t, cfg, stopInput("s1", "p6", repo, "The task's output.", false)); got != "" {
			t.Errorf("output = %q, want nothing: the program's output is the last message", got)
		}
	})

	t.Run("reminder, prompt entry out of reach", func(t *testing.T) {
		_, cfg := hookState(t, "s1", 210_000)
		repo := gitRepo(t)
		if runPrompt(t, cfg, promptInput("s1", "p1", repo)) == "" {
			t.Fatal("no advice")
		}
		// The transcript holds no entry for p1, only the earlier typed p0.
		appendTranscript(t, "s1", assistantLine(212_000))
		if got := runStop(t, cfg, stopInput("s1", "p1", repo, "Done.", false)); got != "" {
			t.Errorf("output = %q, want nothing", got)
		}
	})
}

// assertNoAdvice fails when session s1's state records an advice, or when the
// handoff folder was created in repo. The stop hook may record the size: it
// looks for the typed prompt only once a reset is due.
func assertNoAdvice(t *testing.T, repo, prompt string) {
	t.Helper()
	st, _ := advisor.Load(filepath.Join(os.Getenv("HOME"), ".jalon", "sessions"), "s1")
	if st.LastAdvised != 0 || st.AdvisedPrompt == prompt || st.Proposed != "" {
		t.Errorf("state = %+v, want no advice", st)
	}
	if _, err := os.Stat(filepath.Join(repo, ".jalon")); err == nil {
		t.Error("the handoff folder was created")
	}
}
