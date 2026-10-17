package claudecode

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

const exe = "/opt/homebrew/bin/jalon"

// promptCommands lists the commands of hooks.UserPromptSubmit, in order.
func promptCommands(t *testing.T, settings []byte) []string {
	t.Helper()
	return hookCommands(t, settings, "UserPromptSubmit")
}

// hookCommands lists the commands of hooks.<event>, in order.
func hookCommands(t *testing.T, settings []byte, event string) []string {
	t.Helper()
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, settings)
	}
	var cmds []string
	for _, g := range s.Hooks[event] {
		for _, h := range g.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds
}

func keys(t *testing.T, settings []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(settings))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var ks []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		ks = append(ks, tok.(string))
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	return ks
}

func install(t *testing.T, settings string, display client.Display) (string, []string) {
	t.Helper()
	out, actions, err := Adapter{}.Install([]byte(settings), exe, display)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), actions
}

func TestInstallEmpty(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "blank": " \n", "empty object": "{}"} {
		t.Run(name, func(t *testing.T) {
			out, _ := install(t, in, client.NoDisplay)
			want := `{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/opt/homebrew/bin/jalon hook prompt --client claude-code",
            "timeout": 2
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/opt/homebrew/bin/jalon hook stop --client claude-code",
            "timeout": 2
          }
        ]
      }
    ]
  }
}
`
			if out != want {
				t.Errorf("got\n%s\nwant\n%s", out, want)
			}
		})
	}
}

func TestInstallKeepsOtherKeysAndHooks(t *testing.T) {
	in := `{
  "env": {"A": "<b>&"},
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}],
    "UserPromptSubmit": [{"hooks": [{"type": "command", "command": "other-tool"}]}]
  },
  "model": "opus"
}`
	out, _ := install(t, in, client.NoDisplay)
	if got, want := keys(t, []byte(out)), []string{"env", "permissions", "hooks", "model"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	if got, want := promptCommands(t, []byte(out)), []string{"other-tool", exe + " hook prompt --client claude-code"}; !slices.Equal(got, want) {
		t.Errorf("prompt hooks = %v, want %v", got, want)
	}
	if !strings.Contains(out, `"rtk hook claude"`) || !strings.Contains(out, `"A": "<b>&"`) {
		t.Errorf("other values changed:\n%s", out)
	}
	hooks, _ := hookjson.ParseObject([]byte(out))
	raw, _ := hooks.Get("hooks")
	if got, want := keys(t, raw), []string{"PreToolUse", "UserPromptSubmit", "Stop"}; !slices.Equal(got, want) {
		t.Errorf("hooks keys = %v, want %v", got, want)
	}
}

func TestInstallIdempotent(t *testing.T) {
	once, _ := install(t, `{"model": "opus"}`, client.StatusLine)
	twice, actions := install(t, once, client.StatusLine)
	if twice != once {
		t.Errorf("second install changed the settings:\n%s", twice)
	}
	if want := []string{"prompt hook: already installed", "stop hook: already installed", "status line: already installed"}; !slices.Equal(actions, want) {
		t.Errorf("actions = %v, want %v", actions, want)
	}
	// Unchanged settings come back byte for byte, whatever their formatting.
	in := "{\"hooks\":{\"UserPromptSubmit\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"" + exe + " hook prompt\"}]}]," +
		"\"Stop\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"" + exe + " hook stop\"}]}]}}"
	if out, _ := install(t, in, client.NoDisplay); out != in {
		t.Errorf("unchanged settings were rewritten:\n%s", out)
	}
}

func TestInstallUpdatesExistingEntry(t *testing.T) {
	tests := map[string]struct{ cmd, want string }{
		"bare":            {"jalon hook prompt", exe + " hook prompt"},
		"absolute":        {"/Users/me/go/bin/jalon hook prompt --client claude-code", exe + " hook prompt --client claude-code"},
		"tilde, flags":    {"~/go/bin/jalon hook prompt -config /x.yaml", exe + " hook prompt -config /x.yaml"},
		"quoted, spaces":  {`"/Users/me/my tools/jalon" hook prompt`, exe + " hook prompt"},
		"extra blanks":    {"  jalon   hook  prompt", exe + " hook  prompt"},
		"single quoted":   {"'/a b/jalon' hook prompt", exe + " hook prompt"},
		"already current": {exe + " hook prompt", exe + " hook prompt"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			in := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":` + hookjson.JSONString(tt.cmd) + `,"timeout":5}]}]}}`
			out, _ := install(t, in, client.NoDisplay)
			if got := promptCommands(t, []byte(out)); !slices.Equal(got, []string{tt.want}) {
				t.Errorf("prompt hooks = %v, want [%s]", got, tt.want)
			}
			if !strings.Contains(out, `"timeout":5`) && !strings.Contains(out, `"timeout": 5`) {
				t.Errorf("timeout lost:\n%s", out)
			}
		})
	}
}

