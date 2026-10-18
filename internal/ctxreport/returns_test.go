package ctxreport

import (
	"slices"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/transcript"
)

// req is a request at at after t0, with its prompt tokens and cache read.
type req struct {
	at           time.Duration
	prompt, read int
}

// talk builds a conversation of prompts a person typed, each with the requests
// that answered it.
func talk(turns ...[]req) transcript.Conversation {
	c := transcript.Conversation{File: "a", Human: len(turns)}
	for ti, reqs := range turns {
		c.Turns = append(c.Turns, transcript.Turn{Time: t0.Add(reqs[0].at), Request: len(c.Requests), Human: true})
		for _, r := range reqs {
			c.Requests = append(c.Requests, transcript.Request{Time: t0.Add(r.at), Turn: ti, PromptTokens: r.prompt, CacheRead: r.read})
		}
	}
	return c
}

// paused is a conversation that grew from 50K to 300K, then a prompt typed
// after pause whose first request sends 301K and reads read of it from the cache.
func paused(pause time.Duration, read int) transcript.Conversation {
	return talk(
		[]req{{0, 50_000, 0}, {time.Minute, 300_000, 290_000}},
		[]req{{time.Minute + pause, 301_000, read}, {2*time.Minute + pause, 302_000, 301_000}},
	)
}

func TestColdReturns(t *testing.T) {
	at := func(d time.Duration) time.Time { return t0.Add(d) }
	expired := ColdReturn{Time: at(41 * time.Minute), Pause: 40 * time.Minute, NotRead: 289_000, Grown: true}
	tests := []struct {
		name string
		conv transcript.Conversation
		edit func(*transcript.Conversation)
		want []ColdReturn
	}{
		{name: "return after an expired cache", conv: paused(40*time.Minute, 12_000), want: []ColdReturn{expired}},
		{name: "pause within the cache lifetime", conv: paused(40*time.Minute, 295_000)},
		{name: "short pause", conv: paused(4*time.Minute, 12_000)},
		{name: "not typed", conv: paused(40*time.Minute, 12_000), edit: func(c *transcript.Conversation) { c.Turns[1].Human = false }},
		{name: "after a compaction", conv: paused(40*time.Minute, 12_000), edit: func(c *transcript.Conversation) {
			c.Compactions = []transcript.Compaction{{Time: at(30 * time.Minute), AfterTurns: 1, AfterRequests: 2}}
		}},
		{name: "handoff already written", conv: paused(40*time.Minute, 12_000), edit: func(c *transcript.Conversation) {
			c.Writes = []transcript.Write{{Path: handoffPath, Time: at(time.Minute)}}
		}, want: []ColdReturn{{Time: expired.Time, Pause: expired.Pause, NotRead: expired.NotRead, Grown: true, AfterHandoff: true}}},
		{name: "handoff written after it", conv: paused(40*time.Minute, 12_000), edit: func(c *transcript.Conversation) {
			c.Writes = []transcript.Write{{Path: handoffPath, Time: at(50 * time.Minute)}}
		}, want: []ColdReturn{expired}},
		{name: "growth below the threshold", conv: paused(40*time.Minute, 12_000), edit: func(c *transcript.Conversation) { c.Requests[0].PromptTokens = 150_000 },
			want: []ColdReturn{{Time: expired.Time, Pause: expired.Pause, NotRead: expired.NotRead}}},
		{name: "first request typed", conv: talk([]req{{time.Hour, 60_000, 0}, {time.Hour + time.Minute, 70_000, 60_000}})},
		{name: "a later request of the turn after a long tool run", conv: talk(
			[]req{{0, 50_000, 0}, {time.Minute, 300_000, 290_000}, {time.Hour, 301_000, 0}},
		)},
		{name: "two cold returns", conv: talk(
			[]req{{0, 50_000, 0}, {time.Minute, 100_000, 50_000}},
			[]req{{11 * time.Minute, 101_000, 1_000}},
			[]req{{12 * time.Minute, 102_000, 101_000}},
			[]req{{2 * time.Hour, 103_000, 0}},
		), want: []ColdReturn{
			{Time: at(11 * time.Minute), Pause: 10 * time.Minute, NotRead: 100_000},
			{Time: at(2 * time.Hour), Pause: 2*time.Hour - 12*time.Minute, NotRead: 103_000},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.conv
			if tt.edit != nil {
				tt.edit(&c)
			}
			if got := ColdReturns([]transcript.Conversation{c}, 200_000).Cold; !slices.Equal(got, tt.want) {
				t.Errorf("cold returns = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestColdReturnsBetween(t *testing.T) {
	rs := ColdReturns([]transcript.Conversation{paused(40*time.Minute, 12_000), shifted(paused(time.Hour, 0), 3*time.Hour)}, 200_000)
	if len(rs.Cold) != 2 || rs.PromptRead != 2*(50_000+300_000+301_000+302_000) {
		t.Fatalf("returns = %+v", rs)
	}
	got := rs.Between(t0.Add(2*time.Hour), t0.Add(10*time.Hour))
	if len(got.Cold) != 1 || got.Cold[0].Pause != time.Hour || got.PromptRead != 50_000+300_000+301_000+302_000 {
		t.Errorf("between = %+v, want the second conversation alone", got)
	}
}
