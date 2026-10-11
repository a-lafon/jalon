package claudecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/a-lafon/jalon/internal/client"
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

var (
	hookWords       = []string{"hook", "prompt"}
	statusLineWords = []string{"statusline"}
)

func (Adapter) Install(settings []byte, exe string, display client.Display) ([]byte, []string, error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	if err := checkExe(exe); err != nil {
		return nil, nil, err
	}
	var actions []string
	hookCmd := quoteExe(exe) + " hook prompt --client claude-code"
	found, changed, err := editPromptHooks(&root, func(cmd string) (string, bool) {
		next := withExe(cmd, exe)
		if next != cmd {
			actions = append(actions, fmt.Sprintf("prompt hook: updated %s -> %s", cmd, next))
		}
		return next, true
	})
	if err != nil {
		return nil, nil, err
	}
	switch {
	case found == 0:
		if err := appendPromptHook(&root, hookCmd); err != nil {
			return nil, nil, err
		}
		changed = true
		actions = append(actions, "prompt hook: added "+hookCmd)
	case found > 1:
		actions = append(actions, fmt.Sprintf("prompt hook: %d Jalon entries, each one gives the advice: remove the extra ones by hand", found))
	case !changed:
		actions = append(actions, "prompt hook: already installed")
	}

	switch display {
	case client.Mod:
		if removeStatusLine(&root) {
			changed = true
			actions = append(actions, "status line: removed, the mod draws the pole")
		}
	case client.StatusLine:
		cmd := quoteExe(exe) + " statusline --client claude-code"
		raw, ok := root.get("statusLine")
		current := ""
		if ok {
			if sl, err := parseObject(raw); err == nil {
				current = sl.str("command")
			}
		}
		switch {
		case !ok:
			root.set("statusLine", json.RawMessage(`{"type":"command","command":`+jsonString(cmd)+`}`))
			changed = true
			actions = append(actions, "status line: added "+cmd)
		case isJalon(current, statusLineWords):
			if next := withExe(current, exe); next != current {
				sl, _ := parseObject(raw)
				sl.set("command", json.RawMessage(jsonString(next)))
				root.set("statusLine", sl.marshal())
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
	out, err := format(root)
	return out, actions, err
}

func (Adapter) Uninstall(settings []byte) ([]byte, []string, error) {
	root, err := parseObject(settings)
	if err != nil {
		return nil, nil, err
	}
	var actions []string
	found, changed, err := editPromptHooks(&root, func(cmd string) (string, bool) {
		actions = append(actions, "prompt hook: removed "+cmd)
		return "", false
	})
	if err != nil {
		return nil, nil, err
	}
	if found == 0 {
		actions = append(actions, "prompt hook: not installed")
	}
	if raw, ok := root.get("statusLine"); ok {
		if sl, err := parseObject(raw); err == nil && isJalon(sl.str("command"), statusLineWords) {
			removeStatusLine(&root)
			changed = true
			actions = append(actions, "status line: removed "+sl.str("command"))
		}
	}
	if !changed {
		return settings, actions, nil
	}
	out, err := format(root)
	return out, actions, err
}

// removeStatusLine removes the status line when it is Jalon's, and tells
// whether it did; another status line is left as it is.
func removeStatusLine(root *object) bool {
	raw, ok := root.get("statusLine")
	if !ok {
		return false
	}
	sl, err := parseObject(raw)
	if err != nil || !isJalon(sl.str("command"), statusLineWords) {
		return false
	}
	root.remove("statusLine")
	return true
}

// editPromptHooks calls edit for each Jalon hook of hooks.UserPromptSubmit,
// which returns the hook's new command, or false to drop the hook. Groups,
// arrays and the hooks object that dropping leaves empty are removed. Only the
// objects that change are rebuilt.
func editPromptHooks(root *object, edit func(cmd string) (string, bool)) (found int, changed bool, err error) {
	hooksRaw, ok := root.get("hooks")
	if !ok {
		return 0, false, nil
	}
	hooks, err := parseObject(hooksRaw)
	if err != nil {
		return 0, false, fmt.Errorf("hooks: %w", err)
	}
	groupsRaw, ok := hooks.get("UserPromptSubmit")
	if !ok {
		return 0, false, nil
	}
	var groups []json.RawMessage
	if err := json.Unmarshal(groupsRaw, &groups); err != nil {
		return 0, false, fmt.Errorf("hooks.UserPromptSubmit: %w", err)
	}
	keptGroups := groups[:0:0]
	for _, g := range groups {
		group, err := parseObject(g)
		if err != nil {
			return 0, false, fmt.Errorf("hooks.UserPromptSubmit: %w", err)
		}
		var list []json.RawMessage
		if raw, ok := group.get("hooks"); ok {
			if err := json.Unmarshal(raw, &list); err != nil {
				return 0, false, fmt.Errorf("hooks.UserPromptSubmit: %w", err)
			}
		}
		groupChanged := false
		kept := list[:0:0]
		for _, h := range list {
			hook, err := parseObject(h)
			if err != nil {
				return 0, false, fmt.Errorf("hooks.UserPromptSubmit: %w", err)
			}
			cmd := hook.str("command")
			if !isJalon(cmd, hookWords) {
				kept = append(kept, h)
				continue
			}
			found++
			next, keep := edit(cmd)
			switch {
			case !keep:
				groupChanged = true
			case next != cmd:
				hook.set("command", json.RawMessage(jsonString(next)))
				kept = append(kept, hook.marshal())
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
		group.set("hooks", marshalArray(kept))
		keptGroups = append(keptGroups, group.marshal())
	}
	if !changed {
		return found, false, nil
	}
	if len(keptGroups) == 0 {
		hooks.remove("UserPromptSubmit")
	} else {
		hooks.set("UserPromptSubmit", marshalArray(keptGroups))
	}
	if len(hooks) == 0 {
		root.remove("hooks")
	} else {
		root.set("hooks", hooks.marshal())
	}
	return found, true, nil
}

// appendPromptHook adds a group running cmd after the existing ones.
func appendPromptHook(root *object, cmd string) error {
	hooks := object{}
	if raw, ok := root.get("hooks"); ok {
		var err error
		if hooks, err = parseObject(raw); err != nil {
			return fmt.Errorf("hooks: %w", err)
		}
	}
	var groups []json.RawMessage
	if raw, ok := hooks.get("UserPromptSubmit"); ok {
		if err := json.Unmarshal(raw, &groups); err != nil {
			return fmt.Errorf("hooks.UserPromptSubmit: %w", err)
		}
	}
	groups = append(groups, json.RawMessage(`{"hooks":[{"type":"command","command":`+jsonString(cmd)+`,"timeout":2}]}`))
	hooks.set("UserPromptSubmit", marshalArray(groups))
	root.set("hooks", hooks.marshal())
	return nil
}

// isJalon tells whether a command runs Jalon's subcommand words: its
// executable's base name is jalon, whatever its path (bare, absolute, ~).
func isJalon(cmd string, words []string) bool {
	exe, rest := splitCommand(cmd)
	if filepath.Base(exe) != "jalon" {
		return false
	}
	fields := strings.Fields(rest)
	return len(fields) >= len(words) && slices.Equal(fields[:len(words)], words)
}

// withExe replaces the executable of cmd, keeping its arguments.
func withExe(cmd, exe string) string {
	_, rest := splitCommand(cmd)
	if rest == "" {
		return quoteExe(exe)
	}
	return quoteExe(exe) + " " + rest
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

// checkExe refuses a path that cannot be written safely in a shell command: one
// with a single quote or a control character.
func checkExe(exe string) error {
	if strings.ContainsRune(exe, '\'') || strings.IndexFunc(exe, unicode.IsControl) >= 0 {
		return fmt.Errorf("the executable path %q holds a single quote or a control character, which cannot be put in a shell command; move jalon to another path", exe)
	}
	return nil
}

// quoteExe leaves a path of plain characters bare and wraps any other in single
// quotes, inside which the shell expands nothing. exe must pass checkExe.
func quoteExe(exe string) string {
	if !strings.ContainsFunc(exe, func(r rune) bool { return !isPlainPathRune(r) }) {
		return exe
	}
	return "'" + exe + "'"
}

func isPlainPathRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._+@:,-", r)
}

// object is a JSON object that keeps its keys in order and its values as they
// were written, so editing settings does not reorder or rewrite the user's file.
type object []member

type member struct {
	key   string
	value json.RawMessage
}

// parseObject reads a JSON object; empty input is an empty object.
func parseObject(raw []byte) (object, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return object{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil {
		return nil, err
	} else if tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		o = append(o, member{key: tok.(string), value: v})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return o, nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// str is the value of key when it is a string, else "".
func (o object) str(key string) string {
	var s string
	if raw, ok := o.get(key); ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// set replaces the value of key in place, or appends key.
func (o *object) set(key string, v json.RawMessage) {
	for i, m := range *o {
		if m.key == key {
			(*o)[i].value = v
			return
		}
	}
	*o = append(*o, member{key: key, value: v})
}

func (o *object) remove(key string) {
	*o = slices.DeleteFunc(*o, func(m member) bool { return m.key == key })
}

func (o object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(m.key))
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func marshalArray(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(item)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// jsonString encodes s as a JSON string, leaving <, > and & as they are.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// format indents the settings with two spaces, as Claude Code writes them.
func format(o object) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, o.marshal(), "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}
