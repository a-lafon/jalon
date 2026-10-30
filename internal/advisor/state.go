package advisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session is what is remembered of one client session between the status line
// and the hooks, which are separate short-lived processes.
type Session struct {
	// Size is the latest context size, in tokens, as measured by the client.
	Size int `json:"size"`
	// Recorded is when Size was last recorded.
	Recorded time.Time `json:"recorded"`
	// LastAdvised is the size at the last advice, 0 when none was given.
	LastAdvised int `json:"last_advised"`
	// Floor is the prompt size of the session's first main-thread request, 0
	// until it is known.
	Floor int `json:"floor,omitempty"`
	// Parent is the handoff this session continues, by its path relative to the
	// handoff folder, "" when none.
	Parent string `json:"parent,omitempty"`
	// Thread is the ID of the handoff thread the session is in, "" when none yet.
	Thread string `json:"thread,omitempty"`
	// Proposed is the handoff name prefix given at the last advice, with its day
	// folder, so a handoff written from it becomes the parent of the next one.
	Proposed string `json:"proposed,omitempty"`
	// Prompt is the ID of the last prompt written by a person that the prompt
	// hook saw, and AdvisedPrompt the ID of the prompt the last advice was given
	// at, "" when the client gives no prompt IDs. The stop hook knows its turn
	// by them.
	Prompt        string `json:"prompt,omitempty"`
	AdvisedPrompt string `json:"advised_prompt,omitempty"`
	// Version is the Jalon version that gave the last advice, "" when the build
	// does not say which it is.
	Version string `json:"version,omitempty"`
	// Typed is true once the transcript has shown a prompt typed by a person in
	// the session: only such a session is advised, not one only a program drove.
	// It is kept, since a long turn can push that prompt out of the part read.
	Typed bool `json:"typed,omitempty"`
}

// SafeSessionID reports whether id can name a state file: letters, digits, "-"
// and "_" only, so it can never reach outside the state directory.
func SafeSessionID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if !idRune(r) {
			return false
		}
	}
	return true
}

// idRune reports whether r may appear in a session ID: an ASCII letter or
// digit, '-' or '_'.
func idRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

func statePath(dir, id string) string { return filepath.Join(dir, id+".json") }

// Load reads the state of a session. It returns false when there is none, it
// cannot be read, or the session ID is not a safe file name.
func Load(dir, id string) (Session, bool) {
	var s Session
	if !SafeSessionID(id) {
		return s, false
	}
	b, err := os.ReadFile(statePath(dir, id))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return Session{}, false
	}
	return s, true
}

// Record stores the latest size of a session, the ID of the prompt of its turn,
// and that the session is typed in by a person once typed is true. When the
// size fell under half of the previous one, as after a compaction, the last
// advised size is reset.
func Record(dir, id string, size int, now time.Time, prompt string, typed bool) error {
	if !SafeSessionID(id) {
		return errors.New("unsafe session id")
	}
	s, _ := Load(dir, id)
	if Compacted(s.Size, size) {
		s.LastAdvised = 0
	}
	s.Size, s.Recorded, s.Prompt = size, now, prompt
	s.Typed = s.Typed || typed
	return write(dir, id, s)
}

// MarkAdvised stores size as the last advised size of a session, the prompt it
// was advised at and version, the build that gives it, keeping the rest of its
// state. The version is the caller's: only the command knows which build it is,
// since the release injects it into the main package.
func MarkAdvised(dir, id string, size int, now time.Time, prompt, version string) error {
	if !SafeSessionID(id) {
		return errors.New("unsafe session id")
	}
	s, _ := Load(dir, id)
	if s.Recorded.IsZero() {
		s.Size, s.Recorded = size, now
	}
	s.LastAdvised, s.AdvisedPrompt, s.Version = size, prompt, version
	return write(dir, id, s)
}

// SetPrompt stores the ID of the prompt of a session's turn, keeping the rest of
// its state: on a turn with no size yet, the size and the last advice stay.
func SetPrompt(dir, id, prompt string) error {
	if !SafeSessionID(id) {
		return errors.New("unsafe session id")
	}
	s, _ := Load(dir, id)
	s.Prompt = prompt
	return write(dir, id, s)
}

// SetFloor stores the floor of a session, keeping the rest of its state.
func SetFloor(dir, id string, floor int) error {
	if !SafeSessionID(id) {
		return errors.New("unsafe session id")
	}
	s, _ := Load(dir, id)
	s.Floor = floor
	return write(dir, id, s)
}

// SetHandoff stores a session's parent handoff, thread and proposed file name
// prefix, keeping the rest of its state.
func SetHandoff(dir, id, parent, thread, proposed string) error {
	if !SafeSessionID(id) {
		return errors.New("unsafe session id")
	}
	s, _ := Load(dir, id)
	s.Parent, s.Thread, s.Proposed = parent, thread, proposed
	return write(dir, id, s)
}

// write replaces the state file through a temporary file in the same folder,
// so a reader never sees a half-written file.
func write(dir, id string, s Session) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*") // created 0600
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), statePath(dir, id))
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// Sweep deletes the state files recorded more than maxAge ago. A file that
// cannot be parsed counts as recorded at its modification time. It also deletes
// the temporary files modified more than maxAge ago, left by a write that did
// not finish; a fresh one may be a write in progress.
func Sweep(dir string, now time.Time, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, name)
		if strings.HasPrefix(name, ".tmp-") {
			if fi, err := e.Info(); err == nil && now.Sub(fi.ModTime()) > maxAge {
				os.Remove(path)
			}
			continue
		}
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		recorded := time.Time{}
		if b, err := os.ReadFile(path); err == nil {
			var s Session
			if json.Unmarshal(b, &s) == nil {
				recorded = s.Recorded
			}
		}
		if recorded.IsZero() {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			recorded = fi.ModTime()
		}
		if now.Sub(recorded) > maxAge {
			os.Remove(path)
		}
	}
}
