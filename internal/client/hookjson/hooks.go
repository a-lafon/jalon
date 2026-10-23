// Package hookjson is what clients that share Claude Code's hook format have in
// common: the "hooks" object of their settings, which jalon init edits, and the
// JSON their hooks print. Claude Code and Codex both use it.
package hookjson

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// hookKinds are Jalon's hooks: the settings event each runs on, its name in
// the actions, and the words that follow the executable in its command.
var hookKinds = []struct {
	event, name string
	words       []string
}{
	{"UserPromptSubmit", "prompt hook", []string{"hook", "prompt"}},
	{"Stop", "stop hook", []string{"hook", "stop"}},
}

// InstallHooks returns settings, a JSON object with a "hooks" object, with
// Jalon's prompt and stop hooks running exe --client clientName, and what it
// did. Content with nothing to change is returned unchanged.
func InstallHooks(settings []byte, exe, clientName string) ([]byte, []string, error) {
	root, err := ParseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	if err := CheckExe(exe); err != nil {
		return nil, nil, err
	}
	changed, actions, err := AddHooks(&root, exe, clientName)
	if err != nil {
		return nil, nil, err
	}
	if !changed {
		return settings, actions, nil
	}
	out, err := Format(root)
	return out, actions, err
}

// UninstallHooks returns settings without Jalon's prompt and stop hooks, as
// InstallHooks wrote them.
func UninstallHooks(settings []byte) ([]byte, []string, error) {
	root, err := ParseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	changed, actions, err := RemoveHooks(&root)
	if err != nil {
		return nil, nil, err
	}
	if !changed {
		return settings, actions, nil
	}
	out, err := Format(root)
	return out, actions, err
}

// AddHooks adds or updates Jalon's hooks in root, running exe, which must
// pass CheckExe, with --client clientName, and tells whether root changed.
func AddHooks(root *Object, exe, clientName string) (bool, []string, error) {
	var actions []string
	changed := false
	for _, k := range hookKinds {
		hookCmd := QuoteExe(exe) + " " + strings.Join(k.words, " ") + " --client " + clientName
		found, edited, err := editHooks(root, k.event, k.words, func(cmd string) (string, bool) {
			next := WithExe(cmd, exe)
			if next != cmd {
				actions = append(actions, fmt.Sprintf("%s: updated %s -> %s", k.name, cmd, next))
			}
			return next, true
		})
		if err != nil {
			return false, nil, err
		}
		changed = changed || edited
		switch {
		case found == 0:
			if err := appendHook(root, k.event, hookCmd); err != nil {
				return false, nil, err
			}
			changed = true
			actions = append(actions, k.name+": added "+hookCmd)
		case found > 1:
			actions = append(actions, fmt.Sprintf("%s: %d Jalon entries, each one runs: remove the extra ones by hand", k.name, found))
		case !edited:
			actions = append(actions, k.name+": already installed")
		}
	}
	return changed, actions, nil
}

// RemoveHooks removes every Jalon hook from root, and tells whether root
// changed.
func RemoveHooks(root *Object) (bool, []string, error) {
	var actions []string
	changed := false
	for _, k := range hookKinds {
		found, edited, err := editHooks(root, k.event, k.words, func(cmd string) (string, bool) {
			actions = append(actions, k.name+": removed "+cmd)
			return "", false
		})
		if err != nil {
			return false, nil, err
		}
		changed = changed || edited
		if found == 0 {
			actions = append(actions, k.name+": not installed")
		}
	}
	return changed, actions, nil
}

