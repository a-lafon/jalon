package ctxreport

import (
	"time"

	"github.com/a-lafon/jalon/internal/transcript"
)

// MinPause is the pause after which a return can find the cache expired: the
// shortest cache lifetime Claude Code writes.
const MinPause = 5 * time.Minute

// ColdReturn is the first request answering a prompt a person typed after a
// pause, that read less than half of the previous request from the cache.
type ColdReturn struct {
	Time  time.Time
	Pause time.Duration
	// NotRead is its prompt tokens that were not read from the cache.
	NotRead int
	// Grown is set when the previous request was at least adviseTokens above
	// the conversation's first one; AfterHandoff when the conversation wrote a
	// handoff file before it.
	Grown, AfterHandoff bool
}

// Returns are the cold returns of the main conversations.
type Returns struct {
	Cold []ColdReturn
	// PromptRead is the prompt tokens of the main conversations' requests.
	PromptRead int64
	requests   []transcript.Request
}

// ColdReturns finds the cold returns of the conversations. The read share, not
// the length of the pause, tells an expired cache: its lifetime differs between
// users.
func ColdReturns(convs []transcript.Conversation, adviseTokens int) Returns {
	var rs Returns
	for _, c := range convs {
		rs.requests = append(rs.requests, c.Requests...)
		for _, t := range c.Turns {
			i := t.Request
			if !t.Human || i == 0 || i >= len(c.Requests) || compactedBefore(c, i) {
				continue
			}
			prev, q := c.Requests[i-1], c.Requests[i]
			pause := q.Time.Sub(prev.Time)
			if pause <= MinPause || 2*q.CacheRead >= prev.PromptTokens {
				continue
			}
			r := ColdReturn{Time: q.Time, Pause: pause, NotRead: q.PromptTokens - q.CacheRead,
				Grown: prev.PromptTokens-c.Requests[0].PromptTokens >= adviseTokens}
			for _, w := range c.Writes {
				r.AfterHandoff = r.AfterHandoff || w.Time.Before(q.Time)
			}
			rs.Cold = append(rs.Cold, r)
		}
	}
	for _, q := range rs.requests {
		rs.PromptRead += int64(q.PromptTokens)
	}
	return rs
}

// compactedBefore reports whether a compaction lies between request i of c and
// the one before it: it changed the context, so the cache could not hold it.
func compactedBefore(c transcript.Conversation, i int) bool {
	for _, comp := range c.Compactions {
		if comp.AfterRequests == i {
			return true
		}
	}
	return false
}

// NotRead is the prompt tokens the cold returns did not read from the cache.
func (rs Returns) NotRead() int64 {
	var n int64
	for _, r := range rs.Cold {
		n += int64(r.NotRead)
	}
	return n
}

// Between keeps the cold returns of [from, to), and the prompt tokens read by
// the requests of that period.
func (rs Returns) Between(from, to time.Time) Returns {
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	out := Returns{}
	for _, r := range rs.Cold {
		if in(r.Time) {
			out.Cold = append(out.Cold, r)
		}
	}
	for _, q := range rs.requests {
		if in(q.Time) {
			out.requests = append(out.requests, q)
			out.PromptRead += int64(q.PromptTokens)
		}
	}
	return out
}
