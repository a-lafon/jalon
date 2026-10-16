//go:build e2e

// Integration tests with the real Claude Code: claude -p runs Jalon's hooks
// against a fake Messages API that reports scripted context sizes, so the
// tests check Jalon's contract with the client (hook input and output, the
// transcript fields it reads) with no model, no key and no network beyond
// localhost. Run with: go test -tags e2e ./cmd/jalon
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/claudecode"
)

// growing is a script whose context grows by 100K tokens a request from 121K,
// every reply ending with a Jalon section.
func growing(n int, fiveMin bool) []step {
	s := make([]step, n)
	for i := range s {
		s[i] = step{size: 121_000 + 100_000*i, fiveMin: fiveMin, section: true}
	}
	return s
}

// holding is how many of reqs contain s.
func holding(reqs []string, s string) int {
	n := 0
	for _, r := range reqs {
		if strings.Contains(r, s) {
			n++
		}
	}
	return n
}

func TestE2EAdviceReachesTheModel(t *testing.T) {
	for name, lifetime := range map[string]string{
		"one-hour cache":    "the prompt cache keeps this context for an hour",
		"five-minute cache": "the prompt cache keeps this context for 5 minutes",
	} {
		t.Run(name, func(t *testing.T) {
			// Given a typed session whose context grows 100K a request from 121K
			c := newClaude(t, true, growing(3, strings.Contains(lifetime, "5 minutes"))...)
			c.run("first")
			if second := c.run("second " + programMark); holding(second, "Jalon:") > 0 {
				t.Error("the second prompt, below the threshold, got Jalon's text")
			}
			// When the third prompt comes 100K above the floor, past advise_tokens
			third := c.run("third")
			// Then its request holds the advice and the cache lifetime, and nothing else follows
			for _, want := range []string{"Jalon: this conversation holds about 221K tokens", lifetime} {
				if holding(third, want) == 0 {
					t.Errorf("no request of the third prompt holds %q", want)
				}
			}
			// The reply ended with the section: the stop hook lets the turn end.
			if holding(third, reminderPrefix) > 0 {
				t.Errorf("the stop hook reminded although the reply had the section")
			}
			if st := c.state(); st["last_advised"] != float64(221_000) || st["proposed"] == nil {
				t.Errorf("state = %v", st)
			}
			for _, p := range []string{".jalon/handoffs", ".jalon/.gitignore"} {
				if _, err := os.Stat(filepath.Join(c.project, p)); err != nil {
					t.Errorf("%s: %v", p, err)
				}
			}
		})
	}
}

func TestE2EProgramSessionIsNeverAdvised(t *testing.T) {
	// Given a session only claude -p drives, past the threshold from its third prompt
	c := newClaude(t, false, growing(3, false)...)
	// When each prompt is sent, then none of its requests holds Jalon's text
	for _, prompt := range []string{"first", "second", "third"} {
		if holding(c.run(prompt), "Jalon:") > 0 {
			t.Errorf("prompt %q got Jalon's text in a session no person typed in", prompt)
		}
	}
}

func TestE2EStopHookRemindsOnce(t *testing.T) {
	// Given a typed session advised at its third prompt, whose reply lacks the section
	c := newClaude(t, true, step{size: 121_000}, step{size: 221_000}, step{size: 222_000}, step{size: 223_000, section: true})
	c.run("first")
	c.run("second " + programMark)
	// When the third turn ends, then the stop hook blocks it once with the reminder
	if n := holding(c.run("third"), reminderPrefix); n != 1 {
		t.Errorf("%d requests of the third prompt hold the reminder, want 1", n)
	}
}

func TestE2EWriteBackNote(t *testing.T) {
	// Given a typed session advised at its third prompt, and the handoff written
	// at the proposed name, as the model would
	c := newClaude(t, true, growing(2, false)...)
	c.run("first")
	c.run("second " + programMark)
	c.run("third")
	proposed, _ := c.state()["proposed"].(string)
	if proposed == "" {
		t.Fatal("no handoff name proposed")
	}
	handoff := filepath.Join(c.project, ".jalon", "handoffs", proposed+"-plan.md")
	if err := os.MkdirAll(filepath.Dir(handoff), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handoff, []byte("Session: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// When the next prompt comes, then its request holds the write-back note
	if holding(c.run("fourth"), "Jalon: the handoff "+handoff+" was written at the last advice") == 0 {
		t.Errorf("the fourth prompt got no write-back note for %s", handoff)
	}
	// When the conversation is compacted, then the note stops: the advice is moot
	c.run("/compact")
	// As a program's prompt, so the stop hook cannot advise it: the note comes
	// from the prompt hook, which acts on it all the same.
	if holding(c.run("fifth "+programMark), "Jalon:") > 0 {
		t.Error("the prompt after the compaction got Jalon's text")
	}
}

func TestE2ETranscriptFieldsJalonReads(t *testing.T) {
	// Given one prompt answered with 121K of context written to the 5-minute cache
	c := newClaude(t, true, step{size: 121_000, fiveMin: true})
	c.run("first")
	// When Jalon's adapter reads the transcript Claude Code wrote, then the fields
	// it needs are there and give that size and lifetime
	raw, err := os.ReadFile(c.transcript())
	if err != nil {
		t.Fatal(err)
	}
	var assistant, prompt bool
	for _, line := range strings.Split(string(raw), "\n") {
		var e struct {
			Type      string           `json:"type"`
			Sidechain *bool            `json:"isSidechain"`
			PromptID  string           `json:"promptId"`
			Message   *json.RawMessage `json:"message"`
		}
		if json.Unmarshal([]byte(line), &e) != nil || e.Message == nil {
			continue
		}
		switch {
		case e.Type == "assistant" && e.Sidechain != nil && strings.Contains(string(*e.Message), `"cache_creation":{`):
			assistant = true
		case e.Type == "user" && e.PromptID != "":
			prompt = true
		}
	}
	if !assistant || !prompt {
		t.Errorf("assistant entry with isSidechain and usage.cache_creation: %v; user entry with promptId: %v", assistant, prompt)
	}
	tail := claudecode.Adapter{}.ReadTail(client.Prompt{Ref: c.transcript()}, "", false)
	if tail.Size != 121_000 || tail.CacheTTL != 5*time.Minute {
		t.Errorf("adapter read size %d, lifetime %v; want 121000, 5m", tail.Size, tail.CacheTTL)
	}
}
