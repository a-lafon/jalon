package codex

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/a-lafon/jalon/internal/client"
	"github.com/a-lafon/jalon/internal/client/hookjson"
	"github.com/a-lafon/jalon/internal/client/skilltext"
)

var _ client.SkillInstaller = Adapter{}

// words are Codex's in the skills' text. Codex passes a skill no arguments: the
// skill comes with the user's message, which holds them after the command.
var words = skilltext.Words{
	Prefix:      "$",
	Args:        "<arguments>",
	Client:      "Codex",
	Init:        "jalon init --client codex",
	Installs:    "the hooks and these commands",
	Transcripts: "Claude Code transcripts",
	Report:      "\n\nThe report reads Claude Code's transcripts only, not Codex's: say so in one line before your findings.",
}

// policy keeps the model from invoking a skill on its own: Codex then leaves
// it out of the skills it lists to the model, and only $name invokes it.
const policy = "# " + skilltext.Marker + "\npolicy:\n  allow_implicit_invocation: false\n"

// skillsDir is the folder of the user skills Codex documents, ~/.agents/skills,
// whatever CODEX_HOME: Codex reads $CODEX_HOME/skills too, but calls it
// deprecated.
func skillsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// Skills gives each skill two files: agents/openai.yaml, which keeps it to the
// user, then SKILL.md, so uninstall empties agents/ before the skill's folder.
func (Adapter) Skills(exe string) ([]client.Skill, error) {
	dir, err := skillsDir()
	if err != nil {
		return nil, err
	}
	set, err := skilltext.Skills(hookjson.QuoteExe(exe), words)
	if err != nil {
		return nil, err
	}
	var out []client.Skill
	for _, sk := range set {
		header := "---\nname: " + sk.Name + "\ndescription: " + sk.Description + "\n---\n\n<!-- " + skilltext.Marker + " -->\n\n"
		if sk.ArgumentHint != "" {
			header += "The user's message holds `$" + sk.Name + "` and its arguments, " + sk.ArgumentHint + ": <arguments> below stands for the words after `$" + sk.Name + "`, which may be none.\n\n"
		}
		out = append(out,
			client.Skill{Path: filepath.Join(dir, sk.Name, "agents", "openai.yaml"), Content: []byte(policy)},
			client.Skill{Path: filepath.Join(dir, sk.Name, "SKILL.md"), Content: []byte(header + sk.Body)})
	}
	return out, nil
}

// RetiredSkills is empty: no earlier version wrote Codex skills.
func (Adapter) RetiredSkills() ([]string, error) { return nil, nil }

// OwnsSkill finds the marker in a SKILL.md, in an HTML comment, as in an
// openai.yaml, in a YAML comment.
func (Adapter) OwnsSkill(content []byte) bool {
	return bytes.Contains(content, []byte(skilltext.Marker))
}

// ownedSkill tells whether the user skill name is one Jalon wrote.
func ownedSkill(name string) bool {
	dir, err := skillsDir()
	if err != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
	return err == nil && Adapter{}.OwnsSkill(b)
}