func TestInstallLeavesLookalikes(t *testing.T) {
	for _, cmd := range []string{"jalon statusline", "jalonx hook prompt", "/bin/jalon hook", "echo jalon hook prompt", `"/unclosed/jalon hook prompt`} {
		in := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":` + hookjson.JSONString(cmd) + `}]}]}}`
		out, _ := install(t, in, client.NoDisplay)
		if got := promptCommands(t, []byte(out)); len(got) != 2 || got[0] != cmd {
			t.Errorf("%q: prompt hooks = %v, want it kept and Jalon's added", cmd, got)
		}
	}
}

func TestInstallQuotesWhatTheShellWouldRead(t *testing.T) {
	tests := map[string]struct{ exe, want string }{
		"plain":     {"/opt/homebrew/bin/jalon", "/opt/homebrew/bin/jalon"},
		"plain too": {"/home/me/go_bin/v1.2+x@h:1,2-3/jalon", "/home/me/go_bin/v1.2+x@h:1,2-3/jalon"},
		"space":     {"/Users/me/my tools/jalon", "'/Users/me/my tools/jalon'"},
		"dollar":    {"/opt/$HOME/jalon", "'/opt/$HOME/jalon'"},
		"ampersand": {"/opt/a&b/jalon", "'/opt/a&b/jalon'"},
		"backtick":  {"/opt/a`id`/jalon", "'/opt/a`id`/jalon'"},
		"semicolon": {"/opt/a;b/jalon", "'/opt/a;b/jalon'"},
		"paren":     {"/opt/a(b)/jalon", "'/opt/a(b)/jalon'"},
		"double":    {`/opt/a"b/jalon`, `'/opt/a"b/jalon'`},
		"non-ASCII": {"/opt/é/jalon", "'/opt/é/jalon'"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			out, _, err := Adapter{}.Install(nil, tt.exe, client.StatusLine)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{tt.want + " hook prompt --client claude-code"}
			if got := promptCommands(t, out); !slices.Equal(got, want) {
				t.Errorf("prompt hooks = %v, want %v", got, want)
			}
			if !strings.Contains(string(out), hookjson.JSONString(tt.want+" statusline --client claude-code")) {
				t.Errorf("status line is not %s statusline:\n%s", tt.want, out)
			}
			if again, _, _ := (Adapter{}).Install(out, tt.exe, client.StatusLine); string(again) != string(out) {
				t.Error("the quoted path is not recognised as Jalon's")
			}
			moved, _, _ := Adapter{}.Install(out, exe, client.StatusLine)
			if got := promptCommands(t, moved); !slices.Equal(got, []string{exe + " hook prompt --client claude-code"}) {
				t.Errorf("after moving, prompt hooks = %v", got)
			}
		})
	}
}

func TestInstallRewritesADoubleQuotedEntryOfAnEarlierInstall(t *testing.T) {
	in := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"\"/opt/my tools/jalon\" hook prompt --client claude-code"}]}]},` +
		`"statusLine":{"type":"command","command":"\"/opt/my tools/jalon\" statusline --client claude-code"}}`
	out, _, err := Adapter{}.Install([]byte(in), "/opt/my tools/jalon", client.StatusLine)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"'/opt/my tools/jalon' hook prompt --client claude-code"}
	if got := promptCommands(t, out); !slices.Equal(got, want) {
		t.Errorf("prompt hooks = %v, want %v", got, want)
	}
	if strings.Contains(string(out), `\"/opt`) {
		t.Errorf("the double-quoted status line was kept:\n%s", out)
	}
	if got, _, _ := (Adapter{}).Uninstall(out); strings.Contains(string(got), "jalon") {
		t.Errorf("uninstall left Jalon behind:\n%s", got)
	}
}

// init adds "nothing written" itself, so the adapter's error must not.
func TestInstallRefusesAPathItCannotQuote(t *testing.T) {
	for name, path := range map[string]string{"single quote": "/opt/it's/jalon", "newline": "/opt/a\nb/jalon", "tab": "/opt/a\tb/jalon", "escape": "/opt/a\x1bb/jalon"} {
		t.Run(name, func(t *testing.T) {
			out, actions, err := Adapter{}.Install([]byte("{}"), path, client.StatusLine)
			if err == nil || !strings.Contains(err.Error(), "cannot be put in a shell command") {
				t.Fatalf("err = %v, want a refusal saying the path cannot be put in a shell command", err)
			}
			if out != nil || actions != nil {
				t.Errorf("out = %q, actions = %v, want nothing", out, actions)
			}
		})
	}
}