// editHooks calls edit for each Jalon hook of hooks.<event> whose command
// runs words, which returns the hook's new command, or false to drop the hook.
// Groups, arrays and the hooks object that dropping leaves empty are removed.
// Only the objects that change are rebuilt.
func editHooks(root *Object, event string, words []string, edit func(cmd string) (string, bool)) (found int, changed bool, err error) {
	hooksRaw, ok := root.Get("hooks")
	if !ok {
		return 0, false, nil
	}
	hooks, err := ParseObject(hooksRaw)
	if err != nil {
		return 0, false, fmt.Errorf("hooks: %w", err)
	}
	groupsRaw, ok := hooks.Get(event)
	if !ok {
		return 0, false, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(groupsRaw, &groups); err != nil {
		return 0, false, fmt.Errorf("hooks.%s: %w", event, err)
	}
	keptGroups := groups[:0:0]
	for _, g := range groups {
		group, err := ParseObject(g)
		if err != nil {
			return 0, false, fmt.Errorf("hooks.%s: %w", event, err)
		}
		var list []json.RawMessage
		if raw, ok := group.Get("hooks"); ok {
			if err := json.Unmarshal(raw, &list); err != nil {
				return 0, false, fmt.Errorf("hooks.%s: %w", event, err)
			}
		}
		groupChanged := false
		kept := list[:0:0]
		for _, h := range list {
			hook, err := ParseObject(h)
			if err != nil {
				return 0, false, fmt.Errorf("hooks.%s: %w", event, err)
			}
			cmd := hook.Str("command")
			if !IsJalon(cmd, words) {
				kept = append(kept, h)
				continue
			}
			found++
			next, keep := edit(cmd)
			switch {
			case !keep:
				groupChanged = true
			case next != cmd:
				hook.Set("command", json.RawMessage(JSONString(next)))
				kept = append(kept, hook.Marshal())
				groupChanged = true
			default:
				kept = append(kept, h)
			}
		}
		if !groupChanged {
			keptGroups = append(keptGroups, g)
			continue
		}
		changed = true
		if len(kept) == 0 {
			continue
		}
		group.Set("hooks", marshalArray(kept))
		keptGroups = append(keptGroups, group.Marshal())
	}
	if !changed {
		return found, false, nil
	}
	if len(keptGroups) == 0 {
		hooks.Remove(event)
	} else {
		hooks.Set(event, marshalArray(keptGroups))
	}
	if len(hooks) == 0 {
		root.Remove("hooks")
	} else {
		root.Set("hooks", hooks.Marshal())
	}
	return found, true, nil
}

// appendHook adds a group running cmd after the existing ones of hooks.<event>.
func appendHook(root *Object, event, cmd string) error {
	hooks := Object{}
	if raw, ok := root.Get("hooks"); ok {
		var err error
		if hooks, err = ParseObject(raw); err != nil {
			return fmt.Errorf("hooks: %w", err)
		}
	}
	var groups []json.RawMessage
	if raw, ok := hooks.Get(event); ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return fmt.Errorf("hooks.%s: %w", event, err)
		}
	}
	groups = append(groups, json.RawMessage(`{"hooks":[{"type":"command","command":`+JSONString(cmd)+`,"timeout":2}]}`))
	hooks.Set(event, marshalArray(groups))
	root.Set("hooks", hooks.Marshal())
	return nil
}

// IsJalon tells whether a command runs Jalon's subcommand words: its
// executable's base name is jalon, whatever its path (bare, absolute, ~).
func IsJalon(cmd string, words []string) bool {
	exe, rest := splitCommand(cmd)
	if filepath.Base(exe) != "jalon" {
		return false
	}
	fields := strings.Fields(rest)
	return len(fields) >= len(words) && slices.Equal(fields[:len(words)], words)
}

// WithExe replaces the executable of cmd, keeping its arguments.
func WithExe(cmd, exe string) string {
	_, rest := splitCommand(cmd)
	if rest == "" {
		return QuoteExe(exe)
	}
	return QuoteExe(exe) + " " + rest
}

// splitCommand returns the first word of a shell command, without its quotes,
// and the rest.
func splitCommand(cmd string) (exe, rest string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", ""
	}
	if q := cmd[0]; q == '"' || q == '\'' {
		end := strings.IndexByte(cmd[1:], q)
		if end < 0 {
			return "", ""
		}
		return cmd[1 : end+1], strings.TrimLeftFunc(cmd[end+2:], unicode.IsSpace)
	}
	if i := strings.IndexFunc(cmd, unicode.IsSpace); i >= 0 {
		return cmd[:i], strings.TrimLeftFunc(cmd[i:], unicode.IsSpace)
	}
	return cmd, ""
}

// CheckExe refuses a path that cannot be written safely in a shell command: one
// with a single quote or a control character.
func CheckExe(exe string) error {
	if strings.ContainsRune(exe, '\'') || strings.IndexFunc(exe, unicode.IsControl) >= 0 {
		return fmt.Errorf("the executable path %q holds a single quote or a control character, which cannot be put in a shell command; move jalon to another path", exe)
	}
	return nil
}

// QuoteExe leaves a path of plain characters bare and wraps any other in single
// quotes, inside which the shell expands nothing. exe must pass CheckExe.
func QuoteExe(exe string) string {
	if !strings.ContainsFunc(exe, func(r rune) bool { return !isPlainPathRune(r) }) {
		return exe
	}
	return "'" + exe + "'"
}

func isPlainPathRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+@:,-", r)
}
