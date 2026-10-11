package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// headMax is how much of the start of a transcript the floor is searched in.
const headMax = 8 << 20

// contextFloor returns the size in tokens of the first request of the main
// conversation: input, cache read and cache creation tokens of the first
// assistant entry that is not a sidechain and has usage. It is what the
// conversation starts with (system prompt, tool definitions, listings), which
// no reset can remove. Lines that cannot be parsed are skipped, and at most 8 MB
// are read from the start of the file.
func contextFloor(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, headMax))
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, usageMark) {
			var e sizeEntry
			if json.Unmarshal(line, &e) == nil && e.Type == "assistant" && !e.IsSidechain && e.Message.Usage != nil {
				u := e.Message.Usage
				if n := u.Input + u.CacheRead + u.CacheCreation; n > 0 {
					return n, true
				}
			}
		}
		if err != nil {
			return 0, false
		}
	}
}
