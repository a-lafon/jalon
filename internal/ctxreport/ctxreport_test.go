package ctxreport

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/transcript"
)

var params = Params{Advisor: advisor.Config{AdviseTokens: 150000, StepTokens: 100000}, StaleAfterTurns: 5}

var t0 = time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)

// conv builds a conversation from the prompt size of each user turn. Each turn
// has one user block of 10 bytes, then one request and one tool result of
// resultBytes, so the bytes of the context are known. The prompt hook acts on
// every turn.
func conv(file string, resultBytes int, prompts ...int) transcript.Conversation {
	c := transcript.Conversation{File: file, Human: 1}
	for i, p := range prompts {
		at := t0.Add(time.Duration(i) * time.Hour)
		c.Blocks = append(c.Blocks, transcript.Block{Turn: i, Bytes: 10, Request: i})
		c.Turns = append(c.Turns, transcript.Turn{Time: at, PromptTokens: p, Block: len(c.Blocks) - 1, Request: i, Advisable: true})
		c.Requests = append(c.Requests, transcript.Request{Time: at, Turn: i, PromptTokens: p})
		c.Blocks = append(c.Blocks, transcript.Block{Turn: i, Bytes: resultBytes, ToolResult: true, Request: i + 1})
	}
	return c
}

// compactAfter records a compaction after n turns, as the reader would.
func compactAfter(c *transcript.Conversation, n int, trigger string, pre int) {
	// The blocks and requests that come before it: those of the first n turns.
	blocks, requests := 0, 0
	for _, b := range c.Blocks {
		if b.Turn < n {
			blocks++
		}
	}
	for _, r := range c.Requests {
		if r.Turn < n {
			requests++
		}
	}
	c.Compactions = append(c.Compactions, transcript.Compaction{
		UUID: trigger + "-" + string(rune('a'+len(c.Compactions))), Time: t0.Add(time.Duration(n)*time.Hour + time.Minute),
		Trigger: trigger, PreTokens: pre, AfterTurns: n, AfterRequests: requests, AfterBlocks: blocks,
	})
}

func TestLeadAndTokensBeyondTheAdvisedSize(t *testing.T) {
	// The advisor fires at turn 2 (210K), not again at 260K (turn 3), and the
	// user compacts after turn 5 (turns 0..5 exist).
	c := conv("a", 100, 50000, 120000, 210000, 260000, 280000, 300000)
	compactAfter(&c, 6, "manual", 300000)
	for i := range c.Turns {
		c.Turns[i].Prompt = string(rune('a' + i))
	}
	c.Turns[4].Prompt = "" // a prompt no person typed

	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Resets) != 1 {
		t.Fatalf("compactions = %+v", res.Resets)
	}
	got := res.Resets[0]
	if !got.Advised || got.Lead != 3 {
		t.Errorf("lead = %d (advised %v), want 3 turns after the advised one", got.Lead, got.Advised)
	}
	// The prompts typed after the advised turn 2: turns 3 and 5.
	if !slices.Equal(got.Prompts, []string{"d", "f"}) {
		t.Errorf("prompts = %q, want d and f", got.Prompts)
	}
	// Requests of turns 2..5 beyond the 210K of the advice: 0 + 50K + 70K + 90K.
	if got.Beyond != 210000 {
		t.Errorf("beyond = %d, want 210000", got.Beyond)
	}
	if got.Trigger != "manual" || got.PreTokens != 300000 {
		t.Errorf("compaction fields lost: %+v", got)
	}
	if len(res.Advices) != 1 || res.Advices[0].Turn != 2 || len(res.Unfollowed) != 0 {
		t.Errorf("advices = %+v, unfollowed = %+v", res.Advices, res.Unfollowed)
	}
}

