package advisor

import (
	"reflect"
	"testing"
)

var cfg = Config{AdviseTokens: 200000, StepTokens: 100000}

func TestAdvise(t *testing.T) {
	tests := []struct {
		name      string
		state     State
		tokens    int
		want      Kind
		wantState State
	}{
		{"below threshold", State{}, 199999, None, State{}},
		{"first crossing", State{}, 210000, Reset, State{LastAdvised: 210000}},
		{"exactly at threshold", State{}, 200000, Reset, State{LastAdvised: 200000}},
		{"no repeat within the step", State{LastAdvised: 210000}, 260000, None, State{LastAdvised: 210000}},
		{"no repeat just under the step", State{LastAdvised: 210000}, 309999, None, State{LastAdvised: 210000}},
		{"repeat after the step", State{LastAdvised: 210000}, 315000, Reset, State{LastAdvised: 315000}},
		{"repeat exactly at the step", State{LastAdvised: 210000}, 310000, Reset, State{LastAdvised: 310000}},
		{"smaller prompt keeps the state", State{LastAdvised: 310000}, 150000, None, State{LastAdvised: 310000}},
	}
	for _, tt := range tests {
		a, st := Advise(tt.state, tt.tokens, 0, cfg)
		if a.Kind != tt.want || st != tt.wantState || a.PromptTokens != tt.tokens {
			t.Errorf("%s: advice %+v state %+v, want kind %s state %+v", tt.name, a, st, tt.want, tt.wantState)
		}
		wantReasons := []string(nil)
		if tt.want == Reset {
			wantReasons = []string{ReasonSize}
		}
		if !reflect.DeepEqual(a.Reasons, wantReasons) {
			t.Errorf("%s: reasons %v, want %v", tt.name, a.Reasons, wantReasons)
		}
	}
}

// The scenarios of "Size trigger with a step": the threshold is growth above the
// floor, the step stays on the absolute size.
func TestAdviseWithFloor(t *testing.T) {
	growth := Config{AdviseTokens: 150000, StepTokens: 150000}
	tests := []struct {
		name          string
		state         State
		tokens, floor int
		want          Kind
	}{
		{"first crossing", State{}, 210000, 50000, Reset},
		{"heavy starting context", State{}, 240000, 230000, None},
		{"heavy starting context, grown", State{}, 385000, 230000, Reset},
		{"just under the growth", State{}, 199999, 50000, None},
		{"exactly the growth", State{}, 200000, 50000, Reset},
		{"within the step", State{LastAdvised: 210000}, 320000, 50000, None},
		{"after the step", State{LastAdvised: 210000}, 365000, 50000, Reset},
		{"floor above the size", State{}, 100000, 230000, None},
		{"unknown floor", State{}, 150000, 0, Reset},
	}
	for _, tt := range tests {
		if a, _ := Advise(tt.state, tt.tokens, tt.floor, growth); a.Kind != tt.want {
			t.Errorf("%s: kind %s, want %s", tt.name, a.Kind, tt.want)
		}
	}
}

func TestResetAfterCompaction(t *testing.T) {
	_, st := Advise(State{}, 300000, 0, cfg)
	if !Compacted(300000, 40000) {
		t.Fatal("a prompt under half of the previous one is a compaction")
	}
	a, _ := Advise(State{}, 210000, 0, cfg)
	if a.Kind != Reset {
		t.Errorf("after a reset the first crossing advises again, got %+v", a)
	}
	if st.LastAdvised != 300000 {
		t.Errorf("state = %+v", st)
	}
}

func TestCompacted(t *testing.T) {
	tests := []struct {
		prev, first int
		want        bool
	}{
		{300000, 100000, true},
		{300000, 149999, true},
		{300000, 150000, false}, // exactly half is not under half
		{300000, 290000, false},
		{0, 0, false}, // first prompt of a conversation
	}
	for _, tt := range tests {
		if got := Compacted(tt.prev, tt.first); got != tt.want {
			t.Errorf("Compacted(%d, %d) = %v, want %v", tt.prev, tt.first, got, tt.want)
		}
	}
}
