package config

import (
	"strings"
	"testing"
)

func TestContextDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if want := (Context{Enabled: true, AdviseTokens: 200000, AdviseStepTokens: 60000, StaleAfterTurns: 5}); cfg.Context != want {
		t.Errorf("context = %+v, want %+v", cfg.Context, want)
	}

	cfg, err = Load(writeConfig(t, "context:\n  advise_tokens: 150000\n  enabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Context.Enabled || cfg.Context.AdviseTokens != 150000 || cfg.Context.AdviseStepTokens != 60000 {
		t.Errorf("context = %+v", cfg.Context)
	}
}

func TestContextValidation(t *testing.T) {
	tests := map[string]struct{ content, wantErr string }{
		"zero threshold": {"context:\n  advise_tokens: 0\n", "context.advise_tokens"},
		"negative step":  {"context:\n  advise_step_tokens: -1\n", "context.advise_step_tokens"},
		"zero stale":     {"context:\n  stale_after_turns: 0\n", "context.stale_after_turns"},
		"unknown field":  {"context:\n  advise: 1\n", "field advise not found"},
	}
	for name, tt := range tests {
		_, err := Load(writeConfig(t, tt.content))
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: error = %v, want it to contain %q", name, err, tt.wantErr)
		}
	}
}
