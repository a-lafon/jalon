// Package skilltext is the text of Jalon's user skills, the /jalon-* commands,
// shared by the clients that take skills. Each client adds its own frontmatter,
// folder and marker line, and fills in the words that differ between clients.
package skilltext

import (
	"embed"
	"strings"
)

//go:embed skills
var bodies embed.FS

// Marker is in every file Jalon writes for a skill, and tells it from someone
// else's.
const Marker = "Written by jalon init, which overwrites edits; jalon init --uninstall removes it."

// Skill is one skill as every client shows it.
type Skill struct {
	Name        string
	Description string
	// ArgumentHint is what the command takes, "" for none.
	ArgumentHint string
	// Body is the instructions, which follow the marker line.
	Body string
}

// Words are what differs between clients in the bodies.
type Words struct {
	// Prefix starts a command, e.g. "/".
	Prefix string
	// Args stands for the command's arguments, e.g. "$ARGUMENTS".
	Args string
	// Client is the client's name, e.g. "Claude Code".
	Client string
	// Init is the command that installs Jalon, and Installs what it installs.
	Init, Installs string
	// Transcripts are what jalon context-report reads.
	Transcripts string
	// StatusLine is the help line of jalon statusline, "" when the client has
	// no status line.
	StatusLine string
	// Report is what /jalon-report adds after its opening line, "" for nothing.
	Report string
}

// skills is the set, in the order jalon init prints them.
var skills = []Skill{
	{Name: "jalon-resume", Description: "Jalon. Resume from this project's latest handoff, or the one named.", ArgumentHint: "[handoff path or words]"},
	{Name: "jalon-recap", Description: "Jalon. Sum up a whole thread of handoffs, from the latest one or the one named back to the first.", ArgumentHint: "[handoff path or words]"},
	{Name: "jalon-handoff", Description: "Jalon. Write a handoff of this conversation now, as Jalon's advice would.", ArgumentHint: "[what to stress]"},
	{Name: "jalon-report", Description: "Jalon. Run jalon context-report on this machine's transcripts and explain what it shows.", ArgumentHint: "[context-report flags, e.g. -since 30d]"},
	{Name: "jalon-help", Description: "Jalon. List Jalon's commands."},
}

// Skills is the set with w filled in, and jalon, the binary /jalon-report runs
// as the shell must read it.
func Skills(jalon string, w Words) ([]Skill, error) {
	r := strings.NewReplacer(
		"{/}", w.Prefix, "{args}", w.Args, "{client}", w.Client, "{init}", w.Init, "{installs}", w.Installs,
		"{transcripts}", w.Transcripts, "{statusline}", w.StatusLine, "{report}", w.Report,
		"{jalon}", jalon)
	out := make([]Skill, len(skills))
	for i, sk := range skills {
		body, err := bodies.ReadFile("skills/" + sk.Name + ".md")
		if err != nil {
			return nil, err
		}
		sk.Body = r.Replace(string(body))
		out[i] = sk
	}
	return out, nil
}
