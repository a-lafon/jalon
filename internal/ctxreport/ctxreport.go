// Package ctxreport replays the compaction advisor over recorded conversations
// and compares its advice with the compactions the client actually did.
package ctxreport

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/a-lafon/jalon/internal/advisor"
	"github.com/a-lafon/jalon/internal/transcript"
)

// Params are the thresholds of a simulation.
type Params struct {
	Advisor advisor.Config
	// StaleAfterTurns is the age in user turns after which a tool result is stale.
	StaleAfterTurns int
	// StopHook replays the stop hook's advice at the end of a turn too.
	StopHook bool
}

// Advice is an advice the advisor would have given at a user turn, at its
// start or, when AtStop is set, at its end.
type Advice struct {
	File string
	Time time.Time
	// Turn is the index of the turn, Request the index of its first request,
	// or of its last one for an advice at its end.
	Turn, Request int
	AtStop        bool
	PromptTokens  int
	// StaleShare is the share of the context bytes taken by tool results older
	// than StaleAfterTurns user turns.
	StaleShare float64
	// StaleByRequests is the same share for tool results older than each of
	// RequestAges requests, counted in distinct assistant messages. It tells
	// stale output apart in conversations with few user turns and long tool loops.
	StaleByRequests []float64
}

// RequestAges are the ages, in requests, for which the stale share is measured.
var RequestAges = []int{10, 25, 50}

// SimReset is an actual reset of a conversation, a compaction or a handoff
// resume, with what the advisor would have said before it.
type SimReset struct {
	// Kind is KindCompaction or KindHandoff.
	Kind string
	Time time.Time
	File string
	// Trigger is "manual" or "auto" for a compaction, "" for a handoff.
	Trigger string
	// PreTokens is the context before the reset: the compaction's own count, or
	// the prompt tokens of the conversation's last request before the resume.
	PreTokens int
	// Advised is false when the advisor never fired since the previous reset.
	Advised bool
	// AdviceGiven is set when the prompt hook actually gave an advice since the
	// previous reset, as the transcript records it.
	AdviceGiven bool
	// CrossedMidTurn is set when it never fired although a request went over the
	// threshold: the context grew past it at a turn's last request, inside a
	// turn when the stop hook is not replayed, or at a turn no hook acts on,
	// and the user reset before the next turn a hook acts on.
	CrossedMidTurn bool
	// Lead is the number of user turns started between the first advice and the
	// reset.
	Lead int
	// Beyond is the prompt tokens carried by the requests from the first advice to
	// the reset, beyond the size at that advice.
	Beyond int
	// Prompts are the starts of the prompts a person typed after the first advice
	// and before the reset, in order.
	Prompts []string
}

// cut is where a conversation is reset: its compactions and its linked handoff
// resumes.
type cut struct {
	kind, trigger                          string
	time                                   time.Time
	preTokens                              int
	afterTurns, afterRequests, afterBlocks int
}

// Result is the outcome of a simulation.
type Result struct {
	// Conversations is the number of conversations simulated, Excluded the
	// number left out because no person typed in them (driven by a program).
	Conversations, Excluded int
	Resets                  []SimReset
	// Floors holds the size of the first request of each conversation simulated.
	Floors []Floor
	// Advices holds every advice given, in conversation order.
	Advices []Advice
	// Unfollowed holds the advices that no compaction followed: the conversation
	// ended or was reset without one.
	Unfollowed []Advice
}

// Floor is the size a conversation started at, which no reset removes.
type Floor struct {
	Time         time.Time
	PromptTokens int
	// Project is the conversation's project folder, Cwd its working directory.
	Project, Cwd string
	// PromptRead is the prompt tokens of its requests in the period, and
	// FloorRead the part of them that was the floor: the lesser of each and
	// PromptTokens.
	PromptRead, FloorRead int64
	requests              []transcript.Request
}

// sumRequests sets the sums of the floor over its requests made when in says so.
func (f Floor) sumRequests(in func(time.Time) bool) Floor {
	f.PromptRead, f.FloorRead = 0, 0
	for _, r := range f.requests {
		if in(r.Time) {
			f.PromptRead += int64(r.PromptTokens)
			f.FloorRead += int64(min(r.PromptTokens, f.PromptTokens))
		}
	}
	return f
}

