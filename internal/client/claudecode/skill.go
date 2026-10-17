package claudecode

import (
	"bytes"
	"embed"
	"os"
	"path/filepath"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
)

var _ client.SkillInstaller = Adapter{}

// skills are Jalon's user skills, which only the user invokes.
//
//go:embed skills
var skills embed.FS

// skillNames is the set jalon init installs, in the order it prints them.
var skillNames = []string{"jalon-resume", "jalon-recap", "jalon-handoff", "jalon-report", "jalon-help"}

// skillMarker is the line that tells a skill Jalon wrote from someone else's.
const skillMarker = "<!-- Written by jalon init, which overwrites edits; jalon init --uninstall removes it. -->"

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
// /jalon-report runs the binary the hooks run.
func (Adapter) Skills(exe string) ([]client.Skill, error) {
	var out []client.Skill
	for _, name := range skillNames {
		content, err := skills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			return nil, err
		}
		path, err := skillPath(name)
		if err != nil {
			return nil, err
		}
		out = append(out, client.Skill{Path: path, Content: bytes.ReplaceAll(content, []byte("{jalon}"), []byte(hookjson.QuoteExe(exe)))})
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
