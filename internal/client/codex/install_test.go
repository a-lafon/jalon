package codex

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
)

const exe = "/opt/homebrew/bin/jalon"

type hooksFile struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// commands lists the commands of hooks.<event>, group by group.
func commands(t *testing.T, raw []byte, event string) []string {
	t.Helper()
	var f hooksFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	var out []string
	for _, g := range f.Hooks[event] {
		for _, h := range g.Hooks {
			out = append(out, h.Command)
		}
	}
	return out
}

func hasAction(actions []string, prefix string) bool {
	return slices.ContainsFunc(actions, func(a string) bool { return strings.HasPrefix(a, prefix) })
}

func TestInstallEmptyHooksFile(t *testing.T) {
	out, actions, err := Adapter{}.Install(nil, exe, client.StatusLine)
	if err != nil {
		t.Fatal(err)
	}
	if got := commands(t, out, "UserPromptSubmit"); !slices.Equal(got, []string{exe + " hook prompt --client codex"}) {
		t.Errorf("prompt hook = %q", got)
	}
	if got := commands(t, out, "Stop"); !slices.Equal(got, []string{exe + " hook stop --client codex"}) {
		t.Errorf("stop hook = %q", got)
	}
	if strings.Contains(string(out), "statusLine") {
		t.Errorf("a status line in the hooks file:\n%s", out)
	}
	if !hasAction(actions, "Codex runs a new or changed hook only once trusted") || !hasAction(actions, "status line: none") {
		t.Errorf("actions = %q", actions)
	}

	// A rerun changes nothing, so there is nothing to trust again.
	again, actions, err := Adapter{}.Install(out, exe, client.NoDisplay)
	if err != nil || string(again) != string(out) {
		t.Fatalf("rerun changed the file: %v\n%s", err, again)
	}
	if hasAction(actions, "Codex runs") || hasAction(actions, "status line") {
		t.Errorf("rerun actions = %q", actions)
	}
}

func TestUninstallKeepsOtherHooks(t *testing.T) {
	settings := []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/bin/notify.sh"}]}]}}`)
	installed, _, err := Adapter{}.Install(settings, exe, client.NoDisplay)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := Adapter{}.Uninstall(installed)
	if err != nil {
		t.Fatal(err)
	}
	if got := commands(t, out, "Stop"); !slices.Equal(got, []string{"~/bin/notify.sh"}) {
		t.Errorf("stop hooks = %q", got)
	}
	if got := commands(t, out, "UserPromptSubmit"); got != nil {
		t.Errorf("prompt hooks = %q", got)
	}
}

func TestSettingsPath(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("CODEX_HOME", "")
	if got, _ := (Adapter{}).SettingsPath(); got != filepath.Join("/home/u", ".codex", "hooks.json") {
		t.Errorf("default path = %s", got)
	}
	t.Setenv("CODEX_HOME", "/srv/codex")
	if got, _ := (Adapter{}).SettingsPath(); got != filepath.Join("/srv/codex", "hooks.json") {
		t.Errorf("CODEX_HOME path = %s", got)
	}
}
