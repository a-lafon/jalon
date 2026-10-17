package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/claudecode"
)

const testExe = "/opt/homebrew/bin/jalon"

// withExecutable makes init see exe as the running binary, in a fresh home
// directory so the config file is never the real one. The home's symlinks are
// resolved, as in the backup paths init prints. No jalon is on the PATH, and no
// claude: a test never runs the real one, see withClaude.
func withExecutable(t *testing.T, exe string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	// Codex reads its hooks there, else under ~/.codex.
	t.Setenv("CODEX_HOME", "")
	oldExe, oldLook, oldRun := executable, lookPath, runClient
	executable = func() (string, error) { return exe, nil }
	lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	runClient = func(name string, _ ...string) ([]byte, error) {
		return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	t.Cleanup(func() { executable, lookPath, runClient = oldExe, oldLook, oldRun })
}

// withClaude makes the claude commands init runs answer from answers, keyed by
// the command line, and returns the commands run so far.
func withClaude(t *testing.T, answers map[string]string) *[]string {
	t.Helper()
	var runs []string
	runClient = func(name string, args ...string) ([]byte, error) {
		cmd := strings.Join(append([]string{name}, args...), " ")
		runs = append(runs, cmd)
		out, ok := answers[cmd]
		if !ok {
			return nil, errors.New("unexpected command " + cmd)
		}
		return []byte(out), nil
	}
	return &runs
}

// homeConfig returns the config file init uses, in the home withExecutable set,
// after writing content to it with mode unless content is empty.
func homeConfig(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".jalon", "config.yaml")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func runInitOK(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runInit(args, &out); err != nil {
		t.Fatalf("init %v: %v", args, err)
	}
	return out.String()
}

func TestInitInstallsOnceWithABackup(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"model": "opus", "hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}]}}`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}

	out := runInitOK(t, "-settings", path)
	if !strings.Contains(out, "prompt hook: added "+testExe+" hook prompt --client claude-code") ||
		!strings.Contains(out, "stop hook: added "+testExe+" hook stop --client claude-code") ||
		!strings.Contains(out, "status line: added "+testExe+" statusline --client claude-code") ||
		!strings.Contains(out, "sessions started from now on") {
		t.Errorf("output = %q", out)
	}
	installed, _ := os.ReadFile(path)
	if !strings.Contains(string(installed), testExe+" hook prompt") || !strings.Contains(string(installed), testExe+" hook stop") ||
		!strings.Contains(string(installed), testExe+" statusline") ||
		!strings.Contains(string(installed), "rtk hook claude") {
		t.Errorf("settings = %s", installed)
	}
	if backup, _ := os.ReadFile(path + ".jalon.bak"); string(backup) != original {
		t.Errorf("backup = %s, want the original", backup)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640 kept", info.Mode().Perm())
	}

	// The second run writes nothing, not even the backup.
	os.Remove(path + ".jalon.bak")
	out = runInitOK(t, "-settings", path)
	if !strings.Contains(out, "already installed") || !strings.Contains(out, "nothing to change") {
		t.Errorf("second run output = %q", out)
	}
	if _, err := os.Stat(path + ".jalon.bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("the second run wrote a backup")
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, installed) {
		t.Error("the second run changed the settings")
	}
}

func TestInitCreatesMissingSettings(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "claude", "settings.json")
	out := runInitOK(t, "-settings", path)
	if strings.Contains(out, "previous version") {
		t.Errorf("output mentions a backup for a new file: %q", out)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), testExe+" statusline --client claude-code") {
		t.Fatalf("settings = %s, %v", b, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runInitOK(t, "-settings", path, "-dry-run")
	if !strings.Contains(out, "Dry run") || !strings.Contains(out, `"UserPromptSubmit"`) {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(path); string(b) != `{}` {
		t.Errorf("settings changed: %s", b)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("dry run left files: %v", entries)
	}
}

func TestInitUninstall(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"model": "opus"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runInitOK(t, "-settings", path)
	out := runInitOK(t, "-settings", path, "-uninstall")
	if !strings.Contains(out, "prompt hook: removed") || !strings.Contains(out, "stop hook: removed") || !strings.Contains(out, "status line: removed") ||
		!strings.Contains(out, "mod: not installed (claude not found)") {
		t.Errorf("output = %q", out)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "jalon") || !strings.Contains(string(b), `"model": "opus"`) {
		t.Errorf("settings = %s", b)
	}
	if out := runInitOK(t, "-settings", path, "-uninstall"); !strings.Contains(out, "nothing to change") || strings.Contains(out, "Thresholds") {
		t.Errorf("second uninstall output = %q", out)
	}
}

func TestInitKeepsASymlinkedSettingsFile(t *testing.T) {
	withExecutable(t, testExe)
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-settings.json")
	link := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	runInitOK(t, "-settings", link)
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if b, _ := os.ReadFile(target); !strings.Contains(string(b), "hook prompt") {
		t.Errorf("target = %s", b)
	}
}

func TestInitRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"hooks": `), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		exe  string
		args []string
		want string
	}{
		"go run build":   {"/var/folders/x/T/go-build123/b001/exe/jalon", []string{"-settings", path}, "temporary go run build"},
		"invalid JSON":   {testExe, []string{"-settings", path}, "nothing written"},
		"unknown client": {testExe, []string{"-settings", path, "-client", "nope"}, "unknown client"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withExecutable(t, tt.exe)
			err := runInit(tt.args, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			if b, _ := os.ReadFile(path); string(b) != `{"hooks": ` {
				t.Errorf("settings changed: %s", b)
			}
		})
	}
}

func TestInitRecordsThePathEntryOfTheRunningBinary(t *testing.T) {
	// On Linux os.Executable resolves links: it reports the versioned file a
	// package manager deletes on upgrade, while the PATH entry is the link it
	// keeps pointing at the new version.
	tests := map[string]struct {
		binDir string
		onPath func(link, other string) string // what lookPath finds, "" for nothing
		wantIs string                          // "link" or "real"
	}{
		"link to the running binary": {"bin", func(link, _ string) string { return link }, "link"},
		"link path with a space":     {"my tools", func(link, _ string) string { return link }, "link"},
		"another jalon on the PATH":  {"bin", func(_, other string) string { return other }, "real"},
		"no jalon on the PATH":       {"bin", func(string, string) string { return "" }, "real"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			tmp, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(tmp, "Cellar", "jalon", "0.1.0", "jalon")
			other := filepath.Join(tmp, "other", "jalon")
			link := filepath.Join(tmp, tt.binDir, "jalon")
			for _, f := range []string{real, other} {
				if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f, []byte(f), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			withExecutable(t, real)
			lookPath = func(string) (string, error) {
				if p := tt.onPath(link, other); p != "" {
					return p, nil
				}
				return "", exec.ErrNotFound
			}
			settings := filepath.Join(tmp, "settings.json")
			runInitOK(t, "-settings", settings)
			b, _ := os.ReadFile(settings)
			want := real
			if tt.wantIs == "link" {
				want = link
			}
			if strings.ContainsAny(want, " ") {
				want = "'" + want + "'"
			}
			if !strings.Contains(string(b), want+" hook prompt --client claude-code") || !strings.Contains(string(b), want+" hook stop --client claude-code") {
				t.Errorf("settings do not run %s:\n%s", want, b)
			}
		})
	}
}

func TestInitRefusesAPathItCannotQuote(t *testing.T) {
	withExecutable(t, "/opt/it's/jalon")
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runInit([]string{"-settings", path}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot be put in a shell command") {
		t.Fatalf("err = %v, want the path refused", err)
	}
	if n := strings.Count(err.Error(), "nothing written"); n != 1 {
		t.Errorf("%q says \"nothing written\" %d times, want once", err, n)
	}
	if b, _ := os.ReadFile(path); string(b) != "{}" {
		t.Errorf("settings changed: %s", b)
	}
	if _, err := os.Stat(path + ".jalon.bak"); err == nil {
		t.Error("a backup was written")
	}
}

func TestInitPrintsDefaultThresholds(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	out := runInitOK(t, "-settings", path)
	if !strings.Contains(out, "Thresholds in force: advise_tokens 200,000, advise_step_tokens 150,000, from the defaults") ||
		!strings.Contains(out, "--advise-tokens <n> --advise-step-tokens <n>") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Dir(homeConfig(t, "", 0))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("init without a threshold created ~/.jalon: %v", err)
	}
}

func TestInitSetsAThresholdInANewConfig(t *testing.T) {
	withExecutable(t, testExe)
	settings, cfg := filepath.Join(t.TempDir(), "settings.json"), homeConfig(t, "", 0)
	out := runInitOK(t, "-settings", settings, "-advise-tokens", "200000")
	if !strings.Contains(out, "Wrote "+cfg+".") ||
		!strings.Contains(out, "Thresholds in force: advise_tokens 200,000, advise_step_tokens 150,000, from "+cfg) {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "context:\n  advise_tokens: 200000\n" {
		t.Errorf("config = %q", b)
	}
	if info, err := os.Stat(cfg); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, %v; want 0600", info, err)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), "hook prompt") {
		t.Errorf("settings = %s", b)
	}
}

// commentedConfig is a config file as a user keeps it: a comment, blank lines and
// comments after the values.
const commentedConfig = `# Jalon configuration.

context:
  enabled: true
  advise_tokens: 200000        # first advice
  advise_step_tokens: 150000   # next advice
  stale_after_turns: 5         # stale tool results
`

func TestInitSetsAThresholdAndKeepsTheOtherKeys(t *testing.T) {
	withExecutable(t, testExe)
	settings, cfg := filepath.Join(t.TempDir(), "settings.json"), homeConfig(t, commentedConfig, 0o644)
	out := runInitOK(t, "-settings", settings, "-advise-step-tokens", "100000")
	if !strings.Contains(out, "Wrote "+cfg+" (previous version in "+cfg+".jalon.bak)") ||
		!strings.Contains(out, "advise_tokens 200,000, advise_step_tokens 100,000, from "+cfg) {
		t.Errorf("output = %q", out)
	}
	want := strings.Replace(commentedConfig, "advise_step_tokens: 150000", "advise_step_tokens: 100000", 1)
	if b, _ := os.ReadFile(cfg); string(b) != want {
		t.Errorf("config:\n%s\nwant:\n%s", b, want)
	}
	if b, _ := os.ReadFile(cfg + ".jalon.bak"); string(b) != commentedConfig {
		t.Errorf("backup = %s, want the original", b)
	}
	if info, _ := os.Stat(cfg); info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 kept", info.Mode().Perm())
	}
}

func TestInitThresholdUnchanged(t *testing.T) {
	withExecutable(t, testExe)
	settings, cfg := filepath.Join(t.TempDir(), "settings.json"), homeConfig(t, commentedConfig, 0o600)
	out := runInitOK(t, "-settings", settings, "-advise-tokens", "200000")
	if !strings.Contains(out, cfg+": nothing to change.") || !strings.Contains(out, "Thresholds in force: advise_tokens 200,000") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(cfg + ".jalon.bak"); !errors.Is(err, os.ErrNotExist) {
		t.Error("an unchanged config got a backup")
	}
	if b, _ := os.ReadFile(cfg); string(b) != commentedConfig {
		t.Errorf("config changed: %s", b)
	}
}

func TestInitThresholdDryRun(t *testing.T) {
	withExecutable(t, testExe)
	settings, cfg := filepath.Join(t.TempDir(), "settings.json"), homeConfig(t, commentedConfig, 0o600)
	out := runInitOK(t, "-settings", settings, "-advise-tokens", "250000", "-dry-run")
	section := "Dry run, " + cfg + " not written. Its context section would become:\ncontext:\n  enabled: true\n  advise_tokens: 250000 # first advice\n"
	if !strings.Contains(out, section) || !strings.Contains(out, "Thresholds once written: advise_tokens 250,000") {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(cfg); string(b) != commentedConfig {
		t.Errorf("config changed: %s", b)
	}
	for _, dir := range []string{filepath.Dir(settings), filepath.Dir(cfg)} {
		if entries, _ := os.ReadDir(dir); len(entries) > 1 {
			t.Errorf("dry run left files: %v", entries)
		}
	}
}

func TestInitThresholdRefusalsWriteNothing(t *testing.T) {
	tests := map[string]struct {
		config string // the config file's content; none when empty
		args   []string
		want   string
	}{
		"zero threshold":       {"", []string{"-advise-tokens", "0"}, "context.advise_tokens must be positive"},
		"negative step":        {commentedConfig, []string{"-advise-step-tokens", "-5"}, "context.advise_step_tokens must be positive"},
		"uninstall with value": {commentedConfig, []string{"-uninstall", "-advise-tokens", "200000"}, "-uninstall does not take -advise-tokens"},
		"invalid config":       {"listn: x\n", nil, "field listn not found"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withExecutable(t, testExe)
			settings, cfg := filepath.Join(t.TempDir(), "settings.json"), homeConfig(t, tt.config, 0o600)
			if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			err := runInit(append([]string{"-settings", settings}, tt.args...), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "nothing written") {
				t.Errorf("err = %v, want %q and nothing written", err, tt.want)
			}
			if b, _ := os.ReadFile(settings); string(b) != `{}` {
				t.Errorf("settings changed: %s", b)
			}
			if b, _ := os.ReadFile(cfg); string(b) != tt.config {
				t.Errorf("config changed: %q", b)
			}
			for _, dir := range []string{filepath.Dir(settings), filepath.Dir(cfg)} {
				if entries, _ := os.ReadDir(dir); len(entries) > 1 {
					t.Errorf("files left: %v", entries)
				}
			}
		})
	}
}

func TestInitWithoutTheStatusLine(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	runInitOK(t, "-settings", path, "-statusline=false")
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "statusLine") || !strings.Contains(string(b), "hook prompt") {
		t.Errorf("settings = %s", b)
	}
}

const (
	claudeVersion = "claude --version"
	modInstall    = "claude plugin install jalon --marketplace a-lafon/jalon --config binary=" + testExe
	modRemove     = "claude plugin marketplace remove jalon --json"
)

func TestInitMod(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	runInitOK(t, "-settings", path)
	runs := withClaude(t, map[string]string{claudeVersion: "2.1.296 (Claude Code)\n", modInstall: "✔ Successfully installed plugin: jalon@jalon (scope: user)\n"})

	out := runInitOK(t, "-settings", path, "-mod")
	if !strings.Contains(out, "status line: removed, the mod draws the pole") || !strings.Contains(out, "mod: installed from a-lafon/jalon") {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "statusLine") || !strings.Contains(string(b), "hook prompt") {
		t.Errorf("settings = %s", b)
	}
	if want := []string{claudeVersion, modInstall}; !slices.Equal(*runs, want) {
		t.Errorf("runs = %v, want %v", *runs, want)
	}
}

func TestInitModDryRun(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	runs := withClaude(t, map[string]string{claudeVersion: "2.1.296 (Claude Code)\n"})
	out := runInitOK(t, "-settings", path, "-mod", "-dry-run")
	if !strings.Contains(out, "mod: would run "+modInstall) {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("the dry run wrote the settings")
	}
	if want := []string{claudeVersion}; !slices.Equal(*runs, want) {
		t.Errorf("runs = %v, want only the version check", *runs)
	}
}

func TestInitUninstallRemovesTheMod(t *testing.T) {
	withExecutable(t, testExe)
	path := filepath.Join(t.TempDir(), "settings.json")
	runInitOK(t, "-settings", path, "-statusline=false")
	withClaude(t, map[string]string{modRemove: `{"command":"marketplace-remove","outcome":"ok","marketplace":"jalon"}` + "\n"})
	if out := runInitOK(t, "-settings", path, "-uninstall"); !strings.Contains(out, "mod: removed") {
		t.Errorf("output = %q", out)
	}
}

func TestInitModRefusalsWriteNothing(t *testing.T) {
	tests := map[string]struct {
		args    []string
		version string
		want    string
	}{
		"old Claude Code":      {[]string{"-mod"}, "2.1.200 (Claude Code)\n", "needs Claude Code 2.1.292 or later"},
		"with the status line": {[]string{"-mod", "-statusline"}, "2.1.296 (Claude Code)\n", "drop -statusline"},
		"with -uninstall":      {[]string{"-mod", "-uninstall"}, "2.1.296 (Claude Code)\n", "takes no -mod"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withExecutable(t, testExe)
			path := filepath.Join(t.TempDir(), "settings.json")
			runs := withClaude(t, map[string]string{claudeVersion: tt.version})
			err := runInit(append([]string{"-settings", path}, tt.args...), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "nothing written") {
				t.Errorf("err = %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Error("settings written")
			}
			for _, r := range *runs {
				if r != claudeVersion {
					t.Errorf("ran %q", r)
				}
			}
		})
	}
}

// noClaude fails the test if init runs a claude command.
func noClaude(t *testing.T) {
	t.Helper()
	runClient = func(name string, args ...string) ([]byte, error) {
		t.Errorf("ran %s %v", name, args)
		return nil, errors.New("no claude command may run")
	}
}

// modSettings writes settings holding Jalon's hooks, the mod as claude plugin
// install records it, enabled or not, with binary unless it is "", and extra
// members, and returns their path.
func modSettings(t *testing.T, enabled bool, binary, extra string) string {
	t.Helper()
	configs := ""
	if binary != "" {
		configs = `, "pluginConfigs": {"jalon@jalon": {"options": {"binary": "` + binary + `"}}}`
	}
	hooks := `"hooks": {` +
		`"UserPromptSubmit": [{"hooks": [{"type": "command", "command": "` + testExe + ` hook prompt --client claude-code", "timeout": 2}]}], ` +
		`"Stop": [{"hooks": [{"type": "command", "command": "` + testExe + ` hook stop --client claude-code", "timeout": 2}]}]}`
	content := fmt.Sprintf(`{%s%s, "enabledPlugins": {"jalon@jalon": %t}%s}`, extra, hooks, enabled, configs)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitKeepsTheMod(t *testing.T) {
	jalonStatusLine := `"statusLine": {"type": "command", "command": "` + testExe + ` statusline --client claude-code"}, `
	tests := map[string]struct {
		enabled           bool
		extra             string
		want, wantNot     []string
		statusLineGone    bool // Jalon's status line was in the settings and is removed
		settingsUnchanged bool
	}{
		"rerun": {
			enabled: true, want: []string{"prompt hook: already installed", "stop hook: already installed", "nothing to change",
				"mod: kept; jalon init --statusline draws the pole in the status line instead"},
			wantNot: []string{"status line:", "(disabled)", "mod: it runs"}, settingsUnchanged: true,
		},
		"status line from an earlier install": {
			enabled: true, extra: jalonStatusLine,
			want:    []string{"status line: removed, the mod draws the pole", "mod: kept;"},
			wantNot: []string{"mod: it runs"}, statusLineGone: true,
		},
		"disabled": {
			want:    []string{"mod: kept (disabled); jalon init --statusline"},
			wantNot: []string{"status line:"}, settingsUnchanged: true,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withExecutable(t, testExe)
			noClaude(t)
			path := modSettings(t, tt.enabled, testExe, tt.extra)
			before, _ := os.ReadFile(path)
			out := runInitOK(t, "-settings", path)
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(out, w) {
					t.Errorf("output holds %q:\n%s", w, out)
				}
			}
			after, _ := os.ReadFile(path)
			if strings.Contains(string(after), "statusLine") || !strings.Contains(string(after), `"jalon@jalon"`) {
				t.Errorf("settings = %s", after)
			}
			if tt.settingsUnchanged != bytes.Equal(after, before) {
				t.Errorf("settings changed: %t, want %t", !bytes.Equal(after, before), tt.settingsUnchanged)
			}
		})
	}
}

func TestInitKeptModRunningAnotherBinary(t *testing.T) {
	tests := map[string]struct {
		binary string // "" for no binary option
		onPath string // what lookPath finds for jalon, "" for nothing
		stale  bool
	}{
		"another path":             {"/usr/local/bin/jalon", testExe, true},
		"the same path":            {testExe, "", false},
		"default, on the PATH":     {"", testExe, false},
		"default, not on the PATH": {"", "", true},
		"default, another on PATH": {"", "/usr/local/bin/jalon", true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withExecutable(t, testExe)
			noClaude(t)
			lookPath = func(string) (string, error) {
				if tt.onPath == "" {
					return "", exec.ErrNotFound
				}
				return tt.onPath, nil
			}
			out := runInitOK(t, "-settings", modSettings(t, true, tt.binary, ""))
			runs := "/usr/local/bin/jalon"
			if tt.binary == "" {
				runs = "jalon"
			}
			line := "mod: it runs " + runs + ", not " + testExe + "; jalon init --mod updates it"
			if strings.Contains(out, line) != tt.stale || strings.Contains(out, "mod: it runs") != tt.stale || !strings.Contains(out, "mod: kept") {
				t.Errorf("stale %t, output:\n%s", tt.stale, out)
			}
		})
	}
}

func TestInitStatusLineNextToTheMod(t *testing.T) {
	withExecutable(t, testExe)
	noClaude(t)
	path := modSettings(t, true, testExe, "")
	out := runInitOK(t, "-settings", path, "-statusline")
	if !strings.Contains(out, "status line: added") || !strings.Contains(out, "mod: installed too, the mod and the status line both draw the pole") ||
		strings.Contains(out, "mod: kept") {
		t.Errorf("output = %q", out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), testExe+" statusline") {
		t.Errorf("settings = %s", b)
	}
}

func TestInitWithoutTheModAddsTheStatusLine(t *testing.T) {
	withExecutable(t, testExe)
	noClaude(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"enabledPlugins": {"other@jalon": true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runInitOK(t, "-settings", path); !strings.Contains(out, "status line: added") || strings.Contains(out, "mod:") {
		t.Errorf("output = %q", out)
	}
}

func TestInitKeptModDryRun(t *testing.T) {
	withExecutable(t, testExe)
	noClaude(t)
	path := modSettings(t, true, testExe, `"statusLine": {"type": "command", "command": "`+testExe+` statusline --client claude-code"}, `)
	before, _ := os.ReadFile(path)
	out := runInitOK(t, "-settings", path, "-dry-run")
	preview := out[strings.Index(out, "It would become:"):]
	if !strings.Contains(out, "mod: kept") || !strings.Contains(preview, `"UserPromptSubmit"`) || strings.Contains(preview, "statusLine") {
		t.Errorf("output = %q", out)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Errorf("settings changed: %s", after)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("dry run left files: %v", entries)
	}
}

// jalonSkills are the skill files init writes, with their content for testExe,
// in the home withExecutable set.
func jalonSkills(t *testing.T) []client.Skill {
	t.Helper()
	skills, err := claudecode.Adapter{}.Skills(testExe)
	if err != nil || len(skills) != 5 {
		t.Fatalf("skills = %d, %v", len(skills), err)
	}
	return skills
}

// retiredSkill is the 0.2.0 skill file, in the home withExecutable set.
func retiredSkill(t *testing.T) string {
	t.Helper()
	paths, err := claudecode.Adapter{}.RetiredSkills()
	if err != nil || len(paths) != 1 {
		t.Fatalf("retired = %v, %v", paths, err)
	}
	return paths[0]
}

func writeSkill(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// checkSkills fails unless every skill file holds its content for testExe.
func checkSkills(t *testing.T, skills []client.Skill) {
	t.Helper()
	for _, sk := range skills {
		if b, err := os.ReadFile(sk.Path); err != nil || !bytes.Equal(b, sk.Content) {
			t.Errorf("%s = %s, %v", sk.Path, b, err)
		}
	}
}

const (
	foreignSkill = "---\nname: jalon-help\ndescription: someone else's\n---\nMine.\n"
	oldSkill     = "---\nname: jalon\n---\n<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->\nOld.\n"
)

func TestInitSkill(t *testing.T) {
	withExecutable(t, testExe)
	settings := filepath.Join(t.TempDir(), "settings.json")
	skills := jalonSkills(t)
	// A plain init installs them.
	out := runInitOK(t, "-settings", settings)
	for _, sk := range skills {
		if !strings.Contains(out, "Wrote "+sk.Path+".") {
			t.Errorf("output lacks %s:\n%s", sk.Path, out)
		}
		if info, _ := os.Stat(sk.Path); info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", sk.Path, info.Mode().Perm())
		}
		if info, _ := os.Stat(filepath.Dir(sk.Path)); info.Mode().Perm() != 0o700 {
			t.Errorf("%s folder mode = %v, want 0700", sk.Path, info.Mode().Perm())
		}
	}
	checkSkills(t, skills)

	// A rerun keeps them and writes nothing, as does one with -skill=false.
	for _, args := range [][]string{{}, {"-skill=false"}} {
		out = runInitOK(t, append([]string{"-settings", settings}, args...)...)
		for _, sk := range skills {
			if !strings.Contains(out, sk.Path+": nothing to change.") {
				t.Errorf("rerun %v output lacks %s:\n%s", args, sk.Path, out)
			}
			if _, err := os.Stat(sk.Path + ".jalon.bak"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the rerun %v wrote a backup of %s", args, sk.Path)
			}
		}
	}
}

func TestInitReportSkillNamesTheBinary(t *testing.T) {
	withExecutable(t, testExe)
	runInitOK(t, "-settings", filepath.Join(t.TempDir(), "settings.json"), "-skill")
	b, _ := os.ReadFile(jalonSkills(t)[3].Path)
	if !strings.Contains(string(b), "`"+testExe+" context-report $ARGUMENTS`") || strings.Contains(string(b), "{jalon}") {
		t.Errorf("report skill = %s", b)
	}
}

func TestInitUpgradesTheSingleSkill(t *testing.T) {
	withExecutable(t, testExe)
	old := retiredSkill(t)
	writeSkill(t, old, oldSkill)
	out := runInitOK(t, "-settings", filepath.Join(t.TempDir(), "settings.json"))
	checkSkills(t, jalonSkills(t))
	if want := "skill: removed " + old + " and its folder, replaced by the commands above"; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if _, err := os.Stat(filepath.Dir(old)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old skill folder left: %v", err)
	}
}

func TestInitUpdatesTheSkills(t *testing.T) {
	withExecutable(t, testExe)
	skills := jalonSkills(t)
	writeSkill(t, skills[0].Path, oldSkill)
	settings := filepath.Join(t.TempDir(), "settings.json")
	out := runInitOK(t, "-settings", settings)
	if !strings.Contains(out, "Wrote "+skills[0].Path+".\n") {
		t.Errorf("output = %q", out)
	}
	checkSkills(t, skills)
	// The skills are Jalon's alone: no backup, so uninstall leaves no folder.
	if _, err := os.Stat(skills[0].Path + ".jalon.bak"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup written: %v", err)
	}
	runInitOK(t, "-settings", settings, "-uninstall")
	if entries, err := os.ReadDir(filepath.Dir(filepath.Dir(skills[0].Path))); err != nil || len(entries) != 0 {
		t.Errorf("skills folder after uninstall = %v, %v", entries, err)
	}
}

func TestInitWithoutTheSkill(t *testing.T) {
	withExecutable(t, testExe)
	if out := runInitOK(t, "-settings", filepath.Join(t.TempDir(), "settings.json"), "-skill=false"); strings.Contains(out, "skill") {
		t.Errorf("output = %q", out)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(jalonSkills(t)[0].Path))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("skills folder created: %v", err)
	}
}

func TestInitLeavesSomeoneElsesSkill(t *testing.T) {
	for _, args := range [][]string{{}, {"-skill=false"}, {"-uninstall"}} {
		t.Run(strings.Join(append([]string{"init"}, args...), " "), func(t *testing.T) {
			withExecutable(t, testExe)
			skills := jalonSkills(t)
			help := skills[4].Path
			writeSkill(t, help, foreignSkill)
			// Someone else's skill named jalon is never taken for the 0.2.0 one.
			writeSkill(t, retiredSkill(t), foreignSkill)
			settings := filepath.Join(t.TempDir(), "settings.json")
			out := runInitOK(t, append([]string{"-settings", settings}, args...)...)
			switch {
			case len(args) == 0:
				if want := "skill: " + help + " is in the way, a skill not written by Jalon; left as is"; !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
				checkSkills(t, skills[:4])
			case args[0] == "-skill=false":
				// No skill of Jalon's is there, so none is written.
				if _, err := os.Stat(skills[0].Path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("skill written: %v", err)
				}
			case args[0] == "-uninstall":
				if want := "skill: " + help + " not written by Jalon, left as is"; !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if len(args) == 0 || args[0] != "-uninstall" {
				if b, _ := os.ReadFile(settings); !strings.Contains(string(b), "hook prompt") {
					t.Errorf("settings = %s", b)
				}
			}
			for _, path := range []string{help, retiredSkill(t)} {
				if b, _ := os.ReadFile(path); string(b) != foreignSkill {
					t.Errorf("%s = %s", path, b)
				}
			}
		})
	}
}

func TestInitUninstallRemovesTheSkills(t *testing.T) {
	withExecutable(t, testExe)
	settings := filepath.Join(t.TempDir(), "settings.json")
	skills := jalonSkills(t)
	runInitOK(t, "-settings", settings, "-skill")
	old := retiredSkill(t)
	writeSkill(t, old, oldSkill)

	out := runInitOK(t, "-settings", settings, "-uninstall", "-dry-run")
	for _, path := range []string{skills[0].Path, skills[4].Path, old} {
		if !strings.Contains(out, "skill: would remove "+path) {
			t.Errorf("dry run output lacks %s:\n%s", path, out)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the dry run removed %s: %v", path, err)
		}
	}
	out = runInitOK(t, "-settings", settings, "-uninstall")
	for _, path := range []string{skills[0].Path, skills[4].Path, old} {
		if !strings.Contains(out, "skill: removed "+path+" and its folder") {
			t.Errorf("output lacks %s:\n%s", path, out)
		}
	}
	if entries, err := os.ReadDir(filepath.Dir(old)); err == nil {
		t.Errorf("skills left: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Dir(filepath.Dir(old))); len(entries) != 0 {
		t.Errorf("skills left: %v", entries)
	}
	if out := runInitOK(t, "-settings", settings, "-uninstall"); !strings.Contains(out, "skills: not installed") {
		t.Errorf("second uninstall output = %q", out)
	}
}

func TestInitSkillDryRun(t *testing.T) {
	withExecutable(t, testExe)
	out := runInitOK(t, "-settings", filepath.Join(t.TempDir(), "settings.json"), "-skill", "-dry-run")
	for _, sk := range jalonSkills(t) {
		if !strings.Contains(out, "Dry run, "+sk.Path+" not written. It would become:\n"+string(sk.Content)) {
			t.Errorf("output lacks %s:\n%s", sk.Path, out)
		}
		if _, err := os.Stat(filepath.Dir(sk.Path)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("skill folder created: %v", err)
		}
	}
}
