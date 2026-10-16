package ctxreport

import (
	"sort"
	"time"

	"github.com/a-lafon/jalon/internal/transcript"
)

// SubagentRun is one subagent, by the sizes of its requests.
type SubagentRun struct {
	// Time is the time of its first request.
	Time time.Time
	Type string
	// Requests is its number of requests; First and Peak the prompt tokens of
	// its first and largest; Read the prompt tokens of all of them.
	Requests, First, Peak int
	Read                  int64
}

// Subagents sums the subagents of a period.
type Subagents struct {
	// Runs are the subagents that started in the period, the most read first.
	Runs []SubagentRun
	// Read is the prompt tokens they read, FirstRead the part of it that was
	// their first requests, PastRead the part read by the Past subagents that
	// went past the advice threshold. MainRead is the prompt tokens of the main
	// conversations' requests in the period.
	Read, FirstRead, PastRead, MainRead int64
	Past                                int
}

// SubagentsBetween sums the subagents whose first request falls in [from, to),
// of every conversation, and the main conversations' prompt tokens read in it.
// A subagent went past the threshold when its largest request is at least
// adviseTokens above its first one, as the advisor counts a conversation.
func SubagentsBetween(convs []transcript.Conversation, adviseTokens int, from, to time.Time) Subagents {
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	var s Subagents
	for _, c := range convs {
		for _, r := range c.Requests {
			if in(r.Time) {
				s.MainRead += int64(r.PromptTokens)
			}
		}
		for _, sub := range c.Subagents {
			if len(sub.Requests) == 0 || !in(sub.Requests[0].Time) {
				continue
			}
			run := SubagentRun{Time: sub.Requests[0].Time, Type: sub.Type, Requests: len(sub.Requests), First: sub.Requests[0].PromptTokens}
			for _, r := range sub.Requests {
				run.Peak = max(run.Peak, r.PromptTokens)
				run.Read += int64(r.PromptTokens)
			}
			s.Runs = append(s.Runs, run)
			s.Read += run.Read
			s.FirstRead += int64(run.First)
			if run.Peak-run.First >= adviseTokens {
				s.Past++
				s.PastRead += run.Read
			}
		}
	}
	sort.SliceStable(s.Runs, func(i, j int) bool { return s.Runs[i].Read > s.Runs[j].Read })
	return s
}
