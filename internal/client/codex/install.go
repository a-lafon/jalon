package codex

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

var _ client.Installer = Adapter{}

// SettingsPath is the user hooks file, in CODEX_HOME when it is set, as Codex
// itself reads it.
func (Adapter) SettingsPath() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "hooks.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "hooks.json"), nil
}

// Install adds Jalon's hooks; hooks.json has the shape of Claude Code's hooks.
func (Adapter) Install(settings []byte, exe string, display client.Display) ([]byte, []string, error) {
	out, actions, err := hookjson.InstallHooks(settings, exe, "codex")
	if err != nil {
		return nil, nil, err
	}
	if display == client.StatusLine {
		actions = append(actions, "status line: none, Codex's status line runs no command; its context-used item shows the size")
	}
	if !bytes.Equal(out, settings) {
		actions = append(actions, "Codex runs a new or changed hook only once trusted: open /hooks in Codex and trust Jalon's")
	}
	return out, actions, nil
}

func (Adapter) Uninstall(settings []byte) ([]byte, []string, error) {
	return hookjson.UninstallHooks(settings)
}
