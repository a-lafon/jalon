package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

var _ client.Installer = Adapter{}

// SettingsPath is the user settings file, where hooks apply to every project.
func (Adapter) SettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

var statusLineWords = []string{"statusline"}

func (Adapter) Install(settings []byte, exe string, display client.Display) ([]byte, []string, error) {
	root, err := hookjson.ParseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	if err := hookjson.CheckExe(exe); err != nil {
		return nil, nil, err
	}
	changed, actions, err := hookjson.AddHooks(&root, exe, "claude-code")
	if err != nil {
		return nil, nil, err
	}

	switch display {
	case client.Mod:
		if removeStatusLine(&root) {
			changed = true
			actions = append(actions, "status line: removed, the mod draws the pole")
		}
	case client.StatusLine:
		cmd := hookjson.QuoteExe(exe) + " statusline --client claude-code"
		raw, ok := root.Get("statusLine")
		current := ""
		if ok {
			if sl, err := hookjson.ParseObject(raw); err == nil {
				current = sl.Str("command")
			}
		}
		switch {
		case !ok:
			root.Set("statusLine", json.RawMessage(`{"type":"command","command":`+hookjson.JSONString(cmd)+`}`))
			changed = true
			actions = append(actions, "status line: added "+cmd)
		case hookjson.IsJalon(current, statusLineWords):
			if next := hookjson.WithExe(current, exe); next != current {
				sl, _ := hookjson.ParseObject(raw)
				sl.Set("command", json.RawMessage(hookjson.JSONString(next)))
				root.Set("statusLine", sl.Marshal())
				changed = true
				actions = append(actions, fmt.Sprintf("status line: updated %s -> %s", current, next))
			} else {
				actions = append(actions, "status line: already installed")
			}
		default:
			actions = append(actions, "status line: left unchanged, it runs another command; to add the Jalon segment, see \"Status line\" in docs/how-it-works.md")
		}
	}

	if !changed {
		return settings, actions, nil
	}
	out, err := hookjson.Format(root)
	return out, actions, err
}

func (Adapter) Uninstall(settings []byte) ([]byte, []string, error) {
	root, err := hookjson.ParseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	changed, actions, err := hookjson.RemoveHooks(&root)
	if err != nil {
		return nil, nil, err
	}
	if raw, ok := root.Get("statusLine"); ok {
		if sl, err := hookjson.ParseObject(raw); err == nil && hookjson.IsJalon(sl.Str("command"), statusLineWords) {
			removeStatusLine(&root)
			changed = true
			actions = append(actions, "status line: removed "+sl.Str("command"))
		}
	}
	if !changed {
		return settings, actions, nil
	}
	out, err := hookjson.Format(root)
	return out, actions, err
}

// removeStatusLine removes the status line when it is Jalon's, and tells
// whether it did; another status line is left as it is.
func removeStatusLine(root *hookjson.Object) bool {
	raw, ok := root.Get("statusLine")
	if !ok {
		return false
	}
	sl, err := hookjson.ParseObject(raw)
	if err != nil || !hookjson.IsJalon(sl.Str("command"), statusLineWords) {
		return false
	}
	root.Remove("statusLine")
	return true
}
