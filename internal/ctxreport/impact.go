package ctxreport

import (
	"slices"
	"time"

	"github.com/a-lafon/jalon/internal/handoff"
	"github.com/a-lafon/jalon/internal/transcript"
)

// The kinds of reset.
const (
	KindCompaction = "compaction"
	KindHandoff    = "handoff"
)

// Reset is a compaction or a linked handoff resume, with the prompt tokens the
// requests after it did not re-read.
type Reset struct {
	Kind string
	Time time.Time
	// File is the conversation that was reset, Into the one that goes on: the
	// same for a compaction.
	File, Into string
	// Path is the handoff file of a handoff resume.
	Path string
	// Before and After are the prompt tokens of the last request before the reset
	// and of the first one after it.
	Before, After int
	// Requests is N: the requests after the reset, up to and excluding the next
	// reset of that line of work or the end of the window.
	Requests int
	// NotReRead is (Before − After) × Requests, 0 when Before ≤ After.
	NotReRead int64
	// PromptTokens and CacheRead sum the prompt tokens and their cache read part
	// over those requests.
	PromptTokens, CacheRead int64
	// Advised is set when an advice preceded it since the previous reset; Lead is
	// the number of user turns started between that advice and the reset.
	Advised bool
	Lead    int
}

// GivenAdvice is an advice the prompt hook gave, with what followed it.
type GivenAdvice struct {
	File string
	Time time.Time
	// FollowedBy is the kind of the reset that followed it, "" when none was seen.
	FollowedBy string
	Lead       int
	// Rendered is set when the turn the advice fired on ended with the
	// recommendation section: when it is not, nobody saw the advice, so no reset
	// is linked to it.
	Rendered bool
	// Version is the Jalon version that gave it, "" when it is not known.
	Version string
}

// Impact is what the advice given and the resets did, in conversations a
// person typed in.
type Impact struct {
	Advices []GivenAdvice
	Resets  []Reset
	// NotLinked are the times of the handoff resumes that no conversation in the
	// window could be linked to.
	NotLinked []time.Time
	// PromptRead is the prompt tokens read by the requests.
	PromptRead int64
	requests   []transcript.Request
}

// reset is a Reset with where it cuts the conversation it resets, and where the
// line of work goes on.
type reset struct {
	Reset
	from, into      int // indexes in the conversations
	cutAt, cutTurns int // requests and turns of from before the reset
	start           int // first request of into after the reset
	sameAs          int // index of an earlier resume of the same handoff from the same conversation, -1 if none
}

// link is a handoff resume linked to the conversation that wrote the handoff.
type link struct {
	from, into int // indexes in the conversations: the parent and the one that resumes
	res        transcript.Resume
	// cutAt and cutTurns are the requests and the user turns of the parent before
	// the resume; before is the prompt tokens of its last request before it.
	cutAt, cutTurns, before int
}

// linkResumes links the handoff resumes of the conversations a person typed in to
// the conversation that wrote the handoff, by parentOf, and cuts that conversation
// at the resume's time. It also returns the times of the resumes that no
// conversation in convs could be linked to. Impacts and Simulate share it.
func linkResumes(convs []transcript.Conversation) (links []link, notLinked []time.Time) {
	for ci, c := range convs {
		if c.Human == 0 || c.Resume == nil {
			continue
		}
		res := *c.Resume
		pi := parentOf(convs, ci)
		if pi < 0 {
			notLinked = append(notLinked, res.Time)
			continue
		}
		p := convs[pi]
		l := link{from: pi, into: ci, res: res,
			cutAt:    countBefore(p.Requests, res.Time, func(r transcript.Request) time.Time { return r.Time }),
			cutTurns: countBefore(p.Turns, res.Time, func(t transcript.Turn) time.Time { return t.Time }),
		}
		if l.cutAt > 0 {
			l.before = p.Requests[l.cutAt-1].PromptTokens
		}
		links = append(links, l)
	}
	return links, notLinked
}

