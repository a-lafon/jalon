package config

import "fmt"

// Context configures the compaction advisor and the offline context report.
type Context struct {
	Enabled bool `yaml:"enabled"`
	// AdviseTokens is the growth of the prompt above the conversation's floor (the
	// size of its first request) at which a context reset is first advised.
	AdviseTokens int `yaml:"advise_tokens"`
	// AdviseStepTokens is how much the prompt must grow after an advice before
	// the next one.
	AdviseStepTokens int `yaml:"advise_step_tokens"`
	// StaleAfterTurns is the age, in user turns, after which a tool result is
	// counted as stale.
	StaleAfterTurns int `yaml:"stale_after_turns"`
}

func defaultContext() Context {
	return Context{Enabled: true, AdviseTokens: 200000, AdviseStepTokens: 60000, StaleAfterTurns: 5}
}

func (c Context) validate() error {
	for name, v := range map[string]int{
		"advise_tokens":      c.AdviseTokens,
		"advise_step_tokens": c.AdviseStepTokens,
		"stale_after_turns":  c.StaleAfterTurns,
	} {
		if v <= 0 {
			return fmt.Errorf("context.%s must be positive, got %d", name, v)
		}
	}
	return nil
}
