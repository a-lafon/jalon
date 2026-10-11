// Package advisor decides when a conversation is worth resetting. Advise is a
// pure function of the prompt size, the conversation's floor and the previous
// advice, so the live hook and the offline report run the same code.
package advisor

// Kind is the advice given at the start of a user turn.
type Kind string

const (
	None  Kind = "none"
	Reset Kind = "reset"
)

// ReasonSize is the reason of an advice given because the prompt is large.
const ReasonSize = "size"

// MessagePrefix starts the message the prompt hook shows when it advises a
// reset. The context report finds the advice given in the transcripts by it.
const MessagePrefix = "Jalon: context at "

// Config holds the thresholds, in prompt tokens.
type Config struct {
	// AdviseTokens is the growth of the prompt above the floor from which a
	// reset is advised.
	AdviseTokens int
	// StepTokens is the growth needed after an advice before the next one.
	StepTokens int
}

// State is what the advisor remembers of a conversation. The zero value means
// no advice has fired yet.
type State struct {
	// LastAdvised is the prompt size at the last advice, 0 when none fired.
	LastAdvised int
}

// Advice is the outcome at one user turn.
type Advice struct {
	Kind    Kind
	Reasons []string
	// PromptTokens is the size the advice is based on.
	PromptTokens int
}

// Advise computes the advice for a user turn that starts at promptTokens, and
// the state to carry to the next turn. The floor is the size the conversation
// started at, which no reset can remove, 0 when unknown. The first advice
// comes with the growth above the floor; the step after an advice stays on the
// absolute size. It must be called on user turns only: inside a tool loop the
// advice stays what it was.
func Advise(st State, promptTokens, floor int, cfg Config) (Advice, State) {
	a := Advice{Kind: None, PromptTokens: promptTokens}
	if promptTokens-floor < cfg.AdviseTokens {
		return a, st
	}
	if st.LastAdvised == 0 || promptTokens-st.LastAdvised >= cfg.StepTokens {
		a.Kind, a.Reasons = Reset, []string{ReasonSize}
		return a, State{LastAdvised: promptTokens}
	}
	return a, st
}

// Compacted reports whether a conversation whose last prompt was prev and whose
// new one is first looks compacted: the prompt fell under half of what it was.
// A detected compaction resets the state (the zero State).
func Compacted(prev, first int) bool {
	return prev > 0 && first*2 < prev
}
