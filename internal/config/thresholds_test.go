package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commentedConfig is a config file as a user keeps it: a comment, blank lines and
// comments after the values of the context section.
const commentedConfig = `# Jalon configuration.

context:
  enabled: true
  advise_tokens: 200000        # first advice
  advise_step_tokens: 150000   # next advice
  stale_after_turns: 5         # stale tool results
`

func intp(v int) *int { return &v }

func TestSetThresholds(t *testing.T) {
	tests := map[string]struct {
		data         string
		tokens, step *int
		want         string
	}{
		"no file": {
			"", intp(200000), nil,
			"context:\n  advise_tokens: 200000\n",
		},
		"both in a new file": {
			"", intp(200000), intp(100000),
			"context:\n  advise_tokens: 200000\n  advise_step_tokens: 100000\n",
		},
		"value replaced, the rest kept": {
			commentedConfig, nil, intp(100000),
			strings.Replace(commentedConfig, "advise_step_tokens: 150000", "advise_step_tokens: 100000", 1),
		},
		"same value": {
			commentedConfig, intp(200000), nil,
			commentedConfig,
		},
		"key added after the last one": {
			"context:\n  enabled: true # on\n  # stale_after_turns: 5\n", intp(200000), intp(100000),
			"context:\n  enabled: true # on\n  advise_tokens: 200000\n  advise_step_tokens: 100000\n  # stale_after_turns: 5\n",
		},
		"empty context key": {
			"context:\n# commented out\n", nil, intp(100000),
			"context:\n  advise_step_tokens: 100000\n# commented out\n",
		},
		"no trailing newline": {
			"context:\n  enabled: true", intp(200000), nil,
			"context:\n  enabled: true\n  advise_tokens: 200000\n",
		},
		"comments only": {
			"# nothing yet\n", intp(200000), nil,
			"# nothing yet\n\ncontext:\n  advise_tokens: 200000\n",
		},
		"flow context": {
			"context: {advise_tokens: 150000, enabled: true}\n", intp(200000), nil,
			"context: {advise_tokens: 200000, enabled: true}\n",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var data []byte
			if tt.data != "" {
				data = []byte(tt.data)
			}
			got, err := SetThresholds("config.yaml", data, tt.tokens, tt.step)
			if err != nil {
				t.Fatalf("SetThresholds: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestSetThresholdsKeepsTheExampleConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "jalon.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := SetThresholds("jalon.example.yaml", data, intp(250000), intp(100000))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(data), "advise_tokens: 200000", "advise_tokens: 250000", 1)
	want = strings.Replace(want, "advise_step_tokens: 150000", "advise_step_tokens: 100000", 1)
	if string(got) != want {
		t.Errorf("got:\n%s", got)
	}
}

func TestSetThresholdsRefusals(t *testing.T) {
	tests := map[string]struct {
		data         string
		tokens, step *int
		wantErr      string
	}{
		"zero":                {"", intp(0), nil, "context.advise_tokens must be positive"},
		"negative step":       {commentedConfig, nil, intp(-1), "context.advise_step_tokens must be positive"},
		"invalid file":        {"listn: x\n", intp(200000), nil, "field listn not found"},
		"invalid YAML":        {"context: [\n", intp(200000), nil, "parse config.yaml"},
		"quoted value":        {"context:\n  advise_tokens: \"1\"\n", intp(200000), nil, "cannot edit context.advise_tokens"},
		"add to a flow map":   {"context: {enabled: true}\n", intp(200000), nil, "set the thresholds by hand"},
		"context not a map":   {"context: 3\n", intp(200000), nil, "context is not a mapping"},
		"top level not a map": {"- a\n", intp(200000), nil, "top level is not a mapping"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := SetThresholds("config.yaml", []byte(tt.data), tt.tokens, tt.step)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestContextSection(t *testing.T) {
	got, err := ContextSection([]byte(commentedConfig))
	if err != nil {
		t.Fatal(err)
	}
	want := "context:\n  enabled: true\n  advise_tokens: 200000 # first advice\n  advise_step_tokens: 150000 # next advice\n  stale_after_turns: 5 # stale tool results\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got, err := ContextSection(nil); got != "" || err != nil {
		t.Errorf("no file: %q, %v", got, err)
	}
}

func TestRuneOffset(t *testing.T) {
	// The YAML parser counts columns in characters, the text is edited in bytes.
	tests := []struct {
		s    string
		n    int
		want int
	}{
		{"a: 1", 3, 3},
		{"é: 1", 3, 4},
		{"é: 1", 4, 5},
		{"é: 1", 5, -1},
		{"", 0, 0},
	}
	for _, tt := range tests {
		if got := runeOffset(tt.s, tt.n); got != tt.want {
			t.Errorf("runeOffset(%q, %d) = %d, want %d", tt.s, tt.n, got, tt.want)
		}
	}
}