func TestInstallStatusLine(t *testing.T) {
	statusCmd := func(t *testing.T, out string) string {
		o, _ := hookjson.ParseObject([]byte(out))
		raw, ok := o.Get("statusLine")
		if !ok {
			return ""
		}
		sl, _ := hookjson.ParseObject(raw)
		return sl.Str("command")
	}
	t.Run("not requested", func(t *testing.T) {
		if out, _ := install(t, `{}`, client.NoDisplay); statusCmd(t, out) != "" {
			t.Error("status line installed with no display")
		}
	})
	t.Run("absent", func(t *testing.T) {
		out, _ := install(t, `{}`, client.StatusLine)
		if got, want := statusCmd(t, out), exe+" statusline --client claude-code"; got != want {
			t.Errorf("status line = %q, want %q", got, want)
		}
	})
	t.Run("Jalon's, other path", func(t *testing.T) {
		out, _ := install(t, `{"statusLine":{"type":"command","command":"~/go/bin/jalon statusline --client claude-code","padding":0}}`, client.StatusLine)
		if got, want := statusCmd(t, out), exe+" statusline --client claude-code"; got != want {
			t.Errorf("status line = %q, want %q", got, want)
		}
		if !strings.Contains(out, `"padding": 0`) {
			t.Errorf("other status line fields lost:\n%s", out)
		}
	})
	t.Run("another command", func(t *testing.T) {
		in := `{"statusLine":{"type":"command","command":"~/bin/my-status.sh"}}`
		out, actions := install(t, in, client.StatusLine)
		if got := statusCmd(t, out); got != "~/bin/my-status.sh" {
			t.Errorf("status line replaced: %q", got)
		}
		if !strings.Contains(strings.Join(actions, "\n"), "left unchanged") {
			t.Errorf("actions do not say it was left: %v", actions)
		}
	})
	t.Run("the mod removes Jalon's", func(t *testing.T) {
		out, actions := install(t, `{"statusLine":{"type":"command","command":"/x/jalon statusline --client claude-code"}}`, client.Mod)
		if got := statusCmd(t, out); got != "" {
			t.Errorf("status line kept with the mod: %q", got)
		}
		if !strings.Contains(strings.Join(actions, "\n"), "status line: removed, the mod draws the pole") {
			t.Errorf("actions = %v", actions)
		}
		if got := promptCommands(t, []byte(out)); len(got) != 1 {
			t.Errorf("prompt hooks = %v, want the hook installed", got)
		}
	})
	t.Run("the mod leaves another", func(t *testing.T) {
		in := `{"statusLine":{"type":"command","command":"~/bin/my-status.sh"}}`
		if out, _ := install(t, in, client.Mod); statusCmd(t, out) != "~/bin/my-status.sh" {
			t.Errorf("another status line removed:\n%s", out)
		}
	})
}

