package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Jalon makes no network connection: the import graph of the binary holds no
// package that opens one. net/url and net/netip parse addresses and do no I/O.
func TestImportGraphHasNoNetwork(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		isNet := pkg == "net" || strings.HasPrefix(pkg, "net/") || pkg == "crypto/tls" || strings.HasPrefix(pkg, "crypto/tls/")
		if isNet && pkg != "net/url" && pkg != "net/netip" {
			t.Errorf("cmd/jalon depends on %s", pkg)
		}
	}
}

func TestUnknownCommandPrintsTheUsage(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "jalon")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "frobnicate").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("err = %v, want exit status 2", err)
	}
	if want := "jalon: unknown command " + strconv.Quote("frobnicate") + "\n\n" + usage; string(out) != want {
		t.Errorf("printed %q, want %q", out, want)
	}
}

// tree lists what exists under root, as paths relative to it: directories end
// with a slash.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			rel += "/"
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// created lists what exists under root now and did not in before.
func created(t *testing.T, root string, before []string) []string {
	t.Helper()
	var added []string
	for _, p := range tree(t, root) {
		if !slices.Contains(before, p) {
			added = append(added, p)
		}
	}
	return added
}

func expectFiles(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s created %v, want exactly %v", what, got, want)
	}
}

func TestHookBelowTheThresholdWritesOnlyTheSessionState(t *testing.T) {
	_, cfg := hookState(t, "s1", 100_000)
	home, repo := os.Getenv("HOME"), gitRepo(t)
	homeBefore, repoBefore := tree(t, home), tree(t, repo)
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got != "" {
		t.Fatalf("output = %q, want no advice", got)
	}
	expectFiles(t, "the hook in the home", created(t, home, homeBefore), ".jalon/", ".jalon/sessions/", ".jalon/sessions/s1.json")
	expectFiles(t, "the hook in the project", created(t, repo, repoBefore))
}

func TestHookAdvisingWritesTheStateAndTheHandoffFolder(t *testing.T) {
	_, cfg := hookState(t, "s1", 212_000)
	home, repo := os.Getenv("HOME"), gitRepo(t)
	homeBefore, repoBefore := tree(t, home), tree(t, repo)
	if got := runPrompt(t, cfg, hookInput("s1", repo)); got == "" {
		t.Fatal("no advice")
	}
	expectFiles(t, "the hook in the home", created(t, home, homeBefore), ".jalon/", ".jalon/sessions/", ".jalon/sessions/s1.json")
	expectFiles(t, "the hook in the project", created(t, repo, repoBefore),
		".jalon/", ".jalon/.gitignore", ".jalon/handoffs/", ".jalon/handoffs/2026-10-12/")
}

func TestStatuslineWritesNothing(t *testing.T) {
	useHome(t)
	home, repo := os.Getenv("HOME"), gitRepo(t)
	homeBefore, repoBefore := tree(t, home), tree(t, repo)
	if got := runStatus(t, configWith(t, ""), statusInput("s1", 230_000)); got == "" {
		t.Fatal("no output")
	}
	expectFiles(t, "the status line in the home", created(t, home, homeBefore))
	expectFiles(t, "the status line in the project", created(t, repo, repoBefore))
}

func TestInitInAFreshHomeWritesTheSettingsAndTheConfig(t *testing.T) {
	withExecutable(t, testExe)
	home, _ := os.UserHomeDir()
	runInitOK(t, "-statusline", "-advise-tokens", "200000")
	expectFiles(t, "init", tree(t, home), ".claude/", ".claude/settings.json", ".jalon/", ".jalon/config.yaml")
}

func TestInitOverExistingFilesAddsOnlyTheirBackups(t *testing.T) {
	withExecutable(t, testExe)
	home, _ := os.UserHomeDir()
	for path, content := range map[string]string{
		".claude/settings.json": `{"model": "opus"}`,
		".jalon/config.yaml":    "context:\n  advise_tokens: 100000\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := tree(t, home)
	runInitOK(t, "-statusline", "-advise-tokens", "200000")
	expectFiles(t, "init", created(t, home, before), ".claude/settings.json.jalon.bak", ".jalon/config.yaml.jalon.bak")
}

func TestStopHookAdvisingWritesTheStateAndTheHandoffFolder(t *testing.T) {
	_, cfg := hookState(t, "s1", 100_000)
	repo := gitRepo(t)
	runPrompt(t, cfg, promptInput("s1", "p1", repo))
	appendTranscript(t, "s1", assistantLine(212_000))
	home := os.Getenv("HOME")
	homeBefore, repoBefore := tree(t, home), tree(t, repo)
	if got := runStop(t, cfg, stopInput("s1", "p1", repo, "Done.", false)); got == "" {
		t.Fatal("no advice")
	}
	// The state file existed already: the advice changes it and creates nothing else.
	expectFiles(t, "the stop hook in the home", created(t, home, homeBefore))
	expectFiles(t, "the stop hook in the project", created(t, repo, repoBefore),
		".jalon/", ".jalon/.gitignore", ".jalon/handoffs/", ".jalon/handoffs/2026-10-12/")
}
