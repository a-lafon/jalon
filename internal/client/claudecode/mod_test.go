package claudecode

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/a-lafon/jalon/internal/client"
)

// fakeClaude answers the claude commands it is given, by their arguments
// joined with spaces, and records each run.
type fakeClaude struct {
	answers map[string]fakeAnswer
	runs    []string
}

type fakeAnswer struct {
	out string
	err error
}

func (f *fakeClaude) run(name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	f.runs = append(f.runs, cmd)
	a, ok := f.answers[cmd]
	if !ok {
		return nil, fmt.Errorf("unexpected command %q", cmd)
	}
	return []byte(a.out), a.err
}

var _ client.Runner = (&fakeClaude{}).run

func TestCheckMod(t *testing.T) {
	notFound := &exec.Error{Name: "claude", Err: exec.ErrNotFound}
	tests := map[string]struct {
		answer fakeAnswer
		want   string // "" for no error
	}{
		"recent":      {fakeAnswer{out: "2.1.296 (Claude Code)\n"}, ""},
		"the first":   {fakeAnswer{out: "2.1.292 (Claude Code)\n"}, ""},
		"next minor":  {fakeAnswer{out: "2.2.0 (Claude Code)\n"}, ""},
		"too old":     {fakeAnswer{out: "2.1.291 (Claude Code)\n"}, "needs Claude Code 2.1.292 or later, this is 2.1.291"},
		"unreadable":  {fakeAnswer{out: "claude dev build\n"}, "cannot read the Claude Code version"},
		"not on PATH": {fakeAnswer{err: notFound}, "did not run"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakeClaude{answers: map[string]fakeAnswer{"claude --version": tt.answer}}
			err := Adapter{}.CheckMod(f.run)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInstallMod(t *testing.T) {
	const install = "claude plugin install jalon --marketplace a-lafon/jalon --config binary=/opt/my tools/jalon"
	t.Run("runs the install with the binary", func(t *testing.T) {
		f := &fakeClaude{answers: map[string]fakeAnswer{install: {out: "✔ Successfully installed plugin: jalon@jalon (scope: user)\n"}}}
		done, err := Adapter{}.InstallMod(f.run, "/opt/my tools/jalon", false)
		if err != nil || !strings.Contains(done, "mod: installed from a-lafon/jalon") {
			t.Errorf("InstallMod = %q, %v", done, err)
		}
		if !slices.Equal(f.runs, []string{install}) {
			t.Errorf("runs = %v", f.runs)
		}
	})
	t.Run("dry run runs nothing", func(t *testing.T) {
		f := &fakeClaude{}
		done, err := Adapter{}.InstallMod(f.run, "/opt/my tools/jalon", true)
		if err != nil || done != "mod: would run "+install {
			t.Errorf("InstallMod = %q, %v", done, err)
		}
		if len(f.runs) != 0 {
			t.Errorf("runs = %v, want none", f.runs)
		}
	})
	t.Run("a failure", func(t *testing.T) {
		f := &fakeClaude{answers: map[string]fakeAnswer{install: {err: errors.New("exit status 1: network down")}}}
		if _, err := (Adapter{}).InstallMod(f.run, "/opt/my tools/jalon", false); err == nil || !strings.Contains(err.Error(), "network down") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUninstallMod(t *testing.T) {
	const remove = "claude plugin marketplace remove jalon --json"
	exitStatus := errors.New("exit status 1")
	tests := map[string]struct {
		answer  fakeAnswer
		want    string
		wantErr string
	}{
		"installed": {fakeAnswer{out: `{"command":"marketplace-remove","outcome":"ok","marketplace":"jalon"}` + "\n"}, "mod: removed", ""},
		"not installed": {fakeAnswer{
			out: `{"command":"marketplace-remove","outcome":"failed","marketplace":"jalon","message":"Marketplace 'jalon' not found","failureCode":"not_configured"}` + "\n",
			err: exitStatus,
		}, "mod: not installed", ""},
		"claude not found": {fakeAnswer{err: &exec.Error{Name: "claude", Err: exec.ErrNotFound}}, "mod: not installed (claude not found)", ""},
		"another failure": {fakeAnswer{
			out: `{"command":"marketplace-remove","outcome":"failed","message":"settings are read-only","failureCode":"write_failed"}` + "\n",
			err: exitStatus,
		}, "", "settings are read-only"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakeClaude{answers: map[string]fakeAnswer{remove: tt.answer}}
			done, err := Adapter{}.UninstallMod(f.run, false)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || done != tt.want {
				t.Errorf("UninstallMod = %q, %v, want %q", done, err, tt.want)
			}
		})
	}
	t.Run("dry run runs nothing", func(t *testing.T) {
		f := &fakeClaude{}
		done, err := Adapter{}.UninstallMod(f.run, true)
		if err != nil || done != "mod: would run claude plugin marketplace remove jalon" || len(f.runs) != 0 {
			t.Errorf("UninstallMod = %q, %v, runs %v", done, err, f.runs)
		}
	})
}

func TestReadMod(t *testing.T) {
	// recorded is the shape claude plugin install wrote on 2026-10-12, Claude Code 2.1.296.
	const recorded = `{
  "model": "opus",
  "enabledPlugins": { "jalon@jalon": true },
  "extraKnownMarketplaces": { "jalon": { "source": { "source": "github", "repo": "a-lafon/jalon" } } },
  "pluginConfigs": { "jalon@jalon": { "options": { "binary": "/opt/homebrew/bin/jalon" } } }
}`
	tests := map[string]struct {
		settings string
		want     client.ModState
	}{
		"recorded":         {recorded, client.ModState{Installed: true, Enabled: true, Binary: "/opt/homebrew/bin/jalon"}},
		"disabled":         {`{"enabledPlugins": {"jalon@jalon": false}}`, client.ModState{Installed: true, Binary: "jalon"}},
		"no binary option": {`{"enabledPlugins": {"jalon@jalon": true}, "pluginConfigs": {"jalon@jalon": {}}}`, client.ModState{Installed: true, Enabled: true, Binary: "jalon"}},
		"empty binary":     {`{"enabledPlugins": {"jalon@jalon": true}, "pluginConfigs": {"jalon@jalon": {"options": {"binary": ""}}}}`, client.ModState{Installed: true, Enabled: true, Binary: "jalon"}},
		"binary not a string": {`{"enabledPlugins": {"jalon@jalon": true}, "pluginConfigs": {"jalon@jalon": {"options": {"binary": 1}}}}`,
			client.ModState{Installed: true, Enabled: true, Binary: "jalon"}},
		"options not an object": {`{"enabledPlugins": {"jalon@jalon": true}, "pluginConfigs": {"jalon@jalon": {"options": []}}}`,
			client.ModState{Installed: true, Enabled: true, Binary: "jalon"}},
		"enabled not a bool":    {`{"enabledPlugins": {"jalon@jalon": "yes"}}`, client.ModState{Installed: true, Binary: "jalon"}},
		"another plugin":        {`{"enabledPlugins": {"other@jalon": true}, "pluginConfigs": {"jalon@jalon": {"options": {"binary": "/x/jalon"}}}}`, client.ModState{}},
		"plugins not an object": {`{"enabledPlugins": ["jalon@jalon"]}`, client.ModState{}},
		"no plugins":            {`{"model": "opus"}`, client.ModState{}},
		"empty file":            {``, client.ModState{}},
		"invalid JSON":          {`{"enabledPlugins": {"jalon@jalon": true`, client.ModState{}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := (Adapter{}).ReadMod([]byte(tt.settings)); got != tt.want {
				t.Errorf("ReadMod = %+v, want %+v", got, tt.want)
			}
		})
	}
}