func TestInstallDuplicateEntries(t *testing.T) {
	in := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"jalon hook prompt"}]},{"hooks":[{"type":"command","command":"jalon hook prompt"}]}]}}`
	out, actions := install(t, in, client.NoDisplay)
	if got := promptCommands(t, []byte(out)); len(got) != 2 {
		t.Errorf("prompt hooks = %v, want both kept", got)
	}
	if !strings.Contains(strings.Join(actions, "\n"), "2 Jalon entries") {
		t.Errorf("actions do not warn about duplicates: %v", actions)
	}
}

func TestInstallStopHook(t *testing.T) {
	stopCmd := exe + " hook stop --client claude-code"
	t.Run("another stop hook", func(t *testing.T) {
		in := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/bin/notify.sh"}]}]}}`
		out, _ := install(t, in, client.NoDisplay)
		if got := hookCommands(t, []byte(out), "Stop"); !slices.Equal(got, []string{"~/bin/notify.sh", stopCmd}) {
			t.Errorf("stop hooks = %v, want notify.sh then Jalon's", got)
		}
		if !strings.Contains(out, `"hooks": [
          {
            "type": "command",
            "command": "~/bin/notify.sh"
          }
        ]
      },
      {`) {
			t.Errorf("Jalon's stop hook is not in a group of its own after the other:\n%s", out)
		}
	})
	t.Run("upgrade from a prompt hook only", func(t *testing.T) {
		in := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"` + exe + ` hook prompt --client claude-code","timeout":2}]}]}}`
		out, actions := install(t, in, client.NoDisplay)
		if got := promptCommands(t, []byte(out)); !slices.Equal(got, []string{exe + " hook prompt --client claude-code"}) {
			t.Errorf("prompt hooks = %v", got)
		}
		if got := hookCommands(t, []byte(out), "Stop"); !slices.Equal(got, []string{stopCmd}) {
			t.Errorf("stop hooks = %v", got)
		}
		if want := []string{"prompt hook: already installed", "stop hook: added " + stopCmd}; !slices.Equal(actions, want) {
			t.Errorf("actions = %v, want %v", actions, want)
		}
	})
	t.Run("entry written by hand", func(t *testing.T) {
		in := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"~/go/bin/jalon hook stop -config /x.yaml","timeout":5}]}]}}`
		out, _ := install(t, in, client.NoDisplay)
		if got := hookCommands(t, []byte(out), "Stop"); !slices.Equal(got, []string{exe + " hook stop -config /x.yaml"}) {
			t.Errorf("stop hooks = %v", got)
		}
		if !strings.Contains(out, `"timeout": 5`) {
			t.Errorf("timeout lost:\n%s", out)
		}
	})
	t.Run("two Jalon stop hooks", func(t *testing.T) {
		in := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"jalon hook stop"}]},{"hooks":[{"type":"command","command":"jalon hook stop"}]}]}}`
		out, actions := install(t, in, client.NoDisplay)
		if got := hookCommands(t, []byte(out), "Stop"); len(got) != 2 {
			t.Errorf("stop hooks = %v, want both kept", got)
		}
		if !slices.Contains(actions, "stop hook: 2 Jalon entries, each one runs: remove the extra ones by hand") {
			t.Errorf("actions = %v", actions)
		}
	})
	t.Run("lookalikes", func(t *testing.T) {
		for _, cmd := range []string{"jalon hook stopwatch", "jalon hook prompt", "jalonx hook stop"} {
			in := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":` + hookjson.JSONString(cmd) + `}]}]}}`
			out, _ := install(t, in, client.NoDisplay)
			if got := hookCommands(t, []byte(out), "Stop"); !slices.Equal(got, []string{cmd, stopCmd}) {
				t.Errorf("%q: stop hooks = %v, want it kept and Jalon's added", cmd, got)
			}
		}
	})
}

func TestUninstall(t *testing.T) {
	in := `{
  "hooks": {
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}],
    "UserPromptSubmit": [
      {"hooks": [{"type": "command", "command": "other-tool"}, {"type": "command", "command": "~/go/bin/jalon hook prompt"}]},
      {"hooks": [{"type": "command", "command": "/x/jalon hook prompt --client claude-code"}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "/x/jalon hook stop --client claude-code"}]}]
  },
  "statusLine": {"type": "command", "command": "/x/jalon statusline"}
}`
	out, actions, err := Adapter{}.Uninstall([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := promptCommands(t, out); !slices.Equal(got, []string{"other-tool"}) {
		t.Errorf("prompt hooks = %v, want [other-tool]", got)
	}
	if got, want := keys(t, out), []string{"hooks"}; !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	root, _ := hookjson.ParseObject(out)
	if raw, _ := root.Get("hooks"); !slices.Equal(keys(t, raw), []string{"PreToolUse", "UserPromptSubmit"}) {
		t.Errorf("hooks keys = %v, want the empty Stop removed", keys(t, raw))
	}
	if len(actions) != 4 || !slices.Contains(actions, "stop hook: removed /x/jalon hook stop --client claude-code") {
		t.Errorf("actions = %v, want two prompt hooks, the stop hook and the status line removed", actions)
	}

	t.Run("leaves no empty containers", func(t *testing.T) {
		installed, _ := install(t, `{"model":"opus"}`, client.StatusLine)
		out, _, err := Adapter{}.Uninstall([]byte(installed))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		_ = json.Unmarshal(out, &got)
		if !reflect.DeepEqual(got, map[string]any{"model": "opus"}) {
			t.Errorf("got %s", out)
		}
	})
	t.Run("keeps another status line", func(t *testing.T) {
		in := `{"statusLine":{"type":"command","command":"my-status.sh"}}`
		out, actions, err := Adapter{}.Uninstall([]byte(in))
		if err != nil || string(out) != in {
			t.Errorf("got %s, %v", out, err)
		}
		if !slices.Equal(actions, []string{"prompt hook: not installed", "stop hook: not installed"}) {
			t.Errorf("actions = %v", actions)
		}
	})
}

func TestInvalidSettings(t *testing.T) {
	for name, in := range map[string]string{
		"not JSON":       "{",
		"array":          "[]",
		"trailing data":  "{} {}",
		"hooks not obj":  `{"hooks": 3}`,
		"groups not arr": `{"hooks": {"UserPromptSubmit": {}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := (Adapter{}).Install([]byte(in), exe, client.NoDisplay); err == nil {
				t.Error("install gave no error")
			}
		})
	}
}