// Impacts links the resets of the conversations to the advice given before them,
// and estimates the prompt tokens they saved.
func Impacts(convs []transcript.Conversation) Impact {
	var im Impact
	var rs []reset
	for ci, c := range convs {
		if c.Human == 0 {
			continue
		}
		im.requests = append(im.requests, c.Requests...)
		for _, comp := range c.Compactions {
			rs = append(rs, reset{
				Reset: Reset{Kind: KindCompaction, Time: comp.Time, File: c.File, Into: c.File, Before: comp.PreTokens},
				from:  ci, into: ci, cutAt: comp.AfterRequests, cutTurns: comp.AfterTurns, start: comp.AfterRequests,
			})
		}
	}
	links, notLinked := linkResumes(convs)
	im.NotLinked = notLinked
	for _, l := range links {
		rs = append(rs, reset{
			Reset: Reset{Kind: KindHandoff, Time: l.res.Time, File: convs[l.from].File, Into: convs[l.into].File, Path: l.res.Path, Before: l.before},
			from:  l.from, into: l.into, cutAt: l.cutAt, start: l.res.Request, cutTurns: l.cutTurns,
		})
	}
	slices.SortStableFunc(rs, func(a, b reset) int { return a.Time.Compare(b.Time) })

	for i := range rs {
		r := &rs[i]
		reqs := convs[r.into].Requests
		end := len(reqs)
		for _, o := range rs[i+1:] {
			if o.from == r.into && o.Time.After(r.Time) && o.cutAt >= r.start && o.cutAt < end {
				end = o.cutAt
			}
		}
		if r.start < end {
			r.Requests = end - r.start
			r.After = reqs[r.start].PromptTokens
			for _, q := range reqs[r.start:end] {
				r.PromptTokens += int64(q.PromptTokens)
				r.CacheRead += int64(q.CacheRead)
			}
		}
		if r.Before > r.After {
			r.NotReRead = int64(r.Before-r.After) * int64(r.Requests)
		}
		r.sameAs = -1
		for j := range rs[:i] {
			if r.Kind == KindHandoff && rs[j].Kind == KindHandoff && rs[j].from == r.from && rs[j].Path == r.Path {
				r.sameAs = j
				break
			}
		}
	}

	for ci, c := range convs {
		if c.Human == 0 {
			continue
		}
		for k, a := range c.Advices {
			g := GivenAdvice{File: c.File, Time: a.Time, Rendered: a.Rendered}
			if !a.Rendered {
				// The user never saw it: whatever reset followed, it did not follow it.
				im.Advices = append(im.Advices, g)
				continue
			}
			for i := range rs {
				r := &rs[i]
				if r.from != ci || r.sameAs >= 0 || !r.Time.After(a.Time) {
					continue
				}
				if k+1 < len(c.Advices) && !r.Time.Before(c.Advices[k+1].Time) {
					break
				}
				g.FollowedBy, g.Lead = r.Kind, max(r.cutTurns-1-a.Turn, 0)
				r.Advised, r.Lead = true, g.Lead
				break
			}
			im.Advices = append(im.Advices, g)
		}
	}
	slices.SortStableFunc(im.Advices, func(a, b GivenAdvice) int { return a.Time.Compare(b.Time) })

	for i, r := range rs {
		if r.sameAs >= 0 {
			r.Advised, r.Lead = rs[r.sameAs].Advised, rs[r.sameAs].Lead
		}
		if r.Kind == KindHandoff && r.Requests == 0 && resumedAgain(rs, i) {
			continue // a try with no request after it: the same reset as the resume that went on
		}
		im.Resets = append(im.Resets, r.Reset)
	}
	for _, q := range im.requests {
		im.PromptRead += int64(q.PromptTokens)
	}
	return im
}

// resumedAgain reports whether another resume of rs[i]'s handoff, from the same
// conversation, has requests after it.
func resumedAgain(rs []reset, i int) bool {
	for k, o := range rs {
		if k != i && o.Kind == KindHandoff && o.from == rs[i].from && o.Path == rs[i].Path && o.Requests > 0 {
			return true
		}
	}
	return false
}

// parentOf is the index of the conversation a resume continues, -1 when none
// is in convs: the one whose main thread last wrote the handoff before the
// resume, otherwise the one the handoff's Session: line names, by file name
// then by the session IDs of its entries.
func parentOf(convs []transcript.Conversation, ci int) int {
	res := convs[ci].Resume
	parent := -1
	var last time.Time
	for pi, p := range convs {
		for _, w := range p.Writes {
			if pi != ci && w.Path == res.Path && w.Time.Before(res.Time) && (parent < 0 || w.Time.After(last)) {
				parent, last = pi, w.Time
			}
		}
	}
	if parent >= 0 {
		return parent
	}
	id := handoff.SessionOf(res.Path)
	if id == "" {
		return -1
	}
	for pi, p := range convs {
		if pi != ci && p.SessionID == id {
			return pi
		}
	}
	for pi, p := range convs {
		if pi != ci && slices.Contains(p.Sessions, id) {
			return pi
		}
	}
	return -1
}

// countBefore is the number of items, in time order, that come before t.
func countBefore[T any](items []T, t time.Time, at func(T) time.Time) int {
	n := 0
	for n < len(items) && at(items[n]).Before(t) {
		n++
	}
	return n
}

// Between keeps the advice, resets and resumes not linked of [from, to), and
// the prompt tokens read by the requests of that period.
func (im Impact) Between(from, to time.Time) Impact {
	in := func(t time.Time) bool { return !t.Before(from) && t.Before(to) }
	out := Impact{}
	for _, a := range im.Advices {
		if in(a.Time) {
			out.Advices = append(out.Advices, a)
		}
	}
	for _, r := range im.Resets {
		if in(r.Time) {
			out.Resets = append(out.Resets, r)
		}
	}
	for _, t := range im.NotLinked {
		if in(t) {
			out.NotLinked = append(out.NotLinked, t)
		}
	}
	for _, q := range im.requests {
		if in(q.Time) {
			out.requests = append(out.requests, q)
			out.PromptRead += int64(q.PromptTokens)
		}
	}
	return out
}