func TestAdviceCountsTheGrowthAboveTheFirstRequest(t *testing.T) {
	// The conversation starts at 230K: 240K is no growth, 385K is 155K of it.
	c := conv("a", 100, 230000, 240000, 385000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 1 || res.Advices[0].Turn != 2 {
		t.Errorf("advices = %+v, want one at turn 2", res.Advices)
	}
	// A heavy start with no growth never advises, even far above the threshold.
	res = Simulate([]transcript.Conversation{conv("b", 100, 240000, 245000)}, params)
	if len(res.Advices) != 0 {
		t.Errorf("advices = %+v, want none", res.Advices)
	}
}

func TestFloorsAreTheFirstRequestOfEachConversation(t *testing.T) {
	script := conv("script", 100, 90000, 95000)
	script.Human = 0
	res := Simulate([]transcript.Conversation{conv("a", 100, 50000, 120000), conv("b", 100, 230000), script}, params)
	if len(res.Floors) != 2 || res.Floors[0].PromptTokens != 50000 || res.Floors[1].PromptTokens != 230000 {
		t.Errorf("floors = %+v, want 50K and 230K, the program's conversation left out", res.Floors)
	}
	if got := res.Between(t0, t0.Add(time.Minute)); len(got.Floors) != 2 {
		t.Errorf("floors in the period = %+v", got.Floors)
	}
	if got := res.Between(t0.Add(time.Hour), t0.Add(2*time.Hour)); len(got.Floors) != 0 {
		t.Errorf("floors outside the period = %+v", got.Floors)
	}
}

func TestFloorShareCountsTheRequestsOfThePeriod(t *testing.T) {
	// Starts at 70K; after a compaction a request falls below it, and counts whole.
	c := conv("a", 100, 70000, 170000, 270000, 40000)
	c.Project, c.Cwd = "/t/-work-app", "/work/app"
	res := Simulate([]transcript.Conversation{c}, params)
	f := res.Floors[0]
	if f.Project != "/t/-work-app" || f.Cwd != "/work/app" || f.PromptRead != 550000 || f.FloorRead != 250000 {
		t.Errorf("floor = %+v, want 250K of 550K", f)
	}
	// A period that cuts the conversation counts only its own requests.
	f = res.Between(t0, t0.Add(150*time.Minute)).Floors[0]
	if f.PromptRead != 510000 || f.FloorRead != 210000 {
		t.Errorf("floor in the period = %+v, want 210K of 510K", f)
	}
}

func TestCompactionWithoutAdvice(t *testing.T) {
	c := conv("a", 100, 50000, 120000, 150000)
	compactAfter(&c, 3, "manual", 150000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Resets) != 1 || res.Resets[0].Advised || res.Resets[0].Lead != 0 {
		t.Errorf("compactions = %+v, want one with no advice", res.Resets)
	}
}

func TestAdviceAfterACompactionStartsOver(t *testing.T) {
	// 210K advises; the user compacts after turn 1; the context grows back to
	// 215K at turn 3, which must advise again, and nothing follows it.
	c := conv("a", 100, 50000, 210000, 40000, 60000, 215000)
	compactAfter(&c, 2, "manual", 210000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 2 || res.Advices[0].Turn != 1 || res.Advices[1].Turn != 4 {
		t.Fatalf("advices = %+v", res.Advices)
	}
	if len(res.Resets) != 1 || !res.Resets[0].Advised || res.Resets[0].Lead != 0 {
		t.Errorf("compactions = %+v", res.Resets)
	}
	if len(res.Unfollowed) != 1 || res.Unfollowed[0].Turn != 4 {
		t.Errorf("unfollowed = %+v, want only the advice of turn 4", res.Unfollowed)
	}
}

func TestShrinkWithoutACompactionEntryResetsTheAdvisor(t *testing.T) {
	// A /clear: the prompt falls under half, and the earlier advice is left
	// unfollowed. The new context crosses the threshold again.
	c := conv("a", 100, 50000, 230000, 20000, 205000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 2 || len(res.Unfollowed) != 2 || len(res.Resets) != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestStaleShare(t *testing.T) {
	// Eight turns: at turn 7 the tool results of turns 0 and 1 are older than
	// five turns. Each turn holds a 10-byte prompt and a 1000-byte tool result.
	c := conv("a", 1000, 10, 10, 10, 10, 10, 10, 10, 210000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 1 {
		t.Fatalf("advices = %+v", res.Advices)
	}
	// Bytes seen at turn 7: 7 full turns (7 x 1010) plus its own prompt (10).
	// Stale: results of turns 0 and 1.
	want := 2000.0 / (7*1010 + 10)
	if got := res.Advices[0].StaleShare; math.Abs(got-want) > 1e-9 {
		t.Errorf("stale share = %v, want %v", got, want)
	}
}

func TestStaleShareStartsAfterTheLastCompaction(t *testing.T) {
	// The tool results before the compaction are gone from the context.
	c := conv("a", 1000, 10, 10, 10, 10, 10, 10, 10, 210000)
	compactAfter(&c, 6, "manual", 10)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 1 {
		t.Fatalf("advices = %+v", res.Advices)
	}
	if got := res.Advices[0].StaleShare; got != 0 {
		t.Errorf("stale share = %v, want 0: everything stale was compacted away", got)
	}
}

func TestBetweenKeepsThePeriod(t *testing.T) {
	c := conv("a", 100, 50000, 210000, 50000, 220000)
	compactAfter(&c, 2, "manual", 210000)
	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 2 {
		t.Fatalf("advices = %+v", res.Advices)
	}
	early := res.Between(t0, t0.Add(90*time.Minute))
	if len(early.Advices) != 1 || early.Advices[0].Turn != 1 || len(early.Resets) != 0 {
		t.Errorf("early = %+v", early)
	}
	late := res.Between(t0.Add(90*time.Minute), t0.Add(24*time.Hour))
	if len(late.Advices) != 1 || late.Advices[0].Turn != 3 || len(late.Resets) != 1 || len(late.Unfollowed) != 1 {
		t.Errorf("late = %+v", late)
	}
}

func TestDistribution(t *testing.T) {
	if d := Distribution(nil); d.N != 0 || d.Max != 0 {
		t.Errorf("empty = %+v", d)
	}
	d := Distribution([]float64{40, 10, 30, 20})
	if d.N != 4 || d.P25 != 10 || d.P50 != 20 || d.P75 != 30 || d.Max != 40 {
		t.Errorf("distribution = %+v", d)
	}
	if ten := Distribution([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}); ten.P10 != 1 || ten.P90 != 9 {
		t.Errorf("p10 %v, p90 %v, want 1 and 9", ten.P10, ten.P90)
	}
	if d := Distribution([]float64{7}); d.P25 != 7 || d.P75 != 7 || d.Max != 7 {
		t.Errorf("single = %+v", d)
	}
}

func TestAutomatedConversationsAreLeftOut(t *testing.T) {
	human := conv("human", 100, 50000, 210000)
	script := conv("script", 100, 50000, 210000, 215000)
	script.Human = 0
	compactAfter(&script, 3, "manual", 215000)

	res := Simulate([]transcript.Conversation{script, human}, params)
	if res.Excluded != 1 || res.Conversations != 1 {
		t.Errorf("excluded %d, simulated %d, want 1 and 1", res.Excluded, res.Conversations)
	}
	if len(res.Advices) != 1 || res.Advices[0].File != "human" || len(res.Resets) != 0 {
		t.Errorf("only the conversation with a person may count: %+v", res)
	}
}

func TestCrossedMidTurn(t *testing.T) {
	// The turns start under the threshold, a request of the last turn's tool loop
	// goes over it, and the user compacts before another turn starts.
	c := conv("a", 100, 50000, 120000, 150000)
	c.Requests = append(c.Requests, transcript.Request{Time: t0.Add(3 * time.Hour), Turn: 2, PromptTokens: 314000})
	compactAfter(&c, 3, "manual", 314000)
	// And a compaction that never got near the threshold.
	small := conv("b", 100, 50000, 60000)
	compactAfter(&small, 2, "manual", 60000)

	res := Simulate([]transcript.Conversation{c, small}, params)
	if len(res.Resets) != 2 {
		t.Fatalf("compactions = %+v", res.Resets)
	}
	byFile := map[string]SimReset{}
	for _, comp := range res.Resets {
		byFile[comp.File] = comp
	}
	if a := byFile["a"]; a.Advised || !a.CrossedMidTurn {
		t.Errorf("a = %+v, want crossed mid-turn", a)
	}
	if b := byFile["b"]; b.Advised || b.CrossedMidTurn {
		t.Errorf("b = %+v, want plain no advice", b)
	}
}

// spanConv is a conversation whose second prompt is answered by one request
// per size: a prompt span the stop hook acts on.
func spanConv(floor int, sizes ...int) transcript.Conversation {
	c := conv("a", 100, floor)
	for i, size := range sizes {
		at := t0.Add(time.Duration(i+1) * time.Minute)
		if i == 0 {
			c.Turns = append(c.Turns, transcript.Turn{Time: at, PromptTokens: size, Block: len(c.Blocks), Request: len(c.Requests), Advisable: true, Human: true})
		}
		c.Requests = append(c.Requests, transcript.Request{Time: at, Turn: 1, PromptTokens: size})
		c.Blocks = append(c.Blocks, transcript.Block{Turn: 1, Bytes: 10, Request: len(c.Requests)})
	}
	return c
}

func TestStopHookAdviceIsPlacedWhereItFired(t *testing.T) {
	stop := Params{Advisor: params.Advisor, StaleAfterTurns: params.StaleAfterTurns, StopHook: true}
	// The hook advised at the request of 205K, blocked once, and the model went
	// on under the same prompt to 260K. Two requests later the transcript holds
	// the advice, as the reader records it.
	c := spanConv(50000, 100000, 190000, 205000, 240000, 260000)
	c.Advices = []transcript.Advice{{UUID: "s", Time: t0.Add(4 * time.Minute), Turn: 1, Request: 5, Rendered: true}}
	res := Simulate([]transcript.Conversation{c}, stop)
	if len(res.Advices) != 1 || !res.Advices[0].AtStop || res.Advices[0].PromptTokens != 205000 {
		t.Errorf("advices = %+v, want one at the stop, at 205K", res.Advices)
	}

	// With no advice in the span, the hook is evaluated at its end, as before.
	c.Advices = nil
	res = Simulate([]transcript.Conversation{c}, stop)
	if len(res.Advices) != 1 || !res.Advices[0].AtStop || res.Advices[0].PromptTokens != 240000 {
		t.Errorf("advices = %+v, want one at the end of the span, at 240K", res.Advices)
	}
}

func TestIgnoredIsAnAdviceTheConversationOutlived(t *testing.T) {
	// The advisor fires at turn 2 and the user goes on without resetting.
	res := Simulate([]transcript.Conversation{conv("a", 100, 50000, 120000, 210000, 215000)}, params)
	if len(res.Unfollowed) != 1 || !res.Unfollowed[0].Outlived {
		t.Errorf("unfollowed = %+v, want one ignored advice", res.Unfollowed)
	}
	// The same advice at the last turn: the conversation ended at it.
	res = Simulate([]transcript.Conversation{conv("a", 100, 50000, 120000, 210000)}, params)
	if len(res.Unfollowed) != 1 || res.Unfollowed[0].Outlived {
		t.Errorf("unfollowed = %+v, want an advice the conversation ended at", res.Unfollowed)
	}
}

func TestResultsAreSortedByTime(t *testing.T) {
	shift := func(c transcript.Conversation, d time.Duration) transcript.Conversation {
		for i := range c.Turns {
			c.Turns[i].Time = c.Turns[i].Time.Add(d)
		}
		for i := range c.Compactions {
			c.Compactions[i].Time = c.Compactions[i].Time.Add(d)
		}
		return c
	}
	late := conv("late", 100, 50000, 210000, 50000)
	compactAfter(&late, 2, "manual", 210000)
	early := shift(conv("early", 100, 50000, 220000, 60000), -24*time.Hour)
	compactAfter(&early, 2, "manual", 220000)
	early.Compactions[0].Time = early.Compactions[0].Time.Add(-24 * time.Hour)
	// Two advices that no compaction followed, given later one first.
	laterAdvice := conv("later", 100, 50000, 230000)
	earlierAdvice := shift(conv("earlier", 100, 50000, 240000), -48*time.Hour)

	res := Simulate([]transcript.Conversation{late, early, laterAdvice, earlierAdvice}, params)
	if len(res.Resets) != 2 || res.Resets[0].File != "early" || res.Resets[1].File != "late" {
		t.Errorf("compactions = %+v, want the early one first", res.Resets)
	}
	if len(res.Unfollowed) != 2 || res.Unfollowed[0].File != "earlier" || res.Unfollowed[1].File != "later" {
		t.Errorf("unfollowed = %+v, want oldest first", res.Unfollowed)
	}
	for i := 1; i < len(res.Advices); i++ {
		if res.Advices[i].Time.Before(res.Advices[i-1].Time) {
			t.Errorf("advices out of order: %+v", res.Advices)
		}
	}
}

func TestStaleShareByRequests(t *testing.T) {
	// Three user turns of one request each would show 0% stale by turns, but 60
	// requests have passed since the first tool results. Build one turn with a
	// long tool loop: blocks at request ages 0..59, 100 bytes each.
	c := transcript.Conversation{File: "a", Human: 1}
	c.Blocks = append(c.Blocks, transcript.Block{Turn: 0, Bytes: 10, Request: 0})
	c.Turns = append(c.Turns, transcript.Turn{Time: t0, PromptTokens: 10, Block: 0, Request: 0, Advisable: true})
	for r := 0; r < 60; r++ {
		c.Requests = append(c.Requests, transcript.Request{Time: t0, Turn: 0, PromptTokens: 10})
		c.Blocks = append(c.Blocks, transcript.Block{Turn: 0, Bytes: 100, ToolResult: true, Request: r + 1})
	}
	c.Turns = append(c.Turns, transcript.Turn{Time: t0.Add(time.Hour), PromptTokens: 250000, Block: len(c.Blocks), Request: 60, Advisable: true})
	c.Blocks = append(c.Blocks, transcript.Block{Turn: 1, Bytes: 10, Request: 60})
	c.Requests = append(c.Requests, transcript.Request{Time: t0.Add(time.Hour), Turn: 1, PromptTokens: 250000})

	res := Simulate([]transcript.Conversation{c}, params)
	if len(res.Advices) != 1 {
		t.Fatalf("advices = %+v", res.Advices)
	}
	a := res.Advices[0]
	if a.StaleShare != 0 {
		t.Errorf("by turns = %v, want 0: both turns are recent", a.StaleShare)
	}
	// Total 10 + 60*100 + 10 = 6020 bytes. At the new turn, 60 requests were made,
	// a result of block request r+1 is 59-r requests old: older than 10 means
	// r+1 < 50 (49 results), older than 25 means r+1 < 35 (34), older than 50 means r+1 < 10 (9).
	want := []float64{4900.0 / 6020, 3400.0 / 6020, 900.0 / 6020}
	if len(a.StaleByRequests) != 3 {
		t.Fatalf("by requests = %v", a.StaleByRequests)
	}
	for i := range want {
		if math.Abs(a.StaleByRequests[i]-want[i]) > 1e-9 {
			t.Errorf("older than %d requests = %v, want %v", RequestAges[i], a.StaleByRequests[i], want[i])
		}
	}
}