// Simulate runs the advisor on the user turns of each conversation that the
// prompt hook would act on: prompts a person typed, in a conversation a person
// typed in before. With p.StopHook, it also runs it at the end of the turns
// the stop hook would act on.
func Simulate(convs []transcript.Conversation, p Params) Result {
	var res Result
	// A handoff resumed more than once from the same conversation is one reset.
	handoffs := map[int][]cut{}
	links, _ := linkResumes(convs)
	seen := map[[2]string]bool{}
	slices.SortStableFunc(links, func(a, b link) int { return a.res.Time.Compare(b.res.Time) })
	for _, l := range links {
		if key := [2]string{convs[l.from].File, l.res.Path}; !seen[key] {
			seen[key] = true
			handoffs[l.from] = append(handoffs[l.from], cut{
				kind: KindHandoff, time: l.res.Time, preTokens: l.before,
				afterTurns: l.cutTurns, afterRequests: l.cutAt,
				afterBlocks: countBlocks(convs[l.from], l.cutTurns),
			})
		}
	}
	for ci, c := range convs {
		if c.Human == 0 {
			res.Excluded++
			continue
		}
		res.Conversations++
		simulateOne(&res, c, cutsOf(c, handoffs[ci]), p)
	}
	byTime := func(a, b time.Time) int { return a.Compare(b) }
	slices.SortStableFunc(res.Resets, func(a, b SimReset) int { return byTime(a.Time, b.Time) })
	slices.SortStableFunc(res.Advices, func(a, b Advice) int { return byTime(a.Time, b.Time) })
	slices.SortStableFunc(res.Unfollowed, func(a, b Advice) int { return byTime(a.Time, b.Time) })
	return res
}

// cutsOf merges the compactions of c and its handoff resumes, in time order.
func cutsOf(c transcript.Conversation, handoffs []cut) []cut {
	cuts := handoffs
	for _, comp := range c.Compactions {
		cuts = append(cuts, cut{kind: KindCompaction, trigger: comp.Trigger, time: comp.Time, preTokens: comp.PreTokens,
			afterTurns: comp.AfterTurns, afterRequests: comp.AfterRequests, afterBlocks: comp.AfterBlocks})
	}
	slices.SortStableFunc(cuts, func(a, b cut) int { return a.time.Compare(b.time) })
	return cuts
}

// countBlocks is the number of blocks of the first turns user turns of c.
func countBlocks(c transcript.Conversation, turns int) int {
	n := 0
	for _, b := range c.Blocks {
		if b.Turn < turns {
			n++
		}
	}
	return n
}

func simulateOne(res *Result, c transcript.Conversation, cuts []cut, p Params) {
	// The floor is the size the conversation started at, which no reset removes.
	floor := 0
	if len(c.Requests) > 0 {
		floor = c.Requests[0].PromptTokens
		f := Floor{Time: c.Requests[0].Time, PromptTokens: floor, Project: c.Project, Cwd: c.Cwd, requests: c.Requests}
		res.Floors = append(res.Floors, f.sumRequests(func(time.Time) bool { return true }))
	}
	var (
		st         advisor.State
		segAdvices []Advice
		segBlock   int       // first block of the current segment
		segReq     int       // first request of the current segment
		prev       int       // prompt size of the previous turn, 0 right after a reset
		next       int       // next cut to apply
		segFrom    time.Time // time of the previous cut
	)
	closeSegment := func(followedBy *cut) {
		if followedBy == nil {
			res.Unfollowed = append(res.Unfollowed, segAdvices...)
		} else {
			res.Resets = append(res.Resets, resetResult(c, *followedBy, segAdvices, segReq, segFrom, floor, p))
			segFrom = followedBy.time
		}
		segAdvices, st, prev = nil, advisor.State{}, 0
	}
	// applyCompactions closes the segment at each cut that comes after turns
	// user turns and requests requests at most.
	applyCompactions := func(turns, requests int) {
		for ; next < len(cuts) && cuts[next].afterTurns <= turns && cuts[next].afterRequests <= requests; next++ {
			ct := cuts[next]
			closeSegment(&ct)
			segBlock, segReq = ct.afterBlocks, ct.afterRequests
		}
	}
	// evaluate runs the advisor on size at turn i, from request req, with the
	// context of the blocks before block end, and reports whether it advised.
	evaluate := func(i, req, size, end int, at time.Time, atStop bool) bool {
		a, ns := advisor.Advise(st, size, floor, p.Advisor)
		st = ns
		if a.Kind != advisor.Reset {
			return false
		}
		context := c.Blocks[min(segBlock, end):min(end, len(c.Blocks))]
		adv := Advice{
			File: c.File, Time: at, Turn: i, Request: req, AtStop: atStop, PromptTokens: size,
			StaleShare: staleShare(context, func(b transcript.Block) int { return i - b.Turn }, p.StaleAfterTurns),
		}
		for _, age := range RequestAges {
			adv.StaleByRequests = append(adv.StaleByRequests,
				staleShare(context, func(b transcript.Block) int { return req - b.Request }, age))
		}
		segAdvices = append(segAdvices, adv)
		res.Advices = append(res.Advices, adv)
		return true
	}

	for i, t := range c.Turns {
		applyCompactions(i, len(c.Requests))
		if advisor.Compacted(prev, t.PromptTokens) {
			// The context shrank without a compaction entry (a /clear, or a format
			// the reader does not know): start over.
			closeSegment(nil)
			segBlock, segReq = t.Block, t.Request
		}
		prev = t.PromptTokens
		advised := t.Advisable && evaluate(i, t.Request, t.PromptTokens, t.Block+1, t.Time, false)

		// The stop hook acts on a typed prompt the prompt hook did not advise at.
		end := len(c.Requests) // the turn's requests are [t.Request, end)
		if i+1 < len(c.Turns) {
			end = c.Turns[i+1].Request
		}
		if !p.StopHook || !t.Human || advised || end-t.Request < 2 {
			continue
		}
		// A compaction inside the turn comes first. The hook reads the request
		// before the last: the transcript does not hold the last one yet.
		applyCompactions(i+1, end-1)
		if last := end - 1; last-1 >= segReq {
			blocks := sort.Search(len(c.Blocks), func(k int) bool { return c.Blocks[k].Request > last })
			evaluate(i, last, c.Requests[last-1].PromptTokens, blocks, c.Requests[last].Time, true)
		}
	}
	applyCompactions(len(c.Turns), len(c.Requests))
	closeSegment(nil)
}

