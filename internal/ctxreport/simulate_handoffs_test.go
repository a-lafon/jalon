package ctxreport

import (
	"slices"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/transcript"
)

// writer is a parent conversation whose simulated advice fires at its last
// turn (210K is 160K above its 50K floor), and that wrote the handoff after it.
func writer(prompts ...int) transcript.Conversation {
	if len(prompts) == 0 {
		prompts = []int{50000, 120000, 150000, 210000}
	}
	p := conv("p", 100, prompts...)
	p.Writes = []transcript.Write{{Path: handoffPath, Time: t0.Add(3*time.Hour + 30*time.Minute)}}
	return p
}

func TestAdviceFollowedByAHandoff(t *testing.T) {
	r := resumed("r", handoffPath, t0.Add(4*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{writer(), r}, params)
	if len(res.Resets) != 1 || len(res.Unfollowed) != 0 {
		t.Fatalf("resets = %+v, unfollowed = %+v", res.Resets, res.Unfollowed)
	}
	got := res.Resets[0]
	if got.Kind != KindHandoff || got.File != "p" || !got.Advised || got.Lead != 0 || got.PreTokens != 210000 || got.Trigger != "" {
		t.Errorf("reset = %+v, want a handoff reset with a lead of 0 turns after 210K", got)
	}
	if got.AdviceGiven {
		t.Errorf("reset = %+v, the hook gave no advice", got)
	}
}

func TestHandoffResumedTwiceIsOneSimulatedReset(t *testing.T) {
	r1 := resumed("r1", handoffPath, t0.Add(4*time.Hour), 40000)
	r2 := resumed("r2", handoffPath, t0.Add(5*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{writer(), r1, r2}, params)
	if len(res.Resets) != 1 {
		t.Errorf("resets = %+v, want one", res.Resets)
	}
}

func TestHandoffResumeNotLinkedCutsNothing(t *testing.T) {
	// No conversation wrote that handoff, and its Session: line names none.
	r := resumed("r", "/r/.jalon/handoffs/2026-10-06-0800-abcd1234-other.md", t0.Add(4*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{writer(), r}, params)
	if len(res.Resets) != 0 || len(res.Unfollowed) != 1 {
		t.Errorf("resets = %+v, unfollowed = %+v, want the advice unfollowed", res.Resets, res.Unfollowed)
	}
}

func TestResetAfterAnAdviceGiven(t *testing.T) {
	p := writer()
	advise(&p, 3)
	r := resumed("r", handoffPath, t0.Add(4*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{p, r}, params)
	if len(res.Resets) != 1 || !res.Resets[0].AdviceGiven {
		t.Errorf("resets = %+v, want one that follows an advice given", res.Resets)
	}
}

func TestParentGoesOnAfterTheResume(t *testing.T) {
	// The parent is used again after the resume: its next stretch starts at the
	// cut, so its advice at turn 5 follows no reset.
	p := writer(50000, 120000, 150000, 210000, 60000, 215000)
	r := resumed("r", handoffPath, t0.Add(4*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{p, r}, params)
	if len(res.Resets) != 1 || res.Resets[0].Lead != 0 {
		t.Fatalf("resets = %+v", res.Resets)
	}
	if len(res.Advices) != 2 || len(res.Unfollowed) != 1 || res.Unfollowed[0].Turn != 5 {
		t.Errorf("advices = %+v, unfollowed = %+v, want the advice of turn 5 alone", res.Advices, res.Unfollowed)
	}
}

func TestCompactionAndHandoffAreListedInTimeOrder(t *testing.T) {
	// A compaction after turn 2, then the handoff after turn 4.
	p := writer(50000, 120000, 210000, 60000, 215000)
	p.Writes[0].Time = t0.Add(4*time.Hour + 30*time.Minute)
	compactAfter(&p, 3, "manual", 210000)
	r := resumed("r", handoffPath, t0.Add(5*time.Hour), 40000)
	res := Simulate([]transcript.Conversation{p, r}, params)
	kinds := []string{}
	for _, x := range res.Resets {
		kinds = append(kinds, x.Kind)
	}
	if !slices.Equal(kinds, []string{KindCompaction, KindHandoff}) {
		t.Errorf("kinds = %v", kinds)
	}
	if res.Resets[0].Trigger != "manual" || res.Resets[1].Lead != 0 || !res.Resets[1].Advised || len(res.Unfollowed) != 0 {
		t.Errorf("resets = %+v, unfollowed = %+v", res.Resets, res.Unfollowed)
	}
}
