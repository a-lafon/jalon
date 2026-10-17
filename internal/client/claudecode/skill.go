package claudecode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
	"github.com/a-lafon/jalon/internal/client/skilltext"
)

// skillMarker is the line that tells a skill Jalon wrote from someone else's.
const skillMarker = "<!-- " + skilltext.Marker + " -->"

// words are Claude Code's in the skills' text.
var words = skilltext.Words{
	Prefix:      "/",
	Args:        "$ARGUMENTS",
	Client:      "Claude Code",
	Init:        "jalon init",
	Installs:    "the hooks, the status line or mod, and these commands",
	Transcripts: "transcripts",
	StatusLine:  "- `jalon statusline`: the context size, for the status line.\n",
}

// skillsDir is the folder of the user skills, which apply to every project.
func skillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// skillPath is the file of the user skill name.
func skillPath(name string) (string, error) {
	dir, err := skillsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name, "SKILL.md"), nil
}

// Skills fills {jalon} with exe, quoted for the shell as in the hooks, so
// /jalon-report runs the binary the hooks run. Only the user invokes them.
func (Adapter) Skills(exe string) ([]client.Skill, error) {
	set, err := skilltext.Skills(hookjson.QuoteExe(exe), words)
	if err != nil {
		return nil, err
	}
	var out []client.Skill
	for _, sk := range set {
		path, err := skillPath(sk.Name)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		b.WriteString("---\nname: " + sk.Name + "\ndescription: " + sk.Description + "\n")
		if sk.ArgumentHint != "" {
			b.WriteString("argument-hint: \"" + sk.ArgumentHint + "\"\n")
		}
		b.WriteString("disable-model-invocation: true\n---\n\n" + skillMarker + "\n\n" + sk.Body)
		out = append(out, client.Skill{Path: path, Content: []byte(b.String())})
	}
	return out, nil
}

// RetiredSkills is the single /jalon skill of 0.2.0, whose /jalon resume the
// set replaces.
func (Adapter) RetiredSkills() ([]string, error) {
	path, err := skillPath("jalon")
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (Adapter) OwnsSkill(content []byte) bool {
	return bytes.Contains(content, []byte(skillMarker))
}

// ownedSkill tells whether the user skill name is one Jalon wrote.
func ownedSkill(name string) bool {
	path, err := skillPath(name)
	if err != nil {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && Adapter{}.OwnsSkill(b)
}