// resetResult says what the advisor would have said before the reset ct, from
// the simulated advices of the stretch since the previous one.
func resetResult(c transcript.Conversation, ct cut, advices []Advice, segReq int, segFrom time.Time, floor int, p Params) SimReset {
	r := SimReset{Kind: ct.kind, Time: ct.time, File: c.File, Trigger: ct.trigger, PreTokens: ct.preTokens}
	for _, a := range c.Advices {
		if a.Time.After(segFrom) && a.Time.Before(ct.time) {
			r.AdviceGiven = true
			break
		}
	}
	if len(advices) == 0 {
		for _, req := range c.Requests[min(segReq, len(c.Requests)):min(ct.afterRequests, len(c.Requests))] {
			if req.PromptTokens-floor >= p.Advisor.AdviseTokens {
				r.CrossedMidTurn = true
				break
			}
		}
		return r
	}
	first := advices[0]
	r.Advised = true
	r.Lead = max(ct.afterTurns-1-first.Turn, 0)
	for _, t := range c.Turns[min(first.Turn+1, len(c.Turns)):max(first.Turn+1, min(ct.afterTurns, len(c.Turns)))] {
		if t.Prompt != "" {
			r.Prompts = append(r.Prompts, t.Prompt)
		}
	}
	// From the advice's request: an advice at a turn's end leaves out its turn.
	for _, req := range c.Requests[first.Request:max(first.Request, min(ct.afterRequests, len(c.Requests)))] {
		if req.PromptTokens > first.PromptTokens {
			r.Beyond += req.PromptTokens - first.PromptTokens
		}
	}
	return r
}

// staleShare is the share of the bytes of blocks that are tool results whose
// age, as measured by age, is over staleAfter.
func staleShare(blocks []transcript.Block, age func(transcript.Block) int, staleAfter int) float64 {
	var stale, total int
	for _, b := range blocks {
		total += b.Bytes
		if b.ToolResult && age(b) > staleAfter {
			stale += b.Bytes
		}
	}
	if total == 0 {
		return 0
	}
	return float64(stale) / float64(total)
}

// Between keeps the compactions and advices that happened in [from, to).
func (r Result) Between(from, to time.Time) Result {
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	out := Result{Conversations: r.Conversations, Excluded: r.Excluded}
	for _, c := range r.Resets {
		if in(c.Time) {
			out.Resets = append(out.Resets, c)
		}
	}
	for _, f := range r.Floors {
		if in(f.Time) {
			out.Floors = append(out.Floors, f.sumRequests(in))
		}
	}
	for _, a := range r.Advices {
		if in(a.Time) {
			out.Advices = append(out.Advices, a)
		}
	}
	for _, a := range r.Unfollowed {
		if in(a.Time) {
			out.Unfollowed = append(out.Unfollowed, a)
		}
	}
	return out
}

// Dist is a distribution summary.
type Dist struct {
	N                            int
	P10, P25, P50, P75, P90, Max float64
}

// Distribution summarizes values with nearest-rank percentiles.
func Distribution(values []float64) Dist {
	d := Dist{N: len(values)}
	if len(values) == 0 {
		return d
	}
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	rank := func(p float64) float64 { return s[int(math.Ceil(p*float64(len(s))))-1] }
	d.P10, d.P25, d.P50, d.P75, d.P90, d.Max = rank(0.1), rank(0.25), rank(0.5), rank(0.75), rank(0.9), s[len(s)-1]
	return d
}
