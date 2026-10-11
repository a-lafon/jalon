// Package handoff finds the handoff files that a text names by their path. The
// prompt hook and the context report share it, so they agree on what a resume
// looks like.
package handoff

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// folder is what comes between a project root and a handoff's file name.
var folder = string(filepath.Separator) + filepath.Join(".jalon", "handoffs") + string(filepath.Separator)

// dateFolder matches the date folder that may hold a handoff: 2026-10-10/.
var dateFolder = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}` + regexp.QuoteMeta(string(filepath.Separator)))

// Mention is a handoff path found in a text.
type Mention struct {
	// Before is the text up to the handoff folder: it ends with the project root.
	Before string
	// Name is the handoff's path relative to the handoff folder: its file name,
	// after its date folder if it has one.
	Name string
}

// Mentions returns the handoff paths in text, in order: a handoff folder,
// optionally a date folder, and a file name ending in .md.
func Mentions(text string) []Mention {
	var out []Mention
	for at := 0; ; {
		i := strings.Index(text[at:], folder)
		if i < 0 {
			return out
		}
		start := at + i + len(folder)
		rest := text[start:]
		date := len(dateFolder.FindString(rest))
		n := strings.IndexFunc(rest[date:], func(r rune) bool { return !nameRune(r) })
		if n < 0 {
			n = len(rest) - date
		}
		if file := rest[date : date+n]; strings.HasSuffix(file, ".md") && file != ".md" {
			out = append(out, Mention{Before: text[:at+i], Name: rest[:date+n]})
		}
		at = start
	}
}

// nameRune reports whether r may appear in a handoff file name: an ASCII letter
// or digit, '-', '_' or '.'.
func nameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
}

// In reports whether the mention is in the handoff folder of root.
func (m Mention) In(root string) bool { return strings.HasSuffix(m.Before, root) }

// Path is the absolute path of the mention, read back from the text to the
// nearest space, quote, bracket or @; "" when that is not an absolute path.
func (m Mention) Path() string {
	root := m.Before[strings.LastIndexAny(m.Before, " \t\r\n\"'`@(<[")+1:]
	if !filepath.IsAbs(root) {
		return ""
	}
	return root + folder + m.Name
}

// IsPath reports whether p is the absolute path of a handoff file.
func IsPath(p string) bool {
	ms := Mentions(p)
	return len(ms) == 1 && ms[0].Path() == p
}

// sessionLines is how many lines of a handoff are searched for its Session: line.
const sessionLines = 10

// SessionOf is the session ID that the handoff at path names on its Session:
// line, "" when the file cannot be read or has none in its first lines. Nothing
// else is read from the file.
func SessionOf(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < sessionLines && sc.Scan(); i++ {
		if id, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "Session:"); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}
